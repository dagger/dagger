//! Pure generated-asset change-domain selection.
//!
//! The planner consults only compatible ownership manifests and observed content
//! digests. It never scans or adopts unknown paths. Target or generator identity
//! changes invalidate the complete module tree; narrower changes select only their
//! owners, byte-changing consumers, and missing or stale owned outputs.

use std::collections::{BTreeMap, BTreeSet};

use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::model::{GeneratedAssetPath, GeneratedModuleAssets, RegenerationClass, Sha256Digest};

/// Deterministic repair set for one generated-module candidate.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct RegenerationPlan {
    /// Input domains which changed relative to the prior compatible manifest.
    pub changed_domains: BTreeSet<RegenerationClass>,
    /// Candidate paths requiring publication or repair.
    pub selected: BTreeSet<GeneratedAssetPath>,
    /// Prior-owned obsolete paths authorized for removal.
    pub removed: BTreeSet<GeneratedAssetPath>,
}

/// Pure scoped-regeneration planner.
pub struct RegenerationPlanner;

impl RegenerationPlanner {
    /// Selects changed, missing, stale, and obsolete owned assets without touching Core.
    pub fn plan(
        previous: Option<&GeneratedModuleAssets>,
        candidate: &GeneratedModuleAssets,
        observed: &BTreeMap<GeneratedAssetPath, Sha256Digest>,
    ) -> Result<RegenerationPlan, ModuleDiagnosticSet> {
        super::render::validate_manifest(candidate).map_err(|_| singleton(stale()))?;

        let Some(previous) = previous else {
            return Ok(RegenerationPlan {
                changed_domains: BTreeSet::from([
                    RegenerationClass::Authoring,
                    RegenerationClass::VisibleSchema,
                    RegenerationClass::Target,
                    RegenerationClass::Generator,
                ]),
                selected: candidate.assets.keys().cloned().collect(),
                removed: BTreeSet::new(),
            });
        };
        if previous.format_version != candidate.format_version
            || previous.manifest_path != candidate.manifest_path
        {
            return Err(singleton(stale()));
        }
        super::render::validate_manifest(previous).map_err(|_| singleton(stale()))?;

        let mut plan = RegenerationPlan::default();
        if previous.target != candidate.target {
            plan.changed_domains.insert(RegenerationClass::Target);
        }
        for (path, asset) in &candidate.assets {
            let Some(prior) = previous.assets.get(path) else {
                plan.changed_domains.insert(asset.regeneration);
                plan.selected.insert(path.clone());
                continue;
            };
            if prior.regeneration != asset.regeneration || prior.input_digest != asset.input_digest
            {
                plan.changed_domains.insert(asset.regeneration);
                plan.selected.insert(path.clone());
            }
            if prior.digest != asset.digest {
                // Consumers may change bytes even when their primary regeneration
                // class is unchanged, so content identity is the final selector.
                plan.selected.insert(path.clone());
            }
            if observed.get(path) != Some(&asset.digest) {
                plan.selected.insert(path.clone());
            }
        }
        plan.removed.extend(
            previous
                .assets
                .keys()
                .filter(|path| !candidate.assets.contains_key(*path))
                .cloned(),
        );

        let generator_changed = plan.changed_domains.contains(&RegenerationClass::Generator);
        if generator_changed || plan.changed_domains.contains(&RegenerationClass::Target) {
            plan.selected = candidate.assets.keys().cloned().collect();
        }
        Ok(plan)
    }
}

fn stale() -> ModuleDiagnostic {
    ModuleDiagnostic::new(
        ModuleDiagnosticCode::GeneratedAssetsStale,
        None,
        "generated asset ownership metadata is incompatible or internally inconsistent",
        "regenerate the complete module tree with the current generator format",
    )
    .expect("static regeneration diagnostics satisfy the safe renderer policy")
}

fn singleton(diagnostic: ModuleDiagnostic) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new([diagnostic])
        .expect("a singleton regeneration diagnostic set is non-empty")
}
