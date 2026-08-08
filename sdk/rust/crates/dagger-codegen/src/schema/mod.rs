//! Schema representations owned by the generator.
//!
//! Raw introspection input is kept separate from the canonical schema that later
//! validation stages will construct. This prevents transport concerns from leaking
//! into projection and rendering.

pub mod canonical;
pub mod raw;
