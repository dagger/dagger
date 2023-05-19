#[cfg(not(feature = "no-gen"))]
mod client;

#[cfg(feature = "gen")]
mod client;

pub mod core;
pub mod errors;

#[cfg(not(feature = "no-gen"))]
mod gen;

#[cfg(feature = "gen")]
mod gen;

pub mod logging;
mod querybuilder;

pub use crate::core::config::Config;

#[cfg(not(feature = "no-gen"))]
pub use client::*;

#[cfg(not(feature = "no-gen"))]
pub use gen::*;

#[cfg(feature = "gen")]
pub use client::*;

#[cfg(feature = "gen")]
pub use gen::*;
