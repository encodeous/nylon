use std::time::Instant;
use log::{info, trace, warn};
use root::concepts::packet::Packet;
use root::framework::RoutingSystem;
use root::router::DummyMAC;
use serde::{Deserialize, Serialize};
use serde_json::json;
use crate::core::control::modules::courier::CourierEvent::RoutePacket;
use crate::core::control::modules::courier::CourierPacket::{Deliver, TraceRoute};
use crate::core::routing::NylonSystem;
use crate::core::structure::network::{UnifiedAddr, CtlPacket, NetworkEvent};
use crate::core::structure::network::CtlPacket::PCourier;
use crate::core::structure::state::NylonEvent::{NoEvent};
use crate::core::structure::state::{NylonState, OperatingState, PersistentState};

#[derive(Serialize, Deserialize, Clone)]
pub enum CourierPacket {
    Deliver{
        dst_id: <NylonSystem as RoutingSystem>::NodeAddress,
        sender_id: <NylonSystem as RoutingSystem>::NodeAddress,
        data: RoutedPacket
    },
    TraceRoute{
        dst_id: <NylonSystem as RoutingSystem>::NodeAddress,
        sender_id: <NylonSystem as RoutingSystem>::NodeAddress,
        path: Vec<<NylonSystem as RoutingSystem>::NodeAddress>
    },
}

#[derive(Serialize, Deserialize, Clone)]
pub enum CourierEvent{
    RoutePacket{
        to: <NylonSystem as RoutingSystem>::NodeAddress,
        from: <NylonSystem as RoutingSystem>::NodeAddress,
        packet: RoutedPacket
    },
}

#[derive(Serialize, Deserialize, Clone)]
pub enum RoutedPacket{
    Ping,
    Pong,
    TracedRoute{
        path: Vec<<NylonSystem as RoutingSystem>::NodeAddress>
    },
    Message(String),
    Undeliverable{
        to: <NylonSystem as RoutingSystem>::NodeAddress
    }
}

pub fn handle_courier_event(
    state: &mut NylonState,
    event: CourierEvent
) -> anyhow::Result<()> {
    match event {
        RoutePacket{to, from, packet} => {
            route_packet(state, packet, to, from)?;
        }
    }
    Ok(())
}

pub fn handle_courier_packet(
    state: &mut NylonState,
    packet: CourierPacket,
    src: UnifiedAddr
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = mq.clone();
    let link = os.node_config.get_link(src.as_link()?).unwrap();
    match packet {
        Deliver { dst_id, sender_id, data } => {
            if dst_id == ps.router.address {
                handle_routed_packet(state, data, sender_id)?;
            } else {
                // do core
                route_packet(state, data, dst_id, sender_id)?;
            }
        }
        TraceRoute { dst_id, sender_id, mut path } => {
            path.push(ps.router.address.clone());
            if dst_id == ps.router.address {
                mq.send_network(NetworkEvent::ECourier(
                    RoutePacket {
                        packet: RoutedPacket::TracedRoute {
                            path
                        },
                        from: ps.router.address.clone(),
                        to: sender_id
                    }
                ));
            } else {
                // do core
                if let Some(route) = ps.router.routes.get(&dst_id) {
                    if os.log_routing {
                        info!("TRT sender: {}, dst: {}, nh: {}", sender_id, dst_id, route.next_hop);
                    }
                    // forward packet
                    mq.send_packet(
                        link.id.clone(),
                        PCourier(
                            TraceRoute {
                                dst_id,
                                sender_id,
                                path,
                            }
                        ),
                        NoEvent
                    );
                }
            }
        }
    }
    Ok(())
}

fn handle_routed_packet(
    state: &mut NylonState,
    pkt: RoutedPacket,
    src: <NylonSystem as RoutingSystem>::NodeAddress
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = mq.clone();
    trace!("Handling routed packet from {src}: {}", json!(pkt));
    match pkt {
        RoutedPacket::Ping => {
            mq.send_network(NetworkEvent::ECourier(
                RoutePacket {
                    to: src,
                    from: ps.router.address.clone(),
                    packet: RoutedPacket::Pong
                }
            ));
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

pub fn route_packet(
    state: &mut NylonState,
    data: RoutedPacket,
    dst_id: <NylonSystem as RoutingSystem>::NodeAddress,
    sender_id: <NylonSystem as RoutingSystem>::NodeAddress
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let peers = &os.node_config;
    if dst_id == ps.router.address {
        handle_routed_packet(state, data, sender_id)?;
    } else {
        // do core
        if let Some(route) = ps.router.routes.get(&dst_id) {
            if os.log_routing {
                info!("DP sender: {}, dst: {}, nh: {}", sender_id, dst_id, route.next_hop);
            }
            // forward packet
            if let Some(peer) = peers.get_link(&route.link) {
                mq.send_packet(
                    peer.id.clone(),
                    PCourier(
                        Deliver {
                            dst_id,
                            sender_id,
                            data,
                        }
                    ),
                    NoEvent
                );
                return Ok(());
            }
        }
    }
    Ok(())
}