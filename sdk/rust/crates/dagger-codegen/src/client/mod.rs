//! Pure standalone-client semantic models.
//!
//! This module owns the client-specific values passed between schema projection,
//! naming, and rendering. They deliberately contain no filesystem handles, Cargo
//! processes, engine objects, sessions, or publication authority. Later compiler
//! phases may enrich these values only after validating the complete client-visible
//! schema against the exact Core manifest.

mod model;

pub use model::{
    CargoPackageName, ClientNamespaceRecord, ClientProjectIdentity, ClientSchemaSurface,
    ModuleRoot, ModuleSurfacePlan, RustIdentifier,
};
