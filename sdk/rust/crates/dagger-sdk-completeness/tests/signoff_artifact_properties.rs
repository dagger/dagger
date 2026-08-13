//! Exact-target artifact identity, real-byte round-trip, and state-machine properties.

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

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
    let mut plan = ArtifactPlan {
        format_version: ArtifactFormatVersion::V1,
        target_descriptor_digest: TargetDigest::new(Digest::sha256([seed, 5])),
        target_revision: commit(seed.max(1)),
        subject: SubjectRevisionObservation {
            revision: commit(seed.wrapping_add(1).max(1)),
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
