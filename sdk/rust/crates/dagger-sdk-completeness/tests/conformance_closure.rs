//! Engine-free child-closure compatibility and exact bundle admission checks.

use std::collections::{BTreeMap, BTreeSet};

use dagger_sdk_completeness::*;
use proptest::prelude::*;

fn target() -> TargetDigest {
    TargetDigest::new(Digest::sha256("closure target"))
}

fn subject() -> SubjectIdentity {
    SubjectIdentity::SourceDigest(Digest::sha256("current Rust source"))
}

fn format_for(child: ChildClosure) -> ChildEvidenceFormat {
    match child {
        ChildClosure::Transport => ChildEvidenceFormat::TransportObservationRegistry,
        ChildClosure::ClientLifecycle => ChildEvidenceFormat::ClientEvidenceRegistry,
        ChildClosure::CoreCodegen => ChildEvidenceFormat::CoreCodegenEvidenceRegistry,
        ChildClosure::EngineIntegration => {
            ChildEvidenceFormat::EngineIntegrationImplementationClosure
        }
        ChildClosure::ModuleAuthoring => ChildEvidenceFormat::ModuleAuthoringImplementationClosure,
        ChildClosure::StandaloneClient => ChildEvidenceFormat::ClientGenerationClosure,
    }
}

fn generated_assets() -> BTreeMap<GeneratedAssetDomain, Digest> {
    required_generated_assets()
        .into_iter()
        .map(|domain| (domain, Digest::sha256(format!("{domain:?}"))))
        .collect()
}

fn valid_input() -> ImplementationClosureBundleInput {
    let target_digest = target();
    let subject = subject();
    let assets = generated_assets();
    let reviewed_asset = Digest::sha256("reviewed module asset");
    let children = required_child_closures()
        .into_iter()
        .map(|child| {
            let subject_binding = if child == ChildClosure::ModuleAuthoring {
                ClosureSubjectBinding::ReviewedAsset {
                    asset_digest: reviewed_asset.clone(),
                }
            } else {
                ClosureSubjectBinding::Subject {
                    identity: subject.clone(),
                }
            };
            let child_assets = match child {
                ChildClosure::CoreCodegen => BTreeMap::from([(
                    GeneratedAssetDomain::CoreBindings,
                    assets[&GeneratedAssetDomain::CoreBindings].clone(),
                )]),
                ChildClosure::EngineIntegration => BTreeMap::from([(
                    GeneratedAssetDomain::EnginePackage,
                    assets[&GeneratedAssetDomain::EnginePackage].clone(),
                )]),
                ChildClosure::ModuleAuthoring => BTreeMap::from([(
                    GeneratedAssetDomain::ModuleAssets,
                    assets[&GeneratedAssetDomain::ModuleAssets].clone(),
                )]),
                ChildClosure::StandaloneClient => BTreeMap::from([(
                    GeneratedAssetDomain::StandaloneClientAssets,
                    assets[&GeneratedAssetDomain::StandaloneClientAssets].clone(),
                )]),
                ChildClosure::Transport | ChildClosure::ClientLifecycle => BTreeMap::new(),
            };
            ChildClosureReference {
                child,
                evidence_format: format_for(child),
                target_digest: target_digest.clone(),
                subject_binding,
                closure_digest: Digest::sha256(format!("{child:?} closure")),
                engine_free: true,
                outcome: ClosureOutcome::Passed,
                generated_assets: child_assets,
            }
        })
        .collect();
    ImplementationClosureBundleInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest,
        subject: subject.clone(),
        child_closures: children,
        compatible_assets: vec![AssetCompatibility {
            asset_digest: reviewed_asset,
            compatible_subject: subject,
            compatibility_input_digest: Digest::sha256("reviewed compatibility decision"),
        }],
        generated_assets: assets,
        platform_matrix_digest: Digest::sha256("portable native matrix"),
        rust_security_digest: Digest::sha256("ordinary Rust security closure"),
        plan: expected_closure_plan().into_inner(),
    }
}

#[test]
fn current_child_formats_adapt_without_replaying_their_work() {
    for child in required_child_closures() {
        let evidence = LegacyChildClosureEvidence {
            child,
            evidence_format: format_for(child),
            target_digest: target(),
            subject_binding: ClosureSubjectBinding::Subject {
                identity: subject(),
            },
            closure_digest: Digest::sha256(format!("{child:?} evidence")),
            engine_free: true,
            outcome: ClosureOutcome::Passed,
            generated_assets: BTreeMap::new(),
        };
        assert_eq!(adapt_child_closure(evidence).unwrap().child, child);
    }
}

#[test]
fn historical_engine_backed_signoff_is_not_relabelled_as_local_closure() {
    let historical = LegacyChildClosureEvidence {
        child: ChildClosure::EngineIntegration,
        evidence_format: ChildEvidenceFormat::EngineIntegrationHistoricalSignoff,
        target_digest: target(),
        subject_binding: ClosureSubjectBinding::Subject {
            identity: subject(),
        },
        closure_digest: Digest::sha256("historical exact-engine signoff"),
        engine_free: false,
        outcome: ClosureOutcome::Passed,
        generated_assets: BTreeMap::new(),
    };
    assert!(adapt_child_closure(historical).is_err());
}

fn independent_closure_model(input: &ImplementationClosureBundleInput) -> bool {
    let children = input
        .child_closures
        .iter()
        .map(|child| child.child)
        .collect::<BTreeSet<_>>();
    let decisions = input
        .compatible_assets
        .iter()
        .map(|decision| (&decision.asset_digest, decision))
        .collect::<BTreeMap<_, _>>();
    let referenced_assets = input
        .child_closures
        .iter()
        .filter_map(|child| match &child.subject_binding {
            ClosureSubjectBinding::ReviewedAsset { asset_digest } => Some(asset_digest),
            ClosureSubjectBinding::Subject { .. } => None,
        })
        .collect::<BTreeSet<_>>();
    input.child_closures.len() == children.len()
        && children == required_child_closures()
        && input.child_closures.iter().all(|child| {
            child.target_digest == input.target_digest
                && child.outcome == ClosureOutcome::Passed
                && child.engine_free
                && child.evidence_format == format_for(child.child)
                && match &child.subject_binding {
                    ClosureSubjectBinding::Subject { identity } => identity == &input.subject,
                    ClosureSubjectBinding::ReviewedAsset { asset_digest } => decisions
                        .get(asset_digest)
                        .is_some_and(|decision| decision.compatible_subject == input.subject),
                }
                && child
                    .generated_assets
                    .iter()
                    .all(|(domain, digest)| input.generated_assets.get(domain) == Some(digest))
        })
        && decisions.len() == input.compatible_assets.len()
        && decisions.keys().copied().collect::<BTreeSet<_>>() == referenced_assets
        && input
            .generated_assets
            .keys()
            .copied()
            .collect::<BTreeSet<_>>()
            == required_generated_assets()
        && CanonicalSet::new(input.plan.clone()) == expected_closure_plan()
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn property_06_closure_exact_current_engine_free(
        mutation in 0_u8..12,
        index in any::<usize>(),
    ) {
        let mut input = valid_input();
        match mutation {
            0 => input.child_closures.reverse(),
            1 => {
                let position = index % input.child_closures.len();
                input.child_closures.remove(position);
            }
            2 => {
                let position = index % input.child_closures.len();
                input.child_closures.push(input.child_closures[position].clone());
            }
            3 => {
                let position = index % input.child_closures.len();
                input.child_closures[position].target_digest =
                    TargetDigest::new(Digest::sha256("stale target"));
            }
            4 => {
                let position = index % input.child_closures.len();
                input.child_closures[position].subject_binding = ClosureSubjectBinding::Subject {
                    identity: SubjectIdentity::SourceDigest(Digest::sha256("stale subject")),
                };
            }
            5 => {
                let position = index % input.child_closures.len();
                input.child_closures[position].outcome = ClosureOutcome::Failed;
            }
            6 => {
                let position = index % input.child_closures.len();
                input.child_closures[position].engine_free = false;
            }
            7 => {
                let position = index % input.child_closures.len();
                if let Some(digest) = input.child_closures[position].generated_assets.values_mut().next() {
                    *digest = Digest::sha256("drifted generated asset");
                } else {
                    input.child_closures[position].generated_assets.insert(
                        GeneratedAssetDomain::CoreBindings,
                        Digest::sha256("foreign generated asset"),
                    );
                }
            }
            8 => {
                let domain = required_generated_assets().into_iter().nth(index % 4).unwrap();
                input.generated_assets.remove(&domain);
            }
            9 => input.plan.push(ClosurePlanAction::StartEngine),
            10 => input.compatible_assets.push(AssetCompatibility {
                asset_digest: Digest::sha256("unused reviewed asset"),
                compatible_subject: input.subject.clone(),
                compatibility_input_digest: Digest::sha256("unused compatibility"),
            }),
            11 => {
                let position = index % input.child_closures.len();
                input.child_closures[position].evidence_format =
                    ChildEvidenceFormat::EngineIntegrationHistoricalSignoff;
            }
            _ => unreachable!(),
        }
        let model_accepts = independent_closure_model(&input);
        let result = assemble_implementation_closure_bundle(input);
        prop_assert_eq!(result.is_ok(), model_accepts);
        if let Ok(bundle) = result {
            prop_assert!(bundle.plan.iter().all(|action| matches!(
                action,
                ClosurePlanAction::ConsumeChild(_)
                    | ClosurePlanAction::ConsumeGeneratedAsset(_)
                    | ClosurePlanAction::ConsumePlatformMatrix
                    | ClosurePlanAction::ConsumeRustSecurity
            )));
        }
    }
}
