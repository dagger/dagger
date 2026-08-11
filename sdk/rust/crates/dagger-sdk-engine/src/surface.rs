//! Closed model of engine hook discovery for the module-backed Rust adapter.
//!
//! Hook presence is derived from callable function names. A placeholder, similarly
//! named helper, or absent method must not advertise an engine capability.

use std::collections::BTreeSet;

/// Engine interfaces the built-in Rust adapter may truthfully expose.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub enum AdapterSurface {
    /// Module source initialization.
    ModuleInitializer,
    /// Module binding generation.
    CodeGenerator,
    /// Standalone client generation hook.
    ClientGenerator,
    /// Runtime container construction.
    Runtime,
    /// Module-backed SDK identity.
    AsModule,
}

/// Detects only exact callable adapter methods plus the inherent module identity.
pub fn detect_adapter_surfaces<'a>(
    callable_functions: impl IntoIterator<Item = &'a str>,
) -> BTreeSet<AdapterSurface> {
    let names = callable_functions.into_iter().collect::<BTreeSet<_>>();
    let mut surfaces = BTreeSet::from([AdapterSurface::AsModule]);
    for (name, surface) in [
        ("initModule", AdapterSurface::ModuleInitializer),
        ("codegen", AdapterSurface::CodeGenerator),
        ("generateClient", AdapterSurface::ClientGenerator),
        ("moduleRuntime", AdapterSurface::Runtime),
    ] {
        if names.contains(name) {
            surfaces.insert(surface);
        }
    }
    surfaces
}
