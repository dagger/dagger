//! Converts one complete engine matrix result into canonical capability-local evidence.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, Command, value_parser};
use dagger_sdk_completeness::{
    AllowedTerminalStatus, Architecture, CanonicalSet, CaseId, CaseObservation, CheckOutcome,
    CommandSpec, Digest, DigestDomain, EngineEvidenceDomain, EngineIntegrationEvidenceArtifact,
    EngineIntegrationMappings, EngineIntegrationObservation, EvidenceId, EvidenceKind,
    EvidenceReference, EvidenceRegistry, ExecutableId, ExpectedOutcome, NonEmptyText,
    OperatingSystem, Platform, RepositoryRelativePath, ResolvedLedger, SourceLocator,
    TargetDescriptor, TargetDigest, assemble_engine_integration_manifest, canonical_bytes,
    canonical_digest, decode_canonical, engine_integration_contract,
    validate_engine_integration_mappings, verify_engine_integration_evidence,
};
use dagger_sdk_engine::{
    EngineEvidenceSubject, FormatVersion, PublishedSdkDependency, TargetIdentity,
};
use serde::Deserialize;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawEngineEvidence {
    format_version: u32,
    cases: BTreeMap<String, String>,
    completeness_target_digest: String,
    descriptor_digest: String,
    engine_revision: String,
    engine_version: String,
    manifest_digest: String,
    mapping_digest: String,
    operation_input_digests: BTreeSet<String>,
    operation_manifest_digests: BTreeSet<String>,
    rust_toolchain: String,
    sdk_dependency: PublishedSdkDependency,
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(detail) => {
            eprintln!("could not build engine-integration evidence: {detail}");
            ExitCode::from(2)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = Command::new("dagger-engine-integration-evidence")
        .arg(
            Arg::new("root")
                .long("root")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("run")
                .long("run")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("evidence-output")
                .long("evidence-output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("registry-output")
                .long("registry-output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .get_matches();

    let root = matches.get_one::<PathBuf>("root").ok_or("root is absent")?;
    let run_path = matches
        .get_one::<PathBuf>("run")
        .ok_or("run path is absent")?;
    let evidence_output = matches
        .get_one::<PathBuf>("evidence-output")
        .ok_or("evidence output path is absent")?;
    let registry_output = matches
        .get_one::<PathBuf>("registry-output")
        .ok_or("registry output path is absent")?;
    let contract = root.join("completeness");

    let target: TargetDescriptor = read_canonical(&contract.join("target.json"))?;
    let ledger: ResolvedLedger = read_canonical(&contract.join("artifacts/ledger.json"))?;
    let mapping_path = contract.join("engine-integration-mappings.json");
    let mapping_bytes = fs::read(&mapping_path).map_err(|_| "mapping bytes are unavailable")?;
    let mappings: EngineIntegrationMappings =
        decode_canonical(&mapping_bytes).map_err(|_| "mappings are not canonical")?;
    let raw: RawEngineEvidence =
        serde_json::from_slice(&fs::read(run_path).map_err(|_| "engine run is unavailable")?)
            .map_err(|_| "engine run is malformed")?;

    let target_digest = TargetDigest::new(
        canonical_digest(DigestDomain::Target, &target).map_err(|_| "target digest failed")?,
    );
    validate_raw_subject(&raw, &target, &target_digest, &mapping_bytes)?;
    let policy = engine_integration_contract().scope;
    let validated =
        validate_engine_integration_mappings(&mappings, &ledger, &policy, &target_digest)
            .map_err(|_| "engine mappings do not match the current ledger")?;

    let implementation_id = EvidenceId::new("implementation/feature-5-engine-integration").unwrap();
    let decision_id = EvidenceId::new("decision/feature-5-engine-idiomatic-equivalences").unwrap();
    let implementation_evidence = validated
        .mappings()
        .keys()
        .cloned()
        .map(|capability_id| (capability_id, implementation_id.clone()))
        .collect();
    let decision_evidence = validated
        .mappings()
        .iter()
        .filter(|(_, mapping)| {
            matches!(
                mapping.allowed_terminal_status,
                AllowedTerminalStatus::IdiomaticEquivalent
            )
        })
        .map(|(capability_id, _)| (capability_id.clone(), decision_id.clone()))
        .collect();
    let cases = raw_cases(&raw)?;
    let subject = evidence_subject(&raw, &target)?;
    let manifest = assemble_engine_integration_manifest(
        &validated,
        &policy,
        subject.clone(),
        CanonicalSet::new(cases.keys().cloned()),
        implementation_evidence,
        decision_evidence,
    )
    .map_err(|_| "engine evidence manifest is invalid")?;

    let mut domains = BTreeSet::new();
    for mapping in validated.mappings().values() {
        domains.extend(mapping.evidence_domains.iter().cloned());
    }
    let observations = domains
        .into_iter()
        .map(|domain| {
            let proved_capabilities = CanonicalSet::new(
                validated
                    .mappings()
                    .iter()
                    .filter(|(_, mapping)| mapping.evidence_domains.contains(&domain))
                    .map(|(capability_id, _)| capability_id.clone()),
            );
            EngineIntegrationObservation {
                format_version: 1,
                evidence_id: verification_id(&domain),
                subject: subject.clone(),
                evidence_domain: domain,
                cases: cases.clone(),
                proved_capabilities,
            }
        })
        .collect::<Vec<_>>();
    let closure = verify_engine_integration_evidence(&manifest, &observations)
        .map_err(|_| "engine observations failed exact-target admission")?;
    if closure.complete_capabilities() != &policy.capability_ids()
        || !closure.missing_domains().is_empty()
    {
        return Err("engine observations do not close the exact approved scope");
    }

    let artifact = EngineIntegrationEvidenceArtifact {
        format_version: 1,
        mapping_digest: Digest::sha256(mapping_bytes),
        manifest,
        observations: observations.clone(),
    };
    let registry = evidence_registry(
        &target,
        &target_digest,
        implementation_id,
        decision_id,
        &artifact,
    )?;
    write_canonical(evidence_output, &artifact)?;
    write_canonical(registry_output, &registry)
}

fn validate_raw_subject(
    raw: &RawEngineEvidence,
    target: &TargetDescriptor,
    target_digest: &TargetDigest,
    mapping_bytes: &[u8],
) -> Result<(), &'static str> {
    if raw.format_version != 1
        || raw.completeness_target_digest != target_digest.to_string()
        || raw.engine_revision != target.dagger_revision.as_str()
        || raw.engine_version != target.engine_version.to_string()
        || raw.rust_toolchain != target.rust_version.to_string()
        || Digest::new(&raw.mapping_digest).ok() != Some(Digest::sha256(mapping_bytes))
        || Digest::new(&raw.descriptor_digest).is_err()
        || Digest::new(&raw.manifest_digest).is_err()
    {
        return Err("engine run subject differs from the checked target or mappings");
    }
    Ok(())
}

fn raw_cases(raw: &RawEngineEvidence) -> Result<BTreeMap<CaseId, CaseObservation>, &'static str> {
    const REQUIRED: [&str; 10] = [
        "resolution",
        "init-empty",
        "init-existing",
        "init-no-generate",
        "operations",
        "runtime-checked",
        "runtime-legacy",
        "negative-generated-lock-toolchain",
        "negative-path-ownership",
        "negative-redaction",
    ];
    if raw.cases.len() != REQUIRED.len()
        || REQUIRED.iter().any(|name| !raw.cases.contains_key(*name))
    {
        return Err("engine run does not contain the complete closed case set");
    }
    raw.cases
        .iter()
        .map(|(name, digest)| {
            Ok((
                CaseId::new(name).map_err(|_| "case identity is malformed")?,
                CaseObservation::Passed {
                    observation_digest: Digest::new(digest)
                        .map_err(|_| "case digest is malformed")?,
                },
            ))
        })
        .collect()
}

fn evidence_subject(
    raw: &RawEngineEvidence,
    target: &TargetDescriptor,
) -> Result<EngineEvidenceSubject, &'static str> {
    let target_identity = TargetIdentity {
        format_version: FormatVersion,
        repository: format!("https://{}", target.dagger_repository)
            .parse()
            .map_err(|_| "target repository is invalid")?,
        dagger_revision: target
            .dagger_revision
            .as_str()
            .parse()
            .map_err(|_| "target revision is invalid")?,
        engine_version: target
            .engine_version
            .to_string()
            .trim_start_matches('v')
            .parse()
            .map_err(|_| "engine version is invalid")?,
        rust_sdk_version: target
            .rust_sdk_version
            .to_string()
            .parse()
            .map_err(|_| "Rust SDK version is invalid")?,
        rust_toolchain: target
            .rust_version
            .to_string()
            .parse()
            .map_err(|_| "Rust toolchain is invalid")?,
        core_schema_digest: target
            .schema_digest
            .as_str()
            .parse()
            .map_err(|_| "schema digest is invalid")?,
    };
    let operation_input_digests = raw
        .operation_input_digests
        .iter()
        .map(|digest| {
            digest
                .parse()
                .map_err(|_| "operation input digest is invalid")
        })
        .collect::<Result<BTreeSet<_>, _>>()?;
    let operation_manifest_digests = raw
        .operation_manifest_digests
        .iter()
        .map(|digest| {
            digest
                .parse()
                .map_err(|_| "operation manifest digest is invalid")
        })
        .collect::<Result<BTreeSet<_>, _>>()?;
    if operation_input_digests.is_empty() || operation_manifest_digests.is_empty() {
        return Err("engine run omitted operation provenance");
    }
    Ok(EngineEvidenceSubject {
        target: target_identity,
        engine_source_digest: raw
            .descriptor_digest
            .parse()
            .map_err(|_| "descriptor digest is invalid")?,
        packaged_assets_digest: raw
            .manifest_digest
            .parse()
            .map_err(|_| "manifest digest is invalid")?,
        sdk_dependency: raw.sdk_dependency.clone(),
        rust_toolchain: target
            .rust_version
            .to_string()
            .parse()
            .map_err(|_| "Rust toolchain is invalid")?,
        operation_input_digests,
        operation_manifest_digests,
    })
}

fn evidence_registry(
    target: &TargetDescriptor,
    target_digest: &TargetDigest,
    implementation_id: EvidenceId,
    decision_id: EvidenceId,
    artifact: &EngineIntegrationEvidenceArtifact,
) -> Result<EvidenceRegistry, &'static str> {
    let all_capabilities = CanonicalSet::new(artifact.manifest.mappings.keys().cloned());
    let idiomatic_capabilities = CanonicalSet::new(
        artifact
            .manifest
            .mappings
            .iter()
            .filter(|(_, mapping)| {
                matches!(
                    mapping.allowed_terminal_status,
                    AllowedTerminalStatus::IdiomaticEquivalent
                )
            })
            .map(|(capability_id, _)| capability_id.clone()),
    );
    let mut evidence = BTreeMap::new();
    evidence.insert(
        implementation_id.clone(),
        reference(
            target,
            target_digest,
            implementation_id,
            EvidenceKind::Implementation,
            "sdk/rust/crates/dagger-sdk-engine/src/lib.rs",
            "engine-integration-private-implementation",
            "Private Rust engine integration implements the mapped adapter contracts",
            all_capabilities,
            false,
        )?,
    );
    evidence.insert(
        decision_id.clone(),
        reference(
            target,
            target_digest,
            decision_id,
            EvidenceKind::Decision,
            ".kiro/specs/rust-sdk-engine-integration/design.md",
            "reviewed-rust-engine-equivalences",
            "Reviewed Rust-native mappings preserve the observable Go engine invariants",
            idiomatic_capabilities,
            false,
        )?,
    );
    for observation in &artifact.observations {
        evidence.insert(
            observation.evidence_id.clone(),
            reference(
                target,
                target_digest,
                observation.evidence_id.clone(),
                EvidenceKind::Verification,
                "toolchains/rust-sdk-dev/main.go",
                domain_slug(&observation.evidence_domain),
                "The complete exact-target engine matrix passed for this capability-local domain",
                observation.proved_capabilities.clone(),
                true,
            )?,
        );
    }
    Ok(EvidenceRegistry { evidence })
}

#[allow(clippy::too_many_arguments)]
fn reference(
    target: &TargetDescriptor,
    target_digest: &TargetDigest,
    evidence_id: EvidenceId,
    evidence_kind: EvidenceKind,
    path: &str,
    locator: &str,
    claim: &str,
    proved_capability_ids: CanonicalSet<dagger_sdk_completeness::CapabilityId>,
    executable: bool,
) -> Result<EvidenceReference, &'static str> {
    Ok(EvidenceReference {
        evidence_id,
        evidence_kind,
        repository: target.dagger_repository.clone(),
        revision: target.dagger_revision.clone(),
        path: RepositoryRelativePath::new(path).map_err(|_| "evidence path is invalid")?,
        locator: SourceLocator::new(locator).map_err(|_| "evidence locator is invalid")?,
        claim: NonEmptyText::new(claim).map_err(|_| "evidence claim is invalid")?,
        command: executable.then(|| CommandSpec {
            program: ExecutableId::new("dagger").unwrap(),
            args: vec![
                "call".to_owned(),
                "-m".to_owned(),
                "../../toolchains/rust-sdk-dev".to_owned(),
                "engine-content".to_owned(),
                "engine-evidence".to_owned(),
            ],
            working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
            environment: BTreeMap::new(),
        }),
        expected_outcome: executable.then(|| ExpectedOutcome {
            outcome: CheckOutcome::Passed,
            assertion: NonEmptyText::new("Complete engine matrix passed").unwrap(),
        }),
        execution_target: Some(target_digest.clone()),
        platform_scope: if executable {
            CanonicalSet::new([Platform {
                operating_system: OperatingSystem::Linux,
                architecture: Architecture::Amd64,
            }])
        } else {
            CanonicalSet::default()
        },
        proved_capability_ids,
    })
}

fn verification_id(domain: &EngineEvidenceDomain) -> EvidenceId {
    EvidenceId::new(format!(
        "verification/feature-5-engine/{}",
        domain_slug(domain)
    ))
    .unwrap()
}

fn domain_slug(domain: &EngineEvidenceDomain) -> &'static str {
    match domain {
        EngineEvidenceDomain::SdkResolution => "sdk-resolution",
        EngineEvidenceDomain::Initialization => "initialization",
        EngineEvidenceDomain::LibraryOperation => "library-operation",
        EngineEvidenceDomain::ModuleOperation => "module-operation",
        EngineEvidenceDomain::ClientHook => "client-hook",
        EngineEvidenceDomain::EntrypointHook => "entrypoint-hook",
        EngineEvidenceDomain::RuntimeConstruction => "runtime-construction",
        EngineEvidenceDomain::RuntimeProtocol => "runtime-protocol",
        EngineEvidenceDomain::Packaging => "packaging",
        EngineEvidenceDomain::Isolation => "isolation",
        EngineEvidenceDomain::ScopePolicy => "scope-policy",
    }
}

fn read_canonical<T>(path: &Path) -> Result<T, &'static str>
where
    T: serde::de::DeserializeOwned + serde::Serialize,
{
    decode_canonical(&fs::read(path).map_err(|_| "contract artifact is unavailable")?)
        .map_err(|_| "contract artifact is not canonical")
}

fn write_canonical<T: serde::Serialize>(path: &Path, value: &T) -> Result<(), &'static str> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|_| "evidence output directory is unavailable")?;
    }
    fs::write(
        path,
        canonical_bytes(value).map_err(|_| "evidence output encoding failed")?,
    )
    .map_err(|_| "evidence output could not be written")
}
