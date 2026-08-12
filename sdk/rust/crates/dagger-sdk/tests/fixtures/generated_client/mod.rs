//! Standalone Dagger client composed over the shared public Rust SDK runtime.
// @generated {"format":"dagger-rust-standalone-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:9ee1f1eeccf3db6eacb6690f6c097fe6ff27d366c4598025d7df1ddc99134787","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
pub use dagger_sdk as core;
pub use dagger_sdk::{Client, ClientConfig, connect, connect_with};
mod generated;
/// Typed bindings for selected GraphQL module `minimal`.
#[path = "generated/minimal/mod.rs"]
pub mod minimal;
/// Adds the selected GraphQL module root to an existing shared client.
pub trait MinimalExt {
    /// Selects GraphQL root field `minimal` without opening another session.
    fn minimal(&self) -> minimal::Client;
}
impl MinimalExt for dagger_sdk::Client {
    fn minimal(&self) -> minimal::Client {
        minimal::Client::from_query(self.query_builder().select("minimal"))
    }
}
impl MinimalExt for dagger_sdk::QueryBuilder {
    fn minimal(&self) -> minimal::Client {
        minimal::Client::from_query(self.select("minimal"))
    }
}
/// Imports the selected module extension trait for method resolution.
pub mod prelude {
    pub use super::MinimalExt as _;
}
