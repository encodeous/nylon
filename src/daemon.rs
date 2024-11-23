use std::path;
use std::path::{Path, PathBuf};
use anyhow::Context;
use crossbeam_channel::unbounded;
use defguard_wireguard_rs::{InterfaceConfiguration, Kernel, Userspace, WGApi, WireguardInterfaceApi};
use log::{error, info};
use root::router::Router;
use tokio::fs;
use tokio_util::sync::CancellationToken;
use crate::config::{CentralConfig, NodeConfig};
use crate::core::control::network::start_networking;
use crate::core::control::timing::register_events;
use crate::core::crypto::sig::SignedClaim;
use crate::core::nylond;
use crate::core::structure::state::{CachedState, MessageQueue, NylonState, OperatingState, PersistentState};

pub fn setup_wireguard(config: &NodeConfig) -> anyhow::Result<WGApi>{
    #[cfg(not(target_os = "macos"))]
    let wgapi = WGApi::<Kernel>::new(config.interface_name.to_string())?;
    #[cfg(target_os = "macos")]
    let wgapi = WGApi::<Userspace>::new(ifname.clone())?;
    
    if let Err(x) = wgapi.create_interface() {
        error!("Failed to create WireGuard interface, did Nylon shutdown correctly? {x}");
    }
    
    Ok(wgapi)
}

pub async fn run(config_dir: PathBuf) -> anyhow::Result<()>{
    info!("Loading node and network configuration from {:?}", path::absolute(config_dir.clone())?);
    
    let node_config_path  = config_dir.join("node.json");
    let net_config_path  = config_dir.join("net.json");
    info!("{node_config_path:?}");
    
    if !node_config_path.exists() {
        info!("This node is not initialized, please run \"ny node init\" first.");
        return Ok(());
    }

    if !net_config_path.exists() {
        info!("No network configuration found, please copy over a net.json or run \"ny net init\"");
        return Ok(());
    }

    let config_str = fs::read_to_string(&node_config_path).await?;
    let config: NodeConfig = serde_json::from_str(&config_str)?;

    let config_str = fs::read_to_string(&net_config_path).await?;
    let central: SignedClaim<CentralConfig> = serde_json::from_str(&config_str)?;

    info!("Verifying configuration integrity");

    central.validate(&central.root_ca)?;
    
    info!("Configuring WireGuard");
    
    let wgapi = setup_wireguard(&config)?;

    // setup state

    let (mtx, mrx) = unbounded();
    let ct = CancellationToken::new();
    let mq = MessageQueue{
        main: mtx,
        cancellation_token: ct
    };
    
    // TODO: merge ps and os
    
    let pubkey = config.node_secret.node_privkey.get_pubkey();
    
    let mut state = NylonState{
        ps: PersistentState {
            router: Router::new(central.nodes.iter().find(|x| x.identity.pubkey == pubkey).unwrap().identity.pubkey.clone())
        },
        os: OperatingState {
            health: Default::default(),
            pings: Default::default(),
            links: Default::default(),
            udp_sock: None,
            wg_api: wgapi,
            join_set: Default::default(),
            prev_itf_config: "".to_string(),
            node_config: config,
            central_config: central.claim.data,
            cached_state: CachedState { pubkey: None },
        },
        mq,
    };
    
    info!("Starting Nylon daemon as node: {} with public key: {}", state.node_info().identity.id, state.pubkey());

    info!("Registering events");
    register_events(&mut state);
    
    info!("Starting networking");
    start_networking(&mut state)?;
    
    let tmq = state.mq.clone();
    
    info!("Starting main thread");
    tokio::task::spawn_blocking(||{
        nylond::main_loop(state, mrx).context("Main Thread Failed: ").unwrap();
    });

    info!("Nylon Daemon has started. Send Ctrl+C to shutdown");
    ctrlc::set_handler(move || {
        tmq.shutdown();
    }).expect("Error setting Ctrl+C handler");
    
    Ok(())
}