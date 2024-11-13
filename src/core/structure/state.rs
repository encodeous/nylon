use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::{Duration, Instant};
use anyhow::anyhow;
use chrono::Utc;
use crossbeam_channel::Sender;
use defguard_wireguard_rs::{InterfaceConfiguration, Kernel, WGApi};
use serde::{Deserialize, Serialize};
use root::router::Router;
use root::framework::RoutingSystem;
use crate::core::routing::{LinkType, NodeAddrType, NylonSystem};
use serde_with::serde_as;
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;
use crate::config::{CentralConfig, NodeConfig, NodeIdentity, NodeInfo};
use crate::core::structure::network::{CtlPacket, NetworkEvent, OutPacket, UdpPacket, Datagram};
use crate::core::structure::state::NetworkEvent::OutboundPacket;
use crate::core::structure::state::NylonEvent::{DispatchCommand, Network};
use crate::core::control::timing::TimedEvent;
use crate::core::crypto::entity::Entity;
use crate::core::crypto::sig::{Claim, SignedClaim};
use crate::core::structure::network::NetworkEvent::{OutboundDatagram};

/// Represents a link that has passed validation and is active
pub struct ActiveLink {
    pub id: Uuid,
    /// address used for datagrams, like ping, UDP
    pub addr_dg: Option<SocketAddr>,
    pub ctl: tokio::sync::mpsc::Sender<OutPacket>,
    pub dst: NodeIdentity
}

pub struct NylonState {
    pub ps: PersistentState,
    pub os: OperatingState,
    pub mq: MessageQueue
}

impl NylonState {
    pub fn get_link(&self, id: &LinkType) -> anyhow::Result<&ActiveLink> {
        self.os.links.iter()
            .find(|(_id, link)| link.id == *id)
            .map(|(_id, link)| link)
            .ok_or(anyhow!("Unable to find link matching id {id}"))
    }
    pub fn get_node_by_name(&self, friendly_name: &String) -> Option<&NodeInfo> {
        self.os.central_config.nodes.iter().find(|x| {
            x.id.friendly_name == *friendly_name
        })
    }
    pub fn get_node_by_pubkey(&self, pubkey: &NodeAddrType) -> Option<&NodeInfo> {
        self.os.central_config.nodes.iter().find(|x| {
            x.id.pubkey == *pubkey
        })
    }
    pub fn node_info(&mut self) -> NodeInfo {
        let pubkey = self.pubkey().clone();
        self.os.central_config.nodes.iter().find(|x| x.id.pubkey == pubkey).unwrap().clone()
    }
    pub fn pubkey(&mut self) -> &Entity {
        self.os.cached_state.pubkey.get_or_insert_with(|| {
            self.os.node_config.node_secret.node_privkey.get_pubkey()
        })
    }
    pub fn sign_claim<T: Clone + Serialize + 'static>(&self, claim: Claim<T>) -> SignedClaim<T> {
        claim.sign_claim(&self.os.node_config.node_secret.node_privkey).unwrap()
    }
    /// Signs a piece of data that expires in 10 seconds
    pub fn sign_ephemeral<T: Clone + Serialize + 'static>(&self, data: T) -> SignedClaim<T> {
        self.sign_claim(Claim::from_now_until(data, Utc::now() + Duration::from_secs(5)))
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
    // Link and Health
    pub health: HashMap<LinkType, LinkHealth>,
    pub pings: HashMap<NodeAddrType, Instant>,
    pub links: HashMap<LinkType, ActiveLink>,
    
    // Network IO
    pub udp_sock: Option<tokio::sync::mpsc::Sender<UdpPacket>>,
    pub itf_config: InterfaceConfiguration,
    #[cfg(not(target_os = "macos"))]
    pub wg_api: WGApi::<Kernel>,
    #[cfg(target_os = "macos")]
    pub wg_api: WGApi::<Userspace>,
    pub join_set: JoinSet<()>,
    pub prev_itf_config: String,
    
    // Core Config
    pub node_config: NodeConfig,
    pub central_config: CentralConfig,
    
    // Core State
    pub cached_state: CachedState,
}

pub struct CachedState {
    pub pubkey: Option<Entity>
}

#[derive(Clone)]
pub struct MessageQueue{
    pub main: Sender<NylonEvent>,
    pub cancellation_token: CancellationToken
}

impl MessageQueue{
    pub fn send_packet(&self, to: <NylonSystem as RoutingSystem>::Link, packet: CtlPacket, failure: NylonEvent) {
        self.send_network(
            OutboundPacket(OutPacket{
                link: to,
                packet,
                failure_event: Some(Arc::new(failure)),
            })
        );
    }
    pub fn send_probe_packet(&self, to: SocketAddr, packet: Datagram) {
        self.send_network(
            OutboundDatagram(UdpPacket{
                addr: to,
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