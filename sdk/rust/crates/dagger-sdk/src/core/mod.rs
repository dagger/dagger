pub const DAGGER_ENGINE_VERSION: &'static str = "0.10.3";

pub mod cli_session;
pub mod config;
pub mod connect_params;
pub mod downloader;
pub mod engine;
pub mod gql_client;
pub mod graphql_client;
pub mod introspection;
pub mod logger;
pub mod schema;
pub mod session;

pub struct Scalar(String);

pub struct Boolean(bool);

pub struct Int(isize);

pub trait Input {}
