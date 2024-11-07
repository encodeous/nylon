use std::collections::HashMap;
use std::net::{IpAddr, Ipv4Addr, SocketAddr};
use aws_lc_rs::signature::EcdsaKeyPair;
use defguard_wireguard_rs::net::IpAddrMask;
use serde::{Deserialize, Serialize};
use crate::core::crypto::entity::{Entity, EntitySecret};

#[derive(Serialize, Deserialize)]
pub struct NodeSecret {
    // used for wireguard
    pub wg_priv_key: String,
    // used for control messages
    pub node_priv_key: EntitySecret,
}


#[derive(Serialize, Deserialize, Clone)]
pub struct NodeLink {
    /// address used for WireGuard connections, UDP
    pub addr_dp: SocketAddr,
    /// address used for control messages, TCP
    pub addr_ctl: SocketAddr,
    /// address used for datagrams, like ping, UDP
    pub addr_dg: SocketAddr
}

#[derive(Serialize, Deserialize, Clone)]
pub struct NodeConfig {
    pub wg_pub_key: String,
    pub addr_vlan: String,
    pub static_links: Vec<NodeLink>,
}

impl NodeConfig {
    pub fn get_link(&self, id: &String) -> Option<&LinkConfig> {
        self.links.iter().find(|p| p.id == *id)
    }
}

#[derive(Serialize, Deserialize, Clone)]
pub struct LinkConfig {
    pub id: String,
    pub public_key: String,
    pub addr_dp: SocketAddr,
    pub addr_ctl: SocketAddr,
    pub addr_dg: SocketAddr,
    pub addr_vlan: IpAddr,
}

#[derive(Serialize, Deserialize, Clone)]
pub struct CentralConfig {
    pub version: u128,
    pub config_repos: Vec<String>,
    pub trusted_nodes: Vec<Entity>,
    pub nodes: Vec<NodeConfig>,
    pub root_ca: Entity,
}