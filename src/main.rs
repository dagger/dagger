use cli::Cli;

pub mod cli;
mod cli_generate;
mod cli_session;
mod config;
mod connect_params;
pub mod dagger;
mod downloader;
mod engine;
mod schema;
mod session;

fn main() -> eyre::Result<()> {
    let args = std::env::args();
    let args = args.collect::<Vec<String>>();
    let args = args.iter().map(|s| s.as_str()).collect::<Vec<&str>>();

    Cli::new()?.execute(args.as_slice())?;

    Ok(())
}
