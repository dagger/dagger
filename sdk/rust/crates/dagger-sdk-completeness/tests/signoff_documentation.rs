//! Engine-free regression checks for the exact-target sign-off operator contract.

const SIGNOFF_RUNBOOK: &str = include_str!("../../../CONFORMANCE_SIGNOFF.md");

#[test]
fn macos_snapshot_hygiene_precedes_source_identity() {
    let runbook = SIGNOFF_RUNBOOK
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ");
    for required in [
        "COPYFILE_DISABLE=1",
        "AppleDouble `._*` files",
        "remove them before hashing",
        "reject the candidate if any remain",
        "Never hash a tree containing those sidecars",
    ] {
        assert!(
            runbook.contains(required),
            "sign-off runbook must retain the macOS snapshot rule: {required}"
        );
    }
}
