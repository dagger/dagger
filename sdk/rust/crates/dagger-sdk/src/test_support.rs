//! Independent valid-first generators used by the Feature 2 foundation properties.
//!
//! Reference cases are deliberately expressed without calling production validators
//! or wire codecs. A shared defect in normalization therefore cannot certify itself.

use proptest::prelude::*;
use serde_json::{Map, Number, Value, json};

use crate::graphql::{
    GraphQlError, GraphQlLocation, GraphQlPathSegment, RawRequest, RawResponse, ResponseData,
};

pub(crate) const PROPTEST_CASES: u32 = 256;

pub(crate) fn proptest_config() -> proptest::test_runner::Config {
    proptest::test_runner::Config {
        cases: PROPTEST_CASES,
        failure_persistence: Some(Box::new(
            proptest::test_runner::FileFailurePersistence::Direct(concat!(
                env!("CARGO_MANIFEST_DIR"),
                "/proptest-regressions/client-lifecycle.txt"
            )),
        )),
        ..proptest::test_runner::Config::default()
    }
}

pub(crate) fn bounded_json() -> impl Strategy<Value = Value> {
    let leaf = prop_oneof![
        Just(Value::Null),
        any::<bool>().prop_map(Value::Bool),
        any::<i32>().prop_map(|value| Value::Number(Number::from(value))),
        "[a-zA-Z0-9 _-]{0,24}".prop_map(Value::String),
    ];
    leaf.prop_recursive(3, 48, 6, |inner| {
        prop_oneof![
            proptest::collection::vec(inner.clone(), 0..5).prop_map(Value::Array),
            proptest::collection::btree_map("[a-zA-Z][a-zA-Z0-9_]{0,8}", inner, 0..5)
                .prop_map(|values| Value::Object(values.into_iter().collect())),
        ]
    })
}

fn optional_json() -> impl Strategy<Value = Option<Value>> {
    prop_oneof![Just(None), bounded_json().prop_map(Some)]
}

fn extensions() -> impl Strategy<Value = Option<Map<String, Value>>> {
    prop_oneof![
        Just(None),
        proptest::collection::btree_map("[a-zA-Z][a-zA-Z0-9_]{0,8}", bounded_json(), 0..5,)
            .prop_map(|values| Some(values.into_iter().collect())),
    ]
}

fn graphql_path() -> impl Strategy<Value = Vec<GraphQlPathSegment>> {
    proptest::collection::vec(
        prop_oneof![
            "[a-zA-Z][a-zA-Z0-9_]{0,12}".prop_map(GraphQlPathSegment::Field),
            (0_u64..32).prop_map(GraphQlPathSegment::Index),
        ],
        0..8,
    )
}

fn graphql_error() -> impl Strategy<Value = GraphQlError> {
    (
        "[a-zA-Z0-9 _-]{0,32}",
        proptest::collection::vec((1_u32..200, 1_u32..200), 0..5),
        graphql_path(),
        extensions(),
    )
        .prop_map(|(message, locations, path, extensions)| {
            let mut error = GraphQlError::new(message)
                .with_locations(
                    locations
                        .into_iter()
                        .map(|(line, column)| GraphQlLocation::new(line, column))
                        .collect(),
                )
                .with_path(path);
            if let Some(extensions) = extensions {
                error = error.with_extensions(extensions);
            }
            error
        })
}

fn non_null_json() -> impl Strategy<Value = Value> {
    bounded_json().prop_filter("ResponseData::Value excludes explicit null", |value| {
        !value.is_null()
    })
}

pub(crate) fn raw_exchange() -> impl Strategy<Value = (RawRequest, RawResponse)> {
    (
        "[a-zA-Z0-9_ {}():!$]{0,80}",
        optional_json(),
        prop::option::of("[a-zA-Z_][a-zA-Z0-9_]{0,16}"),
        prop_oneof![
            Just(ResponseData::Absent),
            Just(ResponseData::Null),
            non_null_json().prop_map(ResponseData::Value),
        ],
        proptest::collection::vec(graphql_error(), 0..6),
        extensions(),
    )
        .prop_map(|(query, variables, operation, data, errors, extensions)| {
            let mut request = RawRequest::new(query);
            if let Some(variables) = variables {
                request = request.with_variables(variables);
            }
            if let Some(operation) = operation {
                request = request.with_operation_name(operation);
            }
            let mut response = RawResponse::new(data).with_errors(errors);
            if let Some(extensions) = extensions {
                response = response.with_extensions(extensions);
            }
            (request, response)
        })
}

pub(crate) fn malformed_response_wire() -> impl Strategy<Value = Value> {
    prop_oneof![
        Just(json!([])),
        Just(json!({"errors": {}})),
        Just(json!({"extensions": []})),
        Just(json!({"errors": [null]})),
        Just(json!({"errors": [{"path": []}]})),
        Just(json!({"errors": [{"message": "bad", "locations": {}}]})),
        Just(json!({"errors": [{"message": "bad", "locations": [{"line": "one", "column": 1}]}]})),
        Just(json!({"errors": [{"message": "bad", "path": {}}]})),
        (-100_i64..0).prop_map(|index| json!({"errors": [{"message": "bad", "path": [index]}]}),),
        Just(json!({"errors": [{"message": "bad", "path": [false]}]})),
        Just(json!({"errors": [{"message": "bad", "extensions": []}]})),
    ]
}

#[derive(Clone, Debug)]
pub(crate) struct ConfigCase {
    pub(crate) workdir: Option<String>,
    pub(crate) workspace: Option<String>,
    pub(crate) diagnostic_sink: bool,
    pub(crate) load_modules: Option<bool>,
    pub(crate) version: Option<String>,
    pub(crate) verbosity: Option<u64>,
    pub(crate) runner_host: Option<String>,
    pub(crate) startup_secs: Option<u64>,
    pub(crate) http_secs: Option<u64>,
    pub(crate) execution_secs: Option<u64>,
    pub(crate) explicit_connection: bool,
    pub(crate) mutation: ConfigMutation,
}

#[derive(Clone, Copy, Debug)]
pub(crate) enum ConfigMutation {
    None,
    EmptyWorkdir,
    EmptyWorkspace,
    InvalidVersion,
    InvalidRunnerHost,
    ZeroStartup,
    ZeroHttpConnect,
    ZeroExecution,
    VerbosityOverflow,
}

pub(crate) fn config_case() -> impl Strategy<Value = ConfigCase> {
    (
        prop::option::of("[a-zA-Z0-9_./-]{1,24}"),
        prop::option::of("[a-zA-Z0-9_./:#-]{1,24}"),
        any::<bool>(),
        prop::option::of(any::<bool>()),
        prop::option::of((0_u16..20, 0_u16..20, 0_u16..20)),
        prop::option::of(0_u64..=u8::MAX as u64),
        prop::option::of((1_u16..5000).prop_map(|port| format!("tcp://runner.test:{port}"))),
        prop::option::of(1_u64..600),
        prop::option::of(1_u64..120),
        prop::option::of(1_u64..120),
        any::<bool>(),
        0_u8..9,
    )
        .prop_map(
            |(
                workdir,
                workspace,
                diagnostic_sink,
                load_modules,
                version,
                verbosity,
                runner_host,
                startup_secs,
                http_secs,
                execution_secs,
                explicit_connection,
                mutation,
            )| ConfigCase {
                workdir,
                workspace,
                diagnostic_sink,
                load_modules,
                version: version.map(|(major, minor, patch)| format!("v{major}.{minor}.{patch}")),
                verbosity,
                runner_host,
                startup_secs,
                http_secs,
                execution_secs,
                explicit_connection,
                mutation: match mutation {
                    0 => ConfigMutation::None,
                    1 => ConfigMutation::EmptyWorkdir,
                    2 => ConfigMutation::EmptyWorkspace,
                    3 => ConfigMutation::InvalidVersion,
                    4 => ConfigMutation::InvalidRunnerHost,
                    5 => ConfigMutation::ZeroStartup,
                    6 => ConfigMutation::ZeroHttpConnect,
                    7 => ConfigMutation::ZeroExecution,
                    _ => ConfigMutation::VerbosityOverflow,
                },
            },
        )
}

#[derive(Clone, Copy, Debug)]
pub(crate) enum EnvironmentMutation {
    None,
    EmptyKey,
    EqualsKey,
    NulKey,
    NulValue,
    Duplicate,
    Reserved { index: usize, case_mask: u64 },
    NonAscii,
}

#[derive(Clone, Debug)]
pub(crate) struct EnvironmentCase {
    pub(crate) entries: Vec<(String, String)>,
    pub(crate) mutation: EnvironmentMutation,
    pub(crate) marker: String,
}

pub(crate) fn environment_case() -> impl Strategy<Value = EnvironmentCase> {
    (
        proptest::collection::vec("[A-Za-z0-9]{0,8}", 0..8),
        "SECRET_[A-Z0-9]{12}",
        0_u8..8,
        0_usize..7,
        any::<u64>(),
    )
        .prop_map(|(suffixes, marker, mutation, reserved, case_mask)| {
            let entries = suffixes
                .into_iter()
                .enumerate()
                .map(|(index, suffix)| {
                    (format!("KEY_{index}_{suffix}"), format!("{marker}_{index}"))
                })
                .collect();
            EnvironmentCase {
                entries,
                marker,
                mutation: match mutation {
                    0 => EnvironmentMutation::None,
                    1 => EnvironmentMutation::EmptyKey,
                    2 => EnvironmentMutation::EqualsKey,
                    3 => EnvironmentMutation::NulKey,
                    4 => EnvironmentMutation::NulValue,
                    5 => EnvironmentMutation::Duplicate,
                    6 => EnvironmentMutation::Reserved {
                        index: reserved,
                        case_mask,
                    },
                    _ => EnvironmentMutation::NonAscii,
                },
            }
        })
}
