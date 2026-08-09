//! Release fences for generator-owned source and handwritten production comments.

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

use sha2::{Digest as _, Sha256};

fn rust_workspace() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .ancestors()
        .nth(2)
        .expect("crate must remain beneath the Rust workspace")
        .to_owned()
}

fn rust_sources(root: &Path) -> Vec<PathBuf> {
    let mut pending = vec![root.to_owned()];
    let mut files = Vec::new();
    while let Some(directory) = pending.pop() {
        let mut entries = fs::read_dir(&directory)
            .expect("source directory must be readable")
            .collect::<Result<Vec<_>, _>>()
            .expect("source entry must be readable");
        entries.sort_by_key(std::fs::DirEntry::file_name);
        for entry in entries {
            let file_type = entry.file_type().expect("source type must be readable");
            assert!(
                !file_type.is_symlink(),
                "generated source must not be a symlink"
            );
            let path = entry.path();
            if file_type.is_dir() {
                pending.push(path);
            } else if path.extension().is_some_and(|extension| extension == "rs") {
                files.push(path);
            }
        }
    }
    files.sort();
    files
}

fn manifest_path(path: &Path, workspace: &Path) -> String {
    path.strip_prefix(workspace)
        .expect("generated path must remain inside the Rust workspace")
        .components()
        .map(|component| component.as_os_str().to_string_lossy())
        .collect::<Vec<_>>()
        .join("/")
}

fn sha256(bytes: &[u8]) -> String {
    format!("sha256:{:x}", Sha256::digest(bytes))
}

#[test]
fn generated_modules_match_the_owned_manifest() {
    let workspace = rust_workspace();
    let generated_root = workspace.join("crates/dagger-sdk/src/gen");
    let mut generated = rust_sources(&generated_root);
    generated.extend([
        workspace.join("crates/dagger-sdk/tests/core_projection.rs"),
        workspace.join("crates/dagger-sdk/tests/core_reachability.rs"),
    ]);
    generated.sort();

    let manifest: serde_json::Value = serde_json::from_slice(
        &fs::read(workspace.join("completeness/artifacts/core-codegen-bindings.json"))
            .expect("binding manifest must be readable"),
    )
    .expect("binding manifest must be valid JSON");
    let artifacts = manifest["artifacts"]
        .as_object()
        .expect("binding manifest must contain artifacts");
    let declared = artifacts
        .keys()
        .filter(|path| {
            path.starts_with("crates/dagger-sdk/src/gen/")
                || matches!(
                    path.as_str(),
                    "crates/dagger-sdk/tests/core_projection.rs"
                        | "crates/dagger-sdk/tests/core_reachability.rs"
                )
        })
        .cloned()
        .collect::<BTreeSet<_>>();
    let actual = generated
        .iter()
        .map(|path| manifest_path(path, &workspace))
        .collect::<BTreeSet<_>>();
    assert_eq!(actual, declared, "owned generated path set drifted");

    for path in generated {
        let relative = manifest_path(&path, &workspace);
        let bytes = fs::read(&path).expect("generated artifact must be readable");
        let expected = artifacts[&relative]["sha256"]
            .as_str()
            .expect("generated artifact must carry a byte digest");
        assert_eq!(
            sha256(&bytes),
            expected,
            "{relative} was edited outside generation"
        );

        let source = std::str::from_utf8(&bytes).expect("generated Rust must be UTF-8");
        assert!(
            source.starts_with("//!"),
            "{relative} lacks module documentation"
        );
        assert!(
            source.contains("\n// @generated "),
            "{relative} lacks provenance"
        );
        if relative.starts_with("crates/dagger-sdk/src/gen/") {
            for forbidden in [
                "#![allow(",
                "panic!(",
                ".unwrap(",
                ".expect(",
                "unsafe {",
                "cargo fix",
                "Feature:",
                "Task ",
                "Property ",
            ] {
                assert!(
                    !source.contains(forbidden),
                    "{relative} contains {forbidden}"
                );
            }
        }
    }
}

#[test]
fn handwritten_production_comments_do_not_embed_planning_metadata() {
    let crates = rust_workspace().join("crates");
    for crate_name in [
        "dagger-bootstrap",
        "dagger-codegen",
        "dagger-sdk",
        "dagger-sdk-completeness",
    ] {
        let source_root = crates.join(crate_name).join("src");
        for path in rust_sources(&source_root) {
            if path.starts_with(crates.join("dagger-sdk/src/gen"))
                || path
                    .file_stem()
                    .is_some_and(|name| name.to_string_lossy().ends_with("_tests"))
            {
                continue;
            }
            let source = fs::read_to_string(&path).expect("handwritten source must be UTF-8");
            let production = source.split("#[cfg(test)]").next().unwrap_or(&source);
            for line in production
                .lines()
                .filter(|line| line.trim_start().starts_with("//"))
            {
                for forbidden in ["Feature:", "Task ", "Property "] {
                    assert!(
                        !line.contains(forbidden),
                        "{} embeds planning metadata in production: {line}",
                        path.display()
                    );
                }
            }
        }
    }
}
