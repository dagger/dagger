//! Exhaustive binding closure, evidence freshness, and conformance admission properties.

use std::collections::{BTreeMap, BTreeSet};

use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{CoreProjectionRequest, project_core};
use dagger_sdk_completeness::{
    CanonicalSet, CapabilityId, CheckOutcome, CommandSpec, CommitSha, ConformanceObservation,
    CoreCodegenEvidencePolicy, CoreCodegenEvidenceRecord, CoreCodegenEvidenceResult,
    CoreCodegenMappings, CoreConformanceRun, Digest, DigestDomain, EvidenceDomain, EvidenceId,
    ExecutableId, FeatureId, GeneratedBindingManifest, ManifestBindingKind, PolicyId,
    RepositoryRelativePath, ResolvedLedger, admit_core_codegen_evidence,
    assemble_core_codegen_manifest, canonical_digest, core_conformance_evidence, decode_canonical,
    required_conformance_categories, validate_core_codegen_bijection,
};
use proptest::prelude::*;
use proptest::test_runner::{Config as ProptestConfig, TestRunner};

const TARGET: &[u8] = include_bytes!("../../../completeness/target.json");
const SCHEMA: &[u8] = include_bytes!("../../../completeness/snapshots/schema.json");
const LEDGER: &[u8] = include_bytes!("../../../completeness/artifacts/ledger.json");
const MAPPINGS: &[u8] = include_bytes!("../../../completeness/core-codegen-mappings.json");
const MANIFEST: &[u8] =
    include_bytes!("../../../completeness/artifacts/core-codegen-bindings.json");

fn exact_manifest() -> (GeneratedBindingManifest, ResolvedLedger) {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
    let ledger = decode_canonical::<ResolvedLedger>(LEDGER).expect("ledger must be canonical");
    let mappings = CoreCodegenMappings::decode(MAPPINGS).expect("mappings must be canonical");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked schema must project");
    let manifest = assemble_core_codegen_manifest(
        &target,
        &ledger,
        &mappings,
        plan.catalog(),
        BTreeMap::new(),
    )
    .expect("exact binding join must close");
    (manifest, ledger)
}

#[test]
fn exact_target_manifest_is_complete_but_status_neutral() {
    let (manifest, ledger) = exact_manifest();
    let active = ledger
        .capabilities
        .values()
        .filter(|row| row.owner_feature == Some(FeatureId::Feature4))
        .count();
    assert_eq!(active, 3_277);
    assert_eq!(manifest.bindings.len(), active);
    assert_eq!(
        manifest
            .bindings
            .values()
            .filter(|binding| binding.authority_id.as_str() == "engine-schema")
            .count(),
        1_567
    );
    assert_eq!(
        manifest
            .bindings
            .values()
            .filter(|binding| binding.authority_id.as_str() == "go-client")
            .count(),
        1_673
    );
    assert_eq!(
        manifest
            .bindings
            .values()
            .filter(|binding| binding.authority_id.as_str() == "go-codegen")
            .count(),
        21
    );
    assert_eq!(
        manifest
            .bindings
            .values()
            .filter(|binding| binding.authority_id.as_str() == "rust-policy")
            .count(),
        16
    );

    let json = serde_json::to_value(&manifest).expect("manifest must serialize");
    assert!(
        json["bindings"]
            .as_object()
            .expect("bindings object")
            .values()
            .all(|binding| binding.get("status").is_none())
    );
}

#[test]
fn checked_binding_manifest_is_canonical_and_bijective() {
    let manifest = GeneratedBindingManifest::decode(MANIFEST)
        .expect("checked binding manifest must be canonical contract JSON");
    let ledger = decode_canonical::<ResolvedLedger>(LEDGER).expect("ledger must be canonical");
    validate_core_codegen_bijection(&ledger, &manifest)
        .expect("checked binding manifest must close the active capability scope");
    assert_eq!(manifest.target_revision, exact_manifest().0.target_revision);
}

#[test]
fn property_02_binding_closure_capability_bijection() {
    // Feature: rust-sdk-core-codegen, Property 2: Binding closure is a capability bijection
    let (manifest, ledger) = exact_manifest();
    let active_ids = manifest.bindings.keys().cloned().collect::<Vec<_>>();
    let strategy = (1_usize..17, any::<usize>(), any::<bool>(), 0_u8..8);
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 256,
        ..ProptestConfig::default()
    });
    runner
        .run(&strategy, |(width, seed, mutate, mutation)| {
            let start = seed % active_ids.len();
            let selected = (0..width)
                .map(|offset| active_ids[(start + offset) % active_ids.len()].clone())
                .collect::<BTreeSet<_>>();
            let sample_ledger = ResolvedLedger {
                capabilities: ledger
                    .capabilities
                    .iter()
                    .filter(|(capability_id, _)| selected.contains(*capability_id))
                    .map(|(capability_id, row)| (capability_id.clone(), row.clone()))
                    .collect(),
            };
            let mut sample_manifest = manifest.clone();
            sample_manifest
                .bindings
                .retain(|capability_id, _| selected.contains(capability_id));
            let capability_id = sample_manifest
                .bindings
                .keys()
                .next()
                .cloned()
                .expect("non-empty sample");

            if mutate {
                match mutation {
                    0 => {
                        sample_manifest.bindings.remove(&capability_id);
                    }
                    1 => {
                        let extra = CapabilityId::new("policy/rust-policy/property-extra")
                            .expect("static capability ID");
                        let mut record = sample_manifest.bindings[&capability_id].clone();
                        record.capability_id = extra.clone();
                        sample_manifest.bindings.insert(extra, record);
                    }
                    2 => {
                        sample_manifest
                            .bindings
                            .get_mut(&capability_id)
                            .expect("selected binding")
                            .capability_fingerprint = Digest::sha256(b"changed capability");
                    }
                    3 => {
                        sample_manifest
                            .bindings
                            .get_mut(&capability_id)
                            .expect("selected binding")
                            .authority_id = dagger_sdk_completeness::AuthorityId::new("wrong")
                            .expect("static authority");
                    }
                    4 => {
                        sample_manifest
                            .bindings
                            .get_mut(&capability_id)
                            .expect("selected binding")
                            .required_evidence
                            .clear();
                    }
                    5 => {
                        let record = sample_manifest
                            .bindings
                            .get_mut(&capability_id)
                            .expect("selected binding");
                        record.rust_symbol = None;
                        record.policy_id = None;
                    }
                    6 => {
                        let record = sample_manifest
                            .bindings
                            .get_mut(&capability_id)
                            .expect("selected binding");
                        record.rust_symbol = Some("crate::Extra".to_owned());
                        record.policy_id = Some(
                            PolicyId::new("policy/core-codegen/extra").expect("static policy"),
                        );
                    }
                    7 => {
                        let record = sample_manifest
                            .bindings
                            .get_mut(&capability_id)
                            .expect("selected binding");
                        record.binding_kind = ManifestBindingKind::IdiomaticEquivalent;
                        record.decision_id = None;
                    }
                    _ => unreachable!(),
                }
            }

            let accepted =
                validate_core_codegen_bijection(&sample_ledger, &sample_manifest).is_ok();
            prop_assert_eq!(accepted, !mutate);
            // The validator is read-only: a failed candidate cannot rewrite the ledger it checks.
            prop_assert_eq!(sample_ledger.capabilities.len(), selected.len(),);
            Ok(())
        })
        .expect("property cases must execute");
}

fn command() -> CommandSpec {
    CommandSpec {
        program: ExecutableId::new("cargo").expect("static executable"),
        args: vec!["test".to_owned(), "--locked".to_owned()],
        working_directory: RepositoryRelativePath::new("sdk/rust")
            .expect("static working directory"),
        environment: BTreeMap::new(),
    }
}

fn scope_digest(ids: &CanonicalSet<CapabilityId>) -> Digest {
    let ids = ids.iter().cloned().collect::<Vec<_>>();
    serde_json::to_vec(&ids)
        .map(Digest::sha256)
        .expect("capability IDs serialize")
}

fn valid_evidence(
    manifest: &GeneratedBindingManifest,
) -> (
    EvidenceId,
    CoreCodegenEvidenceRecord,
    CoreCodegenEvidencePolicy,
) {
    let (capability_id, binding) = manifest.bindings.iter().next().expect("manifest binding");
    let domain = *binding
        .required_evidence
        .iter()
        .next()
        .expect("required evidence domain");
    let capability_ids = CanonicalSet::new([capability_id.clone()]);
    let result = CoreCodegenEvidenceResult {
        outcome: CheckOutcome::Passed,
        assertion: "binding passed".to_owned(),
        capability_scope_digest: scope_digest(&capability_ids),
    };
    let result_digest = canonical_digest(DigestDomain::Artifact, &result).expect("result digest");
    let command = command();
    let command_digest =
        canonical_digest(DigestDomain::Artifact, &command).expect("command digest");
    let target_revision =
        CommitSha::new(manifest.target_revision.clone()).expect("target is a commit");
    let subject_revision = Digest::sha256(b"reviewed source");
    let evidence_id =
        EvidenceId::new("verification/core-codegen/property-30").expect("static evidence ID");
    let record = CoreCodegenEvidenceRecord {
        evidence_id: evidence_id.clone(),
        target_revision,
        schema_digest: Digest::new(manifest.schema_digest.clone()).expect("schema digest"),
        subject_revision: subject_revision.clone(),
        command,
        result,
        result_digest,
        capability_ids,
        projection_fingerprint: manifest.projection_fingerprint.clone(),
        implementation_fingerprints: BTreeMap::from([(
            capability_id.clone(),
            binding.implementation_fingerprint.clone(),
        )]),
        domains: [domain].into(),
    };
    let policy = CoreCodegenEvidencePolicy {
        subject_revision,
        command_digests: BTreeMap::from([(domain, command_digest)]),
    };
    (evidence_id, record, policy)
}

#[test]
fn property_30_evidence_cannot_outlive_subject() {
    // Feature: rust-sdk-core-codegen, Property 30: Evidence cannot outlive its subject
    let (manifest, _) = exact_manifest();
    let strategy = (any::<bool>(), 0_u8..8);
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 256,
        ..ProptestConfig::default()
    });
    runner
        .run(&strategy, |(mutate, mutation)| {
            let (evidence_id, mut record, policy) = valid_evidence(&manifest);
            if mutate {
                match mutation {
                    0 => {
                        record.target_revision =
                            CommitSha::new("0000000000000000000000000000000000000000")
                                .expect("static commit");
                    }
                    1 => {
                        record.subject_revision = Digest::sha256(b"changed subject");
                    }
                    2 => record.command.args.push("--changed".to_owned()),
                    3 => record.result_digest = Digest::sha256(b"changed result"),
                    4 => {
                        let second = manifest
                            .bindings
                            .keys()
                            .nth(1)
                            .expect("second binding")
                            .clone();
                        record.capability_ids = CanonicalSet::new([
                            record.capability_ids.iter().next().expect("first").clone(),
                            second,
                        ]);
                    }
                    5 => record.projection_fingerprint = Digest::sha256(b"changed projection"),
                    6 => {
                        let fingerprint = record
                            .implementation_fingerprints
                            .values_mut()
                            .next()
                            .expect("fingerprint");
                        *fingerprint = Digest::sha256(b"changed implementation");
                    }
                    7 => {
                        let replacement = [
                            EvidenceDomain::Implementation,
                            EvidenceDomain::Property,
                            EvidenceDomain::Compile,
                            EvidenceDomain::QueryProjection,
                            EvidenceDomain::Documentation,
                            EvidenceDomain::ExactTarget,
                            EvidenceDomain::Decision,
                        ]
                        .into_iter()
                        .find(|domain| !record.domains.contains(domain))
                        .expect("another domain");
                        record.domains = [replacement].into();
                    }
                    _ => unreachable!(),
                }
            }
            let accepted =
                admit_core_codegen_evidence(&evidence_id, &record, &manifest, &policy).is_ok();
            prop_assert_eq!(accepted, !mutate);
            Ok(())
        })
        .expect("property cases must execute");
}

fn conformance_run(manifest: &GeneratedBindingManifest) -> (CoreConformanceRun, Digest) {
    let capability_id = manifest
        .bindings
        .iter()
        .find(|(_, binding)| {
            binding
                .required_evidence
                .contains(&EvidenceDomain::ExactTarget)
        })
        .map(|(capability_id, _)| capability_id.clone())
        .expect("exact-target binding");
    let observations = required_conformance_categories()
        .into_iter()
        .map(|category| ConformanceObservation {
            category,
            operation: format!("generated::{category:?}"),
            outcome: CheckOutcome::Passed,
            capability_ids: CanonicalSet::new([capability_id.clone()]),
        })
        .collect::<Vec<_>>();
    let command = command();
    let command_digest =
        canonical_digest(DigestDomain::Artifact, &command).expect("command digest");
    let run = CoreConformanceRun {
        target_revision: CommitSha::new(manifest.target_revision.clone()).expect("target commit"),
        schema_digest: Digest::new(manifest.schema_digest.clone()).expect("schema digest"),
        subject_revision: Digest::sha256(b"reviewed source"),
        command,
        result_digest: canonical_digest(DigestDomain::Artifact, &observations)
            .expect("observations digest"),
        observations,
    };
    (run, command_digest)
}

fn refresh_run_digest(run: &mut CoreConformanceRun) {
    run.result_digest =
        canonical_digest(DigestDomain::Artifact, &run.observations).expect("observations digest");
}

#[test]
fn property_29_exact_target_conformance_spans_every_generated_category() {
    // Feature: rust-sdk-core-codegen, Property 29: Exact-target conformance spans every generated category
    let (manifest, _) = exact_manifest();
    let strategy = (any::<bool>(), 0_u8..8);
    let mut runner = TestRunner::new(ProptestConfig {
        cases: 256,
        ..ProptestConfig::default()
    });
    runner
        .run(&strategy, |(mutate, mutation)| {
            let (mut run, command_digest) = conformance_run(&manifest);
            if mutate {
                match mutation {
                    0 => {
                        run.target_revision =
                            CommitSha::new("0000000000000000000000000000000000000000")
                                .expect("static commit");
                    }
                    1 => run.command.args.push("--changed".to_owned()),
                    2 => {
                        run.observations.pop();
                        refresh_run_digest(&mut run);
                    }
                    3 => {
                        run.observations.push(run.observations[0].clone());
                        refresh_run_digest(&mut run);
                    }
                    4 => {
                        run.observations[0].outcome = CheckOutcome::Failed;
                        refresh_run_digest(&mut run);
                    }
                    5 => run.result_digest = Digest::sha256(b"changed result"),
                    6 => {
                        run.observations[0].capability_ids =
                            CanonicalSet::new([CapabilityId::new(
                                "policy/rust-policy/not-in-manifest",
                            )
                            .expect("static capability")]);
                        refresh_run_digest(&mut run);
                    }
                    7 => {
                        run.observations[0].operation.clear();
                        refresh_run_digest(&mut run);
                    }
                    _ => unreachable!(),
                }
            }
            let accepted = core_conformance_evidence(
                EvidenceId::new("verification/core-codegen/property-29")
                    .expect("static evidence ID"),
                &run,
                &manifest,
                &command_digest,
            )
            .is_ok();
            prop_assert_eq!(accepted, !mutate);
            Ok(())
        })
        .expect("property cases must execute");
}
