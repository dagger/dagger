//! Cargo project discovery and caller-policy-preserving adoption.
//!
//! Discovery consumes Cargo metadata v1 rather than scanning manifests recursively.
//! The pure selector is separated from process execution so package ownership remains
//! deterministic under metadata ordering and can be checked independently.

pub mod manifest;
pub mod source_snapshot;
pub mod toolchain;
pub mod vcs;

use std::collections::BTreeSet;
use std::path::{Path, PathBuf};

use serde::Deserialize;
use sha2::{Digest as _, Sha256};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::post_work::{
    Cancellation, CommandSpec, current_allowlisted_environment, execute_fixed_structured_stdout,
};
use crate::project::toolchain::{ToolchainDeclaration, select_toolchain};
use crate::{
    ArtifactOwnership, CargoPackage, CargoTarget, DiscoveredCargoProject, ExactRustToolchain,
    OperationManifest, OperationRoot, RelativeOperationPath, RuntimeCargoProject, Sha256Digest,
    StableCoordinate, ToolchainSelection,
};

/// Maximum accepted `cargo metadata` output.
pub const MAX_METADATA_BYTES: usize = 8 * 1024 * 1024;

/// Cargo metadata v1 fields used by exact package selection.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
pub struct CargoMetadataV1 {
    /// Local and dependency packages returned by Cargo.
    pub packages: Vec<CargoMetadataPackage>,
    /// Opaque package IDs belonging to the selected workspace.
    pub workspace_members: BTreeSet<String>,
    /// Absolute workspace root reported by Cargo.
    pub workspace_root: PathBuf,
    /// Absolute target directory reported by Cargo.
    pub target_directory: PathBuf,
}

/// Cargo package fields required to determine source ownership.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
pub struct CargoMetadataPackage {
    /// Opaque Cargo package ID.
    pub id: String,
    /// Cargo package name.
    pub name: String,
    /// Absolute owning manifest path.
    pub manifest_path: PathBuf,
}

/// Deterministic outcome before initialization chooses an existing or new package.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum PackageSelection {
    /// One metadata workspace member owns the module root.
    Existing(CargoPackage),
    /// No manifest exists and initialization may create this exact package root.
    Create {
        /// New package manifest beneath the operation root.
        manifest_path: RelativeOperationPath,
        /// Engine-selected package root.
        package_root: RelativeOperationPath,
    },
}

/// Decodes bounded Cargo metadata without rejecting forward-compatible fields.
pub fn decode_metadata(bytes: &[u8]) -> Result<CargoMetadataV1, EngineDiagnostic> {
    if bytes.len() > MAX_METADATA_BYTES {
        return Err(diagnostic(
            EngineDiagnosticCode::CargoManifestInvalid,
            "cargo-metadata",
            "Cargo metadata exceeds its output bound",
        ));
    }
    serde_json::from_slice(bytes).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestInvalid,
            "cargo-metadata",
            "Cargo metadata is not valid version-1 JSON",
        )
    })
}

/// Selects the one workspace member whose package root is the module source root.
pub fn select_package(
    metadata: &CargoMetadataV1,
    operation_root: &Path,
    module_source: &RelativeOperationPath,
    manifest_hint: Option<&RelativeOperationPath>,
) -> Result<CargoPackage, EngineDiagnostic> {
    let root = operation_root.canonicalize().map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OutputPathEscape,
            "operation-root",
            "operation root cannot be resolved",
        )
    })?;
    let source = module_source.join_lexically(&root);
    let hint = manifest_hint.map(|path| path.join_lexically(&root));
    let mut matches = metadata
        .packages
        .iter()
        .filter(|package| metadata.workspace_members.contains(&package.id))
        .filter_map(|package| {
            let manifest = lexical_normalize(&package.manifest_path)?;
            if !manifest.starts_with(&root) || hint.as_ref().is_some_and(|hint| hint != &manifest) {
                return None;
            }
            let package_root = manifest.parent()?.to_path_buf();
            (package_root == source).then_some((package, manifest, package_root))
        })
        .collect::<Vec<_>>();
    matches.sort_by(|left, right| left.0.id.cmp(&right.0.id));

    let [(package, manifest, package_root)] = matches.as_slice() else {
        let code = if matches.is_empty() {
            EngineDiagnosticCode::CargoPackageMissing
        } else {
            EngineDiagnosticCode::CargoPackageAmbiguous
        };
        return Err(diagnostic(
            code,
            module_source.as_str(),
            if matches.is_empty() {
                "no workspace package owns the selected module source"
            } else {
                "multiple workspace packages own the selected module source"
            },
        ));
    };

    // Cargo paths are followed only after lexical selection, preventing metadata from
    // turning a symlink alias into ownership authority.
    let real_manifest = manifest.canonicalize().map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestMissing,
            module_source.as_str(),
            "selected package manifest cannot be resolved",
        )
    })?;
    let real_package_root = package_root.canonicalize().map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OutputSymlinkEscape,
            module_source.as_str(),
            "selected package root cannot be resolved",
        )
    })?;
    if !real_manifest.starts_with(&root)
        || !real_package_root.starts_with(&root)
        || real_package_root != source.canonicalize().unwrap_or(source)
    {
        return Err(diagnostic(
            EngineDiagnosticCode::OutputSymlinkEscape,
            module_source.as_str(),
            "selected Cargo package resolves outside the operation root",
        ));
    }

    Ok(CargoPackage {
        package_id: stable(&package.id, "cargo-metadata.package-id")?,
        name: stable(&package.name, "cargo-metadata.package-name")?,
        manifest_path: relative(&root, manifest)?,
        package_root: relative(&root, package_root)?,
    })
}

/// Selects through metadata when a manifest exists, otherwise plans one exact package.
pub fn select_or_create_package(
    metadata: Option<&CargoMetadataV1>,
    operation_root: &Path,
    module_source: &RelativeOperationPath,
    manifest_hint: Option<&RelativeOperationPath>,
) -> Result<PackageSelection, EngineDiagnostic> {
    match metadata {
        Some(metadata) => select_package(metadata, operation_root, module_source, manifest_hint)
            .map(PackageSelection::Existing),
        None if manifest_hint.is_none() => {
            let manifest_path =
                RelativeOperationPath::parse(&format!("{}/Cargo.toml", module_source.as_str()))
                    .map_err(|_| {
                        diagnostic(
                            EngineDiagnosticCode::OutputPathEscape,
                            module_source.as_str(),
                            "new package manifest path is not canonical",
                        )
                    })?;
            Ok(PackageSelection::Create {
                manifest_path,
                package_root: module_source.clone(),
            })
        }
        None => Err(diagnostic(
            EngineDiagnosticCode::CargoManifestMissing,
            manifest_hint.expect("guarded Some").as_str(),
            "explicit Cargo manifest hint does not exist",
        )),
    }
}

/// Returns the only approved discovery argument vector.
#[must_use]
pub fn metadata_arguments(manifest: &RelativeOperationPath, locked: bool) -> Vec<String> {
    let mut arguments = vec![
        "metadata".to_owned(),
        "--format-version".to_owned(),
        "1".to_owned(),
    ];
    if !locked {
        arguments.push("--no-deps".to_owned());
    } else {
        arguments.push("--locked".to_owned());
    }
    arguments.extend(["--manifest-path".to_owned(), manifest.as_str().to_owned()]);
    arguments
}

/// Reads the nearest exact toolchain declaration in package-to-workspace precedence.
pub fn toolchain_declarations(
    root: &OperationRoot,
    module_root: &RelativeOperationPath,
) -> Result<Vec<(RelativeOperationPath, Vec<u8>)>, EngineDiagnostic> {
    let mut current = Some(module_root.as_str().to_owned());
    while let Some(directory) = current {
        let mut level = Vec::new();
        for name in ["rust-toolchain.toml", "rust-toolchain"] {
            let spelling = if directory.is_empty() {
                name.to_owned()
            } else {
                format!("{directory}/{name}")
            };
            let path = RelativeOperationPath::parse(&spelling).map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    module_root.as_str(),
                    "toolchain search path is not canonical",
                )
            })?;
            if root.exists(&path) {
                level.push((path.clone(), root.read(&path)?));
            }
        }
        if level.len() > 1 {
            return Err(diagnostic(
                EngineDiagnosticCode::ToolchainNonReproducible,
                &directory,
                "multiple toolchain declarations have equal precedence",
            ));
        }
        if !level.is_empty() {
            return Ok(level);
        }
        current = directory
            .rsplit_once('/')
            .map(|(parent, _)| parent.to_owned())
            .or_else(|| (!directory.is_empty()).then(String::new));
    }
    Ok(Vec::new())
}

/// Runs bounded no-dependency metadata and returns a validated discovery typestate.
pub async fn discover_project(
    root: &OperationRoot,
    module_source: &RelativeOperationPath,
    manifest: &RelativeOperationPath,
    toolchains: &[ToolchainDeclaration<'_>],
    cancel: &Cancellation,
) -> Result<DiscoveredCargoProject, EngineDiagnostic> {
    root.regular_file(manifest).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestMissing,
            manifest.as_str(),
            "selected Cargo manifest is missing or not regular",
        )
    })?;
    let outcome = execute_fixed_structured_stdout(
        root,
        &CommandSpec {
            executable: "/usr/local/cargo/bin/cargo",
            arguments: metadata_arguments(manifest, false),
        },
        &current_allowlisted_environment(),
        cancel,
        EngineDiagnosticCode::DependencyResolutionFailed,
        "cargo-metadata",
    )
    .await?;
    if !outcome.success {
        return Err(diagnostic(
            EngineDiagnosticCode::DependencyResolutionFailed,
            "cargo-metadata",
            "Cargo metadata failed while resolving the selected local workspace",
        ));
    }
    if outcome.truncated {
        return Err(diagnostic(
            EngineDiagnosticCode::CargoManifestInvalid,
            "cargo-metadata",
            "Cargo metadata exceeds its process-output bound",
        ));
    }
    let metadata = decode_metadata(&outcome.stdout)?;
    // Both directories influence subsequent command scope, so a caller Cargo config
    // cannot redirect either one outside the mounted operation capability.
    let workspace_root = root.relative(&metadata.workspace_root)?;
    let _target_directory = root.relative(&metadata.target_directory)?;
    let target_package = select_package(&metadata, root.absolute(), module_source, Some(manifest))?;
    let lockfile_candidate =
        RelativeOperationPath::parse(&format!("{}/Cargo.lock", workspace_root.as_str())).map_err(
            |_| {
                diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    "cargo-metadata.workspace-root",
                    "workspace lockfile path is not canonical",
                )
            },
        )?;
    let lockfile = root
        .exists(&lockfile_candidate)
        .then_some(lockfile_candidate);
    Ok(DiscoveredCargoProject {
        workspace_root,
        target_package,
        lockfile,
        toolchain: select_toolchain(toolchains)?,
    })
}

/// Promotes discovery only after every checked-runtime prerequisite is proved.
pub fn promote_runtime_project(
    root: &OperationRoot,
    discovered: DiscoveredCargoProject,
    target_binary: CargoTarget,
    toolchain: ExactRustToolchain,
    manifest: &OperationManifest,
) -> Result<RuntimeCargoProject, EngineDiagnostic> {
    let lockfile = discovered.lockfile.clone().ok_or_else(|| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestMissing,
            "Cargo.lock",
            "checked runtime requires a committed lockfile",
        )
    })?;
    root.regular_file(&lockfile).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestMissing,
            lockfile.as_str(),
            "checked runtime lockfile is missing or not regular",
        )
    })?;
    let selected_toolchain = match &discovered.toolchain {
        ToolchainSelection::Declared { toolchain, .. }
        | ToolchainSelection::TargetDefault { toolchain } => toolchain,
    };
    if selected_toolchain != &toolchain || manifest.target.rust_toolchain != toolchain {
        return Err(diagnostic(
            EngineDiagnosticCode::ToolchainNonReproducible,
            "runtime.toolchain",
            "runtime toolchain differs from discovery or the operation manifest",
        ));
    }
    let target_record = manifest.artifacts.get(&target_binary.source_path);
    if target_binary.name.as_str() != "dagger-module"
        || target_record.is_none_or(|record| record.ownership != ArtifactOwnership::Generator)
    {
        return Err(diagnostic(
            EngineDiagnosticCode::OwnershipConflict,
            "runtime.target.dagger-module",
            "runtime binary target lacks generated-manifest ownership",
        ));
    }
    let target_source = root.read(&target_binary.source_path).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OwnershipConflict,
            target_binary.source_path.as_str(),
            "runtime binary source is missing or not regular",
        )
    })?;
    if hash(&target_source) != target_record.expect("validated Some record").digest {
        return Err(diagnostic(
            EngineDiagnosticCode::OperationManifestStale,
            target_binary.source_path.as_str(),
            "runtime binary source differs from its operation-manifest digest",
        ));
    }
    let operation_manifest_digest = canonical_digest(DigestDomain::OperationManifest, manifest)
        .map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OperationManifestStale,
                "operation-manifest",
                "runtime operation manifest cannot be canonically identified",
            )
        })?;
    Ok(RuntimeCargoProject {
        discovered,
        target_binary,
        lockfile,
        toolchain,
        operation_manifest_digest,
    })
}

fn hash(bytes: &[u8]) -> Sha256Digest {
    format!("sha256:{:x}", Sha256::digest(bytes))
        .parse()
        .expect("SHA-256 formatting must satisfy the digest scalar")
}

fn relative(root: &Path, path: &Path) -> Result<RelativeOperationPath, EngineDiagnostic> {
    let path = path.strip_prefix(root).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OutputPathEscape,
            "cargo-metadata.path",
            "Cargo path is outside the operation root",
        )
    })?;
    let spelling = path
        .components()
        .map(|component| component.as_os_str().to_string_lossy())
        .collect::<Vec<_>>()
        .join("/");
    RelativeOperationPath::parse(&spelling).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OutputPathEscape,
            "cargo-metadata.path",
            "Cargo path is not canonical",
        )
    })
}

fn stable(value: &str, coordinate: &str) -> Result<StableCoordinate, EngineDiagnostic> {
    value.parse().map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestInvalid,
            coordinate,
            "Cargo metadata coordinate is empty or contains control characters",
        )
    })
}

fn lexical_normalize(path: &Path) -> Option<PathBuf> {
    if !path.is_absolute() {
        return None;
    }
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            std::path::Component::Prefix(prefix) => normalized.push(prefix.as_os_str()),
            std::path::Component::RootDir => normalized.push(Path::new("/")),
            std::path::Component::Normal(value) => normalized.push(value),
            std::path::Component::CurDir => {}
            std::path::Component::ParentDir => {
                normalized.pop();
            }
        }
    }
    Some(normalized)
}

fn diagnostic(code: EngineDiagnosticCode, coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}
