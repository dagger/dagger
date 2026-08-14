//! Engine-free process fixture for current implementation-closure assembly.

use std::fs;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

use dagger_sdk_completeness::*;

fn repository_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../..")
        .canonicalize()
        .unwrap()
}

fn native(platform: PlatformDescriptor, source: &Digest) -> NativePlatformObservation {
    NativePlatformObservation {
        format_version: ConformanceFormatVersion::V1,
        link_mechanism: NativeLinkMechanism::PosixSymlink,
        platform,
        runner_digest: Digest::sha256("fixture runner"),
        toolchain_digest: Digest::sha256("fixture toolchain"),
        rust_version: SemverVersion::new("1.97.1").unwrap(),
        source_digest: source.clone(),
        lockfiles_digest: Digest::sha256("fixture lockfiles"),
        test_digest: Digest::sha256("fixture native tests"),
        domains: CanonicalSet::new(required_native_platform_domains()),
        outcome: NativeJobOutcome::Passed,
        native_execution: true,
        dagger_invocations: 0,
        engine_starts: 0,
        docker_invocations: 0,
        other_sdk_invocations: 0,
    }
}

#[test]
fn current_checked_evidence_assembles_one_neutral_six_child_closure() {
    let root = repository_root();
    let scope: ReviewedConformanceScope = decode_canonical(
        &fs::read(root.join("sdk/rust/completeness/conformance-scope.json")).unwrap(),
    )
    .unwrap();
    let source = Digest::sha256("shared native source");
    let platform_set = assemble_supported_native_platform_set(
        scope.target_digest,
        vec![
            native(PlatformDescriptor::linux_amd64(), &source),
            native(
                PlatformDescriptor {
                    operating_system: OperatingSystem::Macos,
                    architecture: Architecture::Arm64,
                },
                &source,
            ),
        ],
    )
    .unwrap();
    let security =
        admit_rust_dependency_security(reviewed_rust_dependency_security_observation()).unwrap();
    let temp = tempfile::tempdir().unwrap();
    let platform_path = temp.path().join("platform.json");
    let security_path = temp.path().join("rust-security.json");
    fs::write(&platform_path, canonical_bytes(&platform_set).unwrap()).unwrap();
    fs::write(&security_path, canonical_bytes(&security).unwrap()).unwrap();
    let output = temp.path().join("implementation-closure.json");
    let markdown = temp.path().join("implementation-closure.md");
    let status = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "implementation-closure",
            "--root",
            root.to_str().unwrap(),
            "--platform",
            platform_path.to_str().unwrap(),
            "--rust-security",
            security_path.to_str().unwrap(),
            "--output",
            output.to_str().unwrap(),
            "--markdown-output",
            markdown.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(status.success());
    let closure: ImplementationClosureBundle =
        decode_canonical(&fs::read(output).unwrap()).unwrap();
    assert_eq!(closure.child_closures.len(), 6);
    assert_eq!(closure.generated_assets.len(), 4);
    assert_eq!(
        closure.platform_matrix_digest,
        platform_set.observation_set_digest
    );
    assert_eq!(closure.rust_security_digest, security.security_digest);
    let summary = fs::read_to_string(markdown).unwrap();
    assert!(summary.contains("Engine-free implementation closure: `passed`"));
    assert!(summary.contains("Exact-engine SDK sign-off: `not executed`"));
}
