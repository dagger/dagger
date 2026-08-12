//! Pure standalone-client schema compilation, naming, projection, and rendering.
//!
//! The compiler consumes one already validated visible-schema plan and returns an
//! immutable Core-plus-module binding plan. It has no filesystem, process, network,
//! Cargo, engine-session, or publication authority.

mod compiler;
mod model;
mod naming;
mod render;

pub use compiler::{ClientCompilationInput, compile_client};
pub use model::{
    CargoPackageName, ClientBindingCatalog, ClientBindingDescriptor, ClientBindingPlan,
    ClientBindingSource, ClientNameKey, ClientNamePlan, ClientNameRole, ClientNamespaceRecord,
    ClientProjectIdentity, ClientSchemaSurface, CoreBindingReference, ModuleRoot,
    ModuleSurfacePlan, RustIdentifier,
};
pub use render::{RenderedClient, render_client, render_client_at};
