//! Referentially coherent durable contract graphs for property tests.
//!
//! The generator starts from a compact seed and derives every identifier, digest, map key, evidence
//! link, and target binding from it. This makes validity the default: later properties can inject
//! one named defect without unrelated dangling references obscuring the result.

use std::collections::BTreeMap;

use dagger_sdk_completeness::*;
use proptest::prelude::*;
use serde_json::json;

use super::models::DurableModel;
use super::scalars::{commit_strategy, semver_strategy};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EquivalentContractCases {
    pub forward: ContractCase,
    pub reverse: ContractCase,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ContractCase {
    pub target: TargetDescriptor,
    pub path_selector: PathSourceSelector,
    pub symbol_selector: SymbolSourceSelector,
    pub source_selector: SourceSelector,
    pub exclusion: SourceExclusion,
    pub extractor: ExtractorIdentity,
    pub authority: AuthoritySource,
    pub authority_registry: AuthorityRegistry,
    pub source_item: SourceItem,
    pub source_inventory: SourceItemInventory,
    pub platform: Platform,
    pub command: CommandSpec,
    pub expected_outcome: ExpectedOutcome,
    pub authority_evidence: EvidenceReference,
    pub capability_definition: CapabilityDefinition,
    pub capability_definitions: CapabilityDefinitions,
    pub inventory: CanonicalInventory,
    pub classification: ClassificationValues,
    pub classification_selector: ClassificationSelector,
    pub expected_set: ExpectedSet,
    pub classification_rule: ClassificationRule,
    pub classification_input: ClassificationInput,
    pub capability_record: CapabilityRecord,
    pub ledger: ResolvedLedger,
    pub evidence_registry: EvidenceRegistry,
    pub harness_mapping: HarnessCheckMapping,
    pub harness_mappings: HarnessMappings,
    pub harness_result: HarnessCheckResult,
    pub scenario: ConformanceScenario,
    pub historical_record: HistoricalCapabilityRecord,
    pub capability_change: CapabilityChange,
    pub authority_change: AuthorityChange,
    pub harness_change: HarnessCheckChange,
    pub spec_reference: SpecReference,
    pub owned_spec_reference: OwnedSpecReference,
    pub rust_api_transition_review: RustApiTransitionReview,
    pub transition: TargetTransition,
    pub inclusive_range: InclusiveTargetRange,
    pub supported_targets: SupportedTargets,
    pub compatibility: CompatibilityClaim,
    pub release_compatibility_metadata: ReleaseCompatibilityMetadata,
    pub complete_exception: CompleteException,
    pub report: CompletenessReport,
    pub diagnostic: ContractDiagnostic,
}

impl ContractCase {
    pub fn durable_models(&self) -> Vec<DurableModel> {
        vec![
            DurableModel::TargetDescriptor(self.target.clone()),
            DurableModel::PathSourceSelector(self.path_selector.clone()),
            DurableModel::SymbolSourceSelector(self.symbol_selector.clone()),
            DurableModel::SourceSelector(self.source_selector.clone()),
            DurableModel::SourceExclusion(self.exclusion.clone()),
            DurableModel::ExtractorIdentity(self.extractor.clone()),
            DurableModel::AuthoritySource(self.authority.clone()),
            DurableModel::AuthorityRegistry(self.authority_registry.clone()),
            DurableModel::SourceItem(self.source_item.clone()),
            DurableModel::SourceItemInventory(self.source_inventory.clone()),
            DurableModel::Platform(self.platform.clone()),
            DurableModel::CommandSpec(self.command.clone()),
            DurableModel::ExpectedOutcome(self.expected_outcome.clone()),
            DurableModel::EvidenceReference(self.authority_evidence.clone()),
            DurableModel::CapabilityDefinition(self.capability_definition.clone()),
            DurableModel::CapabilityDefinitions(self.capability_definitions.clone()),
            DurableModel::CanonicalInventory(self.inventory.clone()),
            DurableModel::ClassificationValues(self.classification.clone()),
            DurableModel::ClassificationSelector(self.classification_selector.clone()),
            DurableModel::ExpectedSet(self.expected_set.clone()),
            DurableModel::ClassificationRule(self.classification_rule.clone()),
            DurableModel::ClassificationInput(self.classification_input.clone()),
            DurableModel::CapabilityRecord(self.capability_record.clone()),
            DurableModel::ResolvedLedger(self.ledger.clone()),
            DurableModel::EvidenceRegistry(self.evidence_registry.clone()),
            DurableModel::HarnessCheckMapping(self.harness_mapping.clone()),
            DurableModel::HarnessMappings(self.harness_mappings.clone()),
            DurableModel::HarnessCheckResult(self.harness_result.clone()),
            DurableModel::ConformanceScenario(self.scenario.clone()),
            DurableModel::HistoricalCapabilityRecord(self.historical_record.clone()),
            DurableModel::CapabilityChange(self.capability_change.clone()),
            DurableModel::AuthorityChange(self.authority_change.clone()),
            DurableModel::HarnessCheckChange(self.harness_change.clone()),
            DurableModel::SpecReference(self.spec_reference.clone()),
            DurableModel::OwnedSpecReference(self.owned_spec_reference.clone()),
            DurableModel::RustApiTransitionReview(self.rust_api_transition_review.clone()),
            DurableModel::TargetTransition(self.transition.clone()),
            DurableModel::InclusiveTargetRange(self.inclusive_range.clone()),
            DurableModel::SupportedTargets(self.supported_targets.clone()),
            DurableModel::CompatibilityClaim(self.compatibility.clone()),
            DurableModel::ReleaseCompatibilityMetadata(self.release_compatibility_metadata.clone()),
            DurableModel::CompleteException(self.complete_exception.clone()),
            DurableModel::CompletenessReport(self.report.clone()),
            DurableModel::ContractDiagnostic(self.diagnostic.clone()),
        ]
    }

    pub fn reference_errors(&self) -> Vec<&'static str> {
        let mut errors = Vec::new();
        let capability_id = &self.capability_definition.capability_id;
        let authority_id = &self.authority.authority_id;
        let target_digest =
            TargetDigest::new(canonical_digest(DigestDomain::Target, &self.target).unwrap());

        if self.authority_registry.authorities.get(authority_id) != Some(&self.authority) {
            errors.push("authority registry key does not resolve to the authority");
        }
        if self
            .source_inventory
            .items
            .get(&self.source_item.source_item_id)
            != Some(&self.source_item)
            || &self.source_item.authority_id != authority_id
        {
            errors.push("source item is not keyed by identity and bound to its authority");
        }
        if self
            .capability_definition
            .source_item_ids
            .iter()
            .any(|source_id| !self.source_inventory.items.contains_key(source_id))
            || &self.capability_definition.authority_id != authority_id
        {
            errors.push("capability definition has an unresolved source or authority");
        }
        if self.inventory.capabilities.get(capability_id) != Some(&self.capability_definition)
            || self.capability_definitions.capabilities != self.inventory.capabilities
        {
            errors.push("capability inventories disagree with the generated definition");
        }

        let expected_capabilities = CanonicalSet::new([capability_id.clone()]);
        let expansion_matches = match &self.expected_set {
            ExpectedSet::CapabilityIds(capabilities) => capabilities == &expected_capabilities,
            ExpectedSet::Digest(digest) => {
                canonical_digest(DigestDomain::RuleExpansion, &expected_capabilities)
                    .is_ok_and(|actual| &actual == digest)
            }
        };
        if !expansion_matches
            || self
                .classification_input
                .rules
                .get(&self.classification_rule.rule_id)
                != Some(&self.classification_rule)
        {
            errors.push("classification rule expansion or key is inconsistent");
        }
        if self.ledger.capabilities.get(capability_id) != Some(&self.capability_record) {
            errors.push("resolved ledger is not keyed by the capability identity");
        }
        for evidence_id in self
            .capability_record
            .implementation_evidence
            .iter()
            .chain(self.capability_record.verification_evidence.iter())
            .chain(self.capability_record.decision_evidence.iter())
        {
            if !self.evidence_registry.evidence.contains_key(evidence_id) {
                errors.push("ledger classification references missing evidence");
            }
        }

        if self
            .harness_mappings
            .checks
            .get(&self.harness_mapping.check_id)
            != Some(&self.harness_mapping)
            || self
                .harness_mapping
                .capability_ids
                .iter()
                .any(|id| !self.inventory.capabilities.contains_key(id))
        {
            errors.push("harness mapping has an unresolved check or capability");
        }
        if self.harness_result.check_id != self.harness_mapping.check_id
            || self.harness_result.check_kind != self.harness_mapping.check_kind
            || self.harness_result.harness_revision != self.harness_mapping.harness_revision
            || self.harness_result.target != self.harness_mapping.execution_target
            || self.harness_result.cli_artifact_digest != self.harness_mapping.cli_artifact_digest
            || self.harness_result.verified_artifact_digest
                != self.harness_mapping.verified_artifact_digest
            || !self
                .harness_mapping
                .platform_scope
                .contains(&self.harness_result.platform)
            || self.harness_result.assertion != self.harness_mapping.expected_outcome.assertion
            || self.harness_result.capability_ids != self.harness_mapping.capability_ids
            || self.harness_mapping.execution_target != target_digest
        {
            errors.push("harness result is not contained by its mapping and target");
        }
        if self
            .scenario
            .capability_ids
            .iter()
            .any(|id| !self.inventory.capabilities.contains_key(id))
        {
            errors.push("conformance scenario references an unknown capability");
        }

        if self.target.previous_target.as_ref() != Some(&self.transition.from_target)
            || self.transition.to_target != self.target
            || self
                .transition
                .added_capabilities
                .iter()
                .any(|id| !self.inventory.capabilities.contains_key(id))
            || self.transition.removed_capabilities.iter().any(|removed| {
                self.inventory
                    .capabilities
                    .contains_key(&removed.capability.capability_id)
            })
            || (self.transition.semver_effect == SemverEffect::Breaking
                && self.transition.migration_requirements.is_empty())
        {
            errors.push("target transition is not coherent with its base and candidate");
        }

        let expected_boundaries = match &self.compatibility.supported_targets {
            SupportedTargets::Exact(targets) => targets.clone(),
            SupportedTargets::InclusiveRange(range) => {
                CanonicalSet::new([range.lower.clone(), range.upper.clone()])
            }
        };
        let expected_claim_digest = canonical_digest(
            DigestDomain::Compatibility,
            &(
                self.compatibility.rust_sdk_version.clone(),
                self.compatibility.supported_targets.clone(),
                expected_boundaries.clone(),
                self.compatibility.conformance_evidence.clone(),
                self.compatibility.outside_range_capability.clone(),
            ),
        )
        .unwrap();
        if self.compatibility.range_boundaries != expected_boundaries
            || self.compatibility.claim_digest != expected_claim_digest
            || self
                .compatibility
                .conformance_evidence
                .iter()
                .any(|id| !self.evidence_registry.evidence.contains_key(id))
        {
            errors.push("compatibility claim has unresolved evidence, boundaries, or digest");
        }

        let blocking = matches!(
            self.capability_record.status,
            Status::Missing | Status::Partial
        );
        if self.report.target_descriptor != self.target
            || self.report.inventory_digest
                != canonical_digest(DigestDomain::Artifact, &self.inventory).unwrap()
            || self.report.ledger_digest
                != canonical_digest(DigestDomain::Artifact, &self.ledger).unwrap()
            || self.report.completeness_verdict != (self.report.integrity_verdict && !blocking)
            || self.report.blocking_capabilities.is_empty() != !blocking
        {
            errors.push("report target, digest, or verdict projection is inconsistent");
        }

        errors
    }
}

#[derive(Clone, Debug)]
struct ContractSeed {
    dagger_revision: CommitSha,
    go_revision: CommitSha,
    harness_revision: CommitSha,
    contract_format_version: SemverVersion,
    engine_version: SemverVersion,
    rust_sdk_version: SemverVersion,
    rust_version: SemverVersion,
    value: u64,
    status: Status,
    stability: Stability,
    feature: FeatureId,
    operating_system: OperatingSystem,
    architecture: Architecture,
    authority_class: AuthorityClass,
    source_state: SourceItemState,
    harness_adapter: HarnessAdapter,
    use_expected_digest: bool,
    use_compatibility_range: bool,
    label_present: bool,
}

prop_compose! {
    pub fn equivalent_contract_cases_strategy()
        (
            dagger_revision in commit_strategy(),
            go_revision in commit_strategy(),
            harness_revision in commit_strategy(),
            contract_format_version in semver_strategy(),
            engine_version in semver_strategy(),
            rust_sdk_version in semver_strategy(),
            rust_version in semver_strategy(),
            value in any::<u64>(),
            status_index in 0_usize..5,
            stability_index in 0_usize..4,
            feature_index in 0_usize..8,
            os_index in 0_usize..3,
            architecture_index in 0_usize..2,
            authority_index in 0_usize..7,
            source_state_index in 0_usize..2,
            harness_adapter_index in 0_usize..2,
            use_expected_digest in any::<bool>(),
            use_compatibility_range in any::<bool>(),
            label_present in any::<bool>(),
        ) -> EquivalentContractCases
    {
        let seed = ContractSeed {
            dagger_revision,
            go_revision,
            harness_revision,
            contract_format_version,
            engine_version,
            rust_sdk_version,
            rust_version,
            value,
            status: [
                Status::Missing,
                Status::Partial,
                Status::Implemented,
                Status::IdiomaticEquivalent,
                Status::Inapplicable,
            ][status_index].clone(),
            stability: [
                Stability::Stable,
                Stability::Experimental,
                Stability::Internal,
                Stability::NotApplicable,
            ][stability_index].clone(),
            feature: [
                FeatureId::Feature2,
                FeatureId::Feature3,
                FeatureId::Feature4,
                FeatureId::Feature5,
                FeatureId::Feature6,
                FeatureId::Feature7,
                FeatureId::Feature8,
                FeatureId::Feature9,
            ][feature_index].clone(),
            operating_system: [
                OperatingSystem::Linux,
                OperatingSystem::Macos,
                OperatingSystem::Windows,
            ][os_index].clone(),
            architecture: [Architecture::Amd64, Architecture::Arm64][architecture_index].clone(),
            authority_class: [
                AuthorityClass::EngineSchema,
                AuthorityClass::GoClient,
                AuthorityClass::GoEngineSdk,
                AuthorityClass::GoCodegen,
                AuthorityClass::GoIntegrationTests,
                AuthorityClass::SdkContractHarness,
                AuthorityClass::RustPolicy,
            ][authority_index].clone(),
            source_state: [SourceItemState::Active, SourceItemState::Deprecated]
                [source_state_index]
                .clone(),
            harness_adapter: [HarnessAdapter::SdkTarget, HarnessAdapter::ModTest]
                [harness_adapter_index]
                .clone(),
            use_expected_digest,
            use_compatibility_range,
            label_present,
        };

        EquivalentContractCases {
            forward: build_contract_case(&seed, false),
            reverse: build_contract_case(&seed, true),
        }
    }
}

pub fn durable_model_strategy() -> BoxedStrategy<DurableModel> {
    equivalent_contract_cases_strategy()
        .prop_flat_map(|cases| proptest::sample::select(cases.forward.durable_models()))
        .boxed()
}

fn build_contract_case(seed: &ContractSeed, reverse: bool) -> ContractCase {
    let suffix = format!("{:016x}", seed.value);
    let authority_id = AuthorityId::new(format!("authority/{suffix}")).unwrap();
    let source_item_id = SourceItemId::new(format!("source/{suffix}")).unwrap();
    let capability_id = CapabilityId::new(format!("capability/{suffix}")).unwrap();
    let historical_capability_id =
        CapabilityId::new(format!("capability/historical-{suffix}")).unwrap();
    let capability_kind = CapabilityKind::new("behavior/client").unwrap();
    let source_item_kind = SourceItemKind::new("go/declaration").unwrap();
    let rule_id = RuleId::new(format!("rule/{suffix}")).unwrap();
    let check_id = CheckId::new(format!("check/{suffix}")).unwrap();
    let scenario_id = ScenarioId::new(format!("scenario/{suffix}")).unwrap();
    let authority_evidence_id = EvidenceId::new(format!("evidence/authority-{suffix}")).unwrap();
    let implementation_evidence_id =
        EvidenceId::new(format!("evidence/implementation-{suffix}")).unwrap();
    let verification_evidence_id =
        EvidenceId::new(format!("evidence/verification-{suffix}")).unwrap();
    let decision_evidence_id = EvidenceId::new(format!("evidence/decision-{suffix}")).unwrap();

    let previous_target = TargetDigest::new(Digest::sha256(format!("previous-{suffix}")));
    let engine_version = DaggerVersion::new(seed.engine_version.to_string()).unwrap();
    let target = TargetDescriptor {
        contract_format_version: seed.contract_format_version.clone(),
        dagger_repository: repository("github.com/dagger/dagger"),
        dagger_revision: seed.dagger_revision.clone(),
        engine_version: engine_version.clone(),
        schema_version: text(format!("schema-{suffix}")),
        schema_digest: Digest::sha256(format!("schema-{suffix}")),
        go_sdk_repository: repository("github.com/dagger/dagger-go-sdk"),
        go_sdk_revision: seed.go_revision.clone(),
        go_sdk_version_label: seed.label_present.then(|| text("v0.21.7")),
        sdk_contract_repository: repository("github.com/dagger/sdk-sdk"),
        sdk_contract_revision: seed.harness_revision.clone(),
        sdk_contract_cli_version: engine_version,
        rust_sdk_version: seed.rust_sdk_version.clone(),
        rust_edition: RustEdition::Edition2024,
        rust_version: seed.rust_version.clone(),
        previous_target: Some(previous_target.clone()),
    };
    let target_digest = TargetDigest::new(canonical_digest(DigestDomain::Target, &target).unwrap());

    let source_path = path(format!("sdk/go/source-{suffix}.go"));
    let path_selector = PathSourceSelector {
        path: source_path.clone(),
    };
    let symbol_selector = SymbolSourceSelector {
        path: source_path.clone(),
        locator: locator(format!("Client.Connect:{suffix}")),
    };
    let source_selector = SourceSelector::Symbol(symbol_selector.clone());
    let exclusion = SourceExclusion {
        selector: source_selector.clone(),
        rationale: text("represented-by-engine-schema"),
    };
    let extractor = ExtractorIdentity {
        extractor_id: ExtractorId::new("go-ast/v1").unwrap(),
        version: SemverVersion::new("1.0.0").unwrap(),
    };
    let include = canonical_set(
        vec![
            SourceSelector::Path(path_selector.clone()),
            source_selector.clone(),
        ],
        reverse,
    );
    let exclude = canonical_set(vec![exclusion.clone()], reverse);
    let source_digest = canonical_digest(
        DigestDomain::Source,
        &(include.clone(), exclude.clone(), extractor.clone()),
    )
    .unwrap();
    let authority = AuthoritySource {
        authority_id: authority_id.clone(),
        authority_class: seed.authority_class.clone(),
        repository: repository("github.com/dagger/dagger"),
        revision: seed.dagger_revision.clone(),
        include,
        exclude,
        extractor: extractor.clone(),
        source_digest: source_digest.clone(),
    };
    let authority_registry = AuthorityRegistry {
        authorities: ordered_map(vec![(authority_id.clone(), authority.clone())], reverse),
    };

    let semantic_signature = json!({
        "async": true,
        "name": format!("connect-{suffix}"),
        "parameters": ["config", "context"],
    });
    let capability_fingerprint =
        canonical_digest(DigestDomain::Capability, &semantic_signature).unwrap();
    let source_item = SourceItem {
        source_item_id: source_item_id.clone(),
        authority_id: authority_id.clone(),
        item_kind: source_item_kind.clone(),
        locator: locator(format!("Client.Connect:{suffix}")),
        semantic_signature: semantic_signature.clone(),
        fingerprint: capability_fingerprint.clone(),
        state: seed.source_state.clone(),
    };
    let source_inventory = SourceItemInventory {
        items: ordered_map(vec![(source_item_id.clone(), source_item.clone())], reverse),
    };

    let platform = Platform {
        operating_system: seed.operating_system.clone(),
        architecture: seed.architecture.clone(),
    };
    let alternate_platform = Platform {
        operating_system: OperatingSystem::Linux,
        architecture: Architecture::Amd64,
    };
    let environment = ordered_map(
        vec![
            ("DAGGER_LOG_FORMAT".to_owned(), "plain".to_owned()),
            ("RUST_BACKTRACE".to_owned(), "1".to_owned()),
        ],
        reverse,
    );
    let command = CommandSpec {
        program: ExecutableId::new("cargo").unwrap(),
        args: vec!["test".to_owned(), "--locked".to_owned()],
        working_directory: path("sdk/rust"),
        environment,
    };
    let expected_outcome = ExpectedOutcome {
        outcome: CheckOutcome::Passed,
        assertion: text("client-connects-to-selected-engine"),
    };

    let authority_evidence = EvidenceReference {
        evidence_id: authority_evidence_id.clone(),
        evidence_kind: EvidenceKind::Authority,
        repository: repository("github.com/dagger/dagger"),
        revision: seed.dagger_revision.clone(),
        path: source_path.clone(),
        locator: locator(format!("Client.Connect:{suffix}")),
        claim: text("defines-client-connection-behaviour"),
        command: None,
        expected_outcome: None,
        execution_target: None,
        platform_scope: CanonicalSet::default(),
        proved_capability_ids: canonical_set(vec![capability_id.clone()], reverse),
    };
    let implementation_evidence = EvidenceReference {
        evidence_id: implementation_evidence_id.clone(),
        evidence_kind: EvidenceKind::Implementation,
        repository: repository("github.com/dagger/dagger"),
        revision: seed.dagger_revision.clone(),
        path: path("sdk/rust/crates/dagger-sdk/src/core/client.rs"),
        locator: locator("connect"),
        claim: text("implements-client-connection-behaviour"),
        command: None,
        expected_outcome: None,
        execution_target: Some(target_digest.clone()),
        platform_scope: CanonicalSet::default(),
        proved_capability_ids: canonical_set(vec![capability_id.clone()], reverse),
    };
    let verification_evidence = EvidenceReference {
        evidence_id: verification_evidence_id.clone(),
        evidence_kind: EvidenceKind::Verification,
        repository: repository("github.com/dagger/dagger"),
        revision: seed.dagger_revision.clone(),
        path: path("sdk/rust/crates/dagger-sdk/tests/mod.rs"),
        locator: locator("test_connect"),
        claim: text("verifies-client-connection-behaviour"),
        command: Some(command.clone()),
        expected_outcome: Some(expected_outcome.clone()),
        execution_target: Some(target_digest.clone()),
        platform_scope: canonical_set(vec![platform.clone(), alternate_platform.clone()], reverse),
        proved_capability_ids: canonical_set(vec![capability_id.clone()], reverse),
    };
    let decision_evidence = EvidenceReference {
        evidence_id: decision_evidence_id.clone(),
        evidence_kind: EvidenceKind::Decision,
        repository: repository("github.com/dagger/dagger"),
        revision: seed.dagger_revision.clone(),
        path: path(".kiro/specs/rust-sdk-completeness-contract/design.md"),
        locator: locator("capability-and-classification-models"),
        claim: text("records-reviewed-classification"),
        command: None,
        expected_outcome: None,
        execution_target: Some(target_digest.clone()),
        platform_scope: CanonicalSet::default(),
        proved_capability_ids: canonical_set(vec![capability_id.clone()], reverse),
    };

    let capability_definition = CapabilityDefinition {
        capability_id: capability_id.clone(),
        authority_id: authority_id.clone(),
        capability_kind: capability_kind.clone(),
        source_item_ids: canonical_set(vec![source_item_id.clone()], reverse),
        source_anchors: canonical_set(vec![authority_evidence.clone()], reverse),
        summary: text("connect-client-to-engine"),
        semantic_signature: semantic_signature.clone(),
        capability_fingerprint: capability_fingerprint.clone(),
        stability: seed.stability.clone(),
    };
    let capabilities = ordered_map(
        vec![(capability_id.clone(), capability_definition.clone())],
        reverse,
    );
    let capability_definitions = CapabilityDefinitions {
        capabilities: capabilities.clone(),
    };
    let inventory = CanonicalInventory { capabilities };

    let classification = classification_values(
        &seed.status,
        &seed.feature,
        &implementation_evidence_id,
        &verification_evidence_id,
        &decision_evidence_id,
        reverse,
    );
    let classification_selector = ClassificationSelector {
        authority_id: Some(authority_id.clone()),
        capability_kind: Some(capability_kind.clone()),
        stability: Some(seed.stability.clone()),
        source_item_kind: Some(source_item_kind),
        source_path: None,
        capability_id_prefix: Some(capability_id.clone()),
    };
    let expected_capability_ids = canonical_set(vec![capability_id.clone()], reverse);
    let expected_set = if seed.use_expected_digest {
        ExpectedSet::Digest(
            canonical_digest(DigestDomain::RuleExpansion, &expected_capability_ids).unwrap(),
        )
    } else {
        ExpectedSet::CapabilityIds(expected_capability_ids)
    };
    let classification_rule = ClassificationRule {
        rule_id: rule_id.clone(),
        authority_id: authority_id.clone(),
        selector: classification_selector.clone(),
        expected_capability_ids: expected_set.clone(),
        classification: classification.clone(),
        overrides: BTreeMap::new(),
    };
    let classification_input = ClassificationInput {
        exact: BTreeMap::new(),
        rules: ordered_map(vec![(rule_id, classification_rule.clone())], reverse),
    };
    let capability_record = CapabilityRecord {
        capability_id: capability_id.clone(),
        authority_id: authority_id.clone(),
        capability_kind: capability_kind.clone(),
        source_item_ids: capability_definition.source_item_ids.clone(),
        source_anchors: capability_definition.source_anchors.clone(),
        summary: capability_definition.summary.clone(),
        semantic_signature,
        capability_fingerprint: capability_fingerprint.clone(),
        status: seed.status.clone(),
        stability: seed.stability.clone(),
        gap: classification.gap.clone(),
        owner_feature: classification.owner_feature.clone(),
        implementation_evidence: classification.implementation_evidence.clone(),
        verification_evidence: classification.verification_evidence.clone(),
        decision_evidence: classification.decision_evidence.clone(),
    };
    let ledger = ResolvedLedger {
        capabilities: ordered_map(
            vec![(capability_id.clone(), capability_record.clone())],
            reverse,
        ),
    };
    let evidence_registry = EvidenceRegistry {
        evidence: ordered_map(
            vec![
                (authority_evidence_id, authority_evidence.clone()),
                (implementation_evidence_id, implementation_evidence),
                (verification_evidence_id.clone(), verification_evidence),
                (decision_evidence_id.clone(), decision_evidence),
            ],
            reverse,
        ),
    };

    let harness_mapping = HarnessCheckMapping {
        check_id: check_id.clone(),
        check_kind: HarnessCheckKind::SubjectConformance,
        harness_revision: seed.harness_revision.clone(),
        source_locator: locator(format!("check_client_connect:{suffix}")),
        source_fingerprint: Digest::sha256(format!("check-source-{suffix}")),
        capability_ids: canonical_set(vec![capability_id.clone()], reverse),
        execution_target: target_digest.clone(),
        cli_artifact_digest: Digest::sha256(format!("cli-{suffix}")),
        verified_artifact_digest: Digest::sha256(format!("rust-workspace-{suffix}")),
        platform_scope: canonical_set(vec![platform.clone(), alternate_platform], reverse),
        invocation: command.clone(),
        expected_outcome: expected_outcome.clone(),
        verification_evidence: Some(verification_evidence_id.clone()),
        limitations: canonical_set(vec![text("selected-platforms-only")], reverse),
    };
    let harness_mappings = HarnessMappings {
        checks: ordered_map(vec![(check_id.clone(), harness_mapping.clone())], reverse),
    };
    let harness_result = HarnessCheckResult {
        check_id: check_id.clone(),
        check_kind: HarnessCheckKind::SubjectConformance,
        harness_revision: seed.harness_revision.clone(),
        target: target_digest.clone(),
        cli_artifact_digest: harness_mapping.cli_artifact_digest.clone(),
        verified_artifact_digest: harness_mapping.verified_artifact_digest.clone(),
        platform: platform.clone(),
        outcome: CheckOutcome::Passed,
        assertion: expected_outcome.assertion.clone(),
        capability_ids: harness_mapping.capability_ids.clone(),
        stdout_digest: Digest::sha256(format!("stdout-{suffix}")),
        stderr_digest: Digest::sha256(format!("stderr-{suffix}")),
    };
    let scenario = ConformanceScenario {
        scenario_id,
        source_anchors: canonical_set(vec![authority_evidence.clone()], reverse),
        observable_behavior: json!({"connects": true, "target": suffix}),
        capability_ids: canonical_set(vec![capability_id.clone()], reverse),
        harness_adapter: seed.harness_adapter.clone(),
        invocation: command.clone(),
        expected_outcome: expected_outcome.clone(),
    };

    let historical_fingerprint = Digest::sha256(format!("historical-capability-{suffix}"));
    let mut historical_capability = capability_record.clone();
    historical_capability.capability_id = historical_capability_id;
    historical_capability.capability_fingerprint = historical_fingerprint.clone();
    let historical_record = HistoricalCapabilityRecord {
        target: previous_target.clone(),
        capability: historical_capability,
    };
    let capability_change = CapabilityChange {
        capability_id: capability_id.clone(),
        from_fingerprint: historical_fingerprint,
        to_fingerprint: capability_fingerprint,
    };
    let authority_change = AuthorityChange {
        authority_id: authority_id.clone(),
        from_source_digest: Digest::sha256(format!("old-source-{suffix}")),
        to_source_digest: source_digest,
    };
    let harness_change = HarnessCheckChange {
        check_id,
        from_fingerprint: Some(Digest::sha256(format!("old-check-{suffix}"))),
        to_fingerprint: Some(Digest::sha256(format!("new-check-{suffix}"))),
    };
    let spec_reference = SpecReference {
        path: path(".kiro/specs/rust-sdk-completeness-contract/design.md"),
        locator: locator("transition-and-compatibility-engines"),
    };
    let owned_spec_reference = OwnedSpecReference {
        owner_feature: FeatureId::Feature9,
        reference: spec_reference.clone(),
    };
    let rust_api_transition_review = RustApiTransitionReview {
        capability_id: capability_id.clone(),
        change_kind: RustApiChangeKind::Incompatible,
        user_facing: true,
        experimental_condition: (seed.stability == Stability::Experimental)
            .then(|| spec_reference.clone()),
        migration_requirement: Some(owned_spec_reference.clone()),
    };
    let transition = TargetTransition {
        from_target: previous_target.clone(),
        to_target: target.clone(),
        added_capabilities: canonical_set(vec![capability_id.clone()], reverse),
        removed_capabilities: vec![historical_record.clone()],
        changed_capabilities: CanonicalSet::default(),
        authority_changes: canonical_set(vec![authority_change.clone()], reverse),
        harness_changes: canonical_set(vec![harness_change.clone()], reverse),
        semver_effect: SemverEffect::Breaking,
        migration_requirements: canonical_set(vec![spec_reference.clone()], reverse),
    };

    let inclusive_range = InclusiveTargetRange {
        lower: previous_target.clone(),
        upper: target_digest.clone(),
    };
    let supported_targets = if seed.use_compatibility_range {
        SupportedTargets::InclusiveRange(inclusive_range.clone())
    } else {
        SupportedTargets::Exact(canonical_set(vec![target_digest.clone()], reverse))
    };
    let range_boundaries = match &supported_targets {
        SupportedTargets::Exact(targets) => targets.clone(),
        SupportedTargets::InclusiveRange(range) => {
            canonical_set(vec![range.lower.clone(), range.upper.clone()], reverse)
        }
    };
    let conformance_evidence = canonical_set(vec![verification_evidence_id], reverse);
    let outside_range_capability = CapabilityId::new("policy/outside-target-range").unwrap();
    let claim_digest = canonical_digest(
        DigestDomain::Compatibility,
        &(
            seed.rust_sdk_version.clone(),
            supported_targets.clone(),
            range_boundaries.clone(),
            conformance_evidence.clone(),
            outside_range_capability.clone(),
        ),
    )
    .unwrap();
    let compatibility = CompatibilityClaim {
        rust_sdk_version: seed.rust_sdk_version.clone(),
        supported_targets: supported_targets.clone(),
        range_boundaries,
        conformance_evidence,
        outside_range_capability,
        claim_digest,
    };
    let release_compatibility_metadata = ReleaseCompatibilityMetadata {
        rust_sdk_version: compatibility.rust_sdk_version.clone(),
        supported_targets: compatibility.supported_targets.clone(),
        claim_digest: compatibility.claim_digest.clone(),
    };

    let complete_exception = CompleteException {
        capability_id: capability_id.clone(),
        status: Status::Inapplicable,
        decision_evidence: canonical_set(vec![decision_evidence_id], reverse),
    };
    let blocking = matches!(seed.status, Status::Missing | Status::Partial);
    let report = CompletenessReport {
        contract_format_version: seed.contract_format_version.clone(),
        target_descriptor: target.clone(),
        inventory_digest: canonical_digest(DigestDomain::Artifact, &inventory).unwrap(),
        ledger_digest: canonical_digest(DigestDomain::Artifact, &ledger).unwrap(),
        integrity_verdict: true,
        completeness_verdict: !blocking,
        counts_by_authority: ordered_map(vec![(authority_id.clone(), 1)], reverse),
        counts_by_kind: ordered_map(vec![(capability_kind, 1)], reverse),
        counts_by_status: ordered_map(vec![(seed.status.clone(), 1)], reverse),
        counts_by_owner: classification
            .owner_feature
            .clone()
            .map(|owner| ordered_map(vec![(owner, 1)], reverse))
            .unwrap_or_default(),
        integrity_errors: Vec::new(),
        blocking_capabilities: if blocking {
            canonical_set(vec![capability_id.clone()], reverse)
        } else {
            CanonicalSet::default()
        },
        complete_exceptions: if seed.status == Status::Inapplicable {
            vec![complete_exception.clone()]
        } else {
            Vec::new()
        },
    };
    let diagnostic = ContractDiagnostic::new(
        DiagnosticCode::LedgerDrift,
        capability_id.to_string(),
        Some(locator(format!("capability:{suffix}"))),
        "generated reference diagnostic",
    );

    ContractCase {
        target,
        path_selector,
        symbol_selector,
        source_selector,
        exclusion,
        extractor,
        authority,
        authority_registry,
        source_item,
        source_inventory,
        platform,
        command,
        expected_outcome,
        authority_evidence,
        capability_definition,
        capability_definitions,
        inventory,
        classification,
        classification_selector,
        expected_set,
        classification_rule,
        classification_input,
        capability_record,
        ledger,
        evidence_registry,
        harness_mapping,
        harness_mappings,
        harness_result,
        scenario,
        historical_record,
        capability_change,
        authority_change,
        harness_change,
        spec_reference,
        owned_spec_reference,
        rust_api_transition_review,
        transition,
        inclusive_range,
        supported_targets,
        compatibility,
        release_compatibility_metadata,
        complete_exception,
        report,
        diagnostic,
    }
}

fn classification_values(
    status: &Status,
    feature: &FeatureId,
    implementation_evidence: &EvidenceId,
    verification_evidence: &EvidenceId,
    decision_evidence: &EvidenceId,
    reverse: bool,
) -> ClassificationValues {
    match status {
        Status::Missing => ClassificationValues {
            status: status.clone(),
            gap: Some(text("implementation-and-verification-missing")),
            owner_feature: Some(feature.clone()),
            implementation_evidence: CanonicalSet::default(),
            verification_evidence: CanonicalSet::default(),
            decision_evidence: CanonicalSet::default(),
        },
        Status::Partial => ClassificationValues {
            status: status.clone(),
            gap: Some(text("verification-incomplete")),
            owner_feature: Some(feature.clone()),
            implementation_evidence: canonical_set(vec![implementation_evidence.clone()], reverse),
            verification_evidence: CanonicalSet::default(),
            decision_evidence: CanonicalSet::default(),
        },
        Status::Implemented | Status::IdiomaticEquivalent => ClassificationValues {
            status: status.clone(),
            gap: None,
            owner_feature: None,
            implementation_evidence: canonical_set(vec![implementation_evidence.clone()], reverse),
            verification_evidence: canonical_set(vec![verification_evidence.clone()], reverse),
            decision_evidence: if status == &Status::IdiomaticEquivalent {
                canonical_set(vec![decision_evidence.clone()], reverse)
            } else {
                CanonicalSet::default()
            },
        },
        Status::Inapplicable => ClassificationValues {
            status: status.clone(),
            gap: None,
            owner_feature: None,
            implementation_evidence: CanonicalSet::default(),
            verification_evidence: CanonicalSet::default(),
            decision_evidence: canonical_set(vec![decision_evidence.clone()], reverse),
        },
    }
}

fn ordered_map<K: Ord, V>(mut entries: Vec<(K, V)>, reverse: bool) -> BTreeMap<K, V> {
    if reverse {
        entries.reverse();
    }
    entries.into_iter().collect()
}

fn canonical_set<T: Ord>(mut values: Vec<T>, reverse: bool) -> CanonicalSet<T> {
    if reverse {
        values.reverse();
    }
    CanonicalSet::new(values)
}

fn repository(value: &str) -> RepositoryId {
    RepositoryId::new(value).unwrap()
}

fn path(value: impl Into<String>) -> RepositoryRelativePath {
    RepositoryRelativePath::new(value).unwrap()
}

fn locator(value: impl Into<String>) -> SourceLocator {
    SourceLocator::new(value).unwrap()
}

fn text(value: impl Into<String>) -> NonEmptyText {
    NonEmptyText::new(value).unwrap()
}
