//! Public Rust SDK surface and the private adapters which currently back it.
//!
//! The 1.0 client foundation keeps configuration, raw GraphQL values, diagnostic
//! delivery, injected connections, and public failures in separate modules. The
//! existing beta connection implementation remains available during the staged
//! migration, but new code should depend on the intentional re-exports below.

pub mod config;
pub mod connection;
pub mod core;
pub mod diagnostic;
pub mod errors;
pub mod graphql;

pub mod logging;
mod querybuilder;

pub use crate::core::config::Config;
pub use config::{ClientConfig, ClientConfigBuilder};
pub use connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
pub use diagnostic::{Diagnostic, DiagnosticSink, DiagnosticSinkError, DiagnosticStream};
pub use errors::{
    CloseError, ConfigError, ConfigOption, ConnectError, QueryBuildError, QueryBuildErrorKind,
    QueryError, RequestEncodingError, RequestEncodingErrorKind, RequestError,
    ResponseDecodingError, ResponseDecodingErrorKind, TimeoutPhase,
};
pub use graphql::{
    GraphQlError, GraphQlLocation, GraphQlPathSegment, RawRequest, RawResponse, ResponseData,
};

#[cfg(feature = "gen")]
#[allow(dead_code)]
mod client;

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

#[cfg(feature = "gen")]
pub use client::*;

#[cfg(feature = "gen")]
pub use r#gen::*;

pub mod id {
    use std::pin::Pin;

    use crate::errors::DaggerError;

    pub trait IntoID<T>: Sized + Clone + Sync + Send + 'static {
        fn into_id(
            self,
        ) -> Pin<Box<dyn core::future::Future<Output = Result<T, DaggerError>> + Send>>;
    }
}

pub use querybuilder::Selection;

pub mod loadable {
    use std::sync::Arc;

    use crate::core::cli_session::DaggerSessionProc;
    use crate::core::graphql_client::DynGraphQLClient;
    use crate::querybuilder::Selection;

    /// Types that can be loaded from an ID via `node(id:)` + inline
    /// fragments. Every generated object and interface client type
    /// with an `id` field implements this.
    pub trait Loadable: Sized {
        /// The GraphQL type name (e.g. `"Container"`).
        fn graphql_type() -> &'static str;

        /// Construct this type from a query selection.
        fn from_query(
            proc: Option<Arc<DaggerSessionProc>>,
            selection: Selection,
            graphql_client: DynGraphQLClient,
        ) -> Self;
    }
}

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
mod test_support;
