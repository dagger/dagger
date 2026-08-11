//! Fixed Cargo checks complementing generated package-graph properties.

use std::collections::BTreeSet;
use std::path::{Path, PathBuf};
use std::process::{Command, Output};

use serde::Deserialize;

#[derive(Debug, Deserialize)]
struct Metadata {
    packages: Vec<Package>,
}

#[derive(Debug, Deserialize)]
struct Package {
    name: String,
    version: String,
    edition: String,
    rust_version: Option<String>,
    repository: Option<String>,
    license: Option<String>,
    publish: Option<Vec<String>>,
    dependencies: Vec<Dependency>,
}

#[derive(Debug, Deserialize)]
struct Dependency {
    name: String,
    req: String,
    kind: Option<String>,
}

#[test]
fn cargo_metadata_exposes_only_the_exact_acyclic_public_pair() {
    let output = cargo(["metadata", "--no-deps", "--format-version", "1", "--locked"]);
    let metadata: Metadata =
        serde_json::from_slice(&output.stdout).expect("Cargo metadata is JSON");
    let publishable = metadata
        .packages
        .iter()
        .filter(|package| {
            package
                .publish
                .as_ref()
                .is_none_or(|registries| !registries.is_empty())
        })
        .map(|package| package.name.as_str())
        .collect::<BTreeSet<_>>();
    assert_eq!(
        publishable,
        BTreeSet::from(["dagger-sdk", "dagger-sdk-macros"])
    );

    let sdk = package(&metadata, "dagger-sdk");
    let macros = package(&metadata, "dagger-sdk-macros");
    for package in [sdk, macros] {
        assert_eq!(package.version, "1.0.0-beta.10");
        assert_eq!(package.edition, "2024");
        assert_eq!(package.rust_version.as_deref(), Some("1.97.1"));
        assert_eq!(
            package.repository.as_deref(),
            Some("https://github.com/dagger/dagger")
        );
        assert_eq!(package.license.as_deref(), Some("Apache-2.0"));
    }
    assert!(sdk.dependencies.iter().any(|dependency| {
        dependency.name == "dagger-sdk-macros"
            && dependency.req == "=1.0.0-beta.10"
            && dependency.kind.is_none()
    }));
    for private in [
        "dagger-bootstrap",
        "dagger-codegen",
        "dagger-sdk-completeness",
        "dagger-sdk-engine",
    ] {
        assert!(
            !sdk.dependencies
                .iter()
                .any(|dependency| dependency.name == private)
        );
        assert!(
            !macros
                .dependencies
                .iter()
                .any(|dependency| dependency.name == private)
        );
    }
    assert!(
        !macros
            .dependencies
            .iter()
            .any(|dependency| dependency.name == "dagger-sdk")
    );
}

#[test]
fn cargo_package_lists_both_public_crates_without_generated_or_private_leakage() {
    let macros = package_list("dagger-sdk-macros");
    assert!(macros.contains("README.md"));
    assert!(macros.contains("src/lib.rs"));
    assert!(
        !macros
            .iter()
            .any(|path| path.starts_with("../") || path.contains("dagger-sdk-engine"))
    );

    let sdk = package_list("dagger-sdk");
    assert!(sdk.contains("README.md"));
    assert!(sdk.contains("LICENSE"));
    assert!(sdk.contains("src/module/codec.rs"));
    assert!(sdk.contains("src/gen/mod.rs"));
    assert!(
        !sdk.iter()
            .any(|path| path.starts_with("../") || path.contains("dagger-sdk-engine"))
    );
}

fn package<'a>(metadata: &'a Metadata, name: &str) -> &'a Package {
    metadata
        .packages
        .iter()
        .find(|package| package.name == name)
        .unwrap_or_else(|| panic!("Cargo metadata omitted {name}"))
}

fn package_list(name: &str) -> BTreeSet<String> {
    let output = cargo(["package", "-p", name, "--list", "--locked", "--allow-dirty"]);
    String::from_utf8(output.stdout)
        .expect("Cargo package list is UTF-8")
        .lines()
        .map(str::to_owned)
        .collect()
}

fn cargo<const N: usize>(arguments: [&str; N]) -> Output {
    let output = Command::new(cargo_binary())
        .args(arguments)
        .current_dir(workspace_root())
        .output()
        .expect("Cargo command starts");
    assert!(
        output.status.success(),
        "Cargo command failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    output
}

fn cargo_binary() -> PathBuf {
    std::env::var_os("CARGO")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("cargo"))
}

fn workspace_root() -> &'static Path {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .and_then(Path::parent)
        .expect("completeness crate remains beneath the Rust workspace")
}
