use std::path::PathBuf;
use base64::Engine;
use base64::prelude::BASE64_STANDARD;
use clap::{Parser, Subcommand};
use ed25519_dalek::SecretKey;
use x25519_dalek::{EphemeralSecret, PublicKey, StaticSecret};
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
    New
}

#[derive(Subcommand, Debug)]
enum NodeCommands {
    #[command(about = "Signals nylond to shutdown")]
    Stop,
    #[command(about = "Gets the node's peers")]
    Peers,
    #[command(about = "Gets the node's route table")]
    Routes
}

pub async fn cli_main() -> anyhow::Result<()>{
    let cli = Args::parse();

    match cli.command {
        Commands::Run { config_folder } => {
            run(config_folder).await?;
        }

        Commands::Net { command } => {
            match command {
                NetCommands::New => {
                    let secret = StaticSecret::random();
                    BASE64_STANDARD.encode(secret.as_bytes())
                    let key = PublicKey::from(&secret);
                    key.
                }
            }
        }
        Commands::Node { command } => {
            match command {
                NodeCommands::Stop => {}
                NodeCommands::Peers => {}
                NodeCommands::Routes => {}
            }
        }
    }

    Ok(())
}