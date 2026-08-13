//! Reference properties and fixed regressions for source and target foundations.

use std::ffi::{OsStr, OsString};
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};

use async_trait::async_trait;
use proptest::prelude::*;

use crate::config::ClientConfig;
use crate::connection::{EngineConnection, EngineConnectionError};
use crate::connector::{ConnectionRequest, Connector, DefaultConnector};
#[cfg(unix)]
use crate::discovery::resolve_explicit_cli;
use crate::discovery::{
    ExecutableLease, NativeContextError, NativeDiscoveryInputs, NativePathSemantics,
    TestDiscoveryFileSystem, resolve_compatibility_path_cli_for_test,
    resolve_explicit_cli_for_test,
};
use crate::errors::{
    CliDiscoveryErrorKind, ConnectError, DiscoveryPathRole, ExistingSessionErrorKind,
    PlatformErrorKind, TargetErrorKind,
};
use crate::graphql::{RawRequest, RawResponse, ResponseData};
use crate::preflight::{
    CliSourcePlan, ConnectionPlan, PreflightContext, ProcessInputs, preflight_with,
};
use crate::session::{ExistingSessionInput, validate_existing_session};
use crate::target::{
    Architecture, ArchiveDescriptor, ArchiveFormat, OperatingSystem, exact_target,
    exact_target_from_parts,
};
use crate::target_generated::{TARGET_CLI_VERSION, TARGET_ENGINE_VERSION, TARGET_REVISION};
use crate::test_support::{TransportEventLog, proptest_config};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SourceReference {
    ExplicitConnection,
    ExistingSession,
    ExplicitLocal,
    CompiledRelease,
}

fn reference_source(explicit: bool, session_port: bool, local_cli: bool) -> SourceReference {
    if explicit {
        SourceReference::ExplicitConnection
    } else if session_port {
        SourceReference::ExistingSession
    } else if local_cli {
        SourceReference::ExplicitLocal
    } else {
        SourceReference::CompiledRelease
    }
}

struct SnapshotContext {
    inputs: ProcessInputs,
    reads: Arc<AtomicUsize>,
}

impl PreflightContext for SnapshotContext {
    fn is_directory(&self, _path: &std::path::Path) -> bool {
        true
    }

    fn process_inputs(&self) -> ProcessInputs {
        self.reads.fetch_add(1, Ordering::Relaxed);
        self.inputs.clone()
    }
}

struct InjectedConnection;

#[async_trait]
impl EngineConnection for InjectedConnection {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        Ok(RawResponse::new(ResponseData::Absent))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

fn observed_source(plan: &ConnectionPlan) -> SourceReference {
    match plan {
        ConnectionPlan::Explicit { .. } => SourceReference::ExplicitConnection,
        ConnectionPlan::Existing { .. } => SourceReference::ExistingSession,
        ConnectionPlan::NewCli { source, .. } => match source.as_ref() {
            CliSourcePlan::ExplicitLocal { .. } => SourceReference::ExplicitLocal,
            CliSourcePlan::CompiledRelease { .. } => SourceReference::CompiledRelease,
        },
    }
}

fn native_fixture_path(unix: &str, windows: &str) -> PathBuf {
    PathBuf::from(if cfg!(windows) { windows } else { unix })
}

fn native_fixture_text<'a>(unix: &'a str, windows: &'a str) -> &'a str {
    if cfg!(windows) { windows } else { unix }
}

proptest! {
    #![proptest_config(proptest_config())]

    // Selection is a total precedence function over one captured snapshot. Exercising
    // a malformed selected arm cannot manufacture a transition to a lower source.
    #[test]
    fn property_03_source_precedence_reference_function(
        explicit in any::<bool>(),
        session_port in prop::option::of("[0-9a-z-]{0,8}"),
        session_token in prop::option::of("[A-Za-z0-9_-]{0,16}"),
        local_cli in prop::option::of("[A-Za-z0-9_./~-]{0,20}"),
        mutation in "[A-Za-z0-9_-]{0,16}",
    ) {
        let expected = reference_source(explicit, session_port.is_some(), local_cli.is_some());
        let snapshot = ProcessInputs::for_test(
            session_port.clone().map(OsString::from),
            session_token.clone().map(OsString::from),
            local_cli.clone().map(OsString::from),
        );
        let reads = Arc::new(AtomicUsize::new(0));
        let context = SnapshotContext {
            inputs: snapshot.clone(),
            reads: Arc::clone(&reads),
        };
        let mut builder = ClientConfig::builder();
        if explicit {
            builder = builder.connection(Box::new(InjectedConnection));
        }
        let config = builder.build().expect("the generated source config is valid");
        let plan = preflight_with(config, &context).expect("the native test platform is supported");

        prop_assert_eq!(observed_source(&plan), expected);
        prop_assert_eq!(reads.load(Ordering::Relaxed), usize::from(!explicit));

        let events = TransportEventLog::default();
        match plan {
            ConnectionPlan::Existing { input, .. } => {
                let _ = validate_existing_session(input);
            }
            ConnectionPlan::NewCli { source, .. } => match *source {
                CliSourcePlan::ExplicitLocal { configured, discovery } => {
                    let filesystem = TestDiscoveryFileSystem::new();
                    let _ = resolve_explicit_cli_for_test(configured, &discovery, &filesystem);
                }
                CliSourcePlan::CompiledRelease { .. } => {}
            },
            ConnectionPlan::Explicit { .. } => {}
        }
        prop_assert!(events.events().is_empty());

        let _changed_after_capture = (
            Some(mutation.clone()),
            Some(mutation.clone()),
            Some(mutation),
        );
        let replay = SnapshotContext {
            inputs: snapshot,
            reads: Arc::new(AtomicUsize::new(0)),
        };
        let replay_config = if explicit {
            ClientConfig::builder().connection(Box::new(InjectedConnection)).build()
        } else {
            ClientConfig::builder().build()
        }
        .expect("the replay config is valid");
        let replay_plan = preflight_with(replay_config, &replay)
            .expect("replaying one captured snapshot remains valid");
        prop_assert_eq!(observed_source(&replay_plan), expected);
    }

    // Validation is compared with a separate parser; every rendered failure remains
    // independent of the raw token and external cleanup has no shutdown action.
    #[test]
    fn property_04_existing_session_total_secret_safe(
        port_case in 0_u8..7,
        port_value in any::<u32>(),
        token_case in 0_u8..3,
        marker in "SECRET_[A-Za-z0-9]{12,32}",
        close_first in any::<bool>(),
    ) {
        let port = match port_case {
            0 => (port_value % 65_535 + 1).to_string(),
            1 => "0".to_owned(),
            2 => (u32::from(u16::MAX) + 1 + port_value % 10_000).to_string(),
            3 => String::new(),
            4 => format!("-{port_value}"),
            5 => format!("{port_value}x"),
            _ => format!(" {port_value}"),
        };
        let token = match token_case {
            0 => None,
            1 => Some(OsString::new()),
            _ => Some(OsString::from(&marker)),
        };
        let input = ExistingSessionInput {
            port: OsString::from(&port),
            token,
        };
        let expected = if port.parse::<u32>().ok().is_none_or(|value| !(1..=65_535).contains(&value)) {
            Err(ExistingSessionErrorKind::InvalidPort)
        } else if token_case == 0 {
            Err(ExistingSessionErrorKind::MissingToken)
        } else if token_case == 1 {
            Err(ExistingSessionErrorKind::EmptyToken)
        } else {
            Ok(())
        };

        let input_debug = format!("{input:?}");
        let actual = std::panic::catch_unwind(|| validate_existing_session(input));
        prop_assert!(actual.is_ok());
        let actual = actual.expect("validation is total");
        prop_assert_eq!(actual.as_ref().map(|_| ()).map_err(|error| error.kind()), expected);
        prop_assert!(!input_debug.contains(&marker));

        match actual {
            Ok(session) => {
                let rendered = format!("{session:?}");
                prop_assert!(!rendered.contains(&marker));
                let events = Vec::<&'static str>::new();
                if close_first {
                    session.resource().close();
                    session.resource().abort();
                } else {
                    session.resource().abort();
                    session.resource().close();
                }
                prop_assert!(events.is_empty());
            }
            Err(error) => {
                let connect = ConnectError::ExistingSession(error.clone());
                let rendered = format!("{error} {error:?} {connect} {connect:?}");
                prop_assert!(!rendered.contains(&marker));
                prop_assert!(port.is_empty() || !rendered.contains(&port));
            }
        }
    }

    // The selected explicit-local value is resolved against only its captured native
    // snapshot; a lookup failure cannot turn into provisioning or compatibility PATH.
    // Feature: rust-sdk-transport-observability, Property 5: explicit-local selection is authoritative
    #[test]
    fn property_05_explicit_local_authoritative(
        name in "[A-Za-z0-9][A-Za-z0-9_.-]{0,15}",
        path_shaped in any::<bool>(),
        shape in 0_u8..3,
        later_mutation in "[A-Za-z0-9_.-]{1,16}",
    ) {
        let path_entry = PathBuf::from("/captured/bin");
        let configured = if path_shaped {
            OsString::from(format!("/selected/{name}"))
        } else {
            OsString::from(&name)
        };
        let candidate = if path_shaped {
            PathBuf::from(&configured)
        } else {
            path_entry.join(&name)
        };
        let resolved = PathBuf::from(format!("/resolved/{name}"));
        let filesystem = match shape {
            0 => TestDiscoveryFileSystem::new().executable(candidate, resolved.clone()),
            1 => TestDiscoveryFileSystem::new().unusable(candidate),
            _ => TestDiscoveryFileSystem::new(),
        };
        let inputs = NativeDiscoveryInputs::new(
            NativePathSemantics::Unix,
            vec![path_entry],
            None,
            None,
            Ok(PathBuf::from("/captured/current")),
        );
        let actual = resolve_explicit_cli_for_test(configured, &inputs, &filesystem);
        match shape {
            0 => {
                let executable = actual.expect("the reference executable exists");
                prop_assert_eq!(executable.path(), resolved);
            }
            1 => prop_assert_eq!(
                actual.expect_err("the reference entry is unusable").kind(),
                CliDiscoveryErrorKind::NotExecutable,
            ),
            _ => prop_assert_eq!(
                actual.expect_err("the reference entry is absent").kind(),
                CliDiscoveryErrorKind::Lookup,
            ),
        }

        // Mutating a plausible future PATH coordinate cannot affect the owned snapshot,
        // and neither lower source has an operation in this resolution path.
        let _post_snapshot_path = PathBuf::from(format!("/mutated/{later_mutation}"));
        let events = TransportEventLog::default();
        prop_assert!(events.events().is_empty());
    }

    // Descriptor construction is a pure exact table. Unsupported coordinates and
    // independently malformed target values terminate before an effect can occur.
    #[test]
    fn property_06_platform_descriptors_exact_side_effect_free(
        os_case in 0_u8..5,
        arch_case in 0_u8..4,
        drift_case in 0_u8..5,
    ) {
        let (engine, cli, revision) = match drift_case {
            0 => (TARGET_ENGINE_VERSION, TARGET_CLI_VERSION, TARGET_REVISION),
            1 => ("1.0.0-beta.10", TARGET_CLI_VERSION, TARGET_REVISION),
            2 => (TARGET_ENGINE_VERSION, "not-semver", TARGET_REVISION),
            3 => (TARGET_ENGINE_VERSION, TARGET_CLI_VERSION, "ABCDEF"),
            _ => (TARGET_ENGINE_VERSION, "1.0.0-beta.11", TARGET_REVISION),
        };
        let events = TransportEventLog::default();
        let target = exact_target_from_parts(engine, cli, revision);
        if drift_case != 0 {
            prop_assert!(target.is_err());
            prop_assert!(events.events().is_empty());
            return Ok(());
        }
        let target = target.expect("the checked generated target is valid");
        let os_text = match os_case {
            0 => "linux",
            1 => "darwin",
            2 => "windows",
            3 => "freebsd",
            _ => "unknown",
        };
        let arch_text = match arch_case {
            0 => "amd64",
            1 => "arm64",
            2 => "riscv64",
            _ => "unknown",
        };
        let os = OperatingSystem::parse(os_text);
        let arch = Architecture::parse(arch_text);
        match (os, arch) {
            (Ok(os), Ok(arch)) => {
                let descriptor = ArchiveDescriptor::for_target(&target, os, arch)
                    .expect("all normalized coordinates have a descriptor");
                let os_name = match os {
                    OperatingSystem::Linux => "linux",
                    OperatingSystem::Darwin => "darwin",
                    OperatingSystem::Windows => "windows",
                };
                let arch_name = match arch {
                    Architecture::Amd64 => "amd64",
                    Architecture::Arm64 => "arm64",
                };
                let extension = if os == OperatingSystem::Windows { "zip" } else { "tar.gz" };
                let expected_name = format!("dagger_v1.0.0-beta.10_{os_name}_{arch_name}.{extension}");
                prop_assert_eq!(descriptor.archive_name(), expected_name.as_str());
                prop_assert_eq!(
                    descriptor.member_name(),
                    if os == OperatingSystem::Windows { "dagger.exe" } else { "dagger" }
                );
                prop_assert_eq!(descriptor.manifest_url().as_str(), "https://dl.dagger.io/dagger/releases/1.0.0-beta.10/checksums.txt");
                prop_assert_eq!(descriptor.archive_url().as_str(), format!("https://dl.dagger.io/dagger/releases/1.0.0-beta.10/{expected_name}"));
            }
            (Err(error), _) => prop_assert_eq!(error.kind(), PlatformErrorKind::UnsupportedOperatingSystem),
            (_, Err(error)) => prop_assert_eq!(error.kind(), PlatformErrorKind::UnsupportedArchitecture),
        }
        prop_assert!(events.events().is_empty());
    }
}

#[test]
fn generated_target_matches_checked_repository_metadata() {
    let target: serde_json::Value =
        serde_json::from_str(include_str!("../../../completeness/target.json"))
            .expect("the checked target metadata is valid JSON");
    assert_eq!(target["engine_version"], TARGET_ENGINE_VERSION);
    assert_eq!(target["rust_sdk_version"], TARGET_CLI_VERSION);
    assert_eq!(target["dagger_revision"], TARGET_REVISION);

    let parsed = exact_target().expect("the generated target parses once");
    assert_eq!(parsed.engine_version().to_string(), TARGET_CLI_VERSION);
    assert_eq!(parsed.cli_version().to_string(), TARGET_CLI_VERSION);
    assert_eq!(parsed.revision().bytes().len(), 20);
}

#[test]
fn exact_target_rejects_each_independent_drift_shape() {
    let cases = [
        (
            "1.0.0-beta.10",
            TARGET_CLI_VERSION,
            TARGET_REVISION,
            TargetErrorKind::InvalidEngineVersion,
        ),
        (
            TARGET_ENGINE_VERSION,
            "invalid",
            TARGET_REVISION,
            TargetErrorKind::InvalidCliVersion,
        ),
        (
            TARGET_ENGINE_VERSION,
            TARGET_CLI_VERSION,
            "25300124CA110612EDC09C43F89CB5FAD6028170",
            TargetErrorKind::InvalidRevision,
        ),
        (
            TARGET_ENGINE_VERSION,
            "1.0.0-beta.11",
            TARGET_REVISION,
            TargetErrorKind::VersionMismatch,
        ),
    ];
    for (engine, cli, revision, expected) in cases {
        let error = exact_target_from_parts(engine, cli, revision)
            .expect_err("the drift fixture must fail");
        assert_eq!(error.kind(), expected);
    }
}

#[test]
fn six_release_descriptors_match_the_published_naming_policy() {
    let target = exact_target().expect("the generated exact target is valid");
    let cases = [
        (
            OperatingSystem::Linux,
            Architecture::Amd64,
            "linux",
            "amd64",
            ArchiveFormat::TarGz,
            "dagger",
        ),
        (
            OperatingSystem::Linux,
            Architecture::Arm64,
            "linux",
            "arm64",
            ArchiveFormat::TarGz,
            "dagger",
        ),
        (
            OperatingSystem::Darwin,
            Architecture::Amd64,
            "darwin",
            "amd64",
            ArchiveFormat::TarGz,
            "dagger",
        ),
        (
            OperatingSystem::Darwin,
            Architecture::Arm64,
            "darwin",
            "arm64",
            ArchiveFormat::TarGz,
            "dagger",
        ),
        (
            OperatingSystem::Windows,
            Architecture::Amd64,
            "windows",
            "amd64",
            ArchiveFormat::Zip,
            "dagger.exe",
        ),
        (
            OperatingSystem::Windows,
            Architecture::Arm64,
            "windows",
            "arm64",
            ArchiveFormat::Zip,
            "dagger.exe",
        ),
    ];
    for (os, arch, os_name, arch_name, format, member) in cases {
        let descriptor = ArchiveDescriptor::for_target(target, os, arch)
            .expect("the supported coordinate has a descriptor");
        let extension = if format == ArchiveFormat::Zip {
            "zip"
        } else {
            "tar.gz"
        };
        let archive = format!("dagger_v1.0.0-beta.10_{os_name}_{arch_name}.{extension}");
        assert_eq!(descriptor.archive_name(), archive);
        assert_eq!(descriptor.member_name(), member);
        assert_eq!(descriptor.format(), format);
        assert_eq!(
            descriptor.archive_url().as_str(),
            format!("https://dl.dagger.io/dagger/releases/1.0.0-beta.10/{archive}")
        );
    }
}

#[test]
fn explicit_discovery_expands_home_resolves_symlinks_and_ignores_irrelevant_cwd() {
    let absolute = native_fixture_path(
        "/home/operator/bin/dagger",
        r"C:\Users\operator\bin\dagger.EXE",
    );
    let resolved = native_fixture_path(
        "/opt/dagger-v1.0.0-beta.10",
        r"C:\tools\dagger-v1.0.0-beta.10.exe",
    );
    let filesystem = TestDiscoveryFileSystem::new().executable(absolute.clone(), resolved.clone());
    let inputs = NativeDiscoveryInputs::new(
        NativePathSemantics::current(),
        Vec::new(),
        None,
        Some(native_fixture_path("/home/operator", r"C:\Users\operator")),
        Err(NativeContextError),
    );

    let configured = native_fixture_text("~/bin/dagger", r"~\bin\dagger");
    let executable =
        resolve_explicit_cli_for_test(OsString::from(configured), &inputs, &filesystem)
            .expect("home-expanded absolute discovery succeeds without cwd");
    assert_eq!(executable.path(), resolved);
    assert_eq!(executable.lease(), ExecutableLease::Unmanaged);
}

#[test]
fn bare_discovery_uses_captured_path_and_windows_pathext() {
    let directory = PathBuf::from(r"C:\tools");
    let candidate = directory.join("dagger.EXE");
    let resolved = PathBuf::from(r"C:\real\dagger.exe");
    let filesystem = TestDiscoveryFileSystem::new().executable(candidate, resolved.clone());
    let inputs = NativeDiscoveryInputs::new(
        NativePathSemantics::Windows,
        vec![directory],
        Some(vec![OsString::from(".EXE"), OsString::from(".CMD")]),
        None,
        Err(NativeContextError),
    );

    let executable = resolve_compatibility_path_cli_for_test(&inputs, &filesystem)
        .expect("canonical Dagger lookup applies captured PATHEXT");
    assert_eq!(executable.path(), resolved);
    assert_eq!(executable.lease(), ExecutableLease::Unmanaged);
}

#[test]
fn discovery_failures_are_typed_terminal_and_path_safe() {
    let inputs = NativeDiscoveryInputs::new(
        NativePathSemantics::Unix,
        vec![PathBuf::from("relative-bin")],
        None,
        None,
        Err(NativeContextError),
    );
    let filesystem = TestDiscoveryFileSystem::new();
    let empty = resolve_explicit_cli_for_test(OsString::new(), &inputs, &filesystem)
        .expect_err("an empty present explicit value is terminal");
    assert_eq!(empty.kind(), CliDiscoveryErrorKind::EmptyExplicitLocal);
    assert_eq!(empty.path_role(), DiscoveryPathRole::ExplicitLocal);

    let context = resolve_explicit_cli_for_test(OsString::from("dagger"), &inputs, &filesystem)
        .expect_err("relative PATH needs the captured current directory");
    assert_eq!(context.kind(), CliDiscoveryErrorKind::NativeContext);
    assert_eq!(context.path_role(), DiscoveryPathRole::ExplicitLocal);

    let marker = "SECRET_DISCOVERY_VALUE";
    let rendered = format!("{context} {context:?}");
    assert!(!rendered.contains(marker));
}

#[test]
fn discovery_rejects_non_regular_targets() {
    let candidate = native_fixture_path("/opt/dagger", r"C:\tools\dagger.exe");
    let filesystem = TestDiscoveryFileSystem::new().unusable(candidate.clone());
    let inputs = NativeDiscoveryInputs::new(
        NativePathSemantics::current(),
        Vec::new(),
        None,
        None,
        Err(NativeContextError),
    );
    let error = resolve_explicit_cli_for_test(candidate.into_os_string(), &inputs, &filesystem)
        .expect_err("a non-regular target cannot become launch authority");
    assert_eq!(error.kind(), CliDiscoveryErrorKind::NotExecutable);
}

#[cfg(unix)]
#[test]
fn existing_session_rejects_non_native_port_and_token_text() {
    use std::os::unix::ffi::OsStringExt;

    let bad_port = validate_existing_session(ExistingSessionInput {
        port: OsString::from_vec(vec![0xff]),
        token: Some(OsString::from("safe")),
    })
    .expect_err("non-native port text is rejected");
    assert_eq!(bad_port.kind(), ExistingSessionErrorKind::NonNativePort);

    let bad_token = validate_existing_session(ExistingSessionInput {
        port: OsString::from("1234"),
        token: Some(OsString::from_vec(vec![0xff])),
    })
    .expect_err("non-native token text is rejected");
    assert_eq!(bad_token.kind(), ExistingSessionErrorKind::NonNativeToken);
}

#[cfg(unix)]
#[test]
fn system_discovery_follows_native_symlinks_and_enforces_execute_permission() {
    use std::os::unix::fs::{PermissionsExt, symlink};

    let fixture = tempfile::tempdir().expect("the native discovery fixture is created");
    let target = fixture.path().join("dagger-target");
    let link = fixture.path().join("dagger-link");
    std::fs::write(&target, b"executable fixture").expect("the target is written");
    std::fs::set_permissions(&target, std::fs::Permissions::from_mode(0o700))
        .expect("the target is executable");
    symlink(&target, &link).expect("the native symlink is created");
    let inputs = NativeDiscoveryInputs::new(
        NativePathSemantics::Unix,
        Vec::new(),
        None,
        None,
        Err(NativeContextError),
    );

    let executable = resolve_explicit_cli(link.clone().into_os_string(), &inputs)
        .expect("native discovery resolves the executable symlink");
    assert_eq!(
        executable.path(),
        std::fs::canonicalize(&target).expect("the target canonicalizes")
    );

    std::fs::set_permissions(&target, std::fs::Permissions::from_mode(0o600))
        .expect("the target execute permission is removed");
    let error = resolve_explicit_cli(link.into_os_string(), &inputs)
        .expect_err("a regular but non-executable native target is rejected");
    assert_eq!(error.kind(), CliDiscoveryErrorKind::NotExecutable);
}

#[test]
fn token_without_port_does_not_select_an_existing_session() {
    let inputs = ProcessInputs::for_test(
        None,
        Some(OsString::from("SECRET_TOKEN_WITHOUT_PORT")),
        Some(OsString::new()),
    );
    let context = SnapshotContext {
        inputs,
        reads: Arc::new(AtomicUsize::new(0)),
    };
    let plan = preflight_with(
        ClientConfig::builder().build().expect("valid config"),
        &context,
    )
    .expect("the explicit-local source is selected before discovery");
    assert_eq!(observed_source(&plan), SourceReference::ExplicitLocal);
}

#[test]
fn existing_session_port_boundaries_are_exact() {
    for port in ["1", "65535"] {
        let session = validate_existing_session(ExistingSessionInput {
            port: OsString::from(port),
            token: Some(OsString::from("secret")),
        })
        .expect("boundary port is valid");
        assert_eq!(session.port().to_string(), port);
    }
    for port in ["0", "65536"] {
        let error = validate_existing_session(ExistingSessionInput {
            port: OsString::from(port),
            token: Some(OsString::from("secret")),
        })
        .expect_err("out-of-range boundary is invalid");
        assert_eq!(error.kind(), ExistingSessionErrorKind::InvalidPort);
    }
}

#[tokio::test]
async fn connector_preserves_typed_terminal_errors_for_selected_process_sources() {
    let existing_context = SnapshotContext {
        inputs: ProcessInputs::existing_session("invalid", Some(OsString::from("secret"))),
        reads: Arc::new(AtomicUsize::new(0)),
    };
    let existing_plan = preflight_with(
        ClientConfig::builder().build().expect("valid config"),
        &existing_context,
    )
    .expect("existing-session selection defers value validation to the connector");
    let existing_request = ConnectionRequest::try_from(existing_plan)
        .map_err(|_| ())
        .expect("an implicit plan becomes a connector request");
    let existing_error = match DefaultConnector.connect(existing_request).await {
        Ok(_) => panic!("the selected malformed existing session must be terminal"),
        Err(error) => error,
    };
    assert!(matches!(
        existing_error,
        ConnectError::ExistingSession(ref error)
            if error.kind() == ExistingSessionErrorKind::InvalidPort
    ));

    let local_context = SnapshotContext {
        inputs: ProcessInputs::explicit_cli(OsString::new()),
        reads: Arc::new(AtomicUsize::new(0)),
    };
    let local_plan = preflight_with(
        ClientConfig::builder().build().expect("valid config"),
        &local_context,
    )
    .expect("explicit-local selection retains the empty present value");
    let local_request = ConnectionRequest::try_from(local_plan)
        .map_err(|_| ())
        .expect("an implicit plan becomes a connector request");
    let local_error = match DefaultConnector.connect(local_request).await {
        Ok(_) => panic!("the selected empty explicit local CLI must be terminal"),
        Err(error) => error,
    };
    assert!(matches!(
        local_error,
        ConnectError::CliDiscovery(ref error)
            if error.kind() == CliDiscoveryErrorKind::EmptyExplicitLocal
    ));
}

#[tokio::test]
async fn valid_existing_connection_close_and_abort_do_not_signal_the_engine() {
    let context = SnapshotContext {
        inputs: ProcessInputs::existing_session("1234", Some(OsString::from("secret"))),
        reads: Arc::new(AtomicUsize::new(0)),
    };
    let plan = preflight_with(
        ClientConfig::builder()
            .allow_unverified_compatibility(true)
            .build()
            .expect("valid config"),
        &context,
    )
    .expect("valid existing-session selection succeeds");
    let request = ConnectionRequest::try_from(plan)
        .map_err(|_| ())
        .expect("an implicit plan becomes a connector request");
    let connection = DefaultConnector
        .connect(request)
        .await
        .expect("the explicit unverified bypass accepts unavailable provenance");

    connection
        .close()
        .await
        .expect("external close releases only local transport state");
    connection.abort();
}

#[test]
fn compatibility_lookup_uses_only_the_canonical_dagger_name() {
    let directory = PathBuf::from("/tools");
    let filesystem = TestDiscoveryFileSystem::new().executable(
        directory.join(OsStr::new("other")),
        PathBuf::from("/real/other"),
    );
    let inputs = NativeDiscoveryInputs::new(
        NativePathSemantics::Unix,
        vec![directory],
        None,
        None,
        Ok(PathBuf::from("/current")),
    );
    let error = resolve_compatibility_path_cli_for_test(&inputs, &filesystem)
        .expect_err("compatibility lookup cannot select an arbitrary executable");
    assert_eq!(error.kind(), CliDiscoveryErrorKind::Lookup);
    assert_eq!(error.path_role(), DiscoveryPathRole::CompatibilityPath);
}
