pub struct Cli {
    cmd: clap::Command,
}

impl Cli {
    pub fn new() -> eyre::Result<Self> {
        Ok(Self {
            cmd: clap::Command::new("dagger-rust")
                .subcommand_required(true)
                .subcommand(clap::Command::new("generate")),
        })
    }

    pub fn execute(self, args: &[&str]) -> eyre::Result<()> {
        let matches = self.cmd.get_matches_from(args);

        match matches.subcommand() {
            Some(("generate", _args)) => Ok(()),
            _ => eyre::bail!("command missing"),
        }
    }
}
