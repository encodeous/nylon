use std::sync::Arc;
use root::framework::RoutingSystem;
use serde::{Deserialize, Serialize};
use crate::routing::packet::NetPacket;
use crate::routing::routing::NylonSystem;
use crate::routing::state::MainLoopEvent;

#[derive(Serialize, Deserialize)]
pub struct OutPacket {
    pub link: <NylonSystem as RoutingSystem>::Link,
    pub packet: NetPacket,
    #[serde(skip_serializing, skip_deserializing)]
    pub failure_event: Option<Arc<MainLoopEvent>>
}

#[derive(Serialize, Deserialize)]
pub struct InPacket {
    pub link: <NylonSystem as RoutingSystem>::Link,
    pub packet: NetPacket
}

#[derive(Serialize, Deserialize, Clone)]
pub struct ConnectRequest {
    pub from: <NylonSystem as RoutingSystem>::NodeAddress,
    pub link_name: <NylonSystem as RoutingSystem>::Link,
}

#[derive(Serialize, Deserialize)]
pub struct ConnectResponse {
    pub reply_from: <NylonSystem as RoutingSystem>::NodeAddress
}