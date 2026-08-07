//! Native executable discovery over one immutable process snapshot.
//!
//! Explicit-local discovery and compatibility lookup are separate entry points. Both
//! may resolve native symlinks, but neither can download or publish an executable, so
//! selecting an explicit path cannot silently cross into provisioning.

use std::env;
use std::ffi::{OsStr, OsString};
use std::fs::{self, File};
use std::path::{Path, PathBuf};
use std::sync::Arc;

use crate::errors::{CliDiscoveryError, CliDiscoveryErrorKind, DiscoveryPathRole};

/// A safe failure captured while observing native process context.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct NativeContextError;

/// Platform-specific path behavior used without rereading process state.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum NativePathSemantics {
    Unix,
    Windows,
}

impl NativePathSemantics {
    pub(crate) const fn current() -> Self {
        if cfg!(windows) {
            Self::Windows
        } else {
            Self::Unix
        }
    }
}

/// Captured native inputs required by explicit and compatibility lookup.
#[derive(Clone, Eq, PartialEq)]
pub(crate) struct NativeDiscoveryInputs {
    semantics: NativePathSemantics,
    path_entries: Vec<PathBuf>,
    path_extensions: Option<Vec<OsString>>,
    home_dir: Option<PathBuf>,
    current_dir: Result<PathBuf, NativeContextError>,
}

impl NativeDiscoveryInputs {
    pub(crate) fn capture() -> Self {
        let path_entries = env::var_os("PATH")
            .map(|value| env::split_paths(&value).collect())
            .unwrap_or_default();
        let path_extensions = env::var_os("PATHEXT").map(split_path_extensions);
        Self {
            semantics: NativePathSemantics::current(),
            path_entries,
            path_extensions,
            home_dir: dirs::home_dir(),
            current_dir: env::current_dir().map_err(|_| NativeContextError),
        }
    }

    #[cfg(test)]
    pub(crate) fn new(
        semantics: NativePathSemantics,
        path_entries: Vec<PathBuf>,
        path_extensions: Option<Vec<OsString>>,
        home_dir: Option<PathBuf>,
        current_dir: Result<PathBuf, NativeContextError>,
    ) -> Self {
        Self {
            semantics,
            path_entries,
            path_extensions,
            home_dir,
            current_dir,
        }
    }
}

#[cfg(windows)]
fn split_path_extensions(value: OsString) -> Vec<OsString> {
    use std::os::windows::ffi::{OsStrExt, OsStringExt};

    value
        .encode_wide()
        .collect::<Vec<_>>()
        .split(|unit| *unit == u16::from(b';'))
        .filter(|extension| !extension.is_empty())
        .map(OsString::from_wide)
        .collect()
}

#[cfg(not(windows))]
fn split_path_extensions(value: OsString) -> Vec<OsString> {
    value
        .to_string_lossy()
        .split(';')
        .filter(|extension| !extension.is_empty())
        .map(OsString::from)
        .collect()
}

impl std::fmt::Debug for NativeDiscoveryInputs {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("NativeDiscoveryInputs")
            .field("semantics", &self.semantics)
            .field("path_entry_count", &self.path_entries.len())
            .field("path_extensions_present", &self.path_extensions.is_some())
            .field("home_present", &self.home_dir.is_some())
            .field("current_dir_available", &self.current_dir.is_ok())
            .finish()
    }
}

/// Ownership attached to an executable selected for launch.
#[derive(Clone, Eq, PartialEq)]
pub(crate) enum ExecutableLease {
    Unmanaged,
    Cache(CacheExecutionLease),
}

impl std::fmt::Debug for ExecutableLease {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unmanaged => formatter.write_str("Unmanaged"),
            Self::Cache(_) => formatter.write_str("Cache"),
        }
    }
}

/// Exclusive cache ownership retained until the selected executable is opened.
#[derive(Clone)]
pub(crate) struct CacheExecutionLease {
    lock: Arc<File>,
}

impl CacheExecutionLease {
    pub(crate) fn new(lock: File) -> Self {
        Self {
            lock: Arc::new(lock),
        }
    }
}

impl std::fmt::Debug for CacheExecutionLease {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("CacheExecutionLease")
    }
}

impl PartialEq for CacheExecutionLease {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.lock, &other.lock)
    }
}

impl Eq for CacheExecutionLease {}

/// One resolved native executable and its ownership policy.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct LaunchExecutable {
    path: PathBuf,
    lease: ExecutableLease,
}

impl LaunchExecutable {
    pub(crate) fn path(&self) -> &Path {
        &self.path
    }

    pub(crate) fn lease(&self) -> ExecutableLease {
        self.lease.clone()
    }

    pub(crate) fn cached(path: PathBuf, lock: File) -> Self {
        Self {
            path,
            lease: ExecutableLease::Cache(CacheExecutionLease::new(lock)),
        }
    }
}

pub(crate) fn resolve_explicit_cli(
    configured: OsString,
    inputs: &NativeDiscoveryInputs,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    resolve_explicit_cli_with(configured, inputs, &SystemDiscoveryFileSystem)
}

pub(crate) fn resolve_compatibility_path_cli(
    inputs: &NativeDiscoveryInputs,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    resolve_compatibility_path_cli_with(inputs, &SystemDiscoveryFileSystem)
}

fn resolve_compatibility_path_cli_with<F: DiscoveryFileSystem>(
    inputs: &NativeDiscoveryInputs,
    filesystem: &F,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    resolve_bare_name(
        OsStr::new("dagger"),
        inputs,
        DiscoveryPathRole::CompatibilityPath,
        filesystem,
    )
}

fn resolve_explicit_cli_with<F: DiscoveryFileSystem>(
    configured: OsString,
    inputs: &NativeDiscoveryInputs,
    filesystem: &F,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    let role = DiscoveryPathRole::ExplicitLocal;
    if configured.is_empty() {
        return Err(CliDiscoveryError::new(
            CliDiscoveryErrorKind::EmptyExplicitLocal,
            role,
        ));
    }

    let expanded = expand_home(PathBuf::from(configured), inputs, role)?;
    if is_path_shaped(&expanded, inputs.semantics) {
        resolve_path(expanded, inputs, role, filesystem)
    } else {
        resolve_bare_name(expanded.as_os_str(), inputs, role, filesystem)
    }
}

fn expand_home(
    configured: PathBuf,
    inputs: &NativeDiscoveryInputs,
    role: DiscoveryPathRole,
) -> Result<PathBuf, CliDiscoveryError> {
    let text = configured.as_os_str().to_string_lossy();
    let has_home_marker = text == "~" || text.starts_with("~/") || text.starts_with("~\\");
    if !has_home_marker {
        return Ok(configured);
    }
    let home = inputs
        .home_dir
        .as_ref()
        .ok_or_else(|| CliDiscoveryError::new(CliDiscoveryErrorKind::HomeUnavailable, role))?;
    if text == "~" {
        return Ok(home.clone());
    }
    let suffix = text[2..].replace('\\', std::path::MAIN_SEPARATOR_STR);
    Ok(home.join(suffix))
}

fn resolve_path<F: DiscoveryFileSystem>(
    configured: PathBuf,
    inputs: &NativeDiscoveryInputs,
    role: DiscoveryPathRole,
    filesystem: &F,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    let candidate = if is_native_absolute(&configured, inputs.semantics) {
        configured
    } else {
        current_dir(inputs, role)?.join(configured)
    };
    let mut saw_unusable = false;
    for candidate in candidate_names(&candidate, inputs) {
        match inspect_candidate(&candidate, inputs.semantics, filesystem) {
            Ok(executable) => return Ok(executable),
            Err(CliDiscoveryErrorKind::NotExecutable) => saw_unusable = true,
            Err(_) => {}
        }
    }
    Err(CliDiscoveryError::new(
        if saw_unusable {
            CliDiscoveryErrorKind::NotExecutable
        } else {
            CliDiscoveryErrorKind::Lookup
        },
        role,
    ))
}

fn resolve_bare_name<F: DiscoveryFileSystem>(
    name: &OsStr,
    inputs: &NativeDiscoveryInputs,
    role: DiscoveryPathRole,
    filesystem: &F,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    let mut saw_unusable = false;
    for directory in &inputs.path_entries {
        let directory = if is_native_absolute(directory, inputs.semantics) {
            directory.clone()
        } else {
            current_dir(inputs, role)?.join(directory)
        };
        for candidate in candidate_names(&directory.join(name), inputs) {
            match inspect_candidate(&candidate, inputs.semantics, filesystem) {
                Ok(executable) => return Ok(executable),
                Err(CliDiscoveryErrorKind::NotExecutable) => saw_unusable = true,
                Err(_) => {}
            }
        }
    }
    Err(CliDiscoveryError::new(
        if saw_unusable {
            CliDiscoveryErrorKind::NotExecutable
        } else {
            CliDiscoveryErrorKind::Lookup
        },
        role,
    ))
}

fn current_dir(
    inputs: &NativeDiscoveryInputs,
    role: DiscoveryPathRole,
) -> Result<&Path, CliDiscoveryError> {
    inputs
        .current_dir
        .as_deref()
        .map_err(|_| CliDiscoveryError::new(CliDiscoveryErrorKind::NativeContext, role))
}

fn candidate_names(candidate: &Path, inputs: &NativeDiscoveryInputs) -> Vec<PathBuf> {
    if inputs.semantics != NativePathSemantics::Windows || candidate.extension().is_some() {
        return vec![candidate.to_path_buf()];
    }
    if let Some(extensions) = inputs
        .path_extensions
        .as_deref()
        .filter(|extensions| !extensions.is_empty())
    {
        return extensions
            .iter()
            .map(|extension| append_extension(candidate, extension))
            .collect();
    }
    [".COM", ".EXE", ".BAT", ".CMD"]
        .into_iter()
        .map(|extension| append_extension(candidate, OsStr::new(extension)))
        .collect()
}

fn append_extension(candidate: &Path, extension: &OsStr) -> PathBuf {
    let mut value = candidate.as_os_str().to_os_string();
    value.push(extension);
    PathBuf::from(value)
}

fn is_path_shaped(path: &Path, semantics: NativePathSemantics) -> bool {
    let text = path.as_os_str().to_string_lossy();
    is_native_absolute(path, semantics)
        || text.contains('/')
        || text.contains('\\')
        || text.starts_with('.')
}

fn is_native_absolute(path: &Path, semantics: NativePathSemantics) -> bool {
    if path.is_absolute() {
        return true;
    }
    if semantics != NativePathSemantics::Windows {
        return false;
    }
    let text = path.as_os_str().to_string_lossy();
    let text = text.as_bytes();
    text.starts_with(b"\\\\")
        || text.starts_with(b"//")
        || (text.len() >= 3
            && text[0].is_ascii_alphabetic()
            && text[1] == b':'
            && matches!(text[2], b'\\' | b'/'))
}

fn inspect_candidate<F: DiscoveryFileSystem>(
    candidate: &Path,
    semantics: NativePathSemantics,
    filesystem: &F,
) -> Result<LaunchExecutable, CliDiscoveryErrorKind> {
    let resolved = filesystem
        .canonicalize(candidate)
        .map_err(|_| CliDiscoveryErrorKind::Lookup)?;
    let metadata = filesystem
        .metadata(&resolved)
        .map_err(|_| CliDiscoveryErrorKind::Lookup)?;
    if !metadata.regular || (semantics == NativePathSemantics::Unix && !metadata.executable) {
        return Err(CliDiscoveryErrorKind::NotExecutable);
    }
    Ok(LaunchExecutable {
        path: resolved,
        lease: ExecutableLease::Unmanaged,
    })
}

#[derive(Clone, Copy)]
struct ExecutableMetadata {
    regular: bool,
    executable: bool,
}

trait DiscoveryFileSystem {
    fn canonicalize(&self, path: &Path) -> Result<PathBuf, ()>;
    fn metadata(&self, path: &Path) -> Result<ExecutableMetadata, ()>;
}

struct SystemDiscoveryFileSystem;

impl DiscoveryFileSystem for SystemDiscoveryFileSystem {
    fn canonicalize(&self, path: &Path) -> Result<PathBuf, ()> {
        fs::canonicalize(path).map_err(|_| ())
    }

    fn metadata(&self, path: &Path) -> Result<ExecutableMetadata, ()> {
        let metadata = fs::metadata(path).map_err(|_| ())?;
        Ok(ExecutableMetadata {
            regular: metadata.is_file(),
            executable: native_executable(&metadata),
        })
    }
}

#[cfg(unix)]
fn native_executable(metadata: &fs::Metadata) -> bool {
    use std::os::unix::fs::PermissionsExt;
    metadata.permissions().mode() & 0o111 != 0
}

#[cfg(not(unix))]
fn native_executable(_metadata: &fs::Metadata) -> bool {
    true
}

#[cfg(test)]
pub(crate) fn resolve_explicit_cli_for_test(
    configured: OsString,
    inputs: &NativeDiscoveryInputs,
    filesystem: &TestDiscoveryFileSystem,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    resolve_explicit_cli_with(configured, inputs, filesystem)
}

#[cfg(test)]
pub(crate) fn resolve_compatibility_path_cli_for_test(
    inputs: &NativeDiscoveryInputs,
    filesystem: &TestDiscoveryFileSystem,
) -> Result<LaunchExecutable, CliDiscoveryError> {
    resolve_compatibility_path_cli_with(inputs, filesystem)
}

#[cfg(test)]
pub(crate) struct TestDiscoveryFileSystem {
    entries: std::collections::BTreeMap<PathBuf, Result<(PathBuf, ExecutableMetadata), ()>>,
}

#[cfg(test)]
impl TestDiscoveryFileSystem {
    pub(crate) fn new() -> Self {
        Self {
            entries: std::collections::BTreeMap::new(),
        }
    }

    pub(crate) fn executable(mut self, candidate: PathBuf, resolved: PathBuf) -> Self {
        self.entries.insert(
            candidate,
            Ok((
                resolved,
                ExecutableMetadata {
                    regular: true,
                    executable: true,
                },
            )),
        );
        self
    }

    pub(crate) fn unusable(mut self, candidate: PathBuf) -> Self {
        self.entries.insert(
            candidate.clone(),
            Ok((
                candidate,
                ExecutableMetadata {
                    regular: false,
                    executable: false,
                },
            )),
        );
        self
    }
}

#[cfg(test)]
impl DiscoveryFileSystem for TestDiscoveryFileSystem {
    fn canonicalize(&self, path: &Path) -> Result<PathBuf, ()> {
        self.entries
            .get(path)
            .cloned()
            .unwrap_or(Err(()))
            .map(|(resolved, _)| resolved)
    }

    fn metadata(&self, path: &Path) -> Result<ExecutableMetadata, ()> {
        self.entries
            .values()
            .filter_map(|entry| entry.as_ref().ok())
            .find(|(resolved, _)| resolved == path)
            .map(|(_, metadata)| *metadata)
            .ok_or(())
    }
}
