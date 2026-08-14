//! Exact-target artifact identity, real-byte round-trip, and state-machine properties.

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};
use std::process::{Command, Stdio};

const CASES: u32 = 256;

fn property_config() -> Config {
    Config {
        cases: CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/signoff-artifact.txt"
        )))),
        ..Config::default()
    }
}

fn commit(byte: u8) -> CommitSha {
    CommitSha::new(format!("{byte:02x}").repeat(20)).unwrap()
}

fn provenance_id(component: ArtifactComponent, suffix: u8) -> ProvenanceId {
    ProvenanceId::new(format!("artifact/{component:?}/{suffix}").to_ascii_lowercase()).unwrap()
}

fn component_record(component: ArtifactComponent, seed: u8) -> ArtifactComponentRecord {
    ArtifactComponentRecord {
        component,
        input_digest: Digest::sha256([seed, component as u8, 1]),
        content_digest: Digest::sha256([seed, component as u8, 2]),
        provenance: CanonicalSet::new([provenance_id(component, seed)]),
    }
}

fn provenance_for(plan: &ArtifactPlan) -> ArtifactProvenanceDocument {
    ArtifactProvenanceDocument {
        format_version: plan.format_version,
        components: plan
            .components
            .iter()
            .map(|(component, record)| (*component, record.provenance.clone()))
            .collect(),
        toolchain_digests: plan.toolchain_digests.clone(),
    }
}

fn refresh_provenance(plan: &mut ArtifactPlan) {
    plan.provenance_digest =
        canonical_digest(DigestDomain::ConformanceSecurity, &provenance_for(plan)).unwrap();
}

fn valid_plan(seed: u8) -> ArtifactPlan {
    let components = required_artifact_components()
        .into_iter()
        .map(|component| (component, component_record(component, seed)))
        .collect();
    let toolchain_digests = required_artifact_toolchains()
        .into_iter()
        .map(|role| (role, Digest::sha256([seed, role as u8, 3])))
        .collect();
    let source = Digest::sha256([seed, 4]);
    let subject_revision = commit(seed.wrapping_add(1).max(1));
    let rust_dependency = RustSdkDependencyDescriptor {
        source: RustSdkDependencySource::Git,
        package: "dagger-sdk".to_owned(),
        url: "https://github.com/iw/dagger".to_owned(),
        revision: subject_revision.clone(),
    };
    let rust_dependency_descriptor_digest = rust_dependency.direct_digest().unwrap();
    let mut plan = ArtifactPlan {
        format_version: ArtifactFormatVersion::V1,
        target_descriptor_digest: TargetDigest::new(Digest::sha256([seed, 5])),
        target_revision: commit(seed.max(1)),
        subject: SubjectRevisionObservation {
            repository: "https://github.com/iw/dagger".to_owned(),
            revision: subject_revision,
            focused_source_digest: source.clone(),
            workspace_focused_source_digest: source,
            reachable: true,
            clean: true,
            immutable: true,
        },
        platform: PlatformDescriptor::linux_amd64(),
        engine_input_digest: Digest::sha256([seed, 6]),
        cli_input_digest: Digest::sha256([seed, 7]),
        go_runtime_digest: Digest::sha256([seed, 8]),
        rust_manifest_digest: Digest::sha256([seed, 9]),
        rust_descriptor_digest: Digest::sha256([seed, 10]),
        rust_dependency,
        rust_dependency_descriptor_digest,
        toolchain_digests,
        components,
        provenance_digest: Digest::sha256([]),
        materialization: ArtifactMaterialization::Build,
    };
    refresh_provenance(&mut plan);
    plan
}

fn manifest_for(plan: &ArtifactPlan, payload: &[u8]) -> ExactTargetArtifactManifest {
    ExactTargetArtifactManifest {
        format_version: plan.format_version,
        target_descriptor_digest: plan.target_descriptor_digest.clone(),
        target_revision: plan.target_revision.clone(),
        subject_revision: plan.subject.revision.clone(),
        subject_source_digest: plan.subject.focused_source_digest.clone(),
        platform: plan.platform.clone(),
        engine_input_digest: plan.engine_input_digest.clone(),
        cli_input_digest: plan.cli_input_digest.clone(),
        go_runtime_digest: plan.go_runtime_digest.clone(),
        rust_manifest_digest: plan.rust_manifest_digest.clone(),
        rust_descriptor_digest: plan.rust_descriptor_digest.clone(),
        rust_dependency: plan.rust_dependency.clone(),
        rust_dependency_descriptor_digest: plan.rust_dependency_descriptor_digest.clone(),
        toolchain_digests: plan.toolchain_digests.clone(),
        components: plan.components.clone(),
        payload_digest: Digest::sha256(payload),
        payload_size_bytes: payload.len() as u64,
        provenance_digest: plan.provenance_digest.clone(),
    }
}

fn bundle_for(plan: &ArtifactPlan, payload: Vec<u8>) -> VerifiedArtifactBundle {
    assemble_artifact_bundle(manifest_for(plan, &payload), provenance_for(plan), payload).unwrap()
}

fn build_events(counters: &ArtifactCounters) -> Vec<ArtifactEvent> {
    let mut events = vec![ArtifactEvent::ConstructionStarted];
    for (component, count) in &counters.component_builds {
        for _ in 0..*count {
            events.push(ArtifactEvent::ComponentBuilt {
                component: *component,
            });
        }
    }
    events.extend([
        ArtifactEvent::PayloadExported,
        ArtifactEvent::ManifestVerified,
        ArtifactEvent::PayloadVerified,
        ArtifactEvent::ComponentsVerified,
        ArtifactEvent::ArtifactReady,
    ]);
    events
}

fn import_events() -> Vec<ArtifactEvent> {
    vec![
        ArtifactEvent::BundleSupplied,
        ArtifactEvent::ManifestVerified,
        ArtifactEvent::PayloadVerified,
        ArtifactEvent::ComponentsVerified,
        ArtifactEvent::ContainerImported,
        ArtifactEvent::ArtifactReady,
    ]
}

fn counters(build: bool) -> ArtifactCounters {
    ArtifactCounters {
        construction: u32::from(build),
        imports: u32::from(!build),
        component_builds: required_artifact_components()
            .into_iter()
            .map(|component| (component, u32::from(build)))
            .collect(),
        forbidden_work: CanonicalSet::default(),
    }
}

fn build_observation(elapsed_millis: u64) -> ArtifactBuildObservation {
    let counters = counters(true);
    ArtifactBuildObservation {
        format_version: ArtifactFormatVersion::V1,
        events: build_events(&counters),
        construction_count: counters.construction,
        import_count: counters.imports,
        component_build_counts: counters.component_builds,
        forbidden_work_counts: forbidden_artifact_work_classes()
            .into_iter()
            .map(|work| (work, 0))
            .collect(),
        materialization_elapsed_millis: NonZeroMillis::new(elapsed_millis).unwrap(),
    }
}

fn import_observation(
    bundle: &VerifiedArtifactBundle,
    elapsed_millis: u64,
) -> ArtifactImportObservation {
    let counters = counters(false);
    ArtifactImportObservation {
        format_version: ArtifactFormatVersion::V1,
        events: import_events(),
        construction_count: counters.construction,
        import_count: counters.imports,
        component_build_counts: counters.component_builds,
        forbidden_work_counts: forbidden_artifact_work_classes()
            .into_iter()
            .map(|work| (work, 0))
            .collect(),
        verified_component_digests: bundle
            .manifest()
            .components
            .iter()
            .map(|(component, record)| (*component, record.content_digest.clone()))
            .collect(),
        materialization_elapsed_millis: NonZeroMillis::new(elapsed_millis).unwrap(),
    }
}

#[test]
fn build_observation_has_one_closed_canonical_adapter_contract() {
    let encoded = canonical_bytes(&build_observation(37)).unwrap();
    let value: serde_json::Value = serde_json::from_slice(&encoded).unwrap();
    assert_eq!(
        value,
        serde_json::json!({
            "format_version": "1.0.0",
            "events": [
                "construction-started",
                {"component-built": {"component": "engine"}},
                {"component-built": {"component": "cli"}},
                {"component-built": {"component": "go-runtime"}},
                {"component-built": {"component": "rust-sdk"}},
                "payload-exported",
                "manifest-verified",
                "payload-verified",
                "components-verified",
                "artifact-ready"
            ],
            "construction_count": 1,
            "import_count": 0,
            "component_build_counts": {
                "engine": 1,
                "cli": 1,
                "go-runtime": 1,
                "rust-sdk": 1
            },
            "forbidden_work_counts": {
                "unrelated-sdk-build": 0,
                "unrelated-sdk-test": 0,
                "complete-go-test-suite": 0,
                "unscoped-generation": 0,
                "distribution-build": 0,
                "strategy-fallback": 0
            },
            "materialization_elapsed_millis": 37
        })
    );
}

#[test]
fn import_observation_has_one_closed_canonical_adapter_contract() {
    let build_plan = valid_plan(27);
    let bundle = bundle_for(&build_plan, b"import contract".to_vec());
    let encoded = canonical_bytes(&import_observation(&bundle, 41)).unwrap();
    let value: serde_json::Value = serde_json::from_slice(&encoded).unwrap();
    assert_eq!(
        value,
        serde_json::json!({
            "format_version": "1.0.0",
            "events": [
                "bundle-supplied",
                "manifest-verified",
                "payload-verified",
                "components-verified",
                "container-imported",
                "artifact-ready"
            ],
            "construction_count": 0,
            "import_count": 1,
            "component_build_counts": {
                "engine": 0,
                "cli": 0,
                "go-runtime": 0,
                "rust-sdk": 0
            },
            "forbidden_work_counts": {
                "unrelated-sdk-build": 0,
                "unrelated-sdk-test": 0,
                "complete-go-test-suite": 0,
                "unscoped-generation": 0,
                "distribution-build": 0,
                "strategy-fallback": 0
            },
            "verified_component_digests": bundle.manifest().components.iter().map(
                |(component, record)| (*component, record.content_digest.clone())
            ).collect::<std::collections::BTreeMap<_, _>>(),
            "materialization_elapsed_millis": 41
        })
    );
}

fn mutate_semantic_input(plan: &mut ArtifactPlan, payload: &mut Vec<u8>, mutation: u8) {
    let changed = Digest::sha256([mutation, 0xfe, 0x01]);
    match mutation {
        1 => plan.target_descriptor_digest = TargetDigest::new(changed),
        2 => plan.target_revision = commit(plan.target_revision.as_str().as_bytes()[0] ^ 0xff),
        3 => plan.subject.revision = commit(plan.subject.revision.as_str().as_bytes()[0] ^ 0xff),
        4 => {
            plan.subject.focused_source_digest = changed.clone();
            plan.subject.workspace_focused_source_digest = changed;
        }
        5 => plan.platform.architecture = Architecture::Arm64,
        6 => plan.engine_input_digest = changed,
        7 => plan.cli_input_digest = changed,
        8 => plan.go_runtime_digest = changed,
        9 => plan.rust_manifest_digest = changed,
        10 => plan.rust_descriptor_digest = changed,
        11 => {
            plan.toolchain_digests
                .insert(ToolchainRole::RustToolchain, changed);
        }
        12 => {
            plan.components
                .get_mut(&ArtifactComponent::Engine)
                .unwrap()
                .input_digest = changed;
        }
        13 => {
            plan.components
                .get_mut(&ArtifactComponent::Cli)
                .unwrap()
                .content_digest = changed;
        }
        14 => {
            plan.components
                .get_mut(&ArtifactComponent::RustSdk)
                .unwrap()
                .provenance = CanonicalSet::new([
                provenance_id(ArtifactComponent::RustSdk, 0xee),
                provenance_id(ArtifactComponent::RustSdk, 0xef),
            ]);
        }
        15 => payload.push(0xff),
        _ => {}
    }
    refresh_provenance(plan);
}

// Invariant: the bundle identity covers every semantic input and every actual payload byte.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_07_artifact_identity_accounts_every_immutable_source(
        seed in any::<u8>(),
        payload_tail in prop::collection::vec(any::<u8>(), 0..64),
        mutation in 0_u8..16,
    ) {
        let plan = valid_plan(seed);
        let mut payload = b"small-oci-fixture\0".to_vec();
        payload.extend(payload_tail);
        let baseline = bundle_for(&plan, payload.clone());

        let mut changed_plan = plan;
        let mut changed_payload = payload;
        mutate_semantic_input(&mut changed_plan, &mut changed_payload, mutation);
        let changed = bundle_for(&changed_plan, changed_payload);

        prop_assert_eq!(
            baseline.bundle_digest() == changed.bundle_digest(),
            mutation == 0
        );
        prop_assert_eq!(decode_artifact_bundle(baseline.bytes()).unwrap(), baseline);
    }
}

#[test]
fn exact_subject_rejects_an_alternate_credential_free_repository() {
    let mut plan = valid_plan(7);
    plan.rust_dependency.url = "https://github.com/iw/alternate-dagger".to_owned();
    plan.rust_dependency_descriptor_digest = plan.rust_dependency.direct_digest().unwrap();
    refresh_provenance(&mut plan);
    assert!(artifact_manifest_for_payload(&plan, b"payload").is_err());
}

#[test]
fn subject_repository_has_one_canonical_credential_free_form() {
    assert!(is_canonical_subject_repository(
        "https://github.com/iw/dagger"
    ));
    for rejected in [
        "https://github.com/iw/dagger.git",
        "https://github.com/iw/dagger/",
        "https://token@github.com/iw/dagger",
        "https://github.com/iw/dagger?ref=main",
        "https://mirror.example/dagger/../iw/dagger",
        "ssh://git@github.com/iw/dagger",
    ] {
        assert!(
            !is_canonical_subject_repository(rejected),
            "accepted {rejected}"
        );
    }
}

// Invariant: a plan admits one branch only, and every mixed, duplicated, or reordered trace fails.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn property_08_build_import_exclusive_at_most_once(
        seed in any::<u8>(),
        import in any::<bool>(),
        mutation in 0_u8..13,
    ) {
        let mut plan = valid_plan(seed);
        let payload = vec![seed, 1, 2, 3];
        let bundle = bundle_for(&plan, payload);
        if import {
            plan.materialization = ArtifactMaterialization::Import {
                manifest_digest: bundle.manifest_digest().clone(),
                payload_digest: bundle.manifest().payload_digest.clone(),
            };
        }
        let mut counts = counters(!import);
        let mut events = if import { import_events() } else { build_events(&counts) };
        let mut strategy = plan.materialization.clone();
        match mutation {
            1 => strategy = if import { ArtifactMaterialization::Build } else {
                ArtifactMaterialization::Import {
                    manifest_digest: bundle.manifest_digest().clone(),
                    payload_digest: bundle.manifest().payload_digest.clone(),
                }
            },
            2 => counts.construction += 1,
            3 => counts.imports += 1,
            4 => { counts.component_builds.insert(ArtifactComponent::Engine, 2); }
            5 => counts.forbidden_work = CanonicalSet::new([ForbiddenArtifactWork::UnrelatedSdkBuild]),
            6 => events.push(ArtifactEvent::ArtifactReady),
            7 => events.swap(0, 1),
            8 => { counts.component_builds.remove(&ArtifactComponent::Cli); }
            9 if import => {
                plan.materialization = ArtifactMaterialization::Import {
                    manifest_digest: Digest::sha256("wrong manifest"),
                    payload_digest: bundle.manifest().payload_digest.clone(),
                };
                strategy = plan.materialization.clone();
            }
            9 => counts.forbidden_work = CanonicalSet::new([ForbiddenArtifactWork::CompleteGoTestSuite]),
            10 => counts.forbidden_work = CanonicalSet::new([ForbiddenArtifactWork::UnscopedGeneration]),
            11 => counts.forbidden_work = CanonicalSet::new([ForbiddenArtifactWork::StrategyFallback]),
            _ => {}
        }
        let mut verified_component_digests = bundle
            .manifest()
            .components
            .iter()
            .map(|(component, record)| (*component, record.content_digest.clone()))
            .collect::<std::collections::BTreeMap<_, _>>();
        if mutation == 12 {
            verified_component_digests.insert(
                ArtifactComponent::Engine,
                Digest::sha256("wrong component bytes"),
            );
        }
        let observation = ArtifactObservation {
            strategy,
            manifest: bundle.manifest().clone(),
            bundle,
            events,
            counters: counts,
            verified_component_digests,
            elapsed_millis: 1,
        };
        prop_assert_eq!(admit_artifact(&plan, observation).is_ok(), mutation == 0);
    }
}

#[test]
fn canonical_bundle_round_trips_real_bytes_across_a_fresh_read() {
    let plan = valid_plan(42);
    let payload = b"oci-layout\0manifest\0layer-canary\0".to_vec();
    let bundle = bundle_for(&plan, payload.clone());
    let temporary = tempfile::NamedTempFile::new().unwrap();
    std::fs::write(temporary.path(), bundle.bytes()).unwrap();

    let restarted_bytes = std::fs::read(temporary.path()).unwrap();
    let restarted = decode_artifact_bundle(&restarted_bytes).unwrap();
    assert_eq!(restarted.payload(), payload);
    assert_eq!(restarted.bundle_digest(), bundle.bundle_digest());
    assert_eq!(restarted.manifest_digest(), bundle.manifest_digest());
}

#[test]
fn missing_payload_bytes_cannot_be_recovered_from_a_digest() {
    let plan = valid_plan(7);
    let payload = b"real bytes".to_vec();
    let manifest = manifest_for(&plan, &payload);
    assert!(assemble_artifact_bundle(manifest, provenance_for(&plan), Vec::new()).is_err());
}

#[test]
fn dependency_descriptor_mutation_and_registry_substitution_fail_closed() {
    let plan = valid_plan(17);

    let mut digest_mutation = plan.clone();
    digest_mutation.rust_dependency_descriptor_digest = Digest::sha256("mutated descriptor");
    assert!(artifact_manifest_for_payload(&digest_mutation, b"payload").is_err());

    let mut credential_mutation = plan.clone();
    credential_mutation.rust_dependency.url = "https://token@github.com/iw/dagger".to_owned();
    credential_mutation.rust_dependency_descriptor_digest =
        credential_mutation.rust_dependency.direct_digest().unwrap();
    assert!(artifact_manifest_for_payload(&credential_mutation, b"payload").is_err());

    let mut registry_substitution = serde_json::to_value(plan).unwrap();
    let dependency = registry_substitution
        .get_mut("rust_dependency")
        .and_then(serde_json::Value::as_object_mut)
        .unwrap();
    dependency.insert("source".to_owned(), serde_json::json!("registry"));
    dependency.insert(
        "exact_version".to_owned(),
        serde_json::json!("=1.0.0-beta.10"),
    );
    assert!(serde_json::from_value::<ArtifactPlan>(registry_substitution).is_err());
}

// Invariant: a persisted Build receipt is reusable only with its exact plan and bundle bytes.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn build_receipt_rejects_every_identity_history_counter_and_duration_mutation(
        seed in any::<u8>(),
        elapsed in 1_u64..1_000_000,
        mutation in 0_u8..16,
    ) {
        let plan = valid_plan(seed);
        let payload = vec![seed, 0x52, 0x43, 0x50];
        let bundle = bundle_for(&plan, payload);
        let mut receipt = artifact_build_receipt(
            &plan,
            &bundle,
            build_observation(elapsed),
        ).unwrap();
        let mut admitted_plan = plan.clone();
        let mut admitted_bundle = decode_artifact_bundle(bundle.bytes()).unwrap();
        match mutation {
            1 => receipt.plan_digest = Digest::sha256("different plan"),
            2 => receipt.bundle_digest = Digest::sha256("different bundle"),
            3 => receipt.manifest_digest = Digest::sha256("different manifest"),
            4 => receipt.payload_digest = Digest::sha256("different payload"),
            5 => receipt.payload_size_bytes += 1,
            6 => {
                receipt.component_digests.insert(
                    ArtifactComponent::Engine,
                    Digest::sha256("different engine component"),
                );
            }
            7 => receipt.events.swap(0, 1),
            8 => receipt.construction_count += 1,
            9 => receipt.import_count += 1,
            10 => {
                receipt.component_build_counts.remove(&ArtifactComponent::Cli);
            }
            11 => {
                receipt.forbidden_work_counts.insert(
                    ForbiddenArtifactWork::UnrelatedSdkBuild,
                    1,
                );
            }
            12 => {
                receipt.materialization_elapsed_millis =
                    NonZeroMillis::new(elapsed.saturating_add(1)).unwrap();
            }
            13 => receipt.receipt_digest = Digest::sha256("different receipt"),
            14 => {
                let alternate = valid_plan(seed.wrapping_add(1));
                admitted_bundle = bundle_for(&alternate, b"alternate payload".to_vec());
            }
            15 => admitted_plan = valid_plan(seed.wrapping_add(1)),
            _ => {}
        }
        prop_assert_eq!(
            admit_artifact_build_receipt(&admitted_plan, admitted_bundle, &receipt).is_ok(),
            mutation == 0,
        );
        if mutation == 0 {
            let encoded = canonical_bytes(&receipt).unwrap();
            prop_assert_eq!(
                decode_canonical::<ArtifactBuildReceipt>(&encoded).unwrap(),
                receipt,
            );
        }
    }
}

// Invariant: receipt construction retains and validates observed work instead of inventing it.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn build_receipt_rejects_invalid_raw_graph_observations(
        seed in any::<u8>(),
        mutation in 0_u8..9,
    ) {
        let plan = valid_plan(seed);
        let bundle = bundle_for(&plan, vec![seed, 0x4f, 0x42, 0x53]);
        let mut observation = build_observation(37);
        match mutation {
            1 => observation.construction_count += 1,
            2 => observation.import_count += 1,
            3 => {
                observation
                    .component_build_counts
                    .insert(ArtifactComponent::Engine, 2);
            }
            4 => {
                observation.forbidden_work_counts.insert(
                    ForbiddenArtifactWork::UnrelatedSdkBuild,
                    1,
                );
            }
            5 => observation.events.swap(0, 1),
            6 => observation.events.push(ArtifactEvent::ArtifactReady),
            7 => {
                observation
                    .component_build_counts
                    .remove(&ArtifactComponent::Cli);
            }
            8 => {
                observation
                    .forbidden_work_counts
                    .remove(&ForbiddenArtifactWork::CompleteGoTestSuite);
            }
            _ => {}
        }
        prop_assert_eq!(
            artifact_build_receipt(&plan, &bundle, observation).is_ok(),
            mutation == 0,
        );
    }
}

// Invariant: the sole Import receipt retains the evaluated graph rather than the expected graph.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn import_receipt_rejects_invalid_raw_graph_observations(
        seed in any::<u8>(),
        mutation in 0_u8..11,
    ) {
        let build_plan = valid_plan(seed);
        let bundle = bundle_for(&build_plan, vec![seed, 0x49, 0x4d, 0x50]);
        let import_plan = artifact_import_plan(&build_plan, &bundle).unwrap();
        let mut observation = import_observation(&bundle, 43);
        match mutation {
            1 => observation.import_count += 1,
            2 => observation.construction_count += 1,
            3 => {
                observation
                    .component_build_counts
                    .insert(ArtifactComponent::Engine, 1);
            }
            4 => {
                observation.forbidden_work_counts.insert(
                    ForbiddenArtifactWork::UnrelatedSdkTest,
                    1,
                );
            }
            5 => observation.events.swap(0, 1),
            6 => observation.events.push(ArtifactEvent::ArtifactReady),
            7 => {
                observation.verified_component_digests.insert(
                    ArtifactComponent::RustSdk,
                    Digest::sha256("substituted imported Rust SDK"),
                );
            }
            8 => {
                observation
                    .component_build_counts
                    .remove(&ArtifactComponent::Cli);
            }
            9 => {
                observation
                    .forbidden_work_counts
                    .remove(&ForbiddenArtifactWork::StrategyFallback);
            }
            10 => {
                observation
                    .verified_component_digests
                    .remove(&ArtifactComponent::GoRuntime);
            }
            _ => {}
        }
        prop_assert_eq!(
            artifact_import_receipt(&import_plan, &bundle, observation).is_ok(),
            mutation == 0,
        );
    }
}

// Invariant: a persisted Import receipt is reusable only without any identity or history change.
proptest! {
    #![proptest_config(property_config())]

    #[test]
    fn import_receipt_rejects_every_substitution(
        seed in any::<u8>(),
        elapsed in 1_u64..1_000_000,
        mutation in 0_u8..17,
    ) {
        let build_plan = valid_plan(seed);
        let bundle = bundle_for(&build_plan, vec![seed, 0x52, 0x43, 0x50]);
        let import_plan = artifact_import_plan(&build_plan, &bundle).unwrap();
        let mut receipt = artifact_import_receipt(
            &import_plan,
            &bundle,
            import_observation(&bundle, elapsed),
        ).unwrap();
        let mut admitted_plan = import_plan.clone();
        let mut admitted_bundle = decode_artifact_bundle(bundle.bytes()).unwrap();
        match mutation {
            1 => receipt.plan_digest = Digest::sha256("different import plan"),
            2 => receipt.bundle_digest = Digest::sha256("different import bundle"),
            3 => receipt.manifest_digest = Digest::sha256("different import manifest"),
            4 => receipt.payload_digest = Digest::sha256("different imported payload"),
            5 => receipt.payload_size_bytes += 1,
            6 => {
                receipt.verified_component_digests.insert(
                    ArtifactComponent::Engine,
                    Digest::sha256("different imported engine"),
                );
            }
            7 => receipt.events.swap(0, 1),
            8 => receipt.construction_count += 1,
            9 => receipt.import_count += 1,
            10 => {
                receipt
                    .component_build_counts
                    .insert(ArtifactComponent::RustSdk, 1);
            }
            11 => {
                receipt.forbidden_work_counts.insert(
                    ForbiddenArtifactWork::DistributionBuild,
                    1,
                );
            }
            12 => {
                receipt.materialization_elapsed_millis =
                    NonZeroMillis::new(elapsed.saturating_add(1)).unwrap();
            }
            13 => receipt.receipt_digest = Digest::sha256("different import receipt"),
            14 => {
                let alternate_build = valid_plan(seed.wrapping_add(1));
                admitted_bundle =
                    bundle_for(&alternate_build, b"alternate import payload".to_vec());
            }
            15 => {
                let alternate_build = valid_plan(seed.wrapping_add(1));
                let alternate_bundle =
                    bundle_for(&alternate_build, b"alternate import payload".to_vec());
                admitted_plan = artifact_import_plan(&alternate_build, &alternate_bundle).unwrap();
            }
            16 => {
                receipt
                    .verified_component_digests
                    .remove(&ArtifactComponent::Cli);
            }
            _ => {}
        }
        prop_assert_eq!(
            admit_artifact_import_receipt(&admitted_plan, admitted_bundle, &receipt).is_ok(),
            mutation == 0,
        );
        if mutation == 0 {
            let encoded = canonical_bytes(&receipt).unwrap();
            prop_assert_eq!(
                decode_canonical::<ArtifactImportReceipt>(&encoded).unwrap(),
                receipt,
            );
        }
    }
}

#[test]
fn artifact_build_command_emits_a_re_admissible_measured_receipt() {
    let temp = tempfile::tempdir().unwrap();
    let plan = valid_plan(91);
    let payload = b"bounded command OCI payload";
    let plan_path = temp.path().join("plan.json");
    let payload_path = temp.path().join("payload.oci.tar.zst");
    let bundle_path = temp.path().join("exact-target.tar");
    let manifest_path = temp.path().join("manifest.json");
    let receipt_path = temp.path().join("build-receipt.json");
    let observation_path = temp.path().join("build-observation.json");
    std::fs::write(&plan_path, canonical_bytes(&plan).unwrap()).unwrap();
    std::fs::write(&payload_path, payload).unwrap();
    std::fs::write(
        &observation_path,
        canonical_bytes(&build_observation(37)).unwrap(),
    )
    .unwrap();

    let status = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "artifact-build",
            "--plan",
            plan_path.to_str().unwrap(),
            "--payload",
            payload_path.to_str().unwrap(),
            "--observation",
            observation_path.to_str().unwrap(),
            "--bundle-output",
            bundle_path.to_str().unwrap(),
            "--manifest-output",
            manifest_path.to_str().unwrap(),
            "--receipt-output",
            receipt_path.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(status.success());

    let receipt_bytes = std::fs::read(&receipt_path).unwrap();
    let receipt: ArtifactBuildReceipt = decode_canonical(&receipt_bytes).unwrap();
    assert_eq!(receipt.materialization_elapsed_millis.get(), 37);
    assert_eq!(receipt.construction_count, 1);
    assert_eq!(receipt.import_count, 0);
    assert!(
        receipt
            .component_build_counts
            .values()
            .all(|count| *count == 1)
    );
    assert!(
        receipt
            .forbidden_work_counts
            .values()
            .all(|count| *count == 0)
    );
    let bundle = decode_artifact_bundle(&std::fs::read(&bundle_path).unwrap()).unwrap();
    let admitted = admit_artifact_build_receipt(&plan, bundle, &receipt).unwrap();
    assert_eq!(admitted.payload_digest(), &Digest::sha256(payload));
    assert_eq!(admitted.import_receipt_digest(), None);

    let invalid_bundle = temp.path().join("invalid-target.tar");
    let invalid_manifest = temp.path().join("invalid-manifest.json");
    let invalid_receipt = temp.path().join("invalid-receipt.json");
    let invalid_observation = temp.path().join("invalid-observation.json");
    let mut invalid = serde_json::to_value(build_observation(37)).unwrap();
    invalid["construction_count"] = serde_json::json!(2);
    std::fs::write(&invalid_observation, canonical_bytes(&invalid).unwrap()).unwrap();
    let rejected = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "artifact-build",
            "--plan",
            plan_path.to_str().unwrap(),
            "--payload",
            payload_path.to_str().unwrap(),
            "--observation",
            invalid_observation.to_str().unwrap(),
            "--bundle-output",
            invalid_bundle.to_str().unwrap(),
            "--manifest-output",
            invalid_manifest.to_str().unwrap(),
            "--receipt-output",
            invalid_receipt.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(!rejected.success());
    assert!(!invalid_bundle.exists());
    assert!(!invalid_manifest.exists());
    assert!(!invalid_receipt.exists());
}

#[test]
fn artifact_import_commands_separate_extraction_from_actual_import_evidence() {
    let temp = tempfile::tempdir().unwrap();
    let build_plan = valid_plan(93);
    let payload = b"retained authoritative import payload";
    let bundle = bundle_for(&build_plan, payload.to_vec());
    let import_plan = artifact_import_plan(&build_plan, &bundle).unwrap();
    let plan_path = temp.path().join("import-plan.json");
    let bundle_path = temp.path().join("exact-target.tar");
    let payload_path = temp.path().join("imported-engine.oci.tar.zst");
    let manifest_path = temp.path().join("imported-manifest.json");
    let observation_path = temp.path().join("import-observation.json");
    let receipt_path = temp.path().join("import-receipt.json");
    std::fs::write(&plan_path, canonical_bytes(&import_plan).unwrap()).unwrap();
    std::fs::write(&bundle_path, bundle.bytes()).unwrap();

    let extracted = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "artifact-extract",
            "--plan",
            plan_path.to_str().unwrap(),
            "--bundle",
            bundle_path.to_str().unwrap(),
            "--payload-output",
            payload_path.to_str().unwrap(),
            "--manifest-output",
            manifest_path.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(extracted.success());
    assert_eq!(std::fs::read(&payload_path).unwrap(), payload);
    assert_eq!(
        decode_canonical::<ExactTargetArtifactManifest>(&std::fs::read(&manifest_path).unwrap())
            .unwrap(),
        *bundle.manifest()
    );

    std::fs::write(
        &observation_path,
        canonical_bytes(&import_observation(&bundle, 47)).unwrap(),
    )
    .unwrap();
    let imported = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "artifact-import",
            "--plan",
            plan_path.to_str().unwrap(),
            "--bundle",
            bundle_path.to_str().unwrap(),
            "--observation",
            observation_path.to_str().unwrap(),
            "--receipt-output",
            receipt_path.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(imported.success());
    let receipt: ArtifactImportReceipt =
        decode_canonical(&std::fs::read(&receipt_path).unwrap()).unwrap();
    assert_eq!(receipt.materialization_elapsed_millis.get(), 47);
    let admitted = admit_artifact_import_receipt(
        &import_plan,
        decode_artifact_bundle(bundle.bytes()).unwrap(),
        &receipt,
    )
    .unwrap();
    assert_eq!(
        admitted.import_receipt_digest(),
        Some(&receipt.receipt_digest)
    );

    let invalid_observation = temp.path().join("invalid-import-observation.json");
    let invalid_receipt = temp.path().join("invalid-import-receipt.json");
    let mut invalid = serde_json::to_value(import_observation(&bundle, 47)).unwrap();
    invalid["import_count"] = serde_json::json!(2);
    std::fs::write(&invalid_observation, canonical_bytes(&invalid).unwrap()).unwrap();
    let rejected = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "artifact-import",
            "--plan",
            plan_path.to_str().unwrap(),
            "--bundle",
            bundle_path.to_str().unwrap(),
            "--observation",
            invalid_observation.to_str().unwrap(),
            "--receipt-output",
            invalid_receipt.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(!rejected.success());
    assert!(!invalid_receipt.exists());
}

#[test]
fn packaged_dependency_descriptor_bytes_match_the_engine_contract() {
    let descriptor = valid_plan(23).rust_dependency;
    let expected = format!(
        "{{\"source\":\"git\",\"package\":\"dagger-sdk\",\"url\":\"https://github.com/iw/dagger\",\"revision\":\"{}\"}}",
        descriptor.revision.as_str()
    );
    assert_eq!(
        serde_json::to_vec(&descriptor).unwrap(),
        expected.as_bytes()
    );
    assert_eq!(
        descriptor.direct_digest().unwrap(),
        Digest::sha256(expected)
    );
}
