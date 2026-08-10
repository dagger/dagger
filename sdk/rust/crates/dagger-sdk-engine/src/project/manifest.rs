//! Format-preserving Cargo manifest adoption.
//!
//! Existing manifests retain their authored layout. The planner either proves an
//! existing Dagger dependency and binary target equivalent to the immutable engine
//! descriptor, adds missing declarations, or rejects the document without rewriting
//! conflicting caller policy.

use std::str::FromStr as _;

use semver::Version;
use sha2::{Digest as _, Sha256};
use toml_edit::{ArrayOfTables, DocumentMut, InlineTable, Item, Table, Value, value};

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::{PublishedSdkDependency, RelativeOperationPath, Sha256Digest};

const RUST_EDITION: &str = "2024";
const RUST_MSRV: &str = "1.97.1";

/// Pure, format-preserving amendment proposed for one selected Cargo package.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CargoManifestPlan {
    /// Digest of the existing authored bytes, absent only for a new package.
    pub original_digest: Option<Sha256Digest>,
    /// Complete candidate manifest bytes.
    pub rendered: Vec<u8>,
    /// Whether the plan adds the immutable public SDK dependency.
    pub dependency_changed: bool,
    /// Whether the plan adds the engine-owned binary target.
    pub binary_target_changed: bool,
}

/// Coordinated package/workspace amendment for an inherited SDK dependency.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CargoWorkspaceAdoptionPlan {
    /// Selected package amendment, retaining its `workspace = true` policy.
    pub package: CargoManifestPlan,
    /// Digest of the authored workspace manifest.
    pub workspace_original_digest: Sha256Digest,
    /// Complete candidate workspace manifest bytes.
    pub workspace_rendered: Vec<u8>,
    /// Whether the owning workspace dependency table gained `dagger-sdk`.
    pub workspace_dependency_changed: bool,
}

/// Plans the only Cargo edits owned by Rust SDK initialization.
pub fn plan_manifest(
    current: Option<&[u8]>,
    package_name: &str,
    dependency: &PublishedSdkDependency,
    generated_binary: &RelativeOperationPath,
) -> Result<CargoManifestPlan, EngineDiagnostic> {
    let original_digest = current.map(digest);
    let mut document = match current {
        Some(bytes) => {
            let source = std::str::from_utf8(bytes)
                .map_err(|_| invalid("manifest", "Cargo manifest must be UTF-8"))?;
            DocumentMut::from_str(source)
                .map_err(|_| invalid("manifest", "Cargo manifest is not valid TOML"))?
        }
        None => new_manifest(package_name),
    };
    if current.is_some() {
        validate_existing_package(&document)?;
    }
    let dependency_changed = plan_dependency(&mut document, dependency)?;
    let binary_target_changed = plan_binary(&mut document, generated_binary)?;
    Ok(CargoManifestPlan {
        original_digest,
        rendered: document.to_string().into_bytes(),
        dependency_changed,
        binary_target_changed,
    })
}

/// Plans an inherited dependency at its unambiguous owning workspace table.
pub fn plan_manifest_with_workspace(
    package_current: &[u8],
    workspace_current: &[u8],
    dependency: &PublishedSdkDependency,
    generated_binary: &RelativeOperationPath,
) -> Result<CargoWorkspaceAdoptionPlan, EngineDiagnostic> {
    let package_source = std::str::from_utf8(package_current)
        .map_err(|_| invalid("manifest", "Cargo manifest must be UTF-8"))?;
    let mut package_document = DocumentMut::from_str(package_source)
        .map_err(|_| invalid("manifest", "Cargo manifest is not valid TOML"))?;
    validate_existing_package(&package_document)?;
    let inherited = package_document
        .get("dependencies")
        .and_then(Item::as_table)
        .and_then(|table| table.get("dagger-sdk"))
        .is_some_and(is_workspace_dependency);
    if !inherited {
        return Err(conflict());
    }
    let binary_target_changed = plan_binary(&mut package_document, generated_binary)?;

    let workspace_source = std::str::from_utf8(workspace_current).map_err(|_| {
        invalid(
            "workspace-manifest",
            "workspace Cargo manifest must be UTF-8",
        )
    })?;
    let mut workspace_document = DocumentMut::from_str(workspace_source).map_err(|_| {
        invalid(
            "workspace-manifest",
            "workspace Cargo manifest is not valid TOML",
        )
    })?;
    let workspace = workspace_document["workspace"]
        .as_table_mut()
        .ok_or_else(|| invalid("workspace-manifest.workspace", "workspace table is missing"))?;
    let dependencies = workspace["dependencies"].or_insert(Item::Table(Table::new()));
    let dependencies = dependencies.as_table_mut().ok_or_else(|| {
        invalid(
            "workspace-manifest.workspace.dependencies",
            "workspace dependencies must be a TOML table",
        )
    })?;
    let workspace_dependency_changed = if let Some(existing) = dependencies.get("dagger-sdk") {
        validate_dependency(existing, dependency)?;
        false
    } else {
        dependencies.insert("dagger-sdk", dependency_item(dependency));
        true
    };
    Ok(CargoWorkspaceAdoptionPlan {
        package: CargoManifestPlan {
            original_digest: Some(digest(package_current)),
            rendered: package_document.to_string().into_bytes(),
            dependency_changed: false,
            binary_target_changed,
        },
        workspace_original_digest: digest(workspace_current),
        workspace_rendered: workspace_document.to_string().into_bytes(),
        workspace_dependency_changed,
    })
}

fn new_manifest(package_name: &str) -> DocumentMut {
    let mut document = DocumentMut::new();
    document["package"]["name"] = value(package_name);
    document["package"]["version"] = value("0.1.0");
    document["package"]["edition"] = value(RUST_EDITION);
    document["package"]["rust-version"] = value(RUST_MSRV);
    document
}

fn validate_existing_package(document: &DocumentMut) -> Result<(), EngineDiagnostic> {
    let package = document
        .get("package")
        .and_then(Item::as_table)
        .ok_or_else(|| invalid("manifest.package", "selected manifest has no package table"))?;
    if let Some(edition) = package.get("edition").and_then(Item::as_str)
        && !matches!(edition, "2024")
    {
        return Err(invalid(
            "manifest.package.edition",
            "selected package must already use a target-compatible Rust edition",
        ));
    }
    if let Some(rust_version) = package.get("rust-version").and_then(Item::as_str) {
        let declared = Version::parse(rust_version).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::ToolchainNonReproducible,
                "manifest.package.rust-version",
                "rust-version must be one exact stable semantic version",
            )
        })?;
        if declared < Version::parse(RUST_MSRV).expect("constant MSRV must parse") {
            return Err(diagnostic(
                EngineDiagnosticCode::ToolchainUnsupported,
                "manifest.package.rust-version",
                "selected package rust-version is below the Rust SDK MSRV",
            ));
        }
    }
    Ok(())
}

fn plan_dependency(
    document: &mut DocumentMut,
    dependency: &PublishedSdkDependency,
) -> Result<bool, EngineDiagnostic> {
    let dependencies = document["dependencies"].or_insert(Item::Table(Table::new()));
    let table = dependencies.as_table_mut().ok_or_else(|| {
        invalid(
            "manifest.dependencies",
            "dependencies must be represented by a TOML table",
        )
    })?;
    if let Some(existing) = table.get("dagger-sdk") {
        validate_dependency(existing, dependency)?;
        return Ok(false);
    }
    table.insert("dagger-sdk", dependency_item(dependency));
    Ok(true)
}

fn dependency_item(dependency: &PublishedSdkDependency) -> Item {
    match dependency {
        PublishedSdkDependency::Registry { exact_version, .. } => {
            value(format!("={exact_version}"))
        }
        PublishedSdkDependency::Git { url, revision, .. } => {
            let mut table = InlineTable::new();
            table.insert("git", Value::from(url.as_str()));
            table.insert("rev", Value::from(revision.as_str()));
            Item::Value(Value::InlineTable(table))
        }
    }
}

fn validate_dependency(
    item: &Item,
    expected: &PublishedSdkDependency,
) -> Result<(), EngineDiagnostic> {
    if let Some(version) = item.as_str() {
        return match expected {
            PublishedSdkDependency::Registry { exact_version, .. }
                if version == format!("={exact_version}") =>
            {
                Ok(())
            }
            _ => Err(conflict()),
        };
    }
    let Some(table) = item.as_inline_table() else {
        if item
            .as_table_like()
            .and_then(|table| table.get("workspace"))
            .and_then(Item::as_bool)
            == Some(true)
        {
            return Err(diagnostic(
                EngineDiagnosticCode::SdkDependencyConflict,
                "manifest.dependencies.dagger-sdk",
                "workspace-inherited dependency must be resolved at its owning workspace table",
            ));
        }
        return Err(mutable_dependency());
    };
    for mutable in ["branch", "tag", "path"] {
        if table.contains_key(mutable) {
            return Err(mutable_dependency());
        }
    }
    match expected {
        PublishedSdkDependency::Registry { exact_version, .. } => {
            let version = table.get("version").and_then(Value::as_str);
            if version == Some(format!("={exact_version}").as_str()) && table.get("git").is_none() {
                Ok(())
            } else {
                Err(conflict())
            }
        }
        PublishedSdkDependency::Git { url, revision, .. } => {
            let actual_url = table.get("git").and_then(Value::as_str);
            let actual_revision = table.get("rev").and_then(Value::as_str);
            if actual_url == Some(url.as_str()) && actual_revision == Some(revision.as_str()) {
                Ok(())
            } else if actual_revision.is_none() {
                Err(mutable_dependency())
            } else {
                Err(conflict())
            }
        }
    }
}

fn is_workspace_dependency(item: &Item) -> bool {
    item.as_inline_table()
        .and_then(|table| table.get("workspace"))
        .and_then(Value::as_bool)
        == Some(true)
        || item
            .as_table_like()
            .and_then(|table| table.get("workspace"))
            .and_then(Item::as_bool)
            == Some(true)
}

fn plan_binary(
    document: &mut DocumentMut,
    generated_binary: &RelativeOperationPath,
) -> Result<bool, EngineDiagnostic> {
    let bins = document
        .entry("bin")
        .or_insert(Item::ArrayOfTables(ArrayOfTables::new()));
    let bins = bins.as_array_of_tables_mut().ok_or_else(|| {
        invalid(
            "manifest.bin",
            "binary targets must use Cargo array-of-tables syntax",
        )
    })?;
    for binary in bins.iter() {
        if binary.get("name").and_then(Item::as_str) == Some("dagger-module") {
            return if binary.get("path").and_then(Item::as_str) == Some(generated_binary.as_str()) {
                Ok(false)
            } else {
                Err(diagnostic(
                    EngineDiagnosticCode::OwnershipConflict,
                    "manifest.bin.dagger-module",
                    "caller-owned binary target conflicts with the generated runtime target",
                ))
            };
        }
    }
    let mut binary = Table::new();
    binary.insert("name", value("dagger-module"));
    binary.insert("path", value(generated_binary.as_str()));
    bins.push(binary);
    Ok(true)
}

fn digest(bytes: &[u8]) -> Sha256Digest {
    let digest = Sha256::digest(bytes);
    format!("sha256:{digest:x}")
        .parse()
        .expect("SHA-256 formatting must satisfy the digest scalar")
}

fn conflict() -> EngineDiagnostic {
    diagnostic(
        EngineDiagnosticCode::SdkDependencyConflict,
        "manifest.dependencies.dagger-sdk",
        "existing dagger-sdk dependency differs from the immutable engine descriptor",
    )
}

fn mutable_dependency() -> EngineDiagnostic {
    diagnostic(
        EngineDiagnosticCode::SdkDependencyMutable,
        "manifest.dependencies.dagger-sdk",
        "dagger-sdk dependency must use an exact registry version or immutable Git revision",
    )
}

fn invalid(coordinate: &str, message: &str) -> EngineDiagnostic {
    diagnostic(
        EngineDiagnosticCode::CargoManifestInvalid,
        coordinate,
        message,
    )
}

fn diagnostic(code: EngineDiagnosticCode, coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}
