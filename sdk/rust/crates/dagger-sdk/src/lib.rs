pub mod core;
pub mod errors;

pub mod logging;
pub mod querybuilder;

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

#[cfg(feature = "module")]
pub mod module;

// Re-export module essentials at the top level when the module feature is enabled,
// so users can write `use dagger_sdk::*` like Python/TypeScript SDKs.
#[cfg(feature = "module")]
pub use module::{dag, dagger_function, dagger_module, run, DaggerModule, FunctionArg, ModuleFunction, TypeKind};

pub mod id {
    use std::pin::Pin;

    use crate::errors::DaggerError;

    pub trait IntoID<T>: Sized + Clone + Sync + Send + 'static {
        fn into_id(
            self,
        ) -> Pin<Box<dyn core::future::Future<Output = Result<T, DaggerError>> + Send>>;
    }
}
