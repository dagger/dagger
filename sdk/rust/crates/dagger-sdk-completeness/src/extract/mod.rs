//! Pure extractors for the independently pinned completeness authorities.
//!
//! Extractors accept exact in-memory bytes. They deliberately have no filesystem or process
//! access, so the validated authority selection remains the only data they can interpret.

pub mod go;
pub mod harness;
pub mod policy;
pub mod schema;
