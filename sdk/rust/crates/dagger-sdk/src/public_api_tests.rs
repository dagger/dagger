//! Manifest, redaction, and source audits for the stable crate boundary.

use std::error::Error;
use std::fmt;

use proptest::prelude::*;

use crate::config::ClientConfig;
use crate::connection::{EngineConnectionError, EngineConnectionErrorKind};
use crate::diagnostic::DiagnosticSinkError;
use crate::errors::{
    CliDiscoveryError, CliDiscoveryErrorKind, CloseError, ConfigError, ConnectError,
    DiscoveryPathRole, ExistingSessionError, ExistingSessionErrorKind, PlatformError,
    PlatformErrorKind, QueryBuildError, QueryBuildErrorKind, QueryError, RequestEncodingError,
    RequestEncodingErrorKind, RequestError, ResponseDecodingError, ResponseDecodingErrorKind,
    TargetError, TargetErrorKind,
};
use crate::test_support::proptest_config;

const PUBLIC_API: &str = include_str!("../api/public-api.txt");
const BETA_API: &str = include_str!("../api/beta-public-api.txt");
const MIGRATION: &str = include_str!("../api/beta-migration.json");
const LIB_SOURCE: &str = include_str!("lib.rs");
const CONFIG_SOURCE: &str = include_str!("config.rs");
const GENERATED_SOURCE: &str = include_str!("gen.rs");

fn manifest_lines(input: &str) -> Vec<&str> {
    input
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty() && !line.starts_with('#'))
        .collect()
}

fn assert_send_sync<T: Send + Sync>() {}

fn production_source_is_panic_free() -> bool {
    [
        include_str!("client.rs"),
        include_str!("connector.rs"),
        include_str!("lifecycle.rs"),
        include_str!("query.rs"),
        include_str!("graphql.rs"),
        include_str!("connection.rs"),
        include_str!("diagnostic.rs"),
        include_str!("discovery.rs"),
        include_str!("config.rs"),
        include_str!("errors.rs"),
        include_str!("preflight.rs"),
        include_str!("session.rs"),
        include_str!("target.rs"),
        include_str!("archive.rs"),
        include_str!("provision.rs"),
        include_str!("provisioning_control.rs"),
        include_str!("provisioning_error.rs"),
        include_str!("launch.rs"),
        include_str!("session_startup.rs"),
    ]
    .into_iter()
    .all(|source| {
        // Test modules may use assertion-oriented conveniences; the production prefix
        // must remain total because these paths run during request and shutdown work.
        let production = source.split("#[cfg(test)]").next().unwrap_or(source);
        [".unwrap(", ".expect(", "panic!(", "unsafe {"]
            .into_iter()
            .all(|forbidden| !production.contains(forbidden))
    })
}

#[derive(Debug)]
struct SecretSource(String);

impl fmt::Display for SecretSource {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl Error for SecretSource {}

proptest! {
    #![proptest_config(proptest_config())]

    // Invariant: generated public handles share safe leases without exposing their storage.
    // Feature: rust-sdk-client-lifecycle, Property 4: public handles are safely shareable and encapsulated
    #[test]
    fn public_handles_are_safely_shareable_and_encapsulated(
        sample in 0_usize..6,
        mutation in any::<bool>(),
    ) {
        assert_send_sync::<crate::Client>();
        assert_send_sync::<crate::QueryBuilder>();
        assert_send_sync::<crate::Query>();
        assert_send_sync::<crate::Container>();
        assert_send_sync::<crate::NodeClient>();

        let handles = ["Query", "Container", "Directory", "File", "NodeClient", "ExportableClient"];
        let selected = if mutation { "UnknownHandle" } else { handles[sample] };
        let declaration = format!("pub struct {selected}");
        prop_assert_eq!(GENERATED_SOURCE.contains(&declaration), !mutation);
        prop_assert!(!GENERATED_SOURCE.contains("pub session: SessionHandle"));
        prop_assert!(!GENERATED_SOURCE.contains("pub selection: Selection"));
        prop_assert!(!GENERATED_SOURCE.contains("graphql_client"));
        prop_assert!(!GENERATED_SOURCE.contains("DaggerSessionProc"));
    }

    // Invariant: ordinary cleanup-related rendering never interpolates opaque caller data.
    // Feature: rust-sdk-client-lifecycle, Property 10: implicit-cleanup diagnostics are secret-safe
    #[test]
    fn implicit_cleanup_diagnostics_are_secret_safe(marker in "SECRET_[A-Za-z0-9]{16,48}") {
        let source = SecretSource(marker.clone());
        let connection = EngineConnectionError::with_source(
            EngineConnectionErrorKind::Transport,
            SecretSource(marker.clone()),
        );
        let values = [
            format!("{} {:?}", connection, connection),
            format!("{} {:?}", CloseError::Connection(connection.clone()), CloseError::Connection(connection.clone())),
            format!("{} {:?}", DiagnosticSinkError::with_source(source), DiagnosticSinkError::new()),
            format!("{} {:?}", QueryBuildError::with_source(QueryBuildErrorKind::LazyIdentifier, SecretSource(marker.clone())), QueryBuildError::new(QueryBuildErrorKind::LazyIdentifier)),
            format!("{:?}", ClientConfig::builder().environment("MARKER", marker.clone()).build().expect("valid environment")),
        ];
        prop_assert!(values.iter().all(|rendered| !rendered.contains(&marker)));
    }

    // Invariant: every removed beta configuration coordinate has one checked stable replacement.
    // Feature: rust-sdk-client-lifecycle, Property 13: stable configuration contains no beta unit/path fields
    #[test]
    fn stable_configuration_contains_no_beta_unit_or_path_fields(
        remove_index in 0_usize..10,
        mutate in any::<bool>(),
    ) {
        let old = manifest_lines(BETA_API);
        let document: serde_json::Value = serde_json::from_str(MIGRATION).expect("checked migration JSON");
        let entries = document["entries"].as_array().expect("migration entries");
        let mut mapped = entries
            .iter()
            .filter_map(|entry| entry["old"].as_str())
            .collect::<Vec<_>>();
        if mutate {
            mapped.remove(remove_index);
        }
        prop_assert_eq!(mapped == old, !mutate);
        prop_assert!(!CONFIG_SOURCE.contains("pub config_path"));
        prop_assert!(!CONFIG_SOURCE.contains("pub timeout_ms"));
        prop_assert!(!CONFIG_SOURCE.contains("pub execute_timeout_ms"));
    }

    // Invariant: representative public failures stay in their phase-specific families without unwind.
    // Feature: rust-sdk-client-lifecycle, Property 22: public failure paths are typed and panic-free
    #[test]
    fn public_failure_paths_are_typed_and_panic_free(case in 0_u8..11) {
        let rendered = std::panic::catch_unwind(|| match case {
            0 => ConfigError::InvalidWorkdir.to_string(),
            1 => ConnectError::Config(ConfigError::InvalidWorkdir).to_string(),
            2 => ConnectError::ExistingSession(ExistingSessionError::new(ExistingSessionErrorKind::InvalidPort)).to_string(),
            3 => ConnectError::CliDiscovery(CliDiscoveryError::new(CliDiscoveryErrorKind::Lookup, DiscoveryPathRole::ExplicitLocal)).to_string(),
            4 => ConnectError::Platform(PlatformError::new(PlatformErrorKind::UnsupportedArchitecture)).to_string(),
            5 => ConnectError::Target(TargetError::new(TargetErrorKind::VersionMismatch)).to_string(),
            6 => RequestError::RequestEncoding(RequestEncodingError::new(RequestEncodingErrorKind::Json)).to_string(),
            7 => RequestError::ResponseDecoding(ResponseDecodingError::new(ResponseDecodingErrorKind::InvalidShape)).to_string(),
            8 => QueryError::Build(QueryBuildError::new(QueryBuildErrorKind::InvalidSelection)).to_string(),
            9 => CloseError::Interrupted.to_string(),
            _ => RequestError::ConnectionPanicked.to_string(),
        });
        prop_assert!(rendered.is_ok());
        prop_assert!(production_source_is_panic_free());
    }

    // Invariant: the normalized facade contains each intentional root export and no beta path.
    // Feature: rust-sdk-client-lifecycle, Property 23: the stable surface is documented and intentionally exported
    #[test]
    fn stable_surface_is_documented_and_intentionally_exported(
        index in 0_usize..37,
        mutate in any::<bool>(),
    ) {
        let manifest = manifest_lines(PUBLIC_API);
        let item = if mutate { "Config" } else { manifest[index] };
        let represented = item != "Config" && (item == "generated::*"
            || LIB_SOURCE.contains(&format!("{item},"))
            || LIB_SOURCE.contains(&format!("{{{item},"))
            || LIB_SOURCE.contains(&format!(", {item}"))
            || LIB_SOURCE.contains(&format!("::{item};"))
            || LIB_SOURCE.contains(&format!("pub use {item}"))
            || matches!(item, "Client" | "connect" | "connect_with"));
        prop_assert_eq!(represented, !mutate);
        prop_assert!(!LIB_SOURCE.contains("pub mod core"));
        prop_assert!(!LIB_SOURCE.contains("connect_legacy"));
        prop_assert!(LIB_SOURCE.contains("#![warn(missing_docs)]"));
    }
}

#[test]
fn production_request_and_shutdown_sources_pass_the_audit() {
    assert!(production_source_is_panic_free());
}
