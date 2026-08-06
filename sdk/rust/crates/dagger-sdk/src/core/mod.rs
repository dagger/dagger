pub mod cli_session;
pub mod config;
pub mod connect_params;
pub mod downloader;
pub mod engine;
pub mod gql_client;
pub mod graphql_client;
pub mod introspection;
pub mod logger;
// These beta introspection adapters remain compiled for the later connector migration,
// but they are not part of the stable public facade and currently have no call site.
#[allow(dead_code)]
pub(crate) mod schema;
#[allow(dead_code)]
pub(crate) mod session;

mod version;

pub const DAGGER_ENGINE_VERSION: &str = version::DAGGER_ENGINE_VERSION;
