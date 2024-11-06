use std::cmp::max;
use std::net::SocketAddr;
use crate::core::structure::state::{LinkHealth, PersistentState};
use std::time::{Duration, Instant};
use bitcode::{Decode, Encode};
use log::debug;
use root::concepts::packet::Packet;
use root::framework::RoutingSystem;
use root::router::INF;
use serde::{Deserialize, Serialize};
use crate::core::control::modules::courier::RoutedPacket;
use crate::core::control::modules::metric::MetricEvent::{PingCheck, PingLink};
use crate::core::control::modules::metric::MetricPacket::Pong;
use crate::core::control::timing::delayed_event;
use crate::core::routing::NylonSystem;
use crate::core::structure::network::{UnifiedAddr, NetworkEvent};
use crate::core::structure::network::NetPacket::PMetric;
use crate::core::structure::network::NetworkEvent::EMetric;
use crate::core::structure::state::NylonEvent::{Network, NoEvent};
use crate::core::structure::state::NylonState;

#[derive(Serialize, Deserialize, Clone)]
#[derive(Encode, Decode, PartialEq, Debug)]
pub enum MetricPacket {
    Ping(<NylonSystem as RoutingSystem>::Link, u8),
    Pong(<NylonSystem as RoutingSystem>::Link, u8),
}

#[derive(Serialize, Deserialize, Clone)]
pub enum MetricEvent {
    PingLink(<NylonSystem as RoutingSystem>::Link),
    PingCheck{link: <NylonSystem as RoutingSystem>::Link, seq: u64}
}

pub fn handle_metric_packet(
    state: &mut NylonState,
    pkt: MetricPacket,
    src: UnifiedAddr
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = state.mq.clone();
    
    match pkt {
        MetricPacket::Ping(id, num) => {
            debug!("Ping received with id: {}", id);
            mq.send_udp_packet(
                src.as_udp()?,
                PMetric(Pong(id.clone(), num + 1))
            );
            if num == 0 {
                // we can also try to ping back via this address, in case they are behind a firewall or NAT
                ping_link(src.as_udp()?, state, id, num + 1);
            }
        }
        Pong(id, num) => {
            debug!("Pong received from {}", id);
            if let Some(health) = os.health.get_mut(&id){
                health.last_ping = Instant::now();
                health.ping = (Instant::now() - health.ping_start) / 2;
                health.ping_seq += 1;
                let ping = health.ping;
                update_link_health(state, id, ping)?;
            }
        }
    }
    Ok(())
}

fn ping_link(addr: SocketAddr, state: &mut NylonState, link: <NylonSystem as RoutingSystem>::Link, seq: u8) {
    let NylonState { ps, os, mq, .. } = state;
    let entry = os.health.entry(link.clone()).or_insert(
        LinkHealth {
            ping: Duration::MAX,
            ping_start: Instant::now(),
            last_ping: Instant::now(),
            ping_seq: 0
        }
    );
    entry.ping_start = Instant::now();
    mq.send_udp_packet(
        addr,
        PMetric(MetricPacket::Ping(link.clone(), seq))
    );
}
    
pub fn handle_metric_event(
    state: &mut NylonState,
    event: MetricEvent,
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = mq.clone();
    match event {
        MetricEvent::PingLink(link) => {
            if let Some(peer) = os.node_config.get_link(&link){
                ping_link(peer.addr_dg, state, link, 0);
            }
        }
        MetricEvent::PingCheck{link, seq} => {
            if let Some(peer) = os.node_config.get_link(&link){
                let mut success = true;
                os.health.entry(link.clone()).and_modify(|entry|{
                    if entry.ping_seq == seq{
                        entry.ping = Duration::MAX;
                        success = false;
                    }
                });
                if !success {
                    debug!("Timed out while pinging {}", peer.id);
                    update_link_health(state, link, Duration::MAX)?;
                }
            }
        }
    }
    Ok(())
}

fn update_link_health(
    state: &mut NylonState,
    link: <NylonSystem as RoutingSystem>::Link,
    new_ping: Duration,
) -> anyhow::Result<()> {
    let ps = &mut state.ps;
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