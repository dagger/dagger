//! Failure-atomic generated-module publication over the shared manifest-last publisher.

use std::cell::Cell;
use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

use dagger_codegen::module::{
    FormatVersion, GeneratedAsset, GeneratedAssetOwner, GeneratedAssetPath, GeneratedModuleAssets,
    ModuleTarget, RegenerationClass, RenderedModuleAssets, Sha256Digest as ModuleDigest,
    TargetValue, canonical_bytes, manifest_digest,
};
use dagger_sdk_engine::publication::{
    ModuleAssetCandidate, PublicationCheckpoint, PublicationObserver, publish, publish_with,
    verify_module_ownership,
};
use dagger_sdk_engine::{
    EngineDiagnostic, EngineDiagnosticCode, OperationRoot, RelativeOperationPath, Sha256Digest,
};
use proptest::prelude::*;
use sha2::{Digest as _, Sha256};

proptest! {
    #![proptest_config(ProptestConfig::with_cases(128))]

    #[test]
    fn property_24_rejection_generation_failure_atomic(seed in any::<u16>(), schedule in 0_u8..12) {
        let temporary = tempfile::tempdir().unwrap();
        let root = OperationRoot::open(temporary.path()).unwrap();
        let previous_candidate = candidate(seed, false);
        let initial = verify_module_ownership(&root, None, &previous_candidate).unwrap();
        publish(&root, initial).unwrap();
        fs::write(temporary.path().join("user-notes.txt"), b"unowned and preserved\n").unwrap();

        let before = snapshot_tree(temporary.path());
        let previous = previous_candidate.rendered.manifest.clone();
        let previous_bytes = canonical_bytes(&previous).unwrap();
        let next = candidate(seed, true);
        let candidate = ModuleAssetCandidate {
            rendered: next.rendered,
            previous_manifest_digest: Some(engine_digest(&previous_bytes)),
        };
        let plan = verify_module_ownership(&root, Some(&previous), &candidate).unwrap();

        if schedule < 4 {
            let code = match schedule {
                3 => EngineDiagnosticCode::FormatFailed,
                _ => EngineDiagnosticCode::GenerationFailed,
            };
            let rejected = EngineDiagnostic::new(code, Some("module-generation"), "injected pre-publication failure");
            prop_assert!(matches!(rejected.code, EngineDiagnosticCode::GenerationFailed | EngineDiagnosticCode::FormatFailed));
        } else {
            let observer = FaultObserver {
                checkpoint: match schedule {
                    4 => PublicationCheckpoint::Staged,
                    5 => PublicationCheckpoint::BackedUp,
                    8 => PublicationCheckpoint::ManifestLast,
                    _ => PublicationCheckpoint::Published,
                },
                suffix: match schedule {
                    7 => Some("obsolete.rs"),
                    9 => Some("generated-module-assets.json"),
                    _ => None,
                },
                fired: Cell::new(false),
                rollback_fault: schedule >= 10,
            };
            prop_assert!(publish_with(&root, plan, &observer).is_err());
        }
        prop_assert_eq!(snapshot_tree(temporary.path()), before);
    }
}

#[test]
fn successful_publication_preserves_unknown_paths_and_converges() {
    let temporary = tempfile::tempdir().expect("publication root must be available");
    let root = OperationRoot::open(temporary.path()).expect("publication root must open");
    let previous_candidate = candidate(7, false);
    let initial = verify_module_ownership(&root, None, &previous_candidate)
        .expect("initial ownership must verify");
    publish(&root, initial).expect("initial candidate must publish");
    fs::write(temporary.path().join("user-notes.txt"), b"preserve me\n")
        .expect("unknown user file must write");

    let previous = previous_candidate.rendered.manifest;
    let previous_bytes = canonical_bytes(&previous).expect("prior manifest must encode");
    let next = candidate(7, true);
    let replacement = ModuleAssetCandidate {
        rendered: next.rendered,
        previous_manifest_digest: Some(engine_digest(&previous_bytes)),
    };
    let plan = verify_module_ownership(&root, Some(&previous), &replacement)
        .expect("replacement ownership must verify");
    assert!(!plan.changes().is_empty());
    publish(&root, plan).expect("replacement must publish");
    assert_eq!(
        fs::read(temporary.path().join("user-notes.txt")).expect("unknown file remains"),
        b"preserve me\n"
    );

    let current_bytes =
        canonical_bytes(&replacement.rendered.manifest).expect("current manifest must encode");
    let converged = ModuleAssetCandidate {
        rendered: replacement.rendered.clone(),
        previous_manifest_digest: Some(engine_digest(&current_bytes)),
    };
    let plan = verify_module_ownership(&root, Some(&replacement.rendered.manifest), &converged)
        .expect("converged ownership must verify");
    assert!(plan.changes().is_empty());
}

#[test]
fn inconsistent_prior_manifest_cannot_authorize_a_different_path() {
    let temporary = tempfile::tempdir().expect("publication root must be available");
    let root = OperationRoot::open(temporary.path()).expect("publication root must open");
    let previous_candidate = candidate(9, false);
    let initial = verify_module_ownership(&root, None, &previous_candidate)
        .expect("initial ownership must verify");
    publish(&root, initial).expect("initial candidate must publish");

    let mut inconsistent = previous_candidate.rendered.manifest.clone();
    inconsistent.assets.values_mut().next().unwrap().path =
        asset_path("src/dagger_generated/not-the-owned-path.rs");
    inconsistent.digest = manifest_digest(&inconsistent).expect("mutated manifest must hash");
    let inconsistent_bytes = canonical_bytes(&inconsistent).expect("mutated manifest must encode");
    let replacement = candidate(9, true);
    let candidate = ModuleAssetCandidate {
        rendered: replacement.rendered,
        previous_manifest_digest: Some(engine_digest(&inconsistent_bytes)),
    };

    let error = verify_module_ownership(&root, Some(&inconsistent), &candidate)
        .expect_err("an inconsistent prior manifest must not authorize replacement");
    assert_eq!(error.code, EngineDiagnosticCode::OperationManifestStale);
}

struct FaultObserver {
    checkpoint: PublicationCheckpoint,
    suffix: Option<&'static str>,
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
            return Err(fault(path));
        }
        let path_matches = self
            .suffix
            .is_none_or(|suffix| path.as_str().ends_with(suffix));
        if checkpoint == self.checkpoint && path_matches && !self.fired.replace(true) {
            return Err(fault(path));
        }
        Ok(())
    }
}

fn fault(path: &RelativeOperationPath) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::PublicationFailed,
        Some(path.as_str()),
        "injected generated-module publication failure",
    )
}

fn candidate(seed: u16, replacement: bool) -> ModuleAssetCandidate {
    let main_path = asset_path("src/dagger_generated/mod.rs");
    let obsolete_path = asset_path("src/dagger_generated/obsolete.rs");
    let added_path = asset_path("src/dagger_generated/added.rs");
    let manifest_path = asset_path("src/dagger_generated/generated-module-assets.json");
    let main = format!(
        "pub const VALUE: &str = \"{}-{seed}\";\n",
        if replacement { "new" } else { "old" }
    )
    .into_bytes();
    let secondary_path = if replacement {
        &added_path
    } else {
        &obsolete_path
    };
    let secondary = format!("pub const SECONDARY: u16 = {seed};\n").into_bytes();
    let source_digest = module_digest(format!("source-{seed}").as_bytes());
    let mut files = BTreeMap::from([
        (main_path.clone(), main.clone()),
        (secondary_path.clone(), secondary.clone()),
    ]);
    let assets = BTreeMap::from([
        (
            main_path.clone(),
            asset(main_path, &main, source_digest.clone()),
        ),
        (
            secondary_path.clone(),
            asset(secondary_path.clone(), &secondary, source_digest),
        ),
    ]);
    let mut manifest = GeneratedModuleAssets {
        format_version: FormatVersion::current(),
        target: module_target(seed),
        descriptor_digest: module_digest(format!("descriptor-{seed}-{replacement}").as_bytes()),
        manifest_path: manifest_path.clone(),
        assets,
        digest: module_digest(b"pending"),
    };
    manifest.digest = manifest_digest(&manifest).expect("fixture manifest must hash");
    files.insert(
        manifest_path,
        canonical_bytes(&manifest).expect("fixture manifest must encode"),
    );
    ModuleAssetCandidate {
        rendered: RenderedModuleAssets { files, manifest },
        previous_manifest_digest: None,
    }
}

fn asset(path: GeneratedAssetPath, content: &[u8], input_digest: ModuleDigest) -> GeneratedAsset {
    GeneratedAsset {
        path,
        digest: module_digest(content),
        owner: GeneratedAssetOwner::Descriptor,
        input_digest,
        regeneration: RegenerationClass::Authoring,
    }
}

fn module_target(seed: u16) -> ModuleTarget {
    ModuleTarget {
        dagger_revision: target_value("0123456789abcdef0123456789abcdef01234567"),
        engine_version: target_value("v1.0.0"),
        rust_sdk_version: target_value("1.0.0-beta.10"),
        rust_toolchain: target_value("1.89.0"),
        rust_edition: target_value("2024"),
        visible_schema_digest: module_digest(format!("schema-{seed}").as_bytes()),
    }
}

fn asset_path(value: &str) -> GeneratedAssetPath {
    GeneratedAssetPath::new(value).expect("fixture asset path is valid")
}

fn target_value(value: &str) -> TargetValue {
    TargetValue::new(value).expect("fixture target value is valid")
}

fn module_digest(bytes: &[u8]) -> ModuleDigest {
    ModuleDigest::hash_bytes(bytes)
}

fn engine_digest(bytes: &[u8]) -> Sha256Digest {
    format!("sha256:{:x}", Sha256::digest(bytes))
        .parse()
        .expect("SHA-256 text is a valid engine digest")
}

fn snapshot_tree(root: &Path) -> BTreeMap<String, Option<Vec<u8>>> {
    fn visit(root: &Path, directory: &Path, snapshot: &mut BTreeMap<String, Option<Vec<u8>>>) {
        let mut entries = fs::read_dir(directory)
            .expect("snapshot directory must read")
            .map(Result::unwrap)
            .collect::<Vec<_>>();
        entries.sort_by_key(|entry| entry.file_name());
        for entry in entries {
            let path = entry.path();
            let relative = path
                .strip_prefix(root)
                .expect("entry remains below root")
                .to_string_lossy()
                .replace('\\', "/");
            if entry.file_type().expect("entry type must read").is_dir() {
                snapshot.insert(relative, None);
                visit(root, &path, snapshot);
            } else {
                snapshot.insert(
                    relative,
                    Some(fs::read(path).expect("entry bytes must read")),
                );
            }
        }
    }
    let mut snapshot = BTreeMap::new();
    visit(root, root, &mut snapshot);
    snapshot
}
