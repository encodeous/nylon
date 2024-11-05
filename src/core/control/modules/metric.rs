use std::cmp::max;
use crate::core::structure::state::{LinkHealth, PersistentState};
use std::time::{Duration, Instant};
use log::debug;
use root::concepts::packet::Packet;
use root::framework::RoutingSystem;
use root::router::INF;
use serde::{Deserialize, Serialize};
use crate::core::control::modules::courier::RoutedPacket;
use crate::core::control::modules::metric::MetricPacket::Pong;
use crate::core::routing::NylonSystem;
use crate::core::structure::network::{UnifiedAddr, NetworkEvent};
use crate::core::structure::network::NetPacket::PMetric;
use crate::core::structure::network::NetworkEvent::EMetric;
use crate::core::structure::state::NylonEvent::{Network, NoEvent};
use crate::core::structure::state::NylonState;

#[derive(Serialize, Deserialize, Clone)]
pub enum MetricPacket {
    Ping(<NylonSystem as RoutingSystem>::Link),
    Pong(<NylonSystem as RoutingSystem>::Link),
}

#[derive(Serialize, Deserialize, Clone)]
pub enum MetricEvent {
    PingLink(<NylonSystem as RoutingSystem>::Link),
    PingFailed(<NylonSystem as RoutingSystem>::Link),
}

pub fn handle_metric_packet(
    state: &mut NylonState,
    pkt: MetricPacket,
    src: UnifiedAddr
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = state.mq.clone();
    
    match pkt {
        MetricPacket::Ping(id) => {
            debug!("Ping received with id: {}", id);
            mq.send_packet(
                id.clone(),
                PMetric(Pong(id)),
                NoEvent
            );
        }
        Pong(id) => {
            debug!("Pong received from {}", id);
            if let Some(health) = os.health.get_mut(&id){
                health.last_ping = Instant::now();
                health.ping = (Instant::now() - health.ping_start) / 2;
                let ping = health.ping;
                update_link_health(state, id, ping)?;
            }
        }
    }
    Ok(())
}

pub fn handle_metric_event(
    state: &mut NylonState,
    event: MetricEvent,
) -> anyhow::Result<()> {
    let NylonState{ps, os, mq, ..} = state;
    let mq = mq.clone();
    match event {
        MetricEvent::PingLink(link) => {
            let entry = os.health.entry(link.clone()).or_insert(
                LinkHealth {
                    ping: Duration::MAX,
                    ping_start: Instant::now(),
                    last_ping: Instant::now(),
                }
            );
            entry.ping_start = Instant::now();

            if let Some(peer) = os.node_config.get_link(&link){
                mq.send_packet(
                    peer.id.clone(),
                    PMetric(MetricPacket::Ping(link.clone())),
                    Network(EMetric(MetricEvent::PingFailed(link.clone())))
                );
            }
        }
        MetricEvent::PingFailed(link) => {
            if let Some(peer) = os.node_config.get_link(&link){
                debug!("Error while pinging {}", peer.id);
                os.health.entry(link.clone()).and_modify(|entry|{
                    entry.ping = Duration::MAX;
                });
                update_link_health(state, link, Duration::MAX)?;
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