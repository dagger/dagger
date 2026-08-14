//! Engine-free compiler for the checked Rust conformance catalogs and closure plan.

use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, ArgAction, Command, value_parser};
use dagger_sdk_completeness::{
    AssertionOrigin, CaseFamily, ClosurePlanAction, ConformanceScopeInput,
    EngineIntegrationMappings, HarnessMappings, ResolvedLedger, ReviewedConformanceScope,
    SubjectIdentity, assertion_catalog_drift, build_observable_fixture_program_artifact,
    build_reviewed_catalog_plan, canonical_bytes, decode_canonical, derive_conformance_scope,
    reviewed_implementation_closure_plan, rust_artifact_digest,
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
    authority_route_case_count: usize,
    scenario_candidate_count: usize,
    scenario_realization_required_count: usize,
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
    let harness: HarnessMappings = read_checked(&completeness.join("harness-mappings.json"))?;
    let engine: EngineIntegrationMappings =
        read_checked(&completeness.join("engine-integration-mappings.json"))?;
    let scope = derive_conformance_scope(&ledger, &reviewed, applicability)
        .map_err(|_| "checked conformance scope was rejected")?;
    let subject = SubjectIdentity::SourceDigest(
        rust_artifact_digest(root).map_err(|_| "Rust subject identity could not be computed")?,
    );
    let plan = build_reviewed_catalog_plan(&ledger, &scope, &harness, &engine, subject.clone())
        .map_err(|_| "reviewed conformance catalog was rejected")?;
    let closure_plan =
        reviewed_implementation_closure_plan().map_err(|_| "reviewed closure plan was rejected")?;
    let observable_programs = build_observable_fixture_program_artifact(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
    )
    .map_err(|_| "observable fixture program registry was rejected")?;
    let scenario_candidates = scaffold_rust_first_conformance_manifest(
        &plan.assertion_catalog,
        &plan.fixture_registry,
        &plan.case_catalog,
    )
    .map_err(|_| "Rust-first scenario candidates could not be scaffolded")?;
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
        authority_route_case_count: plan
            .cases
            .cases
            .iter()
            .filter(|case| case.family == CaseFamily::IntegrationAssertion)
            .count(),
        scenario_candidate_count: scenario_candidates.scenarios.len(),
        scenario_realization_required_count: scenario_candidates
            .scenarios
            .iter()
            .filter(|scenario| {
                matches!(
                    scenario.realization,
                    dagger_sdk_completeness::RustScenarioRealization::RealizationRequired
                )
            })
            .count(),
        assertion_drift: assertion_catalog_drift(&scope, &plan.assertions),
        executable_text_present,
        engine_action_present,
        replay_action_present,
    };
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
