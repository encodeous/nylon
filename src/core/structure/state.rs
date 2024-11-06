use std::collections::HashMap;
use std::net::{IpAddr, Ipv4Addr, SocketAddr};
use std::sync::Arc;
use std::time::{Duration, Instant};
use anyhow::{anyhow, bail};
use crossbeam_channel::Sender;
use defguard_wireguard_rs::{InterfaceConfiguration, Kernel, WGApi};
use root::concepts::packet::Packet;
use serde::{Deserialize, Serialize};
use root::router::Router;
use root::framework::RoutingSystem;
use serde_json::json;
use crate::core::routing::NylonSystem;
use serde_with::serde_as;
use tokio::net::TcpStream;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;
use crate::config::{LinkConfig, NodeConfig};
use crate::core::control::modules::courier::CourierPacket;
use crate::core::control::modules::metric::MetricEvent;
use crate::core::structure::network::{ConnectRequest, InPacket, NetPacket, NetworkEvent, OutPacket, OutUdpPacket, UnifiedAddr};
use crate::core::structure::state::NetworkEvent::OutboundPacket;
use crate::core::structure::state::NylonEvent::{DispatchCommand, Network};
use crate::core::control::timing::TimedEvent;
use crate::core::structure::network::NetworkEvent::OutboundUdpPacket;
use crate::util::channel::DuplexChannel;

pub struct NylonState {
    pub ps: PersistentState,
    pub os: OperatingState,
    pub mq: MessageQueue
}

impl NylonState {
    pub fn get_link(&self, id: &String) -> anyhow::Result<&LinkConfig> {
        self.os.node_config.links.iter().find(|p| p.id == *id).ok_or(anyhow!("Unable to find link matching id {id}"))
    }
    pub fn get_link_uni(&mut self, link: &UnifiedAddr) -> anyhow::Result<&LinkConfig> {
        if let UnifiedAddr::Link(link) = &link {
            Ok(self.get_link(link)?)
        }
        else {
            bail!("Unable to find the link matching the specified address {}", json!(link));
        }
    }
}

#[serde_as]
#[derive(Serialize, Deserialize)]
pub struct PersistentState {
    pub router: Router<NylonSystem>
}

pub struct LinkHealth{
    pub last_ping: Instant,
    pub ping: Duration,
    pub ping_start: Instant,
    pub ping_seq: u64
}

pub struct OperatingState {
    pub health: HashMap<<NylonSystem as RoutingSystem>::Link, LinkHealth>,
    pub pings: HashMap<<NylonSystem as RoutingSystem>::NodeAddress, Instant>,
    pub ctl_links: HashMap<<NylonSystem as RoutingSystem>::Link, tokio::sync::mpsc::Sender<OutPacket>>,
    pub udp_sock: Option<tokio::sync::mpsc::Sender<OutUdpPacket>>,
    pub log_routing: bool,
    pub log_delivery: bool,
    pub node_config: NodeConfig,
    pub itf_config: InterfaceConfiguration,
    #[cfg(not(target_os = "macos"))]
    pub wg_api: WGApi::<Kernel>,
    #[cfg(target_os = "macos")]
    pub wg_api: WGApi::<Userspace>,
    pub prev_itf_config: String,
    pub join_set: JoinSet<()>
}

#[derive(Clone)]
pub struct MessageQueue{
    pub main: Sender<NylonEvent>,
    pub cancellation_token: CancellationToken
}

impl MessageQueue{
    pub fn send_packet(&self, to: <NylonSystem as RoutingSystem>::Link, packet: NetPacket, failure: NylonEvent) {
        self.send_network(
            OutboundPacket(OutPacket{
                link: to,
                packet,
                failure_event: Some(Arc::new(failure)),
            })
        );
    }
    pub fn send_udp_packet(&self, to: SocketAddr, packet: NetPacket) {
        self.send_network(
            OutboundUdpPacket(OutUdpPacket{
                sock: to,
                packet,
            })
        );
    }
    pub fn send_network(&self, event: NetworkEvent){
        self.main.send(Network(event)).unwrap()
    }
    pub fn send(&self, event: NylonEvent){
        self.main.send(event).unwrap()
    }
    /// Shuts down nylon
    pub fn shutdown(&self) {
        self.cancellation_token.cancel();
        self.main.send(DispatchCommand(String::new())).unwrap();
    }
}


#[derive(Serialize)]
pub enum NylonEvent {
    DispatchCommand(String),
    Network(NetworkEvent),
    Timer(TimedEvent),
    Shutdown,
    NoEvent
}