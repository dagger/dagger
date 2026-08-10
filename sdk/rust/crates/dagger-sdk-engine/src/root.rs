//! Filesystem authority confined to one explicit operation root.
//!
//! Canonical models retain only relative paths. Absolute paths exist transiently in
//! this private capability, which rejects symlinks and aliases before access and
//! revalidates parents immediately before publication.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Component, Path, PathBuf};

use crate::RelativeOperationPath;
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};

/// Maximum bytes accepted from one operation input or control file.
pub const MAX_INPUT_BYTES: u64 = 16 * 1024 * 1024;

/// Private directory capability used for all operation filesystem access.
#[derive(Clone, Debug)]
pub struct OperationRoot {
    root: PathBuf,
}

impl OperationRoot {
    /// Opens an existing real directory as the sole filesystem authority.
    pub fn open(path: impl AsRef<Path>) -> Result<Self, EngineDiagnostic> {
        let canonical = fs::canonicalize(path.as_ref()).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "operation-root",
                "operation root does not resolve to an existing directory",
            )
        })?;
        let metadata = fs::metadata(&canonical).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "operation-root",
                "operation root metadata is unavailable",
            )
        })?;
        if !metadata.is_dir() {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "operation-root",
                "operation root must be a directory",
            ));
        }
        Ok(Self { root: canonical })
    }

    /// Resolves an existing regular file after rejecting every symlink component.
    pub fn regular_file(&self, path: &RelativeOperationPath) -> Result<PathBuf, EngineDiagnostic> {
        let resolved = self.resolve_existing(path)?;
        let metadata = fs::symlink_metadata(&resolved).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                path.as_str(),
                "required file is missing",
            )
        })?;
        if !metadata.file_type().is_file() {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                path.as_str(),
                "operation input must be a regular file",
            ));
        }
        Ok(resolved)
    }

    /// Resolves an existing path without following symlinks inside the capability.
    pub fn resolve_existing(
        &self,
        path: &RelativeOperationPath,
    ) -> Result<PathBuf, EngineDiagnostic> {
        let mut current = self.root.clone();
        for component in path.as_str().split('/') {
            reject_case_alias(&current, component, path)?;
            current.push(component);
            let metadata = fs::symlink_metadata(&current).map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    path.as_str(),
                    "operation-relative path does not exist",
                )
            })?;
            if metadata.file_type().is_symlink() {
                return Err(diagnostic(
                    EngineDiagnosticCode::OutputSymlinkEscape,
                    path.as_str(),
                    "symlink components are not admitted by the operation root",
                ));
            }
        }
        self.require_confined(&current, path)?;
        Ok(current)
    }

    /// Resolves and revalidates an existing destination parent for a pending write.
    pub fn parent_for_write(
        &self,
        path: &RelativeOperationPath,
    ) -> Result<PathBuf, EngineDiagnostic> {
        let mut components = path.as_str().rsplitn(2, '/');
        let _file_name = components.next();
        let Some(parent) = components.next() else {
            reject_case_alias(&self.root, path.as_str(), path)?;
            return Ok(self.root.clone());
        };
        let parent = RelativeOperationPath::parse(parent).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                path.as_str(),
                "destination parent is not canonical",
            )
        })?;
        let resolved = self.resolve_existing(&parent)?;
        if !fs::metadata(&resolved).is_ok_and(|metadata| metadata.is_dir()) {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                path.as_str(),
                "destination parent must be an existing directory",
            ));
        }
        let file_name = path.as_str().rsplit('/').next().unwrap_or(path.as_str());
        reject_case_alias(&resolved, file_name, path)?;
        Ok(resolved)
    }

    /// Converts an absolute Cargo path back into a canonical operation-relative path.
    pub fn relative(&self, path: &Path) -> Result<RelativeOperationPath, EngineDiagnostic> {
        let normalized = lexical_absolute(path).ok_or_else(|| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "cargo-metadata.path",
                "Cargo returned a non-normalized path",
            )
        })?;
        let relative = normalized.strip_prefix(&self.root).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "cargo-metadata.path",
                "Cargo returned a path outside the operation root",
            )
        })?;
        let spelling = relative
            .components()
            .map(|component| component.as_os_str().to_string_lossy())
            .collect::<Vec<_>>()
            .join("/");
        RelativeOperationPath::parse(&spelling).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                "cargo-metadata.path",
                "Cargo returned a non-canonical operation path",
            )
        })
    }

    /// Reads a bounded regular file without allowing special-file substitution.
    pub fn read(&self, path: &RelativeOperationPath) -> Result<Vec<u8>, EngineDiagnostic> {
        let absolute = self.regular_file(path)?;
        let metadata = fs::metadata(&absolute).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                path.as_str(),
                "operation input metadata is unavailable",
            )
        })?;
        if metadata.len() > MAX_INPUT_BYTES {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                path.as_str(),
                "operation input exceeds its byte bound",
            ));
        }
        fs::read(absolute).map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                path.as_str(),
                "operation input could not be read",
            )
        })
    }

    /// Rejects case-fold aliases before a plan can address the same path twice.
    pub fn validate_distinct<'a>(
        paths: impl IntoIterator<Item = &'a RelativeOperationPath>,
    ) -> Result<(), EngineDiagnostic> {
        let mut folded = BTreeMap::<String, &str>::new();
        for path in paths {
            let key = path.as_str().to_lowercase();
            if let Some(previous) = folded.insert(key, path.as_str())
                && previous != path.as_str()
            {
                return Err(diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    path.as_str(),
                    "operation paths collide after portable case folding",
                ));
            }
        }
        Ok(())
    }

    /// Returns whether an operation-relative path currently exists without following it.
    pub fn exists(&self, path: &RelativeOperationPath) -> bool {
        fs::symlink_metadata(path.join_lexically(&self.root)).is_ok()
    }

    /// Borrows the private absolute root for fixed child-process working directories.
    pub(crate) fn absolute(&self) -> &Path {
        &self.root
    }

    /// Creates missing directory components while refusing aliases and symlinks.
    pub(crate) fn ensure_parent(
        &self,
        path: &RelativeOperationPath,
    ) -> Result<PathBuf, EngineDiagnostic> {
        let mut current = self.root.clone();
        let components = path.as_str().split('/').collect::<Vec<_>>();
        for component in &components[..components.len().saturating_sub(1)] {
            reject_case_alias(&current, component, path)?;
            current.push(component);
            match fs::symlink_metadata(&current) {
                Ok(metadata) if metadata.file_type().is_dir() => {}
                Ok(_) => {
                    return Err(diagnostic(
                        EngineDiagnosticCode::OutputSymlinkEscape,
                        path.as_str(),
                        "destination parent is not a real directory",
                    ));
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                    fs::create_dir(&current).map_err(|_| {
                        diagnostic(
                            EngineDiagnosticCode::PublicationFailed,
                            path.as_str(),
                            "destination parent could not be created",
                        )
                    })?;
                }
                Err(_) => {
                    return Err(diagnostic(
                        EngineDiagnosticCode::PublicationFailed,
                        path.as_str(),
                        "destination parent could not be inspected",
                    ));
                }
            }
        }
        self.require_confined(&current, path)?;
        Ok(current)
    }

    fn require_confined(
        &self,
        absolute: &Path,
        coordinate: &RelativeOperationPath,
    ) -> Result<(), EngineDiagnostic> {
        if !absolute.starts_with(&self.root) {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputSymlinkEscape,
                coordinate.as_str(),
                "resolved path crossed the operation root",
            ));
        }
        Ok(())
    }
}

fn lexical_absolute(path: &Path) -> Option<PathBuf> {
    if !path.is_absolute() {
        return None;
    }
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::Prefix(prefix) => normalized.push(prefix.as_os_str()),
            Component::RootDir => normalized.push(Path::new("/")),
            Component::Normal(value) => normalized.push(value),
            Component::CurDir => {}
            Component::ParentDir => {
                if !normalized.pop() {
                    return None;
                }
            }
        }
    }
    Some(normalized)
}

fn diagnostic(code: EngineDiagnosticCode, coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}

fn reject_case_alias(
    parent: &Path,
    requested: &str,
    coordinate: &RelativeOperationPath,
) -> Result<(), EngineDiagnostic> {
    let folded = requested.to_lowercase();
    let entries = match fs::read_dir(parent) {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(_) => {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                coordinate.as_str(),
                "operation path parent could not be enumerated safely",
            ));
        }
    };
    for entry in entries {
        let name = entry
            .map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OutputPathEscape,
                    coordinate.as_str(),
                    "operation path parent could not be enumerated safely",
                )
            })?
            .file_name();
        let name = name.to_string_lossy();
        if name.to_lowercase() == folded && name != requested {
            return Err(diagnostic(
                EngineDiagnosticCode::OutputPathEscape,
                coordinate.as_str(),
                "operation path collides with an existing portable case alias",
            ));
        }
    }
    Ok(())
}

/// Rejects overlap between generated and ignored paths in one operation plan.
pub fn validate_path_sets(
    generated: &BTreeSet<RelativeOperationPath>,
    ignored: &BTreeSet<RelativeOperationPath>,
) -> Result<(), EngineDiagnostic> {
    OperationRoot::validate_distinct(generated.iter().chain(ignored))?;
    if let Some(path) = generated.intersection(ignored).next() {
        return Err(diagnostic(
            EngineDiagnosticCode::OutputPathEscape,
            path.as_str(),
            "a generated artifact cannot also be an ignored cache path",
        ));
    }
    Ok(())
}
