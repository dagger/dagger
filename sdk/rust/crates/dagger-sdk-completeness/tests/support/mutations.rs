//! Named, independently composable target-identity mutations.
//!
//! A mutation changes one observed contract condition and owns the diagnostic that condition must
//! produce. Sets are applied in stable order, allowing a property to compare validation with the
//! exact multiset of expected defects instead of inferring intent from an opaque bitmask.

use std::collections::BTreeSet;

use dagger_sdk_completeness::*;
use proptest::collection::btree_set;
use proptest::prelude::*;
use serde::Deserialize;

use super::contract_case::equivalent_contract_cases_strategy;
use super::scalars::alternate_commit;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TargetCase {
    pub target: TargetDescriptor,
    pub observation: TargetObservation,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, Hash, Ord, PartialEq, PartialOrd)]
#[serde(rename_all = "kebab-case")]
pub enum TargetMutation {
    ContractFormat,
    DaggerRepository,
    DaggerRevision,
    EngineVersion,
    SchemaVersion,
    SchemaDigest,
    GoRepository,
    GoRevision,
    GoVersionLabel,
    HarnessRepository,
    HarnessRevision,
    HarnessCliVersion,
    HarnessEngineVersion,
    RustSdkVersion,
    RustEdition,
    RustVersion,
    AuthoritySourceDigest,
}

impl TargetMutation {
    pub const ALL: [Self; 17] = [
        Self::ContractFormat,
        Self::DaggerRepository,
        Self::DaggerRevision,
        Self::EngineVersion,
        Self::SchemaVersion,
        Self::SchemaDigest,
        Self::GoRepository,
        Self::GoRevision,
        Self::GoVersionLabel,
        Self::HarnessRepository,
        Self::HarnessRevision,
        Self::HarnessCliVersion,
        Self::HarnessEngineVersion,
        Self::RustSdkVersion,
        Self::RustEdition,
        Self::RustVersion,
        Self::AuthoritySourceDigest,
    ];

    pub const fn expected_code(self) -> DiagnosticCode {
        match self {
            Self::ContractFormat => DiagnosticCode::FormatUnsupported,
            Self::DaggerRepository => DiagnosticCode::TargetRepositoryInvalid,
            Self::DaggerRevision => DiagnosticCode::TargetRevisionInvalid,
            Self::EngineVersion => DiagnosticCode::TargetVersionMismatch,
            Self::SchemaVersion => DiagnosticCode::SchemaVersionMismatch,
            Self::SchemaDigest => DiagnosticCode::SchemaDigestMismatch,
            Self::GoRepository => DiagnosticCode::GoAuthorityInvalid,
            Self::GoRevision => DiagnosticCode::GoRevisionMismatch,
            Self::GoVersionLabel => DiagnosticCode::GoVersionLabelMismatch,
            Self::HarnessRepository => DiagnosticCode::SdkContractAuthorityInvalid,
            Self::HarnessRevision => DiagnosticCode::SdkContractRevisionMismatch,
            Self::HarnessCliVersion | Self::HarnessEngineVersion => {
                DiagnosticCode::SdkContractTargetMismatch
            }
            Self::RustSdkVersion | Self::RustEdition | Self::RustVersion => {
                DiagnosticCode::RustTargetMismatch
            }
            Self::AuthoritySourceDigest => DiagnosticCode::AuthorityDrift,
        }
    }

    fn apply(self, case: &mut TargetCase) {
        match self {
            Self::ContractFormat => {
                case.observation.contract_format_version =
                    bump_semver(&case.target.contract_format_version);
            }
            Self::DaggerRepository => {
                case.observation.dagger_repository =
                    RepositoryId::new("github.com/dagger/other-dagger").unwrap();
            }
            Self::DaggerRevision => {
                case.observation.dagger_revision = alternate_commit(&case.target.dagger_revision);
            }
            Self::EngineVersion => {
                case.observation.engine_version = bump_dagger_version(&case.target.engine_version);
            }
            Self::SchemaVersion => {
                case.observation.schema_version =
                    NonEmptyText::new(format!("{}-other", case.target.schema_version)).unwrap();
            }
            Self::SchemaDigest => {
                case.observation.schema_digest = Digest::sha256("other-schema");
            }
            Self::GoRepository => {
                case.observation.go_sdk_repository =
                    RepositoryId::new("github.com/dagger/other-go-sdk").unwrap();
            }
            Self::GoRevision => {
                case.observation.engine_selected_go_revision =
                    alternate_commit(&case.target.go_sdk_revision);
            }
            Self::GoVersionLabel => {
                let label = NonEmptyText::new("v0.21.7").unwrap();
                case.target.go_sdk_version_label = Some(label.clone());
                case.observation.go_version_label_resolution = Some(GoVersionLabelResolution {
                    label,
                    ref_object: alternate_commit(&case.target.go_sdk_revision),
                    peeled_commit: alternate_commit(&case.target.go_sdk_revision),
                });
            }
            Self::HarnessRepository => {
                case.observation.sdk_contract_repository =
                    RepositoryId::new("github.com/dagger/other-sdk-sdk").unwrap();
            }
            Self::HarnessRevision => {
                case.observation.sdk_contract_revision =
                    alternate_commit(&case.target.sdk_contract_revision);
            }
            Self::HarnessCliVersion => {
                case.observation.harness_cli_version =
                    bump_dagger_version(&case.target.sdk_contract_cli_version);
            }
            Self::HarnessEngineVersion => {
                case.observation.harness_engine_version =
                    bump_dagger_version(&case.target.sdk_contract_cli_version);
            }
            Self::RustSdkVersion => {
                case.observation.rust_sdk_version = bump_semver(&case.target.rust_sdk_version);
            }
            Self::RustEdition => {
                case.observation.rust_edition = RustEdition::Edition2021;
            }
            Self::RustVersion => {
                case.observation.rust_version = bump_semver(&case.target.rust_version);
            }
            Self::AuthoritySourceDigest => {
                case.observation.source_digest_mismatches =
                    CanonicalSet::new([AuthorityId::new("go-client").unwrap()]);
            }
        }
    }
}

pub fn target_case_strategy() -> BoxedStrategy<TargetCase> {
    equivalent_contract_cases_strategy()
        .prop_map(|cases| {
            let target = cases.forward.target;
            let go_version_label_resolution =
                target
                    .go_sdk_version_label
                    .clone()
                    .map(|label| GoVersionLabelResolution {
                        label,
                        ref_object: alternate_commit(&target.go_sdk_revision),
                        peeled_commit: target.go_sdk_revision.clone(),
                    });
            let observation = TargetObservation {
                contract_format_version: target.contract_format_version.clone(),
                dagger_repository: target.dagger_repository.clone(),
                dagger_revision: target.dagger_revision.clone(),
                engine_version: target.engine_version.clone(),
                schema_version: target.schema_version.clone(),
                schema_digest: target.schema_digest.clone(),
                go_sdk_repository: target.go_sdk_repository.clone(),
                engine_selected_go_revision: target.go_sdk_revision.clone(),
                go_version_label_resolution,
                sdk_contract_repository: target.sdk_contract_repository.clone(),
                sdk_contract_revision: target.sdk_contract_revision.clone(),
                harness_cli_version: target.sdk_contract_cli_version.clone(),
                harness_engine_version: target.sdk_contract_cli_version.clone(),
                rust_sdk_version: target.rust_sdk_version.clone(),
                rust_edition: target.rust_edition.clone(),
                rust_version: target.rust_version.clone(),
                source_digest_mismatches: CanonicalSet::default(),
            };
            TargetCase {
                target,
                observation,
            }
        })
        .boxed()
}

pub fn single_target_mutation_strategy() -> BoxedStrategy<TargetMutation> {
    proptest::sample::select(TargetMutation::ALL.to_vec()).boxed()
}

pub fn target_mutation_set_strategy() -> BoxedStrategy<BTreeSet<TargetMutation>> {
    btree_set(
        single_target_mutation_strategy(),
        0..TargetMutation::ALL.len() + 1,
    )
    .boxed()
}

pub fn apply_target_mutations(
    case: &mut TargetCase,
    mutations: &BTreeSet<TargetMutation>,
) -> Vec<DiagnosticCode> {
    let mut expected = Vec::with_capacity(mutations.len());
    for mutation in mutations {
        mutation.apply(case);
        expected.push(mutation.expected_code());
    }
    expected.sort_unstable();
    expected
}

fn bump_semver(version: &SemverVersion) -> SemverVersion {
    let mut version = version.version().clone();
    version.patch += 1;
    SemverVersion::new(version.to_string()).unwrap()
}

fn bump_dagger_version(version: &DaggerVersion) -> DaggerVersion {
    let mut version = version.version().clone();
    version.patch += 1;
    DaggerVersion::new(version.to_string()).unwrap()
}
