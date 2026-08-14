//! Closed contracts used to establish Go-level Rust SDK conformance.
//!
//! This module is deliberately private to the completeness toolchain. It owns canonical
//! conformance identities, host admission, scope policy, and sign-off diagnostics without adding
//! a dependency or public API to `dagger-sdk` or `dagger-sdk-macros`.

mod admission;
mod applicability;
mod applicability_review;
mod artifact;
mod assertion;
mod case_catalog;
mod closure;
mod diagnostic;
mod execution;
mod facade;
mod identifiers;
mod observable;
mod planning;
mod platform;
pub mod preflight;
mod program_registry;
mod scenario;
mod security;
mod verdict;

pub use admission::*;
pub use applicability::*;
pub use applicability_review::*;
pub use artifact::*;
pub use assertion::*;
pub use case_catalog::*;
pub use closure::*;
pub use diagnostic::*;
pub use execution::*;
pub use facade::*;
pub use identifiers::*;
pub use observable::*;
pub use planning::*;
pub use platform::*;
pub use preflight::*;
pub use program_registry::*;
pub use scenario::*;
pub use security::*;
pub use verdict::*;
