use std::net::SocketAddr;
use std::sync::Arc;
use anyhow::bail;
use root::concepts::packet::Packet;
use root::framework::RoutingSystem;
use serde::{Deserialize, Serialize};
use tokio::net::TcpStream;
use crate::core::control::modules::courier::{CourierEvent, CourierPacket};
use crate::core::control::modules::metric::{MetricEvent, MetricPacket};
use crate::core::routing::NylonSystem;
use crate::core::structure::state::NylonEvent;
use crate::util::channel::DuplexChannel;

#[derive(Serialize, Deserialize)]
pub struct OutPacket {
    pub link: <NylonSystem as RoutingSystem>::Link,
    pub packet: NetPacket,
    #[serde(skip_serializing, skip_deserializing)]
    pub failure_event: Option<Arc<NylonEvent>>
}

#[derive(Serialize, Deserialize)]
pub enum UnifiedAddr {
    Link(<NylonSystem as RoutingSystem>::Link),
    Udp(SocketAddr)
}

impl UnifiedAddr {
    pub fn as_link(&self) -> anyhow::Result<&<NylonSystem as RoutingSystem>::Link>{
        match self {
            UnifiedAddr::Link(v) => Ok(v),
            UnifiedAddr::Udp(_) => bail!("The inbound packet was not from a link!")
        }
    }
    pub fn as_udp(&self) -> anyhow::Result<SocketAddr>{
        match self {
            UnifiedAddr::Link(_) => bail!("The inbound packet was not from a UDP socket!"),
            UnifiedAddr::Udp(v) => Ok(v.clone())
        }
    }
}

#[derive(Serialize, Deserialize)]
pub struct InPacket {
    pub src: UnifiedAddr,
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

#[derive(Serialize, Deserialize, Clone)]
pub enum NetPacket{
    PMetric(MetricPacket),
    PCourier(CourierPacket),
    Routing(Packet<NylonSystem>),
}

#[derive(Serialize)]
pub enum NetworkEvent {
    HandleConnect{
        req: ConnectRequest,
        #[serde(skip_serializing, skip_deserializing)]
        duplex: DuplexChannel<InPacket, OutPacket>,
        incoming_addr: SocketAddr,
    },
    SpawnLink{
        #[serde(skip_serializing, skip_deserializing)]
        stream: TcpStream,
        #[serde(skip_serializing, skip_deserializing)]
        upstream: DuplexChannel<OutPacket, InPacket>,
        link: <NylonSystem as RoutingSystem>::Link,
    },
    InboundPacket(InPacket),
    OutboundPacket(OutPacket),
    ECourier(CourierEvent),
    EMetric(MetricEvent)
}