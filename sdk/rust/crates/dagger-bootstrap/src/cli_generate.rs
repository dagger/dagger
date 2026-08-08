use clap::{Arg, ArgMatches};
use dagger_codegen::generate;
use dagger_codegen::schema::raw::IntrospectionResponse;
use std::fs;
use std::io::Write;

#[allow(dead_code)]
pub struct GenerateCommand;

#[allow(dead_code)]
impl GenerateCommand {
    pub fn new_cmd() -> clap::Command {
        clap::Command::new("generate")
            .arg(Arg::new("introspection-json").required(true))
            .arg(Arg::new("output").long("output"))
    }

    pub async fn exec(arg_matches: &ArgMatches) -> eyre::Result<()> {
        let introspection_json_path = arg_matches.get_one::<String>("introspection-json").unwrap();
        let introspection_json = fs::read_to_string(introspection_json_path)?;
        let response = serde_json::from_str::<IntrospectionResponse>(&introspection_json)?;
        let schema = response
            .into_schema()
            .schema
            .ok_or_else(|| eyre::eyre!("introspection response did not contain __schema"))?;
        let code = generate(schema)?;

        if let Some(output) = arg_matches.get_one::<String>("output") {
            let mut file = std::fs::File::create(output)?;
            file.write_all(code.as_bytes())?;
        } else {
            println!("{}", code);
        }

        Ok(())
    }
}
