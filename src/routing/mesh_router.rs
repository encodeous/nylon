use std::cmp::max;
use std::collections::hash_map::Entry;
use std::collections::HashMap;
use std::fs;
use std::net::{IpAddr, Ipv4Addr, SocketAddr, SocketAddrV4};
use std::process::exit;
use std::str::FromStr;
use std::sync::Arc;
use std::time::{Duration, Instant};
use anyhow::{anyhow, Context};
use crossbeam_channel::{Receiver, unbounded};
use defguard_wireguard_rs::net::IpAddrMask;
use defguard_wireguard_rs::WireguardInterfaceApi;
use log::{debug, error, info, trace, warn};
use net_route::{Handle, Route};
use serde_json::{json, to_string};
use tokio::io::{duplex, AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, DuplexStream};
use tokio::net::{TcpListener, TcpStream};
use tokio::time::{sleep, timeout, Timeout};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;
use root::concepts::neighbour::Neighbour;
use root::framework::RoutingSystem;
use root::router::{DummyMAC, INF};
use tokio::time::error::Elapsed;
use crate::config::LinkConfig;
use crate::routing::packet::{NetPacket, RoutedPacket};
use crate::routing::packet::NetPacket::{Ping, Pong, TraceRoute};
use crate::routing::routing::NylonSystem;
use crate::routing::state::{LinkHealth, MainLoopEvent, MessageQueue, OperatingState, PersistentState};
use crate::routing::network::{ConnectRequest, InPacket, OutPacket};
use crate::routing::route_table::route_table_updater;
use crate::routing::state::MainLoopEvent::{DispatchPingLink, HandleConnect, InboundPacket, NoEvent, PingResultFailed, RoutePacket, Shutdown, TimerPingUpdate, TimerRouteUpdate, TimerSysRouteUpdate};
use crate::util::channel::{map_channel, map_channel_xb, DuplexChannel};
use crate::util::serialized_io::{read_data, write_data};

// region Link IO

pub fn spawn_link(
    mq: MessageQueue,
    stream: TcpStream,
    link: <NylonSystem as RoutingSystem>::Link,
    upstream: DuplexChannel<OutPacket, InPacket>
) {
    let (mut sr, mut sw) = stream.into_split();
    let (mut sink, mut send) = upstream.split();

    let mq1 = mq.clone();
    tokio::spawn(async move {
        let mut bytes: Vec<u8> = Vec::new();
        while !mq1.cancellation_token.is_cancelled() {
            let packet: Option<OutPacket> = sink.recv().await;

            if let Some(packet) = packet {
                trace!("Writing packet: {} to {}", json!(packet.packet), packet.link);
                let fail = packet.failure_event;

                if let Err(x) = write_data(&mut sw, &mut bytes, &packet.packet).await{
                    if let Some(act) = fail{
                        if let Ok(fail) = Arc::try_unwrap(act) {
                            mq1.main.send(fail).unwrap();
                        }
                    }

                    debug!("Error in writer thread: {x}");
                    break;
                }
            }
            else{
                break;
            }
        }
        sink.close();
        sw.shutdown().await;
    });
    let mq1 = mq.clone();
    tokio::spawn(async move {
        let reader = async {
            let mut bytes: Vec<u8> = Vec::new();
            while !mq1.cancellation_token.is_cancelled() {
                let packet: NetPacket = read_data(&mut sr, &mut bytes).await?;
                trace!("Got packet {} via {link}", json!(packet));
                send.send(InPacket{
                    link: link.clone(),
                    packet,
                }).await?;
            }
            anyhow::Result::<()>::Ok(())
        };

        if let Err(x) = reader.await{
            // ignored
            debug!("Error in reader thread: {x}");
        }
    });
}

async fn link_ctl_listener(mq: MessageQueue, ctl: SocketAddr) -> anyhow::Result<()> {
    info!("Listening on {ctl}");
    let listener = TcpListener::bind(ctl).await?;
    let mut bytes: Vec<u8> = Vec::new();
    while !mq.cancellation_token.is_cancelled(){
        let (mut sock, addr) = listener.accept().await?;
        
        trace!("Inbound connection from {addr}");

        if let Ok(Ok(req)) = timeout(Duration::from_secs(5), read_data(&mut sock, &mut bytes)).await{
            let res: ConnectRequest = req;
            let (us, ds) = DuplexChannel::new(512);
            spawn_link(mq.clone(), sock, res.link_name.clone(), us);

            mq.main.send(
                HandleConnect {
                    duplex: ds,
                    req: res,
                    incoming_addr: addr
                }
            )?;
        }
        else{
            debug!("Rejected connection from {addr}");
        }
    }
    info!("Listener closed");
    Ok(())
}

fn connect_ctl(os: &mut OperatingState, mq: MessageQueue, link: <NylonSystem as RoutingSystem>::Link) -> anyhow::Result<()> {
    let node_id = IpAddrMask::from_str(&os.node_config.addr_vlan)?.ip;
    if let Some(peer) = os.node_config.get_link(&link){
        let pid = peer.id.clone();
        
        let (mut us, ds) = DuplexChannel::new(512);
        
        os.ctl_links.insert(link.clone(), ds.producer);

        map_channel_xb(ds.sink, mq.main.clone(), |pkt: InPacket| InboundPacket {
            link: pkt.link,
            packet: pkt.packet
        }, mq.cancellation_token.clone());
        let addr = peer.addr_ctl;

        tokio::spawn(async move {
            let client = TcpStream::connect(addr).await;
            if let Ok(mut stream) = client {
                write_data(&mut stream, &mut vec![], &ConnectRequest {
                    from: node_id,
                    link_name: pid.clone(),
                }).await.unwrap();
                info!("Connected to peer {} via {}", node_id, pid);
                spawn_link(mq, stream, link, us);
            }
            else{
                us.close();
            }
        });
    }
    Ok(())
}

// endregion

pub fn start_router(ps: PersistentState, os: OperatingState) -> MessageQueue{
    info!("Starting router");
    let (mtx, mrx) = unbounded();
    let ct = CancellationToken::new();
    let mq = MessageQueue{
        main: mtx,
        cancellation_token: ct
    };
    let tmq = mq.clone();
    let addr_ctl = os.node_config.addr_ctl.clone();
    tokio::task::spawn_blocking(||{
        main_loop(ps, os, tmq, mrx).context("Main Thread Failed: ").unwrap();
    });
    tokio::spawn(link_ctl_listener(mq.clone(), addr_ctl));
    let tmq = mq.clone();
    // ping neighbours
    tokio::spawn(async move {
        while !tmq.cancellation_token.is_cancelled(){
            tmq.main.send(TimerPingUpdate).unwrap();
            sleep(Duration::from_secs(15)).await;
        }
    });
    let tmq = mq.clone();
    // broadcast routes
    tokio::spawn(async move {
        while !tmq.cancellation_token.is_cancelled(){
            tmq.main.send(TimerRouteUpdate).unwrap();
            sleep(Duration::from_secs(10)).await;
        }
    });
    let tmq = mq.clone();
    // update system route table
    tokio::spawn(async move {
        while !tmq.cancellation_token.is_cancelled(){
            tmq.main.send(TimerSysRouteUpdate).unwrap();
            sleep(Duration::from_secs(10)).await;
        }
    });
    info!("Router started");
    mq
}

// MAIN THREAD

fn main_loop(
    mut ps: PersistentState,
    mut os: OperatingState,
    mqs: MessageQueue,
    mqr: Receiver<MainLoopEvent>
) -> anyhow::Result<()>{
    while !mqs.cancellation_token.is_cancelled() {
        let event = mqr.recv()?;
        trace!("Main Loop Event: {}", json!(event));
        match event {
            MainLoopEvent::HandleConnect { req, mut duplex, .. } => {
                if os.node_config.get_link(&req.link_name).is_none(){
                    info!("Link {} from {} does not match any peer name, rejecting!", req.link_name, req.from);
                    duplex.close();
                    continue;
                }
                os.ctl_links.insert(req.link_name.clone(), duplex.producer);
                info!("Connected to peer {} via {}", req.from, req.link_name);
                map_channel_xb(duplex.sink, mqs.main.clone(), |pkt| InboundPacket {
                    link: pkt.link,
                    packet: pkt.packet
                }, mqs.cancellation_token.clone());
            }
            MainLoopEvent::InboundPacket { link, packet } => {
                handle_packet(&mut ps, &mut os, mqs.clone(), packet, link)?
            }
            MainLoopEvent::OutboundPacket(packet) => {
                let mut sent = false;
                let link = packet.link.clone();
                let failure = packet.failure_event.clone();
                if let Some(conn) = os.ctl_links.get(&packet.link){
                    sent = conn.try_send(packet).is_ok();
                }
                if !sent {
                    connect_ctl(&mut os, mqs.clone(), link)?;
                    if let Some(act) = failure{
                        if let Ok(fail) = Arc::try_unwrap(act) {
                            mqs.main.send(fail)?;
                        }
                    }
                }
            }
            RoutePacket { to, from, packet } => {
                route_packet(&mut ps, &mut os, mqs.clone(), packet, to, from)?;
            }
            MainLoopEvent::DispatchCommand(cmd) => {
                if let Err(err) = handle_command(&mut ps, &mut os, cmd, mqs.clone()){
                    error!("Error while handling command: {err}");
                }
            }
            MainLoopEvent::TimerSysRouteUpdate => {
                if let Err(e) = route_table_updater(&ps, &mut os){
                    warn!("Error trying to update route table: {e}");
                }

            }
            MainLoopEvent::TimerRouteUpdate => {
                ps.router.full_update();
                write_routing_packets(&mut ps, &mut os, mqs.clone())?;
            }
            MainLoopEvent::TimerPingUpdate => {
                for peer in &os.node_config.links {
                    mqs.main.send(DispatchPingLink {link_id: peer.id.clone()})?;
                }
            }
            Shutdown => {
                mqs.cancellation_token.cancel()
            }
            MainLoopEvent::DispatchPingLink { link_id } => {
                let entry = os.health.entry(link_id.clone()).or_insert(
                    LinkHealth {
                        ping: Duration::MAX,
                        ping_start: Instant::now(),
                        last_ping: Instant::now(),
                    }
                );
                entry.ping_start = Instant::now();

                if let Some(peer) = os.node_config.get_link(&link_id){
                    mqs.send_packet(
                        peer.id.clone(),
                        Ping(link_id.clone()),
                        PingResultFailed {
                            link_id
                        }
                    )?;
                }
            }
            PingResultFailed { link_id } => {
                if let Some(peer) = os.node_config.get_link(&link_id){
                    debug!("Error while pinging {}", peer.id);
                    os.health.entry(link_id.clone()).and_modify(|entry|{
                        entry.ping = Duration::MAX;
                    });
                    update_link_health(&mut ps, link_id, Duration::MAX)?;
                }
            }
            MainLoopEvent::NoEvent => {
                // do nothing
            }
        }

        for warn in ps.router.warnings.drain(..){
            warn!("{warn:?}");
        }
    }

    info!("The router has shutdown, saving state...");

    let content = {
        serde_json::to_vec(&ps)?
    };
    fs::write("./route_table.json", content)?;
    debug!("Saved State");
    
    os.wg_api.remove_interface()?;

    exit(0);
    Ok(())
}

fn handle_packet(
    ps: &mut PersistentState,
    os: &mut OperatingState,
    mq: MessageQueue,
    pkt: NetPacket,
    link: String,
) -> anyhow::Result<()> {
    debug!("Handling packet {} via {link}", json!(pkt));

    let node = &os.node_config;
    if node.get_link(&link).is_none(){
        warn!("Dropped packet {}, {link} not found in config", json!(pkt));
        trace!("Links in config: {}", json!(os.node_config.links));
        return Ok(())
    }
    let link = node.get_link(&link).unwrap();

    match pkt {
        Ping(id) => {
            debug!("Ping received from {} nid: {}", link.id, link.addr_vlan);
            mq.send_packet(
                link.id.clone(),
                Pong(id),
                NoEvent
            )?;
        }
        Pong(id) => {
            debug!("Pong received from {}", link.id);
            if let Some(health) = os.health.get_mut(&id){
                health.last_ping = Instant::now();
                health.ping = (Instant::now() - health.ping_start) / 2;
                update_link_health(ps, id, health.ping)?;
            }
        }
        NetPacket::Routing { link_id, data } => {
            if os.log_routing {
                info!("RP From: {}, {}, via {}", link.addr_vlan, json!(data), link.id);
            }
            ps.router.handle_packet(&DummyMAC::from(data), &link_id, &link.addr_vlan)?;
            ps.router.update();
            write_routing_packets(ps, os, mq)?;
        }
        NetPacket::Deliver { dst_id, sender_id, data } => {
            if dst_id == ps.router.address {
                handle_routed_packet(ps, os, mq, data, sender_id)?;
            } else {
                // do routing
                route_packet(ps, os, mq, data, dst_id, sender_id)?;
            }
        }
        NetPacket::TraceRoute { dst_id, sender_id, mut path } => {
            path.push(ps.router.address.clone());
            if dst_id == ps.router.address {
                mq.main.send(RoutePacket {
                    packet: RoutedPacket::TracedRoute {
                        path
                    },
                    from: ps.router.address.clone(),
                    to: sender_id
                })?;
            } else {
                // do routing
                if let Some(route) = ps.router.routes.get(&dst_id) {
                    if os.log_routing {
                        info!("TRT sender: {}, dst: {}, nh: {}", sender_id, dst_id, route.next_hop);
                    }
                    // forward packet
                    mq.send_packet(
                        link.id.clone(),
                        TraceRoute {
                            dst_id,
                            sender_id,
                            path,
                        },
                        NoEvent
                    )?;
                }
            }
        }
    }

    Ok(())
}

fn write_routing_packets(ps: &mut PersistentState,
                         os: &mut OperatingState,
                         mq: MessageQueue) -> anyhow::Result<()> {
    let node = &os.node_config;
    for pkt in ps.router.outbound_packets.drain(..){
        if let Some(peer) = node.get_link(&pkt.link){
            mq.send_packet(
                peer.id.clone(),
                NetPacket::Routing {
                    link_id: pkt.link,
                    data: pkt.packet.data.clone(),
                },
                NoEvent
            )?;
        }
    }
    Ok(())
}

fn update_link_health(
    ps: &mut PersistentState,
    link: <NylonSystem as RoutingSystem>::Link,
    new_ping: Duration,
) -> anyhow::Result<()> {
    if let Some(neigh) = ps.router.links.get_mut(&link) {
        neigh.metric = {
            if new_ping == Duration::MAX {
                INF
            } else {
                max(new_ping.as_millis() as u16, 1)
            }
        }
    }
    ps.router.update();
    Ok(())
}


fn route_packet(
    ps: &mut PersistentState,
    os: &mut OperatingState,
    mq: MessageQueue,
    data: RoutedPacket,
    dst_id: <NylonSystem as RoutingSystem>::NodeAddress,
    sender_id: <NylonSystem as RoutingSystem>::NodeAddress
) -> anyhow::Result<()> {
    let peers = &os.node_config;
    if dst_id == ps.router.address {
        handle_routed_packet(ps, os, mq, data, sender_id)?;
    } else {
        // do routing
        if let Some(route) = ps.router.routes.get(&dst_id) {
            if os.log_routing {
                info!("DP sender: {}, dst: {}, nh: {}", sender_id, dst_id, route.next_hop);
            }
            // forward packet
            if let Some(peer) = peers.get_link(&route.link) {
                mq.send_packet(
                    peer.id.clone(),
                    NetPacket::Deliver {
                        dst_id,
                        sender_id,
                        data,
                    },
                    NoEvent
                )?;
                return Ok(());
            }
        }
    }
    Ok(())
}

fn handle_routed_packet(
    ps: &mut PersistentState,
    os: &mut OperatingState,
    mq: MessageQueue,
    pkt: RoutedPacket,
    src: <NylonSystem as RoutingSystem>::NodeAddress
) -> anyhow::Result<()> {
    trace!("Handling routed packet from {src}: {}", json!(pkt));
    match pkt {
        RoutedPacket::Ping => {
            mq.main.send(RoutePacket {
                to: src,
                from: ps.router.address.clone(),
                packet: RoutedPacket::Pong
            })?;
        }
        RoutedPacket::Pong => {
            if let Some(start) = os.pings.remove(&src) {
                info!("Pong from {src} {:?}", (Instant::now() - start) / 2);
            }
        }
        RoutedPacket::TracedRoute { path } => {
            info!("Traced route from {src}: {}", path.iter().map(|v|v.to_string()).collect::<Vec<String>>().join(" -> "));
        }
        RoutedPacket::Message(msg) => {
            info!("{src}> {msg}");
        }
        RoutedPacket::Undeliverable { to } => {
            warn!("Undeliverable destination: {to}");
        }
    }
    Ok(())
}

fn handle_command(
    ps: &mut PersistentState,
    os: &mut OperatingState,
    cmd: String,
    mq: MessageQueue,
) -> anyhow::Result<()> {
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
                [routing]
                - route -- prints whole route table
                - ping <node-name> -- pings node
                - msg <node-name> <message> -- sends a message to a node
                - traceroute/tr <node-name> -- traces a route to a node
                [debug]
                - rpkt -- log routing protocol control packets
                - dpkt -- log routing/forwarded packets
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
            mq.main.send(RoutePacket {
                to: node.parse()?,
                from: ps.router.address.clone(),
                packet: RoutedPacket::Ping
            })?;
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
                        TraceRoute {
                            path: vec![],
                            dst_id: node.clone(),
                            sender_id: s_addr,
                        },
                        NoEvent
                    )?;
                }
            }
        }
        "msg" => {
            if split.len() <= 2 {
                return Err(anyhow!("Expected at least two arguments"));
            }
            let node: IpAddr = split[1].to_string().parse()?;
            let msg = split[2..].join(" ");
            mq.main.send(RoutePacket {
                to: node,
                from: ps.router.address.clone(),
                packet: RoutedPacket::Message(msg)
            })?;
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