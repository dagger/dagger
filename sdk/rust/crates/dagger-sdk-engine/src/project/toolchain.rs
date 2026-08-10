//! Exact Rust toolchain selection without rewriting caller policy.
//!
//! Selection follows package-local, then nearest enclosing declarations. Moving
//! channels and below-MSRV releases fail before initialization can produce a result.

use std::str::FromStr as _;

use semver::Version;
use toml_edit::{DocumentMut, Item};

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::{ExactRustToolchain, RelativeOperationPath, ToolchainSelection};

const TARGET_TOOLCHAIN: &str = "1.97.1";

/// One candidate toolchain declaration in deterministic precedence order.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ToolchainDeclaration<'a> {
    /// Operation-relative declaration path.
    pub path: &'a RelativeOperationPath,
    /// Exact bytes of `rust-toolchain` or `rust-toolchain.toml`.
    pub bytes: &'a [u8],
}

/// Chooses the first exact compatible declaration or the target default.
pub fn select_toolchain(
    declarations: &[ToolchainDeclaration<'_>],
) -> Result<ToolchainSelection, EngineDiagnostic> {
    let Some(declaration) = declarations.first() else {
        return Ok(ToolchainSelection::TargetDefault {
            toolchain: TARGET_TOOLCHAIN
                .parse()
                .expect("target toolchain constant must parse"),
        });
    };
    let selected = parse_declaration(declaration)?;
    let selected_version =
        Version::parse(selected.as_str()).expect("validated toolchain must parse");
    let required = Version::parse(TARGET_TOOLCHAIN).expect("target toolchain constant must parse");
    if selected_version < required {
        return Err(diagnostic(
            EngineDiagnosticCode::ToolchainUnsupported,
            declaration.path.as_str(),
            "declared Rust toolchain is below the Rust SDK MSRV",
        ));
    }
    Ok(ToolchainSelection::Declared {
        toolchain: selected,
        declaration_path: declaration.path.clone(),
    })
}

fn parse_declaration(
    declaration: &ToolchainDeclaration<'_>,
) -> Result<ExactRustToolchain, EngineDiagnostic> {
    let source =
        std::str::from_utf8(declaration.bytes).map_err(|_| non_reproducible(declaration.path))?;
    let value = if declaration.path.as_str().ends_with(".toml") {
        let document =
            DocumentMut::from_str(source).map_err(|_| non_reproducible(declaration.path))?;
        document
            .get("toolchain")
            .and_then(Item::as_table)
            .and_then(|table| table.get("channel"))
            .and_then(Item::as_str)
            .ok_or_else(|| non_reproducible(declaration.path))?
            .to_owned()
    } else {
        source.trim().to_owned()
    };
    value
        .parse()
        .map_err(|_| non_reproducible(declaration.path))
}

fn non_reproducible(path: &RelativeOperationPath) -> EngineDiagnostic {
    diagnostic(
        EngineDiagnosticCode::ToolchainNonReproducible,
        path.as_str(),
        "toolchain declaration must select one exact stable semantic version",
    )
}

fn diagnostic(code: EngineDiagnosticCode, coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}
