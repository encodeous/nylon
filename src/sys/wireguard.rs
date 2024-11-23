use defguard_wireguard_rs::InterfaceConfiguration;
use crate::core::structure::state::NylonState;

pub fn configure_wg(state: &mut NylonState) {
    // let interface_config = InterfaceConfiguration {
    //     name: ifname.to_string(),
    //     prvkey: config.private_key.clone(),
    //     address: config.addr_vlan.to_string(),
    //     port: config.addr_dp.port() as u32,
    //     peers: config.links.iter().map(|peer_cfg|{
    //         let key = Key::try_from(BASE64_STANDARD.decode(peer_cfg.public_key.clone()).unwrap().as_slice()).unwrap();
    //         let mut peer = Peer::new(key);
    //         peer.endpoint = Some(peer_cfg.addr_dp);
    //         peer.persistent_keepalive_interval = Some(25);
    //         peer
    //     }).collect::<Vec<Peer>>(),
    //     mtu: None,
    // };
    // 
    // #[cfg(not(windows))]
    // wgapi.configure_interface(&interface_config)?;
    #[cfg(windows)]
    wgapi.configure_interface(&interface_config, &[], &[])?;
}