use crate::core::structure::state::{NylonState};
use defguard_wireguard_rs::{WireguardInterfaceApi};
use std::collections::{HashSet};

pub fn timed_sys_route_update(state: &mut NylonState,) -> anyhow::Result<()>{
    let NylonState{os, mq, ps} = state;
    
    let table = ps.router.routes.clone();
    let mut active_links = HashSet::new();
    for (vlan, route) in &ps.router.routes{
        active_links.insert(route.link.clone());
    }
    
    // TODO: Rebuild peers based on active links
    
    // for peer in &mut os.itf_config.peers{
    //     trace!("Updating peer {}", peer.public_key.to_string());
    //     let identity = os.central_config.nodes.iter().find(|node|{
    //         node.
    //     })
    //     let link_id = os.links.iter().find(|(link, active)|{
    //         link.public_key == peer.public_key.to_string() 
    //             && peer.endpoint.is_some() && link.addr_dp == peer.endpoint.unwrap()
    //     });
    //     if let Some(cfg) = link_id{
    //         let id = cfg.id.clone();
    //         peer.allowed_ips.clear();
    //         if active_links.contains(&id) {
    //             let prefix = if cfg.addr_vlan.is_ipv4() { 32 } else { 64 };
    //             peer.allowed_ips.push(IpAddrMask::new(cfg.addr_vlan, prefix));
    //             for route in table.values(){
    //                 if route.link == id {
    //                     peer.allowed_ips.push(IpAddrMask::new(route.source.data.addr, prefix));
    //                 }
    //             }
    //         }
    //     }
    //     else{
    //         warn!("Peer [{}] is listed in Wireguard, but isn't found in config!", peer.public_key.to_string());
    //     }
    // }

    // let n_cfg = serde_json::to_string(&os.itf_config)?;
    // 
    // if n_cfg != os.prev_itf_config{
    //     os.prev_itf_config = n_cfg;
    //     #[cfg(not(windows))]
    //     os.wg_api.configure_interface(&os.itf_config)?;
    //     #[cfg(windows)]
    //     os.wg_api.configure_interface(&os.itf_config, &[], &[])?;
    // }
    
    Ok(())
}