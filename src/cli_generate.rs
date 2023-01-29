use clap::{Arg, ArgMatches};
use dagger_codegen::codegen::CodeGeneration;

use crate::{config::Config, engine::Engine, session::Session};

#[allow(dead_code)]
pub struct GenerateCommand;

#[allow(dead_code)]
impl GenerateCommand {
    pub fn new_cmd() -> clap::Command {
        clap::Command::new("generate").arg(Arg::new("output").long("output"))
    }

    pub fn exec(arg_matches: &ArgMatches) -> eyre::Result<()> {
        let cfg = Config::default();
        let (conn, _proc) = Engine::new().start(&cfg)?;
        let session = Session::new();
        let req = session.start(&cfg, &conn)?;
        let schema = session.schema(req)?;
        let code = CodeGeneration::new().generate(&schema)?;

        if let Some(output) = arg_matches.get_one::<String>("output") {
            // TODO: Write to file
        } else {
            println!("{}", code);
        }

        Ok(())
    }
}
