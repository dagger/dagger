//! Typed command-line extraction for repository generation.
//!
//! `clap` validates command shape, while all required values are still handled as
//! fallible data. Fixture overrides are hidden and remain confined to an explicit
//! fixture root; production invocation therefore has one fixed path policy.

use std::ffi::OsString;
use std::path::PathBuf;

use clap::{Arg, ArgAction, ArgGroup, Command, value_parser};
use dagger_codegen::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};

use crate::generate::{GenerateMode, GenerateOutcome, GenerateOverrides, GenerateRequest};

/// Parses and executes one `dagger-rust` command.
pub fn run<I, T>(args: I) -> Result<GenerateOutcome, DiagnosticSet>
where
    I: IntoIterator<Item = T>,
    T: Into<OsString> + Clone,
{
    let matches = command()
        .try_get_matches_from(args)
        .map_err(|_| cli_error())?;
    let (name, arguments) = matches.subcommand().ok_or_else(cli_error)?;
    if name != "generate" {
        return Err(cli_error());
    }

    let workspace = arguments
        .get_one::<PathBuf>("workspace")
        .cloned()
        .ok_or_else(cli_error)?;
    let mode = match (arguments.get_flag("check"), arguments.get_flag("update")) {
        (true, false) => GenerateMode::Check,
        (false, true) => GenerateMode::Update,
        _ => return Err(cli_error()),
    };
    let overrides = GenerateOverrides {
        fixture_root: arguments.get_one::<PathBuf>("fixture-root").cloned(),
        target: arguments.get_one::<PathBuf>("target").cloned(),
        schema: arguments.get_one::<PathBuf>("schema").cloned(),
        ledger: arguments.get_one::<PathBuf>("ledger").cloned(),
        mappings: arguments.get_one::<PathBuf>("mappings").cloned(),
        manifest: arguments.get_one::<PathBuf>("manifest").cloned(),
    };

    crate::generate::execute(GenerateRequest {
        workspace,
        mode,
        overrides,
    })
}

fn command() -> Command {
    Command::new("dagger-rust")
        .subcommand_required(true)
        .subcommand(
            Command::new("generate")
                .arg(
                    Arg::new("workspace")
                        .long("workspace")
                        .required(true)
                        .value_parser(value_parser!(PathBuf)),
                )
                .arg(Arg::new("check").long("check").action(ArgAction::SetTrue))
                .arg(Arg::new("update").long("update").action(ArgAction::SetTrue))
                .group(
                    ArgGroup::new("mode")
                        .args(["check", "update"])
                        .required(true)
                        .multiple(false),
                )
                .arg(hidden_path("fixture-root"))
                .arg(hidden_path("target"))
                .arg(hidden_path("schema"))
                .arg(hidden_path("ledger"))
                .arg(hidden_path("mappings"))
                .arg(hidden_path("manifest")),
        )
}

fn hidden_path(name: &'static str) -> Arg {
    Arg::new(name)
        .long(name)
        .hide(true)
        .value_parser(value_parser!(PathBuf))
}

fn cli_error() -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        DiagnosticCode::GeneratedPublicationFailed,
        Some(DiagnosticCoordinate::new("cli")),
        "generation command arguments are invalid",
    ))
}
