//! Checked-input generation orchestration.
//!
//! This module is the only path from repository authorities to generated filesystem
//! state. It validates every input before pure projection, finalizes source in private
//! state, computes the complete owned diff, and enters publication only for explicit
//! update mode.

pub mod format;
pub mod publish;

use std::fmt;
use std::fs::{self, File};
use std::io::Read as _;
use std::path::{Component, Path, PathBuf};

use dagger_codegen::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{CoreProjectionRequest, project_core, render_core};
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use sha2::{Digest as _, Sha256};

use self::format::{CandidateFormatter, PinnedRustfmt};
use self::publish::{ArtifactChange, ArtifactManifest, PublicationObserver, Publisher};

const TARGET_INPUT: &str = "codegen/target.json";
const SCHEMA_INPUT: &str = "codegen/schema.json";
/// Repository-relative path of the generated binding manifest.
pub const BINDING_MANIFEST: &str = "codegen/generated.json";

const MAX_JSON_INPUT_BYTES: usize = 64 * 1024 * 1024;

/// Verification or publication mode selected by the maintainer.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum GenerateMode {
    /// Compare the complete candidate without changing the workspace.
    Check,
    /// Publish the complete candidate through the atomic transaction.
    Update,
}

/// Hidden fixture-only path overrides accepted by the typed CLI.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct GenerateOverrides {
    /// Explicit root containing every fixture workspace and override.
    pub fixture_root: Option<PathBuf>,
    /// Exact-target descriptor fixture.
    pub target: Option<PathBuf>,
    /// Canonical schema fixture.
    pub schema: Option<PathBuf>,
    /// Previous generated binding manifest fixture.
    pub manifest: Option<PathBuf>,
}

impl GenerateOverrides {
    fn any_path(&self) -> bool {
        self.target.is_some() || self.schema.is_some() || self.manifest.is_some()
    }
}

/// Complete direct-generation request.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct GenerateRequest {
    /// Rust workspace serving as both checked input root and publication boundary.
    pub workspace: PathBuf,
    /// Whether to compare or explicitly publish.
    pub mode: GenerateMode,
    /// Narrow fixture-only overrides.
    pub overrides: GenerateOverrides,
}

/// Successful generation result.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct GenerateOutcome {
    changes: Vec<ArtifactChange>,
}

impl GenerateOutcome {
    /// Returns changed paths in stable lexical order.
    pub fn changed_paths(&self) -> impl Iterator<Item = &str> {
        self.changes.iter().map(|change| change.path().as_str())
    }

    /// Borrows the complete generated-artifact change set.
    #[must_use]
    pub fn changes(&self) -> &[ArtifactChange] {
        &self.changes
    }
}

/// A normalized repository-relative generated artifact path.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct ArtifactPath(String);

impl ArtifactPath {
    /// Validates a platform-independent repository-relative path.
    pub fn new(value: impl AsRef<str>) -> Result<Self, DiagnosticSet> {
        let value = value.as_ref();
        let path = Path::new(value);
        let valid = !value.is_empty()
            && !value.contains('\\')
            && !value.contains(':')
            && !path.is_absolute()
            && value
                .split('/')
                .all(|component| !component.is_empty() && !matches!(component, "." | ".."))
            && path
                .components()
                .all(|component| matches!(component, Component::Normal(_)));
        if !valid {
            return Err(input_error(
                DiagnosticCode::GeneratedProvenanceInvalid,
                "artifact-path",
                "generated artifact path is not normalized repository-relative data",
            ));
        }
        Ok(Self(value.to_owned()))
    }

    /// Borrows the normalized path.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// Resolves the path below a previously validated workspace.
    #[must_use]
    pub fn resolve(&self, workspace: &Path) -> PathBuf {
        workspace.join(&self.0)
    }
}

impl fmt::Display for ArtifactPath {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl Serialize for ArtifactPath {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for ArtifactPath {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        Self::new(value).map_err(serde::de::Error::custom)
    }
}

#[derive(Clone, Debug)]
struct ResolvedPaths {
    workspace: PathBuf,
    target: PathBuf,
    schema: PathBuf,
    manifest: PathBuf,
}

#[derive(Clone, Debug)]
struct InputFile {
    label: &'static str,
    path: PathBuf,
    digest: [u8; 32],
}

#[derive(Clone, Debug)]
struct InputSnapshot {
    files: Vec<InputFile>,
    target_bytes: Vec<u8>,
    schema_bytes: Vec<u8>,
    prior_manifest: ArtifactManifest,
}

/// Runs production generation with the pinned formatter and filesystem publisher.
pub fn execute(request: GenerateRequest) -> Result<GenerateOutcome, DiagnosticSet> {
    execute_with(request, &PinnedRustfmt, &publish::NoopPublicationObserver)
}

/// Runs generation with injectable formatter/publication boundaries for deterministic tests.
pub fn execute_with<F, O>(
    request: GenerateRequest,
    formatter: &F,
    observer: &O,
) -> Result<GenerateOutcome, DiagnosticSet>
where
    F: CandidateFormatter,
    O: PublicationObserver,
{
    let paths = resolve_paths(&request)?;
    let snapshot = InputSnapshot::load(&paths)?;
    let target = CodegenTarget::decode_exact(&snapshot.target_bytes)?;
    snapshot.prior_manifest.validate_target(&target)?;
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: &snapshot.schema_bytes,
    })?;
    let rendered = render_core(&plan)?;
    let formatted = formatter.finalize(&paths.workspace, &target, &rendered)?;
    let candidate_manifest = ArtifactManifest::from_artifacts(&target, &formatted);
    let manifest_bytes = candidate_manifest.encode()?;
    let manifest_path = ArtifactPath::new(BINDING_MANIFEST)?;
    let changes = publish::compare(
        &paths.workspace,
        &formatted,
        &snapshot.prior_manifest,
        &manifest_path,
        &manifest_bytes,
    )?;

    match request.mode {
        GenerateMode::Check if changes.is_empty() => Ok(GenerateOutcome { changes }),
        GenerateMode::Check => Err(publish::drift_diagnostics(&changes)),
        GenerateMode::Update => {
            Publisher::new(&paths.workspace, observer).publish(
                &formatted,
                &snapshot.prior_manifest,
                &manifest_path,
                &manifest_bytes,
                &changes,
                || snapshot.revalidate(),
            )?;
            Ok(GenerateOutcome { changes })
        }
    }
}

fn resolve_paths(request: &GenerateRequest) -> Result<ResolvedPaths, DiagnosticSet> {
    reject_symlink_or_non_directory("workspace", &request.workspace)?;
    let workspace = fs::canonicalize(&request.workspace).map_err(|_| {
        input_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "workspace",
            "generation workspace could not be resolved",
        )
    })?;

    let fixture_root = match &request.overrides.fixture_root {
        Some(root) => {
            reject_symlink_or_non_directory("fixture-root", root)?;
            let root = fs::canonicalize(root).map_err(|_| {
                input_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "fixture-root",
                    "fixture root could not be resolved",
                )
            })?;
            if !workspace.starts_with(&root) {
                return Err(input_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "fixture-root",
                    "fixture workspace is outside the explicit fixture root",
                ));
            }
            Some(root)
        }
        None if request.overrides.any_path() => {
            return Err(input_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "fixture-root",
                "path overrides require an explicit fixture root",
            ));
        }
        None => None,
    };

    let resolve = |label: &'static str,
                   override_path: &Option<PathBuf>,
                   default: &str|
     -> Result<PathBuf, DiagnosticSet> {
        let path = override_path
            .clone()
            .unwrap_or_else(|| workspace.join(default));
        if let Some(root) = &fixture_root {
            let canonical = fs::canonicalize(&path).map_err(|_| {
                input_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    label,
                    "fixture input could not be resolved",
                )
            })?;
            if !canonical.starts_with(root) {
                return Err(input_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    label,
                    "fixture input is outside the explicit fixture root",
                ));
            }
        }
        Ok(path)
    };

    let target = resolve("target", &request.overrides.target, TARGET_INPUT)?;
    let schema = resolve("schema", &request.overrides.schema, SCHEMA_INPUT)?;
    let manifest = resolve("manifest", &request.overrides.manifest, BINDING_MANIFEST)?;
    Ok(ResolvedPaths {
        workspace,
        target,
        schema,
        manifest,
    })
}

impl InputSnapshot {
    fn load(paths: &ResolvedPaths) -> Result<Self, DiagnosticSet> {
        let target_bytes = read_regular(
            "target",
            &paths.target,
            dagger_codegen::target::MAX_TARGET_BYTES,
            DiagnosticCode::TargetIdentityInvalid,
        )?;
        let schema_bytes = read_regular(
            "schema",
            &paths.schema,
            dagger_codegen::target::MAX_SCHEMA_BYTES,
            DiagnosticCode::SchemaRootInvalid,
        )?;
        let manifest_bytes = read_regular(
            "manifest",
            &paths.manifest,
            MAX_JSON_INPUT_BYTES,
            DiagnosticCode::GeneratedProvenanceInvalid,
        )?;

        let prior_manifest = ArtifactManifest::decode(&manifest_bytes)?;
        let files = [
            ("target", &paths.target, &target_bytes),
            ("schema", &paths.schema, &schema_bytes),
            ("manifest", &paths.manifest, &manifest_bytes),
        ]
        .into_iter()
        .map(|(label, path, bytes)| InputFile {
            label,
            path: path.clone(),
            digest: Sha256::digest(bytes).into(),
        })
        .collect();

        Ok(Self {
            files,
            target_bytes,
            schema_bytes,
            prior_manifest,
        })
    }

    fn revalidate(&self) -> Result<(), DiagnosticSet> {
        let mut diagnostics = Vec::new();
        for file in &self.files {
            match read_regular(
                file.label,
                &file.path,
                MAX_JSON_INPUT_BYTES,
                DiagnosticCode::GeneratedPublicationFailed,
            ) {
                Ok(bytes) if <[u8; 32]>::from(Sha256::digest(&bytes)) == file.digest => {}
                Ok(_) => diagnostics.push(Diagnostic::new(
                    DiagnosticCode::GeneratedPublicationFailed,
                    Some(DiagnosticCoordinate::new(file.label)),
                    "generation input changed after planning",
                )),
                Err(errors) => diagnostics.extend(errors.diagnostics().iter().cloned()),
            }
        }
        match DiagnosticSet::new(diagnostics) {
            Some(errors) => Err(errors),
            None => Ok(()),
        }
    }
}

fn read_regular(
    label: &'static str,
    path: &Path,
    limit: usize,
    code: DiagnosticCode,
) -> Result<Vec<u8>, DiagnosticSet> {
    let mut file = open_regular_nofollow(path)
        .map_err(|_| input_error(code, label, "required generation input is unavailable"))?;
    let metadata = file
        .metadata()
        .map_err(|_| input_error(code, label, "generation input could not be inspected"))?;
    if !metadata.is_file() {
        return Err(input_error(
            code,
            label,
            "generation input must be a regular non-symlink file",
        ));
    }
    if metadata.len() > limit as u64 {
        return Err(input_error(
            code,
            label,
            "generation input exceeds its size bound",
        ));
    }
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    file.by_ref()
        .take(limit as u64 + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| input_error(code, label, "generation input could not be read"))?;
    if bytes.len() > limit {
        return Err(input_error(
            code,
            label,
            "generation input exceeded its bound while it was read",
        ));
    }
    Ok(bytes)
}

#[cfg(unix)]
pub(super) fn open_regular_nofollow(path: &Path) -> std::io::Result<File> {
    use rustix::fs::{Mode, OFlags, open};

    // Opening the handle with NOFOLLOW closes the check/read race that a separate
    // symlink_metadata call would leave between validation and byte consumption.
    open(
        path,
        OFlags::RDONLY | OFlags::CLOEXEC | OFlags::NOFOLLOW,
        Mode::empty(),
    )
    .map(File::from)
    .map_err(std::io::Error::from)
}

#[cfg(windows)]
pub(super) fn open_regular_nofollow(path: &Path) -> std::io::Result<File> {
    use std::fs::OpenOptions;
    use std::os::windows::fs::OpenOptionsExt as _;

    const FILE_FLAG_OPEN_REPARSE_POINT: u32 = 0x0020_0000;
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)?;
    let metadata = file.metadata()?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "input is not a regular non-symlink file",
        ));
    }
    Ok(file)
}

#[cfg(not(any(unix, windows)))]
pub(super) fn open_regular_nofollow(path: &Path) -> std::io::Result<File> {
    let metadata = fs::symlink_metadata(path)?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "input is not a regular non-symlink file",
        ));
    }
    File::open(path)
}

fn reject_symlink_or_non_directory(label: &'static str, path: &Path) -> Result<(), DiagnosticSet> {
    let metadata = fs::symlink_metadata(path).map_err(|_| {
        input_error(
            DiagnosticCode::GeneratedPublicationFailed,
            label,
            "required directory is unavailable",
        )
    })?;
    if metadata.file_type().is_symlink() || !metadata.is_dir() {
        return Err(input_error(
            DiagnosticCode::GeneratedPublicationFailed,
            label,
            "required directory must be a non-symlink directory",
        ));
    }
    Ok(())
}

fn input_error(
    code: DiagnosticCode,
    coordinate: &'static str,
    message: &'static str,
) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate)),
        message,
    ))
}
