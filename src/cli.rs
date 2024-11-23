use std::fs;
use std::net::{IpAddr, Ipv4Addr, SocketAddr};
use std::path::PathBuf;
use std::str::FromStr;
use anyhow::bail;
use base64::Engine;
use base64::prelude::BASE64_STANDARD;
use clap::{Parser, Subcommand};
use defguard_wireguard_rs::net::IpAddrMask;
use ed25519_dalek::SecretKey;
use log::{info, warn};
use serde_json::json;
use uuid::Uuid;
use x25519_dalek::{EphemeralSecret, PublicKey, StaticSecret};
use crate::config::{CentralConfig, LinkInfo, NodeConfig, NodeIdentity, NodeInfo, NodeSecret, VLANAddr};
use crate::core::crypto::entity::EntitySecret;
use crate::core::crypto::sig::SignedClaim;
use crate::daemon::run;

#[derive(Parser, Debug)]
#[command(version = "1.0")]
#[command(about = "Nylon Network Management CLI", long_about = None)]
pub struct Args {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand, Debug)]
enum Commands {
    #[command(about = "Run Nylon")]
    Run {
        #[arg(default_value = "./")]
        config_folder: PathBuf
    },
    #[command(about = "Nylon network management commands")]
    Net{
        #[command(subcommand)]
        command: NetCommands,
    },
    #[command(about = "Nylon node management commands")]
    Node{
        #[command(subcommand)]
        command: NodeCommands,
    }
}

#[derive(Subcommand, Debug)]
enum NetCommands {
    #[command(about = "Create a new Nylon Network")]
    Init,
    #[command(about = "Sign the claim inside of the network configuration file using the root identity")]
    Sign {
        #[arg(default_value = "./net.json", short_alias = 'c')]
        net_json_path: PathBuf,
        #[arg(default_value = "./root.json", short_alias = 'k')]
        root_key_path: PathBuf
    },
}

#[derive(Subcommand, Debug)]
enum NodeCommands {
    #[command(about = "Signals nylond to shutdown")]
    Stop,
    #[command(about = "Gets the node's peers")]
    Peers,
    #[command(about = "Gets the node's route table")]
    Routes,
    #[command(about = "Initializes the current node")]
    Init {
        #[arg(help = "The node's id. Must be globally unique")]
        id: String
    }
}

pub async fn cli_main() -> anyhow::Result<()>{
    let cli = Args::parse();

    match cli.command {
        Commands::Run { config_folder } => {
            run(config_folder).await?;
        }

        Commands::Net { command } => {
            match command {
                NetCommands::Init => {
                    // create a new network
                    // generate root key
                    let root = EntitySecret::generate();

                    // TODO: Check for files already in the current folder

                    fs::write("root.json", serde_json::to_vec(&json!(root))?)?;
                    info!("Created network root certificate (root.json).");

                    let config = CentralConfig {
                        version: 0,
                        config_repos: vec![],
                        nodes: vec![],
                        root_ca: root.get_pubkey(),
                    };

                    fs::write("net.json", serde_json::to_vec_pretty(&json!(root.sign_forever(config)?))?)?;
                    info!("Created network central config (net.json).");
                }
                NetCommands::Sign { net_json_path, root_key_path } => {
                    let root: EntitySecret = serde_json::from_str(&fs::read_to_string(root_key_path)?)?;
                    let cfg: SignedClaim<CentralConfig> = serde_json::from_str(&fs::read_to_string(net_json_path.clone())?)?;
                    if cfg.root_ca != root.get_pubkey(){
                        bail!("The root identity {} does not match the root identity in the config {}", root.get_pubkey(), cfg.root_ca);
                    }
                    if cfg.validate(&cfg.root_ca).is_ok() {
                        warn!("The specified config is already valid. No need to sign.");
                    }
                    else{
                        let mut nclaim = cfg.claim;
                        nclaim.serial = Uuid::new_v4();
                        nclaim.data.version += 1;
                        let sig = nclaim.sign_claim(&root)?;
                        fs::write(net_json_path, serde_json::to_vec_pretty(&json!(sig))?)?;
                        info!("Successfully signed a new network config file.");
                    }
                }
            }
        }
        Commands::Node { command } => {
            match command {
                NodeCommands::Stop => {}
                NodeCommands::Peers => {}
                NodeCommands::Routes => {}
                NodeCommands::Init { id } => {
                    // generate wireguard keypair

                    let secret = StaticSecret::random();
                    let wg_privkey = BASE64_STANDARD.encode(secret.as_bytes());
                    let wg_pubkey = BASE64_STANDARD.encode(PublicKey::from(&secret).as_bytes());
                    
                    let nodepriv = EntitySecret::generate();
                    
                    let node_secret = NodeSecret {
                        wg_privkey,
                        node_privkey: nodepriv.clone(),
                    };
                    let node_config = NodeConfig{
                        node_secret,
                        node_sock: LinkInfo {
                            addr_ctl: SocketAddr::from_str("0.0.0.0:59162")?,
                            addr_dg: SocketAddr::from_str("0.0.0.0:59162")?,
                            addr_dp: SocketAddr::from_str("0.0.0.0:59163")?,
                        },
                        interface_name: "nylon".to_string(),
                    };
                    info!("Generated node-specific configuration including secret (node.json)");
                    fs::write("node.json", serde_json::to_vec_pretty(&json!(node_config))?)?;

                    let node_id = NodeIdentity {
                        id,
                        pubkey: nodepriv.get_pubkey(),
                        dp_pubkey: wg_pubkey,
                    };

                    let node_info = NodeInfo{
                       identity: node_id,
                        reachable_via: vec![
                            LinkInfo {
                                addr_ctl: SocketAddr::from_str("192.168.0.1:59162")?,
                                addr_dg: SocketAddr::from_str("192.168.0.1:59162")?,
                                addr_dp: SocketAddr::from_str("192.168.0.1:59163")?
                            }
                        ],
                        addr_vlan: VLANAddr(IpAddrMask{
                            ip: IpAddr::V4(Ipv4Addr::new(10,0,0,1)),
                            cidr: 32,
                        }),
                    };

                    // TODO: Detect interfaces or ip addresses of the node
                    info!("[Instruction] Trusting this node\n
                    Please add the following json object to the \"nodes\" array in net.json to trust this node.\n
                    You can modify the addr_vlan, and replace \"192.168.0.1\" with the real public address of the node.\n
                    After modifying net.json, make sure to run \"ny net sign\" and \"ny net push\"");

                    println!("{}", serde_json::to_string_pretty(&json!(node_info))?);

                }
            }
        }
    }

    Ok(())
}