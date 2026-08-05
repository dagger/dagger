//! Validation of the immutable authority set named by a target descriptor.
//!
//! A target is not merely a collection of version strings: it binds the Dagger engine and schema,
//! the engine-selected Go authority, the scoped `sdk-sdk` harness, and the Rust build policy to
//! immutable revisions and digests. Adapters gather [`TargetObservation`] from those systems; this
//! module compares all observations without performing network or process I/O.

use std::ops::Deref;

use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::model::{
    AuthorityId, CanonicalSet, CommitSha, DaggerVersion, Digest, NonEmptyText, RepositoryId,
    RustEdition, SemverVersion, TargetDescriptor,
};

#[derive(Clone, Debug, Eq, PartialEq)]
/// Immutable resolution evidence for an optional Go SDK version label.
///
/// `ref_object` preserves the tag object's identity while `peeled_commit` is the commit that must
/// equal the engine-selected Go revision. This distinction matters for annotated Git tags.
pub struct GoVersionLabelResolution {
    pub label: NonEmptyText,
    pub ref_object: CommitSha,
    pub peeled_commit: CommitSha,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Independently observed facts against which a target descriptor is validated.
///
/// Observations are separate from [`TargetDescriptor`] so validation cannot accidentally prove a
/// claim using values copied from the claim itself.
pub struct TargetObservation {
    pub contract_format_version: SemverVersion,
    pub dagger_repository: RepositoryId,
    pub dagger_revision: CommitSha,
    pub engine_version: DaggerVersion,
    pub schema_version: NonEmptyText,
    pub schema_digest: Digest,
    pub go_sdk_repository: RepositoryId,
    pub engine_selected_go_revision: CommitSha,
    pub go_version_label_resolution: Option<GoVersionLabelResolution>,
    pub sdk_contract_repository: RepositoryId,
    pub sdk_contract_revision: CommitSha,
    pub harness_cli_version: DaggerVersion,
    pub harness_engine_version: DaggerVersion,
    pub rust_sdk_version: SemverVersion,
    pub rust_edition: RustEdition,
    pub rust_version: SemverVersion,
    pub source_digest_mismatches: CanonicalSet<AuthorityId>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// A target descriptor whose complete observed identity matched.
///
/// Construction is restricted to [`validate_target`], making this type evidence that no target
/// diagnostic was present at validation time.
pub struct ValidatedTargetDescriptor(TargetDescriptor);

impl ValidatedTargetDescriptor {
    /// Returns the validated durable descriptor.
    pub fn into_inner(self) -> TargetDescriptor {
        self.0
    }
}

impl Deref for ValidatedTargetDescriptor {
    type Target = TargetDescriptor;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

/// Validates every independently observable component of `target`.
///
/// All mismatches are accumulated to make drift actionable in one run. The descriptor is returned
/// only when the engine/schema, Go authority, harness, Rust policy, and observed authority digests
/// all agree.
pub fn validate_target(
    target: TargetDescriptor,
    observation: &TargetObservation,
) -> Validation<ValidatedTargetDescriptor> {
    let mut diagnostics = DiagnosticCollector::default();

    compare(
        &mut diagnostics,
        target.contract_format_version == observation.contract_format_version,
        DiagnosticCode::FormatUnsupported,
        "contract_format_version",
        &target.contract_format_version,
        &observation.contract_format_version,
    );
    compare(
        &mut diagnostics,
        target.dagger_repository == observation.dagger_repository,
        DiagnosticCode::TargetRepositoryInvalid,
        "dagger_repository",
        &target.dagger_repository,
        &observation.dagger_repository,
    );
    compare(
        &mut diagnostics,
        target.dagger_revision == observation.dagger_revision,
        DiagnosticCode::TargetRevisionInvalid,
        "dagger_revision",
        &target.dagger_revision,
        &observation.dagger_revision,
    );
    compare(
        &mut diagnostics,
        target.engine_version == observation.engine_version,
        DiagnosticCode::TargetVersionMismatch,
        "engine_version",
        &target.engine_version,
        &observation.engine_version,
    );
    compare(
        &mut diagnostics,
        target.schema_version == observation.schema_version,
        DiagnosticCode::SchemaVersionMismatch,
        "schema_version",
        &target.schema_version,
        &observation.schema_version,
    );
    compare(
        &mut diagnostics,
        target.schema_digest == observation.schema_digest,
        DiagnosticCode::SchemaDigestMismatch,
        "schema_digest",
        &target.schema_digest,
        &observation.schema_digest,
    );
    compare(
        &mut diagnostics,
        target.go_sdk_repository == observation.go_sdk_repository,
        DiagnosticCode::GoAuthorityInvalid,
        "go_sdk_repository",
        &target.go_sdk_repository,
        &observation.go_sdk_repository,
    );
    compare(
        &mut diagnostics,
        target.go_sdk_revision == observation.engine_selected_go_revision,
        DiagnosticCode::GoRevisionMismatch,
        "go_sdk_revision",
        &target.go_sdk_revision,
        &observation.engine_selected_go_revision,
    );
    validate_go_label(&target, observation, &mut diagnostics);
    compare(
        &mut diagnostics,
        target.sdk_contract_repository == observation.sdk_contract_repository,
        DiagnosticCode::SdkContractAuthorityInvalid,
        "sdk_contract_repository",
        &target.sdk_contract_repository,
        &observation.sdk_contract_repository,
    );
    compare(
        &mut diagnostics,
        target.sdk_contract_revision == observation.sdk_contract_revision,
        DiagnosticCode::SdkContractRevisionMismatch,
        "sdk_contract_revision",
        &target.sdk_contract_revision,
        &observation.sdk_contract_revision,
    );
    compare(
        &mut diagnostics,
        target.sdk_contract_cli_version == observation.harness_cli_version,
        DiagnosticCode::SdkContractTargetMismatch,
        "harness_cli_version",
        &target.sdk_contract_cli_version,
        &observation.harness_cli_version,
    );
    compare(
        &mut diagnostics,
        target.sdk_contract_cli_version == observation.harness_engine_version,
        DiagnosticCode::SdkContractTargetMismatch,
        "harness_engine_version",
        &target.sdk_contract_cli_version,
        &observation.harness_engine_version,
    );
    compare(
        &mut diagnostics,
        target.rust_sdk_version == observation.rust_sdk_version,
        DiagnosticCode::RustTargetMismatch,
        "rust_sdk_version",
        &target.rust_sdk_version,
        &observation.rust_sdk_version,
    );
    compare(
        &mut diagnostics,
        target.rust_edition == observation.rust_edition,
        DiagnosticCode::RustTargetMismatch,
        "rust_edition",
        &target.rust_edition,
        &observation.rust_edition,
    );
    compare(
        &mut diagnostics,
        target.rust_version == observation.rust_version,
        DiagnosticCode::RustTargetMismatch,
        "rust_version",
        &target.rust_version,
        &observation.rust_version,
    );

    // Source adapters own byte acquisition and hashing (Task 4). Keeping only their independently
    // observed mismatch IDs here preserves this validator's pure, deterministic boundary.
    for authority_id in observation.source_digest_mismatches.as_slice() {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::AuthorityDrift,
            authority_id.to_string(),
            None,
            "recorded source digest differs from the observed pinned source",
        ));
    }

    diagnostics.finish(ValidatedTargetDescriptor(target))
}

fn validate_go_label(
    target: &TargetDescriptor,
    observation: &TargetObservation,
    diagnostics: &mut DiagnosticCollector,
) {
    let Some(expected_label) = &target.go_sdk_version_label else {
        // Labels are convenience metadata, not required authority. A target that does not claim a
        // label remains anchored by the engine-selected full commit.
        return;
    };
    let Some(resolution) = &observation.go_version_label_resolution else {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::GoVersionLabelMismatch,
            "go_sdk_version_label",
            None,
            "the target declares a label but has no immutable resolution evidence",
        ));
        return;
    };

    // Compare the peeled commit, not merely the tag object: annotated tags have a distinct object
    // SHA and only the peeled commit proves equality with the engine-selected Go authority.
    if expected_label != &resolution.label || target.go_sdk_revision != resolution.peeled_commit {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::GoVersionLabelMismatch,
            "go_sdk_version_label",
            None,
            format!(
                "expected label {expected_label} to peel to {}, observed {} peeling to {}",
                target.go_sdk_revision, resolution.label, resolution.peeled_commit
            ),
        ));
    }
}

fn compare<T: std::fmt::Display>(
    diagnostics: &mut DiagnosticCollector,
    matches: bool,
    code: DiagnosticCode,
    subject: &'static str,
    expected: &T,
    observed: &T,
) {
    if !matches {
        diagnostics.push(ContractDiagnostic::new(
            code,
            subject,
            None,
            format!("expected {expected}, observed {observed}"),
        ));
    }
}

#[cfg(test)]
mod tests {
    use pretty_assertions::assert_eq;

    use super::*;

    fn target() -> TargetDescriptor {
        TargetDescriptor {
            contract_format_version: SemverVersion::new("1.0.0").unwrap(),
            dagger_repository: RepositoryId::new("github.com/dagger/dagger").unwrap(),
            dagger_revision: CommitSha::new("25300124ca110612edc09c43f89cb5fad6028170").unwrap(),
            engine_version: DaggerVersion::new("v1.0.0-beta.9").unwrap(),
            schema_version: NonEmptyText::new("v1").unwrap(),
            schema_digest: Digest::sha256("schema"),
            go_sdk_repository: RepositoryId::new("github.com/dagger/dagger-go-sdk").unwrap(),
            go_sdk_revision: CommitSha::new("1309520660f6a5b35ef97b4fbe151e32a06a8dc5").unwrap(),
            go_sdk_version_label: None,
            sdk_contract_repository: RepositoryId::new("github.com/dagger/sdk-sdk").unwrap(),
            sdk_contract_revision: CommitSha::new("8c164424b7a8a37b33a77367ef7547490d5b87b5")
                .unwrap(),
            sdk_contract_cli_version: DaggerVersion::new("v1.0.0-beta.9").unwrap(),
            rust_sdk_version: SemverVersion::new("1.0.0-beta.10").unwrap(),
            rust_edition: RustEdition::Edition2024,
            rust_version: SemverVersion::new("1.97.1").unwrap(),
            previous_target: None,
        }
    }

    fn observation(target: &TargetDescriptor) -> TargetObservation {
        TargetObservation {
            contract_format_version: target.contract_format_version.clone(),
            dagger_repository: target.dagger_repository.clone(),
            dagger_revision: target.dagger_revision.clone(),
            engine_version: target.engine_version.clone(),
            schema_version: target.schema_version.clone(),
            schema_digest: target.schema_digest.clone(),
            go_sdk_repository: target.go_sdk_repository.clone(),
            engine_selected_go_revision: target.go_sdk_revision.clone(),
            go_version_label_resolution: None,
            sdk_contract_repository: target.sdk_contract_repository.clone(),
            sdk_contract_revision: target.sdk_contract_revision.clone(),
            harness_cli_version: target.sdk_contract_cli_version.clone(),
            harness_engine_version: target.sdk_contract_cli_version.clone(),
            rust_sdk_version: target.rust_sdk_version.clone(),
            rust_edition: target.rust_edition.clone(),
            rust_version: target.rust_version.clone(),
            source_digest_mismatches: CanonicalSet::default(),
        }
    }

    #[test]
    fn matching_target_is_valid() {
        let target = target();
        let observation = observation(&target);

        assert_eq!(
            validate_target(target.clone(), &observation)
                .unwrap()
                .into_inner(),
            target
        );
    }

    #[test]
    fn independent_target_mismatches_accumulate() {
        let target = target();
        let mut observation = observation(&target);
        observation.engine_selected_go_revision = CommitSha::new("a".repeat(40)).unwrap();
        observation.schema_digest = Digest::sha256("other schema");
        observation.rust_edition = RustEdition::Edition2021;

        let diagnostics = validate_target(target, &observation).unwrap_err();
        let codes = diagnostics
            .as_slice()
            .iter()
            .map(|diagnostic| diagnostic.code)
            .collect::<Vec<_>>();

        assert_eq!(
            codes,
            vec![
                DiagnosticCode::GoRevisionMismatch,
                DiagnosticCode::RustTargetMismatch,
                DiagnosticCode::SchemaDigestMismatch,
            ]
        );
    }

    #[test]
    fn declared_go_label_requires_matching_immutable_resolution() {
        let mut target = target();
        target.go_sdk_version_label = Some(NonEmptyText::new("v0.21.7").unwrap());
        let mut observation = observation(&target);
        observation.go_version_label_resolution = Some(GoVersionLabelResolution {
            label: NonEmptyText::new("v0.21.7").unwrap(),
            ref_object: CommitSha::new("b".repeat(40)).unwrap(),
            peeled_commit: CommitSha::new("c".repeat(40)).unwrap(),
        });

        let diagnostics = validate_target(target, &observation).unwrap_err();
        assert_eq!(
            diagnostics.as_slice()[0].code,
            DiagnosticCode::GoVersionLabelMismatch
        );
    }
}
