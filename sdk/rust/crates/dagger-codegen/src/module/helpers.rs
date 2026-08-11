//! Exhaustive target-bound ownership of the definitive module helper surface.
//!
//! The Go SDK revision pinned by the completeness ledger defines 36 helpers in
//! `dag/dag.gen.go`. Their behavior is authoritative, but its process-global client
//! is not: generated Rust assigns query operations to the active module root, exposes
//! call-local facts through `ModuleContext`, and leaves close to the entrypoint.

use std::collections::BTreeMap;
use std::error::Error;
use std::fmt;

/// Rust ownership assigned to one definitive helper capability.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum HelperOwner {
    /// Operation reached through the active-session generated query root.
    ModuleQuery { rust_method: &'static str },
    /// Operation represented by call-local context rather than a global query.
    ScopedContext { rust_method: &'static str },
    /// Session close owned exactly once by the generated entrypoint.
    EntrypointClose,
    /// Target-bound incompatibility with an explicit reviewed reason.
    RustInapplicable { rationale: &'static str },
}

/// One fixed helper capability and its Rust ownership.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct HelperMapping {
    /// Definitive helper name at the pinned Go SDK revision.
    pub helper: &'static str,
    /// Exact Rust ownership for the behavior.
    pub owner: HelperOwner,
}

/// Failure to prove exact-once ownership of the pinned helper inventory.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum HelperMappingError {
    /// A definitive helper has no assignment.
    Missing,
    /// A non-definitive helper was introduced.
    Added,
    /// One definitive helper has multiple assignments.
    Duplicate,
    /// A helper moved away from its reviewed Rust owner.
    Reassigned,
    /// An inapplicability has no meaningful reviewed rationale.
    UnreviewedInapplicability,
}

impl fmt::Display for HelperMappingError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Missing => "a definitive module helper mapping is missing",
            Self::Added => "an unknown module helper mapping was added",
            Self::Duplicate => "a definitive module helper was mapped more than once",
            Self::Reassigned => "a definitive module helper changed reviewed ownership",
            Self::UnreviewedInapplicability => {
                "a Rust-inapplicable module helper lacks reviewed rationale"
            }
        })
    }
}

impl Error for HelperMappingError {}

const DEFINITIVE_HELPERS: [HelperMapping; 36] = [
    query("Address", "address"),
    query("CacheVolume", "cache_volume"),
    query("Changeset", "changeset"),
    HelperMapping {
        helper: "Close",
        owner: HelperOwner::EntrypointClose,
    },
    query("Cloud", "cloud"),
    query("Container", "container"),
    context("CurrentFunctionCall", "current_function_call"),
    context("CurrentModule", "current_module"),
    context("CurrentNode", "current_node"),
    query("CurrentTypeDefs", "current_type_defs"),
    query("CurrentWorkspace", "current_workspace"),
    query("DefaultPlatform", "default_platform"),
    query("Directory", "directory"),
    query("Engine", "engine"),
    query("EngineVolume", "engine_volume"),
    query("EnvFile", "env_file"),
    query("Error", "error"),
    query("File", "file"),
    query("Function", "function"),
    query("GeneratedCode", "generated_code"),
    query("Git", "git"),
    query("Host", "host"),
    query("HTTP", "http"),
    query("ID", "id"),
    query("JSON", "json"),
    query("LLM", "llm"),
    query("Module", "module"),
    query("ModuleSource", "module_source"),
    query("Node", "node"),
    query("Schema", "schema"),
    query("Secret", "secret"),
    query("SetSecret", "set_secret"),
    query("SourceMap", "source_map"),
    query("SshfsVolume", "sshfs_volume"),
    query("TypeDef", "type_def"),
    query("Version", "version"),
];

const fn query(helper: &'static str, rust_method: &'static str) -> HelperMapping {
    HelperMapping {
        helper,
        owner: HelperOwner::ModuleQuery { rust_method },
    }
}

const fn context(helper: &'static str, rust_method: &'static str) -> HelperMapping {
    HelperMapping {
        helper,
        owner: HelperOwner::ScopedContext { rust_method },
    }
}

/// Returns the exact pinned helper inventory and reviewed ownership mapping.
#[must_use]
pub const fn definitive_helper_mapping() -> &'static [HelperMapping; 36] {
    &DEFINITIVE_HELPERS
}

/// Accepts only the complete, exact-once reviewed helper assignment.
pub fn validate_definitive_helper_mapping(
    candidate: &[HelperMapping],
) -> Result<(), HelperMappingError> {
    let definitive = DEFINITIVE_HELPERS
        .iter()
        .map(|mapping| (mapping.helper, mapping.owner))
        .collect::<BTreeMap<_, _>>();
    let mut observed = BTreeMap::new();
    for mapping in candidate {
        if matches!(
            mapping.owner,
            HelperOwner::RustInapplicable { rationale } if rationale.trim().is_empty()
        ) {
            return Err(HelperMappingError::UnreviewedInapplicability);
        }
        let Some(expected) = definitive.get(mapping.helper) else {
            return Err(HelperMappingError::Added);
        };
        if observed.insert(mapping.helper, mapping.owner).is_some() {
            return Err(HelperMappingError::Duplicate);
        }
        if &mapping.owner != expected {
            return Err(HelperMappingError::Reassigned);
        }
    }
    if observed.len() != DEFINITIVE_HELPERS.len() {
        return Err(HelperMappingError::Missing);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::{
        HelperMapping, HelperOwner, definitive_helper_mapping, validate_definitive_helper_mapping,
    };

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(256))]

        #[test]
        fn property_20_definitive_helper_capabilities_exhaustively_mapped(
            index in any::<usize>(),
            mutation in 0_u8..4,
        ) {
            let exact = definitive_helper_mapping();
            prop_assert!(validate_definitive_helper_mapping(exact).is_ok());

            let selected = index % exact.len();
            let mut changed = exact.to_vec();
            match mutation {
                0 => { changed.remove(selected); }
                1 => changed.push(exact[selected]),
                2 => {
                    changed[selected].owner = if matches!(
                        changed[selected].owner,
                        HelperOwner::EntrypointClose
                    ) {
                        HelperOwner::ModuleQuery { rust_method: "close" }
                    } else {
                        HelperOwner::EntrypointClose
                    };
                }
                _ => changed.push(HelperMapping {
                    helper: "UnexpectedHelper",
                    owner: HelperOwner::ModuleQuery { rust_method: "unexpected_helper" },
                }),
            }
            prop_assert!(validate_definitive_helper_mapping(&changed).is_err());
        }
    }
}
