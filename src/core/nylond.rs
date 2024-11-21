use crate::core::control::network::{network_controller, start_networking};
use crate::core::control::timing::{handle_timed_event, register_events};
use crate::core::structure::state::NylonEvent::Shutdown;
use crate::core::structure::state::{MessageQueue, NylonEvent, NylonState, OperatingState, PersistentState};
use anyhow::Context;
use crossbeam_channel::{unbounded, Receiver};
use defguard_wireguard_rs::WireguardInterfaceApi;
use log::{debug, error, info, trace, warn};
use serde_json::json;
use std::fs;
use std::process::exit;
use tokio_util::sync::CancellationToken;

#[cfg(target_os = "linux")]
pub fn setup_iptables() -> bool{
    let ipt = iptables::new(false);
    if !ipt.is_ok() { return false; }
    let ipt = ipt.unwrap();
    let success = ipt.append_replace("filter", "INPUT", "-i nylon -j ACCEPT").is_ok();
    success && ipt.append_replace("filter", "FORWARD", "-i nylon -o nylon -j ACCEPT").is_ok()
}

#[cfg(target_os = "linux")]
pub fn cleanup_iptables(){
    let ipt = iptables::new(false).unwrap();
    ipt.delete("filter", "INPUT", "-i nylon -j ACCEPT").unwrap();
    ipt.delete("filter", "FORWARD", "-i nylon -o nylon -j ACCEPT").unwrap();
}

// MAIN THREAD

pub fn main_loop(
    mut state: NylonState,
    mqr: Receiver<NylonEvent>
) -> anyhow::Result<()>{

    #[cfg(target_os = "linux")]
    let iptable_setup = setup_iptables();
    #[cfg(target_os = "linux")]
    if !iptable_setup {
        warn!("Failed to automatically set iptable rules");
    }
    
    let mq = state.mq.clone();
    
    let mut main_iter = ||-> anyhow::Result<()> {
        let event = mqr.recv()?;
        trace!("Main Loop Event: {}", json!(event));

        match event {
            NylonEvent::DispatchCommand(cmd) => {
                // TODO: Finish Puppet module
                // if let Err(err) = handle_command(&mut state, cmd, mq.clone()){
                //     error!("Error while handling command: {err}");
                // }
            }
            Shutdown => mq.cancellation_token.cancel(),
            NylonEvent::Timer(timer) => handle_timed_event(&mut state, timer)?,
            NylonEvent::Network(network) => network_controller(&mut state, network)?,
            NylonEvent::NoEvent => {
                // do nothing
            }
        }

        for warn in state.ps.router.warnings.drain(..){
            warn!("{warn:?}");
        }
        Ok(())
    };

    while !mq.cancellation_token.is_cancelled() {
        if let Err(e) = main_iter(){
            error!("Error occurred in main thread: {e:?}");
        }
    }

    let NylonState{ps, os, ..} = &mut state;

    os.join_set.abort_all();
    
    info!("The router has shutdown, saving state...");

    let content = {
        serde_json::to_vec(&ps)?
    };
    fs::write("./route_table.json", content)?;
    debug!("Saved State");
    
    os.wg_api.remove_interface()?;

    #[cfg(target_os = "linux")]
    if iptable_setup {
        cleanup_iptables();
    }
    
    exit(0);
    
    Ok(())
}