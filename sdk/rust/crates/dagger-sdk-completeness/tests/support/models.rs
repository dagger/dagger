//! Heterogeneous wrapper used to exercise every durable model through one property.
//!
//! Property 1 is quantified over the durable surface, not merely one convenient fixture shape.
//! Each variant therefore wraps a real contract type and participates in typed canonical
//! encode/decode round trips.

use dagger_sdk_completeness::*;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum DurableModel {
    TargetDescriptor(TargetDescriptor),
    PathSourceSelector(PathSourceSelector),
    SymbolSourceSelector(SymbolSourceSelector),
    SourceSelector(SourceSelector),
    SourceExclusion(SourceExclusion),
    ExtractorIdentity(ExtractorIdentity),
    AuthoritySource(AuthoritySource),
    AuthorityRegistry(AuthorityRegistry),
    SourceItem(SourceItem),
    SourceItemInventory(SourceItemInventory),
    Platform(Platform),
    CommandSpec(CommandSpec),
    ExpectedOutcome(ExpectedOutcome),
    EvidenceReference(EvidenceReference),
    CapabilityDefinition(CapabilityDefinition),
    CapabilityDefinitions(CapabilityDefinitions),
    CanonicalInventory(CanonicalInventory),
    ClassificationValues(ClassificationValues),
    ClassificationSelector(ClassificationSelector),
    ExpectedSet(ExpectedSet),
    ClassificationRule(ClassificationRule),
    ClassificationInput(ClassificationInput),
    CapabilityRecord(CapabilityRecord),
    ResolvedLedger(ResolvedLedger),
    EvidenceRegistry(EvidenceRegistry),
    HarnessCheckMapping(HarnessCheckMapping),
    HarnessMappings(HarnessMappings),
    HarnessCheckResult(HarnessCheckResult),
    ConformanceScenario(ConformanceScenario),
    HistoricalCapabilityRecord(HistoricalCapabilityRecord),
    CapabilityChange(CapabilityChange),
    AuthorityChange(AuthorityChange),
    HarnessCheckChange(HarnessCheckChange),
    SpecReference(SpecReference),
    OwnedSpecReference(OwnedSpecReference),
    RustApiTransitionReview(RustApiTransitionReview),
    TargetTransition(TargetTransition),
    InclusiveTargetRange(InclusiveTargetRange),
    SupportedTargets(SupportedTargets),
    CompatibilityClaim(CompatibilityClaim),
    ReleaseCompatibilityMetadata(ReleaseCompatibilityMetadata),
    CompleteException(CompleteException),
    CompletenessReport(CompletenessReport),
    ContractDiagnostic(ContractDiagnostic),
}

impl DurableModel {
    pub const VARIANT_COUNT: usize = 44;

    pub fn kind(&self) -> &'static str {
        match self {
            Self::TargetDescriptor(_) => "target-descriptor",
            Self::PathSourceSelector(_) => "path-source-selector",
            Self::SymbolSourceSelector(_) => "symbol-source-selector",
            Self::SourceSelector(_) => "source-selector",
            Self::SourceExclusion(_) => "source-exclusion",
            Self::ExtractorIdentity(_) => "extractor-identity",
            Self::AuthoritySource(_) => "authority-source",
            Self::AuthorityRegistry(_) => "authority-registry",
            Self::SourceItem(_) => "source-item",
            Self::SourceItemInventory(_) => "source-item-inventory",
            Self::Platform(_) => "platform",
            Self::CommandSpec(_) => "command-spec",
            Self::ExpectedOutcome(_) => "expected-outcome",
            Self::EvidenceReference(_) => "evidence-reference",
            Self::CapabilityDefinition(_) => "capability-definition",
            Self::CapabilityDefinitions(_) => "capability-definitions",
            Self::CanonicalInventory(_) => "canonical-inventory",
            Self::ClassificationValues(_) => "classification-values",
            Self::ClassificationSelector(_) => "classification-selector",
            Self::ExpectedSet(_) => "expected-set",
            Self::ClassificationRule(_) => "classification-rule",
            Self::ClassificationInput(_) => "classification-input",
            Self::CapabilityRecord(_) => "capability-record",
            Self::ResolvedLedger(_) => "resolved-ledger",
            Self::EvidenceRegistry(_) => "evidence-registry",
            Self::HarnessCheckMapping(_) => "harness-check-mapping",
            Self::HarnessMappings(_) => "harness-mappings",
            Self::HarnessCheckResult(_) => "harness-check-result",
            Self::ConformanceScenario(_) => "conformance-scenario",
            Self::HistoricalCapabilityRecord(_) => "historical-capability-record",
            Self::CapabilityChange(_) => "capability-change",
            Self::AuthorityChange(_) => "authority-change",
            Self::HarnessCheckChange(_) => "harness-check-change",
            Self::SpecReference(_) => "spec-reference",
            Self::OwnedSpecReference(_) => "owned-spec-reference",
            Self::RustApiTransitionReview(_) => "rust-api-transition-review",
            Self::TargetTransition(_) => "target-transition",
            Self::InclusiveTargetRange(_) => "inclusive-target-range",
            Self::SupportedTargets(_) => "supported-targets",
            Self::CompatibilityClaim(_) => "compatibility-claim",
            Self::ReleaseCompatibilityMetadata(_) => "release-compatibility-metadata",
            Self::CompleteException(_) => "complete-exception",
            Self::CompletenessReport(_) => "completeness-report",
            Self::ContractDiagnostic(_) => "contract-diagnostic",
        }
    }
}
