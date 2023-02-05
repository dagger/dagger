pub mod cli_session;
pub mod config;
pub mod connect_params;
pub mod downloader;
pub mod engine;
pub mod introspection;
pub mod schema;
pub mod session;

pub struct Scalar(String);

pub struct Boolean(bool);

pub struct Int(isize);

pub trait Input {}
