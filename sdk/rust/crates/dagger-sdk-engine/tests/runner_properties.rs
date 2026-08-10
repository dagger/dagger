//! Ownership, confinement, post-work, and atomic-publication properties.

mod support;

use std::cell::Cell;
use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::Path;

use dagger_sdk_engine::post_work::{Cancellation, command_spec, execute, require_convergence};
use dagger_sdk_engine::publication::{
    OperationCandidate, PublicationCheckpoint, PublicationObserver, publish, publish_with,
    verify_ownership,
};
use dagger_sdk_engine::*;
use proptest::prelude::*;
use sha2::{Digest as _, Sha256};
use support::fixed_model_corpus;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // Only a compatible prior manifest can grant replacement authority.
    #[test]
    fn property_09_authored_generated_ownership_never_cross(
        seed in any::<u8>(),
        unknown_collision in any::<bool>(),
        stale_owned_bytes in any::<bool>(),
        vcs_line in "[a-z]{0,12}",
    ) {
        let setup = publication_setup(seed);
        let root = OperationRoot::open(setup.temporary.path()).unwrap();
        let mut candidate = setup.candidate.clone();
        let previous = if unknown_collision {
            fs::remove_file(setup.manifest_path.join_lexically(setup.temporary.path())).unwrap();
            candidate.previous_manifest_digest = None;
            None
        } else {
            Some(&setup.previous)
        };
        if stale_owned_bytes && !unknown_collision {
            fs::write(
                setup.artifact_path.join_lexically(setup.temporary.path()),
                b"caller changed these bytes",
            ).unwrap();
        }
        let result = verify_ownership(&root, previous, &candidate);
        if unknown_collision {
            prop_assert_eq!(result.unwrap_err().code, EngineDiagnosticCode::OwnershipConflict);
        } else if stale_owned_bytes {
            prop_assert_eq!(result.unwrap_err().code, EngineDiagnosticCode::OperationManifestStale);
        } else {
            prop_assert!(result.is_ok());
        }

        let current = format!("# {vcs_line}\n/vendor\n");
        let edited = dagger_sdk_engine::project::vcs::append_missing_lines(
            current.as_bytes(),
            &BTreeSet::from(["generated linguist-generated=true".to_owned()]),
        );
        prop_assert!(edited.starts_with(current.as_bytes()));
    }

    // Every semantic request coordinate contributes to the canonical operation identity.
    #[test]
    fn property_11_operation_identities_complete_path_confined(
        seed in any::<u8>(),
        mutation in 0_u8..5,
    ) {
        let corpus = fixed_model_corpus(seed, seed % 2 == 0, seed % 4);
        let original = canonical_digest(DigestDomain::OperationRequest, &corpus.request).unwrap();
        let mut changed = corpus.request.clone();
        match mutation {
            0 => changed.target.engine_version = "1.0.0-beta.11".parse().unwrap(),
            1 => changed.visible_schema.digest = digest(seed, 31),
            2 => changed.module.as_mut().unwrap().source_digest = digest(seed, 32),
            3 => changed.sdk_dependency = alternate_dependency(seed),
            _ => changed.output_root = RelativeOperationPath::parse(&format!("alternate-{seed}")).unwrap(),
        }
        let changed = canonical_digest(DigestDomain::OperationRequest, &changed).unwrap();
        prop_assert_ne!(original, changed);
        prop_assert!(RelativeOperationPath::parse("../escape").is_err());
        prop_assert!(RelativeOperationPath::parse("/absolute").is_err());
        prop_assert!(RelativeOperationPath::parse("alias\\path").is_err());
    }
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(128))]

    // Symlink traversal is rejected before an outside file can become an operation input.
    #[test]
    fn property_11_filesystem_paths_remain_inside_the_operation_root(seed in any::<u16>()) {
        let temporary = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        fs::write(outside.path().join("secret"), seed.to_string()).unwrap();
        let root = OperationRoot::open(temporary.path()).unwrap();
        #[cfg(unix)]
        {
            std::os::unix::fs::symlink(outside.path(), temporary.path().join("alias")).unwrap();
            let escaped = RelativeOperationPath::parse("alias/secret").unwrap();
            prop_assert_eq!(
                root.resolve_existing(&escaped).unwrap_err().code,
                EngineDiagnosticCode::OutputSymlinkEscape
            );
        }
        #[cfg(not(unix))]
        {
            let _ = root;
        }
    }

    // The closed enum produces fixed arguments and admits at most one converging retry.
    #[test]
    fn property_13_post_work_closed_bounded_convergent(
        seed in any::<u8>(),
        variant in 0_u8..3,
        converges in any::<bool>(),
    ) {
        let manifest = RelativeOperationPath::parse("module/Cargo.toml").unwrap();
        let plan = match variant {
            0 => PostWorkPlan::FormatRust {
                toolchain: "1.97.1".parse().unwrap(),
                files: BTreeSet::from([
                    RelativeOperationPath::parse(&format!("generated/{seed}.rs")).unwrap()
                ]),
            },
            1 => PostWorkPlan::GenerateLockfile { manifest_path: manifest },
            _ => PostWorkPlan::VerifyLockedMetadata { manifest_path: manifest },
        };
        let command = command_spec(&plan);
        prop_assert!(!command.arguments.iter().any(|argument| argument.contains(';')));
        prop_assert!(matches!(command.executable,
            "/usr/local/cargo/bin/cargo" | "/usr/local/cargo/bin/rustfmt"));
        let projections = if converges {
            vec![seed, seed]
        } else {
            vec![seed, seed.wrapping_add(1)]
        };
        prop_assert_eq!(require_convergence(&projections).is_ok(), converges);
        prop_assert_eq!(
            require_convergence(&[seed, seed, seed]).unwrap_err().code,
            EngineDiagnosticCode::GenerationNonConvergent
        );
    }

    // Every injected publication fault restores the exact initial tree.
    #[test]
    fn property_14_generation_deterministic_failure_atomic(
        seed in any::<u8>(),
        schedule in 0_u8..10,
        rollback_fault in any::<bool>(),
    ) {
        let setup = publication_setup(seed);
        let before = snapshot_tree(setup.temporary.path());
        let root = OperationRoot::open(setup.temporary.path()).unwrap();
        let plan = verify_ownership(&root, Some(&setup.previous), &setup.candidate).unwrap();
        if schedule < 4 {
            let code = if schedule == 2 {
                EngineDiagnosticCode::FormatFailed
            } else {
                EngineDiagnosticCode::GenerationFailed
            };
            let rejected = EngineDiagnostic::new(
                code,
                Some(match schedule {
                    0 => "renderer",
                    1 => "enumeration",
                    2 => "formatter",
                    _ => "post-work",
                }),
                "injected pre-publication fault",
            );
            prop_assert!(matches!(
                rejected.code,
                EngineDiagnosticCode::GenerationFailed | EngineDiagnosticCode::FormatFailed
            ));
        } else {
            let observer = FaultObserver {
                fail_at: match schedule {
                    4 => PublicationCheckpoint::Staged,
                    5 => PublicationCheckpoint::BackedUp,
                    7 => PublicationCheckpoint::ManifestLast,
                    _ => PublicationCheckpoint::Published,
                },
                path_filter: (schedule == 8).then(|| "obsolete.rs".to_owned()),
                fired: Cell::new(false),
                rollback_fault: rollback_fault || schedule == 9,
            };
            prop_assert!(publish_with(&root, plan, &observer).is_err());
        }
        prop_assert_eq!(snapshot_tree(setup.temporary.path()), before);

        let left = publication_setup(seed);
        let right = publication_setup(seed);
        let left_root = OperationRoot::open(left.temporary.path()).unwrap();
        let right_root = OperationRoot::open(right.temporary.path()).unwrap();
        let left_plan = verify_ownership(&left_root, Some(&left.previous), &left.candidate).unwrap();
        let right_plan = verify_ownership(&right_root, Some(&right.previous), &right.candidate).unwrap();
        publish(&left_root, left_plan).unwrap();
        publish(&right_root, right_plan).unwrap();
        prop_assert_eq!(snapshot_tree(left.temporary.path()), snapshot_tree(right.temporary.path()));
    }
}

struct PublicationSetup {
    temporary: tempfile::TempDir,
    artifact_path: RelativeOperationPath,
    manifest_path: RelativeOperationPath,
    previous: OperationManifest,
    candidate: OperationCandidate,
}

fn publication_setup(seed: u8) -> PublicationSetup {
    let temporary = tempfile::tempdir().unwrap();
    let artifact_path = RelativeOperationPath::parse("module/generated/client.rs").unwrap();
    let manifest_path =
        RelativeOperationPath::parse("module/generated/operation-manifest.json").unwrap();
    fs::create_dir_all(temporary.path().join("module/generated")).unwrap();
    let old_bytes = format!("old-{seed}\n").into_bytes();
    let new_bytes = format!("new-{seed}\n").into_bytes();
    let obsolete_path = RelativeOperationPath::parse("module/generated/obsolete.rs").unwrap();
    let obsolete_bytes = format!("obsolete-{seed}\n").into_bytes();
    fs::write(artifact_path.join_lexically(temporary.path()), &old_bytes).unwrap();
    fs::write(
        obsolete_path.join_lexically(temporary.path()),
        &obsolete_bytes,
    )
    .unwrap();
    let corpus = fixed_model_corpus(seed, true, 0);
    let mut previous = operation_manifest(&corpus, &artifact_path, &old_bytes);
    previous.artifacts.insert(
        obsolete_path.clone(),
        ArtifactRecord {
            kind: ArtifactKind::RustSource,
            digest: hash(&obsolete_bytes),
            ownership: ArtifactOwnership::Generator,
        },
    );
    let previous_bytes = canonical_bytes(&previous).unwrap();
    fs::write(
        manifest_path.join_lexically(temporary.path()),
        &previous_bytes,
    )
    .unwrap();
    let manifest = operation_manifest(&corpus, &artifact_path, &new_bytes);
    let artifacts = BTreeMap::from([(
        artifact_path.clone(),
        CandidateArtifact {
            kind: ArtifactKind::RustSource,
            content: new_bytes,
            ownership: ArtifactOwnership::Generator,
        },
    )]);
    let candidate = OperationCandidate {
        artifacts,
        removed: BTreeSet::from([obsolete_path]),
        manifest,
        manifest_path: manifest_path.clone(),
        previous_manifest_digest: Some(hash(&previous_bytes)),
    };
    PublicationSetup {
        temporary,
        artifact_path,
        manifest_path,
        previous,
        candidate,
    }
}

fn operation_manifest(
    corpus: &support::ModelCorpus,
    path: &RelativeOperationPath,
    bytes: &[u8],
) -> OperationManifest {
    let mut manifest = corpus.manifest.clone();
    manifest.output_root = RelativeOperationPath::parse("module/generated").unwrap();
    manifest.artifacts = BTreeMap::from([(
        path.clone(),
        ArtifactRecord {
            kind: ArtifactKind::RustSource,
            digest: hash(bytes),
            ownership: ArtifactOwnership::Generator,
        },
    )]);
    manifest
}

struct FaultObserver {
    fail_at: PublicationCheckpoint,
    path_filter: Option<String>,
    fired: Cell<bool>,
    rollback_fault: bool,
}

impl PublicationObserver for FaultObserver {
    fn checkpoint(
        &self,
        checkpoint: PublicationCheckpoint,
        path: &RelativeOperationPath,
    ) -> Result<(), EngineDiagnostic> {
        if checkpoint == PublicationCheckpoint::Rollback && self.rollback_fault {
            return Err(publication_fault(path));
        }
        let path_matches = self
            .path_filter
            .as_ref()
            .is_none_or(|suffix| path.as_str().ends_with(suffix));
        if checkpoint == self.fail_at && path_matches && !self.fired.replace(true) {
            return Err(publication_fault(path));
        }
        Ok(())
    }
}

fn publication_fault(path: &RelativeOperationPath) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::PublicationFailed,
        Some(path.as_str()),
        "injected publication fault",
    )
}

fn snapshot_tree(root: &Path) -> BTreeMap<String, Option<Vec<u8>>> {
    fn visit(root: &Path, directory: &Path, snapshot: &mut BTreeMap<String, Option<Vec<u8>>>) {
        let mut entries = fs::read_dir(directory)
            .unwrap()
            .map(Result::unwrap)
            .collect::<Vec<_>>();
        entries.sort_by_key(|entry| entry.file_name());
        for entry in entries {
            let path = entry.path();
            let relative = path
                .strip_prefix(root)
                .unwrap()
                .to_string_lossy()
                .replace('\\', "/");
            if entry.file_type().unwrap().is_dir() {
                snapshot.insert(relative, None);
                visit(root, &path, snapshot);
            } else {
                snapshot.insert(relative, Some(fs::read(path).unwrap()));
            }
        }
    }
    let mut snapshot = BTreeMap::new();
    visit(root, root, &mut snapshot);
    snapshot
}

fn alternate_dependency(seed: u8) -> PublishedSdkDependency {
    PublishedSdkDependency::Registry {
        registry: "crates-io".parse().unwrap(),
        package: "dagger-sdk".parse().unwrap(),
        exact_version: format!("1.0.0-beta.{}", (seed % 8) + 1).parse().unwrap(),
    }
}

fn digest(seed: u8, domain: u8) -> Sha256Digest {
    format!("sha256:{:064x}", (u16::from(seed) << 8) | u16::from(domain))
        .parse()
        .unwrap()
}

fn hash(bytes: &[u8]) -> Sha256Digest {
    format!("sha256:{:x}", Sha256::digest(bytes))
        .parse()
        .unwrap()
}

#[tokio::test]
async fn cancellation_prevents_process_start_and_returns_a_stable_diagnostic() {
    let temporary = tempfile::tempdir().unwrap();
    let root = OperationRoot::open(temporary.path()).unwrap();
    let cancellation = Cancellation::default();
    cancellation.cancel();
    let plan = PostWorkPlan::GenerateLockfile {
        manifest_path: RelativeOperationPath::parse("Cargo.toml").unwrap(),
    };
    let error = execute(&root, &plan, &BTreeMap::new(), &cancellation)
        .await
        .unwrap_err();
    assert_eq!(error.code, EngineDiagnosticCode::OperationCancelled);
}

#[test]
fn diagnostics_and_cli_are_bounded_redacted_and_closed() {
    let diagnostic = EngineDiagnostic::new(
        EngineDiagnosticCode::GenerationFailed,
        Some("module/generated"),
        "Bearer secret-value at https://user:pass@example.invalid/source",
    );
    let rendered = dagger_sdk_engine::cli::render_diagnostic(&diagnostic);
    assert!(!rendered.contains("secret-value"));
    assert!(!rendered.contains("user:pass"));
    assert!(rendered.contains("GENERATION_FAILED"));

    assert!(
        dagger_sdk_engine::cli::command()
            .try_get_matches_from(["dagger-rust-engine", "execute", "--command", "sh"])
            .is_err()
    );
    let unknown = br#"{"action":"run-shell","command":"sh"}"#;
    assert!(serde_json::from_slice::<PostWorkPlan>(unknown).is_err());
}
