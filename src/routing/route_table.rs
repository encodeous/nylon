use std::collections::{HashMap, HashSet};
use std::net::IpAddr;
use defguard_wireguard_rs::{Kernel, WGApi, WireguardInterfaceApi};
use defguard_wireguard_rs::net::IpAddrMask;
use log::{trace, warn};
use net_route::{Handle, Route};
use serde_json::json;
use crate::routing::routing::NylonSystem;
use crate::routing::state::{OperatingState, PersistentState};

pub fn route_table_updater(ps: &PersistentState, os: &mut OperatingState) -> anyhow::Result<()>{
    
    let mut active_links = HashSet::new();
    for (vlan, route) in &ps.router.routes{
        active_links.insert(route.link.clone());
    }
    
    for peer in &mut os.itf_config.peers{
        trace!("Updating peer {}", peer.public_key.to_string());
        let link_id = os.node_config.links.iter().find(|link|{
            link.public_key == peer.public_key.to_string() 
                && peer.endpoint.is_some() && link.addr_dp == peer.endpoint.unwrap()
        });
        if let Some(cfg) = link_id{
            let id = cfg.id.clone();
            peer.allowed_ips.clear();
            if active_links.contains(&id) {
                let prefix = if cfg.addr_vlan.is_ipv4() { 32 } else { 64 };
                peer.allowed_ips.push(IpAddrMask::new(cfg.addr_vlan, prefix));
            }
        }
        else{
            warn!("Peer [{}] is listed in Wireguard, but isn't found in config!", peer.public_key.to_string());
        }
    }

    let n_cfg = serde_json::to_string(&os.itf_config)?;
    
    if n_cfg != os.prev_itf_config{
        os.prev_itf_config = n_cfg;
        #[cfg(not(windows))]
        os.wg_api.configure_interface(&os.itf_config)?;
        #[cfg(windows)]
        os.wg_api.configure_interface(&os.itf_config, &[], &[])?;
    }

    let table = ps.router.routes.clone();
    tokio::spawn(async move {
        let updater = async {
            let handle = Handle::new()?;
            for route in table.values(){
                let src_addr = route.source.data.addr;
                if src_addr == route.next_hop{
                    // wireguard can handle this :)
                    continue;
                }
                let s_route = Route::new(src_addr, if src_addr.is_ipv4() { 32 } else { 64 })
                    .with_gateway(route.next_hop);
                handle.add(&s_route).await?
            }
            anyhow::Result::<()>::Ok(())
        };
        if let Err(x) = updater.await{
            // ignored
            warn!("Failed to apply system routing rules {x}");
        }
    });
    
    Ok(())
}