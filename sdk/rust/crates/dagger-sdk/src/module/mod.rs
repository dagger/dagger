//! Rust module-authoring values and the version-locked generated bridge boundary.
//!
//! Public application errors are ordinary owned Rust values. The hidden bridge traits
//! expose only typed access needed by generated code; they deliberately contain no
//! JSON, wire-name, transport, session, or dispatch policy.

mod codec;
mod error;
mod wire;

pub use error::{ModuleError, ModuleErrorBuildError, ModuleErrorDetail};

#[doc(hidden)]
pub mod __private {
    pub use super::codec::__private::*;
    pub use super::error::ModuleError;
    pub use super::wire::{CallEnvelope, ModuleJson, ModuleWireName, NamedModuleArgument};
}
