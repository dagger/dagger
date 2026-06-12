pub mod core;
pub mod errors;

pub mod logging;
mod querybuilder;

pub use crate::core::config::Config;

#[cfg(feature = "gen")]
#[allow(dead_code)]
mod client;

#[cfg(feature = "gen")]
#[allow(dead_code)]
mod gen;

#[cfg(feature = "gen")]
pub use client::*;

#[cfg(feature = "gen")]
pub use gen::*;

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
