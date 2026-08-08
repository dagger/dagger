//! Shared exact-target fixtures for bootstrap integration properties.

#![allow(dead_code)]

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use dagger_bootstrap::generate::format::FormattedArtifactSet;
use dagger_bootstrap::generate::publish::ArtifactManifest;
use dagger_bootstrap::generate::{ArtifactPath, GenerateMode, GenerateOverrides, GenerateRequest};
use dagger_codegen::target::CodegenTarget;
use tempfile::TempDir;

pub const TARGET: &[u8] = include_bytes!("../../../../completeness/target.json");
pub const SCHEMA: &[u8] = include_bytes!("../../../../completeness/snapshots/schema.json");
const TOOLCHAIN: &[u8] = include_bytes!("../../../../rust-toolchain.toml");

pub struct Fixture {
    _root: TempDir,
    pub workspace: PathBuf,
}

impl Fixture {
    pub fn new() -> Self {
        let root = tempfile::tempdir().expect("fixture root must be available");
        let workspace = root.path().join("workspace");
        fs::create_dir_all(workspace.join("target"))
            .expect("fixture target directory must be created");
        write(&workspace.join("rust-toolchain.toml"), TOOLCHAIN);
        write(&workspace.join("completeness/target.json"), TARGET);
        write(
            &workspace.join("completeness/snapshots/schema.json"),
            SCHEMA,
        );
        write(
            &workspace.join("completeness/artifacts/ledger.json"),
            b"{}\n",
        );
        write(
            &workspace.join("completeness/core-codegen-mappings.json"),
            b"{}\n",
        );
        let target = target();
        let empty = FormattedArtifactSet::from_bytes(&target, BTreeMap::new(), "fixture")
            .expect("empty formatted set must be valid");
        let manifest = ArtifactManifest::from_artifacts(&target, &empty)
            .encode()
            .expect("empty manifest must encode");
        write(
            &workspace.join("completeness/artifacts/core-codegen-bindings.json"),
            &manifest,
        );
        Self {
            _root: root,
            workspace,
        }
    }

    pub fn request(&self, mode: GenerateMode) -> GenerateRequest {
        GenerateRequest {
            workspace: self.workspace.clone(),
            mode,
            overrides: GenerateOverrides::default(),
        }
    }
}

pub fn target() -> CodegenTarget {
    CodegenTarget::decode_exact(TARGET).expect("checked target fixture must decode")
}

pub fn source(target: &CodegenTarget, name: &str, value: usize, spaced: bool) -> Vec<u8> {
    let provenance = serde_json::json!({
        "format": "dagger-rust-client-v1",
        "ownership": "dagger-codegen",
        "schema_digest": target.schema_digest().to_string(),
        "target_revision": target.dagger_revision().as_str(),
    });
    if spaced {
        format!(
            "//! Generated fixture module.\n// @generated {provenance}\npub const {name} : usize = {value} ;\n"
        )
        .into_bytes()
    } else {
        format!(
            "//! Generated fixture module.\n// @generated {provenance}\npub const {name}: usize = {value};\n"
        )
        .into_bytes()
    }
}

pub fn formatted(
    target: &CodegenTarget,
    files: impl IntoIterator<Item = (&'static str, Vec<u8>)>,
) -> FormattedArtifactSet {
    let files = files
        .into_iter()
        .map(|(path, bytes)| {
            (
                ArtifactPath::new(path).expect("fixture artifact path must be valid"),
                bytes,
            )
        })
        .collect();
    FormattedArtifactSet::from_bytes(target, files, "fixture")
        .expect("fixture artifacts must be valid")
}

pub fn write(path: &Path, bytes: &[u8]) {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).expect("fixture parent must be created");
    }
    fs::write(path, bytes).expect("fixture bytes must be written");
}

pub fn record_files(root: &Path) -> BTreeMap<String, Vec<u8>> {
    fn walk(root: &Path, directory: &Path, files: &mut BTreeMap<String, Vec<u8>>) {
        let mut entries = fs::read_dir(directory)
            .expect("fixture directory must be readable")
            .collect::<Result<Vec<_>, _>>()
            .expect("fixture entries must be readable");
        entries.sort_by_key(std::fs::DirEntry::file_name);
        for entry in entries {
            let file_type = entry
                .file_type()
                .expect("fixture entry type must be readable");
            if file_type.is_dir() {
                walk(root, &entry.path(), files);
            } else if file_type.is_file() {
                let relative = entry
                    .path()
                    .strip_prefix(root)
                    .expect("fixture file must remain below root")
                    .to_string_lossy()
                    .into_owned();
                files.insert(
                    relative,
                    fs::read(entry.path()).expect("fixture file must be readable"),
                );
            } else {
                let relative = entry
                    .path()
                    .strip_prefix(root)
                    .expect("fixture entry must remain below root")
                    .to_string_lossy()
                    .into_owned();
                files.insert(relative, b"<non-regular>".to_vec());
            }
        }
    }

    let mut files = BTreeMap::new();
    walk(root, root, &mut files);
    files
}
