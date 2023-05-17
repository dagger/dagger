use crate::cli_generate;

pub struct Cli {
    cmd: clap::Command,
}

impl Cli {
    pub fn new() -> eyre::Result<Self> {
        Ok(Self {
            cmd: clap::Command::new("dagger-rust")
                .subcommand_required(true)
                .subcommand(cli_generate::GenerateCommand::new_cmd()),
        })
    }

    pub async fn execute(self, args: &[&str]) -> eyre::Result<()> {
        let matches = self.cmd.get_matches_from(args);

        match matches.subcommand() {
            Some(("generate", args)) => cli_generate::GenerateCommand::exec(args).await?,
            _ => eyre::bail!("command missing"),
        }

        Ok(())
    }
}
