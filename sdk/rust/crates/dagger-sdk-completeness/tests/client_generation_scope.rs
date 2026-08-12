//! Engine-free exact-set properties for standalone-client scope and evidence.

use std::collections::BTreeSet;

use dagger_sdk_completeness::{
    CapabilityId, ClientAuthority, ClientDependencyScope, ClientEvidenceDomain,
    ClientEvidenceObservation, ClientEvidenceOutcome, ClientGenerationFormatVersion,
    ClientImplementationSubject, ClientReportSection, ClientTerminalStatus, Digest, EvidenceId,
    FeatureId, NonEmptyText, ResolvedLedger, Status, TargetDigest, admit_client_evidence,
    apply_client_ownership_correction, client_generation_scope_input, decode_canonical,
    derive_client_generation_scope,
};
use proptest::prelude::*;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // A status change is possible only for one complete, current, reviewed evidence domain.
    #[test]
    fn property_01_capability_scope_exact_attributable_evidence_gated(
        seed in any::<u8>(),
        scope_mutation in 0_u8..23,
        evidence_mutation in 0_u8..11,
        reverse_claims in any::<bool>(),
    ) {
        let target = target_digest(seed);
        let mut input = client_generation_scope_input(target.clone());
        let mapping_index = usize::from(seed) % input.mappings.len();
        match scope_mutation {
            0 => {}
            1 => { input.mappings.pop(); }
            2 => input.mappings.push(input.mappings[mapping_index].clone()),
            3 => input.mappings[mapping_index].capability_id = capability("policy/rust-policy/client-catch-all"),
            4 => input.mappings[mapping_index].capability_fingerprint = Digest::sha256([seed, 4]),
            5 => input.mappings[mapping_index].authority = match input.mappings[mapping_index].authority {
                ClientAuthority::GoClient => ClientAuthority::RustPolicy,
                _ => ClientAuthority::GoClient,
            },
            6 => input.mappings[mapping_index].requirement = text("unreviewed"),
            7 => input.mappings[mapping_index].implementation_subject = match input.mappings[mapping_index].implementation_subject {
                ClientImplementationSubject::Publication => ClientImplementationSubject::Initialization,
                _ => ClientImplementationSubject::Publication,
            },
            8 => input.mappings[mapping_index].evidence_domains.clear(),
            9 => input.mappings[mapping_index].allowed_terminal_status = ClientTerminalStatus::IdiomaticEquivalent,
            10 => input.mappings[mapping_index].report_section = match input.mappings[mapping_index].report_section {
                ClientReportSection::SdkSignoff => ClientReportSection::LocalClosure,
                _ => ClientReportSection::SdkSignoff,
            },
            11 => input.mappings[mapping_index].target_digest = target_digest(seed.wrapping_add(1)),
            12 => input.mappings[mapping_index].blocker = false,
            13 => input.target_digest = target_digest(seed.wrapping_add(1)),
            14 => input.dependency_scope = ClientDependencyScope::MergedTransitiveGraph,
            15 => input.ownership_corrections[0].status = Status::Implemented,
            16 => input.ownership_corrections[0].capability_fingerprint = Digest::sha256([seed, 16]),
            17 => input.ownership_corrections[0].to = FeatureId::Feature7,
            18 => input.preserved_boundaries[0].owner = FeatureId::Feature7,
            19 => input.preserved_boundaries[0].capability_fingerprint = Digest::sha256([seed, 19]),
            20 => { input.preserved_boundaries.pop(); }
            21 => input.mappings.push(unreviewed_mapping(&target)),
            22 => input.ownership_corrections[0].from = FeatureId::Feature6,
            _ => unreachable!(),
        }

        let derived = derive_client_generation_scope(&input, &target);
        if scope_mutation != 0 {
            prop_assert!(derived.is_err());
            return Ok(());
        }
        let scope = derived.unwrap();
        prop_assert_eq!(scope.mappings().len(), 25);
        prop_assert_eq!(scope.preserved_boundaries().len(), 3);
        prop_assert_eq!(&scope.ownership_correction().status, &Status::Partial);
        prop_assert_eq!(&scope.ownership_correction().to, &FeatureId::Feature3);
        let mappings_are_attributable = scope.mappings().values().all(|mapping| {
            !mapping.requirement.as_str().is_empty() && !mapping.evidence_domains.is_empty()
        });
        prop_assert!(mappings_are_attributable);

        let domains = [
            ClientEvidenceDomain::AdapterFixture,
            ClientEvidenceDomain::WorkspaceProperty,
            ClientEvidenceDomain::SchemaProperty,
            ClientEvidenceDomain::GeneratedApiProperty,
            ClientEvidenceDomain::ProjectProperty,
            ClientEvidenceDomain::PublicationProperty,
            ClientEvidenceDomain::QueryTransportProperty,
            ClientEvidenceDomain::DiagnosticSecurity,
            ClientEvidenceDomain::ImplementationClosure,
            ClientEvidenceDomain::ExactEngineSignoff,
        ];
        let selected_domain = domains[usize::from(seed) % domains.len()];
        let mut capability_ids = scope
            .mappings()
            .values()
            .filter(|mapping| mapping.evidence_domains.contains(&selected_domain))
            .map(|mapping| mapping.capability_id.clone())
            .collect::<Vec<_>>();
        if reverse_claims {
            capability_ids.reverse();
        }
        let mut observation = ClientEvidenceObservation {
            format_version: ClientGenerationFormatVersion::current(),
            evidence_id: EvidenceId::new(format!("client/property-{seed}")).unwrap(),
            target_digest: target.clone(),
            mapping_digest: scope.mapping_digest().clone(),
            domain: selected_domain,
            capability_ids,
            result: ClientEvidenceOutcome::Passed {
                observation_digest: Digest::sha256([seed, 1]),
            },
        };
        match evidence_mutation {
            0 => {}
            1 => observation.target_digest = target_digest(seed.wrapping_add(1)),
            2 => observation.mapping_digest = Digest::sha256([seed, 2]),
            3 => observation.result = ClientEvidenceOutcome::Failed { diagnostic: text("failed") },
            4 => observation.result = ClientEvidenceOutcome::Skipped { reason: text("skipped") },
            5 => { observation.capability_ids.pop(); }
            6 => observation.capability_ids.push(observation.capability_ids[0].clone()),
            7 => observation.capability_ids.push(capability("policy/rust-policy/client-outsider")),
            8 => observation.domain = ClientEvidenceDomain::EngineHook,
            9 => observation.domain = domains[(usize::from(seed) + 1) % domains.len()],
            10 => observation.capability_ids.clear(),
            _ => unreachable!(),
        }

        let admission = admit_client_evidence(&scope, &observation);
        let remaining = blocker_count(&admission.report.blockers);
        if evidence_mutation == 0 {
            prop_assert!(admission.rejection.is_none());
            prop_assert_eq!(admission.status_changes.len(), observation.capability_ids.len());
            prop_assert_eq!(remaining + admission.status_changes.len(), 25);
        } else {
            prop_assert!(admission.rejection.is_some());
            prop_assert!(admission.status_changes.is_empty());
            prop_assert_eq!(remaining, 25);
        }
        prop_assert_eq!(admission.report.blockers.len(), 7);
    }
}

#[test]
fn provision_correction_changes_only_the_reviewed_owner() {
    let target = target_digest(31);
    let scope =
        derive_client_generation_scope(&client_generation_scope_input(target.clone()), &target)
            .unwrap();
    let mut baseline: ResolvedLedger = decode_canonical(include_bytes!(
        "../../../completeness/artifacts/ledger.json"
    ))
    .unwrap();
    let capability_id = scope.ownership_correction().capability_id.clone();
    baseline
        .capabilities
        .get_mut(&capability_id)
        .unwrap()
        .owner_feature = Some(FeatureId::Feature7);
    let before = baseline.capabilities[&capability_id].clone();

    let corrected = apply_client_ownership_correction(&baseline, &scope).unwrap();
    let mut expected = before;
    expected.owner_feature = Some(FeatureId::Feature3);
    assert_eq!(corrected.capabilities[&capability_id], expected);
    assert_eq!(corrected.capabilities.len(), baseline.capabilities.len());
}

fn unreviewed_mapping(target: &TargetDigest) -> dagger_sdk_completeness::ClientGenerationMapping {
    dagger_sdk_completeness::ClientGenerationMapping {
        capability_id: capability("policy/rust-policy/client-outsider"),
        capability_fingerprint: Digest::sha256(b"outsider"),
        authority: ClientAuthority::RustPolicy,
        requirement: text("unreviewed"),
        implementation_subject: ClientImplementationSubject::EvidenceBoundary,
        rationale: text("unreviewed"),
        evidence_domains: BTreeSet::from([ClientEvidenceDomain::ImplementationClosure]),
        allowed_terminal_status: ClientTerminalStatus::Implemented,
        report_section: ClientReportSection::LocalClosure,
        target_digest: target.clone(),
        blocker: true,
    }
}

fn blocker_count(
    blockers: &std::collections::BTreeMap<ClientReportSection, BTreeSet<CapabilityId>>,
) -> usize {
    blockers.values().map(BTreeSet::len).sum()
}

fn target_digest(seed: u8) -> TargetDigest {
    TargetDigest::new(Digest::sha256([seed, 0x77]))
}

fn capability(value: &str) -> CapabilityId {
    CapabilityId::new(value).unwrap()
}

fn text(value: &str) -> NonEmptyText {
    NonEmptyText::new(value).unwrap()
}
