//! Fixed conformance inventory, wire-boundary, and diagnostic foundation checks.

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
    assert_eq!(reviewed.policy_capability_ids.len(), 22);
    assert_eq!(reviewed_policy_capabilities().len(), 22);
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
fn canonical_applicability_and_closed_catalogs_are_admitted() {
    let ledger: ResolvedLedger = decode_canonical(&artifact("artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&artifact("conformance-scope.json")).unwrap();
    let input: ConformanceScopeInput =
        decode_canonical(&artifact("conformance-applicability.json")).unwrap();
    let scope = derive_conformance_scope(&ledger, &reviewed, input).unwrap();
    assert_eq!(scope.existing_records().len(), 1_081);
    assert_eq!(scope.policy_capabilities().len(), 22);

    let assertions: AssertionCatalogInput =
        decode_canonical(&artifact("conformance-assertions.json")).unwrap();
    let fixtures: FixtureRegistryInput =
        decode_canonical(&artifact("conformance-fixtures.json")).unwrap();
    let cases: CaseCatalogInput = decode_canonical(&artifact("conformance-cases.json")).unwrap();
    let assertion_catalog = compile_assertion_catalog(&scope, assertions.clone()).unwrap();
    let fixture_registry = compile_fixture_registry(fixtures.clone()).unwrap();
    let case_catalog =
        compile_case_catalog(&scope, &assertion_catalog, &fixture_registry, cases.clone()).unwrap();
    assert_eq!(assertions.assertions.len(), 1_050);
    assert_eq!(fixtures.fixtures.len(), 1_050);
    assert_eq!(cases.cases.len(), 675);
    assert_eq!(case_catalog.cases().len(), 675);
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
fn checked_scenario_candidates_are_totally_bound_to_the_reviewed_rust_runner() {
    let candidates: RustFirstConformanceManifestInput =
        decode_canonical(&artifact("conformance-scenario-candidates.json")).unwrap();
    let registry: RustScenarioRegistryInput =
        decode_canonical(&artifact("conformance-scenario-realizations.json")).unwrap();
    assert_eq!(candidates.scenarios.len(), 612);
    assert_eq!(registry.registrations.len(), 612);
    assert_eq!(
        candidates
            .scenarios
            .iter()
            .flat_map(|scenario| scenario.spine.authority_context_digests.iter())
            .collect::<std::collections::BTreeSet<_>>()
            .len(),
        161
    );
    assert_eq!(
        registry
            .registrations
            .iter()
            .map(|registration| &registration.proof_id)
            .collect::<std::collections::BTreeSet<_>>()
            .len(),
        13
    );
    assert_eq!(
        registry
            .registrations
            .iter()
            .filter_map(|registration| match &registration.realization {
                RustScenarioRealization::ReviewedRustFixture { realization_id, .. } => {
                    Some(realization_id)
                }
                _ => None,
            })
            .collect::<std::collections::BTreeSet<_>>()
            .len(),
        30
    );
    assert_eq!(
        registry.scenario_candidate_digest,
        rust_scenario_candidate_digest(&candidates).unwrap()
    );
    assert_eq!(
        registry.runner_source_digest,
        Digest::sha256(include_bytes!(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../../../toolchains/rust-sdk-dev/testdata/scenario_conformance.rs"
        )))
    );
    assert!(candidates.scenarios.iter().all(|scenario| {
        matches!(
            scenario.realization,
            RustScenarioRealization::RealizationRequired
        )
    }));
    assert_eq!(
        candidates
            .scenarios
            .iter()
            .map(|scenario| scenario.spine.id.clone())
            .collect::<std::collections::BTreeSet<_>>()
            .len(),
        612
    );
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
