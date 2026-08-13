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
    let windows_preflight = read(".github/workflows/rust-sdk-windows-preflight.yml");
    let security_preflight = read("sdk/rust/scripts/ci-security-preflight.sh");
    let platform_preflight = read("sdk/rust/scripts/ci-platform-preflight.sh");
    for workflow in [&security, &platform] {
        assert!(workflow.contains("permissions:\n  contents: read"));
    }
    assert!(windows_preflight.contains("permissions: {}"));
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
    assert!(!platform.contains("actions/upload-artifact@v"));
    assert!(!platform.contains("actions/download-artifact@v"));
    assert!(!platform.contains("actions/upload-artifact@"));
    assert!(!platform.contains("actions/download-artifact@"));
    assert!(platform.contains("name: Rust SDK Development Platforms"));
    assert!(platform.contains("runner: ubuntu-24.04"));
    assert!(platform.contains("runner: macos-15"));
    assert!(!platform.contains("runner: windows-2025"));
    assert!(!platform.contains("dagger-rust-sdk-platform\n          aggregate"));
    assert!(platform.contains("Portable platform matrix admitted: \\`no\\`"));
    assert!(windows_preflight.contains("on:\n  workflow_dispatch: {}"));
    assert!(!windows_preflight.contains("pull_request:"));
    assert!(!windows_preflight.contains("push:"));
    assert!(!windows_preflight.contains("uses:"));
    assert!(windows_preflight.contains("runs-on: windows-2025"));
    assert!(windows_preflight.contains("Fetch the exact public revision without credentials"));
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
