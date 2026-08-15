//! End-to-end typed command, formatting, check, and update verification.

mod support;

use std::fs;
use std::process::Command;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{Receiver, SyncSender, sync_channel};

use dagger_bootstrap::generate::ArtifactPath;
use dagger_bootstrap::generate::format::{CandidateFormatter, FormattedArtifactSet, PinnedRustfmt};
use dagger_bootstrap::generate::publish::{
    ArtifactManifest, NoopPublicationObserver, PublicationCheckpoint, PublicationInterruption,
    PublicationObserver, Publisher, compare,
};
use dagger_bootstrap::generate::{BINDING_MANIFEST, GenerateMode, execute, execute_with};
use dagger_codegen::RenderedCandidate;
use dagger_codegen::diagnostic::DiagnosticSet;
use dagger_codegen::target::CodegenTarget;

use support::{Fixture, record_files, write};

#[test]
fn typed_cli_requires_exactly_one_generation_mode() {
    let fixture = Fixture::new();
    for args in [
        vec![
            "dagger-rust",
            "generate",
            "--workspace",
            fixture
                .workspace
                .to_str()
                .expect("fixture path must be UTF-8"),
        ],
        vec![
            "dagger-rust",
            "generate",
            "--workspace",
            fixture
                .workspace
                .to_str()
                .expect("fixture path must be UTF-8"),
            "--check",
            "--update",
        ],
    ] {
        let error = dagger_bootstrap::cli::run(args)
            .expect_err("invalid mode selection must be diagnostic");
        assert!(
            !error.to_string().contains(
                fixture
                    .workspace
                    .to_str()
                    .expect("fixture path must be UTF-8")
            )
        );
    }

    let output = Command::new(env!("CARGO_BIN_EXE_dagger-rust"))
        .args(["generate", "--workspace", "."])
        .output()
        .expect("dagger-rust process must start");
    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    assert_eq!(
        String::from_utf8(output.stderr).expect("diagnostic must be UTF-8"),
        "GENERATED_PUBLICATION_FAILED [cli]: generation command arguments are invalid\n"
    );
    for invalid in [
        "crates//dagger-sdk/src/gen.rs",
        "crates/dagger-sdk/src/gen.rs/",
        "./crates/dagger-sdk/src/gen.rs",
        "crates/dagger-sdk/src/../gen.rs",
    ] {
        assert!(ArtifactPath::new(invalid).is_err());
    }
}

#[test]
fn repository_generation_is_private_deterministic_and_failure_free() {
    let fixture = Fixture::new();
    let legacy = fixture.workspace.join("crates/dagger-sdk/src/gen.rs");
    write(&legacy, b"// legacy generated predecessor\n");
    let before_update = record_files(&fixture.workspace);

    let update = execute(fixture.request(GenerateMode::Update))
        .expect("explicit update must publish the complete exact-target candidate");
    let changed = update.changed_paths().collect::<Vec<_>>();
    assert!(changed.windows(2).all(|pair| pair[0] < pair[1]));
    assert!(changed.len() > 100);
    assert!(!legacy.exists());
    assert!(
        fixture
            .workspace
            .join("crates/dagger-sdk/src/gen/mod.rs")
            .is_file()
    );
    assert_ne!(record_files(&fixture.workspace), before_update);

    let after_update = record_files(&fixture.workspace);
    let check = execute(fixture.request(GenerateMode::Check))
        .expect("identical private generation must produce no drift");
    assert!(check.changes().is_empty());
    assert_eq!(record_files(&fixture.workspace), after_update);

    let cli = Command::new(env!("CARGO_BIN_EXE_dagger-rust"))
        .args([
            "generate",
            "--workspace",
            fixture
                .workspace
                .to_str()
                .expect("fixture path must be UTF-8"),
            "--check",
        ])
        .output()
        .expect("typed check process must start");
    assert!(cli.status.success());
    assert!(cli.stdout.is_empty());
    assert!(cli.stderr.is_empty());
    assert_eq!(record_files(&fixture.workspace), after_update);

    let transaction = fixture
        .workspace
        .join("target/dagger-codegen-publication.json");
    assert!(!transaction.exists());
    assert!(
        fs::read_dir(fixture.workspace.join("target"))
            .expect("fixture target directory must remain readable")
            .next()
            .is_none()
    );
}

#[test]
fn stale_transaction_paths_cannot_expand_publication_authority() {
    let fixture = Fixture::new();
    let target = support::target();
    let candidate = FormattedArtifactSet::from_bytes(&target, Default::default(), "fixture")
        .expect("empty candidate must be valid");
    let prior = ArtifactManifest::from_artifacts(&target, &candidate);
    let manifest_bytes = prior.encode().expect("empty manifest must encode");
    let manifest_path = ArtifactPath::new(BINDING_MANIFEST).expect("manifest path must be valid");
    let planned = compare(
        &fixture.workspace,
        &candidate,
        &prior,
        &manifest_path,
        &manifest_bytes,
    )
    .expect("unchanged fixture must compare");
    assert!(planned.is_empty());

    let sentinel = fixture.workspace.join("sentinel.txt");
    write(&sentinel, b"outside publication ownership\n");
    let journal = serde_json::json!({
        "transaction_id": "123-0",
        "commit_marker": fixture.workspace.join("target/.dagger-commit-123-0"),
        "entries": [{
            "path": "crates/dagger-sdk/src/gen/alpha.rs",
            "staged": sentinel,
            "backup": fixture.workspace.join("also-not-a-backup"),
            "had_original": false,
            "publishes_candidate": true
        }]
    });
    write(
        &fixture
            .workspace
            .join("target/dagger-codegen-publication.json"),
        &serde_json::to_vec(&journal).expect("forged fixture journal must encode"),
    );

    let error = Publisher::new(&fixture.workspace, &NoopPublicationObserver)
        .publish(
            &candidate,
            &prior,
            &manifest_path,
            &manifest_bytes,
            &planned,
            || Ok(()),
        )
        .expect_err("undeclared recovery paths must be rejected");
    assert!(error.to_string().contains("undeclared path"));
    assert_eq!(
        fs::read(sentinel).expect("sentinel must remain readable"),
        b"outside publication ownership\n"
    );
}

#[test]
fn committed_stale_transaction_finishes_cleanup_without_rolling_back() {
    let fixture = Fixture::new();
    let target = support::target();
    let prior_set = support::formatted(
        &target,
        [(
            "crates/dagger-sdk/src/gen/alpha.rs",
            support::source(&target, "ALPHA", 3, false),
        )],
    );
    let prior = ArtifactManifest::from_artifacts(&target, &prior_set);
    let candidate = support::formatted(
        &target,
        [(
            "crates/dagger-sdk/src/gen/alpha.rs",
            support::source(&target, "ALPHA", 9, false),
        )],
    );
    let manifest_bytes = ArtifactManifest::from_artifacts(&target, &candidate)
        .encode()
        .expect("candidate manifest must encode");
    let artifact = fixture.workspace.join("crates/dagger-sdk/src/gen/alpha.rs");
    write(&artifact, &support::source(&target, "ALPHA", 9, false));
    write(&fixture.workspace.join(BINDING_MANIFEST), &manifest_bytes);

    let transaction_id = "456-0";
    let parent = artifact.parent().expect("artifact must have a parent");
    let backup = parent.join(format!(".dagger-backup-{transaction_id}-0"));
    write(&backup, &support::source(&target, "ALPHA", 3, false));
    let marker = fixture
        .workspace
        .join(format!("target/.dagger-commit-{transaction_id}"));
    write(&marker, transaction_id.as_bytes());
    let journal = serde_json::json!({
        "transaction_id": transaction_id,
        "commit_marker": marker,
        "entries": [{
            "path": "crates/dagger-sdk/src/gen/alpha.rs",
            "staged": parent.join(format!(".dagger-stage-{transaction_id}-0")),
            "backup": backup,
            "had_original": true,
            "publishes_candidate": true
        }]
    });
    write(
        &fixture
            .workspace
            .join("target/dagger-codegen-publication.json"),
        &serde_json::to_vec(&journal).expect("committed fixture journal must encode"),
    );
    let manifest_path = ArtifactPath::new(BINDING_MANIFEST).expect("manifest path must be valid");
    // The pre-crash transaction had already applied this candidate, so its original
    // publication plan is now represented by an empty post-recovery diff.
    let planned = Vec::new();

    Publisher::new(&fixture.workspace, &NoopPublicationObserver)
        .publish(
            &candidate,
            &prior,
            &manifest_path,
            &manifest_bytes,
            &planned,
            || Ok(()),
        )
        .expect("committed recovery must finish cleanup");
    assert_eq!(
        fs::read(&artifact).expect("candidate artifact must remain"),
        support::source(&target, "ALPHA", 9, false)
    );
    assert!(!backup.exists());
    assert!(!marker.exists());
    assert!(
        !fixture
            .workspace
            .join("target/dagger-codegen-publication.json")
            .exists()
    );
}

struct BlockingStageObserver {
    entered: SyncSender<()>,
    resume: Mutex<Receiver<()>>,
    blocked: AtomicBool,
}

impl PublicationObserver for BlockingStageObserver {
    fn checkpoint(
        &self,
        checkpoint: PublicationCheckpoint,
        _path: Option<&ArtifactPath>,
    ) -> Result<(), PublicationInterruption> {
        if checkpoint == PublicationCheckpoint::Stage && !self.blocked.swap(true, Ordering::SeqCst)
        {
            self.entered
                .send(())
                .expect("check thread must observe the staging boundary");
            self.resume
                .lock()
                .expect("resume channel must not be poisoned")
                .recv()
                .expect("update thread must be released");
        }
        Ok(())
    }
}

#[test]
fn concurrent_check_observes_complete_prior_or_complete_candidate() {
    let fixture = Fixture::new();
    let target = support::target();
    let prior_set = support::formatted(
        &target,
        [(
            "crates/dagger-sdk/src/gen/alpha.rs",
            support::source(&target, "ALPHA", 3, false),
        )],
    );
    let prior = ArtifactManifest::from_artifacts(&target, &prior_set);
    write(
        &fixture.workspace.join(BINDING_MANIFEST),
        &prior.encode().expect("prior manifest must encode"),
    );
    write(
        &fixture.workspace.join("crates/dagger-sdk/src/gen/alpha.rs"),
        &support::source(&target, "ALPHA", 3, false),
    );
    let candidate = support::formatted(
        &target,
        [(
            "crates/dagger-sdk/src/gen/alpha.rs",
            support::source(&target, "ALPHA", 9, false),
        )],
    );
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
    .expect("prior state must produce a publication plan");
    let (entered_tx, entered_rx) = sync_channel(0);
    let (resume_tx, resume_rx) = sync_channel(0);
    let observer = BlockingStageObserver {
        entered: entered_tx,
        resume: Mutex::new(resume_rx),
        blocked: AtomicBool::new(false),
    };

    std::thread::scope(|scope| {
        let update = scope.spawn(|| {
            Publisher::new(&fixture.workspace, &observer).publish(
                &candidate,
                &prior,
                &manifest_path,
                &manifest_bytes,
                &planned,
                || Ok(()),
            )
        });
        entered_rx
            .recv()
            .expect("update must reach its first staging boundary");
        let before_check = record_files(&fixture.workspace);
        let concurrent = compare(
            &fixture.workspace,
            &candidate,
            &prior,
            &manifest_path,
            &manifest_bytes,
        )
        .expect("check must observe the complete prior state");
        assert_eq!(concurrent, planned);
        assert_eq!(record_files(&fixture.workspace), before_check);
        resume_tx.send(()).expect("update must be released");
        update
            .join()
            .expect("update thread must not panic")
            .expect("update must publish the complete candidate");
    });
    assert!(
        compare(
            &fixture.workspace,
            &candidate,
            &prior,
            &manifest_path,
            &manifest_bytes,
        )
        .expect("final candidate must compare")
        .is_empty()
    );
}

struct InputMutatingFormatter;

impl CandidateFormatter for InputMutatingFormatter {
    fn finalize(
        &self,
        workspace: &std::path::Path,
        target: &CodegenTarget,
        candidate: &RenderedCandidate,
    ) -> Result<FormattedArtifactSet, DiagnosticSet> {
        let formatted = PinnedRustfmt.finalize(workspace, target, candidate)?;
        write(
            &workspace.join("codegen/target.json"),
            b"{\"changed_after_planning\":true}\n",
        );
        Ok(formatted)
    }
}

#[test]
fn update_revalidates_all_planning_inputs_before_publication() {
    let fixture = Fixture::new();
    let manifest_path = fixture.workspace.join(BINDING_MANIFEST);
    let manifest_before = fs::read(&manifest_path).expect("fixture manifest must be readable");

    let error = execute_with(
        fixture.request(GenerateMode::Update),
        &InputMutatingFormatter,
        &NoopPublicationObserver,
    )
    .expect_err("input drift after planning must prevent publication");
    assert!(error.to_string().contains("input changed after planning"));
    assert_eq!(
        fs::read(manifest_path).expect("prior manifest must remain readable"),
        manifest_before
    );
    assert!(!fixture.workspace.join("crates/dagger-sdk/src/gen").exists());
    assert!(
        !fixture
            .workspace
            .join("target/dagger-codegen-publication.json")
            .exists()
    );
}
