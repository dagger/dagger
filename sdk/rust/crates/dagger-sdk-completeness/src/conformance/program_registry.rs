//! Closed production program registry for the fixed exact-engine case families.
//!
//! The registry does not contain command text, paths, or arbitrary selectors. Each catalog
//! program maps to one typed production boundary and one workspace/dependency policy, so the
//! executable facade can dispatch the complete inventory without inventing behaviour at runtime.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::model::Digest;
use crate::{ClientSignoffCase, ModuleSignoffCase};

use super::{
    CaseProgram, ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    DiagnosticCoordinate, DiagnosticPhase, required_fixed_programs,
};

/// Public or production boundary exercised by one fixed case program.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FixedProgramBoundary {
    /// Pinned common SDK harness subject-check entrypoint.
    CommonHarnessSubject,
    /// Stable connector's production distribution and session lifecycle.
    StableConnectorDistribution,
    /// Public generated Rust Core API.
    PublicGeneratedCore,
    /// Existing CLI integration assertions over the shared installed baseline.
    SharedBaselineCli,
    /// Production Rust TypeDef registration and dispatcher runtime.
    ProductionModuleDispatcher,
    /// Public generated standalone-client API and packaged runtime.
    PublicGeneratedClient,
    /// Public idiomatic Rust client API corresponding to a definitive Go observation.
    PublicRustClient,
}

/// Mutable workspace policy required by one fixed program.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FixedProgramWorkspace {
    /// Private branch of the common installed Rust baseline.
    IsolatedBaselineBranch,
    /// Independent Cargo workspace outside the repository checkout.
    ExternalPackagedWorkspace,
}

/// Reviewed authority used to define one program's observable predicate.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FixedProgramAuthority {
    /// Pinned common sdk-sdk subject contract.
    CommonHarness,
    /// Rust SDK production contract and checked engine-integration assertions.
    RustProduction,
    /// Definitive Go SDK source, translated only at the observable-result boundary.
    DefinitiveGoSource,
}

/// SDK dependency source allowed to reach a fixed program.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum FixedProgramSdkSource {
    /// Exact packaged Rust content from the admitted artifact.
    ExactArtifactPackage,
}

/// One closed executable program route with no arbitrary-command escape hatch.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FixedCaseProgramSpec {
    /// Exact catalog program selected by this route.
    pub program: CaseProgram,
    /// Production API or lifecycle boundary the route must call.
    pub boundary: FixedProgramBoundary,
    /// Mutable workspace isolation policy.
    pub workspace: FixedProgramWorkspace,
    /// Reviewed authority for the observable predicate.
    pub authority: FixedProgramAuthority,
    /// Sole permissible SDK dependency source.
    pub sdk_source: FixedProgramSdkSource,
}

/// Complete validated fixed-program registry in canonical program order.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FixedCaseProgramRegistry {
    programs: BTreeMap<CaseProgram, FixedCaseProgramSpec>,
    digest: Digest,
}

impl FixedCaseProgramRegistry {
    /// Borrows every fixed program in canonical typed-program order.
    pub fn programs(&self) -> &BTreeMap<CaseProgram, FixedCaseProgramSpec> {
        &self.programs
    }

    /// Borrows the canonical identity of all program routes and policies.
    pub fn digest(&self) -> &Digest {
        &self.digest
    }
}

/// Constructs and validates the complete fixed exact-engine program registry.
pub fn compile_fixed_case_program_registry()
-> Result<FixedCaseProgramRegistry, ConformanceDiagnosticSet> {
    let required = required_fixed_programs();
    let programs = required
        .iter()
        .cloned()
        .map(|program| {
            let spec = fixed_program_spec(program.clone());
            (program, spec)
        })
        .collect::<BTreeMap<_, _>>();
    if programs.keys().cloned().collect::<BTreeSet<_>>() != required {
        return Err(registry_error(
            "fixed program registry does not equal the required inventory",
        ));
    }
    if programs
        .iter()
        .any(|(program, spec)| program != &spec.program)
    {
        return Err(registry_error(
            "fixed program registry key and route disagree",
        ));
    }
    if programs.values().any(|spec| {
        spec.sdk_source != FixedProgramSdkSource::ExactArtifactPackage
            || !fixed_program_policy_is_valid(spec)
    }) {
        return Err(registry_error(
            "fixed program route is not production-bound",
        ));
    }
    let ordered_specs = programs.values().collect::<Vec<_>>();
    let digest = canonical_digest(DigestDomain::ConformanceProgramRegistry, &ordered_specs)
        .map_err(|_| registry_error("fixed program registry cannot be encoded canonically"))?;
    Ok(FixedCaseProgramRegistry { programs, digest })
}

fn fixed_program_spec(program: CaseProgram) -> FixedCaseProgramSpec {
    let (boundary, workspace, authority) = match &program {
        CaseProgram::CommonHarness { .. } => (
            FixedProgramBoundary::CommonHarnessSubject,
            FixedProgramWorkspace::IsolatedBaselineBranch,
            FixedProgramAuthority::CommonHarness,
        ),
        CaseProgram::StableConnector => (
            FixedProgramBoundary::StableConnectorDistribution,
            FixedProgramWorkspace::IsolatedBaselineBranch,
            FixedProgramAuthority::RustProduction,
        ),
        CaseProgram::CoreShape { .. } => (
            FixedProgramBoundary::PublicGeneratedCore,
            FixedProgramWorkspace::IsolatedBaselineBranch,
            FixedProgramAuthority::RustProduction,
        ),
        CaseProgram::EngineIntegration { .. } => (
            FixedProgramBoundary::SharedBaselineCli,
            FixedProgramWorkspace::IsolatedBaselineBranch,
            FixedProgramAuthority::RustProduction,
        ),
        CaseProgram::ModuleAuthoring {
            case: ModuleSignoffCase::PackagedSelfConsumer,
        } => (
            FixedProgramBoundary::ProductionModuleDispatcher,
            FixedProgramWorkspace::ExternalPackagedWorkspace,
            FixedProgramAuthority::RustProduction,
        ),
        CaseProgram::ModuleAuthoring { .. } => (
            FixedProgramBoundary::ProductionModuleDispatcher,
            FixedProgramWorkspace::IsolatedBaselineBranch,
            FixedProgramAuthority::RustProduction,
        ),
        CaseProgram::StandaloneClient {
            case:
                ClientSignoffCase::InitializedLocalClient
                | ClientSignoffCase::PinnedRemoteClient
                | ClientSignoffCase::SchemaRegeneration
                | ClientSignoffCase::CoreQuery
                | ClientSignoffCase::NamespacedModuleQuery,
        } => (
            FixedProgramBoundary::PublicGeneratedClient,
            FixedProgramWorkspace::ExternalPackagedWorkspace,
            FixedProgramAuthority::RustProduction,
        ),
        CaseProgram::DefinitiveGoClient { .. } => (
            FixedProgramBoundary::PublicRustClient,
            FixedProgramWorkspace::IsolatedBaselineBranch,
            FixedProgramAuthority::DefinitiveGoSource,
        ),
        CaseProgram::IntegrationAssertion { .. } => {
            unreachable!("per-authority integration assertions are not part of the fixed registry")
        }
    };
    FixedCaseProgramSpec {
        program,
        boundary,
        workspace,
        authority,
        sdk_source: FixedProgramSdkSource::ExactArtifactPackage,
    }
}

fn fixed_program_policy_is_valid(spec: &FixedCaseProgramSpec) -> bool {
    !matches!(spec.program, CaseProgram::IntegrationAssertion { .. })
        && fixed_program_spec(spec.program.clone()) == *spec
}

fn registry_error(detail: &'static str) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
        ConformanceDiagnosticCode::ConformanceCaseCatalogInvalid,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Case),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )])
    .expect("one registry diagnostic is non-empty")
}
