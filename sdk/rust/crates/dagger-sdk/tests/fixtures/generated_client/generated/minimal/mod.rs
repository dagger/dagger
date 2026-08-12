//! Typed bindings for GraphQL module root `minimal`.
// @generated {"format":"dagger-rust-standalone-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:9ee1f1eeccf3db6eacb6690f6c097fe6ff27d366c4598025d7df1ddc99134787","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[path = "client.rs"]
mod client;
pub use client::*;
#[path = "config.rs"]
mod config;
pub use config::*;
#[path = "item.rs"]
mod item;
pub use item::*;
#[path = "minimal_client.rs"]
mod minimal_client;
pub use minimal_client::*;
#[path = "node.rs"]
mod node;
pub use node::*;
#[path = "state.rs"]
mod state;
pub use state::*;
#[path = "token.rs"]
mod token;
pub use token::*;
