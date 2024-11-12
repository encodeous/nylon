use std::path::{Path, PathBuf};
use clap::{Parser, Subcommand};

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
    Init,
    Config
}

pub async fn cli_main() -> anyhow::Result<()>{
    let cli = Args::parse();
    
    Ok(())
}