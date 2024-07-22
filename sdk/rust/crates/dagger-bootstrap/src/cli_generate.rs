use std::io::Write;
use std::sync::Arc;

use clap::{Arg, ArgMatches};
use dagger_codegen::generate;
use dagger_codegen::rust::RustGenerator;
use dagger_sdk::core::config::Config;
use dagger_sdk::core::engine::Engine;
use dagger_sdk::core::session::Session;

#[allow(dead_code)]
pub struct GenerateCommand;

#[allow(dead_code)]
impl GenerateCommand {
    pub fn new_cmd() -> clap::Command {
        clap::Command::new("generate").arg(Arg::new("output").long("output"))
    }

    pub async fn exec(arg_matches: &ArgMatches) -> eyre::Result<()> {
        let cfg = Config::default();
        let (conn, _proc) = Engine::new().start(&cfg).await?;
        let session = Session::new();
        let req = session.start(&cfg, &conn)?;
        let schema = session.schema(req).await?;
        let code = generate(
            schema.into_schema().schema.unwrap(),
            Arc::new(RustGenerator {}),
        )?;

        if let Some(output) = arg_matches.get_one::<String>("output") {
            let mut file = std::fs::File::create(output)?;
            file.write_all(code.as_bytes())?;
        } else {
            println!("{}", code);
        }

        Ok(())
    }
}
