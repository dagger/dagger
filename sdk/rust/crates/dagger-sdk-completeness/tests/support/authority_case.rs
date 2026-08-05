//! Valid-first authority registries, source bundles, and named containment mutations.
//!
//! Every generated case starts with all seven authority classes bound to the target repositories
//! and revisions. Mutations alter one reviewable condition at a time and compose without needing
//! malformed scalar values.

use std::collections::{BTreeMap, BTreeSet};

use dagger_sdk_completeness::*;
use proptest::collection::btree_set;
use proptest::prelude::*;
use serde::Deserialize;

use super::mutations::{TargetCase, target_case_strategy};
use super::scalars::alternate_commit;

#[derive(Clone, Debug)]
pub struct AuthorityCase {
    pub target: TargetCase,
    pub registry: AuthorityRegistry,
    pub bundles: AuthoritySourceBundles,
}

impl AuthorityCase {
    pub fn validated_target(&self) -> ValidatedTargetDescriptor {
        validate_target(self.target.target.clone(), &self.target.observation).unwrap()
    }

    pub fn validate(self) -> Result<ValidatedAuthoritySources, DiagnosticSet> {
        let target = self.validated_target();
        let registry = validate_authority_registry(&target, self.registry)?;
        validate_authority_sources(registry, self.bundles)
    }
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, Hash, Ord, PartialEq, PartialOrd)]
#[serde(rename_all = "kebab-case")]
pub enum AuthorityMutation {
    RemoveAuthorityClass,
    DuplicateAuthorityClass,
    AuthorityIdMismatch,
    RepositoryMismatch,
    RevisionMismatch,
    HarnessRevisionMismatch,
    EmptyInclude,
    ExclusionOutsideInclude,
    MissingBundle,
    MissingIncludedFile,
    StaleExclusionPath,
    SourceDigestMismatch,
    ExtraBundle,
}

impl AuthorityMutation {
    pub const ALL: [Self; 13] = [
        Self::RemoveAuthorityClass,
        Self::DuplicateAuthorityClass,
        Self::AuthorityIdMismatch,
        Self::RepositoryMismatch,
        Self::RevisionMismatch,
        Self::HarnessRevisionMismatch,
        Self::EmptyInclude,
        Self::ExclusionOutsideInclude,
        Self::MissingBundle,
        Self::MissingIncludedFile,
        Self::StaleExclusionPath,
        Self::SourceDigestMismatch,
        Self::ExtraBundle,
    ];

    pub const fn expected_code(self) -> DiagnosticCode {
        match self {
            Self::RemoveAuthorityClass | Self::DuplicateAuthorityClass => {
                DiagnosticCode::AuthorityClassInvalid
            }
            Self::AuthorityIdMismatch | Self::ExtraBundle => DiagnosticCode::AuthorityDuplicate,
            Self::RepositoryMismatch => DiagnosticCode::AuthorityRepositoryInvalid,
            Self::RevisionMismatch => DiagnosticCode::AuthorityRevisionMismatch,
            Self::HarnessRevisionMismatch => DiagnosticCode::SdkContractRevisionMismatch,
            Self::EmptyInclude | Self::MissingBundle | Self::MissingIncludedFile => {
                DiagnosticCode::AuthoritySourceEmpty
            }
            Self::ExclusionOutsideInclude | Self::StaleExclusionPath => {
                DiagnosticCode::AuthorityExclusionInvalid
            }
            Self::SourceDigestMismatch => DiagnosticCode::AuthorityDrift,
        }
    }

    fn apply(self, case: &mut AuthorityCase) {
        let engine_id = authority_id(AuthorityClass::EngineSchema);
        match self {
            Self::RemoveAuthorityClass => {
                case.registry
                    .authorities
                    .remove(&authority_id(AuthorityClass::RustPolicy));
            }
            Self::DuplicateAuthorityClass => {
                authority_mut(case, AuthorityClass::GoClient).authority_class =
                    AuthorityClass::EngineSchema;
            }
            Self::AuthorityIdMismatch => {
                let source = case.registry.authorities.remove(&engine_id).unwrap();
                case.registry
                    .authorities
                    .insert(AuthorityId::new("wrong-map-key").unwrap(), source);
            }
            Self::RepositoryMismatch => {
                authority_mut(case, AuthorityClass::EngineSchema).repository =
                    RepositoryId::new("github.com/dagger/not-dagger").unwrap();
            }
            Self::RevisionMismatch => {
                let source = authority_mut(case, AuthorityClass::EngineSchema);
                source.revision = alternate_commit(&source.revision);
            }
            Self::HarnessRevisionMismatch => {
                let source = authority_mut(case, AuthorityClass::SdkContractHarness);
                source.revision = alternate_commit(&source.revision);
            }
            Self::EmptyInclude => {
                let source = authority_mut(case, AuthorityClass::EngineSchema);
                source.include = CanonicalSet::default();
                source.exclude = CanonicalSet::default();
            }
            Self::ExclusionOutsideInclude => {
                let source = authority_mut(case, AuthorityClass::EngineSchema);
                source.exclude = CanonicalSet::new([SourceExclusion {
                    selector: SourceSelector::Path(PathSourceSelector {
                        path: RepositoryRelativePath::new("elsewhere/not-selected.rs").unwrap(),
                    }),
                    rationale: NonEmptyText::new("not-part-of-the-contract").unwrap(),
                }]);
            }
            Self::MissingBundle => {
                let mut bundles = case.bundles.bundles().clone();
                bundles.remove(&engine_id);
                case.bundles = AuthoritySourceBundles::new(bundles);
            }
            Self::MissingIncludedFile => {
                let mut bundles = case.bundles.bundles().clone();
                bundles.insert(engine_id, SourceBundle::default());
                case.bundles = AuthoritySourceBundles::new(bundles);
            }
            Self::StaleExclusionPath => {
                let source = authority_mut(case, AuthorityClass::EngineSchema);
                let Some(include_path) =
                    source
                        .include
                        .as_slice()
                        .first()
                        .map(|selector| match selector {
                            SourceSelector::Path(selector) => selector.path.clone(),
                            SourceSelector::Symbol(selector) => selector.path.clone(),
                        })
                else {
                    return;
                };
                source.exclude = CanonicalSet::new([SourceExclusion {
                    selector: SourceSelector::Path(PathSourceSelector {
                        path: RepositoryRelativePath::new(format!("{include_path}/not-present.rs"))
                            .unwrap(),
                    }),
                    rationale: NonEmptyText::new("generated-binding").unwrap(),
                }]);
            }
            Self::SourceDigestMismatch => {
                authority_mut(case, AuthorityClass::EngineSchema).source_digest =
                    Digest::sha256("unreviewed-source-movement");
            }
            Self::ExtraBundle => {
                let mut bundles = case.bundles.bundles().clone();
                bundles.insert(
                    AuthorityId::new("extra-authority").unwrap(),
                    SourceBundle::new([(
                        RepositoryRelativePath::new("extra/source.rs").unwrap(),
                        b"extra".to_vec(),
                    )]),
                );
                case.bundles = AuthoritySourceBundles::new(bundles);
            }
        }
    }
}

pub fn authority_case_strategy() -> BoxedStrategy<AuthorityCase> {
    (
        target_case_strategy(),
        any::<u64>(),
        1_usize..5,
        any::<bool>(),
    )
        .prop_map(|(target, salt, file_count, overlapping_symbol)| {
            build_authority_case(target, salt, file_count, overlapping_symbol)
        })
        .boxed()
}

pub fn authority_mutation_set_strategy() -> BoxedStrategy<BTreeSet<AuthorityMutation>> {
    btree_set(
        proptest::sample::select(AuthorityMutation::ALL.to_vec()),
        0..AuthorityMutation::ALL.len() + 1,
    )
    .boxed()
}

pub fn apply_authority_mutations(
    case: &mut AuthorityCase,
    mutations: &BTreeSet<AuthorityMutation>,
) {
    for mutation in mutations {
        mutation.apply(case);
    }
}

fn build_authority_case(
    target: TargetCase,
    salt: u64,
    file_count: usize,
    overlapping_symbol: bool,
) -> AuthorityCase {
    let mut authorities = BTreeMap::new();
    let mut bundles = BTreeMap::new();

    for authority_class in [
        AuthorityClass::EngineSchema,
        AuthorityClass::GoClient,
        AuthorityClass::GoEngineSdk,
        AuthorityClass::GoCodegen,
        AuthorityClass::GoIntegrationTests,
        AuthorityClass::SdkContractHarness,
        AuthorityClass::RustPolicy,
    ] {
        let authority_id = authority_id(authority_class.clone());
        let directory = RepositoryRelativePath::new(format!(
            "authority/{}/case-{salt:016x}",
            authority_id.as_str()
        ))
        .unwrap();
        let files = (0..file_count)
            .map(|index| {
                (
                    RepositoryRelativePath::new(format!("{directory}/source-{index}.rs")).unwrap(),
                    format!("{}:{salt:016x}:{index}\n", authority_id.as_str()).into_bytes(),
                )
            })
            .collect::<Vec<_>>();
        let first_file = files[0].0.clone();
        let bundle = SourceBundle::new(files);
        let (repository, revision) = authority_identity(&target.target, &authority_class);
        let mut source = AuthoritySource {
            authority_id: authority_id.clone(),
            authority_class: authority_class.clone(),
            repository,
            revision,
            include: CanonicalSet::new(
                [SourceSelector::Path(PathSourceSelector { path: directory })]
                    .into_iter()
                    .chain(overlapping_symbol.then(|| {
                        SourceSelector::Symbol(SymbolSourceSelector {
                            path: first_file.clone(),
                            locator: SourceLocator::new("PrimarySymbol").unwrap(),
                        })
                    })),
            ),
            exclude: CanonicalSet::default(),
            extractor: ExtractorIdentity {
                extractor_id: ExtractorId::new(format!("extractor/{}", authority_id.as_str()))
                    .unwrap(),
                version: SemverVersion::new("1.0.0").unwrap(),
            },
            source_digest: Digest::sha256("placeholder"),
        };
        if authority_class == AuthorityClass::GoClient {
            source.exclude = CanonicalSet::new([SourceExclusion {
                selector: SourceSelector::Symbol(SymbolSourceSelector {
                    path: first_file,
                    locator: SourceLocator::new("GeneratedBinding").unwrap(),
                }),
                rationale: NonEmptyText::new("represented-by-engine-schema").unwrap(),
            }]);
        }
        source.source_digest = recompute_source_digest(&source, &bundle).unwrap();
        authorities.insert(authority_id.clone(), source);
        bundles.insert(authority_id, bundle);
    }

    AuthorityCase {
        target,
        registry: AuthorityRegistry { authorities },
        bundles: AuthoritySourceBundles::new(bundles),
    }
}

fn authority_mut(
    case: &mut AuthorityCase,
    authority_class: AuthorityClass,
) -> &mut AuthoritySource {
    let expected_id = authority_id(authority_class);
    case.registry
        .authorities
        .values_mut()
        .find(|source| source.authority_id == expected_id)
        .unwrap()
}

pub fn authority_id(authority_class: AuthorityClass) -> AuthorityId {
    AuthorityId::new(match authority_class {
        AuthorityClass::EngineSchema => "engine-schema",
        AuthorityClass::GoClient => "go-client",
        AuthorityClass::GoEngineSdk => "go-engine-sdk",
        AuthorityClass::GoCodegen => "go-codegen",
        AuthorityClass::GoIntegrationTests => "go-integration-tests",
        AuthorityClass::SdkContractHarness => "sdk-contract-harness",
        AuthorityClass::RustPolicy => "rust-policy",
    })
    .unwrap()
}

fn authority_identity(
    target: &TargetDescriptor,
    authority_class: &AuthorityClass,
) -> (RepositoryId, CommitSha) {
    match authority_class {
        AuthorityClass::GoClient => (
            target.go_sdk_repository.clone(),
            target.go_sdk_revision.clone(),
        ),
        AuthorityClass::SdkContractHarness => (
            target.sdk_contract_repository.clone(),
            target.sdk_contract_revision.clone(),
        ),
        AuthorityClass::EngineSchema
        | AuthorityClass::GoEngineSdk
        | AuthorityClass::GoCodegen
        | AuthorityClass::GoIntegrationTests
        | AuthorityClass::RustPolicy => (
            target.dagger_repository.clone(),
            target.dagger_revision.clone(),
        ),
    }
}
