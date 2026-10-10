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
            no_lock: None,
            protocol: Some(RegistryProtocol::Https),
            registry_service: None,
            version: None,
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

    #[tokio::test]
    async fn input_object_fields_use_schema_names() {
        use crate::{LlmMessageOriginInput, LlmMessageOriginKind};

        let origin = LlmMessageOriginInput {
            agent_name: "bot".to_string(),
            kind: LlmMessageOriginKind::Agent,
            r#ref: "ref-1".to_string(),
            reply_to: "msg-1".to_string(),
        };

        let query = crate::querybuilder::query()
            .select("withOrigin")
            .arg("origin", origin)
            .build()
            .await
            .unwrap();

        assert!(query.contains(r#"agentName:"bot""#), "{query}");
        assert!(query.contains(r#"replyTo:"msg-1""#), "{query}");
        assert!(query.contains(r#"ref:"ref-1""#), "{query}");
        assert!(!query.contains("agent_name"), "{query}");
        assert!(!query.contains("reply_to"), "{query}");
    }
}
