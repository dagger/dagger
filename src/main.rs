use cli::Cli;

pub mod cli;
mod cli_generate;

fn main() -> eyre::Result<()> {
    // again
    color_eyre::install().unwrap();

    let args = std::env::args();
    let args = args.collect::<Vec<String>>();
    let args = args.iter().map(|s| s.as_str()).collect::<Vec<&str>>();

    Cli::new()?.execute(args.as_slice())?;

    Ok(())
}
