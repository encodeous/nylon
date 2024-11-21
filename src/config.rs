use std::net::{SocketAddr};
use serde::{Deserialize, Serialize};
use zeroize::Zeroize;
use crate::core::crypto::entity::{Entity, EntitySecret};
// Node Specific States
#[derive(Serialize, Deserialize, Clone, Zeroize)]
pub struct NodeSecret {
    // used for wireguard
    pub wg_privkey: String,
    // used for control messages
    pub node_privkey: EntitySecret,
}

#[derive(Serialize, Deserialize, Clone)]
pub struct NodeConfig {
    pub node_secret: NodeSecret,
    pub node_sock: LinkInfo,
    pub saved_links: Vec<LinkInfo>,
    pub interface_name: String
}

// Shared Config
#[derive(Serialize, Deserialize, Clone, Eq, Hash, PartialEq, Debug)]
pub struct NodeIdentity {
    pub friendly_name: String,
    pub pubkey: Entity
}

#[derive(Serialize, Deserialize, Clone)]
pub struct LinkInfo {
    /// the destination node's friendly name
    pub friendly_name: String,
    /// address used for control messages, TCP
    pub addr_ctl: SocketAddr,
    /// address used for datagrams, like ping, UDP
    pub addr_dg: SocketAddr,
    /// address used for WireGuard connections, UDP
    pub addr_dp: SocketAddr,
    /// public key used for WireGuard
    pub dp_pubkey: String,
}

// Global/Central Configuration

#[derive(Serialize, Deserialize, Clone)]
pub struct NodeInfo {
    pub id: NodeIdentity,
    pub reachable_via: Vec<LinkInfo>,
    pub addr_vlan: String,
}

#[derive(Serialize, Deserialize, Clone)]
pub struct CentralConfig {
    pub version: u128,
    pub config_repos: Vec<String>,
    pub nodes: Vec<NodeInfo>,
    pub root_ca: Entity,
}