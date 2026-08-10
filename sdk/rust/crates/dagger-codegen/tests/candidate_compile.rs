//! Private-state compile and rustdoc verification for the exact generated candidate.

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{CoreProjectionRequest, project_core, render_core};

const TARGET: &[u8] = include_bytes!("../../../completeness/target.json");
const SCHEMA: &[u8] = include_bytes!("../../../completeness/snapshots/schema.json");

#[test]
fn exact_candidate_compiles_with_supported_features_and_warning_free_docs() {
    let manifest_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let workspace = manifest_dir
        .parent()
        .and_then(Path::parent)
        .expect("codegen crate must live below the Rust workspace");
    let temporary = tempfile::tempdir().expect("private candidate root must be available");
    let root = temporary.path();

    fs::copy(workspace.join("Cargo.toml"), root.join("Cargo.toml"))
        .expect("workspace manifest must copy");
    fs::copy(workspace.join("Cargo.lock"), root.join("Cargo.lock"))
        .expect("workspace lockfile must copy");
    for package in [
        "dagger-bootstrap",
        "dagger-codegen",
        "dagger-sdk-completeness",
        "dagger-sdk-engine",
    ] {
        copy_tree(
            &workspace.join("crates").join(package),
            &root.join("crates").join(package),
            None,
        );
    }
    let source_package = workspace.join("crates/dagger-sdk");
    let candidate_package = root.join("crates/dagger-sdk");
    fs::create_dir_all(&candidate_package).expect("candidate package directory must exist");
    for file in ["Cargo.toml", "README.md", "LICENSE"] {
        fs::copy(source_package.join(file), candidate_package.join(file))
            .unwrap_or_else(|error| panic!("{file} must copy: {error}"));
    }
    copy_tree(
        &source_package.join("src"),
        &candidate_package.join("src"),
        Some("gen.rs"),
    );

    // The published crate must consume sanitized generated docs directly. Reintroducing
    // a module-wide exemption would hide regressions that this candidate build owns.
    let library_path = candidate_package.join("src/lib.rs");
    let library = fs::read_to_string(&library_path).expect("copied library must be UTF-8");
    let unsuppressed = "#[cfg(feature = \"gen\")]\nmod r#gen;";
    assert!(library.contains(unsuppressed));
    assert!(!library.contains("#[allow(rustdoc::"));
    assert!(!library.contains("#[allow(missing_docs)]"));

    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked target must project");
    let candidate = render_core(&plan).expect("checked target must render");
    for (path, bytes) in candidate.artifacts() {
        let destination = root.join(path);
        if let Some(parent) = destination.parent() {
            fs::create_dir_all(parent).expect("candidate artifact parent must exist");
        }
        fs::write(destination, bytes).expect("private candidate artifact must write");
    }

    // Candidate sources vary independently of the checked-in workspace. Keeping their
    // artifacts beside the private candidate prevents concurrent or containerized
    // verification from reusing an identically hashed but different source tree.
    let target_dir = root.join("target");
    run_cargo(
        root,
        &target_dir,
        &[
            "check",
            "-p",
            "dagger-sdk",
            "--all-features",
            "--locked",
            "--offline",
        ],
    );
    run_cargo(
        root,
        &target_dir,
        &[
            "test",
            "-p",
            "dagger-sdk",
            "--test",
            "core_projection",
            "--all-features",
            "--locked",
            "--offline",
        ],
    );
    run_cargo(
        root,
        &target_dir,
        &[
            "check",
            "-p",
            "dagger-sdk",
            "--test",
            "core_reachability",
            "--all-features",
            "--locked",
            "--offline",
        ],
    );
    run_cargo(
        root,
        &target_dir,
        &[
            "check",
            "-p",
            "dagger-sdk",
            "--no-default-features",
            "--locked",
            "--offline",
        ],
    );
    run_cargo(
        root,
        &target_dir,
        &[
            "rustdoc",
            "-p",
            "dagger-sdk",
            "--all-features",
            "--locked",
            "--offline",
            "--",
            "-D",
            "warnings",
        ],
    );
}

fn copy_tree(source: &Path, destination: &Path, excluded_file: Option<&str>) {
    fs::create_dir_all(destination).expect("candidate source directory must exist");
    for entry in fs::read_dir(source).expect("source directory must be readable") {
        let entry = entry.expect("source entry must be readable");
        let file_type = entry
            .file_type()
            .expect("source entry type must be readable");
        let name = entry.file_name();
        if excluded_file.is_some_and(|excluded| name == excluded) {
            continue;
        }
        let target = destination.join(&name);
        if file_type.is_dir() {
            copy_tree(&entry.path(), &target, None);
        } else if file_type.is_file() {
            fs::copy(entry.path(), target).expect("candidate source file must copy");
        }
    }
}

fn run_cargo(root: &Path, target_dir: &Path, arguments: &[&str]) {
    let output = Command::new(env!("CARGO"))
        .args(arguments)
        .current_dir(root)
        .env("CARGO_TARGET_DIR", target_dir)
        .output()
        .expect("candidate cargo command must start");
    assert!(
        output.status.success(),
        "candidate cargo {} failed:\nstdout:\n{}\nstderr:\n{}",
        arguments.join(" "),
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr),
    );
}
