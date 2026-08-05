//! Contained local source loading for normal verification.
//!
//! The normal-verification adapter accepts only a [`ValidatedAuthorityRegistry`], walks only
//! explicitly registered local repository roots, canonicalizes every selected entry, and returns
//! exact bytes in filesystem-free [`SourceBundle`] values. It has no network client. Immutable
//! retrieval for an explicit target transition is a separate trait so normal verification cannot
//! acquire network authority accidentally.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::io;
use std::path::{Path, PathBuf};

use thiserror::Error;

use crate::authority::{
    AuthoritySourceBundles, SourceBundle, ValidatedAuthorityRegistry, selector_path,
};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticSet, ToolError};
use crate::model::{CommitSha, RepositoryId, RepositoryRelativePath, SourceSelector};

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Local checkout roots keyed by the immutable repository identities in the target.
///
/// Host paths remain adapter-only data and are never serialized into contract artifacts or
/// diagnostics.
pub struct RepositoryRoots {
    roots: BTreeMap<RepositoryId, PathBuf>,
}

impl RepositoryRoots {
    /// Constructs local repository-root bindings.
    pub fn new(roots: impl IntoIterator<Item = (RepositoryId, PathBuf)>) -> Self {
        Self {
            roots: roots.into_iter().collect(),
        }
    }

    /// Borrows the configured host root for a repository identity.
    pub fn get(&self, repository: &RepositoryId) -> Option<&Path> {
        self.roots.get(repository).map(PathBuf::as_path)
    }
}

#[derive(Debug, Error)]
/// Failure to construct contained source bundles.
pub enum SourceLoadError {
    #[error(transparent)]
    Contract(#[from] DiagnosticSet),
    #[error(transparent)]
    Tool(#[from] ToolError),
}

/// Explicit extension boundary for retrieving a repository at a full immutable commit.
///
/// Normal verification never accepts or invokes this adapter. Only transition orchestration may
/// materialize a missing immutable checkout and then pass its local root to [`RepositoryRoots`].
pub trait ImmutableTransitionRetrieval {
    /// Materializes `repository` at `revision` and returns a local checkout root.
    fn materialize(
        &self,
        repository: &RepositoryId,
        revision: &CommitSha,
    ) -> Result<PathBuf, ToolError>;
}

/// Loads exact selected source bytes without network access or extractor filesystem access.
///
/// Missing selections and containment defects accumulate as contract diagnostics. Permission and
/// other host failures remain redacted operational errors because they do not describe contract
/// truth.
pub fn load_source_bundles(
    registry: &ValidatedAuthorityRegistry,
    roots: &RepositoryRoots,
) -> Result<AuthoritySourceBundles, SourceLoadError> {
    let mut diagnostics = Vec::new();
    let mut bundles = AuthoritySourceBundles::default();

    for (authority_id, source) in &registry.authorities {
        let Some(configured_root) = roots.get(&source.repository) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityRepositoryInvalid,
                authority_id.to_string(),
                None,
                "registered repository has no local normal-verification root",
            ));
            continue;
        };
        let canonical_root = match fs::canonicalize(configured_root) {
            Ok(root) if root.is_dir() => root,
            Ok(_) => {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::AuthorityRepositoryInvalid,
                    authority_id.to_string(),
                    None,
                    "registered local repository root is not a directory",
                ));
                continue;
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::AuthorityRepositoryInvalid,
                    authority_id.to_string(),
                    None,
                    "registered local repository root does not exist",
                ));
                continue;
            }
            Err(error) => {
                return Err(ToolError::io("canonicalize repository root", &error, None).into());
            }
        };

        let mut bundle = SourceBundle::default();
        for selector in source.include.as_slice() {
            let mut active_directories = BTreeSet::new();
            collect_selector(
                authority_id.to_string(),
                &canonical_root,
                selector,
                &mut active_directories,
                &mut bundle,
                &mut diagnostics,
            )?;
        }
        bundles.insert(authority_id.clone(), bundle);
    }

    match DiagnosticSet::new(diagnostics) {
        Some(diagnostics) => Err(diagnostics.into()),
        None => Ok(bundles),
    }
}

fn collect_selector(
    authority_id: String,
    root: &Path,
    selector: &SourceSelector,
    active_directories: &mut BTreeSet<PathBuf>,
    bundle: &mut SourceBundle,
    diagnostics: &mut Vec<ContractDiagnostic>,
) -> Result<(), ToolError> {
    collect_entry(
        &authority_id,
        root,
        selector_path(selector),
        active_directories,
        bundle,
        diagnostics,
    )
}

fn collect_entry(
    authority_id: &str,
    root: &Path,
    logical_path: &RepositoryRelativePath,
    active_directories: &mut BTreeSet<PathBuf>,
    bundle: &mut SourceBundle,
    diagnostics: &mut Vec<ContractDiagnostic>,
) -> Result<(), ToolError> {
    let unresolved = root.join(logical_path.as_str());
    let canonical = match fs::canonicalize(&unresolved) {
        Ok(path) => path,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthoritySourceEmpty,
                authority_id,
                None,
                format!("selected path {logical_path} does not exist"),
            ));
            return Ok(());
        }
        Err(error) => {
            return Err(ToolError::io(
                "canonicalize selected source",
                &error,
                Some(logical_path.clone()),
            ));
        }
    };

    // `RepositoryRelativePath` rejects lexical traversal, while this canonical check also closes
    // symlink escapes and platform-specific aliasing before any file bytes are opened.
    if !canonical.starts_with(root) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::AuthorityRepositoryInvalid,
            authority_id,
            None,
            format!("selected path {logical_path} escapes its registered repository root"),
        ));
        return Ok(());
    }

    let metadata = fs::metadata(&canonical).map_err(|error| {
        ToolError::io(
            "inspect selected source",
            &error,
            Some(logical_path.clone()),
        )
    })?;
    if metadata.is_file() {
        let bytes = fs::read(&canonical).map_err(|error| {
            ToolError::io("read selected source", &error, Some(logical_path.clone()))
        })?;
        bundle.insert(logical_path.clone(), bytes);
        return Ok(());
    }
    if !metadata.is_dir() {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::AuthorityRepositoryInvalid,
            authority_id,
            None,
            format!("selected path {logical_path} is neither a regular file nor directory"),
        ));
        return Ok(());
    }

    // Track only the active recursion ancestry. Internal directory aliases remain usable, but a
    // symlink cycle cannot recurse forever or silently broaden the selected source tree.
    if !active_directories.insert(canonical.clone()) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::AuthorityRepositoryInvalid,
            authority_id,
            None,
            format!("selected directory {logical_path} forms a symlink cycle"),
        ));
        return Ok(());
    }

    let entries = fs::read_dir(&canonical).map_err(|error| {
        ToolError::io(
            "enumerate selected source directory",
            &error,
            Some(logical_path.clone()),
        )
    })?;
    for entry in entries {
        let entry = entry.map_err(|error| {
            ToolError::io(
                "enumerate selected source entry",
                &error,
                Some(logical_path.clone()),
            )
        })?;
        let Some(name) = entry.file_name().to_str().map(str::to_owned) else {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityRepositoryInvalid,
                authority_id,
                None,
                format!("selected directory {logical_path} contains a non-UTF-8 path"),
            ));
            continue;
        };
        let child = match RepositoryRelativePath::new(format!("{logical_path}/{name}")) {
            Ok(path) => path,
            Err(_) => {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::AuthorityRepositoryInvalid,
                    authority_id,
                    None,
                    format!("selected directory {logical_path} contains a non-portable child path"),
                ));
                continue;
            }
        };
        collect_entry(
            authority_id,
            root,
            &child,
            active_directories,
            bundle,
            diagnostics,
        )?;
    }
    active_directories.remove(&canonical);
    Ok(())
}
