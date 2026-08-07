//! Public Rust SDK surface and the private adapters which currently back it.
//!
//! The 1.0 client foundation keeps configuration, raw GraphQL values, diagnostic
//! delivery, injected connections, and public failures in separate modules. The
//! existing beta connection implementation remains available during the staged
//! migration, but new code should depend on the intentional re-exports below.
//!
//! An owned client can serve generated and raw requests concurrently. Explicit close
//! is repeatable across clones; dropping the final lease starts non-blocking cleanup.
//!
//! ```no_run
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

#![warn(missing_docs)]

mod config;
mod connection;
mod connector;
mod core;
mod diagnostic;
#[allow(dead_code)]
mod discovery;
mod errors;
mod graphql;
#[allow(dead_code)]
mod target;
mod target_generated;

mod client;
mod lifecycle;
// Keeping the planning module private lets the owned client and concrete connector
// adopt it without making that staging seam part of the stable API.
#[allow(dead_code)]
mod preflight;
mod query;
mod session;

#[allow(dead_code)]
mod archive;
#[allow(dead_code)]
mod provision;
#[allow(dead_code)]
mod provisioning_control;
#[allow(dead_code)]
mod provisioning_error;

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

/// Introspection wire types used by Dagger's Rust code-generation tools.
///
/// This namespace is hidden because application code should consume generated types;
/// it exists so the separately packaged generator does not require concrete client,
/// transport, process, or lifecycle internals.
#[doc(hidden)]
#[allow(missing_docs)]
pub mod introspection {
    pub use crate::core::introspection::*;
}

#[cfg(feature = "gen")]
#[allow(dead_code)]
// Schema descriptions are external input and can contain text that rustdoc
// interprets as links, HTML, or bare URLs.
#[allow(
    rustdoc::bare_urls,
    rustdoc::broken_intra_doc_links,
    rustdoc::invalid_html_tags
)]
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

#[cfg(test)]
mod query_tests;

#[cfg(test)]
mod public_api_tests;

#[cfg(test)]
mod source_foundation_tests;

#[cfg(test)]
mod connector_tests;

#[cfg(test)]
mod contract_regression_tests;

#[cfg(test)]
mod preflight_tests;

#[cfg(test)]
mod provisioning_tests;

#[cfg(test)]
mod test_support;

#[cfg(test)]
mod transport_foundation_tests;
