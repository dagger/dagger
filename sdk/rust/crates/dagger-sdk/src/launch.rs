//! Finite CLI selection and native child-launch policy.
//!
//! A compiled release has one compatibility escape hatch: checksum-manifest 403/404
//! may select the canonical `dagger` executable from the captured PATH snapshot. The
//! selected executable and any cache lease then remain one owned value through the
//! bounded spawn state machine; neither fallback nor retry can reconsider a source.

use std::error::Error;
use std::ffi::{OsStr, OsString};
use std::fmt;
use std::future::Future;
use std::io;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::Arc;
use std::time::Duration;

use tokio::process::{Child, ChildStderr, ChildStdin, ChildStdout, Command};

use crate::diagnostic::{Diagnostic, DiagnosticInput, DiagnosticStream};
use crate::discovery::{LaunchExecutable, NativeDiscoveryInputs, resolve_compatibility_path_cli};
use crate::preflight::CliLaunchRequest;
use crate::provision::{DefaultCliProvisioner, ProvisioningHttp, RetentionRemover};
use crate::provisioning_control::{ProvisioningCancellation, ProvisioningObserver};
use crate::provisioning_error::{ProvisionError, ProvisionErrorKind};
use crate::target::ArchiveDescriptor;

const MAX_SPAWN_ATTEMPTS: u8 = 10;
const SPAWN_BACKOFF: Duration = Duration::from_millis(100);
const RUNNER_HOST_KEY: &str = "_EXPERIMENTAL_DAGGER_RUNNER_HOST";
const RUNNER_TOKEN_KEY: &str = "_EXPERIMENTAL_DAGGER_RUNNER_TOKEN";

/// Statically dispatched compiled-release acquisition boundary.
pub(crate) trait CliProvisioner: Send + Sync {
    fn acquire<'a>(
        &'a self,
        descriptor: &'a ArchiveDescriptor,
        cancellation: &'a ProvisioningCancellation,
    ) -> impl Future<Output = Result<LaunchExecutable, ProvisionError>> + Send + 'a;
}

impl<H, O, R> CliProvisioner for DefaultCliProvisioner<H, O, R>
where
    H: ProvisioningHttp,
    O: ProvisioningObserver,
    R: RetentionRemover,
{
    async fn acquire(
        &self,
        descriptor: &ArchiveDescriptor,
        cancellation: &ProvisioningCancellation,
    ) -> Result<LaunchExecutable, ProvisionError> {
        DefaultCliProvisioner::acquire(self, descriptor, cancellation).await
    }
}

/// Pure compatibility lookup boundary over one captured native snapshot.
pub(crate) trait CompatibilityResolver {
    fn resolve(
        &self,
        inputs: &NativeDiscoveryInputs,
    ) -> Result<LaunchExecutable, crate::CliDiscoveryError>;
}

/// Native canonical-name compatibility resolver.
pub(crate) struct NativeCompatibilityResolver;

impl CompatibilityResolver for NativeCompatibilityResolver {
    fn resolve(
        &self,
        inputs: &NativeDiscoveryInputs,
    ) -> Result<LaunchExecutable, crate::CliDiscoveryError> {
        resolve_compatibility_path_cli(inputs)
    }
}

/// A CLI selected without leaving a lower-priority source available for reconsideration.
pub(crate) struct SelectedCli {
    executable: LaunchExecutable,
    release_unavailable: Option<ProvisionError>,
    unavailable_version: Option<String>,
}

impl SelectedCli {
    pub(crate) fn explicit(executable: LaunchExecutable) -> Self {
        Self {
            executable,
            release_unavailable: None,
            unavailable_version: None,
        }
    }

    pub(crate) fn executable(&self) -> &LaunchExecutable {
        &self.executable
    }

    pub(crate) fn is_compatibility_fallback(&self) -> bool {
        self.release_unavailable.is_some()
    }

    pub(crate) fn release_unavailable(&self) -> Option<&ProvisionError> {
        self.release_unavailable.as_ref()
    }

    pub(crate) fn into_parts(self) -> (LaunchExecutable, Option<ProvisionError>) {
        (self.executable, self.release_unavailable)
    }
}

/// Terminal compiled-release selection failure.
pub(crate) enum CliSelectionError {
    Provision(ProvisionError),
    Compatibility {
        release: ProvisionError,
        lookup: crate::CliDiscoveryError,
    },
}

impl CliSelectionError {
    pub(crate) fn release_cause(&self) -> Option<&ProvisionError> {
        match self {
            Self::Provision(error) => Some(error),
            Self::Compatibility { release, .. } => Some(release),
        }
    }

    pub(crate) fn lookup_cause(&self) -> Option<&crate::CliDiscoveryError> {
        match self {
            Self::Provision(_) => None,
            Self::Compatibility { lookup, .. } => Some(lookup),
        }
    }
}

impl fmt::Display for CliSelectionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Provision(error) => error.fmt(formatter),
            Self::Compatibility { .. } => {
                formatter.write_str("the compiled CLI and compatibility PATH lookup both failed")
            }
        }
    }
}

impl fmt::Debug for CliSelectionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Provision(error) => formatter.debug_tuple("Provision").field(error).finish(),
            Self::Compatibility { release, lookup } => formatter
                .debug_struct("Compatibility")
                .field("release", release)
                .field("lookup", lookup)
                .finish(),
        }
    }
}

impl Error for CliSelectionError {}

/// Acquires the selected compiled CLI or takes the single allowed compatibility edge.
pub(crate) async fn select_compiled_cli<P, D>(
    provisioner: &P,
    resolver: &D,
    descriptor: &ArchiveDescriptor,
    discovery: &NativeDiscoveryInputs,
    cancellation: &ProvisioningCancellation,
) -> Result<SelectedCli, CliSelectionError>
where
    P: CliProvisioner,
    D: CompatibilityResolver,
{
    let version = descriptor
        .cli_version()
        .map_err(|_| {
            CliSelectionError::Provision(ProvisionError::new(ProvisionErrorKind::InvalidReleaseUrl))
        })?
        .to_string();
    match provisioner.acquire(descriptor, cancellation).await {
        Ok(executable) => Ok(SelectedCli {
            executable,
            release_unavailable: None,
            unavailable_version: None,
        }),
        Err(release) if release.kind() == ProvisionErrorKind::ReleaseUnavailable => {
            let executable =
                resolver
                    .resolve(discovery)
                    .map_err(|lookup| CliSelectionError::Compatibility {
                        release: release.clone(),
                        lookup,
                    })?;
            Ok(SelectedCli {
                executable,
                release_unavailable: Some(release),
                unavailable_version: Some(version),
            })
        }
        Err(error) => Err(CliSelectionError::Provision(error)),
    }
}

/// The two SDK labels required by the Dagger session protocol.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct SessionLabels {
    sdk_name: &'static str,
    sdk_version: &'static str,
}

impl SessionLabels {
    pub(crate) const fn rust_sdk() -> Self {
        Self {
            sdk_name: "rust",
            sdk_version: env!("CARGO_PKG_VERSION"),
        }
    }

    fn append_to(self, arguments: &mut Vec<OsString>) {
        arguments.extend([
            OsString::from("--label"),
            OsString::from(format!("dagger.io/sdk.name:{}", self.sdk_name)),
            OsString::from("--label"),
            OsString::from(format!("dagger.io/sdk.version:{}", self.sdk_version)),
        ]);
    }
}

/// Complete typed input for one CLI-owned session start.
pub(crate) struct CliSessionStart {
    selected: SelectedCli,
    options: CliLaunchRequest,
    labels: SessionLabels,
}

impl CliSessionStart {
    pub(crate) fn new(selected: SelectedCli, options: CliLaunchRequest) -> Self {
        if let Some(version) = selected.unavailable_version.as_deref() {
            let warning = format!(
                "compiled Dagger CLI {version} is unavailable; using dagger from PATH; version compatibility is not guaranteed"
            );
            options
                .diagnostics
                .ingest(DiagnosticInput::Progress(Diagnostic {
                    stream: DiagnosticStream::Lifecycle,
                    payload: warning.as_bytes(),
                }));
        }
        Self {
            selected,
            options,
            labels: SessionLabels::rust_sdk(),
        }
    }

    pub(crate) fn selected(&self) -> &SelectedCli {
        &self.selected
    }

    pub(crate) fn options(&self) -> &CliLaunchRequest {
        &self.options
    }

    pub(crate) fn projection(&self) -> CliLaunchProjection {
        let mut arguments = self.options.arguments().to_vec();
        self.labels.append_to(&mut arguments);

        let mut environment = Vec::new();
        for (key, value) in self.options.environment() {
            set_environment(&mut environment, key.clone(), value.clone());
        }

        let ambient = self.options.ambient();
        if !contains_environment(&environment, OsStr::new(RUNNER_HOST_KEY))
            && let Some(host) = ambient.runner().host()
        {
            set_environment(
                &mut environment,
                OsString::from(RUNNER_HOST_KEY),
                host.to_os_string(),
            );
        }
        if let Some(token) = ambient.runner().token() {
            set_environment(
                &mut environment,
                OsString::from(RUNNER_TOKEN_KEY),
                token.to_os_string(),
            );
        }
        for (key, value) in ambient.propagation().values() {
            if let Some(value) = value {
                set_environment(&mut environment, OsString::from(key), value.to_os_string());
            }
        }

        CliLaunchProjection {
            executable: self.selected.executable.path().to_path_buf(),
            arguments,
            environment,
            stdio: StdioProjection::piped(),
        }
    }

    pub(crate) fn into_parts(self) -> (SelectedCli, CliLaunchRequest) {
        (self.selected, self.options)
    }
}

/// Exact process inputs passed to a native spawn attempt.
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct CliLaunchProjection {
    executable: PathBuf,
    arguments: Vec<OsString>,
    environment: Vec<(OsString, OsString)>,
    stdio: StdioProjection,
}

impl CliLaunchProjection {
    pub(crate) fn executable(&self) -> &Path {
        &self.executable
    }

    pub(crate) fn arguments(&self) -> &[OsString] {
        &self.arguments
    }

    pub(crate) fn environment(&self) -> &[(OsString, OsString)] {
        &self.environment
    }

    pub(crate) const fn stdio(&self) -> StdioProjection {
        self.stdio
    }

    fn configure(&self, command: &mut Command) {
        command
            .args(&self.arguments)
            .envs(self.environment.iter().map(|(key, value)| (key, value)))
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .kill_on_drop(true);
    }
}

/// Reviewable child-standard-I/O policy retained in the launch projection.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct StdioProjection {
    pub(crate) stdin_piped: bool,
    pub(crate) stdout_piped: bool,
    pub(crate) stderr_piped: bool,
}

impl StdioProjection {
    const fn piped() -> Self {
        Self {
            stdin_piped: true,
            stdout_piped: true,
            stderr_piped: true,
        }
    }
}

fn set_environment(environment: &mut Vec<(OsString, OsString)>, key: OsString, value: OsString) {
    environment.retain(|(candidate, _)| !same_environment_key(candidate, &key));
    environment.push((key, value));
}

fn contains_environment(environment: &[(OsString, OsString)], key: &OsStr) -> bool {
    environment
        .iter()
        .any(|(candidate, _)| same_environment_key(candidate, key))
}

fn same_environment_key(left: &OsStr, right: &OsStr) -> bool {
    match (left.to_str(), right.to_str()) {
        (Some(left), Some(right)) => left.eq_ignore_ascii_case(right),
        _ => left == right,
    }
}

/// Native pipes acquired by one successful spawn.
pub(crate) struct SpawnedProcess {
    pub(crate) child: Child,
    pub(crate) stdin: ChildStdin,
    pub(crate) stdout: ChildStdout,
    pub(crate) stderr: ChildStderr,
}

/// Synchronous spawn seam; each call owns a fresh native pipe configuration.
pub(crate) trait ProcessSpawner<T> {
    fn spawn(&mut self, projection: &CliLaunchProjection) -> io::Result<T>;
}

/// Tokio-native process spawner.
pub(crate) struct TokioProcessSpawner;

impl ProcessSpawner<SpawnedProcess> for TokioProcessSpawner {
    fn spawn(&mut self, projection: &CliLaunchProjection) -> io::Result<SpawnedProcess> {
        let mut command = Command::new(projection.executable());
        projection.configure(&mut command);
        let mut child = command.spawn()?;
        // A successful piped spawn must yield all three handles. Treating absence as a
        // typed spawn failure keeps partially acquired process state behind kill-on-drop.
        let stdin = child
            .stdin
            .take()
            .ok_or_else(|| io::Error::other("piped child stdin was unavailable"))?;
        let stdout = child
            .stdout
            .take()
            .ok_or_else(|| io::Error::other("piped child stdout was unavailable"))?;
        let stderr = child
            .stderr
            .take()
            .ok_or_else(|| io::Error::other("piped child stderr was unavailable"))?;
        Ok(SpawnedProcess {
            child,
            stdin,
            stdout,
            stderr,
        })
    }
}

/// Replaceable backoff clock used without runtime-global test controls.
pub(crate) trait RetryClock: Send + Sync {
    fn sleep(&self, duration: Duration) -> impl Future<Output = ()> + Send;
}

/// Tokio monotonic backoff clock.
pub(crate) struct TokioRetryClock;

impl RetryClock for TokioRetryClock {
    async fn sleep(&self, duration: Duration) {
        tokio::time::sleep(duration).await;
    }
}

/// Successful spawn plus the selected executable whose lease remains owned.
#[derive(Debug)]
pub(crate) struct SpawnSuccess<T> {
    pub(crate) output: T,
    pub(crate) executable: LaunchExecutable,
    pub(crate) attempts: u8,
}

/// Stable internal classification for bounded process-start failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum SpawnErrorKind {
    Cancelled,
    ExecutableBusy,
    Process,
}

/// Credential-safe process-start failure with bounded attempt metadata.
pub(crate) struct SpawnError {
    kind: SpawnErrorKind,
    attempts: u8,
    source: Option<Arc<io::Error>>,
}

impl SpawnError {
    fn cancelled(attempts: u8) -> Self {
        Self {
            kind: SpawnErrorKind::Cancelled,
            attempts,
            source: None,
        }
    }

    fn process(kind: SpawnErrorKind, attempts: u8, source: io::Error) -> Self {
        Self {
            kind,
            attempts,
            source: Some(Arc::new(source)),
        }
    }

    pub(crate) const fn kind(&self) -> SpawnErrorKind {
        self.kind
    }

    pub(crate) const fn attempts(&self) -> u8 {
        self.attempts
    }
}

impl fmt::Display for SpawnError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self.kind {
            SpawnErrorKind::Cancelled => formatter.write_str("CLI process startup was cancelled"),
            SpawnErrorKind::ExecutableBusy => write!(
                formatter,
                "CLI executable remained busy after {} spawn attempts",
                self.attempts
            ),
            SpawnErrorKind::Process => write!(
                formatter,
                "CLI process startup failed after {} spawn attempt(s)",
                self.attempts
            ),
        }
    }
}

impl fmt::Debug for SpawnError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("SpawnError")
            .field("kind", &self.kind)
            .field("attempts", &self.attempts)
            .finish_non_exhaustive()
    }
}

impl Error for SpawnError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.source
            .as_deref()
            .map(|source| source as &(dyn Error + 'static))
    }
}

/// Spawn failure which retains the release cause when compatibility PATH was selected.
pub(crate) struct SessionSpawnError {
    spawn: SpawnError,
    release_unavailable: Option<ProvisionError>,
}

impl SessionSpawnError {
    pub(crate) fn new(spawn: SpawnError, release_unavailable: Option<ProvisionError>) -> Self {
        Self {
            spawn,
            release_unavailable,
        }
    }

    pub(crate) fn spawn(&self) -> &SpawnError {
        &self.spawn
    }

    pub(crate) fn release_unavailable(&self) -> Option<&ProvisionError> {
        self.release_unavailable.as_ref()
    }
}

impl fmt::Display for SessionSpawnError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.release_unavailable.is_some() {
            formatter.write_str("the unavailable compiled CLI and PATH CLI startup both failed")
        } else {
            self.spawn.fmt(formatter)
        }
    }
}

impl fmt::Debug for SessionSpawnError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("SessionSpawnError")
            .field("spawn", &self.spawn)
            .field(
                "release_unavailable_present",
                &self.release_unavailable.is_some(),
            )
            .finish()
    }
}

impl Error for SessionSpawnError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(&self.spawn)
    }
}

/// Spawns once for ordinary failures and at most ten times for native executable-busy.
pub(crate) async fn spawn_with_retry<T, S, C>(
    executable: LaunchExecutable,
    projection: &CliLaunchProjection,
    spawner: &mut S,
    clock: &C,
    cancellation: &ProvisioningCancellation,
) -> Result<SpawnSuccess<T>, SpawnError>
where
    S: ProcessSpawner<T>,
    C: RetryClock,
{
    let mut attempts = 0_u8;
    loop {
        if cancellation.is_cancelled() {
            return Err(SpawnError::cancelled(attempts));
        }
        attempts += 1;
        match spawner.spawn(projection) {
            Ok(output) => {
                return Ok(SpawnSuccess {
                    output,
                    executable,
                    attempts,
                });
            }
            Err(error) if error.kind() == io::ErrorKind::ExecutableFileBusy => {
                if attempts == MAX_SPAWN_ATTEMPTS {
                    return Err(SpawnError::process(
                        SpawnErrorKind::ExecutableBusy,
                        attempts,
                        error,
                    ));
                }
                // Dropping the outer startup future drops both branches. Explicit
                // cancellation additionally makes deterministic callers stop without
                // waiting for the bounded retry delay to expire.
                tokio::select! {
                    () = clock.sleep(SPAWN_BACKOFF) => {}
                    () = cancellation.cancelled() => {
                        return Err(SpawnError::cancelled(attempts));
                    }
                }
            }
            Err(error) => {
                return Err(SpawnError::process(
                    SpawnErrorKind::Process,
                    attempts,
                    error,
                ));
            }
        }
    }
}
