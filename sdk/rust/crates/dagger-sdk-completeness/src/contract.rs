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

use crate::authority::{
    SourceCoverage, SourceItemCoverage, SourceItemDisposition, ValidatedAuthoritySources,
    validate_authority_registry, validate_authority_sources, validate_source_coverage,
};
use crate::canonical::{DigestDomain, canonical_bytes, canonical_digest, decode_canonical};
use crate::classification::{resolve_classifications, validate_status_entries};
use crate::command::CommandPolicy;
use crate::compatibility::validate_compatibility_claim;
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
use crate::harness::{
    HarnessMappingContext, build_harness_check_inventory, validate_harness_mappings,
};
use crate::inventory::{
    CapabilityCandidate, CapabilityOrigin, build_inventory, derive_schema_candidates,
};
use crate::io::{RepositoryRoots, SourceLoadError, load_source_bundles};
use crate::model::*;
use crate::report::{build_report, render_human_report};
use crate::target::{TargetObservation, validate_target};

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
    let mappings: HarnessMappings =
        read_canonical(&contract.join("harness-mappings.json"), "harness mappings")?;
    let compatibility: CompatibilityClaim =
        read_canonical(&contract.join("compatibility.json"), "compatibility claim")?;

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
    )?;
    let candidates = capability_candidates(&definitions, &authorities)?;
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
    attach_schema_anchors(&mut inventory, &source_items, &authorities)?;

    let ledger = validated(
        resolve_classifications(&inventory, &source_items, &classifications),
        "resolved ledger",
    )?;
    validated(
        validate_status_entries(&ledger, &evidence),
        "status entries",
    )?;

    let target_digest = TargetDigest::new(
        canonical_digest(DigestDomain::Target, &target).map_err(|_| ToolError::Decode {
            artifact: "target digest",
        })?,
    );
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
            ],
        ),
        "Rust policy extraction",
    )?);
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

fn attach_schema_anchors(
    inventory: &mut CanonicalInventory,
    source_items: &SourceItemInventory,
    authorities: &AuthorityRegistry,
) -> Result<(), ToolError> {
    for definition in inventory.capabilities.values_mut() {
        if !definition.source_anchors.is_empty() {
            continue;
        }
        let source_item = definition
            .source_item_ids
            .first()
            .and_then(|source_item_id| source_items.items.get(source_item_id))
            .ok_or(ToolError::Decode {
                artifact: "schema source anchor",
            })?;
        let authority = authorities
            .authorities
            .get(&definition.authority_id)
            .ok_or(ToolError::Decode {
                artifact: "schema authority",
            })?;
        definition.source_anchors = CanonicalSet::new([EvidenceReference {
            evidence_id: EvidenceId::new(format!("authority/{}", definition.capability_id))
                .map_err(|_| ToolError::Decode {
                    artifact: "schema authority evidence ID",
                })?,
            evidence_kind: EvidenceKind::Authority,
            repository: authority.repository.clone(),
            revision: authority.revision.clone(),
            path: source_item_path(source_item)?,
            locator: source_item.locator.clone(),
            claim: NonEmptyText::new("Pinned schema source defines this atomic capability")
                .expect("static evidence claim is valid"),
            command: None,
            expected_outcome: None,
            execution_target: None,
            platform_scope: CanonicalSet::default(),
            proved_capability_ids: CanonicalSet::new([definition.capability_id.clone()]),
        }]);
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
