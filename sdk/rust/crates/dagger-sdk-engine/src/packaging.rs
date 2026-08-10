//! Hermetic packaged-content assembly and shipped security-graph derivation.
//!
//! The Go engine builder owns Dagger object-graph composition, while this module owns
//! the content policy it invokes: every payload byte is hashed beneath one explicit
//! root, the acyclic asset manifest is written before its descriptor, and the complete
//! shipped graph is derived from the resulting assets. Build metadata never authorizes
//! a path outside that root or substitutes a private crate for the public SDK.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Path, PathBuf};

use sha2::{Digest as _, Sha256};

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::{
    CanonicalRepositoryUrl, DigestDomain, EngineSourceDescriptor, ExactRustToolchain, ExactVersion,
    FormatVersion, FullRevision, PackagedAsset, PackagedAssetManifest, PublishedSdkDependency,
    RelativeOperationPath, Sha256Digest, StableCoordinate, canonical_bytes, canonical_digest,
};

/// Canonical descriptor path beneath packaged Rust SDK content.
pub const DESCRIPTOR_PATH: &str = "dist/engine-source.json";
/// Canonical packaged-asset manifest path beneath Rust SDK content.
pub const PACKAGED_ASSET_MANIFEST_PATH: &str = "dist/packaged-assets.json";

const REQUIRED_PAYLOADS: [&str; 11] = [
    "LICENSE",
    "dist/client-generation.json",
    "dist/dagger-rust-engine",
    "runtime/dagger.gen.go",
    "runtime/dagger-module.toml",
    "runtime/go.mod",
    "runtime/go.sum",
    "runtime/internal/dagger/dagger.gen.go",
    "runtime/internal/dagger/rust-sdk.gen.go",
    "runtime/internal/metadata/client_generation.go",
    "runtime/main.go",
];

/// Immutable coordinates supplied by the engine build before payload hashing.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PackageIdentity {
    /// Credential-free immutable repository identity.
    pub repository: CanonicalRepositoryUrl,
    /// Full source revision containing the packaged SDK.
    pub dagger_revision: FullRevision,
    /// Exact compatible engine version without a leading `v`.
    pub engine_version: ExactVersion,
    /// Exact public Rust SDK version.
    pub rust_sdk_version: ExactVersion,
    /// Exact compiler used for the private operation executable.
    pub rust_toolchain: ExactRustToolchain,
    /// Public dependency rendered into generated Cargo projects.
    pub sdk_dependency: PublishedSdkDependency,
    /// Checked target core-schema identity.
    pub core_schema_digest: Sha256Digest,
}

/// Stable class of one subject reachable from the Rust SDK distribution.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum SecuritySubjectKind {
    /// The one public Cargo package consumed by generated projects.
    PublishableCrate,
    /// A private Cargo workspace build input.
    PrivateCrate,
    /// The module-backed Go ABI adapter.
    GoRuntimeAdapter,
    /// The private packaged operation executable.
    PackagedBinary,
    /// Another immutable content asset.
    PackagedAsset,
    /// A digest-pinned build image or toolchain.
    ToolchainImage,
}

/// One node in the derived shipped security graph.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SecuritySubject {
    /// Stable graph identity.
    pub id: StableCoordinate,
    /// Audit policy applied to this subject.
    pub kind: SecuritySubjectKind,
}

/// Closed dependency graph used to prove repository security-input coverage.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SecurityAuditGraph {
    subjects: BTreeMap<StableCoordinate, SecuritySubject>,
    edges: BTreeMap<StableCoordinate, BTreeSet<StableCoordinate>>,
    roots: BTreeSet<StableCoordinate>,
}

impl SecurityAuditGraph {
    /// Constructs a graph from explicit shipped subjects, dependency edges, and roots.
    ///
    /// Construction deliberately retains edges to missing subjects so [`Self::reachable`]
    /// can diagnose an incomplete derived graph instead of silently pruning it.
    #[must_use]
    pub fn new(
        subjects: BTreeMap<StableCoordinate, SecuritySubject>,
        edges: BTreeMap<StableCoordinate, BTreeSet<StableCoordinate>>,
        roots: BTreeSet<StableCoordinate>,
    ) -> Self {
        Self {
            subjects,
            edges,
            roots,
        }
    }

    /// Returns every subject reachable from the declared distribution roots exactly once.
    pub fn reachable(&self) -> Result<BTreeSet<StableCoordinate>, EngineDiagnostic> {
        let mut pending = self.roots.iter().cloned().collect::<Vec<_>>();
        let mut reachable = BTreeSet::new();
        while let Some(subject) = pending.pop() {
            if !self.subjects.contains_key(&subject) {
                return Err(packaging_error(
                    EngineDiagnosticCode::SecurityAuditIncomplete,
                    subject.as_str(),
                    "security graph references an undeclared shipped subject",
                ));
            }
            if !reachable.insert(subject.clone()) {
                continue;
            }
            if let Some(dependencies) = self.edges.get(&subject) {
                pending.extend(dependencies.iter().cloned());
            }
        }
        Ok(reachable)
    }

    /// Requires the locked repository audit inputs to cover the complete reachable graph.
    pub fn validate_inputs(
        &self,
        audited: &BTreeSet<StableCoordinate>,
    ) -> Result<(), EngineDiagnostic> {
        let reachable = self.reachable()?;
        if let Some(missing) = reachable.difference(audited).next() {
            return Err(packaging_error(
                EngineDiagnosticCode::SecurityAuditIncomplete,
                missing.as_str(),
                "shipped subject is absent from the locked security audit inputs",
            ));
        }
        Ok(())
    }

    /// Borrows the derived subject map for inspection and evidence production.
    #[must_use]
    pub const fn subjects(&self) -> &BTreeMap<StableCoordinate, SecuritySubject> {
        &self.subjects
    }
}

/// Hashes a complete content root, writes its acyclic manifest, then writes the bound descriptor.
pub fn build_packaged_content(
    root: &Path,
    identity: PackageIdentity,
) -> Result<(PackagedAssetManifest, EngineSourceDescriptor), EngineDiagnostic> {
    let root = canonical_directory(root)?;
    let assets = collect_assets(&root)?;
    let manifest = PackagedAssetManifest {
        format_version: FormatVersion,
        assets,
    };
    validate_asset_manifest(&manifest)?;
    let manifest_digest =
        canonical_digest(DigestDomain::PackagedAssets, &manifest).map_err(|_| {
            packaging_error(
                EngineDiagnosticCode::PackagedAssetInvalid,
                "packaged-assets",
                "packaged asset manifest could not be hashed",
            )
        })?;
    let descriptor = EngineSourceDescriptor {
        format_version: FormatVersion,
        repository: identity.repository,
        dagger_revision: identity.dagger_revision,
        engine_version: identity.engine_version,
        rust_sdk_version: identity.rust_sdk_version,
        rust_toolchain: identity.rust_toolchain,
        sdk_dependency: identity.sdk_dependency,
        core_schema_digest: identity.core_schema_digest,
        packaged_asset_manifest_digest: manifest_digest,
    };
    validate_packaged_source(&manifest, &descriptor)?;
    write_canonical(&root, PACKAGED_ASSET_MANIFEST_PATH, &manifest)?;
    write_canonical(&root, DESCRIPTOR_PATH, &descriptor)?;
    Ok((manifest, descriptor))
}

/// Verifies the acyclic asset inventory and its exact descriptor binding.
pub fn validate_packaged_source(
    manifest: &PackagedAssetManifest,
    descriptor: &EngineSourceDescriptor,
) -> Result<(), EngineDiagnostic> {
    validate_asset_manifest(manifest)?;
    descriptor.validate()?;
    let observed = canonical_digest(DigestDomain::PackagedAssets, manifest).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            "packaged-assets",
            "packaged asset manifest identity could not be computed",
        )
    })?;
    if observed != descriptor.packaged_asset_manifest_digest {
        return Err(packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            "descriptor.packaged_asset_manifest_digest",
            "engine source descriptor differs from the packaged asset inventory",
        ));
    }
    Ok(())
}

/// Derives every Rust distribution subject from the actual packaged asset manifest.
pub fn derive_shipped_audit_graph(
    manifest: &PackagedAssetManifest,
) -> Result<SecurityAuditGraph, EngineDiagnostic> {
    validate_asset_manifest(manifest)?;
    let distribution = coordinate("distribution:rust-sdk")?;
    let mut subjects = BTreeMap::new();
    let mut distribution_edges = BTreeSet::new();

    for (id, kind) in [
        ("cargo:dagger-sdk", SecuritySubjectKind::PublishableCrate),
        ("cargo:dagger-codegen", SecuritySubjectKind::PrivateCrate),
        ("cargo:dagger-bootstrap", SecuritySubjectKind::PrivateCrate),
        ("cargo:dagger-sdk-engine", SecuritySubjectKind::PrivateCrate),
        ("go:sdk/rust/runtime", SecuritySubjectKind::GoRuntimeAdapter),
        ("image:rust-1.97.1", SecuritySubjectKind::ToolchainImage),
    ] {
        let id = coordinate(id)?;
        subjects.insert(
            id.clone(),
            SecuritySubject {
                id: id.clone(),
                kind,
            },
        );
        distribution_edges.insert(id);
    }

    for asset in manifest.assets.values() {
        let id = coordinate(&format!("asset:{}", asset.path))?;
        let kind = if asset.path.as_str() == "dist/dagger-rust-engine" {
            SecuritySubjectKind::PackagedBinary
        } else {
            SecuritySubjectKind::PackagedAsset
        };
        subjects.insert(
            id.clone(),
            SecuritySubject {
                id: id.clone(),
                kind,
            },
        );
        distribution_edges.insert(id);
    }

    subjects.insert(
        distribution.clone(),
        SecuritySubject {
            id: distribution.clone(),
            kind: SecuritySubjectKind::PackagedAsset,
        },
    );
    let mut edges = BTreeMap::new();
    edges.insert(distribution.clone(), distribution_edges);
    let mut roots = BTreeSet::new();
    roots.insert(distribution);
    Ok(SecurityAuditGraph::new(subjects, edges, roots))
}

/// Enforces the public/private crate boundary over the derived distribution graph.
pub fn validate_packaged_distribution(
    manifest: &PackagedAssetManifest,
    publishable_crates: &BTreeSet<StableCoordinate>,
    generated_dependency: &PublishedSdkDependency,
) -> Result<SecurityAuditGraph, EngineDiagnostic> {
    let expected = BTreeSet::from([coordinate("cargo:dagger-sdk")?]);
    if publishable_crates != &expected {
        return Err(packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            "cargo-publication",
            "dagger-sdk must be the sole publishable Rust workspace crate",
        ));
    }
    match generated_dependency {
        PublishedSdkDependency::Registry { package, .. }
        | PublishedSdkDependency::Git { package, .. }
            if package.as_str() == "dagger-sdk" => {}
        _ => {
            return Err(packaging_error(
                EngineDiagnosticCode::PackagedAssetInvalid,
                "generated-dependency",
                "generated projects may depend only on the public dagger-sdk crate",
            ));
        }
    }
    derive_shipped_audit_graph(manifest)
}

fn canonical_directory(root: &Path) -> Result<PathBuf, EngineDiagnostic> {
    let metadata = fs::symlink_metadata(root).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            "content-root",
            "packaged content root is missing",
        )
    })?;
    if metadata.file_type().is_symlink() || !metadata.is_dir() {
        return Err(packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            "content-root",
            "packaged content root must be a non-symlink directory",
        ));
    }
    fs::canonicalize(root).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            "content-root",
            "packaged content root could not be resolved",
        )
    })
}

fn collect_assets(
    root: &Path,
) -> Result<BTreeMap<RelativeOperationPath, PackagedAsset>, EngineDiagnostic> {
    let mut pending = vec![root.to_path_buf()];
    let mut assets = BTreeMap::new();
    while let Some(directory) = pending.pop() {
        let entries = fs::read_dir(&directory).map_err(|_| {
            packaging_error(
                EngineDiagnosticCode::PackagedAssetInvalid,
                "content-root",
                "packaged content directory could not be enumerated",
            )
        })?;
        for entry in entries {
            let entry = entry.map_err(|_| {
                packaging_error(
                    EngineDiagnosticCode::PackagedAssetInvalid,
                    "content-root",
                    "packaged content entry could not be enumerated",
                )
            })?;
            let path = entry.path();
            let metadata = fs::symlink_metadata(&path).map_err(|_| {
                packaging_error(
                    EngineDiagnosticCode::PackagedAssetInvalid,
                    "content-root",
                    "packaged content metadata could not be read",
                )
            })?;
            if metadata.file_type().is_symlink() {
                return Err(packaging_error(
                    EngineDiagnosticCode::PackagedAssetInvalid,
                    "content-root",
                    "packaged content must not contain symlinks",
                ));
            }
            if metadata.is_dir() {
                pending.push(path);
                continue;
            }
            if !metadata.is_file() {
                return Err(packaging_error(
                    EngineDiagnosticCode::PackagedAssetInvalid,
                    "content-root",
                    "packaged content may contain only regular files",
                ));
            }
            let relative = path.strip_prefix(root).map_err(|_| {
                packaging_error(
                    EngineDiagnosticCode::PackagedAssetInvalid,
                    "content-root",
                    "packaged content entry escaped its root",
                )
            })?;
            let spelling = relative
                .components()
                .map(|component| component.as_os_str().to_string_lossy())
                .collect::<Vec<_>>()
                .join("/");
            if matches!(
                spelling.as_str(),
                DESCRIPTOR_PATH | PACKAGED_ASSET_MANIFEST_PATH
            ) {
                continue;
            }
            let relative = RelativeOperationPath::parse(&spelling).map_err(|_| {
                packaging_error(
                    EngineDiagnosticCode::PackagedAssetInvalid,
                    "content-root",
                    "packaged content path is not canonical",
                )
            })?;
            let bytes = fs::read(&path).map_err(|_| {
                packaging_error(
                    EngineDiagnosticCode::PackagedAssetInvalid,
                    relative.as_str(),
                    "packaged asset could not be read",
                )
            })?;
            let asset = PackagedAsset {
                path: relative.clone(),
                digest: bytes_digest(&bytes),
                executable: is_executable(&metadata),
            };
            assets.insert(relative, asset);
        }
    }
    Ok(assets)
}

fn validate_asset_manifest(manifest: &PackagedAssetManifest) -> Result<(), EngineDiagnostic> {
    for required in REQUIRED_PAYLOADS {
        let required =
            RelativeOperationPath::parse(required).expect("required paths are canonical");
        if !manifest.assets.contains_key(&required) {
            return Err(packaging_error(
                EngineDiagnosticCode::PackagedAssetInvalid,
                required.as_str(),
                "required packaged Rust SDK asset is missing",
            ));
        }
    }
    for (path, asset) in &manifest.assets {
        if path != &asset.path {
            return Err(packaging_error(
                EngineDiagnosticCode::PackagedAssetInvalid,
                path.as_str(),
                "packaged asset map key differs from its recorded path",
            ));
        }
        if matches!(
            path.as_str(),
            DESCRIPTOR_PATH | PACKAGED_ASSET_MANIFEST_PATH
        ) {
            return Err(packaging_error(
                EngineDiagnosticCode::PackagedAssetInvalid,
                path.as_str(),
                "acyclic metadata files must not hash themselves",
            ));
        }
        if path.as_str() == "dist/dagger-rust-engine" && !asset.executable {
            return Err(packaging_error(
                EngineDiagnosticCode::PackagedAssetInvalid,
                path.as_str(),
                "packaged Rust operation tool must be executable",
            ));
        }
    }
    Ok(())
}

fn write_canonical<T: serde::Serialize>(
    root: &Path,
    relative: &str,
    value: &T,
) -> Result<(), EngineDiagnostic> {
    let destination = root.join(relative);
    let parent = destination.parent().expect("metadata paths have a parent");
    fs::create_dir_all(parent).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            relative,
            "metadata parent could not be created",
        )
    })?;
    let bytes = canonical_bytes(value).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            relative,
            "packaged metadata could not be encoded",
        )
    })?;
    let temporary = destination.with_extension("json.tmp");
    fs::write(&temporary, bytes).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            relative,
            "packaged metadata could not be staged",
        )
    })?;
    fs::rename(temporary, destination).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            relative,
            "packaged metadata could not be published",
        )
    })
}

fn bytes_digest(bytes: &[u8]) -> Sha256Digest {
    let digest = Sha256::digest(bytes);
    Sha256Digest::new(format!("sha256:{digest:x}"))
        .expect("SHA-256 output always satisfies the digest grammar")
}

#[cfg(unix)]
fn is_executable(metadata: &fs::Metadata) -> bool {
    use std::os::unix::fs::PermissionsExt as _;
    metadata.permissions().mode() & 0o111 != 0
}

#[cfg(not(unix))]
fn is_executable(_metadata: &fs::Metadata) -> bool {
    false
}

fn coordinate(value: &str) -> Result<StableCoordinate, EngineDiagnostic> {
    StableCoordinate::new(value.to_owned()).map_err(|_| {
        packaging_error(
            EngineDiagnosticCode::PackagedAssetInvalid,
            "security-graph",
            "derived security subject is not canonical",
        )
    })
}

fn packaging_error(
    code: EngineDiagnosticCode,
    coordinate: &str,
    message: &str,
) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}
