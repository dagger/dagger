//! Properties and fixed examples for the public-value foundation.

use std::error::Error;
use std::ffi::OsString;
use std::fmt;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use async_trait::async_trait;
use proptest::prelude::*;
use serde_json::{Map, Value};

use crate::config::ClientConfig;
use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
use crate::diagnostic::{Diagnostic, DiagnosticSink, DiagnosticSinkError};
use crate::errors::{
    CloseError, ConfigError, ConfigOption, ConnectError, QueryBuildError, QueryBuildErrorKind,
    QueryError, RequestEncodingError, RequestEncodingErrorKind, RequestError,
    ResponseDecodingError, ResponseDecodingErrorKind, TimeoutPhase,
};
use crate::graphql::{RawRequest, RawResponse};
use crate::test_support::{
    ConfigCase, ConfigMutation, EnvironmentCase, EnvironmentMutation, config_case,
    environment_case, malformed_response_wire, proptest_config, raw_exchange,
};

#[derive(Default)]
struct BoundaryCounts {
    execute: AtomicUsize,
    close: AtomicUsize,
    abort: AtomicUsize,
    diagnostics: AtomicUsize,
}

struct TestConnection(Arc<BoundaryCounts>);

#[async_trait]
impl EngineConnection for TestConnection {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.0.execute.fetch_add(1, Ordering::Relaxed);
        Ok(RawResponse::new(crate::ResponseData::Absent))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        self.0.close.fetch_add(1, Ordering::Relaxed);
        Ok(())
    }

    fn abort(&self) {
        self.0.abort.fetch_add(1, Ordering::Relaxed);
    }
}

struct TestSink(Arc<BoundaryCounts>);

impl DiagnosticSink for TestSink {
    fn emit(&self, _diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
        self.0.diagnostics.fetch_add(1, Ordering::Relaxed);
        Ok(())
    }
}

fn build_config(
    case: &ConfigCase,
    counts: Arc<BoundaryCounts>,
) -> Result<ClientConfig, ConfigError> {
    let mut builder = ClientConfig::builder();
    if let Some(value) = &case.workdir {
        builder = builder.workdir(value);
    }
    if let Some(value) = &case.workspace {
        builder = builder.workspace(value);
    }
    if case.diagnostic_sink {
        builder = builder.diagnostic_sink(Arc::new(TestSink(counts.clone())));
    }
    if let Some(value) = case.load_modules {
        builder = builder.load_workspace_modules(value);
    }
    if let Some(value) = &case.version {
        builder = builder.version(value);
    }
    if let Some(value) = case.verbosity {
        builder = builder.verbosity(value);
    }
    if let Some(value) = &case.runner_host {
        builder = builder.runner_host(value);
    }
    if let Some(value) = case.startup_secs {
        builder = builder.session_startup_timeout(Duration::from_secs(value));
    }
    if let Some(value) = case.http_secs {
        builder = builder.http_connect_timeout(Duration::from_secs(value));
    }
    if let Some(value) = case.execution_secs {
        builder = builder.graphql_execution_timeout(Duration::from_secs(value));
    }

    builder = match case.mutation {
        ConfigMutation::None => builder,
        ConfigMutation::EmptyWorkdir => builder.workdir(""),
        ConfigMutation::EmptyWorkspace => builder.workspace(""),
        ConfigMutation::InvalidVersion => builder.version("not-a-version"),
        ConfigMutation::InvalidRunnerHost => builder.runner_host("relative/runner"),
        ConfigMutation::ZeroStartup => builder.session_startup_timeout(Duration::ZERO),
        ConfigMutation::ZeroHttpConnect => builder.http_connect_timeout(Duration::ZERO),
        ConfigMutation::ZeroExecution => builder.graphql_execution_timeout(Duration::ZERO),
        ConfigMutation::VerbosityOverflow => builder.verbosity(u8::MAX as u64 + 1),
    };

    if case.explicit_connection {
        builder = builder.connection(Box::new(TestConnection(counts)));
    }
    builder.build()
}

fn expected_config_error(case: &ConfigCase) -> Option<ConfigError> {
    let scalar = match case.mutation {
        ConfigMutation::None => None,
        ConfigMutation::EmptyWorkdir => Some(ConfigError::InvalidWorkdir),
        ConfigMutation::EmptyWorkspace => Some(ConfigError::InvalidWorkspace),
        ConfigMutation::InvalidVersion => Some(ConfigError::InvalidVersion),
        ConfigMutation::InvalidRunnerHost => Some(ConfigError::InvalidRunnerHost),
        ConfigMutation::ZeroStartup => Some(ConfigError::InvalidTimeout {
            phase: TimeoutPhase::SessionStartup,
        }),
        ConfigMutation::ZeroHttpConnect => Some(ConfigError::InvalidTimeout {
            phase: TimeoutPhase::HttpConnect,
        }),
        ConfigMutation::ZeroExecution => Some(ConfigError::InvalidTimeout {
            phase: TimeoutPhase::GraphQlExecution,
        }),
        ConfigMutation::VerbosityOverflow => Some(ConfigError::VerbosityOutOfRange),
    };
    if scalar.is_some() {
        return scalar;
    }
    if !case.explicit_connection {
        return None;
    }
    let option = if case.workdir.is_some() {
        Some(ConfigOption::Workdir)
    } else if case.workspace.is_some() {
        Some(ConfigOption::Workspace)
    } else if case.diagnostic_sink {
        Some(ConfigOption::DiagnosticSink)
    } else if case.load_modules.is_some() {
        Some(ConfigOption::LoadWorkspaceModules)
    } else if case.version.is_some() {
        Some(ConfigOption::Version)
    } else if case.verbosity.is_some_and(|value| value > 0) {
        Some(ConfigOption::Verbosity)
    } else if case.runner_host.is_some() {
        Some(ConfigOption::RunnerHost)
    } else if case.startup_secs.is_some() {
        Some(ConfigOption::SessionStartupTimeout)
    } else if case.http_secs.is_some() {
        Some(ConfigOption::HttpConnectTimeout)
    } else {
        None
    };
    option.map(|option| ConfigError::ExplicitConnectionConflict { option })
}

fn reference_request_wire(request: &RawRequest) -> Value {
    let mut wire = Map::new();
    wire.insert("query".into(), Value::String(request.query().into()));
    if let Some(variables) = request.variables() {
        wire.insert("variables".into(), variables.clone());
    }
    if let Some(operation_name) = request.operation_name() {
        wire.insert("operationName".into(), Value::String(operation_name.into()));
    }
    Value::Object(wire)
}

fn reference_response_wire(response: &RawResponse) -> Value {
    let mut wire = Map::new();
    match response.data() {
        crate::ResponseData::Absent => {}
        crate::ResponseData::Null => {
            wire.insert("data".into(), Value::Null);
        }
        crate::ResponseData::Value(value) => {
            wire.insert("data".into(), value.clone());
        }
    }
    if !response.errors().is_empty() {
        let errors = response
            .errors()
            .iter()
            .map(|error| {
                let mut value = Map::new();
                value.insert("message".into(), Value::String(error.message().into()));
                if !error.locations().is_empty() {
                    value.insert(
                        "locations".into(),
                        Value::Array(
                            error
                                .locations()
                                .iter()
                                .map(|location| {
                                    Value::Object(Map::from_iter([
                                        ("line".into(), Value::from(location.line())),
                                        ("column".into(), Value::from(location.column())),
                                    ]))
                                })
                                .collect(),
                        ),
                    );
                }
                if !error.path().is_empty() {
                    value.insert(
                        "path".into(),
                        Value::Array(
                            error
                                .path()
                                .iter()
                                .map(|segment| match segment {
                                    crate::GraphQlPathSegment::Field(field) => {
                                        Value::String(field.clone())
                                    }
                                    crate::GraphQlPathSegment::Index(index) => Value::from(*index),
                                })
                                .collect(),
                        ),
                    );
                }
                if let Some(extensions) = error.extensions() {
                    value.insert("extensions".into(), Value::Object(extensions.clone()));
                }
                Value::Object(value)
            })
            .collect();
        wire.insert("errors".into(), Value::Array(errors));
    }
    if let Some(extensions) = response.extensions() {
        wire.insert("extensions".into(), Value::Object(extensions.clone()));
    }
    Value::Object(wire)
}

proptest! {
    #![proptest_config(proptest_config())]

    // Construction is a total pure normalization: any rejection is typed and no
    // injected connection or diagnostic boundary can be observed.
    // Feature: rust-sdk-client-lifecycle, Property 11: configuration construction is total and side-effect free
    #[test]
    fn property_11_configuration_construction_is_total_and_side_effect_free(case in config_case()) {
        let counts = Arc::new(BoundaryCounts::default());
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            build_config(&case, counts.clone())
        }));
        prop_assert!(result.is_ok());
        let result = result.expect("the property established that build did not unwind");
        let expected = expected_config_error(&case);
        match (result, expected) {
            (Err(actual), Some(expected)) => prop_assert_eq!(actual, expected),
            (Ok(config), None) => {
                prop_assert_eq!(config.session_startup_timeout(), case.startup_secs.map(Duration::from_secs).unwrap_or(Duration::from_secs(300)));
                prop_assert_eq!(config.http_connect_timeout(), case.http_secs.map(Duration::from_secs).unwrap_or(Duration::from_secs(10)));
                prop_assert_eq!(config.graphql_execution_timeout(), case.execution_secs.map(Duration::from_secs));
                prop_assert_eq!(config.verbosity(), case.verbosity.unwrap_or(0) as u8);
            }
            (actual, expected) => prop_assert!(false, "reference mismatch: actual={actual:?}, expected={expected:?}"),
        }
        prop_assert_eq!(counts.execute.load(Ordering::Relaxed), 0);
        prop_assert_eq!(counts.close.load(Ordering::Relaxed), 0);
        prop_assert_eq!(counts.abort.load(Ordering::Relaxed), 0);
        prop_assert_eq!(counts.diagnostics.load(Ordering::Relaxed), 0);
    }
}

const RESERVED: [&str; 7] = [
    "DAGGER_SESSION_PORT",
    "DAGGER_SESSION_TOKEN",
    "_EXPERIMENTAL_DAGGER_CLI_BIN",
    "_EXPERIMENTAL_DAGGER_RUNNER_HOST",
    "TRACEPARENT",
    "TRACESTATE",
    "BAGGAGE",
];

fn apply_environment_mutation(case: &EnvironmentCase) -> Vec<(String, String)> {
    let mut entries = case.entries.clone();
    match case.mutation {
        EnvironmentMutation::None => {}
        EnvironmentMutation::EmptyKey => entries.push((String::new(), case.marker.clone())),
        EnvironmentMutation::EqualsKey => entries.push(("BAD=KEY".into(), case.marker.clone())),
        EnvironmentMutation::NulKey => entries.push(("BAD\0KEY".into(), case.marker.clone())),
        EnvironmentMutation::NulValue => {
            entries.push(("VALID_KEY".into(), format!("{}\0", case.marker)))
        }
        EnvironmentMutation::Duplicate => {
            if entries.is_empty() {
                entries.push(("Dupe".into(), case.marker.clone()));
            }
            let key = entries[0].0.to_ascii_lowercase();
            entries.push((key, case.marker.clone()));
        }
        EnvironmentMutation::Reserved { index, case_mask } => {
            let key = RESERVED[index]
                .bytes()
                .enumerate()
                .map(|(offset, byte)| {
                    if case_mask & (1_u64 << (offset % 64)) == 0 {
                        byte.to_ascii_lowercase()
                    } else {
                        byte.to_ascii_uppercase()
                    }
                })
                .map(char::from)
                .collect();
            entries.push((key, case.marker.clone()));
        }
        EnvironmentMutation::NonAscii => {
            entries.push((format!("κλειδί_{}", entries.len()), case.marker.clone()));
        }
    }
    entries
}

fn expected_environment_error(
    case: &EnvironmentCase,
    entries: &[(String, String)],
) -> Option<ConfigError> {
    let index = entries.len().saturating_sub(1);
    match case.mutation {
        EnvironmentMutation::None | EnvironmentMutation::NonAscii => None,
        EnvironmentMutation::EmptyKey
        | EnvironmentMutation::EqualsKey
        | EnvironmentMutation::NulKey => Some(ConfigError::InvalidEnvironmentKey { index }),
        EnvironmentMutation::NulValue => Some(ConfigError::InvalidEnvironmentValue { index }),
        EnvironmentMutation::Duplicate => Some(ConfigError::DuplicateEnvironmentKey {
            first: 0,
            duplicate: index,
        }),
        EnvironmentMutation::Reserved { .. } => Some(ConfigError::ReservedEnvironmentKey { index }),
    }
}

proptest! {
    #![proptest_config(proptest_config())]

    // Native environment validation is an ASCII-only reference normalization which
    // preserves accepted order and never renders caller values.
    // Feature: rust-sdk-client-lifecycle, Property 15: additional environment validation is portable
    #[test]
    fn property_15_additional_environment_validation_is_portable(case in environment_case()) {
        let entries = apply_environment_mutation(&case);
        let mut builder = ClientConfig::builder();
        for (key, value) in &entries {
            builder = builder.environment(OsString::from(key), OsString::from(value));
        }
        let result = builder.build();
        match (result, expected_environment_error(&case, &entries)) {
            (Err(actual), Some(expected)) => {
                prop_assert_eq!(&actual, &expected);
                prop_assert!(!actual.to_string().contains(&case.marker));
                let rendered = format!("{actual:?}");
                prop_assert!(!rendered.contains(&case.marker));
            }
            (Ok(config), None) => {
                let actual = config
                    .environment()
                    .map(|(key, value)| (key.to_os_string(), value.to_os_string()))
                    .collect::<Vec<_>>();
                let expected = entries
                    .iter()
                    .map(|(key, value)| (OsString::from(key), OsString::from(value)))
                    .collect::<Vec<_>>();
                prop_assert_eq!(actual, expected);
                let rendered = format!("{config:?}");
                prop_assert!(!rendered.contains(&case.marker));
            }
            (actual, expected) => prop_assert!(false, "reference mismatch: actual={actual:?}, expected={expected:?}"),
        }
    }
}

proptest! {
    #![proptest_config(proptest_config())]

    // The production codec must be a lossless representation of every public raw
    // request/response coordinate, including absent versus explicit null data.
    // Feature: rust-sdk-client-lifecycle, Property 19: raw GraphQL round-trips protocol information
    #[test]
    fn property_19_raw_graphql_round_trips_protocol_information(
        (request, response) in raw_exchange(),
        malformed_response in malformed_response_wire(),
    ) {
        let expected_request_wire = reference_request_wire(&request);
        let request_bytes = request.encode_wire().expect("serde_json::Value requests are encodable");
        let encoded_request_wire: Value = serde_json::from_slice(&request_bytes).expect("the production request encoder emits JSON");
        prop_assert_eq!(encoded_request_wire, expected_request_wire.clone());
        let reference_request_bytes = serde_json::to_vec(&expected_request_wire).expect("the reference request contains JSON values");
        let decoded_request = RawRequest::decode_wire(&reference_request_bytes).expect("reference request has the public wire shape");
        prop_assert_eq!(decoded_request, request);

        let expected_response_wire = reference_response_wire(&response);
        let response_bytes = response.encode_wire().expect("public response values are JSON encodable");
        let encoded_response_wire: Value = serde_json::from_slice(&response_bytes).expect("the production response encoder emits JSON");
        prop_assert_eq!(encoded_response_wire, expected_response_wire.clone());
        let reference_response_bytes = serde_json::to_vec(&expected_response_wire).expect("the reference response contains JSON values");
        let decoded_response = RawResponse::decode_wire(&reference_response_bytes).expect("reference response has the public wire shape");
        prop_assert_eq!(decoded_response, response);

        let malformed_bytes = serde_json::to_vec(&malformed_response).expect("malformed shapes are still JSON");
        let malformed_error = RawResponse::decode_wire(&malformed_bytes).expect_err("the generated malformed response must fail closed");
        prop_assert!(matches!(
            malformed_error,
            RequestError::ResponseDecoding(ref error)
                if error.kind() == ResponseDecodingErrorKind::InvalidShape
        ));
    }
}

#[test]
fn malformed_raw_wire_forms_map_to_their_typed_codec_family() {
    let malformed_request =
        RawRequest::decode_wire(br#"{"query":7}"#).expect_err("a non-string query is malformed");
    assert!(matches!(
        malformed_request,
        RequestError::RequestEncoding(ref error)
            if error.kind() == RequestEncodingErrorKind::Json
    ));

    for malformed_response in [
        br#"[]"#.as_slice(),
        br#"{"errors":{}}"#,
        br#"{"extensions":[]}"#,
        br#"{"errors":[{"message":"bad","path":[-1]}]}"#,
    ] {
        let error = RawResponse::decode_wire(malformed_response)
            .expect_err("the response mutation must fail closed");
        assert!(matches!(
            error,
            RequestError::ResponseDecoding(ref error)
                if error.kind() == ResponseDecodingErrorKind::InvalidShape
        ));
    }

    let error = RawResponse::decode_wire(b"{")
        .expect_err("invalid JSON must use the response decoding family");
    assert!(matches!(
        error,
        RequestError::ResponseDecoding(ref error)
            if error.kind() == ResponseDecodingErrorKind::Json
    ));
}

#[derive(Debug)]
struct SecretSource(&'static str);

impl fmt::Display for SecretSource {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.0)
    }
}

impl Error for SecretSource {}

#[test]
fn public_error_rendering_is_redacted_while_sources_remain_inspectable() {
    const MARKER: &str = "SECRET_ERROR_SOURCE_MARKER";
    let connection = EngineConnectionError::with_source(
        EngineConnectionErrorKind::Transport,
        SecretSource(MARKER),
    );
    let request_encoding =
        RequestEncodingError::with_source(RequestEncodingErrorKind::Json, SecretSource(MARKER));
    let response_decoding =
        ResponseDecodingError::with_source(ResponseDecodingErrorKind::Json, SecretSource(MARKER));
    let query_build =
        QueryBuildError::with_source(QueryBuildErrorKind::ArgumentEncoding, SecretSource(MARKER));
    let diagnostic = DiagnosticSinkError::with_source(SecretSource(MARKER));
    let connect = ConnectError::Connection(connection.clone());
    let request = RequestError::RequestEncoding(request_encoding.clone());
    let query = QueryError::Build(query_build.clone());
    let close = CloseError::Connection(connection.clone());

    for rendered in [
        connection.to_string(),
        format!("{connection:?}"),
        request_encoding.to_string(),
        format!("{request_encoding:?}"),
        response_decoding.to_string(),
        format!("{response_decoding:?}"),
        query_build.to_string(),
        format!("{query_build:?}"),
        diagnostic.to_string(),
        format!("{diagnostic:?}"),
        connect.to_string(),
        format!("{connect:?}"),
        request.to_string(),
        format!("{request:?}"),
        query.to_string(),
        format!("{query:?}"),
        close.to_string(),
        format!("{close:?}"),
    ] {
        assert!(!rendered.contains(MARKER));
    }
    assert!(Error::source(&connection).is_some());
    assert!(Error::source(&request_encoding).is_some());
    assert!(Error::source(&response_decoding).is_some());
    assert!(Error::source(&query_build).is_some());
    assert!(Error::source(&diagnostic).is_some());
    assert!(Error::source(&connect).is_some());
    assert!(Error::source(&request).is_some());
    assert!(Error::source(&query).is_some());
    assert!(matches!(close.clone(), CloseError::Connection(_)));
}

#[test]
fn explicit_connection_rejects_every_cli_or_startup_option_but_allows_execution_timeout() {
    let counts = Arc::new(BoundaryCounts::default());
    let connection = || Box::new(TestConnection(counts.clone())) as Box<dyn EngineConnection>;
    let candidates = vec![
        (
            ConfigOption::Workdir,
            ClientConfig::builder()
                .connection(connection())
                .workdir("."),
        ),
        (
            ConfigOption::Workspace,
            ClientConfig::builder()
                .connection(connection())
                .workspace("workspace"),
        ),
        (
            ConfigOption::DiagnosticSink,
            ClientConfig::builder()
                .connection(connection())
                .diagnostic_sink(Arc::new(TestSink(counts.clone()))),
        ),
        (
            ConfigOption::LoadWorkspaceModules,
            ClientConfig::builder()
                .connection(connection())
                .load_workspace_modules(false),
        ),
        (
            ConfigOption::Version,
            ClientConfig::builder()
                .connection(connection())
                .version("v1.2.3"),
        ),
        (
            ConfigOption::Verbosity,
            ClientConfig::builder()
                .connection(connection())
                .verbosity(1),
        ),
        (
            ConfigOption::RunnerHost,
            ClientConfig::builder()
                .connection(connection())
                .runner_host("tcp://runner.test:1234"),
        ),
        (
            ConfigOption::Environment,
            ClientConfig::builder()
                .connection(connection())
                .environment("SAFE_KEY", "hidden"),
        ),
        (
            ConfigOption::SessionStartupTimeout,
            ClientConfig::builder()
                .connection(connection())
                .session_startup_timeout(Duration::from_secs(300)),
        ),
        (
            ConfigOption::HttpConnectTimeout,
            ClientConfig::builder()
                .connection(connection())
                .http_connect_timeout(Duration::from_secs(10)),
        ),
    ];

    for (option, builder) in candidates {
        assert!(matches!(
            builder.build(),
            Err(ConfigError::ExplicitConnectionConflict { option: actual })
                if actual == option
        ));
    }

    let config = ClientConfig::builder()
        .connection(connection())
        .verbosity(0)
        .graphql_execution_timeout(Duration::from_secs(30))
        .build()
        .expect("request execution timeout is meaningful for an injected connection");
    assert!(config.has_explicit_connection());
    assert_eq!(
        config.graphql_execution_timeout(),
        Some(Duration::from_secs(30))
    );
    assert_eq!(counts.execute.load(Ordering::Relaxed), 0);
    assert_eq!(counts.close.load(Ordering::Relaxed), 0);
    assert_eq!(counts.abort.load(Ordering::Relaxed), 0);
    assert_eq!(counts.diagnostics.load(Ordering::Relaxed), 0);
}

#[test]
fn client_config_debug_exposes_shape_but_not_sensitive_values() {
    const MARKER: &str = "SECRET_CONFIG_MARKER";
    let config = ClientConfig::builder()
        .workdir(format!("/tmp/{MARKER}"))
        .workspace(format!("https://user:{MARKER}@workspace.test/project"))
        .runner_host(format!("tcp://{MARKER}.example:1234"))
        .environment("SAFE_KEY", MARKER)
        .build()
        .expect("the marker-bearing values are structurally valid");

    let rendered = format!("{config:?}");
    assert!(!rendered.contains(MARKER));
    assert!(rendered.contains("workdir_present: true"));
    assert!(rendered.contains("workspace_present: true"));
    assert!(rendered.contains("runner_host_present: true"));
    assert!(rendered.contains("SAFE_KEY"));
}

#[test]
fn every_reserved_environment_key_is_ascii_case_insensitive() {
    for key in RESERVED {
        let mixed = key
            .bytes()
            .enumerate()
            .map(|(index, byte)| {
                if index % 2 == 0 {
                    byte.to_ascii_lowercase()
                } else {
                    byte.to_ascii_uppercase()
                }
            })
            .map(char::from)
            .collect::<String>();
        assert!(matches!(
            ClientConfig::builder().environment(mixed, "hidden").build(),
            Err(ConfigError::ReservedEnvironmentKey { index: 0 })
        ));
    }
}

#[test]
fn diagnostic_and_connection_contracts_are_send_sync() {
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<TestConnection>();
    assert_send_sync::<TestSink>();
}
