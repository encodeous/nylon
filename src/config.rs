use std::collections::HashMap;
use std::net::{IpAddr, Ipv4Addr, SocketAddr};
use defguard_wireguard_rs::net::IpAddrMask;
use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize)]
pub struct NodeConfig {
    pub addr_vlan: String,
    // used for wireguard
    pub private_key: String,
    pub addr_dp: SocketAddr,
    pub addr_ctl: SocketAddr,
    pub links: Vec<LinkConfig>,
    pub network_key: String
}

impl NodeConfig {
    pub fn get_link(&self, id: &String) -> Option<&LinkConfig> {
        self.links.iter().find(|p| p.id == *id)
    }
}

#[derive(Serialize, Deserialize)]
pub struct LinkConfig {
    pub id: String,
    pub public_key: String,
    pub addr_dp: SocketAddr,
    pub addr_ctl: SocketAddr,
    pub addr_vlan: IpAddr,
}