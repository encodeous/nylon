use std::time::Duration;
use log::warn;
use serde::{Deserialize, Serialize};
use crate::core::structure::state::{NylonEvent, NylonState};
use tokio::time::{sleep};
use crate::core::control::modules::metric::MetricEvent::PingLink;
use crate::core::control::modules::routing::timed_routing_update;
use crate::core::route_table::timed_sys_route_update;
use crate::core::structure::network::NetworkEvent::EMetric;

#[derive(Serialize, Deserialize, Clone)]
pub enum TimedEvent {
    MetricUpdate,
    RouteUpdate,
    SysRouteUpdate
}

pub fn delayed_event(state: &mut NylonState, event: NylonEvent, interval: Duration){
    let tmq = state.mq.clone();
    state.os.join_set.spawn(async move {
        sleep(interval).await;
        tmq.send(event);
    });
}

pub fn timed_repeating_event(state: &mut NylonState, event: TimedEvent, interval: Duration){
    let tmq = state.mq.clone();
    state.os.join_set.spawn(async move {
        while !tmq.cancellation_token.is_cancelled() {
            tmq.send(NylonEvent::Timer(event.clone()));
            sleep(interval).await;
        }
    });
}

pub fn register_events(state: &mut NylonState) {
    timed_repeating_event(state, TimedEvent::RouteUpdate, Duration::from_secs(10));
    timed_repeating_event(state, TimedEvent::MetricUpdate, Duration::from_secs(10));
    timed_repeating_event(state, TimedEvent::SysRouteUpdate, Duration::from_secs(10));
}

pub fn handle_timed_event(state: &mut NylonState, event: TimedEvent) -> anyhow::Result<()>{
    match event {
        TimedEvent::MetricUpdate => {
            for peer in &state.os.node_config.links {
                state.mq.send(NylonEvent::Network(EMetric(PingLink(peer.id.clone()))));
            }
        }
        TimedEvent::RouteUpdate => timed_routing_update(state)?,
        TimedEvent::SysRouteUpdate => {
            if let Err(e) = timed_sys_route_update(state){
                warn!("Error trying to update route table: {e}");
            }
        }
    }
    Ok(())
}