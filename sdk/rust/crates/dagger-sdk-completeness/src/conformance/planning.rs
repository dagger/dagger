//! Deterministic construction of the reviewed assertion, fixture, and case inputs.
//!
//! Construction is deliberately separate from admission. It translates only pinned authority
//! artifacts and closed sibling inventories; the compilers then validate the resulting graph as
//! if it had been authored independently. A checked JSON diff remains the review boundary.

#![warn(missing_docs)]

use std::collections::BTreeSet;

use crate::client_generation::{ClientImplementationSubject, client_generation_scope_input};
use crate::engine_integration::{EngineIntegrationMappings, ImplementationSubject};
use crate::model::{
    CanonicalSet, CapabilityId, Digest, HarnessCheckKind, HarnessMappings, RepositoryRelativePath,
    ResolvedLedger, SourceItemKind,
};
use crate::module_authoring::{ModuleImplementationSubject, module_authoring_scope_input};

use super::{
    ApplicabilityDecision, AssertionCatalog, AssertionCatalogInput, AssertionFamily, AssertionId,
    AssertionOrigin, AuthorityAnchor, CaseCatalog, CaseCatalogInput, CaseDefinition, CaseFamily,
    CaseProgram, CommonHarnessCheck, ConcurrencyClass, ConformanceAssertion, ConformanceDiagnostic,
    ConformanceDiagnosticCode, ConformanceDiagnosticSet, ConformanceFormatVersion,
    ConformanceScope, CoreCaseShape, DiagnosticCoordinate, EngineIntegrationCase,
    FilesystemObservation, FixtureContextId, FixtureRegistry, FixtureRegistryInput,
    GoClientBehaviour, InfrastructureFailureClass, IsolationObservation, LifecycleObservation,
    MetadataObservation, NetworkPolicyId, NonZeroCount, NonZeroMillis, ObservablePredicate,
    OmissionObservation, PlatformDescriptor, QueryObservation, ResultObservation, RetryPolicy,
    ReviewedFixture, ReviewedFixtureId, SignoffCaseId, SubjectIdentity, TypedErrorObservation,
    compile_assertion_catalog, compile_case_catalog, compile_fixture_registry,
    fixture_executor_for, required_common_harness_checks, required_core_shapes,
    required_engine_integration_cases, required_module_authoring_cases,
    required_standalone_client_cases, reviewed_fixture_digest,
};

/// Complete checked planning documents and their independently admitted identities.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ReviewedCatalogPlan {
    /// Canonical authored assertion artifact.
    pub assertions: AssertionCatalogInput,
    /// Canonical authored fixture-registry artifact.
    pub fixtures: FixtureRegistryInput,
    /// Canonical authored case-catalog artifact.
    pub cases: CaseCatalogInput,
    /// Independently admitted assertion catalog and reverse indexes.
    pub assertion_catalog: AssertionCatalog,
    /// Independently admitted immutable fixture registry.
    pub fixture_registry: FixtureRegistry,
    /// Independently admitted complete case graph.
    pub case_catalog: CaseCatalog,
}

struct PlannedAssertion {
    assertion: ConformanceAssertion,
    program: CaseProgram,
}

/// Builds all catalog inputs from the pinned ledger, applicability scope, and child inventories.
pub fn build_reviewed_catalog_plan(
    ledger: &ResolvedLedger,
    scope: &ConformanceScope,
    harness: &HarnessMappings,
    engine: &EngineIntegrationMappings,
    subject: SubjectIdentity,
) -> Result<ReviewedCatalogPlan, ConformanceDiagnosticSet> {
    let mut planned = applicability_assertions(scope)?;
    planned.extend(fixed_assertions(ledger, scope, harness, engine)?);
    planned.sort_by(|left, right| left.assertion.id.cmp(&right.assertion.id));

    let assertion_input = AssertionCatalogInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest: scope.target_digest().clone(),
        scope_digest: scope.digest().clone(),
        assertions: planned
            .iter()
            .map(|planned| planned.assertion.clone())
            .collect(),
    };
    let assertion_catalog = compile_assertion_catalog(scope, assertion_input.clone())?;
    let fixture_input = build_fixtures(scope, &planned)?;
    let fixture_registry = compile_fixture_registry(fixture_input.clone())?;
    let case_input = build_cases(
        scope,
        &planned,
        &assertion_catalog,
        &fixture_registry,
        subject,
    )?;
    let case_catalog = compile_case_catalog(
        scope,
        &assertion_catalog,
        &fixture_registry,
        case_input.clone(),
    )?;
    Ok(ReviewedCatalogPlan {
        assertions: assertion_input,
        fixtures: fixture_input,
        cases: case_input,
        assertion_catalog,
        fixture_registry,
        case_catalog,
    })
}

fn applicability_assertions(
    scope: &ConformanceScope,
) -> Result<Vec<PlannedAssertion>, ConformanceDiagnosticSet> {
    let mut planned = Vec::new();
    for (assertion_id, capability_ids) in scope.assertion_capabilities() {
        let records = scope
            .existing_records()
            .values()
            .filter(|record| record.assertion_ids.contains(assertion_id))
            .collect::<Vec<_>>();
        let Some(record) = records.first() else {
            return Err(planning_error(
                "applicability assertion has no authority record",
            ));
        };
        let family = if assertion_id
            .as_str()
            .starts_with("assertion/definitive-go-client/")
        {
            AssertionFamily::DefinitiveGoClient
        } else {
            AssertionFamily::IntegrationAssertion
        };
        let fixture_id = fixture_id(assertion_id)?;
        let program = if family == AssertionFamily::DefinitiveGoClient {
            CaseProgram::DefinitiveGoClient {
                behaviour: go_behaviour(assertion_id)?,
            }
        } else {
            CaseProgram::IntegrationAssertion {
                fixture: fixture_id.clone(),
            }
        };
        let equivalence_decision = match &record.decision_evidence {
            Some(ApplicabilityDecision::IdiomaticEquivalence { rust_mechanism, .. }) => {
                Some(rust_mechanism.clone())
            }
            _ => None,
        };
        planned.push(PlannedAssertion {
            assertion: ConformanceAssertion {
                id: assertion_id.clone(),
                origin: AssertionOrigin::Applicability,
                authority_anchors: CanonicalSet::new(
                    records.iter().map(|record| record.authority_anchor.clone()),
                ),
                source_fingerprints: CanonicalSet::new(
                    records
                        .iter()
                        .map(|record| record.source_fingerprint.clone()),
                ),
                capability_ids: capability_ids.clone(),
                fixture_context: fixture_context(assertion_id)?,
                predicate: predicate_for_applicability(
                    assertion_id,
                    &record.authority_anchor.path,
                )?,
                equivalence_decision,
                permitted_families: CanonicalSet::new([family]),
            },
            program,
        });
    }
    Ok(planned)
}

fn fixed_assertions(
    ledger: &ResolvedLedger,
    scope: &ConformanceScope,
    harness: &HarnessMappings,
    engine: &EngineIntegrationMappings,
) -> Result<Vec<PlannedAssertion>, ConformanceDiagnosticSet> {
    let mut specs = Vec::<(CaseProgram, Vec<CapabilityId>, ObservablePredicate)>::new();

    for check in required_common_harness_checks() {
        let check_id = common_harness_name(check);
        let mapping = harness
            .checks
            .values()
            .find(|mapping| mapping.check_id.as_str() == check_id)
            .filter(|mapping| mapping.check_kind == HarnessCheckKind::SubjectConformance)
            .ok_or_else(|| planning_error("common harness subject check is missing"))?;
        specs.push((
            CaseProgram::CommonHarness { check },
            mapping.capability_ids.iter().cloned().collect(),
            harness_predicate(check),
        ));
    }

    specs.push((
        CaseProgram::StableConnector,
        capability_ids(
            ledger,
            &[
                "policy/rust-policy/transport-cli-version-selection",
                "policy/rust-policy/transport-download-fallback-boundary",
                "policy/rust-policy/transport-loopback-authentication",
                "policy/rust-policy/transport-session-protocol",
                "policy/rust-policy/transport-shutdown-bound",
            ],
        )?,
        ObservablePredicate::Lifecycle(LifecycleObservation::CloseAndReap),
    ));

    for shape in required_core_shapes() {
        let capability = core_shape_capability(shape);
        specs.push((
            CaseProgram::CoreShape { shape },
            capability_ids(ledger, &[capability])?,
            core_shape_predicate(shape),
        ));
    }

    for case in required_engine_integration_cases() {
        let capabilities = CanonicalSet::new(
            engine
                .mappings
                .iter()
                .filter(|mapping| {
                    engine_case_subjects(case).contains(&mapping.implementation_subject)
                })
                .map(|mapping| mapping.capability_id.clone()),
        );
        specs.push((
            CaseProgram::EngineIntegration { case },
            capabilities.into_inner(),
            engine_case_predicate(case),
        ));
    }

    let module = module_authoring_scope_input(scope.target_digest().clone());
    for case in required_module_authoring_cases() {
        let capabilities = CanonicalSet::new(
            module
                .mappings
                .iter()
                .filter(|mapping| {
                    module_case_subjects(case).contains(&mapping.implementation_subject)
                        && ledger.capabilities.contains_key(&mapping.capability_id)
                })
                .map(|mapping| mapping.capability_id.clone()),
        );
        specs.push((
            CaseProgram::ModuleAuthoring { case },
            capabilities.into_inner(),
            module_case_predicate(case),
        ));
    }

    let client = client_generation_scope_input(scope.target_digest().clone());
    let client_capabilities = client
        .mappings
        .iter()
        .filter(|mapping| ledger.capabilities.contains_key(&mapping.capability_id))
        .map(|mapping| mapping.capability_id.clone())
        .collect::<Vec<_>>();
    for case in required_standalone_client_cases() {
        let mut capabilities = client
            .mappings
            .iter()
            .filter(|mapping| {
                client_case_subjects(case).contains(&mapping.implementation_subject)
                    && ledger.capabilities.contains_key(&mapping.capability_id)
            })
            .map(|mapping| mapping.capability_id.clone())
            .collect::<Vec<_>>();
        if capabilities.is_empty() {
            // The checked ledger retains one engine-bound client capability. Every deferred case
            // participates in proving that boundary; richer Rust-owned claims remain in the
            // separately admitted implementation-closure evidence.
            capabilities.clone_from(&client_capabilities);
        }
        specs.push((
            CaseProgram::StandaloneClient { case },
            capabilities,
            client_case_predicate(case),
        ));
    }

    specs
        .into_iter()
        .map(|(program, capabilities, predicate)| {
            fixed_assertion(ledger, scope, program, capabilities, predicate)
        })
        .collect()
}

fn fixed_assertion(
    ledger: &ResolvedLedger,
    scope: &ConformanceScope,
    program: CaseProgram,
    mut capabilities: Vec<CapabilityId>,
    predicate: ObservablePredicate,
) -> Result<PlannedAssertion, ConformanceDiagnosticSet> {
    let catalog_policy = CapabilityId::new("policy/rust-policy/conformance-case-catalog")
        .expect("reviewed catalog policy identity is valid");
    capabilities.push(catalog_policy);
    let capabilities = CanonicalSet::new(capabilities).into_inner();
    let label = fixed_program_label(&program);
    let assertion_id = AssertionId::new(format!("assertion/fixed/{label}"))
        .map_err(|_| planning_error("fixed assertion identity is invalid"))?;
    if capabilities.is_empty() {
        return Err(planning_error_at_assertion(
            assertion_id,
            "fixed case has no exact capability scope",
        ));
    }
    let mut anchors = Vec::new();
    let mut fingerprints = Vec::new();
    for capability_id in &capabilities {
        if let Some(record) = ledger.capabilities.get(capability_id) {
            let source_item_kind = SourceItemKind::new(record.capability_kind.as_str())
                .map_err(|_| planning_error("fixed assertion source kind is invalid"))?;
            if record.source_anchors.is_empty() {
                return Err(planning_error("fixed assertion has no authority anchor"));
            }
            anchors.extend(record.source_anchors.iter().map(|source| AuthorityAnchor {
                repository: source.repository.clone(),
                revision: source.revision.clone(),
                path: source.path.clone(),
                locator: source.locator.clone(),
                source_item_kind: source_item_kind.clone(),
            }));
            fingerprints.push(record.capability_fingerprint.clone());
            continue;
        }
        let policy = scope
            .policy_capabilities()
            .get(capability_id)
            .ok_or_else(|| {
                planning_error("fixed assertion capability is outside reviewed scope")
            })?;
        let template =
            scope.existing_records().values().next().ok_or_else(|| {
                planning_error("policy assertion has no target authority identity")
            })?;
        anchors.push(AuthorityAnchor {
            repository: template.authority_anchor.repository.clone(),
            revision: template.authority_anchor.revision.clone(),
            path: RepositoryRelativePath::new(
                ".kiro/specs/rust-sdk-conformance-security/requirements.md",
            )
            .expect("reviewed policy source path is valid"),
            locator: policy.requirement_coordinate.clone(),
            source_item_kind: SourceItemKind::new("rust-policy-requirement")
                .expect("reviewed policy source kind is valid"),
        });
        fingerprints.push(policy.fingerprint.clone());
    }
    let family = AssertionFamily::from(program.family());
    Ok(PlannedAssertion {
        assertion: ConformanceAssertion {
            id: assertion_id.clone(),
            origin: AssertionOrigin::FixedCase { family },
            authority_anchors: CanonicalSet::new(anchors),
            source_fingerprints: CanonicalSet::new(fingerprints),
            capability_ids: CanonicalSet::new(capabilities),
            fixture_context: fixture_context(&assertion_id)?,
            predicate,
            equivalence_decision: None,
            permitted_families: CanonicalSet::new([family]),
        },
        program,
    })
}

fn build_fixtures(
    scope: &ConformanceScope,
    assertions: &[PlannedAssertion],
) -> Result<FixtureRegistryInput, ConformanceDiagnosticSet> {
    let mut fixtures = Vec::with_capacity(assertions.len());
    for planned in assertions {
        let id = fixture_id(&planned.assertion.id)?;
        let program = match &planned.program {
            CaseProgram::IntegrationAssertion { .. } => CaseProgram::IntegrationAssertion {
                fixture: id.clone(),
            },
            program => program.clone(),
        };
        let network = network_policy(&program);
        let mut fixture = ReviewedFixture {
            id,
            context_id: planned.assertion.fixture_context.clone(),
            executor: fixture_executor_for(&program),
            program,
            immutable_inputs: CanonicalSet::new(
                planned
                    .assertion
                    .source_fingerprints
                    .iter()
                    .cloned()
                    .chain([
                        scope.target_digest().digest().clone(),
                        scope.digest().clone(),
                    ]),
            ),
            network,
            permitted_family: planned.program.family(),
            fixture_digest: Digest::sha256("uncomputed fixture digest"),
        };
        fixture.fixture_digest = reviewed_fixture_digest(&fixture)?;
        fixtures.push(fixture);
    }
    Ok(FixtureRegistryInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest: scope.target_digest().clone(),
        fixtures,
    })
}

fn build_cases(
    scope: &ConformanceScope,
    planned: &[PlannedAssertion],
    assertions: &AssertionCatalog,
    fixtures: &FixtureRegistry,
    subject: SubjectIdentity,
) -> Result<CaseCatalogInput, ConformanceDiagnosticSet> {
    let mut cases = Vec::new();
    for planned in planned {
        let fixture_id = fixture_id(&planned.assertion.id)?;
        let fixture = fixtures
            .fixtures()
            .get(&fixture_id)
            .ok_or_else(|| planning_error("planned assertion fixture is missing"))?;
        let case_id = match planned.assertion.origin {
            AssertionOrigin::FixedCase { .. } => SignoffCaseId::new(format!(
                "case/fixed/{}",
                fixed_program_label(&planned.program)
            ))
            .map_err(|_| planning_error("fixed case identity is invalid"))?,
            AssertionOrigin::Applicability => {
                let routes = scope
                    .existing_records()
                    .values()
                    .filter(|record| record.assertion_ids.contains(&planned.assertion.id))
                    .flat_map(|record| record.case_ids.iter().cloned())
                    .collect::<BTreeSet<_>>();
                if routes.is_empty() {
                    continue;
                }
                let routes = routes.into_iter().collect::<Vec<_>>();
                let [route] = routes.as_slice() else {
                    return Err(planning_error(
                        "assertion has more than one reviewed case route",
                    ));
                };
                route.clone()
            }
        };
        let retry = retry_policy(&planned.program);
        cases.push(CaseDefinition {
            id: case_id,
            family: planned.program.family(),
            program: fixture.program.clone(),
            fixture_id: fixture.id.clone(),
            fixture_digest: fixture.fixture_digest.clone(),
            assertion_ids: CanonicalSet::new([planned.assertion.id.clone()]),
            capability_ids: planned.assertion.capability_ids.clone(),
            timeout: case_timeout(planned.program.family()),
            retry,
            network: fixture.network.clone(),
            concurrency_class: concurrency_class(&planned.program),
        });
    }
    Ok(CaseCatalogInput {
        format_version: ConformanceFormatVersion::V1,
        target_digest: scope.target_digest().clone(),
        subject,
        platform: PlatformDescriptor::linux_amd64(),
        scope_digest: scope.digest().clone(),
        assertion_catalog_digest: assertions.digest().clone(),
        fixture_registry_digest: fixtures.digest().clone(),
        cases,
    })
}

fn predicate_for_applicability(
    assertion_id: &AssertionId,
    path: &crate::model::RepositoryRelativePath,
) -> Result<ObservablePredicate, ConformanceDiagnosticSet> {
    if assertion_id
        .as_str()
        .starts_with("assertion/definitive-go-client/")
    {
        return Ok(go_predicate(go_behaviour(assertion_id)?));
    }
    let predicate = match path.as_str() {
        "core/integration/module_error_test.go"
        | "core/integration/module_terminal_test.go"
        | "core/integration/module_validation_test.go" => {
            ObservablePredicate::TypedError(TypedErrorObservation::Category)
        }
        "core/integration/module_constructor_test.go"
        | "core/integration/module_runtime_behavior_test.go"
        | "core/integration/module_type_test.go" => {
            ObservablePredicate::Omission(OmissionObservation::ExplicitValue)
        }
        "core/integration/module_path_inputs_test.go"
        | "core/integration/module_private_deps_test.go"
        | "internal/cmd/dagger/module_init_test.go" => {
            ObservablePredicate::Filesystem(FilesystemObservation::PathBoundary)
        }
        "core/integration/workspace_modules_test.go" => {
            ObservablePredicate::Isolation(IsolationObservation::Workspace)
        }
        "core/integration/module_call_test.go" => {
            ObservablePredicate::Isolation(IsolationObservation::Call)
        }
        "core/integration/module_dependency_runtime_test.go"
        | "core/integration/module_dependency_cli_test.go" => {
            ObservablePredicate::Query(QueryObservation::Dependency)
        }
        "core/integration/module_current_module_test.go"
        | "core/integration/module_self_calls_test.go" => {
            ObservablePredicate::Query(QueryObservation::Module)
        }
        "core/integration/module_introspection_cli_test.go" => {
            ObservablePredicate::Query(QueryObservation::Introspection)
        }
        "core/integration/module_definition_test.go"
        | "core/integration/module_deprecation_test.go"
        | "core/integration/module_runtime_codegen_test.go" => {
            ObservablePredicate::Metadata(MetadataObservation::Definition)
        }
        "core/integration/module_engine_version_test.go" => {
            ObservablePredicate::Compatibility(super::CompatibilityObservation::TargetVersion)
        }
        "core/integration/module_config_compat_test.go"
        | "core/integration/module_custom_sdk_test.go"
        | "core/integration/module_loading_test.go"
        | "internal/cmd/dagger/module_sdk_test.go"
        | "internal/cmd/dagger/sdk_init_dynamic_test.go" => {
            ObservablePredicate::Compatibility(super::CompatibilityObservation::Configuration)
        }
        "core/integration/module_up_test.go" => {
            ObservablePredicate::Lifecycle(LifecycleObservation::Invoke)
        }
        "core/integration/module_benchmark_test.go"
        | "core/integration/module_config_test.go"
        | "core/integration/module_iface_test.go"
        | "internal/cmd/dagger/module_test.go"
        | "core/integration/module_builtin_dang_test.go"
        | "core/integration/module_dang_test.go"
        | "core/integration/module_elixir_test.go"
        | "core/integration/module_go_test.go"
        | "core/integration/module_java_test.go"
        | "core/integration/module_php_test.go"
        | "core/integration/module_python_test.go"
        | "core/integration/module_typescript_test.go" => {
            ObservablePredicate::Result(ResultObservation::ExactValue)
        }
        _ => {
            return Err(planning_error(
                "assertion authority path has no reviewed predicate",
            ));
        }
    };
    Ok(predicate)
}

fn go_behaviour(id: &AssertionId) -> Result<GoClientBehaviour, ConformanceDiagnosticSet> {
    let suffix = id
        .as_str()
        .strip_prefix("assertion/definitive-go-client/")
        .ok_or_else(|| planning_error("definitive client assertion identity is invalid"))?;
    let behaviour = match suffix {
        "directory" => GoClientBehaviour::Directory,
        "git" => GoClientBehaviour::Git,
        "container" => GoClientBehaviour::Container,
        "container-mutation" => GoClientBehaviour::ContainerMutation,
        "list" => GoClientBehaviour::List,
        "typed-exec-error" => GoClientBehaviour::TypedExecError,
        "exec-error-output-fields" => GoClientBehaviour::ExecErrorOutputFields,
        "exec-error-empty-output" => GoClientBehaviour::ExecErrorEmptyOutput,
        "non-exec-error-separation" => GoClientBehaviour::NonExecErrorSeparation,
        _ => return Err(planning_error("definitive client assertion is unknown")),
    };
    Ok(behaviour)
}

fn go_predicate(behaviour: GoClientBehaviour) -> ObservablePredicate {
    match behaviour {
        GoClientBehaviour::Directory | GoClientBehaviour::Git | GoClientBehaviour::Container => {
            ObservablePredicate::Result(ResultObservation::ExactValue)
        }
        GoClientBehaviour::ContainerMutation => {
            ObservablePredicate::Result(ResultObservation::MutationVisible)
        }
        GoClientBehaviour::List => ObservablePredicate::Result(ResultObservation::CollectionShape),
        GoClientBehaviour::TypedExecError => {
            ObservablePredicate::TypedError(TypedErrorObservation::Category)
        }
        GoClientBehaviour::ExecErrorOutputFields => {
            ObservablePredicate::TypedError(TypedErrorObservation::Fields)
        }
        GoClientBehaviour::ExecErrorEmptyOutput => {
            ObservablePredicate::TypedError(TypedErrorObservation::EmptyOutput)
        }
        GoClientBehaviour::NonExecErrorSeparation => {
            ObservablePredicate::TypedError(TypedErrorObservation::NonExecutionSeparation)
        }
    }
}

fn common_harness_name(check: CommonHarnessCheck) -> &'static str {
    use CommonHarnessCheck::*;
    match check {
        DepsListSucceeds => "deps-list-succeeds",
        EngineRequiredReportsVersion => "engine-required-reports-version",
        GenerateExposesGenerator => "generate-exposes-generator",
        GenerateRespectsCwd => "generate-respects-cwd",
        GenerateSucceeds => "generate-succeeds",
        InitModuleDoesNotRemoveExistingFiles => "init-module-does-not-remove-existing-files",
        InitModuleDoesNotWriteConfig => "init-module-does-not-write-config",
        InitModuleHonorsCustomPath => "init-module-honors-custom-path",
        InitModuleSeedsFiles => "init-module-seeds-files",
        InitRecordsAuthoringSdk => "init-records-authoring-sdk",
        InitRegistersModule => "init-registers-module",
        InitScaffoldsModule => "init-scaffolds-module",
        InitWritesModuleConfig => "init-writes-module-config",
        InstallMarksAsSdk => "install-marks-as-sdk",
        InstallRegistersSdk => "install-registers-sdk",
        ScaffoldedModuleLoads => "scaffolded-module-loads",
        SdkReportsModuleOptions => "sdk-reports-module-options",
    }
}

fn harness_predicate(check: CommonHarnessCheck) -> ObservablePredicate {
    use CommonHarnessCheck::*;
    match check {
        InitModuleDoesNotRemoveExistingFiles | InitModuleDoesNotWriteConfig => {
            ObservablePredicate::Filesystem(FilesystemObservation::Preservation)
        }
        InitModuleHonorsCustomPath
        | InitModuleSeedsFiles
        | InitScaffoldsModule
        | InitWritesModuleConfig => ObservablePredicate::Filesystem(FilesystemObservation::Content),
        DepsListSucceeds => ObservablePredicate::Query(QueryObservation::Dependency),
        EngineRequiredReportsVersion => {
            ObservablePredicate::Compatibility(super::CompatibilityObservation::TargetVersion)
        }
        GenerateExposesGenerator | GenerateRespectsCwd | GenerateSucceeds => {
            ObservablePredicate::Result(ResultObservation::GeneratedSurface)
        }
        InitRecordsAuthoringSdk
        | InitRegistersModule
        | InstallMarksAsSdk
        | InstallRegistersSdk
        | SdkReportsModuleOptions => ObservablePredicate::Metadata(MetadataObservation::Definition),
        ScaffoldedModuleLoads => ObservablePredicate::Lifecycle(LifecycleObservation::Load),
    }
}

fn core_shape_capability(shape: CoreCaseShape) -> &'static str {
    match shape {
        CoreCaseShape::Scalar => "schema/engine-schema/schema-type/scalar/%53tring",
        CoreCaseShape::Enum => "schema/engine-schema/schema-type/enum/%43ache%53haring%4Dode",
        CoreCaseShape::Input => "schema/engine-schema/schema-type/input-object/%42uild%41rg",
        CoreCaseShape::Object => "schema/engine-schema/schema-type/object/%43ontainer",
        CoreCaseShape::Interface => "schema/engine-schema/schema-type/interface/%4Eode",
        CoreCaseShape::Nullable => "schema/engine-schema/schema-field/%43ontainer/env%56ariable",
        CoreCaseShape::ListObject => "schema/engine-schema/schema-field/%43ontainer/mounts",
        CoreCaseShape::ExpectedType => "schema/engine-schema/schema-type/object/%54ype%44ef",
        CoreCaseShape::Void => "schema/engine-schema/schema-type/scalar/%56oid",
    }
}

fn core_shape_predicate(shape: CoreCaseShape) -> ObservablePredicate {
    match shape {
        CoreCaseShape::Nullable => ObservablePredicate::Omission(OmissionObservation::Null),
        CoreCaseShape::ListObject => {
            ObservablePredicate::Result(ResultObservation::CollectionShape)
        }
        _ => ObservablePredicate::Query(QueryObservation::Core),
    }
}

fn engine_case_subjects(case: EngineIntegrationCase) -> BTreeSet<ImplementationSubject> {
    use EngineIntegrationCase::*;
    use ImplementationSubject::*;
    match case {
        Resolution => BTreeSet::from([BuiltinSdkResolution]),
        InitEmpty | InitNoGenerate => BTreeSet::from([WorkspaceInitialization]),
        InitExisting | NegativePathOwnership => BTreeSet::from([CargoProjectAdoption]),
        Operations => BTreeSet::from([OperationCompiler]),
        RuntimeChecked => BTreeSet::from([RuntimeProtocol]),
        RuntimeLegacy | NegativeGeneratedLockToolchain => BTreeSet::from([RuntimeConstruction]),
        NegativeRedaction => BTreeSet::from([EnginePackaging, CompletenessEvidence]),
    }
}

fn engine_case_predicate(case: EngineIntegrationCase) -> ObservablePredicate {
    use EngineIntegrationCase::*;
    match case {
        Resolution => {
            ObservablePredicate::Compatibility(super::CompatibilityObservation::Configuration)
        }
        InitEmpty | InitExisting | InitNoGenerate => {
            ObservablePredicate::Lifecycle(LifecycleObservation::Initialize)
        }
        Operations => ObservablePredicate::Result(ResultObservation::GeneratedSurface),
        RuntimeChecked | RuntimeLegacy => {
            ObservablePredicate::Lifecycle(LifecycleObservation::Invoke)
        }
        NegativeGeneratedLockToolchain | NegativePathOwnership | NegativeRedaction => {
            ObservablePredicate::TypedError(TypedErrorObservation::Category)
        }
    }
}

fn module_case_subjects(case: crate::ModuleSignoffCase) -> BTreeSet<ModuleImplementationSubject> {
    use crate::ModuleSignoffCase::*;
    use ModuleImplementationSubject::*;
    match case {
        Registration | Types => BTreeSet::from([TypeProjection]),
        ConstructorState => BTreeSet::from([TypeProjection, DispatchRuntime]),
        ExecutionShapes | NegativeDispatch => BTreeSet::from([SourceCompiler, DispatchRuntime]),
        ConcurrencyCancellation => BTreeSet::from([DispatchRuntime, ModuleContext]),
        HandlesContext => BTreeSet::from([ModuleContext]),
        PackagedSelfConsumer => BTreeSet::from([
            AuthoringBridge,
            SourceCompiler,
            TypeProjection,
            GeneratedAssets,
        ]),
        CommonHarness => BTreeSet::from([SourceCompiler, EvidenceBoundary]),
    }
}

fn module_case_predicate(case: crate::ModuleSignoffCase) -> ObservablePredicate {
    use crate::ModuleSignoffCase::*;
    match case {
        Registration => ObservablePredicate::Metadata(MetadataObservation::Definition),
        ConstructorState => ObservablePredicate::Omission(OmissionObservation::Default),
        ExecutionShapes => ObservablePredicate::Lifecycle(LifecycleObservation::Invoke),
        Types => ObservablePredicate::Result(ResultObservation::ExactValue),
        HandlesContext => ObservablePredicate::Query(QueryObservation::Module),
        NegativeDispatch => ObservablePredicate::TypedError(TypedErrorObservation::Category),
        ConcurrencyCancellation => {
            ObservablePredicate::Isolation(IsolationObservation::Cancellation)
        }
        PackagedSelfConsumer => ObservablePredicate::Filesystem(FilesystemObservation::Ownership),
        CommonHarness => ObservablePredicate::Lifecycle(LifecycleObservation::Load),
    }
}

fn client_case_subjects(case: crate::ClientSignoffCase) -> BTreeSet<ClientImplementationSubject> {
    use crate::ClientSignoffCase::*;
    use ClientImplementationSubject::*;
    match case {
        InitializedLocalClient => BTreeSet::from([Initialization, ProjectReconciliation]),
        PinnedRemoteClient => BTreeSet::from([WorkspaceSelection]),
        SchemaRegeneration => BTreeSet::from([SchemaCompiler, Publication]),
        CoreQuery => BTreeSet::from([CoreComposition, QueryRuntime]),
        NamespacedModuleQuery => BTreeSet::from([ModuleApi, QueryRuntime]),
    }
}

fn client_case_predicate(case: crate::ClientSignoffCase) -> ObservablePredicate {
    use crate::ClientSignoffCase::*;
    match case {
        InitializedLocalClient => ObservablePredicate::Lifecycle(LifecycleObservation::Initialize),
        PinnedRemoteClient => {
            ObservablePredicate::Compatibility(super::CompatibilityObservation::ImmutableReference)
        }
        SchemaRegeneration => ObservablePredicate::Filesystem(FilesystemObservation::Preservation),
        CoreQuery => ObservablePredicate::Query(QueryObservation::Core),
        NamespacedModuleQuery => ObservablePredicate::Query(QueryObservation::Module),
    }
}

fn fixed_program_label(program: &CaseProgram) -> String {
    match program {
        CaseProgram::CommonHarness { check } => {
            format!("common-harness/{}", common_harness_name(*check))
        }
        CaseProgram::StableConnector => "stable-connector".to_owned(),
        CaseProgram::CoreShape { shape } => format!("core/{}", core_shape_name(*shape)),
        CaseProgram::EngineIntegration { case } => {
            format!("engine-integration/{}", engine_case_name(*case))
        }
        CaseProgram::ModuleAuthoring { case } => {
            format!("module-authoring/{}", serde_name(case))
        }
        CaseProgram::StandaloneClient { case } => {
            format!("standalone-client/{}", serde_name(case))
        }
        CaseProgram::DefinitiveGoClient { behaviour } => {
            format!("definitive-go-client/{}", go_behaviour_name(*behaviour))
        }
        CaseProgram::IntegrationAssertion { fixture } => {
            format!("integration/{}", fixture.as_str())
        }
    }
}

fn serde_name<T: serde::Serialize>(value: &T) -> String {
    serde_json::to_value(value)
        .expect("closed enum serializes")
        .as_str()
        .expect("closed enum serializes as a string")
        .to_owned()
}

fn core_shape_name(shape: CoreCaseShape) -> &'static str {
    match shape {
        CoreCaseShape::Scalar => "scalar",
        CoreCaseShape::Enum => "enum",
        CoreCaseShape::Input => "input",
        CoreCaseShape::Object => "object",
        CoreCaseShape::Interface => "interface",
        CoreCaseShape::Nullable => "nullable",
        CoreCaseShape::ListObject => "list-object",
        CoreCaseShape::ExpectedType => "expected-type",
        CoreCaseShape::Void => "void",
    }
}

fn engine_case_name(case: EngineIntegrationCase) -> &'static str {
    use EngineIntegrationCase::*;
    match case {
        Resolution => "resolution",
        InitEmpty => "init-empty",
        InitExisting => "init-existing",
        InitNoGenerate => "init-no-generate",
        Operations => "operations",
        RuntimeChecked => "runtime-checked",
        RuntimeLegacy => "runtime-legacy",
        NegativeGeneratedLockToolchain => "negative-generated-lock-toolchain",
        NegativePathOwnership => "negative-path-ownership",
        NegativeRedaction => "negative-redaction",
    }
}

fn go_behaviour_name(behaviour: GoClientBehaviour) -> &'static str {
    use GoClientBehaviour::*;
    match behaviour {
        Directory => "directory",
        Git => "git",
        Container => "container",
        ContainerMutation => "container-mutation",
        List => "list",
        TypedExecError => "typed-exec-error",
        ExecErrorOutputFields => "exec-error-output-fields",
        ExecErrorEmptyOutput => "exec-error-empty-output",
        NonExecErrorSeparation => "non-exec-error-separation",
    }
}

fn fixture_id(assertion_id: &AssertionId) -> Result<ReviewedFixtureId, ConformanceDiagnosticSet> {
    let suffix = assertion_id
        .as_str()
        .strip_prefix("assertion/")
        .ok_or_else(|| planning_error("assertion identity lacks its namespace"))?;
    ReviewedFixtureId::new(format!("fixture/{suffix}"))
        .map_err(|_| planning_error("fixture identity exceeds the durable format"))
}

fn fixture_context(
    assertion_id: &AssertionId,
) -> Result<FixtureContextId, ConformanceDiagnosticSet> {
    let suffix = assertion_id
        .as_str()
        .strip_prefix("assertion/")
        .ok_or_else(|| planning_error("assertion identity lacks its namespace"))?;
    FixtureContextId::new(format!("context/{suffix}"))
        .map_err(|_| planning_error("fixture context exceeds the durable format"))
}

fn capability_ids(
    ledger: &ResolvedLedger,
    values: &[&str],
) -> Result<Vec<CapabilityId>, ConformanceDiagnosticSet> {
    values
        .iter()
        .map(|value| {
            let id = CapabilityId::new(*value)
                .map_err(|_| planning_error("fixed capability identity is invalid"))?;
            ledger
                .capabilities
                .contains_key(&id)
                .then_some(id)
                .ok_or_else(|| planning_error("fixed capability identity is absent"))
        })
        .collect()
}

fn network_policy(program: &CaseProgram) -> NetworkPolicyId {
    let value = match program {
        CaseProgram::StableConnector => "network/manifest-and-engine",
        CaseProgram::StandaloneClient {
            case: crate::ClientSignoffCase::PinnedRemoteClient,
        } => "network/immutable-remote",
        _ => "network/engine-only",
    };
    NetworkPolicyId::new(value).expect("closed network policy identity is valid")
}

fn retry_policy(program: &CaseProgram) -> RetryPolicy {
    let retryable = match program {
        CaseProgram::StableConnector => {
            CanonicalSet::new([InfrastructureFailureClass::OrchestrationTransportLost])
        }
        CaseProgram::StandaloneClient {
            case: crate::ClientSignoffCase::PinnedRemoteClient,
        } => CanonicalSet::new([InfrastructureFailureClass::ImmutableRemoteUnavailable]),
        _ => CanonicalSet::default(),
    };
    let attempts = if retryable.is_empty() { 1 } else { 2 };
    RetryPolicy {
        maximum_attempts: NonZeroCount::new(attempts).expect("attempt count is non-zero"),
        retryable,
    }
}

fn case_timeout(family: CaseFamily) -> NonZeroMillis {
    let millis = match family {
        CaseFamily::StableConnector | CaseFamily::DefinitiveGoClient => 300_000,
        CaseFamily::CoreGeneratedApi => 180_000,
        CaseFamily::CommonHarness
        | CaseFamily::EngineIntegration
        | CaseFamily::ModuleAuthoring
        | CaseFamily::StandaloneClient
        | CaseFamily::IntegrationAssertion => 600_000,
    };
    NonZeroMillis::new(millis).expect("closed case timeout is non-zero and bounded")
}

fn concurrency_class(program: &CaseProgram) -> ConcurrencyClass {
    match program {
        CaseProgram::StableConnector
        | CaseProgram::EngineIntegration {
            case:
                EngineIntegrationCase::InitEmpty
                | EngineIntegrationCase::InitExisting
                | EngineIntegrationCase::InitNoGenerate,
        } => ConcurrencyClass::ExclusiveMutation,
        CaseProgram::CoreShape { .. } | CaseProgram::DefinitiveGoClient { .. } => {
            ConcurrencyClass::SharedReadOnly
        }
        _ => ConcurrencyClass::IsolatedWorkspace,
    }
}

fn planning_error(detail: &'static str) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
        DiagnosticCoordinate {
            phase: Some(super::DiagnosticPhase::Catalog),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )])
    .expect("one planning diagnostic is non-empty")
}

fn planning_error_at_assertion(
    assertion_id: AssertionId,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
        DiagnosticCoordinate {
            phase: Some(super::DiagnosticPhase::Catalog),
            assertion_id: Some(assertion_id),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )])
    .expect("one planning diagnostic is non-empty")
}
