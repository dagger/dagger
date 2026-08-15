//! Exhaustive ownership comparison and failure-atomic publication.
//!
//! Ownership comes only from the prior manifest, the complete candidate, and the one
//! named legacy predecessor. Directory enumeration detects conflicts but never grants
//! permission to adopt or delete a path. Update mode stages beside each destination,
//! journals the transaction, and restores prior bytes after any failed replacement.

use std::collections::{BTreeMap, BTreeSet};
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write as _};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

use dagger_codegen::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use dagger_codegen::render::LEGACY_GENERATED_PREDECESSOR;
use dagger_codegen::target::CodegenTarget;
use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};

use super::format::{ArtifactKind, FormattedArtifactSet, Provenance};
use super::{ArtifactPath, BINDING_MANIFEST, open_regular_nofollow};

const MANIFEST_FORMAT_VERSION: u32 = 1;
const GENERATED_MODULE_ROOT: &str = "crates/dagger-sdk/src/gen";
const JOURNAL_PATH: &str = "target/dagger-codegen-publication.json";
const MAX_GENERATED_ARTIFACT_BYTES: u64 = 64 * 1024 * 1024;
const MAX_JOURNAL_BYTES: u64 = 8 * 1024 * 1024;
const MAX_JOURNAL_ENTRIES: usize = 4_096;
static TRANSACTION_SEQUENCE: AtomicU64 = AtomicU64::new(0);

/// Final artifact record embedded in the generated manifest.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ArtifactRecord {
    kind: ArtifactKind,
    sha256: String,
    semantic_sha256: String,
    provenance: Provenance,
}

/// Complete generated ownership manifest.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ArtifactManifest {
    format_version: u32,
    target_revision: String,
    schema_digest: String,
    artifacts: BTreeMap<ArtifactPath, ArtifactRecord>,
}

impl ArtifactManifest {
    /// Constructs the manifest projection after final formatting and provenance checks.
    #[must_use]
    pub fn from_artifacts(target: &CodegenTarget, artifacts: &FormattedArtifactSet) -> Self {
        let artifacts = artifacts
            .files()
            .iter()
            .map(|(path, artifact)| {
                (
                    path.clone(),
                    ArtifactRecord {
                        kind: artifact.kind(),
                        sha256: artifact.sha256().to_owned(),
                        semantic_sha256: artifact.semantic_sha256().to_owned(),
                        provenance: artifact.provenance().clone(),
                    },
                )
            })
            .collect();
        Self {
            format_version: MANIFEST_FORMAT_VERSION,
            target_revision: target.dagger_revision().as_str().to_owned(),
            schema_digest: target.schema_digest().to_string(),
            artifacts,
        }
    }

    /// Decodes the artifact projection of a previous generated binding manifest.
    pub fn decode(bytes: &[u8]) -> Result<Self, DiagnosticSet> {
        let manifest = serde_json::from_slice::<Self>(bytes).map_err(|_| {
            publication_error(
                DiagnosticCode::GeneratedProvenanceInvalid,
                "manifest",
                "previous generated manifest is invalid JSON",
            )
        })?;
        if manifest.format_version != MANIFEST_FORMAT_VERSION {
            return Err(publication_error(
                DiagnosticCode::GeneratedProvenanceInvalid,
                "manifest.format_version",
                "previous generated manifest format is unsupported",
            ));
        }
        for path in manifest.artifacts.keys() {
            validate_generated_source_path(path)?;
        }
        Ok(manifest)
    }

    /// Encodes deterministic manifest bytes with one terminal newline.
    pub fn encode(&self) -> Result<Vec<u8>, DiagnosticSet> {
        let mut bytes = serde_json::to_vec(self).map_err(|_| {
            publication_error(
                DiagnosticCode::GeneratedProvenanceInvalid,
                "manifest",
                "generated manifest could not be encoded",
            )
        })?;
        bytes.push(b'\n');
        Ok(bytes)
    }

    /// Verifies manifest and artifact provenance against the selected exact target.
    pub fn validate_target(&self, target: &CodegenTarget) -> Result<(), DiagnosticSet> {
        let mut diagnostics = Vec::new();
        if self.target_revision != target.dagger_revision().as_str() {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::GeneratedProvenanceInvalid,
                Some(DiagnosticCoordinate::new("manifest.target_revision")),
                "previous manifest target revision differs from the exact target",
            ));
        }
        if self.schema_digest != target.schema_digest().to_string() {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::GeneratedProvenanceInvalid,
                Some(DiagnosticCoordinate::new("manifest.schema_digest")),
                "previous manifest schema digest differs from the exact target",
            ));
        }
        for (path, record) in &self.artifacts {
            if let Err(errors) = record.provenance.validate(target, path) {
                diagnostics.extend(errors.diagnostics().iter().cloned());
            }
            if record.kind != artifact_kind_for_path(path)
                || !is_canonical_sha256(&record.sha256)
                || !is_canonical_sha256(&record.semantic_sha256)
            {
                diagnostics.push(Diagnostic::new(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    Some(DiagnosticCoordinate::new(path.as_str())),
                    "previous artifact record is inconsistent with its declared path",
                ));
            }
        }
        match DiagnosticSet::new(diagnostics) {
            Some(errors) => Err(errors),
            None => Ok(()),
        }
    }

    /// Borrows declared source artifacts in stable path order.
    #[must_use]
    pub const fn artifacts(&self) -> &BTreeMap<ArtifactPath, ArtifactRecord> {
        &self.artifacts
    }
}

/// Observable relationship between committed and candidate generated output.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum ArtifactChangeKind {
    /// Candidate contains a path absent from the checkout.
    Added,
    /// Candidate bytes differ from the committed path.
    Changed,
    /// A previously owned path is obsolete and still present.
    Removed,
}

/// One generated-artifact difference.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct ArtifactChange {
    path: ArtifactPath,
    kind: ArtifactChangeKind,
}

impl ArtifactChange {
    /// Creates a typed change for tests and reference models.
    #[must_use]
    pub const fn new(path: ArtifactPath, kind: ArtifactChangeKind) -> Self {
        Self { path, kind }
    }

    /// Borrows the changed path.
    #[must_use]
    pub const fn path(&self) -> &ArtifactPath {
        &self.path
    }

    /// Returns the change category.
    #[must_use]
    pub const fn kind(&self) -> ArtifactChangeKind {
        self.kind
    }
}

/// Compares candidate bytes against the complete declared owned output set.
pub fn compare(
    workspace: &Path,
    candidate: &FormattedArtifactSet,
    prior: &ArtifactManifest,
    manifest_path: &ArtifactPath,
    manifest_bytes: &[u8],
) -> Result<Vec<ArtifactChange>, DiagnosticSet> {
    if manifest_path.as_str() != BINDING_MANIFEST {
        return Err(publication_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "manifest",
            "generated binding manifest path differs from the declared destination",
        ));
    }

    let mut candidate_bytes = candidate
        .files()
        .iter()
        .map(|(path, artifact)| (path.clone(), artifact.bytes().to_vec()))
        .collect::<BTreeMap<_, _>>();
    candidate_bytes.insert(manifest_path.clone(), manifest_bytes.to_vec());
    let legacy = ArtifactPath::new(LEGACY_GENERATED_PREDECESSOR)?;
    let mut owned = prior.artifacts().keys().cloned().collect::<BTreeSet<_>>();
    owned.extend(candidate_bytes.keys().cloned());
    owned.insert(manifest_path.clone());
    owned.insert(legacy);
    for path in &owned {
        validate_owned_path(path, manifest_path)?;
    }
    reject_unknown_generated_files(workspace, &owned)?;

    let mut changes = Vec::new();
    for path in &owned {
        let current = read_destination(path, workspace)?;
        match (current, candidate_bytes.get(path)) {
            (None, Some(_)) => {
                changes.push(ArtifactChange::new(path.clone(), ArtifactChangeKind::Added))
            }
            (Some(current), Some(expected)) if current != *expected => changes.push(
                ArtifactChange::new(path.clone(), ArtifactChangeKind::Changed),
            ),
            (Some(_), None) => changes.push(ArtifactChange::new(
                path.clone(),
                ArtifactChangeKind::Removed,
            )),
            _ => {}
        }
    }
    changes.sort();
    Ok(changes)
}

/// Converts a non-empty drift set into sorted stable diagnostics.
#[must_use]
pub fn drift_diagnostics(changes: &[ArtifactChange]) -> DiagnosticSet {
    let diagnostics = changes
        .iter()
        .map(|change| {
            let state = match change.kind {
                ArtifactChangeKind::Added => "is missing from the checkout",
                ArtifactChangeKind::Changed => "differs from generated bytes",
                ArtifactChangeKind::Removed => "is obsolete but still present",
            };
            Diagnostic::new(
                DiagnosticCode::GeneratedOutputDrift,
                Some(DiagnosticCoordinate::new(change.path.as_str())),
                state,
            )
        })
        .collect();
    DiagnosticSet::new(diagnostics).unwrap_or_else(|| {
        publication_error(
            DiagnosticCode::GeneratedOutputDrift,
            "generated-output",
            "generated output drift was reported without a changed artifact",
        )
    })
}

/// Injectable publication boundary used for deterministic failure schedules.
pub trait PublicationObserver: Send + Sync {
    /// Accepts or rejects one transaction checkpoint.
    fn checkpoint(
        &self,
        checkpoint: PublicationCheckpoint,
        path: Option<&ArtifactPath>,
    ) -> Result<(), PublicationInterruption>;
}

/// Deliberate interruption injected at a publication checkpoint.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct PublicationInterruption;
/// Publication phase visible to failure-injection tests.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum PublicationCheckpoint {
    /// Lock acquired and stale recovery completed.
    Validate,
    /// Candidate file is about to be staged.
    Stage,
    /// Staged bytes are about to be flushed.
    Flush,
    /// Candidate is about to replace a destination.
    Replace,
    /// Obsolete destination is about to be retired.
    Retire,
    /// Applied replacements are about to be committed for recovery.
    Commit,
    /// Prior bytes are being restored.
    Rollback,
}

/// Production observer that never injects failure.
pub struct NoopPublicationObserver;

impl PublicationObserver for NoopPublicationObserver {
    fn checkpoint(
        &self,
        _checkpoint: PublicationCheckpoint,
        _path: Option<&ArtifactPath>,
    ) -> Result<(), PublicationInterruption> {
        Ok(())
    }
}

/// Update-mode publisher scoped to one validated Rust workspace.
pub struct Publisher<'a, O> {
    workspace: &'a Path,
    observer: &'a O,
}

impl<'a, O> Publisher<'a, O>
where
    O: PublicationObserver,
{
    /// Creates a publisher that has no authority outside the workspace.
    #[must_use]
    pub const fn new(workspace: &'a Path, observer: &'a O) -> Self {
        Self {
            workspace,
            observer,
        }
    }

    /// Publishes an already validated candidate or restores the complete prior state.
    ///
    /// `revalidate` runs while the update lock is held and before any destination is
    /// staged or replaced.
    pub fn publish<R>(
        &self,
        candidate: &FormattedArtifactSet,
        prior: &ArtifactManifest,
        manifest_path: &ArtifactPath,
        manifest_bytes: &[u8],
        planned_changes: &[ArtifactChange],
        revalidate: R,
    ) -> Result<(), DiagnosticSet>
    where
        R: FnOnce() -> Result<(), DiagnosticSet>,
    {
        let _lock = acquire_lock(self.workspace)?;
        self.recover_stale()?;
        self.observe(PublicationCheckpoint::Validate, None)?;
        revalidate()?;
        let current = compare(
            self.workspace,
            candidate,
            prior,
            manifest_path,
            manifest_bytes,
        )?;
        if current != planned_changes {
            return Err(publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "generated-output",
                "generated output changed after publication planning",
            ));
        }
        if current.is_empty() {
            return Ok(());
        }

        let candidate_bytes = candidate
            .files()
            .iter()
            .map(|(path, artifact)| (path.clone(), artifact.bytes().to_vec()))
            .chain(std::iter::once((
                manifest_path.clone(),
                manifest_bytes.to_vec(),
            )))
            .collect::<BTreeMap<_, _>>();
        let transaction_id = format!(
            "{}-{}",
            std::process::id(),
            TRANSACTION_SEQUENCE.fetch_add(1, Ordering::Relaxed)
        );
        let mut journal = Journal {
            transaction_id: transaction_id.clone(),
            commit_marker: self
                .workspace
                .join("target")
                .join(format!(".dagger-commit-{transaction_id}")),
            entries: Vec::new(),
        };

        for (index, change) in current.iter().enumerate() {
            let destination = change.path.resolve(self.workspace);
            validate_destination_ancestry(self.workspace, &change.path)?;
            let parent = destination.parent().ok_or_else(|| {
                publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    change.path.as_str(),
                    "generated destination has no parent directory",
                )
            })?;
            fs::create_dir_all(parent).map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    change.path.as_str(),
                    "generated destination parent could not be created",
                )
            })?;
            let staged = parent.join(format!(".dagger-stage-{transaction_id}-{index}"));
            let backup = parent.join(format!(".dagger-backup-{transaction_id}-{index}"));
            let had_original = fs::symlink_metadata(&destination).is_ok();
            journal.entries.push(JournalEntry {
                path: change.path.clone(),
                staged,
                backup,
                had_original,
                publishes_candidate: candidate_bytes.contains_key(&change.path),
            });
            let entry = journal
                .entries
                .last()
                .expect("the just-pushed journal entry must remain present");
            if let Some(bytes) = candidate_bytes.get(&change.path) {
                let staged = (|| {
                    self.observe(PublicationCheckpoint::Stage, Some(&change.path))?;
                    let mut file = OpenOptions::new()
                        .write(true)
                        .create_new(true)
                        .open(&entry.staged)
                        .map_err(|_| publication_failure(&change.path, "stage"))?;
                    file.write_all(bytes)
                        .map_err(|_| publication_failure(&change.path, "stage"))?;
                    self.observe(PublicationCheckpoint::Flush, Some(&change.path))?;
                    file.sync_all()
                        .map_err(|_| publication_failure(&change.path, "flush"))
                })();
                if let Err(cause) = staged {
                    cleanup_staged(&journal);
                    return Err(cause);
                }
            }
        }
        if let Err(cause) = self.write_journal(&journal) {
            cleanup_staged(&journal);
            return Err(cause);
        }

        if let Err(cause) = self.apply(&journal) {
            return self.fail_with_rollback(&journal, cause);
        }
        if let Err(cause) = self
            .observe(PublicationCheckpoint::Commit, None)
            .and_then(|()| self.mark_committed(&journal))
        {
            return self.fail_with_rollback(&journal, cause);
        }
        self.finish_committed(&journal)
    }

    fn fail_with_rollback(
        &self,
        journal: &Journal,
        cause: DiagnosticSet,
    ) -> Result<(), DiagnosticSet> {
        match self.rollback(journal) {
            Ok(()) => Err(cause),
            Err(rollback) => {
                let mut diagnostics = cause.diagnostics().to_vec();
                diagnostics.extend(rollback.diagnostics().iter().cloned());
                Err(DiagnosticSet::new(diagnostics).unwrap_or(cause))
            }
        }
    }

    fn apply(&self, journal: &Journal) -> Result<(), DiagnosticSet> {
        for entry in &journal.entries {
            let destination = entry.path.resolve(self.workspace);
            let checkpoint = if entry.publishes_candidate {
                PublicationCheckpoint::Replace
            } else {
                PublicationCheckpoint::Retire
            };
            self.observe(checkpoint, Some(&entry.path))?;
            if entry.had_original {
                fs::rename(&destination, &entry.backup)
                    .map_err(|_| publication_failure(&entry.path, "backup"))?;
            }
            if entry.publishes_candidate {
                fs::rename(&entry.staged, &destination)
                    .map_err(|_| publication_failure(&entry.path, "replace"))?;
                sync_parent(&destination);
            }
        }
        Ok(())
    }

    fn rollback(&self, journal: &Journal) -> Result<(), DiagnosticSet> {
        let mut diagnostics = Vec::new();
        for entry in journal.entries.iter().rev() {
            if self
                .observer
                .checkpoint(PublicationCheckpoint::Rollback, Some(&entry.path))
                .is_err()
            {
                diagnostics.push(Diagnostic::new(
                    DiagnosticCode::GeneratedPublicationFailed,
                    Some(DiagnosticCoordinate::new(entry.path.as_str())),
                    "generated publication rollback was interrupted",
                ));
                continue;
            }
            let destination = entry.path.resolve(self.workspace);
            if entry.backup.exists() {
                if destination.exists() && fs::remove_file(&destination).is_err() {
                    diagnostics.push(rollback_failure(&entry.path));
                    continue;
                }
                if fs::rename(&entry.backup, &destination).is_err() {
                    diagnostics.push(rollback_failure(&entry.path));
                    continue;
                }
            } else if !entry.had_original
                && destination.exists()
                && fs::remove_file(&destination).is_err()
            {
                diagnostics.push(rollback_failure(&entry.path));
                continue;
            }
            if entry.staged.exists() && fs::remove_file(&entry.staged).is_err() {
                diagnostics.push(rollback_failure(&entry.path));
            }
        }
        if diagnostics.is_empty() {
            remove_transaction_file(&journal.commit_marker)?;
            remove_journal(self.workspace)?;
            Ok(())
        } else {
            Err(DiagnosticSet::new(diagnostics).unwrap_or_else(|| {
                publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "rollback",
                    "generated publication rollback failed",
                )
            }))
        }
    }

    fn recover_stale(&self) -> Result<(), DiagnosticSet> {
        let path = self.workspace.join(JOURNAL_PATH);
        let metadata = match fs::symlink_metadata(&path) {
            Ok(metadata) => metadata,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(_) => {
                return Err(publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "transaction",
                    "stale publication transaction could not be inspected",
                ));
            }
        };
        if metadata.file_type().is_symlink() || !metadata.is_file() {
            return Err(publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "transaction",
                "stale publication transaction is not a regular file",
            ));
        }
        if metadata.len() > MAX_JOURNAL_BYTES {
            return Err(publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "transaction",
                "stale publication transaction exceeds its size bound",
            ));
        }
        let mut file = open_regular_nofollow(&path).map_err(|_| {
            publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "transaction",
                "stale publication transaction could not be read",
            )
        })?;
        let mut bytes = Vec::with_capacity(metadata.len() as usize);
        Read::by_ref(&mut file)
            .take(MAX_JOURNAL_BYTES + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "transaction",
                    "stale publication transaction could not be read",
                )
            })?;
        if bytes.len() as u64 > MAX_JOURNAL_BYTES {
            return Err(publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "transaction",
                "stale publication transaction exceeds its size bound",
            ));
        }
        let journal = serde_json::from_slice::<Journal>(&bytes).map_err(|_| {
            publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "transaction",
                "stale publication transaction is invalid",
            )
        })?;
        validate_transaction_id(&journal.transaction_id)?;
        if journal.entries.len() > MAX_JOURNAL_ENTRIES {
            return Err(publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "transaction",
                "stale publication transaction has too many entries",
            ));
        }
        let mut declared = BTreeSet::new();
        validate_commit_marker(self.workspace, &journal)?;
        for (index, entry) in journal.entries.iter().enumerate() {
            validate_owned_path(&entry.path, &ArtifactPath::new(BINDING_MANIFEST)?)?;
            validate_transaction_entry(self.workspace, &journal.transaction_id, index, entry)?;
            if !declared.insert(entry.path.clone()) {
                return Err(publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    entry.path.as_str(),
                    "stale publication transaction repeats a destination",
                ));
            }
        }
        if commit_marker_matches(&journal)? {
            self.finish_committed(&journal)
        } else {
            remove_transaction_file(&journal.commit_marker)?;
            self.rollback(&journal)
        }
    }

    fn mark_committed(&self, journal: &Journal) -> Result<(), DiagnosticSet> {
        let result = (|| {
            let mut marker = OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&journal.commit_marker)
                .map_err(|_| publication_commit_failure())?;
            marker
                .write_all(journal.transaction_id.as_bytes())
                .and_then(|()| marker.sync_all())
                .map_err(|_| publication_commit_failure())?;
            sync_parent(&journal.commit_marker);
            Ok(())
        })();
        if result.is_err() {
            let _ = remove_transaction_file(&journal.commit_marker);
        }
        result
    }

    fn finish_committed(&self, journal: &Journal) -> Result<(), DiagnosticSet> {
        for entry in &journal.entries {
            remove_if_regular(&entry.backup, &entry.path)?;
            remove_if_regular(&entry.staged, &entry.path)?;
        }
        // Removing the journal first makes any crash after this point a committed
        // transaction with, at worst, a harmless private marker left to clean.
        remove_journal(self.workspace)?;
        remove_transaction_file(&journal.commit_marker)
    }

    fn write_journal(&self, journal: &Journal) -> Result<(), DiagnosticSet> {
        let path = self.workspace.join(JOURNAL_PATH);
        if let Some(parent) = path.parent() {
            if let Ok(metadata) = fs::symlink_metadata(parent)
                && metadata.file_type().is_symlink()
            {
                return Err(publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "transaction",
                    "publication transaction directory is a symlink",
                ));
            }
            fs::create_dir_all(parent).map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "transaction",
                    "publication transaction directory could not be created",
                )
            })?;
        }
        let bytes = serde_json::to_vec(journal).map_err(|_| {
            publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                "transaction",
                "publication transaction could not be encoded",
            )
        })?;
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(path)
            .map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "transaction",
                    "publication transaction could not be recorded",
                )
            })?;
        file.write_all(&bytes)
            .and_then(|()| file.sync_all())
            .map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    "transaction",
                    "publication transaction could not be flushed",
                )
            })
    }

    fn observe(
        &self,
        checkpoint: PublicationCheckpoint,
        path: Option<&ArtifactPath>,
    ) -> Result<(), DiagnosticSet> {
        self.observer.checkpoint(checkpoint, path).map_err(|_| {
            publication_error(
                DiagnosticCode::GeneratedPublicationFailed,
                path.map_or("publication", ArtifactPath::as_str),
                "generated publication failed at an injected checkpoint",
            )
        })
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct Journal {
    transaction_id: String,
    commit_marker: PathBuf,
    entries: Vec<JournalEntry>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct JournalEntry {
    path: ArtifactPath,
    staged: PathBuf,
    backup: PathBuf,
    had_original: bool,
    publishes_candidate: bool,
}

fn validate_transaction_id(transaction_id: &str) -> Result<(), DiagnosticSet> {
    if transaction_id.is_empty()
        || transaction_id.len() > 64
        || transaction_id
            .bytes()
            .any(|byte| !byte.is_ascii_digit() && byte != b'-')
    {
        return Err(publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "transaction",
            "stale publication transaction identity is invalid",
        ));
    }
    Ok(())
}

fn validate_commit_marker(workspace: &Path, journal: &Journal) -> Result<(), DiagnosticSet> {
    let expected = workspace
        .join("target")
        .join(format!(".dagger-commit-{}", journal.transaction_id));
    if journal.commit_marker != expected {
        return Err(publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "transaction",
            "stale publication transaction contains an undeclared commit marker",
        ));
    }
    match fs::symlink_metadata(&journal.commit_marker) {
        Ok(metadata) if metadata.is_file() && !metadata.file_type().is_symlink() => Ok(()),
        Ok(_) => Err(publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "transaction",
            "stale publication commit marker is not a regular file",
        )),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => Err(publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "transaction",
            "stale publication commit marker could not be inspected",
        )),
    }
}

fn commit_marker_matches(journal: &Journal) -> Result<bool, DiagnosticSet> {
    let mut marker = match open_regular_nofollow(&journal.commit_marker) {
        Ok(marker) => marker,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(false),
        Err(_) => return Err(publication_commit_failure()),
    };
    let mut bytes = Vec::new();
    Read::by_ref(&mut marker)
        .take(65)
        .read_to_end(&mut bytes)
        .map_err(|_| publication_commit_failure())?;
    Ok(bytes == journal.transaction_id.as_bytes())
}

fn validate_transaction_entry(
    workspace: &Path,
    transaction_id: &str,
    index: usize,
    entry: &JournalEntry,
) -> Result<(), DiagnosticSet> {
    validate_destination_ancestry(workspace, &entry.path)?;
    let destination = entry.path.resolve(workspace);
    let parent = destination.parent().ok_or_else(|| {
        publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            entry.path.as_str(),
            "stale publication destination has no parent directory",
        )
    })?;
    let expected_stage = parent.join(format!(".dagger-stage-{transaction_id}-{index}"));
    let expected_backup = parent.join(format!(".dagger-backup-{transaction_id}-{index}"));
    if entry.staged != expected_stage || entry.backup != expected_backup {
        return Err(publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            entry.path.as_str(),
            "stale publication transaction contains an undeclared path",
        ));
    }
    for state_path in [&entry.staged, &entry.backup] {
        match fs::symlink_metadata(state_path) {
            Ok(metadata) if metadata.is_file() && !metadata.file_type().is_symlink() => {}
            Ok(_) => {
                return Err(publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    entry.path.as_str(),
                    "stale publication transaction state is not a regular file",
                ));
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => {
                return Err(publication_error(
                    DiagnosticCode::GeneratedPublicationFailed,
                    entry.path.as_str(),
                    "stale publication transaction state could not be inspected",
                ));
            }
        }
    }
    Ok(())
}

fn validate_generated_source_path(path: &ArtifactPath) -> Result<(), DiagnosticSet> {
    if path
        .as_str()
        .starts_with(&format!("{GENERATED_MODULE_ROOT}/"))
        || matches!(
            path.as_str(),
            "crates/dagger-sdk/tests/core_reachability.rs"
                | "crates/dagger-sdk/tests/core_projection.rs"
        )
    {
        Ok(())
    } else {
        Err(publication_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            path.as_str(),
            "manifest declares an artifact outside generated source roots",
        ))
    }
}

fn artifact_kind_for_path(path: &ArtifactPath) -> ArtifactKind {
    if path
        .as_str()
        .starts_with(&format!("{GENERATED_MODULE_ROOT}/"))
    {
        ArtifactKind::RustModule
    } else {
        ArtifactKind::RustTest
    }
}

fn is_canonical_sha256(value: &str) -> bool {
    value.strip_prefix("sha256:").is_some_and(|hex| {
        hex.len() == 64
            && hex
                .bytes()
                .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
    })
}

fn validate_owned_path(
    path: &ArtifactPath,
    manifest_path: &ArtifactPath,
) -> Result<(), DiagnosticSet> {
    if path == manifest_path || path.as_str() == LEGACY_GENERATED_PREDECESSOR {
        return Ok(());
    }
    validate_generated_source_path(path)
}

fn reject_unknown_generated_files(
    workspace: &Path,
    owned: &BTreeSet<ArtifactPath>,
) -> Result<(), DiagnosticSet> {
    let root = workspace.join(GENERATED_MODULE_ROOT);
    let metadata = match fs::symlink_metadata(&root) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(_) => {
            return Err(publication_error(
                DiagnosticCode::GeneratedProvenanceInvalid,
                GENERATED_MODULE_ROOT,
                "generated module root could not be inspected",
            ));
        }
    };
    if metadata.file_type().is_symlink() || !metadata.is_dir() {
        return Err(publication_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            GENERATED_MODULE_ROOT,
            "generated module root must be a non-symlink directory",
        ));
    }
    let mut pending = vec![root];
    let mut diagnostics = Vec::new();
    while let Some(directory) = pending.pop() {
        let mut entries = fs::read_dir(&directory)
            .map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    GENERATED_MODULE_ROOT,
                    "generated module root could not be enumerated",
                )
            })?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    GENERATED_MODULE_ROOT,
                    "generated module entry could not be inspected",
                )
            })?;
        entries.sort_by_key(std::fs::DirEntry::file_name);
        for entry in entries {
            let file_type = entry.file_type().map_err(|_| {
                publication_error(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    GENERATED_MODULE_ROOT,
                    "generated module entry could not be inspected",
                )
            })?;
            if file_type.is_symlink() {
                diagnostics.push(Diagnostic::new(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    Some(DiagnosticCoordinate::new(GENERATED_MODULE_ROOT)),
                    "generated module tree contains a symlink",
                ));
            } else if file_type.is_dir() {
                pending.push(entry.path());
            } else if file_type.is_file() {
                let relative =
                    entry.path().strip_prefix(workspace).ok().and_then(|path| {
                        path.to_str().and_then(|path| ArtifactPath::new(path).ok())
                    });
                if relative.as_ref().is_none_or(|path| !owned.contains(path)) {
                    diagnostics.push(Diagnostic::new(
                        DiagnosticCode::GeneratedProvenanceInvalid,
                        Some(DiagnosticCoordinate::new(GENERATED_MODULE_ROOT)),
                        "generated module tree contains an undeclared file",
                    ));
                }
            } else {
                diagnostics.push(Diagnostic::new(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    Some(DiagnosticCoordinate::new(GENERATED_MODULE_ROOT)),
                    "generated module tree contains a non-regular entry",
                ));
            }
        }
    }
    match DiagnosticSet::new(diagnostics) {
        Some(errors) => Err(errors),
        None => Ok(()),
    }
}

fn read_destination(
    path: &ArtifactPath,
    workspace: &Path,
) -> Result<Option<Vec<u8>>, DiagnosticSet> {
    let physical = path.resolve(workspace);
    let metadata = match fs::symlink_metadata(&physical) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(publication_failure(path, "inspect")),
    };
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(publication_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            path.as_str(),
            "generated destination must be a regular non-symlink file",
        ));
    }
    if metadata.len() > MAX_GENERATED_ARTIFACT_BYTES {
        return Err(publication_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            path.as_str(),
            "generated destination exceeds its size bound",
        ));
    }
    let mut file =
        open_regular_nofollow(&physical).map_err(|_| publication_failure(path, "read"))?;
    let mut bytes = Vec::with_capacity(metadata.len() as usize);
    Read::by_ref(&mut file)
        .take(MAX_GENERATED_ARTIFACT_BYTES + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| publication_failure(path, "read"))?;
    if bytes.len() as u64 > MAX_GENERATED_ARTIFACT_BYTES {
        return Err(publication_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            path.as_str(),
            "generated destination exceeds its size bound",
        ));
    }
    Ok(Some(bytes))
}

fn validate_destination_ancestry(
    workspace: &Path,
    path: &ArtifactPath,
) -> Result<(), DiagnosticSet> {
    let mut current = workspace.to_path_buf();
    let components = Path::new(path.as_str()).components().collect::<Vec<_>>();
    for component in components.iter().take(components.len().saturating_sub(1)) {
        current.push(component.as_os_str());
        match fs::symlink_metadata(&current) {
            Ok(metadata) if metadata.file_type().is_symlink() || !metadata.is_dir() => {
                return Err(publication_error(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    path.as_str(),
                    "generated destination ancestry contains a non-directory or symlink",
                ));
            }
            Ok(_) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => break,
            Err(_) => return Err(publication_failure(path, "inspect")),
        }
    }
    Ok(())
}

fn acquire_lock(workspace: &Path) -> Result<File, DiagnosticSet> {
    let lock_root = std::env::temp_dir().join("dagger-rust-codegen-locks");
    fs::create_dir_all(&lock_root).map_err(|_| {
        publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "publication-lock",
            "publication lock directory could not be created",
        )
    })?;
    let metadata = fs::symlink_metadata(&lock_root).map_err(|_| {
        publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "publication-lock",
            "publication lock directory could not be inspected",
        )
    })?;
    if metadata.file_type().is_symlink() || !metadata.is_dir() {
        return Err(publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "publication-lock",
            "publication lock root is not a regular directory",
        ));
    }
    let identity = digest(workspace.as_os_str().as_encoded_bytes());
    let path = lock_root.join(identity.trim_start_matches("sha256:"));
    let file = open_lock_nofollow(&path).map_err(|_| {
        publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "publication-lock",
            "publication lock could not be opened",
        )
    })?;
    fs4::FileExt::lock(&file).map_err(|_| {
        publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "publication-lock",
            "publication lock could not be acquired",
        )
    })?;
    Ok(file)
}

#[cfg(unix)]
fn open_lock_nofollow(path: &Path) -> std::io::Result<File> {
    use rustix::fs::{Mode, OFlags, open};

    open(
        path,
        OFlags::CREATE | OFlags::RDWR | OFlags::CLOEXEC | OFlags::NOFOLLOW,
        Mode::RUSR | Mode::WUSR,
    )
    .map(File::from)
    .map_err(std::io::Error::from)
}

#[cfg(windows)]
fn open_lock_nofollow(path: &Path) -> std::io::Result<File> {
    use std::os::windows::fs::OpenOptionsExt as _;

    const FILE_FLAG_OPEN_REPARSE_POINT: u32 = 0x0020_0000;
    let file = OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)?;
    if file.metadata()?.file_type().is_symlink() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "lock path is a symlink",
        ));
    }
    Ok(file)
}

#[cfg(not(any(unix, windows)))]
fn open_lock_nofollow(path: &Path) -> std::io::Result<File> {
    OpenOptions::new()
        .create(true)
        .truncate(false)
        .read(true)
        .write(true)
        .open(path)
}

fn remove_journal(workspace: &Path) -> Result<(), DiagnosticSet> {
    let path = workspace.join(JOURNAL_PATH);
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => Err(publication_error(
            DiagnosticCode::GeneratedPublicationFailed,
            "transaction",
            "publication transaction record could not be removed",
        )),
    }
}

fn remove_transaction_file(path: &Path) -> Result<(), DiagnosticSet> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_file() && !metadata.file_type().is_symlink() => {
            fs::remove_file(path).map_err(|_| publication_commit_failure())
        }
        Ok(_) => Err(publication_commit_failure()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => Err(publication_commit_failure()),
    }
}

fn remove_if_regular(path: &Path, coordinate: &ArtifactPath) -> Result<(), DiagnosticSet> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_file() && !metadata.file_type().is_symlink() => {
            fs::remove_file(path).map_err(|_| publication_failure(coordinate, "cleanup"))
        }
        Ok(_) => Err(publication_failure(coordinate, "cleanup")),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => Err(publication_failure(coordinate, "cleanup")),
    }
}

fn cleanup_staged(journal: &Journal) {
    for entry in &journal.entries {
        if let Ok(metadata) = fs::symlink_metadata(&entry.staged)
            && metadata.is_file()
            && !metadata.file_type().is_symlink()
        {
            let _ = fs::remove_file(&entry.staged);
        }
    }
}

#[cfg(unix)]
fn sync_parent(path: &Path) {
    if let Some(parent) = path.parent()
        && let Ok(directory) = File::open(parent)
    {
        let _ = directory.sync_all();
    }
}

#[cfg(not(unix))]
fn sync_parent(_path: &Path) {}

fn digest(bytes: &[u8]) -> String {
    format!("sha256:{:x}", Sha256::digest(bytes))
}

fn publication_failure(path: &ArtifactPath, phase: &'static str) -> DiagnosticSet {
    publication_error(
        DiagnosticCode::GeneratedPublicationFailed,
        path.as_str(),
        match phase {
            "inspect" => "generated destination could not be inspected",
            "read" => "generated destination could not be read",
            "stage" => "generated candidate could not be staged",
            "flush" => "generated candidate could not be flushed",
            "backup" => "generated destination could not enter rollback state",
            "replace" => "generated candidate could not replace its destination",
            "cleanup" => "generated transaction state could not be cleaned",
            _ => "generated publication failed",
        },
    )
}

fn rollback_failure(path: &ArtifactPath) -> Diagnostic {
    Diagnostic::new(
        DiagnosticCode::GeneratedPublicationFailed,
        Some(DiagnosticCoordinate::new(path.as_str())),
        "generated publication rollback could not restore prior bytes",
    )
}

fn publication_commit_failure() -> DiagnosticSet {
    publication_error(
        DiagnosticCode::GeneratedPublicationFailed,
        "transaction",
        "generated publication commit state could not be persisted",
    )
}

fn publication_error(
    code: DiagnosticCode,
    coordinate: &str,
    message: &'static str,
) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate)),
        message,
    ))
}
