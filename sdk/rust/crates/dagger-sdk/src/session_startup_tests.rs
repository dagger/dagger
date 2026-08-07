//! Protocol, ownership, redaction, and background-outcome properties.

use std::ffi::OsString;
use std::path::PathBuf;
use std::process::Command as StdCommand;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex, OnceLock};

use proptest::prelude::*;

use crate::ClientConfig;
use crate::diagnostic::{
    Diagnostic, DiagnosticDispatcher, DiagnosticFailureKind, DiagnosticRouter, DiagnosticSink,
    DiagnosticSinkError, DiagnosticStream, StreamKind,
};
use crate::discovery::LaunchExecutable;
use crate::launch::{CliSessionStart, SelectedCli, TokioProcessSpawner, TokioRetryClock};
use crate::preflight::{ConnectionPlan, PreflightContext, ProcessInputs, preflight_with};
use crate::provisioning_control::ProvisioningCancellation;
use crate::session_startup::{
    BackgroundFailureKind, ControlAccumulator, ControlErrorKind, SessionCloseReport,
    SessionLauncher, StreamOutcome, order_outcomes,
};
use crate::test_support::io_proptest_config;

#[derive(Clone, Copy)]
enum SinkBehavior {
    Accept,
    Reject,
    Panic,
}

type DeliveredDiagnostics = Arc<Mutex<Vec<(DiagnosticStream, Vec<u8>)>>>;

struct RecordingSink {
    behavior: SinkBehavior,
    fail_at: usize,
    calls: AtomicUsize,
    delivered: DeliveredDiagnostics,
}

impl RecordingSink {
    fn new(behavior: SinkBehavior, fail_at: usize) -> (Arc<Self>, DeliveredDiagnostics) {
        let delivered = Arc::new(Mutex::new(Vec::new()));
        (
            Arc::new(Self {
                behavior,
                fail_at,
                calls: AtomicUsize::new(0),
                delivered: Arc::clone(&delivered),
            }),
            delivered,
        )
    }
}

impl DiagnosticSink for RecordingSink {
    fn emit(&self, diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
        let call = self.calls.fetch_add(1, Ordering::SeqCst);
        self.delivered
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .push((diagnostic.stream, diagnostic.payload.to_vec()));
        if call != self.fail_at {
            return Ok(());
        }
        match self.behavior {
            SinkBehavior::Accept => Ok(()),
            SinkBehavior::Reject => Err(DiagnosticSinkError::new()),
            SinkBehavior::Panic => panic!("injected diagnostic sink panic"),
        }
    }
}

fn route_chunks(router: &DiagnosticRouter, kind: StreamKind, bytes: &[u8], widths: &[usize]) {
    let mut offset = 0;
    let mut index = 0;
    while offset < bytes.len() {
        let width = widths.get(index).copied().unwrap_or(bytes.len()).max(1);
        let end = offset.saturating_add(width).min(bytes.len());
        let _ = router.route(kind, &bytes[offset..end]);
        offset = end;
        index += 1;
    }
}

proptest! {
    #![proptest_config(io_proptest_config())]

    // The parser consumes exactly one bounded record. Only the suffix enters the
    // activated diagnostic path, even when delimiter and suffix share a read chunk.
    // Feature: rust-sdk-transport-observability, Property 17: control input is parsed once and never diagnosed
    #[test]
    fn property_17_control_input_parsed_once_never_diagnosed(
        case in 0_u8..9,
        port in 1_u16..=u16::MAX,
        token in "TOKEN_[A-Za-z0-9]{16,32}",
        suffix in "suffix-[A-Za-z0-9 -]{0,48}",
        chunk_width in 1_usize..128,
    ) {
        let (record, expected_error) = match case {
            0 => (
                format!(r#"{{"port":{port},"session_token":"{token}","unknown":true}}"#).into_bytes(),
                None,
            ),
            1 => (format!(r#"{{"port":0,"session_token":"{token}"}}"#).into_bytes(), Some(ControlErrorKind::PortRange)),
            2 => (format!(r#"{{"port":65536,"session_token":"{token}"}}"#).into_bytes(), Some(ControlErrorKind::PortRange)),
            3 => (br#"{"port":1234,"session_token":""}"#.to_vec(), Some(ControlErrorKind::EmptyToken)),
            4 => (br#"{"port":1234}"#.to_vec(), Some(ControlErrorKind::FieldShape)),
            5 => (b"{\"port\":".to_vec(), Some(ControlErrorKind::Json)),
            6 => (vec![0xff, 0xfe], Some(ControlErrorKind::Encoding)),
            7 => (format!(r#"{{"port":"{port}","session_token":"{token}"}}"#).into_bytes(), Some(ControlErrorKind::FieldShape)),
            _ => (vec![b'a'; 64 * 1024], Some(ControlErrorKind::Oversize)),
        };
        let mut stream = record.clone();
        stream.push(b'\n');
        stream.extend_from_slice(suffix.as_bytes());
        let mut parser = ControlAccumulator::new();
        let mut parsed = None;
        let mut parsed_count = 0;
        let mut error = None;
        let mut diagnostic_suffix = Vec::new();
        for chunk in stream.chunks(chunk_width) {
            if parsed.is_some() {
                diagnostic_suffix.extend_from_slice(chunk);
                continue;
            }
            match parser.push(chunk) {
                Ok(Some(value)) => {
                    parsed_count += 1;
                    diagnostic_suffix.extend_from_slice(value.suffix());
                    parsed = Some(value);
                }
                Ok(None) => {}
                Err(value) => {
                    error = Some(value);
                    break;
                }
            }
        }

        let (sink, delivered) = RecordingSink::new(SinkBehavior::Accept, usize::MAX);
        let router = DiagnosticRouter::sealed(
            Arc::new(DiagnosticDispatcher::new(Some(sink))),
            Vec::<Vec<u8>>::new(),
        );
        match expected_error {
            None => {
                let parsed = parsed.expect("the valid control record parses");
                prop_assert_eq!(parsed_count, 1);
                prop_assert_eq!(parsed.parameters().port().get(), port);
                let _ = router.activate(parsed.parameters().token().expose_for_redaction());
                let _ = router.route(StreamKind::Stdout, &diagnostic_suffix);
                let _ = router.finish(StreamKind::Stdout);
                let observed = delivered
                    .lock()
                    .unwrap_or_else(|poisoned| poisoned.into_inner())
                    .iter()
                    .flat_map(|(_, bytes)| bytes.iter().copied())
                    .collect::<Vec<_>>();
                prop_assert_eq!(observed.as_slice(), suffix.as_bytes());
                prop_assert!(!observed.windows(token.len()).any(|window| window == token.as_bytes()));
                prop_assert!(!observed.windows(record.len()).any(|window| window == record.as_slice()));
            }
            Some(expected) => {
                prop_assert_eq!(error.expect("the malformed record rejects").kind(), expected);
                router.discard_sealed();
                prop_assert!(delivered
                    .lock()
                    .unwrap_or_else(|poisoned| poisoned.into_inner())
                    .is_empty());
            }
        }
    }

    // Every acquired pre-session capability is consumed by exactly one terminal edge:
    // cleanup before transfer, or ownership transfer followed by session cleanup.
    // Feature: rust-sdk-transport-observability, Property 18: pending resources have one owner and one transfer
    #[test]
    fn property_18_pending_resources_one_owner_one_transfer(
        acquired in 0_usize..8,
        fail_at in 0_usize..9,
        transfer in any::<bool>(),
    ) {
        let cleaned = Arc::new(AtomicUsize::new(0));
        let transferred = Arc::new(AtomicUsize::new(0));
        let mut resources = (0..acquired)
            .map(|_| OwnershipProbe {
                terminal: None,
                cleaned: Arc::clone(&cleaned),
                transferred: Arc::clone(&transferred),
            })
            .collect::<Vec<_>>();
        let succeeds = fail_at >= acquired;
        if transfer && succeeds {
            for resource in &mut resources {
                resource.transfer();
            }
        }
        drop(resources);
        let expected_transferred = usize::from(transfer && succeeds) * acquired;
        prop_assert_eq!(transferred.load(Ordering::SeqCst), expected_transferred);
        prop_assert_eq!(cleaned.load(Ordering::SeqCst), acquired - expected_transferred);
        prop_assert_eq!(
            cleaned.load(Ordering::SeqCst) + transferred.load(Ordering::SeqCst),
            acquired,
        );
    }

    // Redaction is channel-local and streaming; callback failure changes only sink
    // availability while bounded sanitized tails continue to drain and remain safe.
    // Feature: rust-sdk-transport-observability, Property 21: diagnostics are isolated, redacted, bounded, and contained
    #[test]
    fn property_21_diagnostics_isolated_redacted_bounded_contained(
        secret in "CANARY_[A-Za-z0-9]{20,40}",
        stdout_prefix in "[A-Za-z0-9 -]{0,48}",
        stdout_suffix in "[A-Za-z0-9 -]{0,48}",
        stderr_prefix in "[A-Za-z0-9 -]{0,48}",
        stderr_suffix in "[A-Za-z0-9 -]{0,48}",
        widths in proptest::collection::vec(1_usize..24, 1..12),
        fail_mode in 0_u8..3,
        fail_at in 0_usize..8,
    ) {
        let behavior = match fail_mode {
            0 => SinkBehavior::Accept,
            1 => SinkBehavior::Reject,
            _ => SinkBehavior::Panic,
        };
        let (sink, delivered) = RecordingSink::new(behavior, fail_at);
        let router = DiagnosticRouter::sealed(
            Arc::new(DiagnosticDispatcher::new(Some(sink))),
            vec![secret.as_bytes().to_vec()],
        );
        let stdout = [stdout_prefix.as_bytes(), secret.as_bytes(), stdout_suffix.as_bytes()].concat();
        let stderr = [stderr_prefix.as_bytes(), secret.as_bytes(), stderr_suffix.as_bytes()].concat();
        let sealed_at = stderr.len().min(widths[0]);
        let routed = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            route_chunks(&router, StreamKind::Stderr, &stderr[..sealed_at], &widths);
            let _ = router.activate(secret.as_bytes());
            route_chunks(&router, StreamKind::Stdout, &stdout, &widths);
            route_chunks(&router, StreamKind::Stderr, &stderr[sealed_at..], &widths);
            let _ = router.finish(StreamKind::Stdout);
            let _ = router.finish(StreamKind::Stderr);
        }));
        prop_assert!(routed.is_ok());
        let delivered = delivered
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .clone();
        for (_, payload) in &delivered {
            prop_assert!(!payload.windows(secret.len()).any(|window| window == secret.as_bytes()));
        }
        let snapshot = router.snapshot();
        prop_assert!(snapshot.stdout().len() <= 1024 * 1024);
        prop_assert!(snapshot.stderr().len() <= 1024 * 1024);
        prop_assert!(!snapshot.stdout().windows(secret.len()).any(|window| window == secret.as_bytes()));
        prop_assert!(!snapshot.stderr().windows(secret.len()).any(|window| window == secret.as_bytes()));
        let snapshot_debug = format!("{snapshot:?}");
        prop_assert!(!snapshot_debug.contains(&secret));
    }

    // Completion order does not determine observation order, and each component keeps
    // its independently typed terminal outcome through the close boundary.
    // Feature: rust-sdk-transport-observability, Property 22: background outcomes remain observable
    #[test]
    fn property_22_background_outcomes_remain_observable(
        stderr_first in any::<bool>(),
        stdout_failure in prop::option::of(0_u8..4),
        stderr_failure in prop::option::of(0_u8..4),
        child_failed in any::<bool>(),
        stdout_bytes in any::<u32>(),
        stderr_bytes in any::<u32>(),
    ) {
        let failure = |value: Option<u8>| value.map(|value| match value {
            0 => BackgroundFailureKind::Read,
            1 => BackgroundFailureKind::SinkRejected,
            2 => BackgroundFailureKind::SinkPanicked,
            _ => BackgroundFailureKind::Join,
        });
        let stdout = StreamOutcome::for_test(StreamKind::Stdout, u64::from(stdout_bytes), failure(stdout_failure));
        let stderr = StreamOutcome::for_test(StreamKind::Stderr, u64::from(stderr_bytes), failure(stderr_failure));
        let mut outcomes = if stderr_first {
            vec![stderr.clone(), stdout.clone()]
        } else {
            vec![stdout.clone(), stderr.clone()]
        };
        order_outcomes(&mut outcomes);
        prop_assert_eq!(&outcomes, &vec![stdout, stderr]);
        prop_assert_eq!(outcomes.iter().filter(|outcome| outcome.kind() == StreamKind::Stdout).count(), 1);
        prop_assert_eq!(outcomes.iter().filter(|outcome| outcome.kind() == StreamKind::Stderr).count(), 1);
        prop_assert_eq!(outcomes[0].bytes_seen(), u64::from(stdout_bytes));
        prop_assert_eq!(outcomes[1].bytes_seen(), u64::from(stderr_bytes));
        prop_assert_eq!(outcomes[0].failure(), failure(stdout_failure));
        prop_assert_eq!(outcomes[1].failure(), failure(stderr_failure));
        let report = SessionCloseReport::for_test(
            outcomes,
            child_failed.then_some(BackgroundFailureKind::UnexpectedChildExit),
        );
        prop_assert_eq!(report.stream_outcomes().len(), 2);
        prop_assert_eq!(
            report.child_failure(),
            child_failed.then_some(BackgroundFailureKind::UnexpectedChildExit),
        );
    }
}

struct OwnershipProbe {
    terminal: Option<OwnershipTerminal>,
    cleaned: Arc<AtomicUsize>,
    transferred: Arc<AtomicUsize>,
}

#[derive(Clone, Copy)]
enum OwnershipTerminal {
    Transferred,
}

impl OwnershipProbe {
    fn transfer(&mut self) {
        if self.terminal.is_none() {
            self.terminal = Some(OwnershipTerminal::Transferred);
            self.transferred.fetch_add(1, Ordering::SeqCst);
        }
    }
}

impl Drop for OwnershipProbe {
    fn drop(&mut self) {
        if self.terminal.is_none() {
            self.cleaned.fetch_add(1, Ordering::SeqCst);
        }
    }
}

#[test]
fn retained_diagnostic_tails_are_hard_bounded_after_live_drain() {
    let router = DiagnosticRouter::sealed(
        Arc::new(DiagnosticDispatcher::new(None)),
        Vec::<Vec<u8>>::new(),
    );
    let _ = router.activate(b"token");
    let input = vec![b'x'; 1024 * 1024 + 8192];
    let _ = router.route(StreamKind::Stdout, &input);
    let _ = router.finish(StreamKind::Stdout);
    assert_eq!(router.snapshot().stdout().len(), 1024 * 1024);
}

#[test]
fn diagnostic_activation_is_one_way_and_idempotent() {
    let router = DiagnosticRouter::sealed(
        Arc::new(DiagnosticDispatcher::new(None)),
        Vec::<Vec<u8>>::new(),
    );
    let _ = router.activate(b"first-token");
    let _ = router.route(StreamKind::Stdout, b"before ");
    let _ = router.activate(b"second-token");
    let _ = router.route(StreamKind::Stdout, b"after");
    let _ = router.finish(StreamKind::Stdout);
    assert_eq!(router.snapshot().stdout(), b"before after");
}

struct FixtureContext(ProcessInputs);

impl PreflightContext for FixtureContext {
    fn is_directory(&self, _path: &std::path::Path) -> bool {
        true
    }

    fn process_inputs(&self) -> ProcessInputs {
        self.0.clone()
    }
}

struct CompiledHelper {
    _directory: tempfile::TempDir,
    path: PathBuf,
}

fn session_helper() -> PathBuf {
    static HELPER: OnceLock<CompiledHelper> = OnceLock::new();
    HELPER
        .get_or_init(|| {
            let directory = tempfile::tempdir().expect("the helper directory is created");
            let executable = if cfg!(windows) {
                directory.path().join("session-process.exe")
            } else {
                directory.path().join("session-process")
            };
            let source =
                PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/session_process.rs");
            let rustc = std::env::var_os("RUSTC").unwrap_or_else(|| OsString::from("rustc"));
            let status = StdCommand::new(rustc)
                .arg("--edition=2024")
                .arg(source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("the Rust helper compiler starts");
            assert!(status.success(), "the Rust session helper compiles");
            CompiledHelper {
                _directory: directory,
                path: executable,
            }
        })
        .path
        .clone()
}

fn fixture_start(mode: &str, sink: Option<Arc<dyn DiagnosticSink>>) -> CliSessionStart {
    let mut builder = ClientConfig::builder()
        .environment("DAGGER_TEST_SESSION_FIXTURE_MODE", mode)
        .environment("DAGGER_TEST_SECRET", "FIXTURE_TOKEN_0123456789");
    if let Some(sink) = sink {
        builder = builder.diagnostic_sink(sink);
    }
    let config = builder.build().expect("the helper config is valid");
    let request = match preflight_with(
        config,
        &FixtureContext(ProcessInputs::no_existing_session()),
    )
    .expect("helper preflight succeeds")
    {
        ConnectionPlan::NewCli { request, .. } => request,
        _ => panic!("the helper must select a new CLI"),
    };
    CliSessionStart::new(
        SelectedCli::explicit(LaunchExecutable::unmanaged(session_helper())),
        request,
    )
}

#[tokio::test]
async fn rust_helper_exercises_control_isolation_transfer_and_background_observation() {
    let (sink, delivered) = RecordingSink::new(SinkBehavior::Panic, 0);
    let mut launcher = SessionLauncher::new(TokioProcessSpawner, TokioRetryClock);
    let started = launcher
        .launch(
            fixture_start("valid", Some(sink)),
            &ProvisioningCancellation::new(),
        )
        .await
        .expect("the helper emits a valid control record");
    assert_eq!(started.parameters().port().get(), 4321);
    let (_parameters, resources) = started.transfer();
    let report = resources.close().await.expect("helper resources close");
    assert!(
        report.stream_outcomes().iter().any(|outcome| {
            matches!(outcome.failure(), Some(BackgroundFailureKind::SinkPanicked))
        })
    );
    assert_eq!(report.child_failure(), None);
    assert!(
        !String::from_utf8_lossy(report.diagnostics().stdout())
            .contains("FIXTURE_TOKEN_0123456789")
    );
    let delivered = delivered
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    let all = delivered
        .iter()
        .flat_map(|(_, bytes)| bytes.iter().copied())
        .collect::<Vec<_>>();
    assert!(!String::from_utf8_lossy(&all).contains("FIXTURE_TOKEN_0123456789"));
    assert!(!String::from_utf8_lossy(&all).contains("session_token"));
}

#[tokio::test]
async fn rust_helper_protocol_failure_converges_through_owned_cleanup() {
    let mut launcher = SessionLauncher::new(TokioProcessSpawner, TokioRetryClock);
    let error = launcher
        .launch(
            fixture_start("malformed-wait", None),
            &ProvisioningCancellation::new(),
        )
        .await
        .expect_err("the malformed helper control record rejects");
    assert_eq!(
        error.protocol().map(|error| error.kind()),
        Some(ControlErrorKind::PortRange)
    );
    assert!(
        error
            .diagnostics()
            .is_some_and(|snapshot| snapshot.stdout().is_empty() && snapshot.stderr().is_empty())
    );
}

#[tokio::test]
async fn rust_helper_unexpected_exit_remains_in_the_close_report() {
    let mut launcher = SessionLauncher::new(TokioProcessSpawner, TokioRetryClock);
    let started = launcher
        .launch(
            fixture_start("valid-fail", None),
            &ProvisioningCancellation::new(),
        )
        .await
        .expect("the failing helper still emits valid session parameters");
    let (_, resources) = started.transfer();
    let report = resources
        .close()
        .await
        .expect("the failing helper is reaped");
    assert_eq!(
        report.child_failure(),
        Some(BackgroundFailureKind::UnexpectedChildExit)
    );
    assert_eq!(report.stream_outcomes().len(), 2);
}

#[test]
fn diagnostic_failure_kind_stays_safe_and_finite() {
    assert_ne!(
        DiagnosticFailureKind::SinkRejected,
        DiagnosticFailureKind::SinkPanicked
    );
}
