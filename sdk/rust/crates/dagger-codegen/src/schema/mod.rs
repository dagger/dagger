//! Schema representations owned by the generator.
//!
//! Raw introspection input is kept separate from the canonical schema that later
//! validation stages will construct. This prevents transport concerns from leaking
//! into projection and rendering.

pub mod canonical;
mod compatibility;
pub mod defaults;
pub mod raw;
mod validate;

pub use compatibility::{CoreCoordinateManifest, SchemaCompatibilityMode};
pub use validate::{decode_and_validate, decode_and_validate_with_mode};
