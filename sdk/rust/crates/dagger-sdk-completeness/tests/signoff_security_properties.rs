//! Security admission properties over dependency, provenance, finding, and secret mutations.

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

use dagger_sdk_completeness::*;

#[path = "support/packaged_artifact.rs"]
mod packaged_artifact;
use proptest::prelude::*;

fn text(value: &str) -> NonEmptyText {
    NonEmptyText::new(value).unwrap()
}

fn relative(value: &str) -> RepositoryRelativePath {
    RepositoryRelativePath::new(value).unwrap()
}

fn valid_dependency_observation() -> RustDependencySecurityObservation {
    let roots = required_cargo_roots();
    RustDependencySecurityObservation {
        format_version: ConformanceFormatVersion::V1,
        cargo_roots: roots.clone().into_inner(),
        committed_lockfiles: required_committed_lockfiles(),
        locked_roots: CanonicalSet::new(roots.iter().map(|root| root.manifest.clone())),
        cargo_deny_classes: CanonicalSet::new([
            CargoDenyClass::Advisories,
            CargoDenyClass::Licenses,
            CargoDenyClass::Bans,
            CargoDenyClass::Sources,
        ]),
        reachable_advisories: CanonicalSet::default(),
        unapproved_licenses: CanonicalSet::default(),
        unapproved_wildcards: CanonicalSet::default(),
        unknown_sources: CanonicalSet::default(),
        workspace_unsafe_denied: true,
        unsafe_exceptions: Vec::new(),
        automated_cargo_roots: CanonicalSet::new(roots.iter().map(|root| root.manifest.clone())),
        inapplicable_automation: CanonicalSet::default(),
        packaged_dependencies_immutable: true,
        workflow_permissions: BTreeMap::from([(text("contents"), WorkflowPermissionLevel::Read)]),
    }
}

fn record(role: ExternalInputRole) -> ProvenanceRecord {
    let (id, publisher, repository, immutable_digest) = match role {
        ExternalInputRole::ArtifactBuilderImage => (
            "image/rust/1.97.1-bookworm",
            "docker-official-images",
            "github.com/docker-library/rust",
            "sha256:705e294093973d7c10e83400393dce7b3611f8e03e55a80af7fff6d02ae1affb",
        ),
        ExternalInputRole::EngineBaseImage => (
            "image/dagger-engine/beta.9",
            "dagger",
            "github.com/dagger/dagger",
            "sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e",
        ),
        ExternalInputRole::RustToolchain => (
            "toolchain/rust/1.97.1",
            "rust-project",
            "github.com/rust-lang/rust",
            "sha256:aaee35fb21cd459b58e34a10e4ca2dbf2fe2484162e3ab14c7aa32ee9420c55e",
        ),
        ExternalInputRole::GoToolchain => (
            "image/golang/1.26.1-bookworm",
            "docker-official-images",
            "github.com/docker-library/golang",
            "sha256:ab3d6955bbc813a0f3fdf220c1d817dd89c0b3f283777db8ece4a32fe7858edd",
        ),
        ExternalInputRole::PreflightCli => (
            "binary/preflight/d40f9c27",
            "dagger-rust-sdk-maintainers",
            "github.com/dagger/dagger",
            "sha256:d40f9c27e780321fcd0aaa59dde74ad0a7b851caf7378d9026df3ea7ed6f5ed6",
        ),
        ExternalInputRole::PreflightEngine => (
            "image/preflight-engine/beta.9",
            "dagger",
            "github.com/dagger/dagger",
            "sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e",
        ),
        ExternalInputRole::CliArchive => (
            "archive/dagger-cli/beta.9/linux-amd64",
            "dagger",
            "github.com/dagger/dagger",
            "sha256:776a390ecef59ff2ad8c0a3b3ca6d793bb62556bb8a512f475a725bdc830e40c",
        ),
        ExternalInputRole::ScannerImage => (
            "image/trivy/0.69.3",
            "aqua-security",
            "github.com/aquasecurity/trivy",
            "sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c",
        ),
        ExternalInputRole::VulnerabilityDatabaseSource => (
            "image/trivy-db/sha256-10a3832219beaf45a3eb86065e30b39e528ae9c1650aa5f733d4666afd0712c5",
            "aqua-security",
            "github.com/aquasecurity/trivy-db",
            "sha256:f9083665f64bbcc8111ef4d185a712c6524e129f9213e39a91069f262bb01e1d",
        ),
    };
    ProvenanceRecord {
        id: ProvenanceId::new(id).unwrap(),
        role,
        publisher: text(publisher),
        repository: RepositoryId::new(repository).unwrap(),
        immutable_digest: Digest::new(immutable_digest).unwrap(),
        review_evidence_digest: Digest::sha256(format!("reviewed {id}")),
    }
}

fn provenance_input() -> ExternalProvenanceRegistryInput {
    ExternalProvenanceRegistryInput {
        format_version: ConformanceFormatVersion::V1,
        records: required_external_input_roles()
            .into_iter()
            .map(record)
            .collect(),
    }
}

#[test]
fn checked_preflight_cli_review_binds_the_reviewed_provenance_record() {
    let review = fs::read(
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../completeness/evidence/preflight-cli-review.json"),
    )
    .unwrap();
    let registry =
        compile_external_provenance_registry(reviewed_external_provenance_input()).unwrap();
    let preflight = registry
        .records
        .get(&ExternalInputRole::PreflightCli)
        .unwrap();
    assert_eq!(preflight.review_evidence_digest, Digest::sha256(review));
}

fn finding(payload: &Digest) -> VulnerabilityFinding {
    VulnerabilityFinding {
        finding_id: FindingId::new("cve-2099-0001").unwrap(),
        package: text("fixture-package"),
        installed_version: text("1.0.0"),
        severity: VulnerabilitySeverity::High,
        artifact_payload_digest: payload.clone(),
    }
}

fn exception() -> SecurityException {
    SecurityException {
        finding_id: FindingId::new("cve-2099-0001").unwrap(),
        reachability_digest: Digest::sha256("reachability review"),
        impact_digest: Digest::sha256("impact review"),
        owner: ProvenanceId::new("team/rust-sdk-security").unwrap(),
        upstream_remediation_digest: Digest::sha256("upstream remediation"),
        expiry: ExpiryPredicate::FixedDate {
            expires_on: UtcDate::new("2099-01-01").unwrap(),
        },
    }
}

fn exception_context() -> ExceptionEvaluationContext {
    ExceptionEvaluationContext {
        current_date: UtcDate::new("2026-08-13").unwrap(),
        target_revision: CommitSha::new("25300124ca110612edc09c43f89cb5fad6028170").unwrap(),
        fixed_versions: BTreeMap::new(),
        withdrawn_advisories: CanonicalSet::default(),
    }
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(100))]

    #[test]
    fn property_18_rust_dependency_security_locked_complete_least_privileged(
        mutation in 0_u8..13,
        index in any::<usize>(),
    ) {
        let mut observation = valid_dependency_observation();
        let expected = mutation == 0;
        match mutation {
            0 => observation.cargo_roots.reverse(),
            1 => { observation.cargo_roots.remove(index % observation.cargo_roots.len()); }
            2 => observation.cargo_roots.push(observation.cargo_roots[0].clone()),
            3 => { observation.locked_roots = CanonicalSet::default(); }
            4 => { observation.cargo_deny_classes = CanonicalSet::new([CargoDenyClass::Advisories]); }
            5 => { observation.reachable_advisories = CanonicalSet::new([FindingId::new("rustsec-0001").unwrap()]); }
            6 => { observation.unapproved_licenses = CanonicalSet::new([text("unapproved")]); }
            7 => { observation.unapproved_wildcards = CanonicalSet::new([relative("sdk/rust/Cargo.toml")]); }
            8 => { observation.unknown_sources = CanonicalSet::new([text("registry.example")]); }
            9 => observation.workspace_unsafe_denied = false,
            10 => { observation.automated_cargo_roots = CanonicalSet::default(); }
            11 => observation.packaged_dependencies_immutable = false,
            12 => { observation.workflow_permissions.insert(text("pull-requests"), WorkflowPermissionLevel::Write); }
            _ => unreachable!(),
        }
        prop_assert_eq!(admit_rust_dependency_security(observation).is_ok(), expected);
    }
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn property_19_external_provenance_exact_payload_vulnerability_fail_closed(
        provenance_mutation in 0_u8..4,
        finding_mutation in 0_u8..10,
    ) {
        let mut input = provenance_input();
        let provenance_expected = provenance_mutation < 2;
        match provenance_mutation {
            0 => {}
            1 => input.records.reverse(),
            2 => { input.records.pop(); }
            3 => input.records[0].publisher = text("unknown-publisher"),
            _ => unreachable!(),
        }
        let registry = compile_external_provenance_registry(input);
        prop_assert_eq!(registry.is_ok(), provenance_expected);
        let Ok(registry) = registry else { return Ok(()); };

        let payload = Digest::sha256("exact OCI payload");
        let mut findings = vec![finding(&payload)];
        let mut exceptions = vec![exception()];
        let mut scanner = record(ExternalInputRole::ScannerImage).id;
        let mut database = record(ExternalInputRole::VulnerabilityDatabaseSource).immutable_digest;
        let mut rebuilt = false;
        let mut context = exception_context();
        let finding_expected = finding_mutation < 3;
        match finding_mutation {
            0 => {}
            1 => { findings[0].severity = VulnerabilitySeverity::Low; exceptions.clear(); }
            2 => findings.reverse(),
            3 => findings[0].artifact_payload_digest = Digest::sha256("other payload"),
            4 => rebuilt = true,
            5 => scanner = ProvenanceId::new("image/other-scanner").unwrap(),
            6 => database = Digest::sha256([]),
            7 => exceptions.clear(),
            8 => context.current_date = UtcDate::new("2099-01-01").unwrap(),
            9 => findings.push(findings[0].clone()),
            _ => unreachable!(),
        }
        let result = admit_vulnerability_findings(
            payload, scanner, database, &registry, findings, exceptions, &context, rebuilt,
        );
        prop_assert_eq!(result.is_ok(), finding_expected);
    }

    #[test]
    fn property_20_canaries_and_host_identity_never_persist(
        seed in any::<u8>(),
        mutation in 0_u8..7,
        chunk_width in 1_usize..24,
    ) {
        let entropy = (0_u8..32).map(|byte| byte ^ seed).collect::<Vec<_>>();
        let canaries = secret_canary_set_from_entropy(&entropy).unwrap();
        let identity = b"personal-host-identity".to_vec();
        let identities = SensitiveIdentitySet::new([identity.clone()]).unwrap();
        let mut output = b"bounded safe diagnostic".to_vec();
        match mutation {
            0 | 6 => {}
            1 => canaries.visit(|category, value| {
                if category == SecretCanaryCategory::Session {
                    output.extend_from_slice(value);
                }
            }),
            2 => output.extend_from_slice(b"/Users/operator/work"),
            3 => output.extend_from_slice(&identity),
            4 => output.extend_from_slice(b"\x1b[31m"),
            5 => output.extend_from_slice(b"token=live-credential"),
            _ => unreachable!(),
        }
        let independent_canary_match = {
            let mut matched = false;
            canaries.visit(|_, value| matched |= output.windows(value.len()).any(|window| window == value));
            matched
        };
        let chunks = output.chunks(chunk_width).collect::<Vec<_>>();
        let inspected = inspect_canary_chunks(
            &canaries,
            SecretInspectionDomain::ProcessOutput,
            relative("evidence/process-output"),
            chunks,
        ).unwrap();
        prop_assert_eq!(!inspected.leaks.is_empty(), independent_canary_match);

        let sanitized = sanitize_durable_evidence(&output, &canaries, &identities);
        let expected_safe = matches!(mutation, 0 | 6);
        prop_assert_eq!(sanitized.is_ok(), expected_safe);
        if let Ok(sanitized) = sanitized {
            let inspections = required_secret_inspection_domains().iter().map(|domain| {
                if *domain == SecretInspectionDomain::ProcessOutput {
                    inspected.clone()
                } else {
                    SecretInspectionObservation {
                        domain: *domain,
                        inspected_bytes: 1,
                        leaks: CanonicalSet::default(),
                    }
                }
            }).collect();
            let report = admit_secret_evidence(SecretEvidenceInput {
                canary_set_digest: canaries.digest().clone(),
                inspections,
                sanitized_outputs: vec![sanitized],
                packaged_artifacts: packaged_artifact::packaged_artifact_scan_bundle(),
                artifact_credentials_absent: true,
                verdict_credentials_absent: true,
                redaction_proven: true,
            });
            prop_assert!(report.is_ok());
        }
    }
}

#[test]
fn trivy_provenance_is_aqua_reviewed_and_digest_pinned() {
    let registry = compile_external_provenance_registry(provenance_input()).unwrap();
    let scanner = &registry.records[&ExternalInputRole::ScannerImage];
    assert_eq!(scanner.publisher.as_str(), "aqua-security");
    assert_eq!(scanner.repository.as_str(), "github.com/aquasecurity/trivy");
    assert_eq!(
        scanner.immutable_digest.as_str(),
        "sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c"
    );
}

#[test]
fn every_exception_expiry_variant_fails_at_its_boundary() {
    let revision = CommitSha::new("25300124ca110612edc09c43f89cb5fad6028170").unwrap();
    let finding_id = FindingId::new("cve-2099-0001").unwrap();
    let mut context = exception_context();
    let predicates = [
        ExpiryPredicate::FixedDate {
            expires_on: context.current_date,
        },
        ExpiryPredicate::TargetRevision {
            reviewed_revision: CommitSha::new("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa").unwrap(),
        },
        ExpiryPredicate::PatchedVersion {
            package: text("fixture-package"),
            patched_version: SemverVersion::new("2.0.0").unwrap(),
        },
        ExpiryPredicate::AdvisoryWithdrawal {
            advisory: finding_id.clone(),
        },
    ];
    context.fixed_versions.insert(
        text("fixture-package"),
        SemverVersion::new("2.0.0").unwrap(),
    );
    context.withdrawn_advisories = CanonicalSet::new([finding_id]);
    assert_eq!(context.target_revision, revision);
    for predicate in predicates {
        let mut security_exception = exception();
        security_exception.expiry = predicate;
        let payload = Digest::sha256("exact OCI payload");
        let registry = compile_external_provenance_registry(provenance_input()).unwrap();
        assert!(
            admit_vulnerability_findings(
                payload.clone(),
                record(ExternalInputRole::ScannerImage).id,
                Digest::sha256("database"),
                &registry,
                vec![finding(&payload)],
                vec![security_exception],
                &context,
                false,
            )
            .is_err()
        );
    }
}
