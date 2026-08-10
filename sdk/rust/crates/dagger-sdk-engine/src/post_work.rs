//! Closed child-process execution and bounded projection convergence.
//!
//! Process names and argument shapes are derived exclusively from typed plans. The
//! runner clears ambient environment, never starts a shell, bounds captured output,
//! and kills then reaps children when the operation is cancelled.

use std::collections::BTreeMap;
use std::process::Stdio;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use tokio::io::{AsyncRead, AsyncReadExt as _};
use tokio::process::Command;
use tokio::sync::Notify;

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::{OperationRoot, PostWorkPlan};

const CARGO: &str = "/usr/local/cargo/bin/cargo";
const RUSTFMT: &str = "/usr/local/cargo/bin/rustfmt";
const MAX_PROCESS_OUTPUT_BYTES: usize = 256 * 1024;
const FIXED_SECRET_MOUNTS: &[&str] = &[
    "/run/secrets/dagger-rust/cargo-credentials.toml",
    "/run/secrets/dagger-rust/git-credentials",
];

/// Cooperative operation cancellation shared with every spawned child.
#[derive(Clone, Debug, Default)]
pub struct Cancellation {
    cancelled: Arc<AtomicBool>,
    notify: Arc<Notify>,
}

impl Cancellation {
    /// Requests cancellation of current and future child-process work.
    pub fn cancel(&self) {
        self.cancelled.store(true, Ordering::Release);
        self.notify.notify_waiters();
    }

    /// Reports whether cancellation has already been requested.
    #[must_use]
    pub fn is_cancelled(&self) -> bool {
        self.cancelled.load(Ordering::Acquire)
    }

    async fn cancelled(&self) {
        if self.is_cancelled() {
            return;
        }
        self.notify.notified().await;
    }
}

/// One fixed executable and argument vector derived from a closed post-work plan.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CommandSpec {
    /// Packaged absolute executable path.
    pub executable: &'static str,
    /// Runner-authored arguments in exact order.
    pub arguments: Vec<String>,
}

/// Bounded result retained for digesting or credential-safe diagnostics.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ProcessOutcome {
    /// Child exit success.
    pub success: bool,
    /// Bounded standard output.
    pub stdout: Vec<u8>,
    /// Bounded standard error.
    pub stderr: Vec<u8>,
    /// Whether either stream exceeded its retained bound.
    pub truncated: bool,
}

/// Derives the only admitted process invocation for one plan.
#[must_use]
pub fn command_spec(plan: &PostWorkPlan) -> CommandSpec {
    match plan {
        PostWorkPlan::FormatRust { toolchain, files } => {
            let mut arguments = vec![
                format!("+{toolchain}"),
                "--edition".to_owned(),
                "2024".to_owned(),
            ];
            arguments.extend(files.iter().map(ToString::to_string));
            CommandSpec {
                executable: RUSTFMT,
                arguments,
            }
        }
        PostWorkPlan::GenerateLockfile { manifest_path } => CommandSpec {
            executable: CARGO,
            arguments: vec![
                "generate-lockfile".to_owned(),
                "--manifest-path".to_owned(),
                manifest_path.to_string(),
            ],
        },
        PostWorkPlan::VerifyLockedMetadata { manifest_path } => CommandSpec {
            executable: CARGO,
            arguments: vec![
                "metadata".to_owned(),
                "--format-version".to_owned(),
                "1".to_owned(),
                "--locked".to_owned(),
                "--manifest-path".to_owned(),
                manifest_path.to_string(),
            ],
        },
    }
}

/// Copies only non-secret path configuration admitted by child-process policy.
#[must_use]
pub fn current_allowlisted_environment() -> BTreeMap<String, String> {
    ["CARGO_HOME", "RUSTUP_HOME", "SSL_CERT_DIR", "SSL_CERT_FILE"]
        .into_iter()
        .filter_map(|name| {
            std::env::var(name)
                .ok()
                .map(|value| (name.to_owned(), value))
        })
        .collect()
}

/// Executes one typed post-work action with no ambient shell or environment.
pub async fn execute(
    root: &OperationRoot,
    plan: &PostWorkPlan,
    allowlisted_environment: &BTreeMap<String, String>,
    cancel: &Cancellation,
) -> Result<ProcessOutcome, EngineDiagnostic> {
    let failure_code = if matches!(plan, PostWorkPlan::FormatRust { .. }) {
        EngineDiagnosticCode::FormatFailed
    } else {
        EngineDiagnosticCode::GenerationFailed
    };
    execute_fixed(
        root,
        &command_spec(plan),
        allowlisted_environment,
        cancel,
        failure_code,
        "post-work",
    )
    .await
}

/// Executes a runner-authored fixed command used by Cargo discovery and post-work.
pub(crate) async fn execute_fixed(
    root: &OperationRoot,
    spec: &CommandSpec,
    allowlisted_environment: &BTreeMap<String, String>,
    cancel: &Cancellation,
    failure_code: EngineDiagnosticCode,
    coordinate: &str,
) -> Result<ProcessOutcome, EngineDiagnostic> {
    validate_environment(allowlisted_environment)?;
    validate_fixed_secret_mounts()?;
    if cancel.is_cancelled() {
        return Err(cancelled());
    }
    let mut command = Command::new(spec.executable);
    command
        .args(&spec.arguments)
        .current_dir(root.absolute())
        .env_clear()
        .envs(allowlisted_environment)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    let failure = || {
        EngineDiagnostic::new(
            failure_code,
            Some(coordinate),
            "allowlisted child process could not complete",
        )
    };
    let mut child = command.spawn().map_err(|_| failure())?;
    let stdout = child.stdout.take().ok_or_else(&failure)?;
    let stderr = child.stderr.take().ok_or_else(&failure)?;
    let stdout_task = tokio::spawn(read_bounded(stdout));
    let stderr_task = tokio::spawn(read_bounded(stderr));

    let status = tokio::select! {
        status = child.wait() => status.map_err(|_| failure())?,
        () = cancel.cancelled() => {
            let _ = child.start_kill();
            let _ = child.wait().await;
            let _ = stdout_task.await;
            let _ = stderr_task.await;
            return Err(cancelled());
        }
    };
    let (stdout, stdout_truncated) = stdout_task
        .await
        .map_err(|_| failure())?
        .map_err(|_| failure())?;
    let (stderr, stderr_truncated) = stderr_task
        .await
        .map_err(|_| failure())?
        .map_err(|_| failure())?;
    Ok(ProcessOutcome {
        success: status.success(),
        stdout: redact_output(stdout),
        stderr: redact_output(stderr),
        truncated: stdout_truncated || stderr_truncated,
    })
}

/// Accepts optional credentials only as regular files at fixed container mounts.
pub fn validate_fixed_secret_mounts() -> Result<(), EngineDiagnostic> {
    for path in FIXED_SECRET_MOUNTS {
        match std::fs::symlink_metadata(path) {
            Ok(metadata) if metadata.file_type().is_file() => {}
            Ok(_) => {
                return Err(EngineDiagnostic::new(
                    EngineDiagnosticCode::PostWorkRejected,
                    Some("post-work.secret-mount"),
                    "optional credential mount must be a regular non-symlink file",
                ));
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => {
                return Err(EngineDiagnostic::new(
                    EngineDiagnosticCode::PostWorkRejected,
                    Some("post-work.secret-mount"),
                    "optional credential mount could not be inspected safely",
                ));
            }
        }
    }
    Ok(())
}

/// Requires one initial pass and at most one converging follow-up pass.
pub fn require_convergence<T: Eq>(projections: &[T]) -> Result<(), EngineDiagnostic> {
    match projections {
        [] | [_] => Ok(()),
        [first, second] if first == second => Ok(()),
        _ => Err(EngineDiagnostic::new(
            EngineDiagnosticCode::GenerationNonConvergent,
            Some("operation.projection"),
            "projection did not reach a fixed point within two passes",
        )),
    }
}

async fn read_bounded(mut reader: impl AsyncRead + Unpin) -> std::io::Result<(Vec<u8>, bool)> {
    let mut output = Vec::new();
    let mut truncated = false;
    let mut buffer = [0_u8; 8192];
    loop {
        let count = reader.read(&mut buffer).await?;
        if count == 0 {
            break;
        }
        let remaining = MAX_PROCESS_OUTPUT_BYTES.saturating_sub(output.len());
        output.extend_from_slice(&buffer[..count.min(remaining)]);
        truncated |= count > remaining;
    }
    Ok((output, truncated))
}

fn validate_environment(environment: &BTreeMap<String, String>) -> Result<(), EngineDiagnostic> {
    const ALLOWED: &[&str] = &["CARGO_HOME", "RUSTUP_HOME", "SSL_CERT_DIR", "SSL_CERT_FILE"];
    if environment
        .keys()
        .any(|name| !ALLOWED.contains(&name.as_str()))
    {
        return Err(EngineDiagnostic::new(
            EngineDiagnosticCode::PostWorkRejected,
            Some("post-work.environment"),
            "child environment contains a value outside the fixed allowlist",
        ));
    }
    Ok(())
}

fn redact_output(mut output: Vec<u8>) -> Vec<u8> {
    let text = String::from_utf8_lossy(&output);
    if ["https://", "http://", "Authorization:", "Bearer ", "token="]
        .iter()
        .any(|marker| text.contains(marker))
    {
        output.clear();
        output.extend_from_slice(b"[REDACTED]");
    }
    output
}

fn cancelled() -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::OperationCancelled,
        Some("operation"),
        "operation was cancelled and its child process was reaped",
    )
}
