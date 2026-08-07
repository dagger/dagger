//! Exact-target and filesystem-boundary regressions for the checked completeness baseline.

use std::fs;
use std::path::{Path, PathBuf};

use dagger_sdk_completeness::*;
use serde::Serialize;

const DAGGER_REVISION: &str = "25300124ca110612edc09c43f89cb5fad6028170";
const GO_REVISION: &str = "1309520660f6a5b35ef97b4fbe151e32a06a8dc5";
const HARNESS_REVISION: &str = "8c164424b7a8a37b33a77367ef7547490d5b87b5";
const CLI_DIGEST: &str = "sha256:e670234e6f8c0544e209423f8c42c8300e06cd9780921d19a9a22ef9e3890a40";
const GO_CLIENT_FEATURE2_DIGEST: &str =
    "sha256:bb1907e7d7990649c44e288388096b2a29eb613a3278615b24ac44ea95d965cf";
const RUST_ARTIFACT_DIGEST: &str =
    "sha256:1ad0923286d67e03f58b6ba4e2fb759e86616e9678a1a136f209680c7a8e78dd";

#[derive(Serialize)]
struct OwnershipProjection<'a> {
    capability_fingerprint: &'a Digest,
    capability_id: &'a CapabilityId,
    #[serde(skip_serializing_if = "CanonicalSet::is_empty")]
    decision_evidence: &'a CanonicalSet<EvidenceId>,
    #[serde(skip_serializing_if = "CanonicalSet::is_empty")]
    implementation_evidence: &'a CanonicalSet<EvidenceId>,
    source_anchors: &'a CanonicalSet<EvidenceReference>,
    status: &'a Status,
    #[serde(skip_serializing_if = "CanonicalSet::is_empty")]
    verification_evidence: &'a CanonicalSet<EvidenceId>,
}

#[test]
fn target_locks_authorities_harness_status_and_ownership() {
    let root = repository_root();
    assert_eq!(
        rust_artifact_digest(&root).unwrap().as_str(),
        RUST_ARTIFACT_DIGEST
    );
    let contract = root.join("sdk/rust/completeness");
    let target: TargetDescriptor = read_canonical(&contract.join("target.json"));
    assert_eq!(target.dagger_revision.as_str(), DAGGER_REVISION);
    assert_eq!(target.go_sdk_revision.as_str(), GO_REVISION);
    assert_eq!(target.sdk_contract_revision.as_str(), HARNESS_REVISION);
    assert_eq!(target.rust_version.to_string(), "1.97.1");
    assert_eq!(target.rust_edition, RustEdition::Edition2024);
    assert_eq!(target.engine_version.to_string(), "v1.0.0-beta.10");
    assert_eq!(target.schema_version.as_str(), "v1.0.0");
    assert_eq!(target.go_sdk_version_label, None);

    let schema: serde_json::Value =
        serde_json::from_slice(&fs::read(contract.join("snapshots/schema.json")).unwrap()).unwrap();
    let schema_names = schema["__schema"]["types"]
        .as_array()
        .unwrap()
        .iter()
        .filter_map(|schema_type| schema_type["name"].as_str())
        .collect::<std::collections::BTreeSet<_>>();
    // The authoritative EngineDev capture must never regress to cmd/codegen's synthetic fixture.
    assert!(schema_names.contains("WorkspaceSDK"));
    assert!(!schema_names.contains("Sub1"));
    assert!(!schema_names.contains("Sub2"));
    assert!(!schema_names.contains("Test"));

    let provenance: serde_json::Value = serde_json::from_slice(
        &fs::read(contract.join("sources/dagger-cli/v1.0.0-beta.9/provenance.json")).unwrap(),
    )
    .unwrap();
    assert_eq!(
        provenance["engine_linux_amd64_manifest_digest"],
        "sha256:df96f6801fea0f511b1c62e143461af7daa6074216d62ea8f092e035c4afaffd"
    );

    let engine_go = fs::read_to_string(root.join("core/sdk/go_sdk.go")).unwrap();
    assert!(engine_go.contains(&format!("goSDKLibVersion = \"{GO_REVISION}\"")));
    // The adjacent label is known to diverge; only the evaluated full commit literal is identity.
    assert!(engine_go.contains("// v0.21.7"));

    let mappings: HarnessMappings = read_canonical(&contract.join("harness-mappings.json"));
    assert_eq!(mappings.checks.len(), 18);
    assert_eq!(
        mappings
            .checks
            .values()
            .filter(|mapping| mapping.check_kind == HarnessCheckKind::SubjectConformance)
            .count(),
        17
    );
    let self_check = mappings
        .checks
        .get(&CheckId::new("init-module-renders-root-type").unwrap())
        .unwrap();
    assert_eq!(self_check.check_kind, HarnessCheckKind::HarnessSelf);
    assert!(self_check.capability_ids.is_empty());
    for mapping in mappings.checks.values() {
        assert_eq!(mapping.cli_artifact_digest.as_str(), CLI_DIGEST);
        assert_eq!(mapping.platform_scope, CanonicalSet::new([linux_amd64()]));
        assert_eq!(
            mapping.invocation.args,
            [
                "check".to_owned(),
                mapping.check_id.to_string(),
                "--no-generate".to_owned(),
            ]
        );
        assert!(!mapping.limitations.is_empty());
    }

    let derived = derive_contract(&root, true).unwrap();
    assert!(derived.report.integrity_verdict);
    assert!(!derived.report.completeness_verdict);
    assert_eq!(
        derived.report.inventory_digest.as_str(),
        "sha256:e90dfa2a0c383dbbc4003091d05f0e8a8f537fc80fb87672ce82a2d49d3e86a9"
    );
    assert_eq!(
        derived.report.ledger_digest.as_str(),
        "sha256:1dd7c47e32ee446326cdcf10c17ea51586f792160836878700618931af0e62c5"
    );
    assert_eq!(derived.report.blocking_capabilities.len(), 4_557);
    assert_eq!(derived.report.complete_exceptions.len(), 10);
    assert!(
        derived
            .report
            .complete_exceptions
            .iter()
            .all(|exception| exception.status == Status::IdiomaticEquivalent)
    );
    let go_client_projection = derived
        .ledger
        .capabilities
        .values()
        .filter(|row| row.authority_id.as_str() == "go-client")
        .map(|row| OwnershipProjection {
            capability_fingerprint: &row.capability_fingerprint,
            capability_id: &row.capability_id,
            decision_evidence: &row.decision_evidence,
            implementation_evidence: &row.implementation_evidence,
            source_anchors: &row.source_anchors,
            status: &row.status,
            verification_evidence: &row.verification_evidence,
        })
        .collect::<Vec<_>>();
    assert_eq!(go_client_projection.len(), 1_783);
    assert_eq!(
        canonical_digest(DigestDomain::Artifact, &go_client_projection)
            .unwrap()
            .as_str(),
        GO_CLIENT_FEATURE2_DIGEST
    );
    assert_eq!(
        derived.report.counts_by_authority,
        std::collections::BTreeMap::from([
            (AuthorityId::new("engine-schema").unwrap(), 1_567),
            (AuthorityId::new("go-client").unwrap(), 1_783),
            (AuthorityId::new("go-codegen").unwrap(), 83),
            (AuthorityId::new("go-engine-sdk").unwrap(), 13),
            (AuthorityId::new("go-integration-tests").unwrap(), 1_072),
            (AuthorityId::new("rust-policy").unwrap(), 47),
            (AuthorityId::new("sdk-contract-harness").unwrap(), 17),
        ])
    );
    assert_eq!(
        derived.report.counts_by_status,
        std::collections::BTreeMap::from([
            (Status::IdiomaticEquivalent, 10),
            (Status::Implemented, 15),
            (Status::Inapplicable, 0),
            (Status::Missing, 1_129),
            (Status::Partial, 3_428),
        ])
    );
    assert_eq!(
        derived.report.counts_by_owner,
        std::collections::BTreeMap::from([
            (FeatureId::Feature2, 13),
            (FeatureId::Feature3, 47),
            (FeatureId::Feature4, 3_329),
            (FeatureId::Feature5, 12),
            (FeatureId::Feature6, 53),
            (FeatureId::Feature7, 2),
            (FeatureId::Feature8, 1_081),
            (FeatureId::Feature9, 20),
        ])
    );
    let init_client = derived
        .ledger
        .capabilities
        .get(&CapabilityId::new("behavior/go-client/init-client-lifecycle").unwrap())
        .unwrap();
    assert_eq!(init_client.status, Status::Missing);
    assert_eq!(init_client.owner_feature, Some(FeatureId::Feature7));
    for owner in [
        FeatureId::Feature2,
        FeatureId::Feature3,
        FeatureId::Feature4,
        FeatureId::Feature5,
        FeatureId::Feature6,
        FeatureId::Feature7,
        FeatureId::Feature8,
        FeatureId::Feature9,
    ] {
        assert!(
            derived
                .report
                .counts_by_owner
                .get(&owner)
                .copied()
                .unwrap_or_default()
                > 0
        );
    }

    assert!(derived.source_items.items.values().any(|item| {
        item.item_kind.as_str() == "schema-argument"
            && item
                .semantic_signature
                .get("type")
                .and_then(|value| value.get("kind"))
                .and_then(serde_json::Value::as_str)
                == Some("NON_NULL")
            && item
                .semantic_signature
                .get("type")
                .and_then(|value| value.get("ofType"))
                .is_some_and(|value| !value.is_null())
    }));
    assert!(CommitSha::new("main").is_err());
    assert!(RepositoryRelativePath::new("../schema.json").is_err());

    let compatibility: CompatibilityClaim = read_canonical(&contract.join("compatibility.json"));
    let target_digest = TargetDigest::new(canonical_digest(DigestDomain::Target, &target).unwrap());
    assert_eq!(
        compatibility.supported_targets,
        SupportedTargets::Exact(CanonicalSet::new([target_digest.clone()]))
    );
    assert_eq!(
        compatibility.range_boundaries,
        CanonicalSet::new([target_digest])
    );
}

#[test]
fn verification_and_render_are_root_independent_and_byte_exact() {
    let original = repository_root();
    let fixture = tempfile::tempdir().unwrap();
    materialize_contract_fixture(&original, fixture.path());

    let original_derived = derive_contract(&original, true).unwrap();
    let fixture_derived = derive_contract(fixture.path(), true).unwrap();
    assert_eq!(original_derived, fixture_derived);

    for (index, root) in [original.as_path(), fixture.path()].into_iter().enumerate() {
        let output_parent = tempfile::tempdir().unwrap();
        let output = output_parent.path().join(format!("render-{index}"));
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        let status = run_with_backend(
            [
                "dagger-sdk-completeness",
                "render",
                "--root",
                root.to_str().unwrap(),
                "--output",
                output.to_str().unwrap(),
            ],
            &ContractCliBackend,
            &mut stdout,
            &mut stderr,
        );
        assert_eq!(status, 0, "{}", String::from_utf8_lossy(&stderr));
        for artifact in [
            "source-items.json",
            "inventory.json",
            "ledger.json",
            "report.json",
            "report.md",
            "release-compatibility.json",
        ] {
            assert_eq!(
                fs::read(output.join("artifacts").join(artifact)).unwrap(),
                fs::read(root.join("sdk/rust/completeness/artifacts").join(artifact)).unwrap(),
                "rendered artifact {artifact} drifted",
            );
        }
    }
}

fn materialize_contract_fixture(source: &Path, destination: &Path) {
    copy_tree(
        &source.join("sdk/rust/completeness"),
        &destination.join("sdk/rust/completeness"),
    );
    for file in [
        "sdk/rust/Cargo.toml",
        "sdk/rust/Cargo.lock",
        "sdk/rust/rust-toolchain.toml",
        "sdk/rust/AGENTS.md",
        "internal/version/VERSION",
        ".kiro/specs/rust-sdk-client-lifecycle/design.md",
    ] {
        copy_file(&source.join(file), &destination.join(file));
    }
    for directory in [
        "sdk/rust/crates/dagger-sdk",
        "sdk/rust/crates/dagger-codegen",
        "sdk/rust/crates/dagger-bootstrap",
    ] {
        copy_tree(&source.join(directory), &destination.join(directory));
    }

    let authorities: AuthorityRegistry =
        read_canonical(&source.join("sdk/rust/completeness/authorities.json"));
    for authority in authorities.authorities.values() {
        let (source_root, destination_root) = match authority.authority_class {
            AuthorityClass::GoClient => (source.join("sdk/go"), destination.join("sdk/go")),
            AuthorityClass::SdkContractHarness => continue,
            _ => (source.to_path_buf(), destination.to_path_buf()),
        };
        for selector in authority.include.iter() {
            let path = match selector {
                SourceSelector::Path(selector) => &selector.path,
                SourceSelector::Symbol(selector) => &selector.path,
            };
            let from = source_root.join(path.as_str());
            let to = destination_root.join(path.as_str());
            if !to.exists() {
                copy_file(&from, &to);
            }
        }
    }
}

fn copy_tree(source: &Path, destination: &Path) {
    for entry in walk(source) {
        let relative = entry.strip_prefix(source).unwrap();
        copy_file(&entry, &destination.join(relative));
    }
}

fn walk(root: &Path) -> Vec<PathBuf> {
    let mut files = Vec::new();
    let mut pending = vec![root.to_path_buf()];
    while let Some(directory) = pending.pop() {
        let mut entries = fs::read_dir(directory)
            .unwrap()
            .collect::<Result<Vec<_>, _>>()
            .unwrap();
        entries.sort_by_key(fs::DirEntry::file_name);
        for entry in entries {
            if entry.file_type().unwrap().is_dir() {
                pending.push(entry.path());
            } else {
                files.push(entry.path());
            }
        }
    }
    files.sort();
    files
}

fn copy_file(source: &Path, destination: &Path) {
    fs::create_dir_all(destination.parent().unwrap()).unwrap();
    if fs::hard_link(source, destination).is_err() {
        fs::copy(source, destination).unwrap();
    }
}

fn read_canonical<T>(path: &Path) -> T
where
    T: serde::de::DeserializeOwned + serde::Serialize,
{
    decode_canonical(&fs::read(path).unwrap()).unwrap()
}

fn repository_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .ancestors()
        .nth(4)
        .unwrap()
        .to_path_buf()
}

fn linux_amd64() -> Platform {
    Platform {
        operating_system: OperatingSystem::Linux,
        architecture: Architecture::Amd64,
    }
}
