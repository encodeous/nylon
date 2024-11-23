use std::fmt::Formatter;
use std::net::{SocketAddr};
use std::str::FromStr;
use defguard_wireguard_rs::net::IpAddrMask;
use serde::{de, Deserialize, Deserializer, Serialize, Serializer};
use serde::de::{Error, Visitor};
use serde_with::SerializeDisplay;
use zeroize::Zeroize;
use crate::core::crypto::entity::{Entity, EntitySecret};
use crate::core::crypto::sig::SignedClaim;

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
    pub interface_name: String
}

// Shared Config
#[derive(Serialize, Deserialize, Clone, Eq, Hash, PartialEq, Debug)]
pub struct NodeIdentity {
    /// the friendly name / universal id for the node
    pub id: String,
    /// public key of the node
    pub pubkey: Entity,
    /// public key used for WireGuard
    pub dp_pubkey: String,
}

#[derive(Serialize, Deserialize, Clone)]
pub struct LinkInfo {
    /// address used for control messages, TCP
    pub addr_ctl: SocketAddr,
    /// address used for datagrams, like ping, UDP
    pub addr_dg: SocketAddr,
    /// address used for WireGuard connections, UDP
    pub addr_dp: SocketAddr,
}

// Global/Central Configuration

#[derive(Clone)]
pub struct VLANAddr(pub IpAddrMask);
impl Serialize for VLANAddr {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer
    {
        serializer.serialize_str(&self.0.to_string())
    }
}

struct VLANVisitor;
impl<'de> Visitor<'de> for VLANVisitor {
    type Value = VLANAddr;

    fn expecting(&self, formatter: &mut Formatter) -> std::fmt::Result {
        formatter.write_str("Expected an ip address with a subnet mask such as (192.168.0.8/)")
    }

    fn visit_str<E>(self, v: &str) -> Result<Self::Value, E>
    where
        E: Error
    {
        let result = IpAddrMask::from_str(v).map_err(|e| de::Error::custom(e))?;
        Ok(VLANAddr(result))
    }
}

impl<'de> Deserialize<'de> for VLANAddr {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>
    {
        deserializer.deserialize_str(VLANVisitor)
    }
}

#[derive(Serialize, Deserialize, Clone)]
pub struct NodeInfo {
    pub identity: NodeIdentity,
    pub reachable_via: Vec<LinkInfo>,
    pub addr_vlan: VLANAddr,
}

#[derive(Serialize, Deserialize, Clone)]
pub struct CentralConfig {
    pub version: u128,
    pub config_repos: Vec<String>,
    pub nodes: Vec<NodeInfo>,
    pub root_ca: Entity,
}