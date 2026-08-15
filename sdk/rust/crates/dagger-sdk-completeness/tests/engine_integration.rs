//! Engine-integration scope and evidence-separation properties.
//!
//! Tests build valid contracts first and mutate one reviewed boundary at a time. The
//! reference model remains deliberately smaller than production validation so a
//! counterexample identifies the exact scope, owner, status, fingerprint, or evidence
//! separation rule that diverged.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::LazyLock;

use dagger_sdk_completeness::extract::policy::{PolicyClauseSelection, extract_policy_clauses};
use dagger_sdk_completeness::{
    AllowedTerminalStatus, Architecture, AuthorityId, AuthorityRegistry, CanonicalSet,
    CapabilityId, CaseId, CaseObservation, CheckOutcome, CommandSpec, CommitSha, Digest,
    EngineEvidenceDomain, EngineIntegrationManifest, EngineIntegrationMappings,
    EngineIntegrationObservation, EngineMappingDisposition, EvidenceId, EvidenceKind,
    EvidenceReference, EvidenceRegistry, ExecutableId, ExpectedOutcome, FeatureId,
    FeatureScopeDeclaration, FeatureScopePolicy, HarnessMappings, NonEmptyText, OperatingSystem,
    Platform, RepositoryRelativePath, ResolvedLedger, SourceBundle, SourceLocator, Status,
    TargetDigest, apply_engine_integration_statuses, apply_feature_status_changes,
    assemble_engine_integration_manifest, decode_canonical, engine_integration_contract,
    recompute_source_digest, rust_artifact_digest, validate_engine_integration_mappings,
    verify_engine_integration_evidence,
};
use dagger_sdk_engine::{
    EngineEvidenceSubject, FormatVersion, PublishedSdkDependency, TargetIdentity,
};
use proptest::prelude::*;

const AUTHORITIES: &[u8] = include_bytes!("../../../completeness/authorities.json");
const LEDGER: &[u8] = include_bytes!("../../../completeness/artifacts/ledger.json");
const EVIDENCE: &[u8] = include_bytes!("../../../completeness/evidence/registry.json");
const HARNESS_MAPPINGS: &[u8] = include_bytes!("../../../completeness/harness-mappings.json");
const ENGINE_MAPPINGS: &[u8] =
    include_bytes!("../../../completeness/engine-integration-mappings.json");
const REQUIREMENTS: &str =
    include_str!("../../../../../.kiro/specs/rust-sdk-engine-integration/requirements.md");

static ENGINE_LEDGER: LazyLock<ResolvedLedger> =
    LazyLock::new(|| decode_canonical(LEDGER).unwrap());
static ENGINE_MAPPING_INPUT: LazyLock<EngineIntegrationMappings> =
    LazyLock::new(|| decode_canonical(ENGINE_MAPPINGS).unwrap());
static ENGINE_POLICY: LazyLock<FeatureScopePolicy> =
    LazyLock::new(|| engine_integration_contract().scope);
static ENGINE_DECLARATION: LazyLock<FeatureScopeDeclaration> =
    LazyLock::new(|| FeatureScopeDeclaration {
        feature: ENGINE_POLICY.feature.clone(),
        existing_capability_ids: ENGINE_POLICY.existing_capability_ids.clone(),
        existing_scope_digest: ENGINE_POLICY.existing_scope_digest.clone(),
        policy_capability_ids: ENGINE_POLICY.policy_capability_ids.clone(),
    });

#[test]
fn engine_policy_authority_digest_binds_the_approved_requirements() {
    let registry: AuthorityRegistry = decode_canonical(AUTHORITIES).unwrap();
    let source = registry
        .authorities
        .get(&"rust-policy".parse().unwrap())
        .unwrap();
    let observed = recompute_source_digest(source, &rust_policy_bundle()).unwrap();
    assert_eq!(observed, source.source_digest);
}

#[test]
fn engine_policy_clauses_match_the_approved_requirements() {
    let contract = engine_integration_contract();
    let selections = contract
        .policy_clauses
        .iter()
        .map(|clause| PolicyClauseSelection {
            clause_id: clause.clause_id.to_owned(),
            exact_text: clause.exact_text.to_owned(),
        })
        .collect::<Vec<_>>();
    let result = extract_policy_clauses(
        &AuthorityId::new("rust-policy").unwrap(),
        contract.requirements_path,
        REQUIREMENTS,
        &selections,
    );
    if let Err(diagnostics) = result {
        panic!("{:#?}", diagnostics.as_slice());
    }
}

#[test]
fn engine_existing_scope_matches_the_preimplementation_ledger() {
    let policy = engine_integration_contract().scope;
    let ledger: ResolvedLedger = decode_canonical(LEDGER).unwrap();
    let owned = CanonicalSet::new(
        ledger
            .capabilities
            .values()
            .filter(|row| {
                row.owner_feature == Some(FeatureId::Feature5)
                    && !policy.policy_capability_ids.contains(&row.capability_id)
            })
            .map(|row| row.capability_id.clone()),
    );
    assert_eq!(owned, policy.existing_capability_ids);

    for capability_id in policy.policy_capability_ids.iter() {
        let row = ledger.capabilities.get(capability_id).unwrap();
        assert_eq!(row.owner_feature, Some(FeatureId::Feature5));
        assert_eq!(row.status, Status::Missing);
    }

    let lines = policy
        .existing_capability_ids
        .iter()
        .map(ToString::to_string)
        .collect::<Vec<_>>();
    assert_eq!(
        Digest::sha256(serde_json::to_vec(&lines).unwrap()),
        policy.existing_scope_digest
    );
}

#[test]
fn harness_mappings_bind_the_current_rust_artifact() {
    let repository_root = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../..");
    let expected = rust_artifact_digest(&repository_root).unwrap();
    let mappings: HarnessMappings = decode_canonical(HARNESS_MAPPINGS).unwrap();
    for mapping in mappings.checks.values() {
        assert_eq!(mapping.verified_artifact_digest, expected);
    }
}

#[test]
fn engine_mappings_are_canonical_and_bind_the_current_ledger() {
    let mappings = &*ENGINE_MAPPING_INPUT;
    let ledger = &*ENGINE_LEDGER;
    let policy = &*ENGINE_POLICY;
    let target = mappings.target_digest.clone();
    let validated =
        validate_engine_integration_mappings(mappings, ledger, policy, &target).unwrap();

    assert_eq!(validated.mappings().len(), 53);
    assert_eq!(
        CanonicalSet::new(validated.mappings().keys().cloned()),
        policy.capability_ids(),
    );
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // Hook evidence cannot close delegated module-dispatch or standalone-client content.
    #[test]
    fn property_01_exact_capability_scope_evidence_separation(
        mutation in 0_u8..11,
        row_index in 0_usize..53,
    ) {
        let mut mappings = (*ENGINE_MAPPING_INPUT).clone();
        let ledger = &*ENGINE_LEDGER;
        let policy = &*ENGINE_POLICY;
        let checked_target = mappings.target_digest.clone();
        let index = row_index % mappings.mappings.len();

        match mutation {
            0 => {
                mappings.mappings.remove(index);
            }
            1 => {
                mappings.mappings.push(mappings.mappings[index].clone());
            }
            2 => {
                mappings.mappings[index].capability_id =
                    "policy/rust-policy/engine-out-of-scope".parse::<CapabilityId>().unwrap();
            }
            3 => {
                mappings.mappings[index].capability_fingerprint =
                    "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
                        .parse::<Digest>()
                        .unwrap();
            }
            4 => {
                mappings.target_digest = TargetDigest::new(
                    "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
                        .parse::<Digest>()
                        .unwrap(),
                );
            }
            5 => {
                let row = &mut mappings.mappings[index];
                row.current_status = match row.current_status {
                    Status::Partial => Status::Missing,
                    _ => Status::Partial,
                };
            }
            6 => {
                mappings.mappings[index].blocking_owner = FeatureId::Feature6;
            }
            7 => {
                mappings.mappings[index].evidence_domains = CanonicalSet::default();
            }
            8 => {
                let row = &mut mappings.mappings[index];
                row.evidence_domains = CanonicalSet::new([EngineEvidenceDomain::LibraryOperation]);
                row.delegated_content = CanonicalSet::new([
                    dagger_sdk_completeness::DelegatedContentDomain::StandaloneClientContent,
                ]);
            }
            9 => {
                let row = &mut mappings.mappings[index];
                row.disposition = EngineMappingDisposition::Direct;
                row.allowed_terminal_status = AllowedTerminalStatus::IdiomaticEquivalent;
            }
            10 => {
                mappings.mappings.reverse();
            }
            _ => unreachable!(),
        }

        let result = validate_engine_integration_mappings(
            &mappings,
            ledger,
            policy,
            &checked_target,
        );
        if mutation == 10 {
            let validated = result.unwrap();
            prop_assert_eq!(validated.mappings().len(), 53);
            prop_assert_eq!(
                CanonicalSet::new(validated.mappings().keys().cloned()),
                policy.capability_ids(),
            );
            let status_identity = validated.mappings().values().all(|row| {
                    ledger.capabilities.get(&row.capability_id).is_some_and(|ledger_row| {
                    row.current_status == ledger_row.status
                        && row.blocking_owner == FeatureId::Feature5
                })
            });
            prop_assert!(status_identity);
        } else {
            prop_assert!(result.is_err());
        }
    }

    #[test]
    fn property_28_evidence_admission_exact_target_capability_local(
        mutation in 0_u8..8,
        row_index in 0_usize..53,
    ) {
        let manifest = engine_manifest();
        let capability_id = manifest.mappings.keys().nth(row_index % 53).unwrap().clone();
        let domain = manifest.mappings[&capability_id].evidence_domains[0].clone();
        let mut observation = observation(
            &manifest,
            "engine/verification/property-28",
            domain,
            [capability_id.clone()],
        );

        match mutation {
            0 => {}
            1 => observation.subject.target.engine_version = "1.0.0-beta.11".parse().unwrap(),
            2 => {
                let case = manifest.required_cases[0].clone();
                observation.cases.insert(
                    case,
                    CaseObservation::Failed {
                        diagnostic: NonEmptyText::new("runtime-protocol-failed").unwrap(),
                    },
                );
            }
            3 => {
                observation.cases.remove(&manifest.required_cases[0]);
            }
            4 => {
                observation.proved_capabilities = CanonicalSet::new([
                    CapabilityId::new("policy/rust-policy/not-engine-integration").unwrap(),
                ]);
            }
            5 => {
                observation.evidence_domain = EngineEvidenceDomain::ScopePolicy;
                if manifest.mappings[&capability_id]
                    .evidence_domains
                    .contains(&EngineEvidenceDomain::ScopePolicy)
                {
                    observation.evidence_domain = EngineEvidenceDomain::SdkResolution;
                }
            }
            6 => {
                let case = manifest.required_cases[0].clone();
                observation.cases.insert(
                    case,
                    CaseObservation::Skipped {
                        reason: NonEmptyText::new("not-run").unwrap(),
                    },
                );
            }
            7 => observation.format_version = 2,
            _ => unreachable!(),
        }

        let before = manifest.clone();
        let result = verify_engine_integration_evidence(&manifest, &[observation]);
        if mutation == 0 {
            let closure = result.unwrap();
            prop_assert_eq!(
                closure.observed_domains().get(&capability_id).unwrap(),
                &CanonicalSet::new([manifest.mappings[&capability_id].evidence_domains[0].clone()]),
            );
        } else {
            prop_assert!(result.is_err());
        }
        prop_assert_eq!(manifest, before);
    }

    #[test]
    fn property_29_completeness_reports_derived_not_presented(
        row_index in 0_usize..53,
        missing_tail in 0_usize..5,
    ) {
        let manifest = engine_manifest();
        let capability_id = manifest.mappings.keys().nth(row_index % 53).unwrap().clone();
        let required_domains = manifest.mappings[&capability_id].evidence_domains.clone();
        let retained = required_domains.len().saturating_sub(missing_tail % (required_domains.len() + 1));
        let observations = required_domains
            .iter()
            .take(retained)
            .enumerate()
            .map(|(index, domain)| {
                observation(
                    &manifest,
                    &format!("engine/verification/property-29-{index}"),
                    domain.clone(),
                    [capability_id.clone()],
                )
            })
            .collect::<Vec<_>>();
        let closure = verify_engine_integration_evidence(&manifest, &observations).unwrap();
        let mut evidence: EvidenceRegistry = decode_canonical(EVIDENCE).unwrap();
        add_manifest_evidence(&mut evidence, &manifest, &observations);

        let transition = apply_engine_integration_statuses(
            &ENGINE_LEDGER,
            &ENGINE_DECLARATION,
            &ENGINE_POLICY,
            &manifest,
            &observations,
            &evidence,
            &manifest.target_digest,
        ).unwrap();
        let direct = apply_feature_status_changes(
            &ENGINE_LEDGER,
            &ENGINE_DECLARATION,
            &ENGINE_POLICY,
            &transition.candidate,
            &evidence,
            &manifest.target_digest,
            &BTreeMap::new(),
            false,
        ).unwrap();

        prop_assert_eq!(&transition.ledger, &direct);
        prop_assert_eq!(&transition.remaining_domains, closure.missing_domains());
        if retained == required_domains.len() {
            prop_assert!(transition.candidate.changes.contains_key(&capability_id));
            prop_assert!(matches!(
                transition.ledger.capabilities[&capability_id].status,
                Status::Implemented | Status::IdiomaticEquivalent
            ));
        } else {
            prop_assert!(!transition.candidate.changes.contains_key(&capability_id));
            prop_assert_eq!(
                &transition.ledger.capabilities[&capability_id],
                &ENGINE_LEDGER.capabilities[&capability_id],
            );
            prop_assert_eq!(
                transition.remaining_domains[&capability_id].len(),
                required_domains.len() - retained,
            );
        }
    }
}

fn engine_manifest() -> EngineIntegrationManifest {
    let target = ENGINE_MAPPING_INPUT.target_digest.clone();
    let validated = validate_engine_integration_mappings(
        &ENGINE_MAPPING_INPUT,
        &ENGINE_LEDGER,
        &ENGINE_POLICY,
        &target,
    )
    .unwrap();
    let implementation_evidence = validated
        .mappings()
        .keys()
        .enumerate()
        .map(|(index, capability_id)| {
            (
                capability_id.clone(),
                EvidenceId::new(format!("engine/implementation/{index:02}")).unwrap(),
            )
        })
        .collect();
    let decision_evidence = validated
        .mappings()
        .iter()
        .enumerate()
        .filter(|(_, (_, mapping))| {
            matches!(
                mapping.allowed_terminal_status,
                AllowedTerminalStatus::IdiomaticEquivalent
            )
        })
        .map(|(index, (capability_id, _))| {
            (
                capability_id.clone(),
                EvidenceId::new(format!("engine/decision/{index:02}")).unwrap(),
            )
        })
        .collect();
    assemble_engine_integration_manifest(
        &validated,
        &ENGINE_POLICY,
        engine_subject(),
        CanonicalSet::new([
            CaseId::new("runtime-checked").unwrap(),
            CaseId::new("runtime-legacy").unwrap(),
        ]),
        implementation_evidence,
        decision_evidence,
    )
    .unwrap()
}

fn engine_subject() -> EngineEvidenceSubject {
    let digest = |byte: u8| format!("sha256:{byte:064x}").parse().unwrap();
    let target = TargetIdentity {
        format_version: FormatVersion,
        repository: "https://github.com/dagger/dagger".parse().unwrap(),
        dagger_revision: "25300124ca110612edc09c43f89cb5fad6028170".parse().unwrap(),
        engine_version: "1.0.0-beta.10".parse().unwrap(),
        rust_sdk_version: "1.0.0-beta.10".parse().unwrap(),
        rust_toolchain: "1.97.1".parse().unwrap(),
        core_schema_digest: digest(1),
    };
    EngineEvidenceSubject {
        target,
        engine_source_digest: digest(2),
        packaged_assets_digest: digest(3),
        sdk_dependency: PublishedSdkDependency::Git {
            url: "https://github.com/iw/dagger".parse().unwrap(),
            revision: "25300124ca110612edc09c43f89cb5fad6028170".parse().unwrap(),
            package: "dagger-sdk".parse().unwrap(),
        },
        rust_toolchain: "1.97.1".parse().unwrap(),
        operation_input_digests: BTreeSet::from([digest(4)]),
        operation_manifest_digests: BTreeSet::from([digest(5)]),
    }
}

fn observation(
    manifest: &EngineIntegrationManifest,
    evidence_id: &str,
    evidence_domain: EngineEvidenceDomain,
    proved_capabilities: impl IntoIterator<Item = CapabilityId>,
) -> EngineIntegrationObservation {
    EngineIntegrationObservation {
        format_version: 1,
        evidence_id: EvidenceId::new(evidence_id).unwrap(),
        subject: manifest.expected_subject.clone(),
        evidence_domain,
        cases: manifest
            .required_cases
            .iter()
            .map(|case_id| {
                (
                    case_id.clone(),
                    CaseObservation::Passed {
                        observation_digest: Digest::sha256(case_id.as_str()),
                    },
                )
            })
            .collect(),
        proved_capabilities: CanonicalSet::new(proved_capabilities),
    }
}

fn add_manifest_evidence(
    registry: &mut EvidenceRegistry,
    manifest: &EngineIntegrationManifest,
    observations: &[EngineIntegrationObservation],
) {
    for (capability_id, evidence_id) in &manifest.implementation_evidence {
        registry.evidence.insert(
            evidence_id.clone(),
            evidence_reference(
                evidence_id.clone(),
                EvidenceKind::Implementation,
                &manifest.target_digest,
                CanonicalSet::new([capability_id.clone()]),
            ),
        );
    }
    for (capability_id, evidence_id) in &manifest.decision_evidence {
        registry.evidence.insert(
            evidence_id.clone(),
            evidence_reference(
                evidence_id.clone(),
                EvidenceKind::Decision,
                &manifest.target_digest,
                CanonicalSet::new([capability_id.clone()]),
            ),
        );
    }
    for observation in observations {
        registry.evidence.insert(
            observation.evidence_id.clone(),
            evidence_reference(
                observation.evidence_id.clone(),
                EvidenceKind::Verification,
                &manifest.target_digest,
                observation.proved_capabilities.clone(),
            ),
        );
    }
}

fn evidence_reference(
    evidence_id: EvidenceId,
    evidence_kind: EvidenceKind,
    target: &TargetDigest,
    proved_capability_ids: CanonicalSet<CapabilityId>,
) -> EvidenceReference {
    let verification = evidence_kind == EvidenceKind::Verification;
    EvidenceReference {
        evidence_id,
        evidence_kind,
        repository: ENGINE_POLICY.evidence_repository.clone(),
        revision: CommitSha::new("25300124ca110612edc09c43f89cb5fad6028170").unwrap(),
        path: RepositoryRelativePath::new(if verification {
            "sdk/rust/crates/dagger-sdk-completeness/tests/engine_integration.rs"
        } else {
            "sdk/rust/crates/dagger-sdk-completeness/src/engine_integration.rs"
        })
        .unwrap(),
        locator: SourceLocator::new("engine-integration-evidence").unwrap(),
        claim: NonEmptyText::new("Exact-target Rust engine integration evidence").unwrap(),
        command: verification.then(|| CommandSpec {
            program: ExecutableId::new("cargo").unwrap(),
            args: vec!["test".to_owned(), "--locked".to_owned()],
            working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
            environment: BTreeMap::new(),
        }),
        expected_outcome: verification.then(|| ExpectedOutcome {
            outcome: CheckOutcome::Passed,
            assertion: NonEmptyText::new("Exact-target engine case passed").unwrap(),
        }),
        execution_target: Some(target.clone()),
        platform_scope: if verification {
            CanonicalSet::new([Platform {
                operating_system: OperatingSystem::Linux,
                architecture: Architecture::Amd64,
            }])
        } else {
            CanonicalSet::default()
        },
        proved_capability_ids,
    }
}

fn rust_policy_bundle() -> SourceBundle {
    SourceBundle::new([
        source(
            ".kiro/specs/rust-sdk-client-lifecycle/requirements.md",
            include_bytes!("../../../../../.kiro/specs/rust-sdk-client-lifecycle/requirements.md"),
        ),
        source(
            ".kiro/specs/rust-sdk-completeness-contract/requirements.md",
            include_bytes!(
                "../../../../../.kiro/specs/rust-sdk-completeness-contract/requirements.md"
            ),
        ),
        source(
            ".kiro/specs/rust-sdk-core-codegen/requirements.md",
            include_bytes!("../../../../../.kiro/specs/rust-sdk-core-codegen/requirements.md"),
        ),
        source(
            ".kiro/specs/rust-sdk-engine-integration/requirements.md",
            include_bytes!(
                "../../../../../.kiro/specs/rust-sdk-engine-integration/requirements.md"
            ),
        ),
        source(
            ".kiro/specs/rust-sdk-transport-observability/requirements.md",
            include_bytes!(
                "../../../../../.kiro/specs/rust-sdk-transport-observability/requirements.md"
            ),
        ),
        source("sdk/rust/AGENTS.md", include_bytes!("../../../AGENTS.md")),
    ])
}

fn source(path: &str, bytes: &[u8]) -> (RepositoryRelativePath, Vec<u8>) {
    (RepositoryRelativePath::new(path).unwrap(), bytes.to_vec())
}
