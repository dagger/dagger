//! End-to-end derivation of the checked-in Rust SDK completeness contract.
//!
//! This module is the filesystem orchestration boundary between authored inputs and the pure
//! validators. It loads only reviewed repository roots, revalidates normalized Go helper output,
//! reconstructs every derived artifact, and optionally compares those bytes with the active tree.
//! Network retrieval and harness execution remain separate explicit operations.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::Path;

use serde::Serialize;
use serde_json::json;

use crate::authority::{
    SourceCoverage, SourceItemCoverage, SourceItemDisposition, ValidatedAuthoritySources,
    validate_authority_registry, validate_authority_sources, validate_source_coverage,
};
use crate::canonical::{DigestDomain, canonical_bytes, canonical_digest, decode_canonical};
use crate::classification::resolve_classifications;
use crate::client_generation::{
    apply_client_ownership_correction, client_generation_scope_input,
    derive_client_generation_scope,
};
use crate::command::CommandPolicy;
use crate::compatibility::validate_compatibility_claim;
use crate::core_codegen::{
    CoreCodegenEvidencePolicy, CoreCodegenEvidenceRegistry, CoreCodegenScopeContract,
    GeneratedBindingManifest, ManifestBindingKind, apply_core_codegen_scope_correction,
    core_codegen_policy_contract, verify_core_codegen_evidence,
};
use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticSet, ToolError};
use crate::evidence::{
    EvidenceAuditContext, EvidenceEligibility, EvidenceSource, EvidenceSourceRegistry,
    audit_evidence_registry,
};
use crate::extract::go::{GoHelperOutput, adapt_go_output, adapt_go_output_without_version};
use crate::extract::harness::{HarnessRefresh, extract_harness, pinned_check_ids};
use crate::extract::policy::{
    PolicyClauseSelection, extract_policy_clauses, extract_test_handoff, merge_source_inventories,
};
use crate::extract::schema::{SchemaExtractionPolicy, decode_introspection, extract_schema};
use crate::feature_scope::{FeatureContractPolicy, reviewed_feature_contracts};
use crate::harness::{
    HarnessMappingContext, build_harness_check_inventory, validate_harness_mappings,
};
use crate::inventory::{
    CapabilityCandidate, CapabilityOrigin, build_inventory, derive_schema_candidates,
    encode_identity_segment, semantic_fingerprint,
};
use crate::io::{RepositoryRoots, SourceLoadError, load_source_bundles};
use crate::model::*;
use crate::observation::{TransportObservationRegistry, validate_transport_observations};
use crate::report::{build_report, render_human_report};
use crate::target::{TargetObservation, validate_target};
use crate::traceability::{
    CandidateStatusChanges, FeatureScopeDeclaration, apply_feature_status_changes,
    parse_feature_scope_declaration, validate_feature_scope_routing,
};

const DAGGER_AUTHORITY: &str = "github.com/dagger/dagger";
const GO_AUTHORITY: &str = "github.com/dagger/dagger-go-sdk";
const HARNESS_AUTHORITY: &str = "github.com/dagger/sdk-sdk";
const SCHEMA_AUTHORITY_ID: &str = "engine-schema";
const HARNESS_AUTHORITY_ID: &str = "sdk-contract-harness";
const INTEGRATION_AUTHORITY_ID: &str = "go-integration-tests";
const POLICY_AUTHORITY_ID: &str = "rust-policy";
const SCHEMA_SNAPSHOT: &str = "sdk/rust/completeness/snapshots/schema.json";
const HARNESS_SOURCE: &str = "sdk-sdk.dang";

#[derive(Clone, Debug, Eq, PartialEq)]
/// Complete deterministic products derived from the authored F1 contract inputs.
pub struct DerivedContract {
    /// Exhaustive normalized authority-source inventory.
    pub source_items: SourceItemInventory,
    /// Atomic schema, behavioural, and Rust-policy capability inventory.
    pub inventory: CanonicalInventory,
    /// Exactly one truthful classification for every active capability.
    pub ledger: ResolvedLedger,
    /// Machine-readable verdict and stable aggregation of the derived contract.
    pub report: CompletenessReport,
    /// Release metadata projected from the validated compatibility claim.
    pub release_metadata: ReleaseCompatibilityMetadata,
}

struct FeatureContractInput {
    policy: FeatureContractPolicy,
    requirements: String,
    declaration: FeatureScopeDeclaration,
}

/// Rebuilds the complete F1 contract from pinned authored inputs.
///
/// When `compare_checked` is true, any active derived artifact drift becomes an Integrity
/// diagnostic in the returned report. The derivation never writes to `repository_root` and uses
/// no network access.
pub fn derive_contract(
    repository_root: &Path,
    compare_checked: bool,
) -> Result<DerivedContract, ToolError> {
    let contract = repository_root.join("sdk/rust/completeness");
    let target: TargetDescriptor = read_canonical(&contract.join("target.json"), "target")?;
    let authorities: AuthorityRegistry =
        read_canonical(&contract.join("authorities.json"), "authorities")?;
    let definitions: CapabilityDefinitions =
        read_canonical(&contract.join("capabilities.json"), "capabilities")?;
    let classifications: ClassificationInput =
        read_canonical(&contract.join("classifications.json"), "classifications")?;
    let evidence: EvidenceRegistry = read_canonical(
        &contract.join("evidence/registry.json"),
        "evidence registry",
    )?;
    let transport_observations: TransportObservationRegistry = read_canonical(
        &contract.join("evidence/transport-observations.json"),
        "transport observations",
    )?;
    let mappings: HarnessMappings =
        read_canonical(&contract.join("harness-mappings.json"), "harness mappings")?;
    let compatibility: CompatibilityClaim =
        read_canonical(&contract.join("compatibility.json"), "compatibility claim")?;
    let core_codegen_scope: CoreCodegenScopeContract = read_canonical(
        &contract.join("core-codegen-scope.json"),
        "core codegen scope",
    )?;
    let core_codegen_manifest: GeneratedBindingManifest = read_canonical(
        &contract.join("artifacts/core-codegen-bindings.json"),
        "core codegen binding manifest",
    )?;
    let core_codegen_evidence: CoreCodegenEvidenceRegistry = read_canonical(
        &contract.join("evidence/core-codegen-registry.json"),
        "core codegen evidence registry",
    )?;
    let core_codegen_evidence_policy: CoreCodegenEvidencePolicy = read_canonical(
        &contract.join("evidence/core-codegen-policy.json"),
        "core codegen evidence policy",
    )?;
    let mut feature_inputs = Vec::new();
    for policy in reviewed_feature_contracts() {
        let requirements = read_text(
            &repository_root.join(policy.requirements_path),
            "feature requirements",
        )?;
        let declaration = if matches!(
            policy.scope.feature,
            FeatureId::Feature5 | FeatureId::Feature8
        ) {
            // Large exact scopes are intentionally stored once in reviewed machine-readable
            // policy. Re-parsing a second Markdown list would allow the two authorities to drift
            // before evidence assembly.
            FeatureScopeDeclaration {
                feature: policy.scope.feature.clone(),
                existing_capability_ids: policy.scope.existing_capability_ids.clone(),
                existing_scope_digest: policy.scope.existing_scope_digest.clone(),
                policy_capability_ids: policy.scope.policy_capability_ids.clone(),
            }
        } else {
            validated(
                parse_feature_scope_declaration(&requirements, &policy.scope),
                "feature scope declaration",
            )?
        };
        feature_inputs.push(FeatureContractInput {
            policy,
            requirements,
            declaration,
        });
    }
    let policy = core_codegen_policy_contract(&core_codegen_scope);
    let requirements = read_text(
        &repository_root.join(policy.requirements_path),
        "core codegen requirements",
    )?;
    let declaration = FeatureScopeDeclaration {
        feature: FeatureId::Feature4,
        existing_capability_ids: CanonicalSet::default(),
        existing_scope_digest: core_codegen_scope.retained.capability_ids_digest.clone(),
        policy_capability_ids: policy.scope.policy_capability_ids.clone(),
    };
    feature_inputs.push(FeatureContractInput {
        policy,
        requirements,
        declaration,
    });

    let schema_bytes = read(&contract.join("snapshots/schema.json"), "schema snapshot")?;
    let schema = decode_introspection(&schema_bytes).map_err(|_| ToolError::Decode {
        artifact: "schema snapshot",
    })?;
    let engine_go_output = read_go_output(&contract, "go-engine-sdk")?;
    let harness_source = read_text(
        &contract.join(format!(
            "sources/sdk-sdk/{}/{HARNESS_SOURCE}",
            target.sdk_contract_revision
        )),
        "harness source",
    )?;
    let observation = observe_target(
        repository_root,
        &target,
        &schema_bytes,
        &schema,
        &engine_go_output,
        &harness_source,
    )?;
    let validated_target = validated(
        validate_target(target.clone(), &observation),
        "validated target",
    )?;
    let validated_registry = validated(
        validate_authority_registry(&validated_target, authorities.clone()),
        "validated authorities",
    )?;
    let roots = repository_roots(repository_root, &contract, &target)?;
    let bundles = load_source_bundles(&validated_registry, &roots).map_err(source_load_error)?;
    let sources = validated(
        validate_authority_sources(validated_registry, bundles),
        "validated authority sources",
    )?;

    let source_items = extract_source_items(
        repository_root,
        &contract,
        &target,
        &schema,
        &harness_source,
        &feature_inputs,
    )?;
    let mut candidates = capability_candidates(&definitions, &authorities)?;
    for feature in &feature_inputs {
        candidates.extend(feature_policy_candidates(
            &feature.policy,
            &feature.declaration,
            &source_items,
            &authorities,
        )?);
    }
    let schema_candidates = validated(
        derive_schema_candidates(&source_items),
        "schema capability candidates",
    )?;
    let coverage = derive_coverage(&sources, &source_items, &candidates, &schema_candidates)?;
    let mut inventory = validated(
        build_inventory(
            &source_items,
            &coverage,
            candidates.into_iter().chain(schema_candidates),
        ),
        "canonical inventory",
    )?;
    attach_missing_anchors(&mut inventory, &source_items, &authorities)?;

    let baseline_ledger = validated(
        resolve_classifications(&inventory, &source_items, &classifications),
        "resolved ledger",
    )?;
    // The authored baseline is an intermediate routing state. Final candidate evidence
    // deliberately retires superseded baseline scopes, so validate evidence links only
    // after ownership correction and the evidence-backed status transition are applied.
    for feature in &feature_inputs {
        if matches!(
            feature.declaration.feature,
            FeatureId::Feature4 | FeatureId::Feature5
        ) {
            continue;
        }
        validated(
            validate_feature_scope_routing(
                &inventory,
                &baseline_ledger,
                &feature.declaration,
                &feature.policy.scope,
            ),
            "feature scope routing",
        )?;
    }
    let core_codegen = validated(
        apply_core_codegen_scope_correction(&inventory, &baseline_ledger, &core_codegen_scope),
        "core codegen scope correction",
    )?;
    validated(
        validate_feature_scope_routing(
            &inventory,
            &core_codegen.ledger,
            &core_codegen.declaration,
            &core_codegen.policy,
        ),
        "core codegen scope routing",
    )?;
    let engine_integration = feature_inputs
        .iter()
        .find(|feature| feature.declaration.feature == FeatureId::Feature5)
        .expect("reviewed feature inputs always contain engine integration");
    // Validate this scope after core-codegen ownership correction, which routes its
    // 19 retained Go-codegen responsibilities to the engine-integration owner.
    validated(
        validate_feature_scope_routing(
            &inventory,
            &core_codegen.ledger,
            &engine_integration.declaration,
            &engine_integration.policy.scope,
        ),
        "engine integration scope routing",
    )?;

    let target_digest = TargetDigest::new(
        canonical_digest(DigestDomain::Target, &target).map_err(|_| ToolError::Decode {
            artifact: "target digest",
        })?,
    );
    let client_scope = derive_client_generation_scope(
        &client_generation_scope_input(target_digest.clone()),
        &target_digest,
    )
    .map_err(|_| ToolError::Decode {
        artifact: "client generation capability scope",
    })?;
    let client_corrected_ledger =
        apply_client_ownership_correction(&core_codegen.ledger, &client_scope).map_err(|_| {
            ToolError::Decode {
                artifact: "client generation ownership correction",
            }
        })?;
    let core_codegen_closure = verify_core_codegen_evidence(
        &core_codegen_manifest,
        &core_codegen_evidence,
        &core_codegen_evidence_policy,
    );
    if !core_codegen_closure.expired_evidence_ids().is_empty() {
        return Err(ToolError::Decode {
            artifact: "core codegen evidence freshness",
        });
    }
    let implementation_evidence = EvidenceId::new("implementation/core-codegen/generated-client")
        .expect("static evidence identity is valid");
    let verification_evidence = EvidenceId::new("verification/core-codegen/release-closure")
        .expect("static evidence identity is valid");
    let mut candidate = CandidateStatusChanges::default();
    for capability_id in core_codegen_closure.closed_capability_ids() {
        let binding =
            core_codegen_manifest
                .bindings
                .get(capability_id)
                .ok_or(ToolError::Decode {
                    artifact: "core codegen closed binding",
                })?;
        if binding.binding_kind == ManifestBindingKind::IdiomaticEquivalent
            || binding.decision_id.is_some()
        {
            return Err(ToolError::Decode {
                artifact: "core codegen direct status transition",
            });
        }
        candidate.changes.insert(
            capability_id.clone(),
            ClassificationValues {
                status: Status::Implemented,
                gap: None,
                owner_feature: None,
                implementation_evidence: CanonicalSet::new([implementation_evidence.clone()]),
                verification_evidence: CanonicalSet::new([verification_evidence.clone()]),
                decision_evidence: CanonicalSet::default(),
            },
        );
    }
    let ledger = validated(
        apply_feature_status_changes(
            &client_corrected_ledger,
            &core_codegen.declaration,
            &core_codegen.policy,
            &candidate,
            &evidence,
            &target_digest,
            &BTreeMap::new(),
            false,
        ),
        "core codegen status transition",
    )?;
    let command_policy = command_policy()?;
    let evidence_sources = evidence_sources(
        repository_root,
        &roots,
        &authorities,
        &source_items,
        &evidence,
    )?;
    let audit_context = EvidenceAuditContext {
        authorities: &authorities,
        sources: &evidence_sources,
        inventory: &inventory,
        target: &target_digest,
        command_policy: &command_policy,
    };
    validated(
        audit_evidence_registry(evidence.clone(), &ledger, &audit_context),
        "evidence audit",
    )?;
    validated(
        validate_transport_observations(
            &transport_observations,
            &evidence,
            &target,
            &target_digest,
            &crate::feature_scope::transport_contract()
                .scope
                .capability_ids(),
            &ledger,
        ),
        "transport observations",
    )?;

    let checks = validated(
        build_harness_check_inventory(&source_items, &target.sdk_contract_revision),
        "harness check inventory",
    )?;
    let cli_digest = cli_executable_digest(&contract)?;
    let rust_digest = rust_artifact_digest(repository_root)?;
    let mapping_context = HarnessMappingContext {
        harness_revision: &target.sdk_contract_revision,
        target: &target_digest,
        cli_artifact_digest: &cli_digest,
        verified_artifact_digest: &rust_digest,
        command_policy: &command_policy,
    };
    validated(
        validate_harness_mappings(mappings, &checks, &inventory, &evidence, &mapping_context),
        "harness mappings",
    )?;

    let mut targets = BTreeMap::new();
    targets.insert(target_digest, target.clone());
    let compatibility = validated(
        validate_compatibility_claim(compatibility, &targets, &evidence, &inventory, &ledger),
        "compatibility claim",
    )?;

    let mut diagnostics = Vec::new();
    if compare_checked {
        compare_artifact(
            &contract.join("artifacts/source-items.json"),
            &source_items,
            DiagnosticCode::AuthorityDrift,
            "source-items",
            &mut diagnostics,
        )?;
        compare_artifact(
            &contract.join("artifacts/inventory.json"),
            &inventory,
            DiagnosticCode::LedgerDrift,
            "inventory",
            &mut diagnostics,
        )?;
        compare_artifact(
            &contract.join("artifacts/ledger.json"),
            &ledger,
            DiagnosticCode::LedgerDrift,
            "ledger",
            &mut diagnostics,
        )?;
    }
    let preliminary = build_report(
        target.contract_format_version.clone(),
        &target,
        &authorities,
        &inventory,
        &ledger,
        diagnostics.clone(),
    );
    if compare_checked {
        compare_report(&contract, &preliminary, &mut diagnostics)?;
    }
    let report = build_report(
        target.contract_format_version.clone(),
        &target,
        &authorities,
        &inventory,
        &ledger,
        diagnostics,
    );
    Ok(DerivedContract {
        source_items,
        inventory,
        ledger,
        report,
        release_metadata: compatibility.release_metadata(),
    })
}

fn observe_target(
    repository_root: &Path,
    target: &TargetDescriptor,
    schema_bytes: &[u8],
    schema: &crate::extract::schema::IntrospectionResponse,
    go_output: &GoHelperOutput,
    harness_source: &str,
) -> Result<TargetObservation, ToolError> {
    let engine_version = read_text(
        &repository_root.join("internal/version/VERSION"),
        "engine version",
    )?;
    let cargo = read_text(
        &repository_root.join("sdk/rust/Cargo.toml"),
        "Rust workspace",
    )?;
    let toolchain = read_text(
        &repository_root.join("sdk/rust/rust-toolchain.toml"),
        "Rust toolchain",
    )?;
    if !cargo.contains(&format!("version = \"{}\"", target.rust_sdk_version))
        || !cargo.contains(&format!("edition = \"{}\"", target.rust_edition))
        || !cargo.contains(&format!("rust-version = \"{}\"", target.rust_version))
        || !toolchain.contains(&format!("channel = \"{}\"", target.rust_version))
        || !harness_source.contains(&format!(
            "daggerCliVersion: String! = \"{}\"",
            target.sdk_contract_cli_version.version()
        ))
    {
        return Err(ToolError::Decode {
            artifact: "observed target metadata",
        });
    }
    let go_revision = go_output
        .go_sdk_lib_version
        .as_deref()
        .ok_or(ToolError::Decode {
            artifact: "Go SDK literal",
        })?;
    Ok(TargetObservation {
        contract_format_version: target.contract_format_version.clone(),
        dagger_repository: RepositoryId::new(DAGGER_AUTHORITY)
            .expect("static repository identity is valid"),
        dagger_revision: target.dagger_revision.clone(),
        engine_version: DaggerVersion::new(engine_version.trim()).map_err(|_| {
            ToolError::Decode {
                artifact: "engine version",
            }
        })?,
        schema_version: NonEmptyText::new(&schema.schema_version).map_err(|_| {
            ToolError::Decode {
                artifact: "schema version",
            }
        })?,
        schema_digest: Digest::sha256(schema_bytes),
        go_sdk_repository: RepositoryId::new(GO_AUTHORITY)
            .expect("static repository identity is valid"),
        engine_selected_go_revision: CommitSha::new(go_revision).map_err(|_| {
            ToolError::Decode {
                artifact: "Go SDK literal",
            }
        })?,
        go_version_label_resolution: None,
        sdk_contract_repository: RepositoryId::new(HARNESS_AUTHORITY)
            .expect("static repository identity is valid"),
        sdk_contract_revision: target.sdk_contract_revision.clone(),
        harness_cli_version: target.sdk_contract_cli_version.clone(),
        harness_engine_version: target.sdk_contract_cli_version.clone(),
        rust_sdk_version: target.rust_sdk_version.clone(),
        rust_edition: target.rust_edition.clone(),
        rust_version: target.rust_version.clone(),
        source_digest_mismatches: CanonicalSet::default(),
    })
}

fn repository_roots(
    repository_root: &Path,
    contract: &Path,
    target: &TargetDescriptor,
) -> Result<RepositoryRoots, ToolError> {
    Ok(RepositoryRoots::new([
        (
            RepositoryId::new(DAGGER_AUTHORITY).expect("static repository identity is valid"),
            repository_root.to_path_buf(),
        ),
        (
            RepositoryId::new(GO_AUTHORITY).expect("static repository identity is valid"),
            repository_root.join("sdk/go"),
        ),
        (
            RepositoryId::new(HARNESS_AUTHORITY).expect("static repository identity is valid"),
            contract.join(format!("sources/sdk-sdk/{}", target.sdk_contract_revision)),
        ),
    ]))
}

fn extract_source_items(
    repository_root: &Path,
    contract: &Path,
    target: &TargetDescriptor,
    schema: &crate::extract::schema::IntrospectionResponse,
    harness_source: &str,
    feature_inputs: &[FeatureContractInput],
) -> Result<SourceItemInventory, ToolError> {
    let schema_authority =
        AuthorityId::new(SCHEMA_AUTHORITY_ID).expect("static authority identity is valid");
    let schema_policy = SchemaExtractionPolicy {
        excluded_type_names: schema
            .schema
            .types
            .iter()
            .filter(|schema_type| schema_type.name.starts_with("__"))
            .map(|schema_type| schema_type.name.clone())
            .collect(),
        excluded_applied_directive_names: BTreeSet::new(),
    };
    let schema_items = validated(
        extract_schema(
            &schema_authority,
            target.schema_version.as_str(),
            schema,
            &schema_policy,
        ),
        "schema extraction",
    )?;

    let mut inventories = vec![schema_items];
    for authority in [
        "go-client",
        "go-codegen",
        "go-engine-sdk",
        INTEGRATION_AUTHORITY_ID,
    ] {
        let bytes = read(
            &contract.join(format!("sources/go/{authority}.json")),
            "Go helper output",
        )?;
        let authority_id = AuthorityId::new(authority).expect("static authority ID is valid");
        let extracted = if authority == "go-engine-sdk" {
            adapt_go_output(&authority_id, &bytes, target.go_sdk_revision.as_str())
        } else {
            adapt_go_output_without_version(&authority_id, &bytes)
        };
        inventories.push(validated(extracted, "Go helper output")?);
    }

    let harness_authority =
        AuthorityId::new(HARNESS_AUTHORITY_ID).expect("static authority identity is valid");
    inventories.push(validated(
        extract_harness(
            &harness_authority,
            harness_source,
            &HarnessRefresh {
                check_ids: pinned_check_ids(),
                require_sdk_target: true,
                require_mod_test: true,
            },
        ),
        "harness extraction",
    )?);

    let integration_authority =
        AuthorityId::new(INTEGRATION_AUTHORITY_ID).expect("static authority identity is valid");
    let handoff = read_text(
        &repository_root.join("future/sdk-tests.md"),
        "removed-test handoff",
    )?;
    inventories.push(validated(
        extract_test_handoff(
            &integration_authority,
            &handoff,
            &CommitSha::new("200e400d5a1463e78b1d52001394d77f743c290a")
                .expect("pinned recovery commit is valid"),
        ),
        "removed-test handoff",
    )?);

    let policy_authority =
        AuthorityId::new(POLICY_AUTHORITY_ID).expect("static authority identity is valid");
    let guidance_path = "sdk/rust/AGENTS.md";
    let guidance = read_text(&repository_root.join(guidance_path), "Rust guidance")?;
    inventories.push(validated(
        extract_policy_clauses(
            &policy_authority,
            guidance_path,
            &guidance,
            &[
                policy(
                    "idiomatic-rust",
                    "Cross-SDK consistency does not justify an unidiomatic Rust API.",
                ),
                policy(
                    "why-comments",
                    "Comments explain why a decision is necessary, not what the next line does.",
                ),
                policy("unsafe-denied", "Unsafe Rust is denied at workspace level."),
                policy(
                    "locked-resolution",
                    "resolution changes are deliberate reviewable changes, never a CI side effect.",
                ),
                policy(
                    "dependency-policy",
                    "New dependencies are design decisions.",
                ),
                policy("cargo-deny", "`cargo deny check` must pass."),
                policy("typed-public-errors", "Prefer typed public errors."),
                policy(
                    "panic-free-library",
                    "Avoid `unwrap` and `panic` in library paths.",
                ),
                policy(
                    "explicit-ownership",
                    "Prefer explicit ownership and borrowing over pervasive cloning or shared `Arc`\n  state.",
                ),
                policy(
                    "secret-safe-output",
                    "Never expose session tokens, registry credentials, environment secrets, or sensitive\n  host paths in errors, tracing, fixtures, snapshots, or generated code.",
                ),
            ],
        ),
        "Rust policy extraction",
    )?);
    for feature in feature_inputs {
        inventories.push(validated(
            extract_policy_clauses(
                &policy_authority,
                feature.policy.requirements_path,
                &feature.requirements,
                &feature
                    .policy
                    .policy_clauses
                    .iter()
                    .map(|clause| policy(clause.clause_id, clause.exact_text))
                    .collect::<Vec<_>>(),
            ),
            "feature policy extraction",
        )?);
    }
    validated(
        merge_source_inventories(inventories),
        "merged source inventory",
    )
}

fn policy(id: &str, text: &str) -> PolicyClauseSelection {
    PolicyClauseSelection {
        clause_id: id.to_owned(),
        exact_text: text.to_owned(),
    }
}

fn capability_candidates(
    definitions: &CapabilityDefinitions,
    authorities: &AuthorityRegistry,
) -> Result<Vec<CapabilityCandidate>, ToolError> {
    definitions
        .capabilities
        .values()
        .map(|definition| {
            let class = authorities
                .authorities
                .get(&definition.authority_id)
                .map(|authority| &authority.authority_class)
                .ok_or(ToolError::Decode {
                    artifact: "capability authority",
                })?;
            let origin = match class {
                AuthorityClass::SdkContractHarness => CapabilityOrigin::Harness,
                AuthorityClass::GoClient
                | AuthorityClass::GoEngineSdk
                | AuthorityClass::GoCodegen
                | AuthorityClass::GoIntegrationTests => CapabilityOrigin::Go,
                AuthorityClass::EngineSchema | AuthorityClass::RustPolicy => {
                    CapabilityOrigin::Independent
                }
            };
            Ok(CapabilityCandidate {
                definition: definition.clone(),
                origin,
                common_contract: false,
                target_compatible: true,
            })
        })
        .collect()
}

fn feature_policy_candidates(
    policy: &FeatureContractPolicy,
    declaration: &FeatureScopeDeclaration,
    source_items: &SourceItemInventory,
    authorities: &AuthorityRegistry,
) -> Result<Vec<CapabilityCandidate>, ToolError> {
    let authority_id =
        AuthorityId::new(POLICY_AUTHORITY_ID).expect("static authority identity is valid");
    if !authorities.authorities.contains_key(&authority_id) {
        return Err(ToolError::Decode {
            artifact: "feature policy authority",
        });
    }

    policy
        .policy_clauses
        .iter()
        .map(|clause| {
            let clause_id = clause.clause_id;
            let capability_id = CapabilityId::new(format!(
                "policy/{POLICY_AUTHORITY_ID}/{clause_id}"
            ))
            .map_err(|_| ToolError::Decode {
                artifact: "feature policy identity",
            })?;
            if !declaration.policy_capability_ids.contains(&capability_id) {
                return Err(ToolError::Decode {
                    artifact: "declared feature policy identity",
                });
            }
            let spec_source_id = policy_source_item_id(clause_id)?;
            let guidance_source_id = policy_source_item_id(clause.guidance_id)?;
            let spec_source = source_items
                .items
                .get(&spec_source_id)
                .ok_or(ToolError::Decode {
                    artifact: "feature policy source",
                })?;
            let guidance_source =
                source_items
                    .items
                    .get(&guidance_source_id)
                    .ok_or(ToolError::Decode {
                        artifact: "feature guidance source",
                    })?;
            let semantic_signature = json!({
                "requirement": spec_source.semantic_signature,
                "rust_guidance": guidance_source.semantic_signature,
            });
            Ok(CapabilityCandidate {
                definition: CapabilityDefinition {
                    capability_id,
                    authority_id: authority_id.clone(),
                    capability_kind: CapabilityKind::new("rust-policy")
                        .expect("static capability kind is valid"),
                    source_item_ids: CanonicalSet::new([spec_source_id, guidance_source_id]),
                    // Anchors are derived after coverage has validated both selected source items;
                    // storing copied line numbers in authored JSON would make harmless prose motion
                    // look like a semantic definition change.
                    source_anchors: CanonicalSet::default(),
                    // Exact Markdown selections may include source line wrapping. The durable
                    // semantic signature retains those bytes, while the user-facing summary uses
                    // one portable line accepted by NonEmptyText.
                    summary: NonEmptyText::new(
                        clause
                            .exact_text
                            .split_whitespace()
                            .collect::<Vec<_>>()
                            .join(" "),
                    )
                    .map_err(|_| ToolError::Decode {
                        artifact: "feature policy summary",
                    })?,
                    capability_fingerprint: semantic_fingerprint(&semantic_signature).map_err(
                        |_| ToolError::Decode {
                            artifact: "feature policy fingerprint",
                        },
                    )?,
                    semantic_signature,
                    stability: Stability::Stable,
                },
                origin: CapabilityOrigin::Independent,
                common_contract: false,
                target_compatible: true,
            })
        })
        .collect()
}

fn policy_source_item_id(clause_id: &str) -> Result<SourceItemId, ToolError> {
    SourceItemId::new(format!(
        "source/{POLICY_AUTHORITY_ID}/rust-policy/{}",
        encode_identity_segment(clause_id)
    ))
    .map_err(|_| ToolError::Decode {
        artifact: "feature policy source identity",
    })
}

fn derive_coverage(
    sources: &ValidatedAuthoritySources,
    source_items: &SourceItemInventory,
    candidates: &[CapabilityCandidate],
    schema_candidates: &[CapabilityCandidate],
) -> Result<crate::authority::ValidatedSourceCoverage, ToolError> {
    let mut by_source = BTreeMap::<SourceItemId, BTreeSet<CapabilityId>>::new();
    for candidate in candidates.iter().chain(schema_candidates) {
        for source_item_id in candidate.definition.source_item_ids.iter() {
            by_source
                .entry(source_item_id.clone())
                .or_default()
                .insert(candidate.definition.capability_id.clone());
        }
    }
    let mut coverage = SourceCoverage::default();
    for (source_item_id, item) in &source_items.items {
        let authority = sources
            .registry()
            .authorities
            .get(&item.authority_id)
            .ok_or(ToolError::Decode {
                artifact: "source-item authority",
            })?;
        let path = source_item_path(item)?;
        let exclusion = authority
            .exclude
            .iter()
            .find(|exclusion| selector_matches_item(&exclusion.selector, &path, &item.locator));
        let (selected_by, disposition) = if let Some(exclusion) = exclusion {
            (
                exclusion.selector.clone(),
                SourceItemDisposition::Excluded(exclusion.clone()),
            )
        } else {
            let capabilities = by_source.get(source_item_id).ok_or(ToolError::Decode {
                artifact: "source-item coverage",
            })?;
            (
                SourceSelector::Path(PathSourceSelector { path }),
                SourceItemDisposition::Primary(CanonicalSet::new(capabilities.clone())),
            )
        };
        coverage.items.insert(
            source_item_id.clone(),
            SourceItemCoverage {
                source_item_id: source_item_id.clone(),
                selected_by,
                disposition,
            },
        );
    }
    validated(
        validate_source_coverage(sources, source_items, coverage),
        "source coverage",
    )
}

fn selector_matches_item(
    selector: &SourceSelector,
    path: &RepositoryRelativePath,
    locator: &SourceLocator,
) -> bool {
    match selector {
        SourceSelector::Path(selected) => &selected.path == path,
        SourceSelector::Symbol(selected) => &selected.path == path && &selected.locator == locator,
    }
}

fn source_item_path(item: &SourceItem) -> Result<RepositoryRelativePath, ToolError> {
    if item.authority_id.as_str() == SCHEMA_AUTHORITY_ID {
        return RepositoryRelativePath::new(SCHEMA_SNAPSHOT).map_err(|_| ToolError::Decode {
            artifact: "schema source path",
        });
    }
    if item.authority_id.as_str() == HARNESS_AUTHORITY_ID {
        return RepositoryRelativePath::new(HARNESS_SOURCE).map_err(|_| ToolError::Decode {
            artifact: "harness source path",
        });
    }
    let path = item.locator.as_str().split(':').next().unwrap_or_default();
    RepositoryRelativePath::new(path).map_err(|_| ToolError::Decode {
        artifact: "source-item path",
    })
}

fn attach_missing_anchors(
    inventory: &mut CanonicalInventory,
    source_items: &SourceItemInventory,
    authorities: &AuthorityRegistry,
) -> Result<(), ToolError> {
    for definition in inventory.capabilities.values_mut() {
        if !definition.source_anchors.is_empty() {
            continue;
        }
        let authority = authorities
            .authorities
            .get(&definition.authority_id)
            .ok_or(ToolError::Decode {
                artifact: "capability authority anchor",
            })?;
        let multiple_sources = definition.source_item_ids.len() > 1;
        let anchors = definition
            .source_item_ids
            .iter()
            .map(|source_item_id| {
                let source_item =
                    source_items
                        .items
                        .get(source_item_id)
                        .ok_or(ToolError::Decode {
                            artifact: "capability authority source",
                        })?;
                let suffix = if multiple_sources {
                    format!("/{}", encode_identity_segment(source_item_id.as_str()))
                } else {
                    String::new()
                };
                Ok(EvidenceReference {
                    evidence_id: EvidenceId::new(format!(
                        "authority/{}{}",
                        definition.capability_id, suffix
                    ))
                    .map_err(|_| ToolError::Decode {
                        artifact: "capability authority evidence ID",
                    })?,
                    evidence_kind: EvidenceKind::Authority,
                    repository: authority.repository.clone(),
                    revision: authority.revision.clone(),
                    path: source_item_path(source_item)?,
                    locator: source_item.locator.clone(),
                    claim: NonEmptyText::new(
                        "Pinned authority source defines this reviewed capability",
                    )
                    .expect("static evidence claim is valid"),
                    command: None,
                    expected_outcome: None,
                    execution_target: None,
                    platform_scope: CanonicalSet::default(),
                    proved_capability_ids: CanonicalSet::new([definition.capability_id.clone()]),
                })
            })
            .collect::<Result<Vec<_>, ToolError>>()?;
        definition.source_anchors = CanonicalSet::new(anchors);
    }
    Ok(())
}

fn evidence_sources(
    repository_root: &Path,
    roots: &RepositoryRoots,
    authorities: &AuthorityRegistry,
    source_items: &SourceItemInventory,
    evidence: &EvidenceRegistry,
) -> Result<EvidenceSourceRegistry, ToolError> {
    let mut sources = BTreeSet::new();
    for item in source_items.items.values() {
        let authority =
            authorities
                .authorities
                .get(&item.authority_id)
                .ok_or(ToolError::Decode {
                    artifact: "evidence authority",
                })?;
        let eligibility = match item.state {
            SourceItemState::HarnessSelf => EvidenceEligibility::HarnessSelf,
            SourceItemState::Skipped | SourceItemState::Removed => EvidenceEligibility::SourceOnly,
            SourceItemState::Active | SourceItemState::Deprecated
                if item.item_kind.as_str().contains("test") =>
            {
                EvidenceEligibility::ExecutableAssertion
            }
            SourceItemState::Active | SourceItemState::Deprecated => {
                EvidenceEligibility::SourceOnly
            }
        };
        sources.insert(EvidenceSource {
            repository: authority.repository.clone(),
            revision: authority.revision.clone(),
            path: source_item_path(item)?,
            locator: item.locator.clone(),
            state: item.state.clone(),
            eligibility,
        });
    }
    for reference in evidence.evidence.values() {
        let root = roots.get(&reference.repository).ok_or(ToolError::Decode {
            artifact: "evidence repository root",
        })?;
        let physical = root.join(reference.path.as_str());
        if !physical.is_file() {
            return Err(ToolError::Decode {
                artifact: "evidence source path",
            });
        }
        let eligibility = if reference.evidence_kind == EvidenceKind::Verification {
            EvidenceEligibility::ExecutableAssertion
        } else {
            EvidenceEligibility::SourceOnly
        };
        sources.insert(EvidenceSource {
            repository: reference.repository.clone(),
            revision: reference.revision.clone(),
            path: reference.path.clone(),
            locator: reference.locator.clone(),
            state: SourceItemState::Active,
            eligibility,
        });
    }
    let _ = repository_root;
    Ok(EvidenceSourceRegistry::new(sources))
}

fn command_policy() -> Result<CommandPolicy, ToolError> {
    Ok(CommandPolicy {
        programs: BTreeSet::from([
            ExecutableId::new("cargo").expect("static executable is valid"),
            ExecutableId::new("dagger").expect("static executable is valid"),
        ]),
        working_directories: BTreeSet::from([
            RepositoryRelativePath::new("sdk/rust").expect("static workdir is valid")
        ]),
        environment_keys: BTreeSet::new(),
    })
}

#[derive(serde::Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct CliProvenance {
    archive: String,
    archive_digest: Digest,
    download_url: String,
    engine_image: String,
    engine_linux_amd64_manifest_digest: Digest,
    engine_multiarch_index_digest: Digest,
    executable_digest: Digest,
    platform: String,
    release: String,
}

fn cli_executable_digest(contract: &Path) -> Result<Digest, ToolError> {
    let provenance: CliProvenance = read_canonical(
        &contract.join("sources/dagger-cli/v1.0.0-beta.9/provenance.json"),
        "CLI provenance",
    )?;
    if provenance.archive != "dagger_v1.0.0-beta.9_linux_amd64.tar.gz"
        || provenance.archive_digest
            != Digest::new(
                "sha256:776a390ecef59ff2ad8c0a3b3ca6d793bb62556bb8a512f475a725bdc830e40c",
            )
            .expect("published archive digest is valid")
        || provenance.download_url
            != "https://dl.dagger.io/dagger/releases/1.0.0-beta.9/dagger_v1.0.0-beta.9_linux_amd64.tar.gz"
        || provenance.engine_image != "registry.dagger.io/engine:v1.0.0-beta.9"
        || provenance.engine_linux_amd64_manifest_digest
            != Digest::new(
                "sha256:df96f6801fea0f511b1c62e143461af7daa6074216d62ea8f092e035c4afaffd",
            )
            .expect("published engine manifest digest is valid")
        || provenance.engine_multiarch_index_digest
            != Digest::new(
                "sha256:de22dbf0c848d618efa9243f76fd47364110d31bb2e24cce063b702e91e1b73e",
            )
            .expect("published engine index digest is valid")
        || provenance.platform != "linux/amd64"
        || provenance.release != "v1.0.0-beta.9"
    {
        return Err(ToolError::Decode {
            artifact: "CLI provenance",
        });
    }
    Ok(provenance.executable_digest)
}

#[derive(Serialize)]
struct RustArtifactFile {
    path: RepositoryRelativePath,
    digest: Digest,
}

/// Computes the exact normalized Rust workspace identity assessed by the harness profile.
pub fn rust_artifact_digest(repository_root: &Path) -> Result<Digest, ToolError> {
    let rust = repository_root.join("sdk/rust");
    let mut files = Vec::new();
    for relative in [
        "Cargo.toml",
        "Cargo.lock",
        "rust-toolchain.toml",
        "crates/dagger-sdk",
        "crates/dagger-codegen",
        "crates/dagger-bootstrap",
    ] {
        collect_rust_files(&rust, Path::new(relative), &mut files)?;
    }
    files.sort_by(|left, right| left.path.cmp(&right.path));
    canonical_digest(DigestDomain::Source, &files).map_err(|_| ToolError::Decode {
        artifact: "Rust artifact digest",
    })
}

fn collect_rust_files(
    rust_root: &Path,
    relative: &Path,
    files: &mut Vec<RustArtifactFile>,
) -> Result<(), ToolError> {
    let physical = rust_root.join(relative);
    let metadata = fs::symlink_metadata(&physical)
        .map_err(|error| ToolError::io("inspect Rust artifact", &error, None))?;
    if metadata.file_type().is_symlink() {
        return Err(ToolError::Decode {
            artifact: "Rust artifact symlink",
        });
    }
    if metadata.is_dir() {
        let mut entries = fs::read_dir(&physical)
            .map_err(|error| ToolError::io("enumerate Rust artifact", &error, None))?
            .collect::<Result<Vec<_>, _>>()
            .map_err(|error| ToolError::io("enumerate Rust artifact", &error, None))?;
        entries.sort_by_key(std::fs::DirEntry::file_name);
        for entry in entries {
            let child = relative.join(entry.file_name());
            if child
                .components()
                .any(|component| component.as_os_str() == "target")
            {
                continue;
            }
            collect_rust_files(rust_root, &child, files)?;
        }
    } else if metadata.is_file() {
        let logical = RepositoryRelativePath::new(relative.to_string_lossy().replace('\\', "/"))
            .map_err(|_| ToolError::Decode {
                artifact: "Rust artifact path",
            })?;
        files.push(RustArtifactFile {
            path: logical,
            digest: Digest::sha256(read(&physical, "Rust artifact")?),
        });
    }
    Ok(())
}

fn compare_artifact<T>(
    path: &Path,
    derived: &T,
    code: DiagnosticCode,
    subject: &str,
    diagnostics: &mut Vec<ContractDiagnostic>,
) -> Result<(), ToolError>
where
    T: Serialize,
{
    let expected = canonical_bytes(derived).map_err(|_| ToolError::Decode {
        artifact: "derived artifact",
    })?;
    if read(path, "checked artifact")? != expected {
        diagnostics.push(ContractDiagnostic::new(
            code,
            subject,
            None,
            "checked-in artifact differs from fresh deterministic derivation",
        ));
    }
    Ok(())
}

fn compare_report(
    contract: &Path,
    derived: &CompletenessReport,
    diagnostics: &mut Vec<ContractDiagnostic>,
) -> Result<(), ToolError> {
    let json = canonical_bytes(derived).map_err(|_| ToolError::Decode {
        artifact: "derived report",
    })?;
    if read(&contract.join("artifacts/report.json"), "checked report")? != json {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::ReportErrorSetMismatch,
            "artifacts/report.json",
            None,
            "checked report differs from the freshly derived report",
        ));
    }
    if read(
        &contract.join("artifacts/report.md"),
        "checked human report",
    )? != render_human_report(derived).as_bytes()
    {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::ReportErrorSetMismatch,
            "artifacts/report.md",
            None,
            "human report is not the pure projection of fresh report data",
        ));
    }
    Ok(())
}

fn read_go_output(contract: &Path, authority: &str) -> Result<GoHelperOutput, ToolError> {
    serde_json::from_slice(&read(
        &contract.join(format!("sources/go/{authority}.json")),
        "Go helper output",
    )?)
    .map_err(|_| ToolError::Decode {
        artifact: "Go helper output",
    })
}

fn read_canonical<T>(path: &Path, artifact: &'static str) -> Result<T, ToolError>
where
    T: serde::de::DeserializeOwned + Serialize,
{
    decode_canonical(&read(path, artifact)?).map_err(|_| ToolError::Decode { artifact })
}

fn read(path: &Path, operation: &'static str) -> Result<Vec<u8>, ToolError> {
    fs::read(path).map_err(|error| ToolError::io(operation, &error, None))
}

fn read_text(path: &Path, operation: &'static str) -> Result<String, ToolError> {
    String::from_utf8(read(path, operation)?).map_err(|_| ToolError::Decode {
        artifact: "UTF-8 source",
    })
}

fn validated<T>(result: Result<T, DiagnosticSet>, artifact: &'static str) -> Result<T, ToolError> {
    result.map_err(|_| ToolError::Decode { artifact })
}

fn source_load_error(error: SourceLoadError) -> ToolError {
    match error {
        SourceLoadError::Tool(error) => error,
        SourceLoadError::Contract(_) => ToolError::Decode {
            artifact: "authority source bundle",
        },
    }
}
