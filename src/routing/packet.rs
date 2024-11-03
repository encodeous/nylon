use serde::{Deserialize, Serialize};
use root::concepts::packet::Packet;
use root::framework::RoutingSystem;
use crate::routing::routing::NylonSystem;

#[derive(Serialize, Deserialize, Clone)]
pub enum NetPacket{
    Ping(<NylonSystem as RoutingSystem>::Link),
    Pong(<NylonSystem as RoutingSystem>::Link),
    Routing {
        link_id: <NylonSystem as RoutingSystem>::Link,
        data: Packet<NylonSystem>
    },
    Deliver{
        dst_id: <NylonSystem as RoutingSystem>::NodeAddress,
        sender_id: <NylonSystem as RoutingSystem>::NodeAddress,
        data: RoutedPacket
    },
    TraceRoute{
        dst_id: <NylonSystem as RoutingSystem>::NodeAddress,
        sender_id: <NylonSystem as RoutingSystem>::NodeAddress,
        path: Vec<<NylonSystem as RoutingSystem>::NodeAddress>
    }
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