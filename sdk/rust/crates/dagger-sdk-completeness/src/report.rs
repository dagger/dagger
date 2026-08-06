//! Deterministic completeness reports, human projection, and independent gate policy.
//!
//! The JSON report is the sole report model. Markdown is a pure projection of that value, so a
//! human summary cannot hide a blocker, exception, zero-count category, or integrity diagnostic.
//! Integrity depends only on contract diagnostics; completeness additionally requires the absence
//! of `Missing` and `Partial` ledger rows.

use std::collections::BTreeMap;
use std::fmt::Write as _;

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::ContractDiagnostic;
use crate::model::{
    AuthorityId, AuthorityRegistry, CanonicalInventory, CanonicalSet, CapabilityKind,
    CompleteException, CompletenessReport, FeatureId, ResolvedLedger, SemverVersion, Status,
    TargetDescriptor,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
/// Verdict selected by a command invocation.
pub enum Gate {
    Integrity,
    Completeness,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
/// Named policy profiles that prevent CI and release callers from choosing opposite defaults.
pub enum GateProfile {
    InitialCi,
    Feature9Release,
}

/// Returns the immutable gate selected by a named policy profile.
pub const fn profile_gate(profile: GateProfile) -> Gate {
    match profile {
        GateProfile::InitialCi => Gate::Integrity,
        GateProfile::Feature9Release => Gate::Completeness,
    }
}

/// Returns whether `report` passes the explicitly selected verdict.
pub const fn gate_passes(report: &CompletenessReport, gate: Gate) -> bool {
    match gate {
        Gate::Integrity => report.integrity_verdict,
        Gate::Completeness => report.completeness_verdict,
    }
}

/// Maps a complete report to the command's success or false-verdict exit status.
///
/// Operational failures use status 2 before a report reaches this function.
pub const fn gate_exit_status(report: &CompletenessReport, gate: Gate) -> u8 {
    if gate_passes(report, gate) { 0 } else { 1 }
}

/// Aggregates one validated target, inventory, and ledger into the canonical report value.
pub fn build_report(
    contract_format_version: SemverVersion,
    target: &TargetDescriptor,
    authorities: &AuthorityRegistry,
    inventory: &CanonicalInventory,
    ledger: &ResolvedLedger,
    diagnostics: impl IntoIterator<Item = ContractDiagnostic>,
) -> CompletenessReport {
    let mut integrity_errors = diagnostics.into_iter().collect::<Vec<_>>();
    integrity_errors.sort_unstable();

    let mut counts_by_authority = authorities
        .authorities
        .keys()
        .cloned()
        .map(|authority| (authority, 0))
        .collect::<BTreeMap<AuthorityId, u64>>();
    let mut counts_by_kind = inventory
        .capabilities
        .values()
        .map(|definition| (definition.capability_kind.clone(), 0))
        .collect::<BTreeMap<CapabilityKind, u64>>();
    for definition in inventory.capabilities.values() {
        *counts_by_authority
            .entry(definition.authority_id.clone())
            .or_default() += 1;
        *counts_by_kind
            .entry(definition.capability_kind.clone())
            .or_default() += 1;
    }

    let mut counts_by_status = all_statuses()
        .into_iter()
        .map(|status| (status, 0))
        .collect::<BTreeMap<_, _>>();
    let mut counts_by_owner = all_features()
        .into_iter()
        .map(|feature| (feature, 0))
        .collect::<BTreeMap<_, _>>();
    let mut blocking_capabilities = Vec::new();
    let mut complete_exceptions = Vec::new();
    for record in ledger.capabilities.values() {
        *counts_by_status.entry(record.status.clone()).or_default() += 1;
        if let Some(owner) = &record.owner_feature {
            *counts_by_owner.entry(owner.clone()).or_default() += 1;
        }
        if matches!(record.status, Status::Missing | Status::Partial) {
            blocking_capabilities.push(record.capability_id.clone());
        }
        if matches!(
            record.status,
            Status::IdiomaticEquivalent | Status::Inapplicable
        ) {
            complete_exceptions.push(CompleteException {
                capability_id: record.capability_id.clone(),
                status: record.status.clone(),
                decision_evidence: record.decision_evidence.clone(),
            });
        }
    }
    complete_exceptions.sort_unstable_by(|left, right| {
        (&left.capability_id, &left.status).cmp(&(&right.capability_id, &right.status))
    });

    let integrity_verdict = integrity_errors.is_empty();
    let completeness_verdict = integrity_verdict && blocking_capabilities.is_empty();
    CompletenessReport {
        contract_format_version,
        target_descriptor: target.clone(),
        inventory_digest: canonical_digest(DigestDomain::Artifact, inventory)
            .expect("validated CanonicalInventory must have a canonical artifact digest"),
        ledger_digest: canonical_digest(DigestDomain::Artifact, ledger)
            .expect("validated ResolvedLedger must have a canonical artifact digest"),
        integrity_verdict,
        completeness_verdict,
        counts_by_authority,
        counts_by_kind,
        counts_by_status,
        counts_by_owner,
        integrity_errors,
        blocking_capabilities: CanonicalSet::new(blocking_capabilities),
        complete_exceptions,
    }
}

/// Renders the stable human report solely from its machine-readable counterpart.
pub fn render_human_report(report: &CompletenessReport) -> String {
    let mut output = String::new();
    writeln!(output, "# Rust SDK completeness report").expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    writeln!(output, "## Target").expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    writeln!(
        output,
        "- Dagger: {}",
        report.target_descriptor.engine_version
    )
    .expect("String writes cannot fail");
    writeln!(
        output,
        "- Dagger revision: `{}`",
        report.target_descriptor.dagger_revision
    )
    .expect("String writes cannot fail");
    writeln!(
        output,
        "- Rust SDK: {}",
        report.target_descriptor.rust_sdk_version
    )
    .expect("String writes cannot fail");
    writeln!(output, "- Inventory digest: `{}`", report.inventory_digest)
        .expect("String writes cannot fail");
    writeln!(output, "- Ledger digest: `{}`", report.ledger_digest)
        .expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    writeln!(output, "## Verdicts").expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    writeln!(
        output,
        "- Integrity: {}",
        verdict_label(report.integrity_verdict)
    )
    .expect("String writes cannot fail");
    writeln!(
        output,
        "- Completeness: {}",
        verdict_label(report.completeness_verdict)
    )
    .expect("String writes cannot fail");

    render_counts(
        &mut output,
        "Counts by authority",
        report
            .counts_by_authority
            .iter()
            .map(|(key, value)| (key.to_string(), *value)),
    );
    render_counts(
        &mut output,
        "Counts by capability kind",
        report
            .counts_by_kind
            .iter()
            .map(|(key, value)| (key.to_string(), *value)),
    );
    render_counts(
        &mut output,
        "Counts by status",
        report
            .counts_by_status
            .iter()
            .map(|(key, value)| (status_label(key).to_owned(), *value)),
    );
    render_counts(
        &mut output,
        "Counts by owner",
        report
            .counts_by_owner
            .iter()
            .map(|(key, value)| (feature_label(key).to_owned(), *value)),
    );

    writeln!(output).expect("String writes cannot fail");
    writeln!(output, "## Blocking capabilities").expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    if report.blocking_capabilities.is_empty() {
        writeln!(output, "None.").expect("String writes cannot fail");
    } else {
        for capability_id in report.blocking_capabilities.iter() {
            writeln!(output, "- `{capability_id}`").expect("String writes cannot fail");
        }
    }

    writeln!(output).expect("String writes cannot fail");
    writeln!(output, "## Complete exceptions").expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    if report.complete_exceptions.is_empty() {
        writeln!(output, "None.").expect("String writes cannot fail");
    } else {
        for exception in &report.complete_exceptions {
            let evidence = exception
                .decision_evidence
                .iter()
                .map(ToString::to_string)
                .collect::<Vec<_>>()
                .join(", ");
            writeln!(
                output,
                "- `{}` — {} — decision evidence: {}",
                exception.capability_id,
                status_label(&exception.status),
                evidence
            )
            .expect("String writes cannot fail");
        }
    }

    writeln!(output).expect("String writes cannot fail");
    writeln!(output, "## Integrity diagnostics").expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    if report.integrity_errors.is_empty() {
        writeln!(output, "None.").expect("String writes cannot fail");
    } else {
        for diagnostic in &report.integrity_errors {
            let locator = diagnostic
                .locator
                .as_ref()
                .map(|locator| format!(" at `{locator}`"))
                .unwrap_or_default();
            writeln!(
                output,
                "- `{}` `{}`{} — {}",
                diagnostic.code, diagnostic.subject, locator, diagnostic.detail
            )
            .expect("String writes cannot fail");
        }
    }
    output
}

fn render_counts(
    output: &mut String,
    heading: &str,
    counts: impl IntoIterator<Item = (String, u64)>,
) {
    writeln!(output).expect("String writes cannot fail");
    writeln!(output, "## {heading}").expect("String writes cannot fail");
    writeln!(output).expect("String writes cannot fail");
    for (name, count) in counts {
        writeln!(output, "- {name}: {count}").expect("String writes cannot fail");
    }
}

const fn verdict_label(verdict: bool) -> &'static str {
    if verdict { "PASS" } else { "FAIL" }
}

const fn status_label(status: &Status) -> &'static str {
    match status {
        Status::Missing => "Missing",
        Status::Partial => "Partial",
        Status::Implemented => "Implemented",
        Status::IdiomaticEquivalent => "Idiomatic_Equivalent",
        Status::Inapplicable => "Inapplicable",
    }
}

const fn feature_label(feature: &FeatureId) -> &'static str {
    match feature {
        FeatureId::Feature2 => "feature-2",
        FeatureId::Feature3 => "feature-3",
        FeatureId::Feature4 => "feature-4",
        FeatureId::Feature5 => "feature-5",
        FeatureId::Feature6 => "feature-6",
        FeatureId::Feature7 => "feature-7",
        FeatureId::Feature8 => "feature-8",
        FeatureId::Feature9 => "feature-9",
    }
}

fn all_statuses() -> [Status; 5] {
    [
        Status::Missing,
        Status::Partial,
        Status::Implemented,
        Status::IdiomaticEquivalent,
        Status::Inapplicable,
    ]
}

fn all_features() -> [FeatureId; 8] {
    [
        FeatureId::Feature2,
        FeatureId::Feature3,
        FeatureId::Feature4,
        FeatureId::Feature5,
        FeatureId::Feature6,
        FeatureId::Feature7,
        FeatureId::Feature8,
        FeatureId::Feature9,
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn policy_profiles_select_independent_verdicts() {
        assert_eq!(profile_gate(GateProfile::InitialCi), Gate::Integrity);
        assert_eq!(
            profile_gate(GateProfile::Feature9Release),
            Gate::Completeness
        );
    }
}
