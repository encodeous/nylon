use std::net::{SocketAddr};
use std::str::FromStr;
use std::sync::Arc;
use std::time::{Duration, Instant};
use crossbeam_channel::Receiver;
use defguard_wireguard_rs::net::IpAddrMask;
use log::{debug, error, info, trace, warn};
use root::framework::RoutingSystem;
use root::router::DummyMAC;
use serde_json::json;
use tokio::io::AsyncWriteExt;
use tokio::net::{TcpListener, TcpStream, UdpSocket};
use tokio::select;
use tokio::time::timeout;
use crate::core::control::modules::courier::{handle_courier_event, handle_courier_packet};
use crate::core::control::modules::metric::{handle_metric_event, handle_metric_packet, MetricPacket};
use crate::core::control::modules::routing::handle_routing_packet;
use crate::core::routing::NylonSystem;
use crate::core::structure::network::{ConnectRequest, InPacket, UnifiedAddr, NetPacket, NetworkEvent, OutPacket, OutUdpPacket};
use crate::core::structure::network::NetPacket::PCourier;
use crate::core::structure::state::{MessageQueue, NylonEvent, NylonState, OperatingState, PersistentState};
use crate::core::structure::network::NetworkEvent::{HandleConnect, InboundPacket, OutboundPacket, SpawnLink};
use crate::core::structure::state::NylonEvent::{Network, NoEvent};
use crate::util::channel::{map_channel_xb, DuplexChannel};
use crate::util::serialized_io::{read_data, write_data};

// region Link IO

pub fn spawn_link(
    mq: MessageQueue,
    stream: TcpStream,
    link: <NylonSystem as RoutingSystem>::Link,
    upstream: DuplexChannel<OutPacket, InPacket>,
    os: &mut OperatingState
) {
    let (mut sr, mut sw) = stream.into_split();
    let (mut sink, mut send) = upstream.split();

    let mq1 = mq.clone();
    os.join_set.spawn(async move {
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
    os.join_set.spawn(async move {
        let reader = async {
            let mut bytes: Vec<u8> = Vec::new();
            while !mq1.cancellation_token.is_cancelled() {
                let packet: NetPacket = read_data(&mut sr, &mut bytes).await?;
                trace!("Got packet {} via {link}", json!(packet));
                send.send(InPacket{
                    src: UnifiedAddr::Link(link.clone()),
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
            mq.send_network(
                SpawnLink {
                    stream: sock,
                    link: res.link_name.clone(),
                    upstream: us,
                }
            );
            mq.send_network(
                HandleConnect {
                    duplex: ds,
                    req: res,
                    incoming_addr: addr
                }
            );
        }
        else{
            debug!("Rejected connection from {addr}");
        }
    }
    info!("Listener closed");
    Ok(())
}

fn connect_ctl(state: &mut NylonState, link: <NylonSystem as RoutingSystem>::Link) -> anyhow::Result<()> {
    let NylonState{os, mq, ..} = state;
    let mq = mq.clone();
    let node_id = IpAddrMask::from_str(&os.node_config.addr_vlan)?.ip;
    if let Some(peer) = os.node_config.get_link(&link){
        let pid = peer.id.clone();

        let (mut us, ds) = DuplexChannel::new(512);

        os.ctl_links.insert(link.clone(), ds.producer);

        map_channel_xb(ds.sink, mq.main.clone(), |pkt: InPacket| Network(InboundPacket(pkt)), mq.cancellation_token.clone());
        let addr = peer.addr_ctl;

        os.join_set.spawn(async move {
            let client = TcpStream::connect(addr).await;
            if let Ok(mut stream) = client {
                write_data(&mut stream, &mut vec![], &ConnectRequest {
                    from: node_id,
                    link_name: pid.clone(),
                }).await.unwrap();
                info!("Connected to peer {} via {}", node_id, pid);
                mq.send_network(SpawnLink {
                    stream,
                    link,
                    upstream: us,
                });
            }
            else{
                us.close();
            }
        });
    }
    Ok(())
}

// endregion

pub async fn udp_socket(sock: SocketAddr, mq: MessageQueue, mut out: tokio::sync::mpsc::Receiver<OutUdpPacket>) -> anyhow::Result<()> {
    let sock = UdpSocket::bind(sock).await?;
    let mut buf = [0; 512];
    while !mq.cancellation_token.is_cancelled() {
        select! {
            val = sock.recv_from(&mut buf) => {
                if let Ok(val) = val {
                    let (len, src) = val;
                    if let Ok(res) = bitcode::deserialize(&buf[..len]) {
                        mq.main.send(Network(
                            InboundPacket(
                                InPacket{
                                    src: UnifiedAddr::Udp(src),
                                    packet: res,
                                }
                            )
                        ))?;
                    }
                }
            }
            val = out.recv() => {
                if let Some(pkt) = val {
                    let bytes = bitcode::serialize(&pkt.packet)?;
                    if let Err(e) = sock.send_to(bytes.as_slice(), pkt.sock).await{
                        warn!("Failed to send UDP packet: {e}");
                    }
                }
                else{
                    break;
                }
            }
        }
    }
    Ok(())
}

pub fn start_networking(state: &mut NylonState) -> anyhow::Result<()> {
    let NylonState{os, mq, ..} = state;
    let addr_ctl = os.node_config.addr_ctl.clone();
    let tmq = mq.clone();
    os.join_set.spawn(async move {
        link_ctl_listener(tmq, addr_ctl).await.unwrap();
    });
    let sock_addr = os.node_config.addr_dg.clone();
    let tmq = mq.clone();
    let tmq2 = mq.clone();

    let (tx, rx) = tokio::sync::mpsc::channel(1024);
    os.udp_sock = Some(tx);

    os.join_set.spawn(async move {
        if let Err(result) = udp_socket(sock_addr, tmq, rx).await {
            error!("UDP/metric socket died: {result}");
            tmq2.shutdown();
        }
    });
    Ok(())
}

pub fn network_controller(state: &mut NylonState, event: NetworkEvent) -> anyhow::Result<()> {
    let NylonState{os, mq, ..} = state;
    let mq = mq.clone();
    match event {
        HandleConnect { req, mut duplex, .. } => {
            if os.node_config.get_link(&req.link_name).is_none(){
                info!("Link {} from {} does not match any peer name, rejecting!", req.link_name, req.from);
                duplex.close();
                return Ok(());
            }
            os.ctl_links.insert(req.link_name.clone(), duplex.producer);
            info!("Connected to peer {} via {}", req.from, req.link_name);
            map_channel_xb(duplex.sink, mq.main.clone(), |pkt| Network(InboundPacket(pkt)), mq.cancellation_token.clone());
        }
        SpawnLink { stream, link, upstream } => {
            spawn_link(mq.clone(), stream, link, upstream, os);
        }
        InboundPacket(pkt) => {
            handle_packet(state, pkt)?
        }
        OutboundPacket(packet) => {
            let mut sent = false;
            let link = packet.link.clone();
            let failure = packet.failure_event.clone();
            if let Some(conn) = os.ctl_links.get(&packet.link){
                sent = conn.try_send(packet).is_ok();
            }
            if !sent {
                connect_ctl(state, link)?;
                if let Some(act) = failure{
                    if let Ok(fail) = Arc::try_unwrap(act) {
                        mq.main.send(fail)?;
                    }
                }
            }
        }
        NetworkEvent::ECourier(ce) => handle_courier_event(state, ce)?,
        NetworkEvent::EMetric(em) => handle_metric_event(state, em)?,
        NetworkEvent::OutboundUdpPacket(pkt) => {
            if let Some(sock) = &os.udp_sock {
                if let Err(e) = sock.try_send(pkt){
                    warn!("Error writing UDP outbound! {e}")
                }
            }
        }
    }
    Ok(())
}

fn handle_packet(
    state: &mut NylonState,
    pkt: InPacket
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = mq.clone();

    if let UnifiedAddr::Link(link) = &pkt.src {
        debug!("Handling packet {} via {link}", json!(pkt));

        let node = &os.node_config;
        if node.get_link(&link).is_none(){
            warn!("Dropped packet {}, {link} not found in config", json!(pkt));
            trace!("Links in config: {}", json!(os.node_config.links));
            return Ok(())
        }
    }

    match pkt.packet {
        NetPacket::PMetric(mp) => handle_metric_packet(state, mp, pkt.src)?,
        NetPacket::PCourier(cp) => handle_courier_packet(state, cp, pkt.src)?,
        NetPacket::Routing(rt) => handle_routing_packet(state, rt, pkt.src)?
    }

    Ok(())
}

