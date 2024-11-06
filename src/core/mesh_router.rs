use crate::config::LinkConfig;
use crate::core::control::modules::courier::CourierPacket::TraceRoute;
use crate::core::control::modules::courier::{route_packet, RoutedPacket};
use crate::core::control::network::{network_controller, start_networking};
use crate::core::control::timing::{handle_timed_event, register_events};
use crate::core::structure::network::NetPacket::PCourier;
use crate::core::structure::network::{ConnectRequest, InPacket, OutPacket};
use crate::core::structure::state::NylonEvent::{NoEvent, Shutdown};
use crate::core::structure::state::{LinkHealth, MessageQueue, NylonEvent, NylonState, OperatingState, PersistentState};
use anyhow::{anyhow, Context};
use crossbeam_channel::{unbounded, Receiver};
use defguard_wireguard_rs::WireguardInterfaceApi;
use log::{debug, error, info, trace, warn};
use root::router::{DummyMAC, INF};
use serde_json::{json, to_string};
use std::cmp::max;
use std::collections::hash_map::Entry;
use std::collections::HashMap;
use std::fs;
use std::net::{IpAddr, Ipv4Addr, SocketAddr, SocketAddrV4};
use std::process::exit;
use std::str::FromStr;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::io::{duplex, AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, DuplexStream};
use tokio::net::{TcpListener, TcpStream};
use tokio::time::error::Elapsed;
use tokio::time::{sleep, timeout, Timeout};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

#[cfg(target_os = "linux")]
pub fn setup_iptables() -> bool{
    let ipt = iptables::new(false);
    if !ipt.is_ok() { return false; }
    let ipt = ipt.unwrap();
    let success = ipt.append_replace("filter", "INPUT", "-i nylon -j ACCEPT").is_ok();
    success && ipt.append_replace("filter", "FORWARD", "-i nylon -o nylon -j ACCEPT").is_ok()
}

#[cfg(target_os = "linux")]
pub fn cleanup_iptables(){
    let ipt = iptables::new(false).unwrap();
    ipt.delete("filter", "INPUT", "-i nylon -j ACCEPT").unwrap();
    ipt.delete("filter", "FORWARD", "-i nylon -o nylon -j ACCEPT").unwrap();
}

pub fn start_router(ps: PersistentState, mut os: OperatingState) -> anyhow::Result<MessageQueue>{
    info!("Starting router");
    let (mtx, mrx) = unbounded();
    let ct = CancellationToken::new();
    let mq = MessageQueue{
        main: mtx,
        cancellation_token: ct
    };
    let tmq = mq.clone();
    let mut state = NylonState{
        ps, os, mq
    };
    register_events(&mut state);
    start_networking(&mut state)?;

    let tmq = state.mq.clone();
    tokio::task::spawn_blocking(||{
        main_loop(state, mrx).context("Main Thread Failed: ").unwrap();
    });
    info!("Router started");
    Ok(tmq)
}

// MAIN THREAD

fn main_loop(
    mut state: NylonState,
    mqr: Receiver<NylonEvent>
) -> anyhow::Result<()>{

    #[cfg(target_os = "linux")]
    let iptable_setup = setup_iptables();
    #[cfg(target_os = "linux")]
    if !iptable_setup {
        warn!("Failed to automatically set iptable rules");
    }
    
    let mq = state.mq.clone();
    
    let mut main_iter = ||-> anyhow::Result<()> {
        let event = mqr.recv()?;
        trace!("Main Loop Event: {}", json!(event));

        match event {
            NylonEvent::DispatchCommand(cmd) => {
                if let Err(err) = handle_command(&mut state, cmd, mq.clone()){
                    error!("Error while handling command: {err}");
                }
            }
            Shutdown => mq.cancellation_token.cancel(),
            NylonEvent::Timer(timer) => handle_timed_event(&mut state, timer)?,
            NylonEvent::Network(network) => network_controller(&mut state, network)?,
            NylonEvent::NoEvent => {
                // do nothing
            }
        }

        for warn in state.ps.router.warnings.drain(..){
            warn!("{warn:?}");
        }
        Ok(())
    };

    while !mq.cancellation_token.is_cancelled() {
        if let Err(e) = main_iter(){
            error!("Error occurred in main thread: {e:?}");
        }
    }

    let NylonState{ps, os, ..} = &mut state;

    os.join_set.abort_all();
    
    info!("The router has shutdown, saving state...");

    let content = {
        serde_json::to_vec(&ps)?
    };
    fs::write("./route_table.json", content)?;
    debug!("Saved State");
    
    os.wg_api.remove_interface()?;

    #[cfg(target_os = "linux")]
    if iptable_setup {
        cleanup_iptables();
    }
    
    exit(0);
    
    Ok(())
}






fn handle_command(
    state: &mut NylonState,
    cmd: String,
    mq: MessageQueue,
) -> anyhow::Result<()> {
    let NylonState{os, mq, ps} = state;
    let mq = mq.clone();
    let split: Vec<&str> = cmd.split_whitespace().collect();
    if split.is_empty() {
        return Ok(());
    }
    let peers = &os.node_config;
    match split[0] {
        "help" => {
            info!(r#"Help:
                - help -- shows this page
                - exit -- exits and saves state
                [direct link]
                - ls -- lists all direct links
                [core]
                - route -- prints whole route table
                - ping <node-name> -- pings node
                - msg <node-name> <message> -- sends a message to a node
                - traceroute/tr <node-name> -- traces a route to a node
                [debug]
                - rpkt -- log core protocol control packets
                - dpkt -- log core/forwarded packets
                "#);
        }
        "route" => {
            let mut rtable = vec![];
            info!("Route Table:");
            rtable.push(String::new());
            rtable.push(format!("Self: {}, seq: {}", ps.router.address, ps.router.seqno));
            for (addr, route) in &ps.router.routes {
                rtable.push(
                    format!("{addr} - via: {}, nh: {}, c: {}, seq: {}, fd: {}, ret: {}",
                            route.link,
                            route.next_hop.clone(),
                            route.metric,
                            route.source.data.seqno,
                            route.fd,
                            route.retracted
                    ))
            }
            info!("{}", rtable.join("\n"));
        }
        "rpkt" => {
            os.log_routing = !os.log_routing;
        }
        "dpkt" => {
            os.log_delivery = !os.log_delivery;
        }
        "exit" => {
            mq.main.send(Shutdown)?;
        }
        "ping" => {
            if split.len() != 2 {
                return Err(anyhow!("Expected one argument"));
            }
            let node = split[1];
            os.pings.insert(node.parse()?, Instant::now());
            let addr = ps.router.address.clone();
            route_packet(state, RoutedPacket::Ping, node.parse()?, addr)?;
        }
        "traceroute" | "tr" => {
            if split.len() != 2 {
                return Err(anyhow!("Expected one argument"));
            }
            let node: IpAddr = split[1].to_string().parse()?;
            if let Some(nh) = ps.router.routes.get(&node) {
                if let Some(peer) = peers.get_link(&nh.link) {
                    let s_addr = ps.router.address.clone();
                    mq.send_packet(
                        peer.id.clone(),
                        PCourier(
                            TraceRoute {
                                path: vec![],
                                dst_id: node.clone(),
                                sender_id: s_addr,
                            }
                        ),
                        NoEvent
                    );
                }
            }
        }
        "msg" => {
            if split.len() <= 2 {
                return Err(anyhow!("Expected at least two arguments"));
            }
            let node: IpAddr = split[1].to_string().parse()?;
            let msg = split[2..].join(" ");
            let addr = ps.router.address.clone();
            route_packet(state, RoutedPacket::Message(msg), node, addr)?;
        }
        "ls" => {
            for LinkConfig { id, addr_ctl, addr_vlan, .. } in &peers.links {
                if let Some(health) = os.health.get(id) {
                    info!("id: {id}, ctl: {}, vlan: {}, ping: {:?}", addr_ctl, addr_vlan, health.ping)
                } else {
                    info!("id: {id}, ctl: {} UNCONNECTED", addr_ctl)
                }
            }
        }
        &_ => {
            error!("Unknown command, please try again or type \"help\" for help.")
        }
    }
    Ok(())
}