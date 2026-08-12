//! Fixed Feature 8 inventory, wire-boundary, and diagnostic foundation checks.

use dagger_sdk_completeness::*;

const COMPLETENESS: &str = "../../completeness";

fn artifact(path: &str) -> Vec<u8> {
    std::fs::read(format!(
        "{}/{COMPLETENESS}/{path}",
        env!("CARGO_MANIFEST_DIR")
    ))
    .unwrap()
}

#[test]
fn exact_existing_and_policy_inventories_are_checked_separately() {
    let ledger: ResolvedLedger = decode_canonical(&artifact("artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&artifact("conformance-scope.json")).unwrap();
    validate_existing_conformance_scope(&ledger, &reviewed).unwrap();
    assert_eq!(reviewed.existing_capability_ids.len(), 1_081);
    assert_eq!(reviewed.existing_records.len(), 1_081);
    assert_eq!(reviewed.policy_capability_ids.len(), 21);
    assert_eq!(reviewed_policy_capabilities().len(), 21);
    assert!(
        reviewed
            .existing_capability_ids
            .iter()
            .all(|id| !reviewed.policy_capability_ids.contains(id))
    );
}

#[test]
fn scope_drift_renderer_reports_exact_stable_categories() {
    let mut ledger: ResolvedLedger = decode_canonical(&artifact("artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&artifact("conformance-scope.json")).unwrap();
    let removed = reviewed.existing_records[0].capability_id.clone();
    let changed = reviewed.existing_records[1].capability_id.clone();
    ledger.capabilities.remove(&removed);
    let row = ledger.capabilities.get_mut(&changed).unwrap();
    row.status = match row.status {
        Status::Missing => Status::Partial,
        _ => Status::Missing,
    };
    row.capability_fingerprint = Digest::sha256("changed semantic input");

    let delta = existing_conformance_scope_delta(&ledger, &reviewed);
    assert_eq!(delta.removed.as_slice(), std::slice::from_ref(&removed));
    assert_eq!(delta.moved.as_slice(), std::slice::from_ref(&changed));
    assert_eq!(
        delta.fingerprint_changed.as_slice(),
        std::slice::from_ref(&changed)
    );
    assert_eq!(
        delta.render(),
        format!(
            "removed:{}\nmoved:{}\nfingerprint-changed:{}",
            removed.as_str(),
            changed.as_str(),
            changed.as_str()
        )
    );
}

#[test]
fn canonical_scaffolds_bind_the_target_but_cannot_claim_completion() {
    let ledger: ResolvedLedger = decode_canonical(&artifact("artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&artifact("conformance-scope.json")).unwrap();
    let input: ConformanceScopeInput =
        decode_canonical(&artifact("conformance-applicability.json")).unwrap();
    let errors = derive_conformance_scope(&ledger, &reviewed, input).unwrap_err();
    assert!(
        errors
            .as_slice()
            .iter()
            .any(|error| { error.code == ConformanceDiagnosticCode::ApplicabilityRecordInvalid })
    );
    assert!(
        errors.as_slice().iter().any(|error| {
            error.code == ConformanceDiagnosticCode::ConformancePolicyScopeChanged
        })
    );

    let assertions: ConformanceAssertionScaffold =
        decode_canonical(&artifact("conformance-assertions.json")).unwrap();
    let cases: ConformanceCaseScaffold =
        decode_canonical(&artifact("conformance-cases.json")).unwrap();
    assert!(assertions.assertions.is_empty());
    assert!(cases.cases.is_empty());
    assert_eq!(assertions.target_digest, reviewed.target_digest);
    assert_eq!(cases.target_digest, reviewed.target_digest);

    let placeholders = scaffold_applicability_placeholders(&reviewed);
    assert_eq!(placeholders.len(), 1_081);
    assert!(
        placeholders.iter().all(|placeholder| {
            placeholder.state == ApplicabilityPlaceholderState::ReviewRequired
        })
    );
    let placeholder_json = canonical_bytes(&placeholders).unwrap();
    assert!(serde_json::from_slice::<ConformanceScopeInput>(&placeholder_json).is_err());
}

#[test]
fn every_error_table_code_has_one_safe_canonical_instance() {
    let diagnostics =
        ConformanceDiagnosticSet::new(ConformanceDiagnosticCode::ALL.iter().copied().map(|code| {
            ConformanceDiagnostic::new(
                code,
                DiagnosticCoordinate::default(),
                "stable contract failure",
            )
        }))
        .unwrap();
    assert_eq!(
        diagnostics.as_slice().len(),
        ConformanceDiagnosticCode::ALL.len()
    );
    for diagnostic in diagnostics.as_slice() {
        let bytes = canonical_bytes(diagnostic).unwrap();
        assert_eq!(
            decode_canonical::<ConformanceDiagnostic>(&bytes).unwrap(),
            *diagnostic
        );
    }
}

#[test]
fn durable_models_reject_unknown_fields_and_unsafe_coordinates() {
    let checked: SignoffHostProfile =
        decode_canonical(&artifact("signoff-host-profile.json")).unwrap();
    let mut profile = serde_json::to_value(checked).unwrap();
    profile
        .as_object_mut()
        .unwrap()
        .insert("provider".to_owned(), serde_json::json!("namespace"));
    assert!(serde_json::from_value::<SignoffHostProfile>(profile).is_err());
    assert!(AssertionId::new("/absolute/path").is_err());
    assert!(FindingId::new("finding\nsecret").is_err());
    assert!(ProvenanceId::new("image:latest").is_err());
}

#[test]
fn checked_host_profile_is_canonical_and_provider_neutral() {
    let bytes = artifact("signoff-host-profile.json");
    let profile: SignoffHostProfile = decode_canonical(&bytes).unwrap();
    plan_host_preflight(profile).unwrap();
    let text = String::from_utf8(bytes).unwrap().to_ascii_lowercase();
    for forbidden in ["namespace", "devbox", "account", "/users/", "credential"] {
        assert!(!text.contains(forbidden));
    }
}

#[test]
fn checked_live_preflight_record_revalidates_without_provider_metadata() {
    let profile: SignoffHostProfile =
        decode_canonical(&artifact("signoff-host-profile.json")).unwrap();
    let plan = plan_host_preflight(profile).unwrap();
    let bytes = artifact("evidence/signoff-host-preflight.json");
    let record: HostPreflightRecord = decode_canonical(&bytes).unwrap();
    validate_host_preflight_record(&plan, &record, &record.container_daemon.daemon_identity)
        .unwrap();
    assert_eq!(record.smoke_start_count.get(), 1);
    assert_eq!(record.smoke_stop_count.get(), 1);
    let text = String::from_utf8(bytes).unwrap().to_ascii_lowercase();
    for forbidden in [
        "namespace",
        "devbox",
        "account",
        "/workspaces/",
        "credential",
    ] {
        assert!(!text.contains(forbidden));
    }
}
