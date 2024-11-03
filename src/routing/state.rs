use std::collections::HashMap;
use std::net::{IpAddr, Ipv4Addr, SocketAddr};
use std::sync::Arc;
use std::time::{Duration, Instant};
use crossbeam_channel::Sender;
use defguard_wireguard_rs::{InterfaceConfiguration, Kernel, WGApi};
use root::concepts::packet::Packet;
use serde::{Deserialize, Serialize};
use root::router::Router;
use root::framework::RoutingSystem;
use crate::routing::routing::NylonSystem;
use serde_with::serde_as;
use tokio_util::sync::CancellationToken;
use crate::config::NodeConfig;
use crate::routing::network::{ConnectRequest, InPacket, OutPacket};
use crate::routing::packet::{NetPacket, RoutedPacket};
use crate::routing::state::MainLoopEvent::OutboundPacket;
use crate::util::channel::DuplexChannel;

#[serde_as]
#[derive(Serialize, Deserialize)]
pub struct PersistentState {
    pub router: Router<NylonSystem>
}

pub struct LinkHealth{
    pub last_ping: Instant,
    pub ping: Duration,
    pub ping_start: Instant,
}

pub struct OperatingState {
    pub health: HashMap<<NylonSystem as RoutingSystem>::Link, LinkHealth>,
    pub pings: HashMap<<NylonSystem as RoutingSystem>::NodeAddress, Instant>,
    pub ctl_links: HashMap<<NylonSystem as RoutingSystem>::Link, tokio::sync::mpsc::Sender<OutPacket>>,
    pub log_routing: bool,
    pub log_delivery: bool,
    pub node_config: NodeConfig,
    pub itf_config: InterfaceConfiguration,
    #[cfg(not(target_os = "macos"))]
    pub wg_api: WGApi::<Kernel>,
    #[cfg(target_os = "macos")]
    pub wg_api: WGApi::<Userspace>,
    pub prev_itf_config: String
}

#[derive(Clone)]
pub struct MessageQueue{
    pub main: Sender<MainLoopEvent>,
    pub cancellation_token: CancellationToken
}

impl MessageQueue{
    pub fn send_packet(&self, to: <NylonSystem as RoutingSystem>::Link, packet: NetPacket, failure: MainLoopEvent) -> anyhow::Result<()> {
        self.main.send(OutboundPacket(
            OutPacket{
                link: to,
                packet,
                failure_event: Some(Arc::new(failure)),
            }
        ))?;
        Ok(())
    }
}


#[derive(Serialize)]
pub enum MainLoopEvent{
    HandleConnect{
        req: ConnectRequest,
        #[serde(skip_serializing, skip_deserializing)]
        duplex: DuplexChannel<InPacket, OutPacket>,
        incoming_addr: SocketAddr,
    },
    InboundPacket{
        link: <NylonSystem as RoutingSystem>::Link,
        packet: NetPacket
    },
    OutboundPacket(OutPacket),
    RoutePacket{
        to: <NylonSystem as RoutingSystem>::NodeAddress,
        from: <NylonSystem as RoutingSystem>::NodeAddress,
        packet: RoutedPacket
    },
    DispatchPingLink{
        link_id: <NylonSystem as RoutingSystem>::Link
    },
    PingResultFailed{
        link_id: <NylonSystem as RoutingSystem>::Link
    },
    DispatchCommand(String),
    TimerRouteUpdate,
    TimerSysRouteUpdate,
    TimerPingUpdate,
    Shutdown,
    NoEvent
}