use std::net::IpAddr;
use uuid::Uuid;
use root::framework::RoutingSystem;
use root::router::NoMACSystem;
use crate::config::NodeIdentity;
use crate::core::crypto::entity::Entity;

pub type LinkType = Uuid;
pub type NodeAddrType = Entity;

pub struct NylonSystem {}
impl RoutingSystem for NylonSystem {
    type NodeAddress = NodeAddrType;
    type Link = LinkType;
    type MACSystem = NoMACSystem;
}