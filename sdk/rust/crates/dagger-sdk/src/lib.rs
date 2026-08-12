//! Owned Rust client for Dagger's GraphQL API.
//!
//! [`connect`] and [`connect_with`] return a cloneable [`Client`] backed by one shared
//! session. Implicit connections select an existing Dagger session before considering
//! a configured local CLI or the exact compiled CLI release. A downloaded CLI is
//! checksum-verified before publication, and implicit HTTP is confined to the
//! authenticated IPv4 loopback session created by Dagger.
//!
//! Generated and raw requests use the same lifecycle. Explicit [`Client::close`] is
//! cancellation-safe and repeatable across clones; dropping the final lease starts a
//! non-blocking abort backstop, but applications should close explicitly when shutdown
//! evidence matters.
//!
//! # Implicit connection and query execution
//!
//! ```no_run
//! # #[cfg(feature = "gen")]
//! # async fn example() -> Result<(), Box<dyn std::error::Error>> {
//! let client = dagger_sdk::connect().await?;
//! let version = client.query().version().await?;
//! let raw = client
//!     .execute(dagger_sdk::RawRequest::new("query { version }"))
//!     .await?;
//! let cloned = client.clone();
//! let task = tokio::spawn(async move { cloned.query().default_platform().await });
//! println!("{version}: {:?}", raw.data());
//! task.await??;
//! client.close().await?;
//! # Ok(())
//! # }
//! ```
//!
//! # Existing sessions
//!
//! Running an application through `dagger run`, for example
//! `dagger run cargo run`, supplies the existing-session port and token. The same
//! stable entry point validates those values and never falls through to CLI discovery
//! when they are malformed.
//!
//! ```no_run
//! # #[cfg(feature = "gen")]
//! # async fn existing_session() -> Result<(), Box<dyn std::error::Error>> {
//! // This process is assumed to have been started by `dagger run`.
//! let client = dagger_sdk::connect().await?;
//! println!("{}", client.query().version().await?);
//! client.close().await?;
//! # Ok(())
//! # }
//! ```
//!
//! # Typed execution failures
//!
//! Generated execution preserves the complete GraphQL response while projecting a
//! conservatively recognized engine `EXEC_ERROR` into [`ExecError`]. Standard output
//! and standard error remain opt-in inspection fields and are omitted from ordinary
//! `Display` and `Debug` output.
//!
//! ```no_run
//! # #[cfg(feature = "gen")]
//! # async fn inspect_exec() -> Result<(), Box<dyn std::error::Error>> {
//! let client = dagger_sdk::connect().await?;
//! let result = client
//!     .query()
//!     .container()
//!     .from("alpine:3.22")
//!     .with_exec(vec!["sh", "-c", "exit 17"])
//!     .stdout()
//!     .await;
//! if let Err(dagger_sdk::QueryError::Exec { error, .. }) = result {
//!     eprintln!("exit={:?}, stderr={:?}", error.exit_code(), error.stderr());
//! }
//! client.close().await?;
//! # Ok(())
//! # }
//! ```
//!
//! # Diagnostics
//!
//! A diagnostic sink receives ordered, already-sanitized CLI progress. The callback
//! must return promptly; failure disables that sink without failing the connection.
//!
//! ```no_run
//! use std::sync::Arc;
//! use dagger_sdk::{Diagnostic, DiagnosticSink, DiagnosticSinkError};
//!
//! struct Progress;
//!
//! impl DiagnosticSink for Progress {
//!     fn emit(&self, diagnostic: Diagnostic<'_>) -> Result<(), DiagnosticSinkError> {
//!         eprintln!("{:?}: {} bytes", diagnostic.stream, diagnostic.payload.len());
//!         Ok(())
//!     }
//! }
//!
//! # async fn diagnostics() -> Result<(), Box<dyn std::error::Error>> {
//! let config = dagger_sdk::ClientConfig::builder()
//!     .diagnostic_sink(Arc::new(Progress))
//!     .build()?;
//! let client = dagger_sdk::connect_with(config).await?;
//! client.close().await?;
//! # Ok(())
//! # }
//! ```
//!
//! # Trace propagation
//!
//! When the application installs a `tracing` subscriber with an OpenTelemetry layer,
//! the active span context takes precedence over inherited `TRACEPARENT`, `TRACESTATE`,
//! and `BAGGAGE` values. The SDK propagates that context to a new CLI and to every
//! implicit GraphQL request without changing the process-global propagator.
//!
//! ```no_run
//! use tracing::Instrument as _;
//!
//! # #[cfg(feature = "gen")]
//! # async fn traced() -> Result<(), Box<dyn std::error::Error>> {
//! let operation = async {
//!     let client = dagger_sdk::connect().await?;
//!     let version = client.query().version().await?;
//!     client.close().await?;
//!     Ok::<_, Box<dyn std::error::Error>>(version)
//! };
//! let version = operation.instrument(tracing::info_span!("dagger.operation")).await?;
//! println!("{version}");
//! # Ok(())
//! # }
//! ```
//!
//! # Compatibility escape hatch
//!
//! Implicit connections normally require exact version and revision evidence. The
//! explicit escape hatch accepts only an otherwise compatible engine whose provenance
//! is unavailable; a known version or revision mismatch is still rejected.
//!
//! ```no_run
//! # async fn compatibility() -> Result<(), Box<dyn std::error::Error>> {
//! let config = dagger_sdk::ClientConfig::builder()
//!     .allow_unverified_compatibility(true)
//!     .build()?;
//! let client = dagger_sdk::connect_with(config).await?;
//! client.close().await?;
//! # Ok(())
//! # }
//! ```

#![warn(missing_docs)]

mod compatibility;
mod config;
mod connection;
mod connector;
mod core;
mod diagnostic;
#[allow(dead_code)]
mod discovery;
mod errors;
mod graphql;
mod id_input;
mod module;
#[allow(dead_code)]
mod target;
mod target_generated;

mod client;
mod lifecycle;
// Plans carry native environment values and owned connection resources. Keeping the
// type private prevents callers from manufacturing a state which bypasses preflight.
#[allow(dead_code)]
mod preflight;
mod query;
mod runtime_errors;
mod scalar;
mod session;
mod transport;

#[allow(dead_code)]
mod archive;
#[allow(dead_code)]
mod launch;
#[allow(dead_code)]
mod propagation;
#[allow(dead_code)]
mod provision;
#[allow(dead_code)]
mod provisioning_control;
#[allow(dead_code)]
mod provisioning_error;
#[allow(dead_code)]
mod session_startup;

pub use config::{ClientConfig, ClientConfigBuilder};
pub use connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
pub use diagnostic::{Diagnostic, DiagnosticSink, DiagnosticSinkError, DiagnosticStream};
pub use errors::{
    CliDiscoveryError, CliDiscoveryErrorKind, CloseError, ConfigError, ConfigOption, ConnectError,
    DiscoveryPathRole, ExistingSessionError, ExistingSessionErrorKind, PlatformError,
    PlatformErrorKind, QueryBuildError, QueryBuildErrorKind, QueryError, RequestEncodingError,
    RequestEncodingErrorKind, RequestError, ResponseDecodingError, ResponseDecodingErrorKind,
    TargetError, TargetErrorKind, TimeoutPhase,
};
pub use graphql::{
    GraphQlError, GraphQlLocation, GraphQlPathSegment, RawRequest, RawResponse, ResponseData,
};
pub use id_input::IdInput;
pub use module::{
    CurrentCall, ModuleCancellation, ModuleError, ModuleErrorBuildError, ModuleErrorDetail,
    TelemetryContext,
};
pub use runtime_errors::{
    CompatibilityError, CompatibilityErrorKind, CompatibilityEvidenceGap, ExecError,
    ProvisioningError, ProvisioningErrorKind, SessionStartupError, SessionStartupErrorKind,
    ShutdownError, ShutdownFailureKind,
};
pub use scalar::{Id, Json, Platform};

pub use dagger_sdk_macros::{enum_type, interface, methods, object, scalar};

/// Version-locked implementation details used by generated module support.
///
/// This namespace is public only because generated code is compiled in a consumer
/// crate. It is not a stable author-facing API and must match the exact macro
/// companion version selected by `dagger-sdk`.
#[doc(hidden)]
pub mod __private {
    pub use crate::id_input::GeneratedIdInputShape;
    pub use crate::module::__private::*;
    pub use serde;
}

#[cfg(feature = "gen")]
mod r#gen;

pub use client::{Client, connect, connect_with};

#[cfg(feature = "gen")]
pub use r#gen::*;

mod id {
    use std::pin::Pin;

    use crate::errors::QueryError;

    /// Converts an ID-like generated value into the unified engine ID.
    pub trait IntoID<T>: Sized + Clone + Sync + Send + 'static {
        /// Resolves the ID, executing a generated lookup when necessary.
        fn into_id(
            self,
        ) -> Pin<Box<dyn core::future::Future<Output = Result<T, QueryError>> + Send>>;
    }
}

pub use id::IntoID;
pub use query::QueryBuilder;

mod loadable {
    /// A generated object or interface which can be loaded through `node(id:)`.
    ///
    /// This trait is sealed: callers can use it as a generic bound but cannot provide
    /// construction logic capable of attaching a value to an unrelated session.
    #[allow(private_bounds)]
    pub trait Loadable: private::Sealed {}

    impl<T> Loadable for T where T: private::Sealed {}

    pub(crate) mod private {
        use crate::lifecycle::SessionHandle;
        use crate::query::Selection;

        #[cfg_attr(not(feature = "gen"), allow(dead_code))]
        pub(crate) trait Sealed: Sized {
            fn graphql_type() -> &'static str;

            fn from_query(session: SessionHandle, selection: Selection) -> Self;
        }
    }
}

pub use loadable::Loadable;

#[cfg(all(test, feature = "gen"))]
mod tests {
    use crate::{ContainerFromOpts, ContainerPublishOpts, RegistryProtocol};

    #[test]
    fn registry_protocol_options_use_owned_enum_values() {
        let from_opts = ContainerFromOpts {
            insecure_skip_tls_verify: Some(true),
            protocol: Some(RegistryProtocol::Https),
            registry_service: None,
        };
        let publish_opts = ContainerPublishOpts {
            forced_compression: None,
            insecure_skip_tls_verify: Some(false),
            media_types: None,
            platform_variants: None,
            protocol: Some(RegistryProtocol::Http),
            registry_service: None,
        };

        assert_eq!(from_opts.protocol, Some(RegistryProtocol::Https));
        assert_eq!(publish_opts.protocol, Some(RegistryProtocol::Http));
    }
}

#[cfg(test)]
mod foundation_tests;

#[cfg(test)]
mod lifecycle_tests;

#[cfg(all(test, feature = "gen"))]
mod query_tests;

#[cfg(all(test, feature = "gen"))]
mod public_api_tests;

#[cfg(test)]
mod source_foundation_tests;

#[cfg(test)]
mod connector_tests;

#[cfg(all(test, feature = "gen"))]
mod core_codegen_runtime_tests;

#[cfg(test)]
mod contract_regression_tests;

#[cfg(test)]
mod preflight_tests;

#[cfg(test)]
mod provisioning_tests;

#[cfg(test)]
mod launch_tests;

#[cfg(test)]
mod session_startup_tests;

#[cfg(test)]
mod test_support;

#[cfg(test)]
mod transport_foundation_tests;

#[cfg(all(test, feature = "gen"))]
mod runtime_connector_tests;
