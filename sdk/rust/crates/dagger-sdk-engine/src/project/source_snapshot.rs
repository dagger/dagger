//! Confined filesystem adapter for immutable module-authoring source snapshots.
//!
//! The adapter runs only after Cargo metadata selected exactly one package. It reads
//! regular UTF-8 source under that package, rejects symlinks and nested package
//! authority, applies explicit bounds, and hands the pure compiler a host-path-free
//! value. It never starts Cargo, rustc, a build script, user code, a network request,
//! or a Dagger engine.

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use dagger_codegen::module::{
    CfgEnvironment, FormatVersion, ModulePackage, ModuleSourcePath, ModuleSourceSnapshot,
    PackageName, Sha256Digest as ModuleDigest, SourceDocument, TargetValue, source_snapshot_digest,
};

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::{CargoPackage, OperationRoot, RelativeOperationPath};

/// Default maximum number of source documents in one selected package.
pub const MAX_SOURCE_FILES: usize = 4_096;
/// Default maximum bytes in one manifest or Rust source document.
pub const MAX_SOURCE_FILE_BYTES: u64 = 2 * 1024 * 1024;
/// Default maximum aggregate bytes in one immutable source snapshot.
pub const MAX_SOURCE_TOTAL_BYTES: u64 = 32 * 1024 * 1024;

const EXCLUDED_DIRECTORIES: &[&str] = &[".git", ".hg", ".svn", "target", "dagger_generated"];

/// Explicit resource limits for confined source capture.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SourceSnapshotLimits {
    /// Maximum admitted document count, including the selected manifest.
    pub max_files: usize,
    /// Maximum bytes admitted from one document.
    pub max_file_bytes: u64,
    /// Maximum bytes admitted across the complete snapshot.
    pub max_total_bytes: u64,
}

impl Default for SourceSnapshotLimits {
    fn default() -> Self {
        Self {
            max_files: MAX_SOURCE_FILES,
            max_file_bytes: MAX_SOURCE_FILE_BYTES,
            max_total_bytes: MAX_SOURCE_TOTAL_BYTES,
        }
    }
}

/// Pure configuration supplied by the already-selected Cargo package operation.
pub struct SourceSnapshotRequest<'a> {
    /// Unique package selected through Cargo metadata.
    pub package: &'a CargoPackage,
    /// Selected library or binary crate root beneath the operation root.
    pub crate_root: &'a RelativeOperationPath,
    /// Exact package edition used by Cargo/rustc.
    pub edition: &'a str,
    /// Explicit target/features/custom cfg environment.
    pub cfg: CfgEnvironment,
}

/// Builder that owns the sole filesystem read boundary for authoring discovery.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SourceSnapshotBuilder {
    limits: SourceSnapshotLimits,
}

impl SourceSnapshotBuilder {
    /// Uses the reviewed production source bounds.
    #[must_use]
    pub fn new() -> Self {
        Self {
            limits: SourceSnapshotLimits::default(),
        }
    }

    /// Uses explicit limits, primarily for bounded model and rejection tests.
    pub fn with_limits(limits: SourceSnapshotLimits) -> Result<Self, EngineDiagnostic> {
        if limits.max_files == 0 || limits.max_file_bytes == 0 || limits.max_total_bytes == 0 {
            return Err(diagnostic(
                EngineDiagnosticCode::OperationInputInvalid,
                "source-snapshot-limits",
                "source snapshot limits must be non-zero",
            ));
        }
        Ok(Self { limits })
    }

    /// Captures one selected package without executing any authored or external code.
    pub fn build(
        &self,
        root: &OperationRoot,
        request: SourceSnapshotRequest<'_>,
    ) -> Result<ModuleSourceSnapshot, EngineDiagnostic> {
        let package_root = root.resolve_existing(&request.package.package_root)?;
        if !fs::metadata(&package_root).is_ok_and(|metadata| metadata.is_dir()) {
            return Err(diagnostic(
                EngineDiagnosticCode::CargoPackageMissing,
                request.package.package_root.as_str(),
                "selected Cargo package root is not a directory",
            ));
        }
        let manifest = root.regular_file(&request.package.manifest_path)?;
        if manifest.parent() != Some(package_root.as_path()) {
            return Err(diagnostic(
                EngineDiagnosticCode::CargoManifestInvalid,
                request.package.manifest_path.as_str(),
                "selected manifest is not owned directly by the selected package",
            ));
        }
        let crate_root = root.regular_file(request.crate_root)?;
        if !crate_root.starts_with(&package_root) {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                request.crate_root.as_str(),
                "selected crate root is outside the selected package",
            ));
        }

        let mut paths = vec![manifest];
        collect_source_files(&package_root, &package_root, &mut paths)?;
        paths.sort();
        paths.dedup();
        if paths.len() > self.limits.max_files {
            return Err(diagnostic(
                EngineDiagnosticCode::OperationInputInvalid,
                request.package.package_root.as_str(),
                "selected package exceeds the source file-count bound",
            ));
        }

        let mut total = 0_u64;
        let mut documents = BTreeMap::new();
        for path in paths {
            let metadata = fs::symlink_metadata(&path).map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    request.package.package_root.as_str(),
                    "source input metadata is unavailable",
                )
            })?;
            if metadata.file_type().is_symlink() || !metadata.is_file() {
                return Err(diagnostic(
                    EngineDiagnosticCode::OutputSymlinkEscape,
                    request.package.package_root.as_str(),
                    "source snapshots admit only real regular files",
                ));
            }
            if metadata.len() > self.limits.max_file_bytes {
                return Err(diagnostic(
                    EngineDiagnosticCode::OperationInputInvalid,
                    request.package.package_root.as_str(),
                    "one source input exceeds the per-file byte bound",
                ));
            }
            total = total.checked_add(metadata.len()).ok_or_else(|| {
                diagnostic(
                    EngineDiagnosticCode::OperationInputInvalid,
                    request.package.package_root.as_str(),
                    "source snapshot byte accounting overflowed",
                )
            })?;
            if total > self.limits.max_total_bytes {
                return Err(diagnostic(
                    EngineDiagnosticCode::OperationInputInvalid,
                    request.package.package_root.as_str(),
                    "selected package exceeds the aggregate source byte bound",
                ));
            }
            let bytes = fs::read(&path).map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    request.package.package_root.as_str(),
                    "source input could not be read",
                )
            })?;
            let contents = String::from_utf8(bytes).map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OperationInputInvalid,
                    request.package.package_root.as_str(),
                    "module authoring source must be UTF-8",
                )
            })?;
            let relative = package_relative(&package_root, &path)?;
            let source_path = ModuleSourcePath::new(relative).map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    request.package.package_root.as_str(),
                    "source input path is not canonical and package-relative",
                )
            })?;
            documents.insert(
                source_path.clone(),
                SourceDocument::new(source_path, contents),
            );
        }

        let crate_root = package_relative(&package_root, &crate_root)?;
        let mut snapshot = ModuleSourceSnapshot {
            format_version: FormatVersion::current(),
            package: ModulePackage {
                name: PackageName::new(request.package.name.as_str()).map_err(|_| {
                    diagnostic(
                        EngineDiagnosticCode::CargoManifestInvalid,
                        request.package.manifest_path.as_str(),
                        "selected package name is not valid for module authoring",
                    )
                })?,
                crate_root: ModuleSourcePath::new(crate_root).map_err(|_| {
                    diagnostic(
                        EngineDiagnosticCode::OutputPathEscape,
                        request.crate_root.as_str(),
                        "selected crate root is not canonical and package-relative",
                    )
                })?,
                edition: TargetValue::new(request.edition).map_err(|_| {
                    diagnostic(
                        EngineDiagnosticCode::CargoManifestInvalid,
                        request.package.manifest_path.as_str(),
                        "selected package edition is invalid",
                    )
                })?,
            },
            cfg: request.cfg,
            documents,
            digest: ModuleDigest::hash_bytes(b"pending-source-snapshot"),
        };
        snapshot.digest = source_snapshot_digest(&snapshot).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OperationInputInvalid,
                request.package.package_root.as_str(),
                "source snapshot could not be canonically identified",
            )
        })?;
        Ok(snapshot)
    }
}

impl Default for SourceSnapshotBuilder {
    fn default() -> Self {
        Self::new()
    }
}

fn collect_source_files(
    package_root: &Path,
    directory: &Path,
    paths: &mut Vec<PathBuf>,
) -> Result<(), EngineDiagnostic> {
    let mut entries = fs::read_dir(directory)
        .map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "source-snapshot",
                "selected package directory could not be enumerated",
            )
        })?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "source-snapshot",
                "selected package directory entry is unavailable",
            )
        })?;
    entries.sort_by_key(fs::DirEntry::file_name);
    for entry in entries {
        let path = entry.path();
        let metadata = fs::symlink_metadata(&path).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "source-snapshot",
                "selected package entry metadata is unavailable",
            )
        })?;
        if metadata.file_type().is_symlink() {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputSymlinkEscape,
                "source-snapshot",
                "symlink entries are not admitted during source capture",
            ));
        }
        if metadata.is_dir() {
            let name = entry.file_name();
            let name = name.to_string_lossy();
            if EXCLUDED_DIRECTORIES.contains(&name.as_ref()) {
                continue;
            }
            // A nested manifest owns a different package and cannot extend the selected
            // package's discovery authority merely by being physically beneath it.
            if path != package_root && path.join("Cargo.toml").is_file() {
                continue;
            }
            collect_source_files(package_root, &path, paths)?;
        } else if metadata.is_file() && path.extension().is_some_and(|extension| extension == "rs")
        {
            paths.push(path);
        }
    }
    Ok(())
}

fn package_relative(root: &Path, path: &Path) -> Result<String, EngineDiagnostic> {
    let relative = path.strip_prefix(root).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OutputPathEscape,
            "source-snapshot",
            "source input resolves outside the selected package",
        )
    })?;
    let spelling = relative
        .components()
        .map(|component| component.as_os_str().to_string_lossy())
        .collect::<Vec<_>>()
        .join("/");
    if spelling.is_empty() {
        return Err(diagnostic(
            EngineDiagnosticCode::OutputPathEscape,
            "source-snapshot",
            "source input path must identify one package-relative file",
        ));
    }
    Ok(spelling)
}

fn diagnostic(code: EngineDiagnosticCode, coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}

#[cfg(test)]
mod tests {
    use std::collections::{BTreeMap, BTreeSet};
    use std::fs;

    use proptest::prelude::*;
    use tempfile::TempDir;

    use super::*;
    use crate::{RelativeOperationPath, StableCoordinate};

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(128))]

        // Snapshot construction admits only the selected bounded package and is byte deterministic.
        #[test]
        fn confined_snapshot_is_deterministic_and_excludes_build_state(
            extra_files in 0_usize..8,
            reverse in any::<bool>(),
        ) {
            let fixture = Fixture::new(extra_files, reverse);
            let first = fixture.build().unwrap();
            let second = fixture.build().unwrap();
            prop_assert_eq!(&first, &second);
            prop_assert!(first.documents.keys().all(|path| !path.as_str().starts_with("target/")));
            prop_assert_eq!(first.documents.len(), extra_files + 2);
        }
    }

    #[cfg(unix)]
    #[test]
    fn symlinked_source_is_rejected_before_read() {
        use std::os::unix::fs::symlink;

        let fixture = Fixture::new(0, false);
        symlink("lib.rs", fixture.package.join("src/alias.rs")).expect("fixture symlink");
        let error = fixture.build().unwrap_err();
        assert_eq!(error.code, EngineDiagnosticCode::OutputSymlinkEscape);
    }

    struct Fixture {
        _temporary: TempDir,
        operation: PathBuf,
        package: PathBuf,
    }

    impl Fixture {
        fn new(extra_files: usize, reverse: bool) -> Self {
            let temporary = tempfile::tempdir().expect("fixture tempdir");
            let operation = temporary.path().join("operation");
            let package = operation.join("module");
            fs::create_dir_all(package.join("src")).expect("fixture source directory");
            fs::create_dir_all(package.join("target/debug")).expect("fixture target directory");
            fs::write(
                package.join("Cargo.toml"),
                "[package]\nname = \"fixture\"\nedition = \"2024\"\n",
            )
            .expect("fixture manifest");
            fs::write(
                package.join("src/lib.rs"),
                "#[dagger_sdk::object(root)] pub struct Root;",
            )
            .expect("fixture root");
            fs::write(package.join("target/debug/generated.rs"), "secret")
                .expect("excluded fixture");
            let order: Box<dyn Iterator<Item = usize>> = if reverse {
                Box::new((0..extra_files).rev())
            } else {
                Box::new(0..extra_files)
            };
            for index in order {
                fs::write(
                    package.join(format!("src/extra_{index}.rs")),
                    "pub struct Extra;",
                )
                .expect("fixture extra source");
            }
            Self {
                _temporary: temporary,
                operation,
                package,
            }
        }

        fn build(&self) -> Result<ModuleSourceSnapshot, EngineDiagnostic> {
            let root = OperationRoot::open(&self.operation)?;
            SourceSnapshotBuilder::new().build(
                &root,
                SourceSnapshotRequest {
                    package: &CargoPackage {
                        package_id: StableCoordinate::new("fixture-id")
                            .expect("fixture package id"),
                        name: StableCoordinate::new("fixture").expect("fixture package name"),
                        manifest_path: relative_path("module/Cargo.toml"),
                        package_root: relative_path("module"),
                    },
                    crate_root: &relative_path("module/src/lib.rs"),
                    edition: "2024",
                    cfg: CfgEnvironment {
                        values: BTreeMap::from([("unix".to_owned(), BTreeSet::new())]),
                        features: BTreeSet::new(),
                    },
                },
            )
        }
    }

    fn relative_path(value: &str) -> RelativeOperationPath {
        RelativeOperationPath::parse(value).expect("fixture path is canonical")
    }
}
