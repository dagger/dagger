//! Ownership, concurrency, formatting, publication, and diagnostic properties.

mod support;

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Barrier, OnceLock};

use dagger_bootstrap::generate::format::{
    CandidateFormatter, FormattedArtifact, FormattedArtifactSet, PinnedRustfmt,
};
use dagger_bootstrap::generate::publish::{
    ArtifactChange, ArtifactChangeKind, ArtifactManifest, NoopPublicationObserver,
    PublicationCheckpoint, PublicationInterruption, PublicationObserver, Publisher, compare,
    drift_diagnostics,
};
use dagger_bootstrap::generate::{
    ArtifactPath, BINDING_MANIFEST, GenerateMode, GenerateOverrides, execute_with,
};
use dagger_codegen::RenderedCandidate;
use dagger_codegen::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use dagger_codegen::target::CodegenTarget;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

use support::{Fixture, formatted, record_files, source, target, write};

const ALPHA: &str = "crates/dagger-sdk/src/gen/alpha.rs";
const BETA: &str = "crates/dagger-sdk/src/gen/beta.rs";

fn property_config(cases: u32, name: &str) -> Config {
    let path = format!(
        "{}/proptest-regressions/{name}.txt",
        env!("CARGO_MANIFEST_DIR")
    );
    let path: &'static str = Box::leak(path.into_boxed_str());
    Config {
        cases,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(path))),
        ..Config::default()
    }
}

fn selected_set(
    target: &CodegenTarget,
    alpha: bool,
    beta: bool,
    value: usize,
) -> FormattedArtifactSet {
    let mut files = Vec::new();
    if alpha {
        files.push((ALPHA, source(target, "ALPHA", value, false)));
    }
    if beta {
        files.push((BETA, source(target, "BETA", value, false)));
    }
    formatted(target, files)
}

fn write_current(
    fixture: &Fixture,
    target: &CodegenTarget,
    path: &'static str,
    state: u8,
    candidate_value: usize,
) {
    let bytes = match state {
        0 => return,
        1 => source(
            target,
            if path == ALPHA { "ALPHA" } else { "BETA" },
            candidate_value,
            false,
        ),
        _ => source(
            target,
            if path == ALPHA { "ALPHA" } else { "BETA" },
            candidate_value + 1,
            false,
        ),
    };
    write(&fixture.workspace.join(path), &bytes);
}

fn expected_change(
    path: &'static str,
    candidate: bool,
    prior: bool,
    state: u8,
) -> Option<ArtifactChange> {
    let path = ArtifactPath::new(path).expect("static path must be valid");
    match (candidate, prior, state) {
        (true, _, 0) => Some(ArtifactChange::new(path, ArtifactChangeKind::Added)),
        (true, _, 2..) => Some(ArtifactChange::new(path, ArtifactChangeKind::Changed)),
        (false, true, 1..) => Some(ArtifactChange::new(path, ArtifactChangeKind::Removed)),
        _ => None,
    }
}

proptest! {
    #![proptest_config(property_config(256, "generation-ownership"))]

    // Feature: rust-sdk-core-codegen, Property 23: Provenance and output ownership are exhaustive
    #[test]
    fn property_23_provenance_output_ownership_exhaustive(
        candidate_alpha in any::<bool>(),
        candidate_beta in any::<bool>(),
        prior_alpha in any::<bool>(),
        prior_beta in any::<bool>(),
        alpha_state in 0_u8..3,
        beta_state in 0_u8..3,
        legacy in any::<bool>(),
        fault in 0_u8..7,
    ) {
        let fixture = Fixture::new();
        let target = target();
        let candidate = selected_set(&target, candidate_alpha, candidate_beta, 7);
        let prior_set = selected_set(&target, prior_alpha, prior_beta, 3);
        let prior = ArtifactManifest::from_artifacts(&target, &prior_set);
        let candidate_manifest = ArtifactManifest::from_artifacts(&target, &candidate);
        let manifest_bytes = candidate_manifest.encode().expect("candidate manifest must encode");
        write(&fixture.workspace.join(BINDING_MANIFEST), &manifest_bytes);

        if candidate_alpha || prior_alpha {
            write_current(&fixture, &target, ALPHA, alpha_state, 7);
        }
        if candidate_beta || prior_beta {
            write_current(&fixture, &target, BETA, beta_state, 7);
        }
        if legacy {
            write(
                &fixture.workspace.join("crates/dagger-sdk/src/gen.rs"),
                b"// legacy\n",
            );
        }

        let manifest_path = ArtifactPath::new(BINDING_MANIFEST).expect("manifest path must be valid");
        match fault {
            1 => write(
                &fixture.workspace.join("crates/dagger-sdk/src/gen/unknown.rs"),
                b"// undeclared\n",
            ),
            #[cfg(unix)]
            2 => {
                use std::os::unix::fs::symlink;
                let root = fixture.workspace.join("crates/dagger-sdk/src/gen");
                fs::create_dir_all(&root).expect("generated root must exist");
                symlink("outside", root.join("linked.rs")).expect("fixture symlink must be created");
            }
            3 => {
                prop_assert!(ArtifactPath::new("crates/dagger-sdk/src/gen/../escape.rs").is_err());
            }
            4 => {
                let invalid = source(&target, "BAD", 1, false)
                    .into_iter()
                    .map(|byte| if byte == b'd' { b'x' } else { byte })
                    .collect::<Vec<_>>();
                let bad_path = ArtifactPath::new(ALPHA).expect("static path must be valid");
                prop_assert!(FormattedArtifact::from_bytes(&bad_path, invalid, &target).is_err());
            }
            5 => {
                let mut drifted = serde_json::from_slice::<serde_json::Value>(&manifest_bytes)
                    .expect("candidate manifest JSON must decode");
                drifted["target_revision"] = serde_json::Value::String("drifted-target".to_owned());
                let drifted = ArtifactManifest::decode(
                    &serde_json::to_vec(&drifted).expect("drifted manifest must encode"),
                )
                .expect("drifted manifest remains structurally valid");
                prop_assert!(drifted.validate_target(&target).is_err());
            }
            6 => {
                let owned = selected_set(&target, true, false, 1);
                let prior = ArtifactManifest::from_artifacts(&target, &owned);
                let mut malformed = serde_json::from_slice::<serde_json::Value>(
                    &prior.encode().expect("prior manifest must encode"),
                )
                .expect("prior manifest JSON must decode");
                let record = malformed["artifacts"]
                    .as_object_mut()
                    .and_then(|artifacts| artifacts.values_mut().next())
                    .expect("owned fixture must contain an artifact record");
                record["sha256"] = serde_json::Value::String("sha256:not-a-digest".to_owned());
                let malformed = ArtifactManifest::decode(
                    &serde_json::to_vec(&malformed).expect("malformed manifest must encode"),
                )
                .expect("malformed digest remains structurally decodable");
                prop_assert!(malformed.validate_target(&target).is_err());
            }
            _ => {}
        }

        let result = compare(
            &fixture.workspace,
            &candidate,
            &prior,
            &manifest_path,
            &manifest_bytes,
        );
        if matches!(fault, 1 | 2) {
            prop_assert!(result.is_err());
        } else {
            let mut expected = [
                expected_change(ALPHA, candidate_alpha, prior_alpha, alpha_state),
                expected_change(BETA, candidate_beta, prior_beta, beta_state),
                legacy.then(|| ArtifactChange::new(
                    ArtifactPath::new("crates/dagger-sdk/src/gen.rs").expect("legacy path must be valid"),
                    ArtifactChangeKind::Removed,
                )),
            ]
            .into_iter()
            .flatten()
            .collect::<Vec<_>>();
            expected.sort();
            prop_assert_eq!(result.expect("valid ownership must compare"), expected);
        }
    }
}

proptest! {
    #![proptest_config(property_config(128, "generation-check-purity"))]

    // Feature: rust-sdk-core-codegen, Property 24: Verification is pure, complete, and concurrency-safe
    #[test]
    fn property_24_verification_pure_complete_concurrency_safe(
        current_state in 0_u8..3,
        workers in 2_usize..6,
    ) {
        let fixture = Fixture::new();
        let target = target();
        let candidate = selected_set(&target, true, false, 11);
        let prior = ArtifactManifest::from_artifacts(
            &target,
            &selected_set(&target, true, false, 5),
        );
        let manifest_bytes = ArtifactManifest::from_artifacts(&target, &candidate)
            .encode()
            .expect("candidate manifest must encode");
        write(&fixture.workspace.join(BINDING_MANIFEST), &manifest_bytes);
        write_current(&fixture, &target, ALPHA, current_state, 11);
        let manifest_path = ArtifactPath::new(BINDING_MANIFEST).expect("manifest path must be valid");
        let before = record_files(&fixture.workspace);
        let expected = compare(
            &fixture.workspace,
            &candidate,
            &prior,
            &manifest_path,
            &manifest_bytes,
        );

        let barrier = Barrier::new(workers);
        let results = std::thread::scope(|scope| {
            let handles = (0..workers)
                .map(|_| {
                    scope.spawn(|| {
                        barrier.wait();
                        compare(
                            &fixture.workspace,
                            &candidate,
                            &prior,
                            &manifest_path,
                            &manifest_bytes,
                        )
                    })
                })
                .collect::<Vec<_>>();
            handles
                .into_iter()
                .map(|handle| handle.join().expect("check worker must not panic"))
                .collect::<Vec<_>>()
        });
        prop_assert!(results.iter().all(|result| result == &expected));
        prop_assert_eq!(record_files(&fixture.workspace), before);
        if let Ok(changes) = expected
            && !changes.is_empty()
        {
            let diagnostics = drift_diagnostics(&changes);
            prop_assert_eq!(diagnostics.diagnostics().len(), changes.len());
            prop_assert!(diagnostics
                .diagnostics()
                .windows(2)
                .all(|pair| pair[0] <= pair[1]));
        }
    }
}

struct FailureSchedule {
    primary: PublicationCheckpoint,
    occurrence: usize,
    seen: AtomicUsize,
    interrupt_rollback: bool,
}

impl PublicationObserver for FailureSchedule {
    fn checkpoint(
        &self,
        checkpoint: PublicationCheckpoint,
        _path: Option<&ArtifactPath>,
    ) -> Result<(), PublicationInterruption> {
        if checkpoint == PublicationCheckpoint::Rollback && self.interrupt_rollback {
            return Err(PublicationInterruption);
        }
        if checkpoint == self.primary && self.seen.fetch_add(1, Ordering::SeqCst) == self.occurrence
        {
            return Err(PublicationInterruption);
        }
        Ok(())
    }
}

proptest! {
    #![proptest_config(property_config(128, "generation-publication"))]

    // Feature: rust-sdk-core-codegen, Property 25: Publication is atomic and failure-preserving
    #[test]
    fn property_25_publication_atomic_failure_preserving(
        phase in 0_u8..7,
        occurrence_seed in any::<usize>(),
        interrupt_rollback in any::<bool>(),
    ) {
        let fixture = Fixture::new();
        let target = target();
        let prior_set = selected_set(&target, true, true, 3);
        let prior = ArtifactManifest::from_artifacts(&target, &prior_set);
        let prior_manifest_bytes = prior.encode().expect("prior manifest must encode");
        write(&fixture.workspace.join(BINDING_MANIFEST), &prior_manifest_bytes);
        write(&fixture.workspace.join(ALPHA), &source(&target, "ALPHA", 3, false));
        write(&fixture.workspace.join(BETA), &source(&target, "BETA", 3, false));
        write(&fixture.workspace.join("sentinel.txt"), b"not owned\n");

        let candidate = selected_set(&target, true, false, 9);
        let manifest_bytes = ArtifactManifest::from_artifacts(&target, &candidate)
            .encode()
            .expect("candidate manifest must encode");
        let manifest_path = ArtifactPath::new(BINDING_MANIFEST).expect("manifest path must be valid");
        let planned = compare(
            &fixture.workspace,
            &candidate,
            &prior,
            &manifest_path,
            &manifest_bytes,
        )
        .expect("publication plan must compare");
        let prior_files = record_files(&fixture.workspace);
        let (primary, available) = match phase {
            0 | 1 => (PublicationCheckpoint::Validate, 1),
            2 => (PublicationCheckpoint::Stage, 2),
            3 => (PublicationCheckpoint::Flush, 2),
            4 => (PublicationCheckpoint::Replace, 2),
            5 => (PublicationCheckpoint::Retire, 1),
            _ => (PublicationCheckpoint::Commit, 1),
        };
        let schedule = FailureSchedule {
            primary,
            occurrence: occurrence_seed % available,
            seen: AtomicUsize::new(0),
            interrupt_rollback: interrupt_rollback
                && matches!(
                    primary,
                    PublicationCheckpoint::Replace
                        | PublicationCheckpoint::Retire
                        | PublicationCheckpoint::Commit
                ),
        };
        if phase == 0 {
            let invalid_path = ArtifactPath::new(ALPHA).expect("static path must be valid");
            let invalid = BTreeMap::from([(invalid_path, b"not generated Rust".to_vec())]);
            prop_assert!(FormattedArtifactSet::from_bytes(&target, invalid, "fixture").is_err());
            prop_assert_eq!(record_files(&fixture.workspace), prior_files);
        } else {
            let failed = Publisher::new(&fixture.workspace, &schedule).publish(
                &candidate,
                &prior,
                &manifest_path,
                &manifest_bytes,
                &planned,
                || Ok(()),
            );
            prop_assert!(failed.is_err());
            if !schedule.interrupt_rollback {
                prop_assert_eq!(record_files(&fixture.workspace), prior_files);
            }
        }

        Publisher::new(&fixture.workspace, &NoopPublicationObserver)
            .publish(
                &candidate,
                &prior,
                &manifest_path,
                &manifest_bytes,
                &planned,
                || Ok(()),
            )
            .expect("a retry must recover stale state and publish the complete candidate");
        prop_assert_eq!(
            fs::read(fixture.workspace.join(ALPHA)).expect("candidate alpha must exist"),
            source(&target, "ALPHA", 9, false),
        );
        prop_assert!(!fixture.workspace.join(BETA).exists());
        prop_assert_eq!(
            fs::read(fixture.workspace.join(BINDING_MANIFEST)).expect("candidate manifest must exist"),
            manifest_bytes,
        );
        prop_assert_eq!(
            fs::read(fixture.workspace.join("sentinel.txt")).expect("sentinel must remain"),
            b"not owned\n",
        );
        prop_assert!(!fixture.workspace.join("target/dagger-codegen-publication.json").exists());
        prop_assert!(!record_files(&fixture.workspace)
            .keys()
            .any(|path| path.contains(".dagger-stage-") || path.contains(".dagger-backup-")));
    }
}

#[derive(Clone)]
struct FormatCase {
    raw: Vec<u8>,
    raw_semantic: String,
    formatted: Vec<u8>,
    formatted_semantic: String,
}

fn format_cases() -> &'static [FormatCase] {
    static CASES: OnceLock<Vec<FormatCase>> = OnceLock::new();
    CASES.get_or_init(|| {
        let fixture = Fixture::new();
        let target = target();
        let mut raw_files = BTreeMap::new();
        let mut inputs = Vec::new();
        for index in 0..256 {
            let path = ArtifactPath::new(format!("crates/dagger-sdk/src/gen/format_{index:03}.rs"))
                .expect("format fixture path must be valid");
            let raw = source(&target, "VALUE", index / 2, index % 2 == 1);
            raw_files.insert(path.clone(), raw.clone());
            inputs.push((path, raw));
        }
        let finalized = PinnedRustfmt
            .finalize_files(&fixture.workspace, &target, raw_files)
            .expect("the pinned formatter must finalize the generated matrix");
        assert_eq!(finalized.formatter(), "rustfmt:1.97.1");
        inputs
            .into_iter()
            .map(|(path, raw)| {
                let raw_artifact = FormattedArtifact::from_bytes(&path, raw.clone(), &target)
                    .expect("pre-format fixture must parse");
                let formatted = finalized
                    .files()
                    .get(&path)
                    .expect("formatted fixture must be complete");
                FormatCase {
                    raw,
                    raw_semantic: raw_artifact.semantic_sha256().to_owned(),
                    formatted: formatted.bytes().to_vec(),
                    formatted_semantic: formatted.semantic_sha256().to_owned(),
                }
            })
            .collect()
    })
}

proptest! {
    #![proptest_config(property_config(256, "generation-formatting"))]

    // Feature: rust-sdk-core-codegen, Property 26: Semantic source and formatting have single owners
    #[test]
    fn property_26_semantic_source_formatting_single_owners(pair_seed in any::<usize>()) {
        let cases = format_cases();
        let pair = pair_seed % 128;
        let compact = &cases[pair * 2];
        let spaced = &cases[pair * 2 + 1];
        let next = &cases[((pair + 1) % 128) * 2];

        prop_assert_ne!(&compact.raw, &spaced.raw);
        prop_assert_eq!(&compact.raw_semantic, &spaced.raw_semantic);
        prop_assert_eq!(&compact.formatted, &spaced.formatted);
        prop_assert_eq!(&compact.formatted_semantic, &spaced.formatted_semantic);
        prop_assert_eq!(&compact.raw_semantic, &compact.formatted_semantic);
        prop_assert_ne!(&compact.raw_semantic, &next.raw_semantic);
        prop_assert!(!include_str!("../src/generate/format.rs").contains("cargo fix"));
    }
}

struct RejectFormatter;

impl CandidateFormatter for RejectFormatter {
    fn finalize(
        &self,
        _workspace: &Path,
        _target: &CodegenTarget,
        _candidate: &RenderedCandidate,
    ) -> Result<FormattedArtifactSet, DiagnosticSet> {
        Err(DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::GeneratedFormatFailed,
            Some(DiagnosticCoordinate::new("rustfmt")),
            "fixture formatter rejected generated source",
        )))
    }
}

proptest! {
    #![proptest_config(property_config(128, "generation-input-failure"))]

    // Feature: rust-sdk-core-codegen, Property 27: Bootstrap input failure is diagnostic
    #[test]
    fn property_27_bootstrap_input_failure_diagnostic(kind in 0_u8..9) {
        let fixture = Fixture::new();
        let secret = b"super-secret-fixture-value";
        let mut request = fixture.request(GenerateMode::Check);
        let target_path = fixture.workspace.join("codegen/target.json");
        let schema_path = fixture.workspace.join("codegen/schema.json");
        let manifest_path = fixture.workspace.join(BINDING_MANIFEST);

        match kind {
            0 => write(&target_path, b"{super-secret-fixture-value"),
            1 => write(&schema_path, &[0xff, 0xfe, 0xfd]),
            2 => write(&manifest_path, secret),
            3 => {
                fs::remove_file(&schema_path).expect("schema fixture must exist");
                fs::create_dir(&schema_path).expect("schema directory fixture must be created");
            }
            #[cfg(unix)]
            4 => {
                use std::os::unix::fs::symlink;
                fs::remove_file(&schema_path).expect("schema fixture must exist");
                symlink("target.json", &schema_path).expect("schema symlink must be created");
            }
            5 => {
                let parent = fixture.workspace.parent().expect("workspace must have a parent");
                let outside = parent.join("outside-target.json");
                write(&outside, support::TARGET);
                request.overrides = GenerateOverrides {
                    fixture_root: Some(fixture.workspace.clone()),
                    target: Some(outside),
                    ..GenerateOverrides::default()
                };
            }
            6 => {
                fs::remove_file(&target_path).expect("target fixture must exist");
                fs::create_dir(&target_path).expect("target directory fixture must be created");
            }
            #[cfg(unix)]
            7 => {
                use std::os::unix::fs::PermissionsExt as _;
                fs::set_permissions(&target_path, fs::Permissions::from_mode(0o000))
                    .expect("target permissions must be changed");
            }
            _ => {}
        }

        let before = if kind == 7 { None } else { Some(record_files(&fixture.workspace)) };
        let first = execute_with(request.clone(), &RejectFormatter, &NoopPublicationObserver)
            .expect_err("invalid input or formatter rejection must fail");
        let second = execute_with(request, &RejectFormatter, &NoopPublicationObserver)
            .expect_err("repeated invalid input must fail stably");
        prop_assert_eq!(first.to_string(), second.to_string());
        prop_assert!(!first.to_string().contains("super-secret-fixture-value"));
        prop_assert!(!first.to_string().contains(
            fixture.workspace.to_str().expect("fixture workspace must be UTF-8")
        ));
        if let Some(before) = before {
            prop_assert_eq!(record_files(&fixture.workspace), before);
        } else {
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt as _;
                let mode = fs::metadata(&target_path)
                    .expect("target metadata must remain")
                    .permissions()
                    .mode()
                    & 0o777;
                prop_assert_eq!(mode, 0);
                fs::set_permissions(&target_path, fs::Permissions::from_mode(0o600))
                    .expect("target permissions must be restored");
            }
        }
        prop_assert!(!fixture.workspace.join("crates/dagger-sdk/src/gen").exists());
        prop_assert!(!fixture.workspace.join("target/dagger-codegen-publication.json").exists());
    }
}
