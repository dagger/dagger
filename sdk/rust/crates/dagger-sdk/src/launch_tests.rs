//! Reference properties for finite source fallback and bounded native launch.

use std::collections::VecDeque;
use std::ffi::{OsStr, OsString};
use std::future::Future;
use std::io;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use proptest::prelude::*;

use crate::ClientConfig;
use crate::diagnostic::{Diagnostic, DiagnosticSink, DiagnosticSinkError};
use crate::discovery::{
    LaunchExecutable, NativeContextError, NativeDiscoveryInputs, NativePathSemantics,
};
use crate::errors::{CliDiscoveryError, CliDiscoveryErrorKind, DiscoveryPathRole};
use crate::launch::{
    CliLaunchProjection, CliProvisioner, CliSessionStart, CompatibilityResolver, ProcessSpawner,
    RetryClock, SessionSpawnError, SpawnErrorKind, select_compiled_cli, spawn_with_retry,
};
use crate::preflight::{ConnectionPlan, PreflightContext, ProcessInputs, preflight_with};
use crate::provisioning_control::ProvisioningCancellation;
use crate::provisioning_error::{ProvisionError, ProvisionErrorKind};
use crate::target::{Architecture, ArchiveDescriptor, OperatingSystem, exact_target};
use crate::test_support::proptest_config;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SelectionEvent {
    Provision,
    CompatibilityLookup,
}

#[derive(Clone)]
struct FixtureProvisioner {
    result: Result<LaunchExecutable, ProvisionError>,
    events: Arc<Mutex<Vec<SelectionEvent>>>,
}

impl CliProvisioner for FixtureProvisioner {
    async fn acquire(
        &self,
        _descriptor: &ArchiveDescriptor,
        _cancellation: &ProvisioningCancellation,
    ) -> Result<LaunchExecutable, ProvisionError> {
        self.events
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .push(SelectionEvent::Provision);
        self.result.clone()
    }
}

struct FixtureResolver {
    succeeds: bool,
    events: Arc<Mutex<Vec<SelectionEvent>>>,
}

impl CompatibilityResolver for FixtureResolver {
    fn resolve(
        &self,
        _inputs: &NativeDiscoveryInputs,
    ) -> Result<LaunchExecutable, CliDiscoveryError> {
        self.events
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .push(SelectionEvent::CompatibilityLookup);
        if self.succeeds {
            Ok(LaunchExecutable::unmanaged(PathBuf::from(
                "/compatibility/dagger",
            )))
        } else {
            Err(CliDiscoveryError::new(
                CliDiscoveryErrorKind::Lookup,
                DiscoveryPathRole::CompatibilityPath,
            ))
        }
    }
}

fn descriptor() -> ArchiveDescriptor {
    ArchiveDescriptor::for_target(
        exact_target().expect("the checked target is valid"),
        OperatingSystem::Linux,
        Architecture::Amd64,
    )
    .expect("the fixture descriptor is valid")
}

fn discovery() -> NativeDiscoveryInputs {
    NativeDiscoveryInputs::new(
        NativePathSemantics::Unix,
        vec![PathBuf::from("/captured/bin")],
        None,
        None,
        Ok(PathBuf::from("/captured/current")),
    )
}

struct LaunchContext(ProcessInputs);

impl PreflightContext for LaunchContext {
    fn is_directory(&self, _path: &std::path::Path) -> bool {
        true
    }

    fn process_inputs(&self) -> ProcessInputs {
        self.0.clone()
    }
}

fn launch_request(
    explicit_runner: Option<&str>,
    configured_token: Option<&str>,
    ambient_runner: Option<&str>,
    ambient_token: Option<&str>,
    traceparent: Option<&str>,
    tracestate: Option<&str>,
    baggage: Option<&str>,
) -> crate::preflight::CliLaunchRequest {
    let mut builder = ClientConfig::builder().environment("CUSTOM_KEY", "custom-value");
    if let Some(runner) = explicit_runner {
        builder = builder.runner_host(runner);
    }
    if let Some(token) = configured_token {
        builder = builder.environment("_EXPERIMENTAL_DAGGER_RUNNER_TOKEN", token);
    }
    let config = builder.build().expect("the fixture launch config is valid");
    let inputs = ProcessInputs::no_existing_session().with_ambient_for_test(
        ambient_runner.map(OsString::from),
        ambient_token.map(OsString::from),
        traceparent.map(OsString::from),
        tracestate.map(OsString::from),
        baggage.map(OsString::from),
    );
    match preflight_with(config, &LaunchContext(inputs)).expect("preflight succeeds") {
        ConnectionPlan::NewCli { request, .. } => request,
        _ => panic!("the fixture snapshot must select a new CLI"),
    }
}

fn environment_value<'a>(environment: &'a [(OsString, OsString)], key: &str) -> Option<&'a OsStr> {
    environment
        .iter()
        .find(|(candidate, _)| {
            candidate
                .to_str()
                .is_some_and(|candidate| candidate.eq_ignore_ascii_case(key))
        })
        .map(|(_, value)| value.as_os_str())
}

#[derive(Clone, Copy)]
enum Attempt {
    Busy,
    Other,
    Success,
}

struct FixtureSpawner {
    attempts: VecDeque<Attempt>,
    calls: usize,
}

impl ProcessSpawner<()> for FixtureSpawner {
    fn spawn(&mut self, _projection: &CliLaunchProjection) -> io::Result<()> {
        self.calls += 1;
        match self.attempts.pop_front().unwrap_or(Attempt::Other) {
            Attempt::Busy => Err(io::Error::from(io::ErrorKind::ExecutableFileBusy)),
            Attempt::Other => Err(io::Error::other("injected process failure")),
            Attempt::Success => Ok(()),
        }
    }
}

struct VirtualClock {
    delays: Arc<Mutex<Vec<Duration>>>,
    cancel_on_delay: Option<usize>,
    cancellation: ProvisioningCancellation,
}

impl RetryClock for VirtualClock {
    fn sleep(&self, duration: Duration) -> impl Future<Output = ()> + Send {
        let delays = Arc::clone(&self.delays);
        let cancel_on_delay = self.cancel_on_delay;
        let cancellation = self.cancellation.clone();
        async move {
            let index = {
                let mut delays = delays
                    .lock()
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
                let index = delays.len();
                delays.push(duration);
                index
            };
            if cancel_on_delay == Some(index) {
                cancellation.cancel();
            }
        }
    }
}

fn runtime() -> tokio::runtime::Runtime {
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("the test runtime builds")
}

proptest! {
    #![proptest_config(proptest_config())]

    // Only checksum-manifest release absence enters the compatibility arm; all other
    // failures remain terminal and a PATH startup failure retains the release cause.
    // Feature: rust-sdk-transport-observability, Property 13: fallback follows the finite policy table
    #[test]
    fn property_13_fallback_finite_policy_table(
        failure_case in 0_u8..8,
        fallback_succeeds in any::<bool>(),
        path_startup_succeeds in any::<bool>(),
    ) {
        let events = Arc::new(Mutex::new(Vec::new()));
        let provision_result = match failure_case {
            0 => Ok(LaunchExecutable::unmanaged(PathBuf::from("/cache/dagger"))),
            1 => Err(ProvisionError::with_status(ProvisionErrorKind::ReleaseUnavailable, 403)),
            2 => Err(ProvisionError::with_status(ProvisionErrorKind::ReleaseUnavailable, 404)),
            3 => Err(ProvisionError::with_status(ProvisionErrorKind::ManifestStatus, 500)),
            4 => Err(ProvisionError::with_status(ProvisionErrorKind::ArchiveStatus, 404)),
            5 => Err(ProvisionError::new(ProvisionErrorKind::ChecksumMismatch)),
            6 => Err(ProvisionError::new(ProvisionErrorKind::CachePublication)),
            _ => Err(ProvisionError::new(ProvisionErrorKind::Cancelled)),
        };
        let provisioner = FixtureProvisioner {
            result: provision_result,
            events: Arc::clone(&events),
        };
        let resolver = FixtureResolver {
            succeeds: fallback_succeeds,
            events: Arc::clone(&events),
        };
        let cancellation = ProvisioningCancellation::new();
        let selection = runtime().block_on(select_compiled_cli(
            &provisioner,
            &resolver,
            &descriptor(),
            &discovery(),
            &cancellation,
        ));
        let fallback_allowed = matches!(failure_case, 1 | 2);
        let observed = events
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .clone();
        prop_assert_eq!(
            observed.iter().filter(|event| **event == SelectionEvent::CompatibilityLookup).count(),
            usize::from(fallback_allowed),
        );

        match selection {
            Ok(selected) => {
                prop_assert_eq!(selected.is_compatibility_fallback(), fallback_allowed);
                if fallback_allowed {
                    prop_assert!(fallback_succeeds);
                    prop_assert_eq!(
                        selected.release_unavailable().and_then(ProvisionError::status),
                        Some(if failure_case == 1 { 403 } else { 404 }),
                    );
                    let request = launch_request(None, None, None, None, None, None, None);
                    let start = CliSessionStart::new(selected, request);
                    let projection = start.projection();
                    let (selected, _) = start.into_parts();
                    let (executable, release) = selected.into_parts();
                    let mut spawner = FixtureSpawner {
                        attempts: VecDeque::from([if path_startup_succeeds {
                            Attempt::Success
                        } else {
                            Attempt::Other
                        }]),
                        calls: 0,
                    };
                    let clock = VirtualClock {
                        delays: Arc::new(Mutex::new(Vec::new())),
                        cancel_on_delay: None,
                        cancellation: cancellation.clone(),
                    };
                    let startup = runtime().block_on(spawn_with_retry(
                        executable,
                        &projection,
                        &mut spawner,
                        &clock,
                        &cancellation,
                    ));
                    if path_startup_succeeds {
                        prop_assert!(startup.is_ok());
                    } else {
                        let compound = SessionSpawnError::new(
                            startup.expect_err("the injected PATH startup fails"),
                            release,
                        );
                        prop_assert!(compound.release_unavailable().is_some());
                        prop_assert_eq!(compound.spawn().kind(), SpawnErrorKind::Process);
                    }
                } else {
                    prop_assert_eq!(failure_case, 0);
                }
            }
            Err(error) => {
                if fallback_allowed {
                    prop_assert!(!fallback_succeeds);
                    prop_assert!(error.release_cause().is_some());
                    prop_assert!(error.lookup_cause().is_some());
                } else {
                    prop_assert_ne!(failure_case, 0);
                    prop_assert!(error.lookup_cause().is_none());
                }
            }
        }
    }

    // Executable-busy is the only retry edge. The virtual clock proves both its 100 ms
    // ceiling and cancellation before another process attempt.
    // Feature: rust-sdk-transport-observability, Property 14: spawn retry is narrow, bounded, and cancellable
    #[test]
    fn property_14_spawn_retry_narrow_bounded_cancellable(
        busy_before_terminal in 0_usize..14,
        terminal_succeeds in any::<bool>(),
        cancel_delay in prop::option::of(0_usize..12),
    ) {
        let terminal = if terminal_succeeds { Attempt::Success } else { Attempt::Other };
        let mut sequence = vec![Attempt::Busy; busy_before_terminal];
        sequence.push(terminal);
        let mut spawner = FixtureSpawner {
            attempts: VecDeque::from(sequence),
            calls: 0,
        };
        let cancellation = ProvisioningCancellation::new();
        let delays = Arc::new(Mutex::new(Vec::new()));
        let clock = VirtualClock {
            delays: Arc::clone(&delays),
            cancel_on_delay: cancel_delay,
            cancellation: cancellation.clone(),
        };
        let request = launch_request(None, None, None, None, None, None, None);
        let start = CliSessionStart::new(
            crate::launch::SelectedCli::explicit(LaunchExecutable::unmanaged(PathBuf::from("/selected/dagger"))),
            request,
        );
        let projection = start.projection();
        let (selected, _) = start.into_parts();
        let (executable, _) = selected.into_parts();
        let outcome = runtime().block_on(spawn_with_retry(
            executable,
            &projection,
            &mut spawner,
            &clock,
            &cancellation,
        ));
        let cancellation_reached = cancel_delay.is_some_and(|delay| delay < busy_before_terminal.min(9));
        let expected_attempts = if let Some(delay) = cancel_delay.filter(|delay| *delay < busy_before_terminal.min(9)) {
            delay + 1
        } else {
            (busy_before_terminal + 1).min(10)
        };
        prop_assert_eq!(spawner.calls, expected_attempts);
        let observed_delays = delays
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .clone();
        prop_assert!(observed_delays.iter().all(|delay| *delay <= Duration::from_millis(100)));
        let expected_delays = if cancellation_reached {
            expected_attempts
        } else {
            busy_before_terminal.min(9)
        };
        prop_assert_eq!(observed_delays.len(), expected_delays);
        match outcome {
            Ok(success) => {
                prop_assert!(!cancellation_reached);
                prop_assert!(terminal_succeeds);
                prop_assert!(busy_before_terminal < 10);
                prop_assert_eq!(usize::from(success.attempts), expected_attempts);
            }
            Err(error) if cancellation_reached => {
                prop_assert_eq!(error.kind(), SpawnErrorKind::Cancelled);
                prop_assert_eq!(usize::from(error.attempts()), expected_attempts);
            }
            Err(error) if busy_before_terminal >= 10 => {
                prop_assert_eq!(error.kind(), SpawnErrorKind::ExecutableBusy);
                prop_assert_eq!(error.attempts(), 10);
            }
            Err(error) => {
                prop_assert!(!terminal_succeeds);
                prop_assert_eq!(error.kind(), SpawnErrorKind::Process);
                prop_assert_eq!(usize::from(error.attempts()), expected_attempts);
            }
        }
    }

    // The complete projection retains the canonical request, adds its fixed labels,
    // pipes every stream, and gives managed ambient values deterministic precedence.
    // Feature: rust-sdk-transport-observability, Property 16: CLI launch projection is complete and collision-free
    #[test]
    fn property_16_cli_launch_projection_complete_collision_free(
        explicit_runner in prop::option::of(1_u16..60000),
        ambient_runner in prop::option::of(1_u16..60000),
        configured_token in prop::option::of("configured-[A-Za-z0-9]{1,16}"),
        ambient_token in prop::option::of("ambient-[A-Za-z0-9]{1,16}"),
        traceparent in prop::option::of("00-[0-9a-f]{32}-[0-9a-f]{16}-01"),
        tracestate in prop::option::of("vendor=[A-Za-z0-9]{1,12}"),
        baggage in prop::option::of("key=[A-Za-z0-9]{1,12}"),
    ) {
        let explicit_runner = explicit_runner.map(|port| format!("tcp://explicit:{port}"));
        let ambient_runner = ambient_runner.map(|port| format!("tcp://ambient:{port}"));
        let request = launch_request(
            explicit_runner.as_deref(),
            configured_token.as_deref(),
            ambient_runner.as_deref(),
            ambient_token.as_deref(),
            traceparent.as_deref(),
            tracestate.as_deref(),
            baggage.as_deref(),
        );
        let canonical_arguments = request.arguments().to_vec();
        let start = CliSessionStart::new(
            crate::launch::SelectedCli::explicit(LaunchExecutable::unmanaged(PathBuf::from("/selected/dagger"))),
            request,
        );
        let projection = start.projection();
        prop_assert_eq!(
            &projection.arguments()[..canonical_arguments.len()],
            canonical_arguments.as_slice(),
        );
        let label_values = projection.arguments().windows(2).filter_map(|pair| {
            (pair[0] == "--label").then_some(&pair[1])
        }).collect::<Vec<_>>();
        prop_assert_eq!(label_values.len(), 2);
        prop_assert_eq!(label_values.iter().filter(|value| value.as_os_str() == "dagger.io/sdk.name:rust").count(), 1);
        let version_label = format!("dagger.io/sdk.version:{}", env!("CARGO_PKG_VERSION"));
        prop_assert_eq!(label_values.iter().filter(|value| value.as_os_str() == version_label.as_str()).count(), 1);
        prop_assert!(projection.stdio().stdin_piped);
        prop_assert!(projection.stdio().stdout_piped);
        prop_assert!(projection.stdio().stderr_piped);

        let environment = projection.environment();
        let mut normalized = environment
            .iter()
            .map(|(key, _)| key.to_string_lossy().to_ascii_lowercase())
            .collect::<Vec<_>>();
        let original_len = normalized.len();
        normalized.sort();
        normalized.dedup();
        prop_assert_eq!(normalized.len(), original_len);
        let expected_runner = explicit_runner.as_deref().or(ambient_runner.as_deref());
        prop_assert_eq!(
            environment_value(environment, "_EXPERIMENTAL_DAGGER_RUNNER_HOST").and_then(OsStr::to_str),
            expected_runner,
        );
        let expected_token = ambient_token.as_deref().or(configured_token.as_deref());
        prop_assert_eq!(
            environment_value(environment, "_EXPERIMENTAL_DAGGER_RUNNER_TOKEN").and_then(OsStr::to_str),
            expected_token,
        );
        for (key, expected) in [
            ("TRACEPARENT", traceparent.as_deref()),
            ("TRACESTATE", tracestate.as_deref()),
            ("BAGGAGE", baggage.as_deref()),
        ] {
            prop_assert_eq!(environment_value(environment, key).and_then(OsStr::to_str), expected);
        }
    }
}

#[test]
fn selection_error_formatting_never_exposes_native_values() {
    let error = crate::launch::CliSelectionError::Compatibility {
        release: ProvisionError::with_status(ProvisionErrorKind::ReleaseUnavailable, 404),
        lookup: CliDiscoveryError::new(
            CliDiscoveryErrorKind::Lookup,
            DiscoveryPathRole::CompatibilityPath,
        ),
    };
    let rendered = format!("{error} {error:?}");
    assert!(!rendered.contains("/captured"));
}

#[test]
fn captured_native_context_failure_is_irrelevant_to_projection() {
    let inputs = NativeDiscoveryInputs::new(
        NativePathSemantics::Unix,
        Vec::new(),
        None,
        None,
        Err(NativeContextError),
    );
    assert!(inputs == inputs.clone());
}

struct WarningSink(Arc<Mutex<Vec<Vec<u8>>>>);

impl DiagnosticSink for WarningSink {
    fn emit(&self, diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
        self.0
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .push(diagnostic.payload.to_vec());
        Ok(())
    }
}

#[test]
fn compatibility_warning_names_the_version_and_the_caveat() {
    let events = Arc::new(Mutex::new(Vec::new()));
    let provisioner = FixtureProvisioner {
        result: Err(ProvisionError::with_status(
            ProvisionErrorKind::ReleaseUnavailable,
            404,
        )),
        events: Arc::clone(&events),
    };
    let resolver = FixtureResolver {
        succeeds: true,
        events,
    };
    let selected = runtime()
        .block_on(select_compiled_cli(
            &provisioner,
            &resolver,
            &descriptor(),
            &discovery(),
            &ProvisioningCancellation::new(),
        ))
        .expect("the compatibility path is available");
    let delivered = Arc::new(Mutex::new(Vec::new()));
    let config = ClientConfig::builder()
        .diagnostic_sink(Arc::new(WarningSink(Arc::clone(&delivered))))
        .build()
        .expect("the warning fixture config is valid");
    let request = match preflight_with(config, &LaunchContext(ProcessInputs::no_existing_session()))
        .expect("warning fixture preflight succeeds")
    {
        ConnectionPlan::NewCli { request, .. } => request,
        _ => panic!("the warning fixture must select a new CLI"),
    };
    let _ = CliSessionStart::new(selected, request);
    let warning = String::from_utf8_lossy(
        delivered
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .first()
            .expect("one compatibility warning is delivered"),
    )
    .into_owned();
    assert!(warning.contains("1.0.0-beta.10"));
    assert!(warning.contains("version compatibility is not guaranteed"));
}
