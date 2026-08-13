//! Engine-free exact-artifact scanner fixtures and security-report admission.

use std::collections::BTreeMap;

use dagger_sdk_completeness::*;

fn text(value: &str) -> NonEmptyText {
    NonEmptyText::new(value).unwrap()
}

fn fixture_payload() -> Vec<u8> {
    decode_base64(include_str!("fixtures/small-oci-canary.tar.base64"))
}

fn scanner_observation() -> ArtifactScannerObservation {
    decode_artifact_scanner_observation(include_bytes!(
        "fixtures/artifact-scanner-observation.json"
    ))
    .unwrap()
}

fn admitted_artifact() -> AdmittedArtifact {
    let provenance = ProvenanceId::new("artifact/exact-target/fixture").unwrap();
    let components = required_artifact_components()
        .into_iter()
        .map(|component| {
            (
                component,
                ArtifactComponentRecord {
                    component,
                    input_digest: Digest::sha256(format!("{component:?} input")),
                    content_digest: Digest::sha256(format!("{component:?} content")),
                    provenance: CanonicalSet::new([provenance.clone()]),
                },
            )
        })
        .collect::<BTreeMap<_, _>>();
    let toolchain_digests = required_artifact_toolchains()
        .into_iter()
        .map(|role| (role, Digest::sha256(format!("{role:?}"))))
        .collect::<BTreeMap<_, _>>();
    let provenance_document = ArtifactProvenanceDocument {
        format_version: ArtifactFormatVersion::V1,
        components: components
            .iter()
            .map(|(component, record)| (*component, record.provenance.clone()))
            .collect(),
        toolchain_digests: toolchain_digests.clone(),
    };
    let provenance_digest =
        canonical_digest(DigestDomain::ConformanceSecurity, &provenance_document).unwrap();
    let source_digest = Digest::sha256("focused fixture source");
    let plan = ArtifactPlan {
        format_version: ArtifactFormatVersion::V1,
        target_descriptor_digest: TargetDigest::new(Digest::sha256("target descriptor")),
        target_revision: CommitSha::new("25300124ca110612edc09c43f89cb5fad6028170").unwrap(),
        subject: SubjectRevisionObservation {
            revision: CommitSha::new("3cc8c7ad80eebc530f00959e856d49fb7aba8992").unwrap(),
            focused_source_digest: source_digest.clone(),
            workspace_focused_source_digest: source_digest,
            reachable: true,
            clean: true,
            immutable: true,
        },
        platform: PlatformDescriptor::linux_amd64(),
        engine_input_digest: Digest::sha256("engine input"),
        cli_input_digest: Digest::sha256("cli input"),
        go_runtime_digest: Digest::sha256("go runtime"),
        rust_manifest_digest: Digest::sha256("rust manifest"),
        rust_descriptor_digest: Digest::sha256("rust descriptor"),
        toolchain_digests,
        components,
        provenance_digest,
        materialization: ArtifactMaterialization::Build,
    };
    let payload = fixture_payload();
    let manifest = artifact_manifest_for_payload(&plan, &payload).unwrap();
    let bundle = assemble_artifact_bundle(manifest.clone(), provenance_document, payload).unwrap();
    let component_builds = required_artifact_components()
        .into_iter()
        .map(|component| (component, 1))
        .collect::<BTreeMap<_, _>>();
    let mut events = vec![ArtifactEvent::ConstructionStarted];
    events.extend(
        component_builds
            .keys()
            .copied()
            .map(|component| ArtifactEvent::ComponentBuilt { component }),
    );
    events.extend([
        ArtifactEvent::PayloadExported,
        ArtifactEvent::ManifestVerified,
        ArtifactEvent::PayloadVerified,
        ArtifactEvent::ComponentsVerified,
        ArtifactEvent::ArtifactReady,
    ]);
    admit_artifact(
        &plan,
        ArtifactObservation {
            strategy: ArtifactMaterialization::Build,
            verified_component_digests: manifest
                .components
                .iter()
                .map(|(component, record)| (*component, record.content_digest.clone()))
                .collect(),
            manifest,
            bundle,
            events,
            counters: ArtifactCounters {
                construction: 1,
                imports: 0,
                component_builds,
                forbidden_work: CanonicalSet::default(),
            },
            elapsed_millis: 1,
        },
    )
    .unwrap()
}

fn rust_security() -> RustDependencySecurityReport {
    let roots = required_cargo_roots();
    admit_rust_dependency_security(RustDependencySecurityObservation {
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
    })
    .unwrap()
}

fn secret_report() -> SecretEvidenceReport {
    admit_secret_evidence(SecretEvidenceInput {
        canary_set_digest: Digest::sha256("ephemeral canary set"),
        inspections: required_secret_inspection_domains()
            .into_iter()
            .map(|domain| SecretInspectionObservation {
                domain,
                inspected_bytes: 1,
                leaks: CanonicalSet::default(),
            })
            .collect(),
        sanitized_outputs: vec![SanitizedEvidence {
            digest: Digest::sha256("sanitized scanner output"),
            byte_count: 32,
        }],
        artifact_credentials_absent: true,
        verdict_credentials_absent: true,
        redaction_proven: true,
    })
    .unwrap()
}

fn exception_context() -> ExceptionEvaluationContext {
    ExceptionEvaluationContext {
        current_date: UtcDate::new("2026-08-13").unwrap(),
        target_revision: CommitSha::new("25300124ca110612edc09c43f89cb5fad6028170").unwrap(),
        fixed_versions: BTreeMap::new(),
        withdrawn_advisories: CanonicalSet::default(),
    }
}

#[test]
fn checked_scanner_fixture_is_canonical_and_bound_to_the_real_archive_bytes() {
    let payload = fixture_payload();
    let scanner = scanner_observation();
    assert_eq!(payload.len(), 2012);
    assert_eq!(scanner.payload_digest, Digest::sha256(&payload));
    assert_eq!(
        canonical_bytes(&scanner).unwrap(),
        include_bytes!("fixtures/artifact-scanner-observation.json")
    );
}

#[test]
fn scanner_translation_preserves_findings_and_rejects_rebuild_or_identity_drift() {
    let artifact = admitted_artifact();
    let registry =
        compile_external_provenance_registry(reviewed_external_provenance_input()).unwrap();
    let ordinary = rust_security();
    let valid = ArtifactSecurityObservation {
        scanner: scanner_observation(),
        exceptions: Vec::new(),
        secret_report: secret_report(),
        policy_elapsed: NonZeroMillis::new(2).unwrap(),
    };
    let report = admit_artifact_security(
        &artifact,
        &ordinary,
        &registry,
        &exception_context(),
        valid.clone(),
    )
    .unwrap();
    assert_eq!(report.vulnerability.findings.len(), 1);
    assert_eq!(
        report.artifact_payload_digest,
        scanner_observation().payload_digest
    );

    let mut mutations = Vec::new();
    let mut payload = valid.clone();
    payload.scanner.payload_digest = Digest::sha256("different archive");
    mutations.push(payload);
    let mut scanner = valid.clone();
    scanner.scanner.scanner_image_digest = Digest::sha256("different scanner");
    mutations.push(scanner);
    let mut database = valid.clone();
    database.scanner.database_provenance = ProvenanceId::new("source/other-db").unwrap();
    mutations.push(database);
    let mut rebuilt = valid.clone();
    rebuilt.scanner.target_build_count = 1;
    mutations.push(rebuilt);
    let mut source_scan = valid;
    source_scan.scanner.source_scan_count = 1;
    mutations.push(source_scan);
    for mutation in mutations {
        assert!(
            admit_artifact_security(
                &artifact,
                &ordinary,
                &registry,
                &exception_context(),
                mutation,
            )
            .is_err()
        );
    }
}

#[test]
fn malformed_severity_and_canary_leak_fail_before_report_assembly() {
    let hostile =
        String::from_utf8(include_bytes!("fixtures/artifact-scanner-observation.json").to_vec())
            .unwrap()
            .replace("\"low\"", "\"catastrophic\"");
    assert!(decode_artifact_scanner_observation(hostile.as_bytes()).is_err());

    let canary = SecretLeakObservation {
        category: SecretCanaryCategory::Session,
        domain: SecretInspectionDomain::ProcessOutput,
        coordinate: RepositoryRelativePath::new("scanner/output").unwrap(),
    };
    assert!(
        admit_secret_evidence(SecretEvidenceInput {
            canary_set_digest: Digest::sha256("canaries"),
            inspections: required_secret_inspection_domains()
                .into_iter()
                .map(|domain| SecretInspectionObservation {
                    domain,
                    inspected_bytes: 1,
                    leaks: if domain == SecretInspectionDomain::ProcessOutput {
                        CanonicalSet::new([canary.clone()])
                    } else {
                        CanonicalSet::default()
                    },
                })
                .collect(),
            sanitized_outputs: vec![SanitizedEvidence {
                digest: Digest::sha256("output"),
                byte_count: 1,
            }],
            artifact_credentials_absent: true,
            verdict_credentials_absent: true,
            redaction_proven: true,
        })
        .is_err()
    );
}

#[test]
fn exact_artifact_scanner_source_consumes_one_file_and_has_no_builder_fallback() {
    let source = include_str!("../../../../../toolchains/security/main.dang");
    let start = source.find("pub scanExactArtifact").unwrap();
    let function = &source[start..];
    assert_eq!(function.matches(".withMountedFile(").count(), 1);
    assert_eq!(function.matches("trivy image").count(), 1);
    assert_eq!(
        function
            .matches("--input=/artifact/engine.oci.tar.zst")
            .count(),
        1
    );
    assert!(!function.contains("engineDev"));
    assert!(!function.contains(".asTarball"));
    assert!(!function.contains("scanSource"));
    assert_eq!(
        source
            .matches("aquasec/trivy:0.69.3@sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c")
            .count(),
        1
    );
}

fn decode_base64(input: &str) -> Vec<u8> {
    let mut output = Vec::new();
    let mut quartet = Vec::with_capacity(4);
    for byte in input.bytes().filter(|byte| !byte.is_ascii_whitespace()) {
        quartet.push(byte);
        if quartet.len() != 4 {
            continue;
        }
        let values = quartet
            .iter()
            .map(|byte| match byte {
                b'A'..=b'Z' => byte - b'A',
                b'a'..=b'z' => byte - b'a' + 26,
                b'0'..=b'9' => byte - b'0' + 52,
                b'+' => 62,
                b'/' => 63,
                b'=' => 0,
                _ => panic!("checked fixture contains invalid base64"),
            })
            .collect::<Vec<_>>();
        output.push(values[0] << 2 | values[1] >> 4);
        if quartet[2] != b'=' {
            output.push(values[1] << 4 | values[2] >> 2);
        }
        if quartet[3] != b'=' {
            output.push(values[2] << 6 | values[3]);
        }
        quartet.clear();
    }
    assert!(quartet.is_empty());
    output
}
