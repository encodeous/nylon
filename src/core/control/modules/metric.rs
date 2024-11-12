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
use crate::core::routing::{LinkType, NylonSystem};
use crate::core::structure::network::{NetworkEvent};
use crate::core::structure::network::NetworkEvent::EMetric;
use crate::core::structure::network::Datagram::PMetric;
use crate::core::structure::state::NylonEvent::{Network, NoEvent};
use crate::core::structure::state::NylonState;

#[derive(Serialize, Deserialize, Clone)]
pub enum MetricPacket {
    Ping(LinkType, bool),
    Pong(LinkType),
}

#[derive(Serialize, Deserialize, Clone)]
pub enum MetricEvent {
    PingLink(<NylonSystem as RoutingSystem>::Link),
    PingCheck{link: <NylonSystem as RoutingSystem>::Link, seq: u64}
}

pub fn handle_metric_packet(
    state: &mut NylonState,
    pkt: MetricPacket,
    src: SocketAddr
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = state.mq.clone();
    
    match pkt {
        MetricPacket::Ping(id, ret_ping) => {
            debug!("Ping received with id: {}", id);
            if state.get_link(&id).is_ok(){
                mq.send_probe_packet(
                    src,
                    PMetric(Pong(id.clone()))
                );
                if !ret_ping {
                    ping_link(src, state, id, true);
                }
            }
        }
        Pong(id) => {
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

fn ping_link(addr: SocketAddr, state: &mut NylonState, link: LinkType, is_return: bool) {
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
    mq.send_probe_packet(
        addr,
        PMetric(MetricPacket::Ping(link.clone(), is_return))
    );
}
    
pub fn handle_metric_event(
    state: &mut NylonState,
    event: MetricEvent,
) -> anyhow::Result<()> {
    match event {
        MetricEvent::PingLink(link) => {
            if let Ok(peer) = state.get_link(&link){
                ping_link(peer.info.addr_dg, state, link, false);
            }
        }
        MetricEvent::PingCheck{link, seq} => {
            if let Ok(peer) = state.get_link(&link){
                let id = peer.id.clone();
                let mut success = true;
                state.os.health.entry(link.clone()).and_modify(|entry|{
                    if entry.ping_seq == seq{
                        entry.ping = Duration::MAX;
                        success = false;
                    }
                });
                if !success {
                    debug!("Timed out while pinging {}", id);
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