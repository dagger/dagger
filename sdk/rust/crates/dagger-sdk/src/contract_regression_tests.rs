//! Fixed examples which make the stable contract's edge coordinates reviewable.

use std::time::Duration;

use crate::{
    ClientConfig, ConfigError, ConfigOption, GraphQlError, GraphQlLocation, GraphQlPathSegment,
    RawResponse, ResponseData, TimeoutPhase,
};

#[test]
fn timeout_defaults_and_every_public_config_error_coordinate_are_stable() {
    let defaults = ClientConfig::default();
    assert_eq!(defaults.session_startup_timeout(), Duration::from_secs(300));
    assert_eq!(defaults.http_connect_timeout(), Duration::from_secs(10));
    assert_eq!(defaults.graphql_execution_timeout(), None);

    let errors = [
        ConfigError::InvalidWorkdir,
        ConfigError::InvalidWorkspace,
        ConfigError::InvalidVersion,
        ConfigError::InvalidRunnerHost,
        ConfigError::InvalidTimeout {
            phase: TimeoutPhase::SessionStartup,
        },
        ConfigError::InvalidTimeout {
            phase: TimeoutPhase::HttpConnect,
        },
        ConfigError::InvalidTimeout {
            phase: TimeoutPhase::GraphQlExecution,
        },
        ConfigError::VerbosityOutOfRange,
        ConfigError::InvalidEnvironmentKey { index: 1 },
        ConfigError::DuplicateEnvironmentKey {
            first: 0,
            duplicate: 1,
        },
        ConfigError::ReservedEnvironmentKey { index: 0 },
        ConfigError::InvalidEnvironmentValue { index: 0 },
        ConfigError::ExplicitConnectionConflict {
            option: ConfigOption::Workdir,
        },
        ConfigError::ExistingSessionConflict {
            option: ConfigOption::Workspace,
        },
        ConfigError::OptionConflict {
            option: ConfigOption::RunnerHost,
        },
        ConfigError::LegacyOptionRemoved,
    ];
    let rendered = errors.map(|error| error.to_string());
    assert!(rendered.iter().all(|message| !message.is_empty()));
    assert_eq!(rendered.len(), 16);
}

#[test]
fn raw_response_presence_partial_data_and_typed_paths_are_distinct() {
    assert_eq!(
        RawResponse::decode_wire(br"{}")
            .expect("missing data response")
            .data(),
        &ResponseData::Absent
    );
    assert_eq!(
        RawResponse::decode_wire(br#"{"data":null}"#)
            .expect("null data response")
            .data(),
        &ResponseData::Null
    );

    let response = RawResponse::new(ResponseData::Value(serde_json::json!({
        "container": {"ports": [null]}
    })))
    .with_errors(vec![
        GraphQlError::new("port unavailable")
            .with_locations(vec![GraphQlLocation::new(4, 9)])
            .with_path(vec![
                GraphQlPathSegment::Field("container".into()),
                GraphQlPathSegment::Field("ports".into()),
                GraphQlPathSegment::Index(0),
            ]),
    ]);
    let decoded = RawResponse::decode_wire(
        &response
            .encode_wire()
            .expect("public response fixture encodes"),
    )
    .expect("partial response decodes");
    assert!(matches!(decoded.data(), ResponseData::Value(_)));
    assert_eq!(
        decoded.errors()[0].path(),
        &[
            GraphQlPathSegment::Field("container".into()),
            GraphQlPathSegment::Field("ports".into()),
            GraphQlPathSegment::Index(0),
        ]
    );
    assert_eq!(
        decoded.errors()[0].locations()[0],
        GraphQlLocation::new(4, 9)
    );
}
