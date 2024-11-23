use std::net::{SocketAddr};
use std::sync::Arc;
use std::time::Duration;
use anyhow::{anyhow, bail, Context};
use chrono::Utc;
use log::{debug, error, info, trace, warn};
use serde_json::json;
use tokio::io::AsyncWriteExt;
use tokio::net::{TcpListener, TcpStream, UdpSocket};
use tokio::select;
use tokio::sync::oneshot;
use uuid::Uuid;
use crate::config::LinkInfo;
use crate::core::control::modules::courier::{handle_courier_event, handle_courier_packet};
use crate::core::control::modules::metric::{handle_metric_event, handle_metric_packet};
use crate::core::control::modules::routing::handle_routing_packet;
use crate::core::crypto::entity::EntitySecret;
use crate::core::crypto::sig::Claim;
use crate::core::routing::LinkType;
use crate::core::structure::network::{InPacket, CtlPacket, NetworkEvent, OutPacket, UdpPacket, Datagram, Connect};
use crate::core::structure::state::{ActiveLink, MessageQueue, NylonState, OperatingState};
use crate::core::structure::network::NetworkEvent::{InboundPacket, InboundDatagram, OutboundPacket, SetupLink, SpawnLink, ValidateConnect};
use crate::core::structure::state::NylonEvent::Network;
use crate::util::channel::{map_channel_xb, DuplexChannel};
use crate::util::serialized_io::{read_data, read_data_timeout, write_data};

// region Link IO

pub fn spawn_link(
    mq: MessageQueue,
    stream: TcpStream,
    link: LinkType,
    upstream: DuplexChannel<OutPacket, InPacket>,
    os: &mut OperatingState
) {
    let (mut sr, mut sw) = stream.into_split();
    let (mut sink, send) = upstream.split();

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
                let packet: CtlPacket = read_data(&mut sr, &mut bytes).await?;
                trace!("Got packet {} via {link}", json!(packet));
                send.send(InPacket{
                    src: link,
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

async fn link_ctl_listener(mq: MessageQueue, ctl: SocketAddr, priv_key: EntitySecret) -> anyhow::Result<()> {
    info!("Listening on {ctl}");
    let listener = TcpListener::bind(ctl).await?;
    let bytes: Vec<u8> = Vec::new();

    let pubkey = priv_key.get_pubkey();

    while !mq.cancellation_token.is_cancelled(){
        let (mut stream, addr) = listener.accept().await?;

        let tmq = mq.clone();
        let priv_key = priv_key.clone();
        let pubkey = pubkey.clone();
        
        tokio::spawn(async move {
            trace!("Inbound connection from {addr}");
            let critical = async {
                let request: Connect = read_data_timeout(&mut stream, &mut vec![], Duration::from_secs(5)).await.context("Failed to get response")?;

                // validate response
                let (compl, wait) = oneshot::channel();
                tmq.send_network(ValidateConnect {
                    expected_node: None,
                    valid_link: None,
                    pkt: request.clone(),
                    result: compl
                });
                let identity = wait.await?;
                
                if let Err(e) = identity {
                    return Err(e);
                }

                let claim = Claim::from_now_until(request.link_id.claim.data, Utc::now() + Duration::from_secs(5));
                let signed = claim.sign_claim(&priv_key)?;

                write_data(&mut stream, &mut vec![], &Connect {
                    peer_addr: pubkey.clone(),
                    link_id: signed,
                }).await.context("Failed to send handshake init")?;

                tmq.send_network(SetupLink {
                    id: request.link_id.claim.data,
                    addr_dg: None,
                    dst: identity?.clone(),
                    stream
                });

                anyhow::Result::Ok(())
            };
            if let Err(x) = critical.await {
                trace!("In link_ctl_listener: {x}")
            }
        });
    }
    info!("Listener closed");
    Ok(())
}

fn connect_ctl(state: &mut NylonState, node: String, link: LinkInfo) -> anyhow::Result<()> {
    let mq = state.mq.clone();

    let expected_pubkey = state.get_node_by_name(&node).ok_or(anyhow!("Specified friendly name not found in nodes"))?.identity.pubkey.clone();
    
    let privkey = state.os.node_config.node_secret.node_privkey.clone();
    let pubkey = state.node_info().identity.pubkey.clone();

    state.os.join_set.spawn(async move {
        let client = TcpStream::connect(link.addr_ctl).await;
        if let Ok(mut stream) = client {
            let critical = async {
                let id = Uuid::new_v4();

                let claim = Claim::from_now_until(id, Utc::now() + Duration::from_secs(5));
                let signed = claim.sign_claim(&privkey)?;
                
                write_data(&mut stream, &mut vec![], &Connect {
                    peer_addr: pubkey,
                    link_id: signed
                }).await.context("Failed to send handshake init")?;

                let resp: Connect = read_data_timeout(&mut stream, &mut vec![], Duration::from_secs(5)).await.context("Failed to get response")?;

                // validate response
                let (compl, wait) = oneshot::channel();
                mq.send_network(ValidateConnect {
                    expected_node: Some(expected_pubkey),
                    valid_link: Some(id),
                    pkt: resp,
                    result: compl
                });
                let identity = wait.await?;

                if let Err(e) = identity {
                    return Err(e);
                }
                let identity = identity.unwrap();

                info!("Connected to peer {} via {}", identity.id, id);

                mq.send_network(SetupLink {
                    id,
                    addr_dg: Some(link.addr_dg),
                    dst: identity.clone(),
                    stream
                });
                anyhow::Result::Ok(())
            };

            if let Err(x) = critical.await {
                trace!("In connect_ctl: {x}")
            }
        }
    });
    Ok(())
}

// endregion

pub async fn udp_socket(sock: SocketAddr, mq: MessageQueue, mut out: tokio::sync::mpsc::Receiver<UdpPacket>) -> anyhow::Result<()> {
    let sock = UdpSocket::bind(sock).await?;
    let mut buf = [0; 1200];
    while !mq.cancellation_token.is_cancelled() {
        select! {
            val = sock.recv_from(&mut buf) => {
                if let Ok(val) = val {
                    let (len, src) = val;
                    if let Ok(res) = bitcode::deserialize(&buf[..len]) {
                        mq.main.send(Network(InboundDatagram(UdpPacket{
                            addr: src,
                            packet: res
                        })))?
                    }
                }
            }
            val = out.recv() => {
                if let Some(pkt) = val {
                    let bytes = bitcode::serialize(&pkt.packet)?;
                    if let Err(e) = sock.send_to(bytes.as_slice(), pkt.addr).await{
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
    let addr_ctl = os.node_config.node_sock.addr_ctl.clone();
    let tmq = mq.clone();
    let priv_key = os.node_config.node_secret.node_privkey.clone();
    os.join_set.spawn(async move {
        link_ctl_listener(tmq, addr_ctl, priv_key).await.unwrap();
    });
    let sock_addr = os.node_config.node_sock.addr_dg.clone();
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
        ValidateConnect { expected_node, valid_link, pkt, result } => {
            let critical = {
                if let Some(link) = valid_link{
                    if link != pkt.link_id.claim.data {
                        bail!("Link ID does not match");
                    }
                }
                if let Some(node) = expected_node {
                    if pkt.peer_addr != node {
                        bail!("Peer node is not the expected node")
                    }
                }
                let res = state.get_node_by_pubkey(&pkt.peer_addr);
                if let None = res {
                    bail!("Destination peer {} is not trusted", hex::encode(&pkt.peer_addr.pub_key));
                }
                pkt.link_id.validate(&pkt.peer_addr)?;
                if state.get_link(&pkt.link_id.claim.data).is_ok() {
                    bail!("A link with the same ID is already active!");
                }
                Ok(res.unwrap().identity.clone())
            };
            result.send(critical).unwrap();
        }
        SetupLink { id, dst, addr_dg, stream } => {
            let (us, ds) = DuplexChannel::new(512);
            mq.send_network(
                SpawnLink {
                    stream,
                    link: id,
                    upstream: us,
                }
            );

            let name = dst.id.clone();

            let active_link = ActiveLink{
                id,
                addr_dg,
                ctl: ds.producer,
                dst,
            };

            debug!("Connected to peer {} via {}", name, id);

            os.links.insert(id, active_link);
            map_channel_xb(ds.sink, mq.main.clone(), |pkt| Network(InboundPacket(pkt)), mq.cancellation_token.clone());
        }
        SpawnLink { stream, link, upstream } => {
            spawn_link(mq.clone(), stream, link, upstream, os);
        }
        InboundPacket(pkt) => {
            handle_packet(state, pkt)?
        }
        OutboundPacket(packet) => {
            let mut sent = false;
            let link = packet.link;
            let failure = packet.failure_event.clone();
            if let Some(conn) = os.links.get(&packet.link){
                sent = conn.ctl.try_send(packet).is_ok();
            }
            if !sent {
                trace!("Failed to send packet via {link}");
                if let Some(act) = failure{
                    if let Ok(fail) = Arc::try_unwrap(act) {
                        mq.main.send(fail)?;
                    }
                }
            }
        }
        NetworkEvent::ECourier(ce) => handle_courier_event(state, ce)?,
        NetworkEvent::EMetric(em) => handle_metric_event(state, em)?,
        NetworkEvent::InboundDatagram(pkt) => {
            match pkt.packet{
                Datagram::PMetric(hpkt) => handle_metric_packet(state, hpkt, pkt.addr)?
            }
        }
        NetworkEvent::OutboundDatagram(pkt) => {
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
    match pkt.packet {
        CtlPacket::PCourier(cp) => handle_courier_packet(state, cp, pkt.src)?,
        CtlPacket::Routing(rt) => handle_routing_packet(state, rt, pkt.src)?
    }
    Ok(())
}

