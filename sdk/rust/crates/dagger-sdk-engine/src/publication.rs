//! Manifest-authorized ownership and failure-atomic publication.
//!
//! A prior operation manifest is the sole authority for replacement or removal.
//! Complete candidates are staged beside their destinations, flushed, journaled in
//! memory, and renamed in stable path order; the acyclic manifest is always last.

use std::collections::{BTreeMap, BTreeSet};
use std::fs::{self, File, OpenOptions};
use std::io::Write as _;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

use sha2::{Digest as _, Sha256};

use dagger_codegen::module::{
    GeneratedAssetPath, GeneratedModuleAssets, RenderedModuleAssets, validate_manifest,
};

use crate::canonical::canonical_bytes;
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::{
    AmendmentCandidate, AmendmentCoordinate, CandidateArtifact, OperationManifest, OperationRoot,
    RelativeOperationPath, Sha256Digest, semantic_amendment_digest,
};

static TRANSACTION_SEQUENCE: AtomicU64 = AtomicU64::new(0);

/// Complete operation output before any filesystem mutation.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OperationCandidate {
    /// Generated artifacts keyed by their normalized destination.
    pub artifacts: BTreeMap<RelativeOperationPath, CandidateArtifact>,
    /// Complete authored-file candidates keyed by their semantic ownership coordinate.
    pub amendments: BTreeMap<AmendmentCoordinate, AmendmentCandidate>,
    /// New non-owned support files, such as an exact local toolchain declaration.
    pub created_files: BTreeMap<RelativeOperationPath, Vec<u8>>,
    /// Prior whole-file artifacts deliberately transferred to semantic ownership.
    pub retained_previous_artifacts: BTreeSet<RelativeOperationPath>,
    /// Previously owned paths no longer present in the candidate.
    pub removed: BTreeSet<RelativeOperationPath>,
    /// Acyclic ownership record published after every artifact.
    pub manifest: OperationManifest,
    /// Fixed operation-relative control-manifest destination.
    pub manifest_path: RelativeOperationPath,
    /// Digest of current manifest bytes when a prior manifest was loaded.
    pub previous_manifest_digest: Option<Sha256Digest>,
}

/// Complete generated-module tree before any filesystem mutation.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleAssetCandidate {
    /// Rendered files and the manifest that owns every non-manifest path.
    pub rendered: RenderedModuleAssets,
    /// Digest of current manifest bytes when a prior manifest was loaded.
    pub previous_manifest_digest: Option<Sha256Digest>,
}

/// Observable category for one sorted publication change.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum PublicationChangeKind {
    /// A destination is newly created.
    Add,
    /// A manifest-owned destination receives different bytes.
    Change,
    /// An obsolete manifest-owned destination is removed.
    Remove,
}

/// One authorized artifact change.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct PublicationChange {
    /// Normalized destination.
    pub path: RelativeOperationPath,
    /// Relationship to the current tree.
    pub kind: PublicationChangeKind,
}

/// Fully validated transaction ready for staging.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PublicationPlan {
    writes: BTreeMap<RelativeOperationPath, Vec<u8>>,
    removals: BTreeSet<RelativeOperationPath>,
    manifest_path: RelativeOperationPath,
    manifest_bytes: Vec<u8>,
    changes: Vec<PublicationChange>,
}

impl PublicationPlan {
    /// Borrows the complete sorted change set, excluding the manifest-last control write.
    #[must_use]
    pub fn changes(&self) -> &[PublicationChange] {
        &self.changes
    }
}

/// Successful manifest-last publication result.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PublicationOutcome {
    /// Digest of exact canonical manifest bytes now visible in the tree.
    pub manifest_digest: Sha256Digest,
    /// Complete sorted artifact change set.
    pub changes: Vec<PublicationChange>,
    /// Whether canonical control-manifest bytes changed.
    pub manifest_changed: bool,
}

/// One complete authored-file transaction used by initialization before any manifest exists.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoredPublicationCandidate {
    /// Complete file candidates paired with their discovery-time byte identity.
    pub files: BTreeMap<RelativeOperationPath, (Option<Sha256Digest>, Vec<u8>)>,
}

/// Deterministic transaction checkpoints used by fault-injection tests.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum PublicationCheckpoint {
    /// A candidate file has been staged and flushed.
    Staged,
    /// A destination has entered backup state.
    BackedUp,
    /// A candidate artifact has become visible.
    Published,
    /// The operation manifest is about to become visible.
    ManifestLast,
    /// Rollback is restoring prior state.
    Rollback,
}

/// Optional observation seam; production uses the no-op implementation.
pub trait PublicationObserver {
    /// Called at a transaction checkpoint and may inject one typed failure.
    fn checkpoint(
        &self,
        _checkpoint: PublicationCheckpoint,
        _path: &RelativeOperationPath,
    ) -> Result<(), EngineDiagnostic> {
        Ok(())
    }
}

impl PublicationObserver for () {}

/// Validates manifest ownership and computes the complete sorted transaction.
pub fn verify_ownership(
    root: &OperationRoot,
    previous: Option<&OperationManifest>,
    candidate: &OperationCandidate,
) -> Result<PublicationPlan, EngineDiagnostic> {
    let mut amended_files = BTreeMap::<RelativeOperationPath, Vec<u8>>::new();
    validate_amendments(candidate, &mut amended_files)?;
    let addressed = candidate
        .artifacts
        .keys()
        .chain(amended_files.keys())
        .chain(candidate.created_files.keys())
        .chain(candidate.removed.iter())
        .chain(std::iter::once(&candidate.manifest_path))
        .collect::<Vec<_>>();
    OperationRoot::validate_distinct(addressed.iter().copied())?;
    let mut unique = BTreeSet::new();
    for path in &addressed {
        if !unique.insert(path.as_str()) {
            return Err(stale(path.as_str(), "publication destinations overlap"));
        }
    }
    if candidate.artifacts.contains_key(&candidate.manifest_path)
        || candidate
            .manifest
            .artifacts
            .contains_key(&candidate.manifest_path)
    {
        return Err(stale(
            candidate.manifest_path.as_str(),
            "operation manifest must remain outside its own artifact map",
        ));
    }
    if candidate.artifacts.len() != candidate.manifest.artifacts.len() {
        return Err(stale(
            "operation-manifest.artifacts",
            "candidate and operation manifest own different artifact sets",
        ));
    }
    for (path, artifact) in &candidate.artifacts {
        let Some(record) = candidate.manifest.artifacts.get(path) else {
            return Err(stale(
                path.as_str(),
                "candidate artifact has no manifest authority",
            ));
        };
        if digest(&artifact.content) != record.digest || artifact.kind != record.kind {
            return Err(stale(
                path.as_str(),
                "candidate bytes differ from the operation manifest record",
            ));
        }
    }

    if let Some(previous) = previous {
        require_compatible(previous, &candidate.manifest)?;
        let expected_manifest_digest =
            candidate.previous_manifest_digest.as_ref().ok_or_else(|| {
                stale(
                    candidate.manifest_path.as_str(),
                    "prior manifest digest is required before replacement",
                )
            })?;
        let current_manifest = root.read(&candidate.manifest_path).map_err(|_| {
            stale(
                candidate.manifest_path.as_str(),
                "prior operation manifest is missing or not regular",
            )
        })?;
        if &digest(&current_manifest) != expected_manifest_digest {
            return Err(stale(
                candidate.manifest_path.as_str(),
                "prior operation manifest changed after it was loaded",
            ));
        }
        for (path, record) in &previous.artifacts {
            let current = root.read(path).map_err(|_| {
                stale(
                    path.as_str(),
                    "previously owned artifact is missing or not regular",
                )
            })?;
            if digest(&current) != record.digest {
                return Err(stale(
                    path.as_str(),
                    "previously owned artifact digest is stale",
                ));
            }
        }
        for (coordinate, record) in &previous.amendments {
            if coordinate.file() != &record.file || coordinate.semantic_key() != &record.coordinate
            {
                return Err(stale(
                    coordinate.file().as_str(),
                    "prior semantic amendment coordinate is internally inconsistent",
                ));
            }
            let current = root.read(&record.file).map_err(|_| {
                stale(
                    record.file.as_str(),
                    "previously amended file is missing or not regular",
                )
            })?;
            if semantic_amendment_digest(record.kind, &record.coordinate, &current).map_err(
                |_| {
                    stale(
                        record.file.as_str(),
                        "previously owned semantic value is no longer parseable",
                    )
                },
            )? != record.semantic_digest
            {
                return Err(stale(
                    record.file.as_str(),
                    "previously owned semantic value changed after publication",
                ));
            }
        }
    } else if root.exists(&candidate.manifest_path) {
        return Err(ownership(
            candidate.manifest_path.as_str(),
            "unknown bytes occupy the operation manifest destination",
        ));
    }

    let prior_paths: BTreeSet<RelativeOperationPath> = previous
        .map(|manifest| manifest.artifacts.keys().cloned().collect())
        .unwrap_or_default();
    if !candidate
        .retained_previous_artifacts
        .is_subset(&prior_paths)
    {
        return Err(stale(
            "operation-candidate.retained-previous-artifacts",
            "baseline migration retained a path without prior whole-file authority",
        ));
    }
    if previous.is_some_and(|manifest| {
        !manifest
            .amendments
            .keys()
            .all(|coordinate| candidate.amendments.contains_key(coordinate))
    }) {
        return Err(stale(
            "operation-manifest.amendments",
            "semantic ownership cannot be silently discarded during regeneration",
        ));
    }
    let mut writes = BTreeMap::new();
    let mut changes = Vec::new();
    for (path, artifact) in &candidate.artifacts {
        if root.exists(path) && !prior_paths.contains(path) {
            return Err(ownership(
                path.as_str(),
                "unknown bytes occupy a generated artifact destination",
            ));
        }
        let kind = if prior_paths.contains(path) {
            let current = root.read(path)?;
            if current == artifact.content {
                continue;
            }
            PublicationChangeKind::Change
        } else {
            PublicationChangeKind::Add
        };
        writes.insert(path.clone(), artifact.content.clone());
        changes.push(PublicationChange {
            path: path.clone(),
            kind,
        });
    }
    for (path, content) in amended_files {
        let candidates = candidate
            .amendments
            .iter()
            .filter(|(coordinate, _)| coordinate.file() == &path)
            .map(|(_, amendment)| amendment)
            .collect::<Vec<_>>();
        let prior_file_digest = candidates
            .first()
            .and_then(|amendment| amendment.prior_file_digest.as_ref());
        if candidates
            .iter()
            .any(|amendment| amendment.prior_file_digest.as_ref() != prior_file_digest)
        {
            return Err(stale(
                path.as_str(),
                "semantic amendments disagree on their authored-file snapshot",
            ));
        }
        let current = if root.exists(&path) {
            Some(root.read(&path)?)
        } else {
            None
        };
        if current.as_deref().map(digest).as_ref() != prior_file_digest {
            return Err(stale(
                path.as_str(),
                "authored file changed after project reconciliation",
            ));
        }
        for (coordinate, amendment) in candidate
            .amendments
            .iter()
            .filter(|(coordinate, _)| coordinate.file() == &path)
        {
            let previous_record = previous.and_then(|manifest| manifest.amendments.get(coordinate));
            if let Some(record) = previous_record {
                if amendment.prior_semantic_digest.as_ref() != Some(&record.semantic_digest) {
                    return Err(stale(
                        path.as_str(),
                        "amendment candidate was not based on the previously owned value",
                    ));
                }
            } else if let Some(prior) = &amendment.prior_semantic_digest {
                let Some(current) = &current else {
                    return Err(stale(
                        path.as_str(),
                        "amendment claims a missing prior value",
                    ));
                };
                if semantic_amendment_digest(amendment.kind, coordinate.semantic_key(), current)
                    .map_err(|_| {
                        stale(
                            path.as_str(),
                            "amendment prior semantic value is no longer parseable",
                        )
                    })?
                    != *prior
                {
                    return Err(stale(
                        path.as_str(),
                        "amendment prior semantic value is stale",
                    ));
                }
            }
        }
        let kind = if current.is_some() {
            if current.as_deref() == Some(content.as_slice()) {
                continue;
            }
            PublicationChangeKind::Change
        } else {
            PublicationChangeKind::Add
        };
        writes.insert(path.clone(), content);
        changes.push(PublicationChange { path, kind });
    }
    for (path, content) in &candidate.created_files {
        if root.exists(path) {
            return Err(ownership(
                path.as_str(),
                "unknown bytes occupy a project support-file destination",
            ));
        }
        writes.insert(path.clone(), content.clone());
        changes.push(PublicationChange {
            path: path.clone(),
            kind: PublicationChangeKind::Add,
        });
    }
    let mut removals = BTreeSet::new();
    for path in prior_paths {
        if !candidate.artifacts.contains_key(&path)
            && !candidate.retained_previous_artifacts.contains(&path)
        {
            removals.insert(path.clone());
            changes.push(PublicationChange {
                path,
                kind: PublicationChangeKind::Remove,
            });
        }
    }
    if removals != candidate.removed {
        return Err(stale(
            "operation-candidate.removed",
            "candidate removal set is not the complete prior-owned difference",
        ));
    }
    changes.sort();
    let manifest_bytes = canonical_bytes(&candidate.manifest).map_err(|_| {
        stale(
            candidate.manifest_path.as_str(),
            "operation manifest could not be canonically encoded",
        )
    })?;
    Ok(PublicationPlan {
        writes,
        removals,
        manifest_path: candidate.manifest_path.clone(),
        manifest_bytes,
        changes,
    })
}

/// Verifies a manifest-free authored-file transaction.
///
/// One changed path is used as the journal's final visibility barrier; it does not
/// become a control manifest or acquire whole-file ownership. This reuses the same
/// staging, backup, flush, and rollback machinery without inventing initialization
/// provenance that does not exist yet.
pub fn verify_authored_publication(
    root: &OperationRoot,
    candidate: &AuthoredPublicationCandidate,
) -> Result<Option<PublicationPlan>, EngineDiagnostic> {
    OperationRoot::validate_distinct(candidate.files.keys())?;
    let mut writes = BTreeMap::new();
    let mut changes = Vec::new();
    for (path, (prior_digest, content)) in &candidate.files {
        let current = root.exists(path).then(|| root.read(path)).transpose()?;
        if current.as_deref().map(digest).as_ref() != prior_digest.as_ref() {
            return Err(stale(
                path.as_str(),
                "authored project file changed after initialization planning",
            ));
        }
        if current.as_deref() == Some(content.as_slice()) {
            continue;
        }
        changes.push(PublicationChange {
            path: path.clone(),
            kind: if current.is_some() {
                PublicationChangeKind::Change
            } else {
                PublicationChangeKind::Add
            },
        });
        writes.insert(path.clone(), content.clone());
    }
    let Some((barrier_path, barrier_bytes)) = writes.pop_last() else {
        return Ok(None);
    };
    Ok(Some(PublicationPlan {
        writes,
        removals: BTreeSet::new(),
        manifest_path: barrier_path,
        manifest_bytes: barrier_bytes,
        changes,
    }))
}

fn validate_amendments(
    candidate: &OperationCandidate,
    amended_files: &mut BTreeMap<RelativeOperationPath, Vec<u8>>,
) -> Result<(), EngineDiagnostic> {
    if candidate.amendments.len() != candidate.manifest.amendments.len() {
        return Err(stale(
            "operation-manifest.amendments",
            "candidate and operation manifest own different semantic amendment sets",
        ));
    }
    for (coordinate, amendment) in &candidate.amendments {
        let Some(record) = candidate.manifest.amendments.get(coordinate) else {
            return Err(stale(
                coordinate.file().as_str(),
                "semantic amendment has no manifest authority",
            ));
        };
        if record.kind != amendment.kind
            || record.file != *coordinate.file()
            || record.coordinate != *coordinate.semantic_key()
            || record.semantic_digest != amendment.next_semantic_digest
            || semantic_amendment_digest(
                amendment.kind,
                coordinate.semantic_key(),
                &amendment.complete_file_bytes,
            )
            .map_err(|_| {
                stale(
                    coordinate.file().as_str(),
                    "semantic amendment candidate is not parseable",
                )
            })? != amendment.next_semantic_digest
        {
            return Err(stale(
                coordinate.file().as_str(),
                "semantic amendment differs from its manifest record",
            ));
        }
        match amended_files.get(coordinate.file()) {
            Some(bytes) if bytes != &amendment.complete_file_bytes => {
                return Err(stale(
                    coordinate.file().as_str(),
                    "semantic amendments for one file propose different complete bytes",
                ));
            }
            Some(_) => {}
            None => {
                amended_files.insert(
                    coordinate.file().clone(),
                    amendment.complete_file_bytes.clone(),
                );
            }
        }
    }
    for retained in &candidate.retained_previous_artifacts {
        if candidate.artifacts.contains_key(retained) || !amended_files.contains_key(retained) {
            return Err(stale(
                retained.as_str(),
                "retained baseline artifact must transfer to semantic amendment ownership",
            ));
        }
    }
    Ok(())
}

/// Validates generated-module ownership and computes a manifest-last transaction.
///
/// Target and generator changes are allowed: their effect is already reflected in the
/// candidate bytes and per-asset input digests. Compatibility is limited to the strict
/// manifest format and its fixed destination, which prevents an old manifest from
/// silently authorizing a different ownership protocol.
pub fn verify_module_ownership(
    root: &OperationRoot,
    previous: Option<&GeneratedModuleAssets>,
    candidate: &ModuleAssetCandidate,
) -> Result<PublicationPlan, EngineDiagnostic> {
    let manifest = &candidate.rendered.manifest;
    validate_manifest(manifest).map_err(|_| module_stale("generated-module-assets"))?;
    let manifest_path = module_path(&manifest.manifest_path)?;
    let manifest_bytes =
        canonical_bytes(manifest).map_err(|_| module_stale("generated-module-assets"))?;
    let Some(rendered_manifest) = candidate.rendered.files.get(&manifest.manifest_path) else {
        return Err(module_stale(manifest.manifest_path.as_str()));
    };
    if rendered_manifest != &manifest_bytes
        || candidate.rendered.files.len() != manifest.assets.len() + 1
    {
        return Err(module_stale("generated-module-assets.files"));
    }

    let mut artifacts = BTreeMap::new();
    for (path, record) in &manifest.assets {
        if path == &manifest.manifest_path {
            return Err(module_stale(path.as_str()));
        }
        let Some(content) = candidate.rendered.files.get(path) else {
            return Err(module_stale(path.as_str()));
        };
        if record.path != *path || record.digest.as_str() != digest(content).as_str() {
            return Err(module_stale(path.as_str()));
        }
        artifacts.insert(
            module_path(path)?,
            CandidateArtifact {
                kind: module_artifact_kind(path),
                content: content.clone(),
                ownership: crate::ArtifactOwnership::Generator,
            },
        );
    }

    let previous_records = if let Some(previous) = previous {
        if previous.format_version != manifest.format_version
            || previous.manifest_path != manifest.manifest_path
        {
            return Err(module_stale("generated-module-assets.compatibility"));
        }
        validate_manifest(previous)
            .map_err(|_| module_stale("generated-module-assets.compatibility"))?;
        let expected_manifest_digest = candidate
            .previous_manifest_digest
            .as_ref()
            .ok_or_else(|| module_stale("generated-module-assets.previous-manifest-digest"))?;
        let current_manifest = root
            .read(&manifest_path)
            .map_err(|_| module_stale(manifest.manifest_path.as_str()))?;
        let previous_bytes =
            canonical_bytes(previous).map_err(|_| module_stale("generated-module-assets"))?;
        if &digest(&current_manifest) != expected_manifest_digest
            || current_manifest != previous_bytes
        {
            return Err(module_stale(manifest.manifest_path.as_str()));
        }
        let mut records = BTreeMap::new();
        for (path, record) in &previous.assets {
            let path = module_path(path)?;
            let current = root.read(&path).map_err(|_| module_stale(path.as_str()))?;
            if digest(&current).as_str() != record.digest.as_str() {
                return Err(module_stale(path.as_str()));
            }
            records.insert(path, record.digest.as_str().to_owned());
        }
        Some(records)
    } else {
        if root.exists(&manifest_path) {
            return Err(ownership(
                manifest_path.as_str(),
                "unknown bytes occupy the generated-module manifest destination",
            ));
        }
        None
    };

    let prior_paths = previous_records
        .as_ref()
        .map(|records| records.keys().cloned().collect::<BTreeSet<_>>())
        .unwrap_or_default();
    let mut writes = BTreeMap::new();
    let mut changes = Vec::new();
    for (path, artifact) in &artifacts {
        if root.exists(path) && !prior_paths.contains(path) {
            return Err(ownership(
                path.as_str(),
                "unknown bytes occupy a generated module asset destination",
            ));
        }
        let kind = if prior_paths.contains(path) {
            if root.read(path)? == artifact.content {
                continue;
            }
            PublicationChangeKind::Change
        } else {
            PublicationChangeKind::Add
        };
        writes.insert(path.clone(), artifact.content.clone());
        changes.push(PublicationChange {
            path: path.clone(),
            kind,
        });
    }
    let mut removals = BTreeSet::new();
    for path in prior_paths {
        if !artifacts.contains_key(&path) {
            removals.insert(path.clone());
            changes.push(PublicationChange {
                path,
                kind: PublicationChangeKind::Remove,
            });
        }
    }
    OperationRoot::validate_distinct(
        artifacts
            .keys()
            .chain(removals.iter())
            .chain(std::iter::once(&manifest_path)),
    )?;
    changes.sort();
    Ok(PublicationPlan {
        writes,
        removals,
        manifest_path,
        manifest_bytes,
        changes,
    })
}

/// Publishes one verified plan with the operation manifest last.
pub fn publish(
    root: &OperationRoot,
    plan: PublicationPlan,
) -> Result<PublicationOutcome, EngineDiagnostic> {
    publish_with(root, plan, &())
}

/// Publishes with a deterministic observer for cancellation and fault tests.
pub fn publish_with(
    root: &OperationRoot,
    plan: PublicationPlan,
    observer: &impl PublicationObserver,
) -> Result<PublicationOutcome, EngineDiagnostic> {
    let lock = File::open(root.absolute()).map_err(|_| publication("publication-lock"))?;
    fs4::FileExt::lock(&lock).map_err(|_| publication("publication-lock"))?;
    let manifest_changed =
        !root.exists(&plan.manifest_path) || root.read(&plan.manifest_path)? != plan.manifest_bytes;
    // An identical replay must not manufacture a filesystem change merely to
    // re-publish the same transaction barrier. This also prevents file timestamps
    // from becoming hidden semantic input to downstream changeset calculation.
    if plan.writes.is_empty() && plan.removals.is_empty() && !manifest_changed {
        return Ok(PublicationOutcome {
            manifest_digest: digest(&plan.manifest_bytes),
            changes: plan.changes,
            manifest_changed: false,
        });
    }
    let transaction = TRANSACTION_SEQUENCE.fetch_add(1, Ordering::Relaxed);
    let mut staged = BTreeMap::new();
    let mut created_directories = BTreeSet::new();
    let staging_result = (|| {
        for (path, bytes) in plan
            .writes
            .iter()
            .chain(std::iter::once((&plan.manifest_path, &plan.manifest_bytes)))
        {
            let before = missing_parents(root, path);
            let parent = root.ensure_parent(path)?;
            created_directories.extend(before);
            let stage = sibling_path(&parent, path, "stage", transaction);
            write_staged(&stage, bytes).map_err(|_| publication(path.as_str()))?;
            staged.insert(path.clone(), stage);
            observer.checkpoint(PublicationCheckpoint::Staged, path)?;
        }
        Ok(())
    })();
    if let Err(error) = staging_result {
        for stage in staged.values() {
            let _ = fs::remove_file(stage);
        }
        for directory in created_directories.iter().rev() {
            let _ = fs::remove_dir(directory);
        }
        return Err(error);
    }

    let mut journal = Vec::new();
    let result = (|| {
        for path in plan.writes.keys().chain(plan.removals.iter()) {
            apply_one(
                root,
                path,
                staged.get(path).cloned(),
                transaction,
                observer,
                &mut journal,
            )?;
            staged.remove(path);
        }
        observer.checkpoint(PublicationCheckpoint::ManifestLast, &plan.manifest_path)?;
        apply_one(
            root,
            &plan.manifest_path,
            staged.get(&plan.manifest_path).cloned(),
            transaction,
            observer,
            &mut journal,
        )?;
        staged.remove(&plan.manifest_path);
        Ok(())
    })();

    if let Err(primary) = result {
        let rollback = rollback(root, &journal, &created_directories, observer);
        for stage in staged.values() {
            let _ = fs::remove_file(stage);
        }
        return match rollback {
            Ok(()) => Err(primary),
            Err(_) => Err(EngineDiagnostic::new(
                EngineDiagnosticCode::RollbackFailed,
                Some("publication"),
                format!("{primary}; rollback could not restore the prior tree"),
            )
            .with_cause(primary.code)),
        };
    }
    for entry in &journal {
        if let Some(backup) = &entry.backup {
            fs::remove_file(backup).map_err(|_| publication(entry.path.as_str()))?;
        }
    }
    Ok(PublicationOutcome {
        manifest_digest: digest(&plan.manifest_bytes),
        changes: plan.changes,
        manifest_changed,
    })
}

#[derive(Debug)]
struct JournalEntry {
    path: RelativeOperationPath,
    destination: PathBuf,
    backup: Option<PathBuf>,
    published: bool,
}

fn apply_one(
    root: &OperationRoot,
    path: &RelativeOperationPath,
    stage: Option<PathBuf>,
    transaction: u64,
    observer: &impl PublicationObserver,
    journal: &mut Vec<JournalEntry>,
) -> Result<(), EngineDiagnostic> {
    let parent = root.parent_for_write(path)?;
    let destination = path.join_lexically(root.absolute());
    let backup = fs::symlink_metadata(&destination)
        .is_ok()
        .then(|| sibling_path(&parent, path, "backup", transaction));
    if let Some(backup) = &backup {
        fs::rename(&destination, backup).map_err(|_| publication(path.as_str()))?;
        sync_directory(&parent).map_err(|_| publication(path.as_str()))?;
    }
    journal.push(JournalEntry {
        path: path.clone(),
        destination: destination.clone(),
        backup,
        published: false,
    });
    if journal.last().is_some_and(|entry| entry.backup.is_some()) {
        observer.checkpoint(PublicationCheckpoint::BackedUp, path)?;
    }
    if let Some(stage) = stage {
        fs::rename(stage, &destination).map_err(|_| publication(path.as_str()))?;
        sync_directory(&parent).map_err(|_| publication(path.as_str()))?;
        journal
            .last_mut()
            .expect("journal entry was just added")
            .published = true;
    }
    observer.checkpoint(PublicationCheckpoint::Published, path)
}

fn rollback(
    root: &OperationRoot,
    journal: &[JournalEntry],
    created_directories: &BTreeSet<PathBuf>,
    observer: &impl PublicationObserver,
) -> Result<(), EngineDiagnostic> {
    let mut failed = false;
    for entry in journal.iter().rev() {
        if observer
            .checkpoint(PublicationCheckpoint::Rollback, &entry.path)
            .is_err()
        {
            failed = true;
        }
        if entry.published
            && fs::remove_file(&entry.destination).is_err()
            && entry.destination.exists()
        {
            failed = true;
        }
        if let Some(backup) = &entry.backup
            && fs::rename(backup, &entry.destination).is_err()
        {
            failed = true;
        }
        if let Ok(parent) = root.parent_for_write(&entry.path) {
            let _ = sync_directory(&parent);
        }
    }
    for directory in created_directories.iter().rev() {
        if fs::remove_dir(directory).is_err() && directory.exists() {
            failed = true;
        }
    }
    if failed {
        Err(EngineDiagnostic::new(
            EngineDiagnosticCode::RollbackFailed,
            Some("publication"),
            "publication rollback could not restore every prior path",
        ))
    } else {
        Ok(())
    }
}

fn require_compatible(
    previous: &OperationManifest,
    candidate: &OperationManifest,
) -> Result<(), EngineDiagnostic> {
    if previous.operation != candidate.operation
        || previous.target != candidate.target
        || previous.sdk_dependency != candidate.sdk_dependency
        || previous.output_root != candidate.output_root
        || previous.generator != candidate.generator
    {
        return Err(stale(
            "operation-manifest",
            "prior operation manifest is incompatible with this generator and target",
        ));
    }
    Ok(())
}

fn module_path(path: &GeneratedAssetPath) -> Result<RelativeOperationPath, EngineDiagnostic> {
    RelativeOperationPath::parse(path.as_str()).map_err(|_| module_stale(path.as_str()))
}

fn module_artifact_kind(path: &GeneratedAssetPath) -> crate::ArtifactKind {
    if path.as_str().ends_with(".rs") {
        crate::ArtifactKind::RustSource
    } else {
        crate::ArtifactKind::ControlManifest
    }
}

fn module_stale(coordinate: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::OperationManifestStale,
        Some(coordinate),
        "generated module assets do not match their ownership manifest",
    )
}

fn missing_parents(root: &OperationRoot, path: &RelativeOperationPath) -> BTreeSet<PathBuf> {
    let components = path.as_str().split('/').collect::<Vec<_>>();
    let mut current = root.absolute().to_path_buf();
    let mut missing = BTreeSet::new();
    for component in &components[..components.len().saturating_sub(1)] {
        current.push(component);
        if fs::symlink_metadata(&current).is_err() {
            missing.insert(current.clone());
        }
    }
    missing
}

fn sibling_path(
    parent: &Path,
    path: &RelativeOperationPath,
    kind: &str,
    transaction: u64,
) -> PathBuf {
    let name = path.as_str().rsplit('/').next().unwrap_or("artifact");
    parent.join(format!(".{name}.dagger-{kind}-{transaction}"))
}

fn write_staged(path: &Path, bytes: &[u8]) -> std::io::Result<()> {
    let mut file = OpenOptions::new().write(true).create_new(true).open(path)?;
    file.write_all(bytes)?;
    file.sync_all()
}

fn sync_directory(path: &Path) -> std::io::Result<()> {
    File::open(path)?.sync_all()
}

fn digest(bytes: &[u8]) -> Sha256Digest {
    let digest = Sha256::digest(bytes);
    format!("sha256:{digest:x}")
        .parse()
        .expect("SHA-256 formatting must satisfy the digest scalar")
}

fn stale(coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::OperationManifestStale,
        Some(coordinate),
        message,
    )
}

fn ownership(coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::OwnershipConflict,
        Some(coordinate),
        message,
    )
}

fn publication(coordinate: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::PublicationFailed,
        Some(coordinate),
        "failure-atomic publication could not complete",
    )
}
