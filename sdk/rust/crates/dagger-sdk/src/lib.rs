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
