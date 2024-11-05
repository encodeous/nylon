use std::net::IpAddr;
use uuid::Uuid;
use root::framework::RoutingSystem;
use root::router::NoMACSystem;

pub struct NylonSystem {}
impl RoutingSystem for NylonSystem {
    type NodeAddress = IpAddr;
    type Link = String;
    type MACSystem = NoMACSystem;
}