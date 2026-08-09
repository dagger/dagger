//! Engine-integration scope and evidence-separation properties.
//!
//! Tests build valid contracts first and mutate one reviewed boundary at a time. The
//! reference model remains deliberately smaller than production validation so a
//! counterexample identifies the exact scope, owner, status, fingerprint, or evidence
//! separation rule that diverged.

use std::sync::LazyLock;

use dagger_sdk_completeness::extract::policy::{PolicyClauseSelection, extract_policy_clauses};
use dagger_sdk_completeness::{
    AllowedTerminalStatus, AuthorityId, AuthorityRegistry, CanonicalSet, CapabilityId, Digest,
    EngineEvidenceDomain, EngineIntegrationMappings, EngineMappingDisposition, FeatureId,
    FeatureScopePolicy, HarnessMappings, RepositoryRelativePath, ResolvedLedger, SourceBundle,
    Status, TargetDigest, decode_canonical, engine_integration_contract, recompute_source_digest,
    rust_artifact_digest, validate_engine_integration_mappings,
};
use proptest::prelude::*;

const AUTHORITIES: &[u8] = include_bytes!("../../../completeness/authorities.json");
const LEDGER: &[u8] = include_bytes!("../../../completeness/artifacts/ledger.json");
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
