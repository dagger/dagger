//! Builds the canonical generated-client evidence registry from passing release gates.
//!
//! The command does not execute verification itself. A release workflow invokes it only
//! after the recorded commands pass and supplies the exact-target record emitted by the
//! engine-backed conformance gate.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use clap::{Arg, Command, value_parser};
use dagger_sdk_completeness::{
    Architecture, CanonicalSet, CheckOutcome, CommandSpec, CommitSha, CoreCodegenEvidencePolicy,
    CoreCodegenEvidenceRecord, CoreCodegenEvidenceRegistry, CoreCodegenEvidenceResult, Digest,
    DigestDomain, EvidenceDomain, EvidenceId, EvidenceKind, EvidenceReference, EvidenceRegistry,
    ExecutableId, ExpectedOutcome, GeneratedBindingManifest, ManifestBindingKind, NonEmptyText,
    OperatingSystem, Platform, RepositoryId, RepositoryRelativePath, SourceLocator,
    TargetDescriptor, TargetDigest, canonical_bytes, canonical_digest, decode_canonical,
    verify_core_codegen_evidence,
};

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(detail) => {
            eprintln!("could not build core-codegen evidence registry: {detail}");
            ExitCode::from(2)
        }
    }
}

fn run() -> Result<(), &'static str> {
    let matches = Command::new("dagger-core-evidence-registry")
        .arg(
            Arg::new("root")
                .long("root")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("exact-target")
                .long("exact-target")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("registry-output")
                .long("registry-output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("policy-output")
                .long("policy-output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .arg(
            Arg::new("evidence-output")
                .long("evidence-output")
                .required(true)
                .value_parser(value_parser!(PathBuf)),
        )
        .get_matches();

    let root = matches.get_one::<PathBuf>("root").ok_or("root is absent")?;
    let exact_path = matches
        .get_one::<PathBuf>("exact-target")
        .ok_or("exact-target evidence path is absent")?;
    let registry_output = matches
        .get_one::<PathBuf>("registry-output")
        .ok_or("registry output path is absent")?;
    let policy_output = matches
        .get_one::<PathBuf>("policy-output")
        .ok_or("policy output path is absent")?;
    let evidence_output = matches
        .get_one::<PathBuf>("evidence-output")
        .ok_or("evidence output path is absent")?;

    let manifest: GeneratedBindingManifest =
        read_canonical(&root.join("completeness/artifacts/core-codegen-bindings.json"))?;
    let exact: CoreCodegenEvidenceRecord = read_canonical(exact_path)?;
    if exact.domains != BTreeSet::from([EvidenceDomain::ExactTarget]) {
        return Err("exact-target evidence declares an unexpected domain");
    }

    let subject_revision = exact.subject_revision.clone();
    let mut records = BTreeMap::from([(exact.evidence_id.clone(), exact.clone())]);
    let mut command_digests =
        BTreeMap::from([(EvidenceDomain::ExactTarget, command_digest(&exact.command)?)]);

    for (domain, evidence_id, assertion, command) in release_commands()? {
        let record = release_record(
            &manifest,
            subject_revision.clone(),
            domain,
            evidence_id,
            assertion,
            command,
        )?;
        command_digests.insert(domain, command_digest(&record.command)?);
        records.insert(record.evidence_id.clone(), record);
    }

    let registry = CoreCodegenEvidenceRegistry { records };
    let policy = CoreCodegenEvidencePolicy {
        subject_revision,
        command_digests,
    };
    let closure = verify_core_codegen_evidence(&manifest, &registry, &policy);
    if !closure.expired_evidence_ids().is_empty() {
        return Err("one or more generated evidence records failed admission");
    }

    write_canonical(registry_output, &registry)?;
    write_canonical(policy_output, &policy)?;
    publish_feature_one_evidence(
        root,
        &manifest,
        closure.closed_capability_ids(),
        evidence_output,
    )?;
    Ok(())
}

fn publish_feature_one_evidence(
    root: &Path,
    manifest: &GeneratedBindingManifest,
    closed: &BTreeSet<dagger_sdk_completeness::CapabilityId>,
    evidence_output: &Path,
) -> Result<(), &'static str> {
    if closed.is_empty() {
        return Err("core-codegen evidence closes no capabilities");
    }
    let mut evidence: EvidenceRegistry =
        read_canonical(&root.join("completeness/evidence/registry.json"))?;
    let target: TargetDescriptor = read_canonical(&root.join("completeness/target.json"))?;
    let target_digest = TargetDigest::new(
        canonical_digest(DigestDomain::Target, &target)
            .map_err(|_| "target digest could not be computed")?,
    );
    let implementation_id = evidence_id("implementation/core-codegen/generated-client")?;
    let verification_id = evidence_id("verification/core-codegen/release-closure")?;
    for (evidence_id, reference) in &mut evidence.evidence {
        if evidence_id != &implementation_id && evidence_id != &verification_id {
            reference.proved_capability_ids = CanonicalSet::new(
                reference
                    .proved_capability_ids
                    .iter()
                    .filter(|capability_id| !closed.contains(*capability_id))
                    .cloned(),
            );
        }
    }
    evidence
        .evidence
        .retain(|_, reference| !reference.proved_capability_ids.is_empty());
    for capability_id in closed {
        let binding = manifest
            .bindings
            .get(capability_id)
            .ok_or("closed capability has no manifest binding")?;
        if binding.binding_kind == ManifestBindingKind::IdiomaticEquivalent
            || binding.decision_id.is_some()
        {
            return Err("direct status publication encountered an idiomatic binding");
        }
        if !matches!(
            binding.authority_id.as_str(),
            "engine-schema" | "go-codegen" | "rust-policy"
        ) {
            return Err("closed capability has no reviewed core-codegen route");
        }
    }

    let repository = RepositoryId::new("github.com/dagger/dagger")
        .map_err(|_| "evidence repository is invalid")?;
    let revision = CommitSha::new(manifest.target_revision.clone())
        .map_err(|_| "evidence revision is invalid")?;
    let evidence_path =
        RepositoryRelativePath::new("sdk/rust/completeness/evidence/core-codegen-registry.json")
            .map_err(|_| "evidence path is invalid")?;
    let capability_ids = CanonicalSet::new(closed.iter().cloned());
    evidence.evidence.insert(
        implementation_id.clone(),
        EvidenceReference {
            evidence_id: implementation_id,
            evidence_kind: EvidenceKind::Implementation,
            repository: repository.clone(),
            revision: revision.clone(),
            path: evidence_path.clone(),
            locator: SourceLocator::new("registry:implementation/core-codegen/generated-client")
                .map_err(|_| "implementation locator is invalid")?,
            claim: NonEmptyText::new(
                "Checked generation reproduced the complete manifest-owned Rust client source",
            )
            .map_err(|_| "implementation claim is invalid")?,
            command: None,
            expected_outcome: None,
            execution_target: Some(target_digest.clone()),
            platform_scope: CanonicalSet::default(),
            proved_capability_ids: capability_ids.clone(),
        },
    );
    evidence.evidence.insert(
        verification_id.clone(),
        EvidenceReference {
            evidence_id: verification_id,
            evidence_kind: EvidenceKind::Verification,
            repository,
            revision,
            path: evidence_path,
            locator: SourceLocator::new("registry:closed-core-codegen-capabilities")
                .map_err(|_| "verification locator is invalid")?,
            claim: NonEmptyText::new(
                "Fresh generation, property, compile, projection, documentation, and exact-target records close every declared domain for this exact capability scope",
            )
            .map_err(|_| "verification claim is invalid")?,
            command: Some(feature_one_transition_command()?),
            expected_outcome: Some(ExpectedOutcome {
                outcome: CheckOutcome::Passed,
                assertion: NonEmptyText::new(
                    "Every published core-codegen status has complete fresh evidence and all unclosed capabilities remain blocking",
                )
                .map_err(|_| "verification assertion is invalid")?,
            }),
            execution_target: Some(target_digest),
            platform_scope: CanonicalSet::new([
                Platform {
                    operating_system: OperatingSystem::Linux,
                    architecture: Architecture::Arm64,
                },
                Platform {
                    operating_system: OperatingSystem::Macos,
                    architecture: Architecture::Arm64,
                },
            ]),
            proved_capability_ids: capability_ids,
        },
    );

    write_canonical(evidence_output, &evidence)
}

fn feature_one_transition_command() -> Result<CommandSpec, &'static str> {
    Ok(CommandSpec {
        program: ExecutableId::new("cargo").map_err(|_| "command program is invalid")?,
        args: [
            "run",
            "-p",
            "dagger-sdk-completeness",
            "--bin",
            "dagger-core-evidence-registry",
            "--locked",
            "--",
            "--root",
            ".",
            "--exact-target",
            "completeness/evidence/core-codegen-exact-target.json",
            "--registry-output",
            "completeness/evidence/core-codegen-registry.json",
            "--policy-output",
            "completeness/evidence/core-codegen-policy.json",
            "--evidence-output",
            "completeness/evidence/registry.json",
        ]
        .into_iter()
        .map(str::to_owned)
        .collect(),
        working_directory: RepositoryRelativePath::new("sdk/rust")
            .map_err(|_| "command working directory is invalid")?,
        environment: BTreeMap::new(),
    })
}

fn release_commands()
-> Result<Vec<(EvidenceDomain, EvidenceId, &'static str, CommandSpec)>, &'static str> {
    let workspace_tests = command(
        "cargo",
        ["test", "--workspace", "--all-features", "--locked"],
        BTreeMap::new(),
    )?;
    Ok(vec![
        (
            EvidenceDomain::Implementation,
            evidence_id("implementation/core-codegen/generated-client")?,
            "checked generation reproduced the complete owned artifact set",
            command(
                "cargo",
                [
                    "run",
                    "-p",
                    "dagger-bootstrap",
                    "--bin",
                    "dagger-rust",
                    "--locked",
                    "--",
                    "generate",
                    "--workspace",
                    ".",
                    "--check",
                ],
                BTreeMap::new(),
            )?,
        ),
        (
            EvidenceDomain::Property,
            evidence_id("verification/core-codegen/properties")?,
            "generated-domain properties passed for the complete projected catalog",
            workspace_tests.clone(),
        ),
        (
            EvidenceDomain::Compile,
            evidence_id("verification/core-codegen/compile")?,
            "positive, negative, and public-reachability compile suites passed",
            workspace_tests.clone(),
        ),
        (
            EvidenceDomain::QueryProjection,
            evidence_id("verification/core-codegen/query-projection")?,
            "every checked field and argument wire name passed query projection",
            workspace_tests,
        ),
        (
            EvidenceDomain::Documentation,
            evidence_id("verification/core-codegen/documentation")?,
            "warning-denied documentation passed for the generated public surface",
            command(
                "cargo",
                [
                    "doc",
                    "--workspace",
                    "--all-features",
                    "--no-deps",
                    "--locked",
                ],
                BTreeMap::from([("RUSTDOCFLAGS".to_owned(), "-D warnings".to_owned())]),
            )?,
        ),
    ])
}

fn command<const N: usize>(
    program: &str,
    args: [&str; N],
    environment: BTreeMap<String, String>,
) -> Result<CommandSpec, &'static str> {
    Ok(CommandSpec {
        program: ExecutableId::new(program).map_err(|_| "command program is invalid")?,
        args: args.into_iter().map(str::to_owned).collect(),
        working_directory: RepositoryRelativePath::new("sdk/rust")
            .map_err(|_| "command working directory is invalid")?,
        environment,
    })
}

fn evidence_id(value: &str) -> Result<EvidenceId, &'static str> {
    EvidenceId::new(value).map_err(|_| "evidence identity is invalid")
}

fn release_record(
    manifest: &GeneratedBindingManifest,
    subject_revision: Digest,
    domain: EvidenceDomain,
    evidence_id: EvidenceId,
    assertion: &str,
    command: CommandSpec,
) -> Result<CoreCodegenEvidenceRecord, &'static str> {
    let capability_ids = CanonicalSet::new(
        manifest
            .bindings
            .iter()
            .filter(|(_, binding)| binding.required_evidence.contains(&domain))
            .map(|(capability_id, _)| capability_id.clone()),
    );
    if capability_ids.is_empty() {
        return Err("evidence domain has no declared capability scope");
    }
    let scoped = capability_ids.iter().cloned().collect::<Vec<_>>();
    let result = CoreCodegenEvidenceResult {
        outcome: CheckOutcome::Passed,
        assertion: assertion.to_owned(),
        capability_scope_digest: Digest::sha256(
            serde_json::to_vec(&scoped).map_err(|_| "capability scope encoding failed")?,
        ),
    };
    let result_digest = canonical_digest(DigestDomain::Artifact, &result)
        .map_err(|_| "evidence result digest failed")?;
    let implementation_fingerprints = capability_ids
        .iter()
        .map(|capability_id| {
            let binding = manifest
                .bindings
                .get(capability_id)
                .ok_or("manifest binding disappeared")?;
            Ok((
                capability_id.clone(),
                binding.implementation_fingerprint.clone(),
            ))
        })
        .collect::<Result<BTreeMap<_, _>, &'static str>>()?;

    Ok(CoreCodegenEvidenceRecord {
        evidence_id,
        target_revision: CommitSha::new(manifest.target_revision.clone())
            .map_err(|_| "manifest target revision is invalid")?,
        schema_digest: Digest::new(manifest.schema_digest.clone())
            .map_err(|_| "manifest schema digest is invalid")?,
        subject_revision,
        command,
        result,
        result_digest,
        capability_ids,
        projection_fingerprint: manifest.projection_fingerprint.clone(),
        implementation_fingerprints,
        domains: BTreeSet::from([domain]),
    })
}

fn command_digest(command: &CommandSpec) -> Result<Digest, &'static str> {
    canonical_digest(DigestDomain::Artifact, command).map_err(|_| "command digest failed")
}

fn read_canonical<T>(path: &Path) -> Result<T, &'static str>
where
    T: serde::de::DeserializeOwned + serde::Serialize,
{
    decode_canonical(&fs::read(path).map_err(|_| "evidence input is unavailable")?)
        .map_err(|_| "evidence input is not canonical JSON")
}

fn write_canonical<T>(path: &Path, value: &T) -> Result<(), &'static str>
where
    T: serde::Serialize,
{
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|_| "evidence output directory is unavailable")?;
    }
    fs::write(
        path,
        canonical_bytes(value).map_err(|_| "evidence output encoding failed")?,
    )
    .map_err(|_| "evidence output could not be written")
}
