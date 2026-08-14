//! Source-bound checks for dependency automation, workflow privilege, and pinned provenance.

use std::path::{Path, PathBuf};

use dagger_sdk_completeness::*;

#[derive(serde::Deserialize, serde::Serialize)]
#[serde(deny_unknown_fields)]
struct TrivyDatabaseReview {
    artifact: TrivyArtifactReview,
    database: TrivyDatabaseContentReview,
    format_version: String,
    materializations: Vec<TrivyMaterializationReview>,
    review_kind: String,
    source: TrivySourceReview,
}

#[derive(serde::Deserialize, serde::Serialize)]
#[serde(deny_unknown_fields)]
struct TrivyArtifactReview {
    artifact_type: String,
    config: TrivyDescriptorReview,
    layer: TrivyDescriptorReview,
    manifest_digest: Digest,
    manifest_media_type: String,
}

#[derive(serde::Deserialize, serde::Serialize)]
#[serde(deny_unknown_fields)]
struct TrivyDescriptorReview {
    digest: Digest,
    media_type: String,
    size_bytes: u64,
}

#[derive(serde::Deserialize, serde::Serialize)]
#[serde(deny_unknown_fields)]
struct TrivyDatabaseContentReview {
    content_digest: Digest,
    size_bytes: u64,
}

#[derive(serde::Deserialize, serde::Serialize)]
#[serde(deny_unknown_fields)]
struct TrivyMaterializationReview {
    database_content_digest: Digest,
    metadata_digest: Digest,
}

#[derive(serde::Deserialize, serde::Serialize)]
#[serde(deny_unknown_fields)]
struct TrivySourceReview {
    repository: String,
}

fn repository_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../..")
        .components()
        .collect()
}

fn read(relative: &str) -> String {
    std::fs::read_to_string(repository_root().join(relative)).unwrap()
}

#[test]
fn dependency_automation_covers_only_the_real_rust_roots() {
    let dependabot = read(".github/dependabot.yml");
    for root in [
        "/sdk/rust",
        "/sdk/rust/examples/backend",
        "/sdk/rust/examples/cli",
        "/sdk/rust/examples/frontend",
    ] {
        assert!(dependabot.contains(&format!("      - \"{root}\"")));
    }
    assert!(!dependabot.contains("package-ecosystem: \"npm\"\n    directory: \"/sdk/rust\"",));
    for lockfile in required_committed_lockfiles().iter() {
        assert!(repository_root().join(lockfile.as_str()).is_file());
    }
}

#[test]
fn rust_workflows_are_locked_engine_free_and_least_privileged() {
    let security = read(".github/workflows/rust-sdk-security.yml");
    let platform = read(".github/workflows/rust-sdk-platform.yml");
    let windows_preflight = read(".github/workflows/rust-sdk-windows-preflight.yml");
    let security_preflight = read("sdk/rust/scripts/ci-security-preflight.sh");
    let platform_preflight = read("sdk/rust/scripts/ci-platform-preflight.sh");
    for workflow in [&security, &platform] {
        assert!(workflow.contains("permissions:\n  contents: read"));
    }
    assert!(windows_preflight.contains("permissions:\n  actions: read\n  contents: read"));
    for workflow in [&security, &platform, &windows_preflight] {
        assert!(!workflow.contains("contents: write"));
        assert!(!workflow.contains("id-token: write"));
        assert_eq!(
            workflow.matches("uses: actions/checkout@").count(),
            workflow.matches("persist-credentials: false").count(),
        );
    }
    for root in [
        "examples/backend/Cargo.toml",
        "examples/cli/Cargo.toml",
        "examples/frontend/Cargo.toml",
    ] {
        assert!(security_preflight.contains(&format!("--manifest-path {root}")));
    }
    for phase in ["dependencies", "source-policy", "runtime", "packages"] {
        assert!(security.contains(&format!("./scripts/ci-security-preflight.sh {phase}")));
    }
    assert!(platform.contains("./scripts/ci-platform-preflight.sh"));
    for workflow in [&platform, &windows_preflight] {
        for line in workflow.lines() {
            let command = line
                .trim_start()
                .strip_prefix("run: ")
                .unwrap_or_else(|| line.trim_start());
            for forbidden in ["docker ", "dagger call ", "dagger develop "] {
                assert!(!command.to_ascii_lowercase().starts_with(forbidden));
            }
        }
        assert!(!workflow.to_ascii_lowercase().contains("sdk/go"));
    }
    for script in [&security_preflight, &platform_preflight] {
        for forbidden in ["docker ", "dagger call ", "dagger develop "] {
            assert!(!script.to_ascii_lowercase().contains(forbidden));
        }
    }
    for workflow in [&platform, &windows_preflight] {
        assert!(!workflow.contains("actions/upload-artifact@v"));
        assert!(!workflow.contains("actions/download-artifact@v"));
    }
    assert!(platform.contains("actions/upload-artifact@"));
    assert!(platform.contains("actions/download-artifact@"));
    assert!(platform.contains("name: Rust SDK Supported Platforms"));
    assert!(platform.contains("runner: ubuntu-24.04"));
    assert!(platform.contains("runner: macos-15"));
    assert!(!platform.contains("runner: windows-2025"));
    assert!(platform.contains("dagger-rust-sdk-platform aggregate-supported"));
    assert!(platform.contains("Current SDK sign-off platform: \\`yes\\`"));
    assert!(platform.contains("Windows support claimed"));
    assert!(!windows_preflight.contains("pull_request:"));
    assert!(!windows_preflight.contains("push:"));
    assert!(windows_preflight.contains("actions/upload-artifact@"));
    assert!(!windows_preflight.contains("actions/download-artifact@"));
    assert!(windows_preflight.contains("runs-on: windows-2025"));
    assert!(windows_preflight.contains("Fetch the exact public revision without credentials"));
    assert!(windows_preflight.contains("Current SDK sign-off input"));
    assert!(windows_preflight.contains("^[0-9a-f]{40}$"));
    assert!(!windows_preflight.to_ascii_lowercase().contains("namespace"));
    assert!(!windows_preflight.contains("CARGO_HOME"));
    assert!(!windows_preflight.contains("CARGO_TARGET_DIR"));
    assert!(read("sdk/rust/Cargo.toml").contains("unsafe_code = \"deny\""));
}

#[test]
fn checked_external_provenance_recompiles_without_drift() {
    let bytes =
        std::fs::read(repository_root().join("sdk/rust/completeness/security-provenance.json"))
            .unwrap();
    let checked: ExternalProvenanceRegistry = decode_canonical(&bytes).unwrap();
    let expected =
        compile_external_provenance_registry(reviewed_external_provenance_input()).unwrap();
    assert_eq!(checked, expected);

    let scanner = &checked.records[&ExternalInputRole::ScannerImage];
    assert_eq!(scanner.publisher.as_str(), "aqua-security");
    assert_eq!(scanner.repository.as_str(), "github.com/aquasecurity/trivy");
    assert_eq!(
        scanner.immutable_digest.as_str(),
        "sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c",
    );
    let scanner_source = read("toolchains/security/main.dang");
    assert!(scanner_source.contains(
        "aquasec/trivy:0.69.3@sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c",
    ));
    let exact_scan = scanner_source
        .split_once("pub scanExactArtifact")
        .expect("exact-artifact scanner function is checked")
        .1;
    assert_eq!(exact_scan.matches(".withMountedFile(").count(), 1);
    assert_eq!(exact_scan.matches("trivy image").count(), 1);
    assert_eq!(
        exact_scan
            .matches("--input=/artifact/engine.oci.tar.zst")
            .count(),
        1
    );
    assert!(!exact_scan.contains("engineDev"));
    assert!(!exact_scan.contains(".asTarball"));
}

#[test]
fn checked_trivy_database_review_binds_the_artifact_and_stable_database_bytes() {
    let bytes = std::fs::read(
        repository_root().join("sdk/rust/completeness/evidence/trivy-db-review.json"),
    )
    .unwrap();
    let review: TrivyDatabaseReview = decode_canonical(&bytes).unwrap();
    let database_digest =
        Digest::new("sha256:76213b27bda05820231b84c09ca2854ec548147e9b46c0974247116f4ced4f67")
            .unwrap();
    assert_eq!(review.format_version, "1.0.0");
    assert_eq!(review.review_kind, "trivy-db-exact-artifact-provenance");
    assert_eq!(review.source.repository, "ghcr.io/aquasecurity/trivy-db");
    assert_eq!(
        review.artifact.manifest_digest.as_str(),
        "sha256:10a3832219beaf45a3eb86065e30b39e528ae9c1650aa5f733d4666afd0712c5"
    );
    assert_eq!(
        review.artifact.manifest_media_type,
        "application/vnd.oci.image.manifest.v1+json"
    );
    assert_eq!(
        review.artifact.artifact_type,
        "application/vnd.aquasec.trivy.config.v1+json"
    );
    assert_eq!(
        review.artifact.config.digest.as_str(),
        "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
    );
    assert_eq!(review.artifact.config.size_bytes, 2);
    assert_eq!(
        review.artifact.config.media_type,
        "application/vnd.oci.empty.v1+json"
    );
    assert_eq!(
        review.artifact.layer.digest.as_str(),
        "sha256:e977429cb00f83a76642c097fa0dddc796aacc38cd07f06bb03fb627736ed41b"
    );
    assert_eq!(review.artifact.layer.size_bytes, 112_064_271);
    assert_eq!(
        review.artifact.layer.media_type,
        "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
    );
    assert_eq!(review.database.content_digest, database_digest);
    assert_eq!(review.database.size_bytes, 1_283_756_032);
    assert_eq!(review.materializations.len(), 2);
    assert!(
        review
            .materializations
            .iter()
            .all(|observation| observation.database_content_digest == database_digest)
    );
    assert_ne!(
        review.materializations[0].metadata_digest,
        review.materializations[1].metadata_digest
    );

    let registry =
        compile_external_provenance_registry(reviewed_external_provenance_input()).unwrap();
    let record = &registry.records[&ExternalInputRole::VulnerabilityDatabaseSource];
    assert_eq!(record.immutable_digest, database_digest);
    assert_eq!(record.review_evidence_digest, Digest::sha256(bytes));
}
