//! Engine-free packaged-output scanner checks over a real minimal OCI layout.

use std::io::Cursor;

use dagger_sdk_completeness::{
    Digest, PackagedArtifactKind, RepositoryRelativePath, assemble_packaged_artifact_scan_bundle,
    scan_packaged_artifact, secret_canary_set_from_entropy,
};

#[path = "support/packaged_artifact.rs"]
mod packaged_artifact;

fn canaries() -> dagger_sdk_completeness::SecretCanarySet {
    secret_canary_set_from_entropy(&std::array::from_fn::<_, 32, _>(|index| index as u8)).unwrap()
}

#[test]
fn gzip_oci_layers_are_expanded_and_scanned() {
    let bytes = packaged_artifact::minimal_oci_tar(br#"{}"#, b"safe layer contents", None);
    let expected = Digest::sha256(&bytes);
    let report = scan_packaged_artifact(
        &mut Cursor::new(bytes),
        RepositoryRelativePath::new("build/backend-image.tar").unwrap(),
        PackagedArtifactKind::OciImageTar,
        &expected,
        &canaries(),
    )
    .unwrap();
    assert!(report.compressed_bytes > 0);
    assert!(report.expanded_bytes > report.compressed_bytes);
    assert!(report.findings.is_empty());
}

#[test]
fn oci_config_canaries_are_reported_and_rejected_from_the_complete_bundle() {
    let canaries = canaries();
    let mut canary = Vec::new();
    canaries.visit(|_, value| {
        if canary.is_empty() {
            canary = value.to_vec();
        }
    });
    let config = serde_json::to_vec(&serde_json::json!({"config": {"Env": [
        format!("TOKEN={}", String::from_utf8(canary).unwrap())
    ]}}))
    .unwrap();
    let bytes = packaged_artifact::minimal_oci_tar(&config, b"safe layer", None);
    let expected = Digest::sha256(&bytes);
    let report = scan_packaged_artifact(
        &mut Cursor::new(bytes),
        RepositoryRelativePath::new("build/backend-image.tar").unwrap(),
        PackagedArtifactKind::OciImageTar,
        &expected,
        &canaries,
    )
    .unwrap();
    assert!(!report.findings.is_empty());
    assert!(assemble_packaged_artifact_scan_bundle([report]).is_err());
}

#[test]
fn unreferenced_oci_blobs_fail_closed() {
    let bytes =
        packaged_artifact::minimal_oci_tar(br#"{}"#, b"safe layer", Some(b"unreferenced blob"));
    let expected = Digest::sha256(&bytes);
    assert!(
        scan_packaged_artifact(
            &mut Cursor::new(bytes),
            RepositoryRelativePath::new("build/backend-image.tar").unwrap(),
            PackagedArtifactKind::OciImageTar,
            &expected,
            &canaries(),
        )
        .is_err()
    );
}
