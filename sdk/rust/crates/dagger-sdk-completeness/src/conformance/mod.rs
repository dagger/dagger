//! Closed contracts used to establish Go-level Rust SDK conformance.
//!
//! This module is deliberately private to the completeness toolchain. It owns canonical
//! conformance identities, host admission, scope policy, and sign-off diagnostics without adding
//! a dependency or public API to `dagger-sdk` or `dagger-sdk-macros`.

mod applicability;
mod applicability_review;
mod diagnostic;
mod identifiers;
pub mod preflight;

pub use applicability::*;
pub use applicability_review::*;
pub use diagnostic::*;
pub use identifiers::*;
pub use preflight::*;
