use std::env;
use crate::cli::cli_main;

mod config;
mod core;
mod util;
mod daemon;
mod cli;
mod sys;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    if env::var("RUST_LOG").is_err() {
        env::set_var("RUST_LOG", "info")
    }
    env_logger::init();
    cli_main().await?;
    
    // TODO: Nylon Daemon
    
    // remember to:
    // sudo iptables -I INPUT -i nylon -j ACCEPT
    // sudo iptables -I FORWARD -i nylon -o nylon -j ACCEPT
    // if !Path::exists(Path::new("./config.json")) {
    //     let x = NodeConfig{
    //         addr_vlan: "10.1.0.1/24".to_string(),
    //         private_key: "<private_key>".to_string(),
    //         addr_dp: SocketAddr::from_str("0.0.0.0:59162")?,
    //         addr_ctl: SocketAddr::from_str("0.0.0.0:59162")?,
    //         addr_dg: SocketAddr::from_str("0.0.0.0:59163")?,
    //         links: vec![
    //             LinkConfig {
    //                 id: "laptop_via_lan".to_string(),
    //                 public_key: "<node public key>".to_string(),
    //                 addr_dp: SocketAddr::from_str("192.168.1.5:59162")?,
    //                 addr_ctl: SocketAddr::from_str("192.168.1.5:59162")?,
    //                 addr_dg: SocketAddr::from_str("0.0.0.0:59163")?,
    //                 addr_vlan: IpAddr::from_str("10.1.0.2")?
    //             }
    //         ],
    //         network_key: "".to_string(),
    //     };
    //     File::create(Path::new("./config.json"))?.write_all(serde_json::to_string_pretty(&json!(x))?.as_ref())?;
    //     info!("config.json created, please configure this node!");
    //     return Ok(());
    // }
    // let config_str = fs::read_to_string(&Path::new("./config.json"))?;
    // let config: NodeConfig = serde_json::from_str(&config_str)?;
    // 
    // let ifname = "nylon";
    // 
    // #[cfg(not(target_os = "macos"))]
    // let wgapi = WGApi::<Kernel>::new(ifname.to_string())?;
    // #[cfg(target_os = "macos")]
    // let wgapi = WGApi::<Userspace>::new(ifname.clone())?;
    // 
    // if let Err(x) = wgapi.create_interface() {
    //     error!("Failed to create wireguard interface, did Nylon shutdown correctly? {x}");
    // }
    // 
    // 
  
    // 
    // let mut saved_state = if let Ok(file) = fs::read_to_string("./route_table.json") {
    //     serde_json::from_str(&file)?
    // } else {
    //     PersistentState {
    //         router: Router::new(IpAddrMask::from_str(&config.addr_vlan)?.ip)
    //     }
    // };
    // 
    // for peer in &config.links {
    //     saved_state.router.links.insert(
    //         peer.id.clone(),
    //         Neighbour {
    //             addr: peer.addr_vlan,
    //             metric: INF,
    //             routes: HashMap::new(),
    //         },
    //     );
    // }
    // saved_state.router.links.retain(|k, _| {
    //     config.links.iter().any(|v| v.id == *k)
    // });
    // 
    // let mq = start_router(saved_state, OperatingState{
    //     health: Default::default(),
    //     pings: Default::default(),
    // 
    //     ctl_links: Default::default(),
    //     udp_sock: None,
    //     log_routing: false,
    //     log_delivery: false,
    //     node_config: config,
    //     itf_config: interface_config,
    //     wg_api: wgapi,
    //     prev_itf_config: String::new(),
    //     join_set: JoinSet::default(),
    // })?;
    // 
    // let mut input_buf = String::new();
    // 
    // let tmq = mq.clone();
    // ctrlc::set_handler(move || {
    //     tmq.shutdown();
    // }).expect("Error setting Ctrl-C handler");
    // 
    // while !mq.cancellation_token.is_cancelled(){
    //     stdin().read_line(&mut input_buf)?;
    //     mq.main.send(DispatchCommand(input_buf))?;
    //     input_buf = String::new();
    // }
    // 
    // sleep(Duration::from_secs(1)).await; // wait for main thread to finish
    
    Ok(())
}
