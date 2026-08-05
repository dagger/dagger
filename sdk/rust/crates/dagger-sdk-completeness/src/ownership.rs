//! Deterministic routing of every blocking capability to one delivery feature.
//!
//! Ownership is reviewed data, not a guess from capability names. Callers assign one closed domain
//! to each blocking row; this module owns the sole domain-to-feature table, including the two
//! easily overclaimed boundaries: `initClient`/client generation and platform coverage.

use std::collections::BTreeMap;

use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{CapabilityId, FeatureId, ResolvedLedger, Status};

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
/// Reviewed umbrella domain for an unresolved capability.
pub enum BlockingDomain {
    ClientLifecycle,
    TransportObservabilityProvisioningReliability,
    CoreSchemaGeneration,
    EngineSdkResolutionRuntimeGeneratorIntegration,
    ModuleAuthoringTypeDiscoveryDispatch,
    StandaloneClientGeneration,
    DependencyClientGeneration,
    InitClient,
    Conformance,
    Platform,
    Security,
    PackagingReleaseCompatibilityDocumentation,
}

impl BlockingDomain {
    /// Returns the only feature allowed to own this umbrella domain.
    pub const fn feature(&self) -> FeatureId {
        match self {
            Self::ClientLifecycle => FeatureId::Feature2,
            Self::TransportObservabilityProvisioningReliability => FeatureId::Feature3,
            Self::CoreSchemaGeneration => FeatureId::Feature4,
            Self::EngineSdkResolutionRuntimeGeneratorIntegration => FeatureId::Feature5,
            Self::ModuleAuthoringTypeDiscoveryDispatch => FeatureId::Feature6,
            Self::StandaloneClientGeneration
            | Self::DependencyClientGeneration
            | Self::InitClient => FeatureId::Feature7,
            Self::Conformance | Self::Platform | Self::Security => FeatureId::Feature8,
            Self::PackagingReleaseCompatibilityDocumentation => FeatureId::Feature9,
        }
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Exact reviewed domain assignment for each blocking Capability_ID.
pub struct OwnershipAssignments {
    pub assignments: BTreeMap<CapabilityId, BlockingDomain>,
}

/// Requires an exhaustive, no-extra assignment set whose features match every blocking row.
pub fn validate_blocking_ownership(
    ledger: &ResolvedLedger,
    assignments: &OwnershipAssignments,
) -> Validation<()> {
    let mut diagnostics = DiagnosticCollector::default();
    for row in ledger.capabilities.values() {
        let blocking = matches!(row.status, Status::Missing | Status::Partial);
        match (blocking, assignments.assignments.get(&row.capability_id)) {
            (true, None) => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityOwnerMissing,
                row.capability_id.to_string(),
                None,
                "blocking capability lacks a reviewed umbrella-domain assignment",
            )),
            (true, Some(domain)) if row.owner_feature.as_ref() != Some(&domain.feature()) => {
                diagnostics.push(ContractDiagnostic::new(
                    DiagnosticCode::CapabilityOwnerMissing,
                    row.capability_id.to_string(),
                    None,
                    format!("blocking domain must be owned by {:?}", domain.feature()),
                ));
            }
            (false, Some(_)) => diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityStatusInvalid,
                row.capability_id.to_string(),
                None,
                "complete capability cannot retain a blocking-work domain assignment",
            )),
            _ => {}
        }
    }
    for capability_id in assignments.assignments.keys() {
        if !ledger.capabilities.contains_key(capability_id) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::CapabilityOwnerMissing,
                capability_id.to_string(),
                None,
                "ownership assignment names no current ledger capability",
            ));
        }
    }
    diagnostics.finish(())
}
