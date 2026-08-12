//! Drift guards for standalone-client user and contributor guidance.

use std::collections::BTreeSet;
use std::path::Path;

use dagger_sdk_completeness::{
    ClientDependencyScope, ClientReportSection, client_generation_scope_input,
};
use dagger_sdk_engine::{CheckpointAction, CheckpointPackage, CheckpointTestTarget};

const GUIDE: &str = include_str!("../../../CLIENT_GENERATION.md");
const ARCHITECTURE: &str = include_str!("../../../ARCHITECTURE.md");
const CONTRIBUTING: &str = include_str!("../../../CONTRIBUTING.md");
const WORKSPACE_README: &str = include_str!("../../../README.md");
const COMPLETENESS_README: &str = include_str!("../../../completeness/README.md");
const CODEGEN_README: &str = include_str!("../../dagger-codegen/README.md");
const ENGINE_README: &str = include_str!("../../dagger-sdk-engine/README.md");
const SDK_README: &str = include_str!("../../dagger-sdk/README.md");
const PROJECT_RECONCILER: &str = include_str!("../../dagger-sdk-engine/src/client/project.rs");
const QUICKSTART_RENDERER: &str = include_str!("../../dagger-codegen/src/client/render.rs");
const USABILITY_HARNESS: &str =
    include_str!("../../dagger-sdk-engine/tests/client_usability_properties.rs");

#[test]
fn documented_local_commands_are_exact_typed_checkpoint_slices() {
    let commands = console_commands(
        GUIDE
            .split("## Contributor checkpoint")
            .nth(1)
            .expect("guide retains contributor checkpoint section"),
    );
    assert_eq!(
        commands,
        vec![
            "cargo test -p dagger-sdk-completeness --test client_generation_evidence --locked",
            "cargo test -p dagger-sdk-completeness --test client_generation_documentation --locked",
            "cargo test -p dagger-sdk-engine --test client_checkpoint_properties --locked",
        ]
    );
    assert_eq!(documented_actions(), expected_actions());
    assert!(commands.iter().all(|command| {
        ![
            "dagger api",
            "./bin/dagger",
            "engine-dev",
            "sdk/go",
            "--workspace",
            "cargo generate",
            "curl ",
        ]
        .iter()
        .any(|forbidden| command.contains(forbidden))
    }));
}

#[test]
fn documented_paths_targets_and_quickstart_contract_exist() {
    let manifest = Path::new(env!("CARGO_MANIFEST_DIR"));
    for path in [
        manifest.join("tests/client_generation_evidence.rs"),
        manifest.join("tests/client_generation_documentation.rs"),
        manifest.join("../dagger-sdk-engine/tests/client_checkpoint_properties.rs"),
    ] {
        assert!(
            path.is_file(),
            "documented target is absent: {}",
            path.display()
        );
    }
    assert!(QUICKSTART_RENDERER.contains("examples/dagger-client-quickstart.rs"));
    assert!(QUICKSTART_RENDERER.contains("let client = dagger_client::connect().await?;"));
    assert!(QUICKSTART_RENDERER.contains("client.close().await?;"));
    assert!(USABILITY_HARNESS.contains("\"quickstart\""));
    assert!(USABILITY_HARNESS.contains("\"--examples\""));
}

#[test]
fn dependency_ownership_and_cross_document_claims_match_production() {
    let target = dagger_sdk_completeness::TargetDigest::new(
        dagger_sdk_completeness::Digest::sha256(b"documentation-target"),
    );
    let input = client_generation_scope_input(target);
    assert_eq!(
        input.dependency_scope,
        ClientDependencyScope::CorePlusOneBoundModule
    );
    let transitive = input
        .mappings
        .iter()
        .find(|mapping| {
            mapping.capability_id.as_str()
                == "policy/rust-policy/client-transitive-dependency-exclusion"
        })
        .expect("dependency-exclusion policy remains mapped");
    assert_eq!(
        transitive.report_section,
        ClientReportSection::GeneratedContent
    );
    assert!(
        transitive
            .rationale
            .as_str()
            .contains("independently bound clients")
    );
    assert!(PROJECT_RECONCILER.contains("bind each dependency through its own client"));

    for document in [
        ARCHITECTURE,
        CONTRIBUTING,
        WORKSPACE_README,
        COMPLETENESS_README,
        CODEGEN_README,
        ENGINE_README,
        SDK_README,
    ] {
        assert!(document.contains("CLIENT_GENERATION.md"));
    }
    assert!(GUIDE.contains("One client contains Core plus at most one bound module"));
    assert!(GUIDE.contains("operation manifest is the sole authority"));
}

fn documented_actions() -> BTreeSet<CheckpointAction> {
    BTreeSet::from([
        test_action(
            CheckpointPackage::DaggerSdkCompleteness,
            "client_generation_evidence",
        ),
        test_action(
            CheckpointPackage::DaggerSdkCompleteness,
            "client_generation_documentation",
        ),
        test_action(
            CheckpointPackage::DaggerSdkEngine,
            "client_checkpoint_properties",
        ),
    ])
}

fn expected_actions() -> BTreeSet<CheckpointAction> {
    documented_actions()
}

fn test_action(package: CheckpointPackage, target: &str) -> CheckpointAction {
    CheckpointAction::Test {
        package,
        targets: BTreeSet::from([
            CheckpointTestTarget::new(target).expect("documented target spelling is valid")
        ]),
        properties: BTreeSet::new(),
    }
}

fn console_commands(section: &str) -> Vec<&str> {
    let mut inside = false;
    let mut commands = Vec::new();
    for line in section.lines() {
        if line == "```console" {
            inside = true;
        } else if line == "```" {
            inside = false;
        } else if inside && !line.is_empty() {
            commands.push(line);
        }
    }
    commands
}
