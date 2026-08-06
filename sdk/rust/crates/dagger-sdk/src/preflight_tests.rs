//! Properties and fixed examples for preflight and source planning.

use std::ffi::OsString;
use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use proptest::prelude::*;

use crate::config::ClientConfig;
use crate::connection::{EngineConnection, EngineConnectionError};
use crate::diagnostic::{
    Diagnostic, DiagnosticDispatcher, DiagnosticInput, DiagnosticSink, DiagnosticSinkError,
    DiagnosticStream,
};
use crate::errors::{ConfigError, ConfigOption, ConnectError};
use crate::graphql::{RawRequest, RawResponse, ResponseData};
use crate::preflight::{
    ConnectionPlan, PreflightContext, ProcessInputs, preflight, preflight_with,
};
use crate::test_support::proptest_config;

#[derive(Default)]
struct BoundaryCounts {
    filesystem: AtomicUsize,
    process_inputs: AtomicUsize,
    discovery: AtomicUsize,
    network: AtomicUsize,
    spawn: AtomicUsize,
    connection: AtomicUsize,
}

struct RecordingContext {
    directory: bool,
    inputs: ProcessInputs,
    counts: Arc<BoundaryCounts>,
}

impl PreflightContext for RecordingContext {
    fn is_directory(&self, _path: &Path) -> bool {
        self.counts.filesystem.fetch_add(1, Ordering::Relaxed);
        self.directory
    }

    fn process_inputs(&self) -> ProcessInputs {
        self.counts.process_inputs.fetch_add(1, Ordering::Relaxed);
        self.inputs.clone()
    }
}

fn assert_no_external_work(counts: &BoundaryCounts) {
    assert_eq!(counts.discovery.load(Ordering::Relaxed), 0);
    assert_eq!(counts.network.load(Ordering::Relaxed), 0);
    assert_eq!(counts.spawn.load(Ordering::Relaxed), 0);
    assert_eq!(counts.connection.load(Ordering::Relaxed), 0);
}

#[derive(Clone, Copy, Debug)]
enum PreflightFailure {
    MissingWorkdir,
    NonDirectoryWorkdir,
    ExistingWorkdir,
    ExistingWorkspace,
    ExistingModuleLoading,
    ExistingVersion,
    ExistingVerbosity,
    ExistingRunnerHost,
    ExistingEnvironment,
}

fn preflight_failure() -> impl Strategy<Value = PreflightFailure> {
    (0_u8..9).prop_map(|value| match value {
        0 => PreflightFailure::MissingWorkdir,
        1 => PreflightFailure::NonDirectoryWorkdir,
        2 => PreflightFailure::ExistingWorkdir,
        3 => PreflightFailure::ExistingWorkspace,
        4 => PreflightFailure::ExistingModuleLoading,
        5 => PreflightFailure::ExistingVersion,
        6 => PreflightFailure::ExistingVerbosity,
        7 => PreflightFailure::ExistingRunnerHost,
        _ => PreflightFailure::ExistingEnvironment,
    })
}

fn failing_preflight_config(failure: PreflightFailure) -> ClientConfig {
    let builder = match failure {
        PreflightFailure::MissingWorkdir | PreflightFailure::NonDirectoryWorkdir => {
            ClientConfig::builder().workdir("/virtual/unavailable")
        }
        PreflightFailure::ExistingWorkdir => ClientConfig::builder().workdir("/virtual/directory"),
        PreflightFailure::ExistingWorkspace => {
            ClientConfig::builder().workspace("github.com/example/workspace")
        }
        PreflightFailure::ExistingModuleLoading => {
            ClientConfig::builder().load_workspace_modules(false)
        }
        PreflightFailure::ExistingVersion => ClientConfig::builder().version("v1.2.3"),
        PreflightFailure::ExistingVerbosity => ClientConfig::builder().verbosity(3),
        PreflightFailure::ExistingRunnerHost => {
            ClientConfig::builder().runner_host("tcp://runner.test:1234")
        }
        PreflightFailure::ExistingEnvironment => {
            ClientConfig::builder().environment("SAFE_KEY", "hidden")
        }
    };
    builder
        .build()
        .expect("the property constructs a structurally valid preflight candidate")
}

fn expected_preflight_error(failure: PreflightFailure) -> ConfigError {
    match failure {
        PreflightFailure::MissingWorkdir | PreflightFailure::NonDirectoryWorkdir => {
            ConfigError::InvalidWorkdir
        }
        PreflightFailure::ExistingWorkdir => ConfigError::ExistingSessionConflict {
            option: ConfigOption::Workdir,
        },
        PreflightFailure::ExistingWorkspace => ConfigError::ExistingSessionConflict {
            option: ConfigOption::Workspace,
        },
        PreflightFailure::ExistingModuleLoading => ConfigError::OptionConflict {
            option: ConfigOption::LoadWorkspaceModules,
        },
        PreflightFailure::ExistingVersion => ConfigError::OptionConflict {
            option: ConfigOption::Version,
        },
        PreflightFailure::ExistingVerbosity => ConfigError::OptionConflict {
            option: ConfigOption::Verbosity,
        },
        PreflightFailure::ExistingRunnerHost => ConfigError::OptionConflict {
            option: ConfigOption::RunnerHost,
        },
        PreflightFailure::ExistingEnvironment => ConfigError::OptionConflict {
            option: ConfigOption::Environment,
        },
    }
}

proptest! {
    #![proptest_config(proptest_config())]

    // Every preflight rejection is decided before a connector-facing effect can be
    // represented, and filesystem rejection also precedes process-input capture.
    // Feature: rust-sdk-client-lifecycle, Property 12: preflight failure precedes external work
    #[test]
    fn property_12_preflight_failure_precedes_external_work(failure in preflight_failure()) {
        let counts = Arc::new(BoundaryCounts::default());
        let directory = !matches!(
            failure,
            PreflightFailure::MissingWorkdir | PreflightFailure::NonDirectoryWorkdir
        );
        let context = RecordingContext {
            directory,
            inputs: ProcessInputs::existing_session("1234", Some(OsString::from("secret"))),
            counts: counts.clone(),
        };
        let outcome = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            preflight_with(failing_preflight_config(failure), &context)
        }));
        prop_assert!(outcome.is_ok());
        let outcome = outcome.expect("the property established that preflight did not unwind");
        prop_assert!(matches!(
            outcome,
            Err(ConnectError::Config(actual)) if actual == expected_preflight_error(failure)
        ));
        prop_assert_eq!(counts.discovery.load(Ordering::Relaxed), 0);
        prop_assert_eq!(counts.network.load(Ordering::Relaxed), 0);
        prop_assert_eq!(counts.spawn.load(Ordering::Relaxed), 0);
        prop_assert_eq!(counts.connection.load(Ordering::Relaxed), 0);
        if matches!(
            failure,
            PreflightFailure::MissingWorkdir | PreflightFailure::NonDirectoryWorkdir
        ) {
            prop_assert_eq!(counts.process_inputs.load(Ordering::Relaxed), 0);
        }
    }
}

#[derive(Clone, Debug)]
struct LaunchCase {
    workdir: Option<String>,
    workspace: Option<String>,
    load_modules: bool,
    version: Option<String>,
    verbosity: u8,
    runner_host: Option<String>,
    environment: Vec<(String, String)>,
}

fn launch_case() -> impl Strategy<Value = LaunchCase> {
    (
        prop::option::of("[a-zA-Z0-9_-]{1,16}"),
        prop::option::of("[a-zA-Z0-9_./:#-]{1,24}"),
        any::<bool>(),
        prop::option::of((0_u8..20, 0_u8..20, 0_u8..20)),
        any::<u8>(),
        prop::option::of(1_u16..5000),
        proptest::collection::vec("[a-zA-Z0-9_-]{0,16}", 0..7),
    )
        .prop_map(
            |(workdir, workspace, load_modules, version, verbosity, runner, values)| LaunchCase {
                workdir: workdir.map(|value| format!("/virtual/{value}")),
                workspace,
                load_modules,
                version: version.map(|(major, minor, patch)| format!("v{major}.{minor}.{patch}")),
                verbosity,
                runner_host: runner.map(|port| format!("tcp://runner.test:{port}")),
                environment: values
                    .into_iter()
                    .enumerate()
                    .map(|(index, value)| (format!("KEY_{index}"), value))
                    .collect(),
            },
        )
}

fn build_launch_config(case: &LaunchCase) -> ClientConfig {
    let mut builder = ClientConfig::builder().load_workspace_modules(case.load_modules);
    if let Some(workdir) = &case.workdir {
        builder = builder.workdir(workdir);
    }
    if let Some(workspace) = &case.workspace {
        builder = builder.workspace(workspace);
    }
    if let Some(version) = &case.version {
        builder = builder.version(version);
    }
    builder = builder.verbosity(u64::from(case.verbosity));
    if let Some(runner_host) = &case.runner_host {
        builder = builder.runner_host(runner_host);
    }
    for (key, value) in &case.environment {
        builder = builder.environment(key, value);
    }
    builder
        .build()
        .expect("the launch strategy produces valid structural inputs")
}

fn reference_launch(case: &LaunchCase) -> (Vec<OsString>, Vec<(OsString, OsString)>) {
    let mut arguments = vec![OsString::from("session")];
    if let Some(workdir) = &case.workdir {
        arguments.push(OsString::from("--workdir"));
        arguments.push(OsString::from(workdir));
    }
    if let Some(workspace) = &case.workspace {
        arguments.push(OsString::from("--workspace"));
        arguments.push(OsString::from(workspace));
    }
    if case.load_modules {
        arguments.push(OsString::from("--load-workspace-modules"));
    }
    if let Some(version) = &case.version {
        arguments.push(OsString::from("--version"));
        arguments.push(OsString::from(version));
    }
    if case.verbosity > 0 {
        arguments.push(OsString::from(format!(
            "-{}",
            "v".repeat(usize::from(case.verbosity))
        )));
    }

    let mut environment = Vec::new();
    if let Some(runner_host) = &case.runner_host {
        environment.push((
            OsString::from("_EXPERIMENTAL_DAGGER_RUNNER_HOST"),
            OsString::from(runner_host),
        ));
    }
    environment.extend(
        case.environment
            .iter()
            .map(|(key, value)| (OsString::from(key), OsString::from(value))),
    );
    (arguments, environment)
}

proptest! {
    #![proptest_config(proptest_config())]

    // The production launch request is compared with a separate canonical projection,
    // including native ordering and an exact repeated-run byte representation.
    // Feature: rust-sdk-client-lifecycle, Property 14: CLI launch projection is deterministic and complete
    #[test]
    fn property_14_cli_launch_projection_is_deterministic_and_complete(case in launch_case()) {
        let first_counts = Arc::new(BoundaryCounts::default());
        let second_counts = Arc::new(BoundaryCounts::default());
        let first_context = RecordingContext {
            directory: true,
            inputs: ProcessInputs::no_existing_session(),
            counts: first_counts,
        };
        let second_context = RecordingContext {
            directory: true,
            inputs: ProcessInputs::no_existing_session(),
            counts: second_counts,
        };

        let first = preflight_with(build_launch_config(&case), &first_context)
            .expect("a valid new-CLI config must produce a plan");
        let second = preflight_with(build_launch_config(&case), &second_context)
            .expect("equal config and inputs must produce a second plan");
        let (first, second) = match (first, second) {
            (
                ConnectionPlan::NewCli { request: first },
                ConnectionPlan::NewCli { request: second },
            ) => (first, second),
            _ => {
                prop_assert!(false, "a no-session snapshot must select NewCli");
                return Ok(());
            }
        };
        let (expected_arguments, expected_environment) = reference_launch(&case);

        prop_assert_eq!(first.arguments(), expected_arguments.as_slice());
        prop_assert_eq!(first.environment(), expected_environment.as_slice());
        prop_assert_eq!(first.arguments(), second.arguments());
        prop_assert_eq!(first.environment(), second.environment());
        prop_assert_eq!(first.encoded_projection(), second.encoded_projection());
        prop_assert!(!first.arguments().iter().any(|value| value == "--project"));
        prop_assert_eq!(
            first
                .arguments()
                .iter()
                .filter(|value| value.as_os_str() == "--load-workspace-modules")
                .count(),
            usize::from(case.load_modules),
        );
    }
}

#[derive(Clone, Copy, Debug)]
enum SinkFailure {
    Never,
    ErrorAt(usize),
    PanicAt(usize),
}

type RecordedDiagnostic = (DiagnosticStream, Vec<u8>);
type DiagnosticCalls = Arc<Mutex<Vec<RecordedDiagnostic>>>;

struct ScheduledSink {
    calls: DiagnosticCalls,
    failure: SinkFailure,
}

impl DiagnosticSink for ScheduledSink {
    fn emit(&self, diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
        let index = {
            let mut calls = match self.calls.lock() {
                Ok(calls) => calls,
                Err(poisoned) => poisoned.into_inner(),
            };
            let index = calls.len();
            calls.push((diagnostic.stream, diagnostic.payload.to_vec()));
            index
        };

        match self.failure {
            SinkFailure::ErrorAt(target) if target == index => Err(DiagnosticSinkError::new()),
            SinkFailure::PanicAt(target) if target == index => {
                panic!("scheduled diagnostic sink panic")
            }
            _ => Ok(()),
        }
    }
}

#[derive(Clone, Debug)]
struct DiagnosticCase {
    present: bool,
    events: Vec<RecordedDiagnostic>,
    failure_kind: u8,
    failure_at: usize,
    control_at: usize,
    operation_outcome: bool,
}

fn diagnostic_sequence() -> impl Strategy<Value = DiagnosticCase> {
    (
        any::<bool>(),
        proptest::collection::vec(
            (0_u8..3, proptest::collection::vec(any::<u8>(), 0..24)),
            0..18,
        ),
        0_u8..3,
        0_usize..18,
        0_usize..19,
        any::<bool>(),
    )
        .prop_map(
            |(present, events, failure, failure_at, control_at, outcome)| {
                let events = events
                    .into_iter()
                    .map(|(stream, payload)| {
                        let stream = match stream {
                            0 => DiagnosticStream::Stdout,
                            1 => DiagnosticStream::Stderr,
                            _ => DiagnosticStream::Lifecycle,
                        };
                        (stream, payload)
                    })
                    .collect();
                DiagnosticCase {
                    present,
                    events,
                    failure_kind: failure,
                    failure_at,
                    control_at,
                    operation_outcome: outcome,
                }
            },
        )
}

proptest! {
    #![proptest_config(proptest_config())]

    // The sink is an observation only: ordered safe bytes are delivered until its
    // first failure, and neither errors nor unwinding can alter the owning operation.
    // Feature: rust-sdk-client-lifecycle, Property 16: diagnostic delivery is ordered and non-fatal
    #[test]
    fn property_16_diagnostic_delivery_is_ordered_and_non_fatal(
        case in diagnostic_sequence(),
    ) {
        let DiagnosticCase {
            present,
            events,
            failure_kind,
            failure_at,
            control_at,
            operation_outcome,
        } = case;
        let calls = Arc::new(Mutex::new(Vec::new()));
        let failure = match failure_kind {
            0 => SinkFailure::Never,
            1 => SinkFailure::ErrorAt(failure_at),
            _ => SinkFailure::PanicAt(failure_at),
        };
        let sink = present.then(|| {
            Arc::new(ScheduledSink {
                calls: calls.clone(),
                failure,
            }) as Arc<dyn DiagnosticSink>
        });
        let dispatcher = DiagnosticDispatcher::new(sink);

        let dispatch = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            for (index, (stream, payload)) in events.iter().enumerate() {
                if index == control_at {
                    dispatcher.ingest(DiagnosticInput::SessionControl);
                }
                dispatcher.ingest(DiagnosticInput::Progress(Diagnostic {
                    stream: *stream,
                    payload,
                }));
            }
            if control_at >= events.len() {
                dispatcher.ingest(DiagnosticInput::SessionControl);
            }
            operation_outcome
        }));
        prop_assert!(dispatch.is_ok());
        prop_assert_eq!(dispatch.expect("the dispatcher contains sink unwinding"), operation_outcome);

        let actual = match calls.lock() {
            Ok(calls) => calls.clone(),
            Err(poisoned) => poisoned.into_inner().clone(),
        };
        let expected_len = if !present {
            0
        } else {
            match failure {
                SinkFailure::Never => events.len(),
                SinkFailure::ErrorAt(index) | SinkFailure::PanicAt(index) => {
                    events.len().min(index.saturating_add(1))
                }
            }
        };
        prop_assert_eq!(actual.as_slice(), &events[..expected_len]);
    }
}

#[derive(Clone, Copy, Debug)]
enum SourceKind {
    Explicit,
    Existing,
    NewCli,
}

#[derive(Clone, Copy, Debug)]
enum OptionKind {
    None,
    Workdir,
    Workspace,
    DiagnosticSink,
    LoadWorkspaceModules,
    Version,
    Verbosity,
    RunnerHost,
    Environment,
    SessionStartupTimeout,
    HttpConnectTimeout,
    GraphQlExecutionTimeout,
}

fn source_option_case() -> impl Strategy<Value = (SourceKind, OptionKind, bool)> {
    (0_u8..3, 0_u8..12, any::<bool>()).prop_map(|(source, option, active)| {
        let source = match source {
            0 => SourceKind::Explicit,
            1 => SourceKind::Existing,
            _ => SourceKind::NewCli,
        };
        let option = match option {
            0 => OptionKind::None,
            1 => OptionKind::Workdir,
            2 => OptionKind::Workspace,
            3 => OptionKind::DiagnosticSink,
            4 => OptionKind::LoadWorkspaceModules,
            5 => OptionKind::Version,
            6 => OptionKind::Verbosity,
            7 => OptionKind::RunnerHost,
            8 => OptionKind::Environment,
            9 => OptionKind::SessionStartupTimeout,
            10 => OptionKind::HttpConnectTimeout,
            _ => OptionKind::GraphQlExecutionTimeout,
        };
        (source, option, active)
    })
}

struct UniqueConnection {
    drops: Arc<AtomicUsize>,
}

impl Drop for UniqueConnection {
    fn drop(&mut self) {
        self.drops.fetch_add(1, Ordering::Relaxed);
    }
}

#[async_trait]
impl EngineConnection for UniqueConnection {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        Ok(RawResponse::new(ResponseData::Absent))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

#[derive(Default)]
struct NoopSink;

impl DiagnosticSink for NoopSink {
    fn emit(&self, _diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
        Ok(())
    }
}

fn configured_source_candidate(
    source: SourceKind,
    option: OptionKind,
    active: bool,
    drops: Arc<AtomicUsize>,
) -> Result<ClientConfig, ConfigError> {
    let mut builder = ClientConfig::builder();
    if matches!(source, SourceKind::Explicit) {
        builder = builder.connection(Box::new(UniqueConnection { drops }));
    }
    builder = match option {
        OptionKind::None => builder,
        OptionKind::Workdir => builder.workdir("/virtual/directory"),
        OptionKind::Workspace => builder.workspace("github.com/example/workspace"),
        OptionKind::DiagnosticSink => builder.diagnostic_sink(Arc::new(NoopSink)),
        OptionKind::LoadWorkspaceModules => builder.load_workspace_modules(active),
        OptionKind::Version => builder.version("v1.2.3"),
        OptionKind::Verbosity => builder.verbosity(if active { 3 } else { 0 }),
        OptionKind::RunnerHost => builder.runner_host("tcp://runner.test:1234"),
        OptionKind::Environment => builder.environment("SAFE_KEY", "hidden"),
        OptionKind::SessionStartupTimeout => {
            builder.session_startup_timeout(Duration::from_secs(301))
        }
        OptionKind::HttpConnectTimeout => builder.http_connect_timeout(Duration::from_secs(11)),
        OptionKind::GraphQlExecutionTimeout => {
            builder.graphql_execution_timeout(Duration::from_secs(17))
        }
    };
    builder.build()
}

fn explicit_conflict(option: OptionKind, active: bool) -> Option<ConfigOption> {
    match option {
        OptionKind::None | OptionKind::GraphQlExecutionTimeout => None,
        OptionKind::Verbosity if !active => None,
        OptionKind::Workdir => Some(ConfigOption::Workdir),
        OptionKind::Workspace => Some(ConfigOption::Workspace),
        OptionKind::DiagnosticSink => Some(ConfigOption::DiagnosticSink),
        OptionKind::LoadWorkspaceModules => Some(ConfigOption::LoadWorkspaceModules),
        OptionKind::Version => Some(ConfigOption::Version),
        OptionKind::Verbosity => Some(ConfigOption::Verbosity),
        OptionKind::RunnerHost => Some(ConfigOption::RunnerHost),
        OptionKind::Environment => Some(ConfigOption::Environment),
        OptionKind::SessionStartupTimeout => Some(ConfigOption::SessionStartupTimeout),
        OptionKind::HttpConnectTimeout => Some(ConfigOption::HttpConnectTimeout),
    }
}

fn existing_conflict(option: OptionKind, active: bool) -> Option<ConfigError> {
    match option {
        OptionKind::Workdir => Some(ConfigError::ExistingSessionConflict {
            option: ConfigOption::Workdir,
        }),
        OptionKind::Workspace => Some(ConfigError::ExistingSessionConflict {
            option: ConfigOption::Workspace,
        }),
        OptionKind::LoadWorkspaceModules => Some(ConfigError::OptionConflict {
            option: ConfigOption::LoadWorkspaceModules,
        }),
        OptionKind::Version => Some(ConfigError::OptionConflict {
            option: ConfigOption::Version,
        }),
        OptionKind::Verbosity if active => Some(ConfigError::OptionConflict {
            option: ConfigOption::Verbosity,
        }),
        OptionKind::RunnerHost => Some(ConfigError::OptionConflict {
            option: ConfigOption::RunnerHost,
        }),
        OptionKind::Environment => Some(ConfigError::OptionConflict {
            option: ConfigOption::Environment,
        }),
        OptionKind::None
        | OptionKind::DiagnosticSink
        | OptionKind::Verbosity
        | OptionKind::SessionStartupTimeout
        | OptionKind::HttpConnectTimeout
        | OptionKind::GraphQlExecutionTimeout => None,
    }
}

proptest! {
    #![proptest_config(proptest_config())]

    // Source selection follows an independent compatibility table, and ownership of
    // an injected connection crosses at most one boundary without host observation.
    // Feature: rust-sdk-client-lifecycle, Property 17: source compatibility fails closed
    #[test]
    fn property_17_source_compatibility_fails_closed(
        (source, option, active) in source_option_case(),
    ) {
        let drops = Arc::new(AtomicUsize::new(0));
        let counts = Arc::new(BoundaryCounts::default());
        let inputs = match source {
            SourceKind::Explicit | SourceKind::Existing => {
                ProcessInputs::existing_session("1234", Some(OsString::from("SECRET_TOKEN")))
            }
            SourceKind::NewCli => ProcessInputs::no_existing_session(),
        };
        let context = RecordingContext {
            directory: true,
            inputs,
            counts: counts.clone(),
        };

        let config = configured_source_candidate(source, option, active, drops.clone());
        if let (SourceKind::Explicit, Some(expected)) = (source, explicit_conflict(option, active)) {
            let is_expected_conflict = matches!(
                config,
                Err(ConfigError::ExplicitConnectionConflict { option }) if option == expected
            );
            prop_assert!(is_expected_conflict, "unexpected explicit-source result");
            prop_assert_eq!(counts.filesystem.load(Ordering::Relaxed), 0);
            prop_assert_eq!(counts.process_inputs.load(Ordering::Relaxed), 0);
            prop_assert_eq!(drops.load(Ordering::Relaxed), 1);
            prop_assert_eq!(counts.discovery.load(Ordering::Relaxed), 0);
            prop_assert_eq!(counts.network.load(Ordering::Relaxed), 0);
            prop_assert_eq!(counts.spawn.load(Ordering::Relaxed), 0);
            prop_assert_eq!(counts.connection.load(Ordering::Relaxed), 0);
            return Ok(());
        }

        let config = config.expect("the reference table accepts this structural candidate");
        let plan = preflight_with(config, &context);
        if let (SourceKind::Existing, Some(expected)) = (source, existing_conflict(option, active)) {
            prop_assert!(matches!(
                plan,
                Err(ConnectError::Config(actual)) if actual == expected
            ));
        } else {
            let plan = plan.expect("the reference table accepts this source candidate");
            match (source, plan) {
                (
                    SourceKind::Explicit,
                    ConnectionPlan::Explicit {
                        connection,
                        execution_timeout,
                    },
                ) => {
                    prop_assert_eq!(
                        execution_timeout,
                        matches!(option, OptionKind::GraphQlExecutionTimeout)
                            .then_some(Duration::from_secs(17)),
                    );
                    drop(connection);
                    prop_assert_eq!(counts.filesystem.load(Ordering::Relaxed), 0);
                    prop_assert_eq!(counts.process_inputs.load(Ordering::Relaxed), 0);
                }
                (SourceKind::Existing, ConnectionPlan::Existing { params, request }) => {
                    prop_assert!(!params.port.is_empty());
                    prop_assert!(params.token.is_some());
                    prop_assert_eq!(request.session_startup_timeout, match option {
                        OptionKind::SessionStartupTimeout => Duration::from_secs(301),
                        _ => Duration::from_secs(300),
                    });
                    prop_assert_eq!(request.http_connect_timeout, match option {
                        OptionKind::HttpConnectTimeout => Duration::from_secs(11),
                        _ => Duration::from_secs(10),
                    });
                    prop_assert_eq!(request.graphql_execution_timeout, match option {
                        OptionKind::GraphQlExecutionTimeout => Some(Duration::from_secs(17)),
                        _ => None,
                    });
                    request.diagnostics.ingest(DiagnosticInput::SessionControl);
                }
                (SourceKind::NewCli, ConnectionPlan::NewCli { request }) => {
                    request.diagnostics.ingest(DiagnosticInput::SessionControl);
                }
                _ => {
                    prop_assert!(false, "the selected plan disagrees with the reference source");
                }
            }
        }

        assert_no_external_work(&counts);
        prop_assert_eq!(
            drops.load(Ordering::Relaxed),
            usize::from(matches!(source, SourceKind::Explicit)),
        );
    }
}

#[test]
fn real_filesystem_preflight_rejects_missing_and_non_directory_workdirs() {
    let fixture = tempfile::tempdir().expect("the filesystem fixture must be created");
    let file = fixture.path().join("ordinary-file");
    std::fs::write(&file, b"not a directory").expect("the fixture file must be written");
    let missing = fixture.path().join("missing");

    for workdir in [file, missing] {
        let config = ClientConfig::builder()
            .workdir(workdir)
            .build()
            .expect("filesystem state is deliberately deferred to preflight");
        assert!(matches!(
            preflight(config),
            Err(ConnectError::Config(ConfigError::InvalidWorkdir))
        ));
    }
}

#[test]
fn session_control_and_process_input_debug_never_render_credentials() {
    const MARKER: &str = "SECRET_SESSION_TOKEN_MARKER";
    let calls = Arc::new(Mutex::new(Vec::new()));
    let dispatcher = DiagnosticDispatcher::new(Some(Arc::new(ScheduledSink {
        calls: calls.clone(),
        failure: SinkFailure::Never,
    })));
    dispatcher.ingest(DiagnosticInput::SessionControl);
    dispatcher.ingest(DiagnosticInput::Progress(Diagnostic {
        stream: DiagnosticStream::Stdout,
        payload: b"safe progress",
    }));

    let inputs = ProcessInputs::existing_session("1234", Some(OsString::from(MARKER)));
    assert!(!format!("{inputs:?}").contains(MARKER));
    let actual = match calls.lock() {
        Ok(calls) => calls.clone(),
        Err(poisoned) => poisoned.into_inner().clone(),
    };
    assert_eq!(
        actual,
        vec![(DiagnosticStream::Stdout, b"safe progress".to_vec())]
    );
}
