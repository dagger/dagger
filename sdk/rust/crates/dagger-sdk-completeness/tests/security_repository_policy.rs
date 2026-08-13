//! Source-bound checks for dependency automation, workflow privilege, and pinned provenance.

use std::path::{Path, PathBuf};

use dagger_sdk_completeness::*;

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
    for workflow in [&security, &platform] {
        assert!(workflow.contains("permissions:\n  contents: read"));
        assert!(!workflow.contains("contents: write"));
    }
    for root in [
        "examples/backend/Cargo.toml",
        "examples/cli/Cargo.toml",
        "examples/frontend/Cargo.toml",
    ] {
        assert!(security.contains(&format!("--manifest-path {root}")));
    }
    for forbidden in ["docker", "dagger call", "dagger develop", "sdk/go"] {
        assert!(!platform.to_ascii_lowercase().contains(forbidden));
    }
    assert!(!platform.contains("actions/upload-artifact@v"));
    assert!(!platform.contains("actions/download-artifact@v"));
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
}
