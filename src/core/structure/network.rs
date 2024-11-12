use std::net::{SocketAddr};
use std::sync::Arc;
use anyhow::bail;
use bitcode::{Decode, Encode};
use root::concepts::packet::Packet;
use root::framework::RoutingSystem;
use serde::{Deserialize, Serialize};
use serde_with::EnumMap;
use tokio::net::TcpStream;
use tokio::sync::oneshot;
use uuid::Uuid;
use crate::config::{LinkInfo, NodeIdentity};
use crate::core::control::modules::courier::{CourierEvent, CourierPacket};
use crate::core::control::modules::metric::{MetricEvent, MetricPacket};
use crate::core::crypto::entity::Entity;
use crate::core::crypto::sig::SignedClaim;
use crate::core::routing::{LinkType, NodeAddrType, NylonSystem};
use crate::core::structure::state::{ActiveLink, NylonEvent};
use crate::util::channel::DuplexChannel;

#[derive(Serialize, Deserialize)]
pub struct OutPacket {
    pub link: <NylonSystem as RoutingSystem>::Link,
    pub packet: CtlPacket,
    #[serde(skip_serializing, skip_deserializing)]
    pub failure_event: Option<Arc<NylonEvent>>
}

#[derive(Serialize, Deserialize)]
pub struct UdpPacket {
    pub addr: SocketAddr,
    pub packet: Datagram
}

#[derive(Serialize, Deserialize)]
pub struct InPacket {
    pub src: Uuid,
    pub packet: CtlPacket
}

#[derive(Serialize, Deserialize, Clone)]
pub struct Connect {
    pub peer_addr: NodeAddrType,
    pub link_id: SignedClaim<LinkType>
}

#[derive(Serialize, Deserialize)]
pub struct ConnectResponse {
    pub reply_from: NodeAddrType,
    pub link_id: SignedClaim<LinkType>
}

#[derive(Serialize, Deserialize, Clone)]
pub enum CtlPacket {
    PCourier(CourierPacket),
    Routing(Packet<NylonSystem>)
}

// UDP/datagram packet
#[derive(Serialize, Deserialize, Clone)]
pub enum Datagram {
    PMetric(MetricPacket)
}

#[derive(Serialize)]
pub enum NetworkEvent {
    ValidateConnect{
        expected_node: Option<Entity>,
        valid_link: Option<LinkType>,
        pkt: Connect,
        #[serde(skip_serializing, skip_deserializing)]
        result: oneshot::Sender<anyhow::Result<NodeIdentity>>
    },
    #[serde(skip_serializing, skip_deserializing)]
    SetupLink {
        id: LinkType,
        addr_dg: Option<SocketAddr>,
        dst: NodeIdentity,
        stream: TcpStream
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
    
    InboundDatagram(UdpPacket),
    OutboundDatagram(UdpPacket),
    
    ECourier(CourierEvent),
    EMetric(MetricEvent),
}