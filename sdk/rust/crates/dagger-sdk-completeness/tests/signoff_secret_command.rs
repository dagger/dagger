//! Engine-free process fixture for the exact-sign-off secret-evidence command.

use std::fs;
use std::process::{Command, Stdio};

use dagger_sdk_completeness::{
    Digest, SecretCanaryCategory, SecretEvidenceReport, SecretInspectionDomain, decode_canonical,
    secret_canary_set_from_entropy, secret_evidence_domain_byte_limit,
    validate_secret_evidence_report,
};

#[path = "support/packaged_artifact.rs"]
mod packaged_artifact;

const DOMAINS: &[&str] = &[
    "source-files",
    "generated-and-packaged-files",
    "artifact-entries",
    "cache-and-provenance",
    "process-output",
    "errors-and-debug",
    "diagnostics-and-traces",
    "reports",
    "draft-verdict",
];

const DOMAIN_LIMITS: &[(&str, SecretInspectionDomain, u64)] = &[
    (
        "source-files",
        SecretInspectionDomain::SourceFiles,
        1024 * 1024,
    ),
    (
        "generated-and-packaged-files",
        SecretInspectionDomain::GeneratedAndPackagedFiles,
        1024 * 1024,
    ),
    (
        "artifact-entries",
        SecretInspectionDomain::ArtifactEntries,
        1024 * 1024,
    ),
    (
        "cache-and-provenance",
        SecretInspectionDomain::CacheAndProvenance,
        256 * 1024,
    ),
    (
        "process-output",
        SecretInspectionDomain::ProcessOutput,
        4 * 1024 * 1024,
    ),
    (
        "errors-and-debug",
        SecretInspectionDomain::ErrorsAndDebug,
        1024 * 1024,
    ),
    (
        "diagnostics-and-traces",
        SecretInspectionDomain::DiagnosticsAndTraces,
        4 * 1024 * 1024,
    ),
    ("reports", SecretInspectionDomain::Reports, 16 * 1024 * 1024),
    (
        "draft-verdict",
        SecretInspectionDomain::DraftVerdict,
        8 * 1024 * 1024,
    ),
];

#[test]
fn live_canaries_are_scanned_but_never_enter_the_durable_report() {
    let temp = tempfile::tempdir().unwrap();
    let input = temp.path().join("input");
    fs::create_dir(&input).unwrap();
    for domain in DOMAINS {
        fs::write(
            input.join(format!("{domain}.evidence")),
            format!("bounded exact-sign-off evidence for {domain}\n"),
        )
        .unwrap();
    }
    let entropy = (0_u8..32).collect::<Vec<_>>();
    let seed = entropy
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let seed_path = temp.path().join("seed");
    fs::write(&seed_path, &seed).unwrap();
    let packaged_scan = make_packaged_scan(temp.path(), &seed_path);
    let output = temp.path().join("secret-report.json");
    let status = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "secret-report",
            "--root",
            input.to_str().unwrap(),
            "--seed",
            seed_path.to_str().unwrap(),
            "--packaged-scan",
            packaged_scan.to_str().unwrap(),
            "--output",
            output.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(status.success());
    let bytes = fs::read(&output).unwrap();
    let report: SecretEvidenceReport = decode_canonical(&bytes).unwrap();
    validate_secret_evidence_report(&report).unwrap();
    assert!(
        !bytes
            .windows(seed.len())
            .any(|window| window == seed.as_bytes())
    );
    assert!(!bytes.windows(14).any(|window| window == b"dagger-canary"));

    let canaries = secret_canary_set_from_entropy(&entropy).unwrap();
    let mut session = Vec::new();
    canaries.visit(|category, value| {
        if category == SecretCanaryCategory::Session {
            session.extend_from_slice(value);
        }
    });
    fs::write(input.join("process-output.evidence"), session).unwrap();
    let rejected_output = temp.path().join("rejected.json");
    let rejected = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "secret-report",
            "--root",
            input.to_str().unwrap(),
            "--seed",
            seed_path.to_str().unwrap(),
            "--packaged-scan",
            packaged_scan.to_str().unwrap(),
            "--output",
            rejected_output.to_str().unwrap(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(!rejected.success());
}

#[test]
fn every_secret_domain_accepts_its_exact_bound_and_rejects_one_excess_byte() {
    let temp = tempfile::tempdir().unwrap();
    let input = temp.path().join("input");
    fs::create_dir(&input).unwrap();
    let seed_path = temp.path().join("seed");
    fs::write(
        &seed_path,
        (0_u8..32)
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>(),
    )
    .unwrap();
    let packaged_scan = make_packaged_scan(temp.path(), &seed_path);

    for (slug, domain, expected_limit) in DOMAIN_LIMITS {
        assert_eq!(secret_evidence_domain_byte_limit(*domain), *expected_limit);
        let limit = usize::try_from(secret_evidence_domain_byte_limit(*domain)).unwrap();
        fs::write(input.join(format!("{slug}.evidence")), vec![b'x'; limit]).unwrap();
    }
    let boundary_output = temp.path().join("boundary.json");
    assert!(
        run_secret_report(&input, &seed_path, &packaged_scan, &boundary_output)
            .status
            .success()
    );
    let report: SecretEvidenceReport =
        decode_canonical(&fs::read(&boundary_output).unwrap()).unwrap();
    validate_secret_evidence_report(&report).unwrap();

    for (oversized_slug, oversized_domain, _) in DOMAIN_LIMITS {
        for domain in DOMAINS {
            fs::write(
                input.join(format!("{domain}.evidence")),
                format!("bounded evidence for {domain}\n"),
            )
            .unwrap();
        }
        let limit = usize::try_from(secret_evidence_domain_byte_limit(*oversized_domain)).unwrap();
        fs::write(
            input.join(format!("{oversized_slug}.evidence")),
            vec![b'x'; limit + 1],
        )
        .unwrap();
        let rejected_output = temp.path().join(format!("rejected-{oversized_slug}.json"));
        let rejected = run_secret_report(&input, &seed_path, &packaged_scan, &rejected_output);
        assert!(
            !rejected.status.success(),
            "oversized {oversized_slug} evidence was admitted"
        );
        assert_eq!(
            String::from_utf8(rejected.stderr).unwrap(),
            "secret inspection domain exceeds its declared byte bound\n"
        );
        assert!(!rejected_output.exists());
    }
}

fn run_secret_report(
    root: &std::path::Path,
    seed: &std::path::Path,
    packaged_scan: &std::path::Path,
    output: &std::path::Path,
) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "secret-report",
            "--root",
            root.to_str().unwrap(),
            "--seed",
            seed.to_str().unwrap(),
            "--packaged-scan",
            packaged_scan.to_str().unwrap(),
            "--output",
            output.to_str().unwrap(),
        ])
        .output()
        .unwrap()
}

fn make_packaged_scan(root: &std::path::Path, seed: &std::path::Path) -> std::path::PathBuf {
    let packaged_root = root.join("packaged");
    fs::create_dir_all(packaged_root.join("build")).unwrap();
    let cli = b"fixture executable".to_vec();
    let backend = packaged_artifact::minimal_oci_tar(br#"{}"#, b"backend", None);
    let frontend = packaged_artifact::minimal_oci_tar(br#"{}"#, b"frontend", None);
    fs::write(packaged_root.join("build/cli"), &cli).unwrap();
    fs::write(packaged_root.join("build/backend-image.tar"), &backend).unwrap();
    fs::write(packaged_root.join("build/frontend-image.tar"), &frontend).unwrap();
    let output = root.join("packaged-scan.json");
    let result = Command::new(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"))
        .args([
            "packaged-scan",
            "--root",
            packaged_root.to_str().unwrap(),
            "--seed",
            seed.to_str().unwrap(),
            "--cli-digest",
            Digest::sha256(cli).as_str(),
            "--backend-digest",
            Digest::sha256(backend).as_str(),
            "--frontend-digest",
            Digest::sha256(frontend).as_str(),
            "--output",
            output.to_str().unwrap(),
        ])
        .output()
        .unwrap();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    output
}
