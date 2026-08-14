//! Engine-free compiler for the checked Rust conformance catalogs and closure plan.

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, ArgAction, Command, value_parser};
use dagger_sdk_completeness::extract::go::{GoHelperOutput, go_scenario_context_index};
use dagger_sdk_completeness::{
    AssertionOrigin, CaseFamily, ClosurePlanAction, ConformanceScopeInput,
    EngineIntegrationMappings, HarnessMappings, ResolvedLedger, ReviewedConformanceScope,
    SubjectIdentity, apply_rust_scenario_registry, assertion_catalog_drift,
    build_observable_fixture_program_artifact, build_reviewed_catalog_plan, canonical_bytes,
    compile_rust_first_conformance_manifest, compile_rust_scenario_registry, decode_canonical,
    derive_conformance_scope, reviewed_implementation_closure_plan,
    reviewed_rust_scenario_registry, rust_artifact_digest, rust_scenario_candidate_digest,
    scaffold_rust_first_conformance_manifest,
};
use serde::Serialize;

#[derive(Serialize)]
struct CatalogAudit<'a> {
    target_digest: &'a dagger_sdk_completeness::TargetDigest,
    subject: &'a SubjectIdentity,
    scope_digest: &'a dagger_sdk_completeness::Digest,
    assertion_catalog_digest: &'a dagger_sdk_completeness::Digest,
    fixture_registry_digest: &'a dagger_sdk_completeness::Digest,
    case_catalog_digest: &'a dagger_sdk_completeness::Digest,
    closure_plan_digest: &'a dagger_sdk_completeness::Digest,
    assertion_count: usize,
    applicability_assertion_count: usize,
    fixture_count: usize,
    case_count: usize,
    fixed_case_count: usize,
    standalone_example_case_count: usize,
    authority_route_case_count: usize,
    scenario_candidate_count: usize,
    scenario_candidate_digest: &'a dagger_sdk_completeness::Digest,
    scenario_registry_digest: &'a dagger_sdk_completeness::Digest,
    scenario_runner_source_digest: &'a dagger_sdk_completeness::Digest,
    scenario_authority_context_source_digest: &'a dagger_sdk_completeness::Digest,
    scenario_authority_context_count: usize,
    scenario_selected_authority_context_count: usize,
    scenario_registration_count: usize,
    scenario_runnable_realization_count: usize,
    scenario_runnable_proof_count: usize,
    scenario_execution_cell_count: usize,
    equivalent_typescript_authority_identity_count: usize,
    equivalent_typescript_enclosing_method_count: usize,
    scenario_realization_required_count: usize,
    #[serde(skip_serializing_if = "Option::is_none")]
    admitted_scenario_manifest_digest: Option<&'a dagger_sdk_completeness::Digest>,
    assertion_drift: dagger_sdk_completeness::AssertionCatalogDrift,
    executable_text_present: bool,
    engine_action_present: bool,
    replay_action_present: bool,
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            eprintln!("{message}");
            ExitCode::from(1)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = Command::new("dagger-conformance-catalog")
        .about("Compile or check the engine-free Rust SDK conformance catalogs")
        .arg(
            Arg::new("root")
                .long("root")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("update")
                .long("update")
                .action(ArgAction::SetTrue)
                .help("Atomically update changed checked artifacts; otherwise only check drift"),
        )
        .get_matches();
    let root = matches
        .get_one::<PathBuf>("root")
        .expect("required root is present");
    let completeness = root.join("sdk/rust/completeness");
    let ledger: ResolvedLedger = read_checked(&completeness.join("artifacts/ledger.json"))?;
    let reviewed: ReviewedConformanceScope =
        read_checked(&completeness.join("conformance-scope.json"))?;
    let applicability: ConformanceScopeInput =
        read_checked(&completeness.join("conformance-applicability.json"))?;
    let harness_path = completeness.join("harness-mappings.json");
    let mut harness: HarnessMappings = read_checked(&harness_path)?;
    let engine: EngineIntegrationMappings =
        read_checked(&completeness.join("engine-integration-mappings.json"))?;
    let scope = derive_conformance_scope(&ledger, &reviewed, applicability)
        .map_err(|_| "checked conformance scope was rejected")?;
    let rust_artifact_digest =
        rust_artifact_digest(root).map_err(|_| "Rust subject identity could not be computed")?;
    if matches.get_flag("update") {
        for mapping in harness.checks.values_mut() {
            mapping.verified_artifact_digest = rust_artifact_digest.clone();
        }
    }
    let subject = SubjectIdentity::SourceDigest(rust_artifact_digest);
    let plan =
        build_reviewed_catalog_plan(root, &ledger, &scope, &harness, &engine, subject.clone())
            .map_err(|_| "reviewed conformance catalog was rejected")?;
    let closure_plan =
        reviewed_implementation_closure_plan().map_err(|_| "reviewed closure plan was rejected")?;
    let observable_programs = build_observable_fixture_program_artifact(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
    )
    .map_err(|_| "observable fixture program registry was rejected")?;
    let authority_context_source =
        fs::read(completeness.join("sources/go/go-integration-tests.json"))
            .map_err(|_| "could not read the checked Go scenario context source")?;
    let authority_context_output: GoHelperOutput =
        serde_json::from_slice(&authority_context_source)
            .map_err(|_| "checked Go scenario context source is invalid")?;
    let authority_contexts = go_scenario_context_index(&authority_context_output)
        .map_err(|_| "checked Go scenario contexts were rejected")?;
    let authority_context_source_digest =
        dagger_sdk_completeness::Digest::sha256(&authority_context_source);
    let scenario_candidates = scaffold_rust_first_conformance_manifest(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
        &authority_contexts,
    )
    .map_err(|errors| {
        eprintln!("{errors:?}");
        "Rust-first scenario candidates could not be scaffolded"
    })?;
    let scenario_candidate_digest = rust_scenario_candidate_digest(&scenario_candidates)
        .map_err(|_| "Rust-first scenario candidates could not be identified")?;
    let runner_source =
        fs::read(root.join("toolchains/rust-sdk-dev/testdata/scenario_conformance.rs"))
            .map_err(|_| "could not read the checked Rust scenario runner")?;
    let runner_source_digest = dagger_sdk_completeness::Digest::sha256(runner_source);
    let scenario_registry_path = completeness.join("conformance-scenario-realizations.json");
    let reviewed_scenario_registry = reviewed_rust_scenario_registry(
        &scenario_candidates,
        &plan.case_catalog,
        runner_source_digest.clone(),
    )
    .map_err(|_| "reviewed Rust scenario projection was rejected")?;
    let scenario_registry_input = if matches.get_flag("update") {
        reviewed_scenario_registry
    } else {
        let bytes = fs::read(&scenario_registry_path)
            .map_err(|_| "checked Rust scenario registry is missing")?;
        let checked = decode_canonical(&bytes)
            .map_err(|_| "checked Rust scenario registry is not canonical")?;
        if checked != reviewed_scenario_registry {
            return Err("checked Rust scenario registry differs from the reviewed projection");
        }
        checked
    };
    let scenario_registry = compile_rust_scenario_registry(
        scenario_registry_input.clone(),
        &scenario_candidates,
        &plan.case_catalog,
        &runner_source_digest,
    )
    .map_err(|_| "checked Rust scenario registry was rejected")?;
    let realized_scenarios =
        apply_rust_scenario_registry(scenario_candidates.clone(), &scenario_registry);
    let admitted_scenario_manifest =
        if scenario_registry.registrations().len() == scenario_candidates.scenarios.len() {
            Some(
                compile_rust_first_conformance_manifest(
                    realized_scenarios.clone(),
                    &plan.assertion_catalog,
                    &plan.fixture_registry,
                    &plan.case_catalog,
                    &scenario_registry,
                )
                .map_err(|_| "complete Rust scenario manifest was rejected")?,
            )
        } else {
            None
        };
    let rendered_cases = canonical_bytes(&plan.cases)
        .map_err(|_| "case catalog could not be inspected for executable text")?;
    let executable_text_present = [
        b"\"command\":".as_slice(),
        b"\"arguments\":".as_slice(),
        b"\"working_directory\":".as_slice(),
        b"\"executable\":".as_slice(),
    ]
    .into_iter()
    .any(|needle| {
        rendered_cases
            .windows(needle.len())
            .any(|window| window == needle)
    });
    let engine_action_present = closure_plan
        .actions
        .iter()
        .any(|action| action == &ClosurePlanAction::StartEngine);
    let replay_action_present = closure_plan.actions.iter().any(|action| {
        matches!(
            action,
            ClosurePlanAction::ReplayRustUnit
                | ClosurePlanAction::ReplayRustFixture
                | ClosurePlanAction::ReplayFormat
                | ClosurePlanAction::ReplayClippy
                | ClosurePlanAction::ReplayRustdoc
                | ClosurePlanAction::ReplayCargoDeny
                | ClosurePlanAction::ReplayDirectGo
        )
    });
    let audit = CatalogAudit {
        target_digest: scope.target_digest(),
        subject: &subject,
        scope_digest: scope.digest(),
        assertion_catalog_digest: plan.assertion_catalog.digest(),
        fixture_registry_digest: plan.fixture_registry.digest(),
        case_catalog_digest: plan.case_catalog.digest(),
        closure_plan_digest: &closure_plan.plan_digest,
        assertion_count: plan.assertions.assertions.len(),
        applicability_assertion_count: plan
            .assertions
            .assertions
            .iter()
            .filter(|assertion| assertion.origin == AssertionOrigin::Applicability)
            .count(),
        fixture_count: plan.fixtures.fixtures.len(),
        case_count: plan.cases.cases.len(),
        fixed_case_count: plan
            .cases
            .cases
            .iter()
            .filter(|case| case.family != CaseFamily::IntegrationAssertion)
            .count(),
        standalone_example_case_count: plan
            .cases
            .cases
            .iter()
            .filter(|case| case.family == CaseFamily::StandaloneExample)
            .count(),
        authority_route_case_count: plan
            .cases
            .cases
            .iter()
            .filter(|case| case.family == CaseFamily::IntegrationAssertion)
            .count(),
        scenario_candidate_count: scenario_candidates.scenarios.len(),
        scenario_candidate_digest: &scenario_candidate_digest,
        scenario_registry_digest: scenario_registry.digest(),
        scenario_runner_source_digest: scenario_registry.runner_source_digest(),
        scenario_authority_context_source_digest: &authority_context_source_digest,
        scenario_authority_context_count: authority_contexts.len(),
        scenario_selected_authority_context_count: scenario_candidates
            .scenarios
            .iter()
            .flat_map(|scenario| scenario.spine.authority_context_digests.iter().cloned())
            .collect::<BTreeSet<_>>()
            .len(),
        scenario_registration_count: scenario_registry.registrations().len(),
        scenario_runnable_realization_count: scenario_registry.realization_ids().len(),
        scenario_runnable_proof_count: scenario_registry
            .registrations()
            .values()
            .map(|registration| registration.proof_id.clone())
            .collect::<BTreeSet<_>>()
            .len(),
        scenario_execution_cell_count: scenario_registry
            .registrations()
            .values()
            .filter_map(|registration| {
                let realization_id = match &registration.realization {
                    dagger_sdk_completeness::RustScenarioRealization::GeneratedCore {
                        realization_id,
                        ..
                    }
                    | dagger_sdk_completeness::RustScenarioRealization::ReviewedRustFixture {
                        realization_id,
                        ..
                    } => realization_id,
                    dagger_sdk_completeness::RustScenarioRealization::RealizationRequired => {
                        return None;
                    }
                };
                Some((realization_id.clone(), registration.proof_id.clone()))
            })
            .collect::<BTreeSet<_>>()
            .len(),
        equivalent_typescript_authority_identity_count: 132,
        equivalent_typescript_enclosing_method_count: 38,
        scenario_realization_required_count: realized_scenarios
            .scenarios
            .iter()
            .filter(|scenario| {
                matches!(
                    scenario.realization,
                    dagger_sdk_completeness::RustScenarioRealization::RealizationRequired
                )
            })
            .count(),
        admitted_scenario_manifest_digest: admitted_scenario_manifest
            .as_ref()
            .map(|manifest| manifest.digest()),
        assertion_drift: assertion_catalog_drift(&scope, &plan.assertions),
        executable_text_present,
        engine_action_present,
        replay_action_present,
    };
    publish(&harness_path, &harness, matches.get_flag("update"))?;
    publish(
        &completeness.join("conformance-assertions.json"),
        &plan.assertions,
        matches.get_flag("update"),
    )?;
    publish(
        &completeness.join("conformance-fixtures.json"),
        &plan.fixtures,
        matches.get_flag("update"),
    )?;
    publish(
        &completeness.join("conformance-cases.json"),
        &plan.cases,
        matches.get_flag("update"),
    )?;
    publish(
        &completeness.join("conformance-observable-programs.json"),
        &observable_programs,
        matches.get_flag("update"),
    )?;
    publish(
        &completeness.join("conformance-scenario-candidates.json"),
        &scenario_candidates,
        matches.get_flag("update"),
    )?;
    publish(
        &scenario_registry_path,
        &scenario_registry_input,
        matches.get_flag("update"),
    )?;
    publish(
        &completeness.join("conformance-closure-plan.json"),
        &closure_plan,
        matches.get_flag("update"),
    )?;
    publish(
        &completeness.join("conformance-catalog-audit.json"),
        &audit,
        matches.get_flag("update"),
    )?;
    Ok(())
}

fn read_checked<T>(path: &Path) -> Result<T, &'static str>
where
    T: serde::de::DeserializeOwned + Serialize,
{
    let bytes = fs::read(path).map_err(|_| "could not read a checked catalog input")?;
    decode_canonical(&bytes).map_err(|_| "catalog input is not canonical")
}

fn publish<T: Serialize>(path: &Path, value: &T, update: bool) -> Result<(), &'static str> {
    let bytes = canonical_bytes(value).map_err(|_| "catalog output could not be encoded")?;
    if fs::read(path).ok().as_deref() == Some(bytes.as_slice()) {
        return Ok(());
    }
    if !update {
        return Err("checked conformance catalog artifacts are stale");
    }
    let parent = path.parent().ok_or("catalog output has no parent")?;
    fs::create_dir_all(parent).map_err(|_| "catalog output directory could not be created")?;
    let temporary = parent.join(format!(".conformance-catalog-{}.tmp", std::process::id()));
    let result = fs::write(&temporary, &bytes)
        .and_then(|()| fs::rename(&temporary, path))
        .map_err(|_| "catalog output could not be published");
    if result.is_err() {
        let _ = fs::remove_file(temporary);
    }
    result
}
