//! Executable package-selection and Cargo-policy preservation properties.

mod support;

use std::collections::{BTreeMap, BTreeSet};
use std::fs;

use dagger_sdk_engine::project::manifest::{plan_manifest, plan_manifest_with_workspace};
use dagger_sdk_engine::project::toolchain::{ToolchainDeclaration, select_toolchain};
use dagger_sdk_engine::project::{
    CargoMetadataPackage, CargoMetadataV1, PackageSelection, select_or_create_package,
    select_package,
};
use dagger_sdk_engine::{
    ArtifactKind, ArtifactOwnership, ArtifactRecord, CargoTarget, EngineDiagnosticCode,
    OperationRoot, PostWorkPlan, PublishedSdkDependency, RelativeOperationPath, ToolchainSelection,
};
use proptest::prelude::*;
use sha2::{Digest as _, Sha256};
use support::fixed_model_corpus;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // Package ownership is a cardinality check over normalized workspace members.
    #[test]
    fn property_07_cargo_package_selection_exactly_one(
        seed in any::<u16>(),
        owners in 0_usize..3,
        reverse in any::<bool>(),
        use_hint in any::<bool>(),
    ) {
        let temporary = tempfile::tempdir().unwrap();
        let module = RelativeOperationPath::parse(&format!("modules/module-{seed}")).unwrap();
        let real_root = temporary.path().canonicalize().unwrap();
        let module_absolute = module.join_lexically(&real_root);
        fs::create_dir_all(&module_absolute).unwrap();
        let manifest = module_absolute.join("Cargo.toml");
        fs::write(&manifest, "[package]\nname = \"module\"\nversion = \"0.1.0\"\n").unwrap();
        let target = real_root.join("target");
        fs::create_dir(&target).unwrap();
        let mut packages = (0..owners)
            .map(|index| CargoMetadataPackage {
                id: format!("module-{index}"),
                name: format!("module-{index}"),
                manifest_path: manifest.clone(),
            })
            .collect::<Vec<_>>();
        if reverse {
            packages.reverse();
        }
        let metadata = CargoMetadataV1 {
            workspace_members: packages.iter().map(|package| package.id.clone()).collect(),
            packages,
            workspace_root: real_root,
            target_directory: target,
        };
        let hint = use_hint.then(|| {
            RelativeOperationPath::parse(&format!("{}/Cargo.toml", module.as_str())).unwrap()
        });
        let result = select_package(
            &metadata,
            temporary.path(),
            &module,
            hint.as_ref(),
        );
        match owners {
            1 => {
                let selected = result.unwrap();
                prop_assert_eq!(selected.package_root, module.clone());
                prop_assert_eq!(selected.manifest_path.as_str(), format!("{}/Cargo.toml", module.as_str()));
            }
            0 => prop_assert_eq!(result.unwrap_err().code, EngineDiagnosticCode::CargoPackageMissing),
            _ => prop_assert_eq!(result.unwrap_err().code, EngineDiagnosticCode::CargoPackageAmbiguous),
        }

        let create = select_or_create_package(None, temporary.path(), &module, None).unwrap();
        let creates_selected_root = matches!(
            create,
            PackageSelection::Create { package_root, .. } if package_root == module
        );
        prop_assert!(creates_selected_root);
    }

    // Adoption changes only the two SDK-owned Cargo subjects and retains authored policy.
    #[test]
    fn property_08_cargo_adoption_preserves_caller_policy(
        seed in any::<u16>(),
        use_git in any::<bool>(),
        inherited in any::<bool>(),
        conflict in any::<bool>(),
        comment in "[a-z]{0,16}",
    ) {
        let dependency = dependency(seed, use_git);
        let binary = RelativeOperationPath::parse("src/bin/dagger-module.rs").unwrap();
        let package = if inherited {
            format!(
                "# {comment}\n[package]\nname = \"caller\"\nversion = \"0.2.0\"\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\ndagger-sdk = {{ workspace = true, features = [\"tracing\"] }}\nserde = \"1\"\n"
            )
        } else if conflict {
            format!(
                "# {comment}\n[package]\nname = \"caller\"\nversion = \"0.2.0\"\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\ndagger-sdk = \"*\"\nserde = \"1\"\n"
            )
        } else {
            format!(
                "# {comment}\n[package]\nname = \"caller\"\nversion = \"0.2.0\"\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\nserde = \"1\"\n\n[profile.release]\nlto = true\n"
            )
        };
        if inherited {
            let workspace = "# workspace-policy\n[workspace]\nmembers = [\"caller\"]\n\n[workspace.dependencies]\nserde = \"1\"\n";
            let plan = plan_manifest_with_workspace(
                package.as_bytes(),
                workspace.as_bytes(),
                &dependency,
                &binary,
            ).unwrap();
            let package_rendered = String::from_utf8(plan.package.rendered).unwrap();
            let workspace_rendered = String::from_utf8(plan.workspace_rendered).unwrap();
            let preserves_comment = package_rendered.contains(&format!("# {}", comment));
            prop_assert!(preserves_comment);
            prop_assert!(package_rendered.contains("features = [\"tracing\"]"));
            prop_assert!(package_rendered.contains("serde = \"1\""));
            assert_runtime_dependency(&package_rendered);
            prop_assert!(workspace_rendered.contains("# workspace-policy"));
            assert_dependency(&workspace_rendered, &dependency);
        } else {
            let result = plan_manifest(Some(package.as_bytes()), "caller", &dependency, &binary);
            if conflict {
                prop_assert_eq!(result.unwrap_err().code, EngineDiagnosticCode::SdkDependencyConflict);
            } else {
                let rendered = String::from_utf8(result.unwrap().rendered).unwrap();
                let preserves_comment = rendered.contains(&format!("# {}", comment));
                prop_assert!(preserves_comment);
                prop_assert!(rendered.contains("serde = \"1\""));
                prop_assert!(rendered.contains("[profile.release]"));
                assert_runtime_dependency(&rendered);
                assert_dependency(&rendered, &dependency);
            }
        }

        let toolchain_path = RelativeOperationPath::parse("rust-toolchain.toml").unwrap();
        let declaration = format!("[toolchain]\nchannel = \"{}.{}.{}\"\n", 1, 97 + seed % 2, 1);
        let selected = select_toolchain(&[ToolchainDeclaration {
            path: &toolchain_path,
            bytes: declaration.as_bytes(),
        }]);
        let exact_declared = matches!(selected.unwrap(), ToolchainSelection::Declared { .. });
        prop_assert!(exact_declared);
    }
}

fn dependency(seed: u16, use_git: bool) -> PublishedSdkDependency {
    if use_git {
        PublishedSdkDependency::Git {
            url: "https://github.com/dagger/dagger".parse().unwrap(),
            revision: format!("{seed:040x}").parse().unwrap(),
            package: "dagger-sdk".parse().unwrap(),
        }
    } else {
        PublishedSdkDependency::Registry {
            registry: "crates-io".parse().unwrap(),
            package: "dagger-sdk".parse().unwrap(),
            exact_version: format!("1.0.0-beta.{}", seed % 10).parse().unwrap(),
        }
    }
}

fn assert_dependency(rendered: &str, dependency: &PublishedSdkDependency) {
    match dependency {
        PublishedSdkDependency::Registry { exact_version, .. } => {
            assert!(rendered.contains(&format!("={exact_version}")));
        }
        PublishedSdkDependency::Git { url, revision, .. } => {
            assert!(rendered.contains(url.as_str()));
            assert!(rendered.contains(revision.as_str()));
            assert!(!rendered.contains("branch ="));
            assert!(!rendered.contains("tag ="));
        }
    }
}

fn assert_runtime_dependency(rendered: &str) {
    assert!(rendered.contains("tokio"));
    assert!(rendered.contains("features = [\"rt\", \"net\", \"time\"]"));
}

#[test]
fn cargo_metadata_and_toolchain_fixtures_reject_ambiguous_or_moving_inputs() {
    let metadata = br#"{
        "packages": [],
        "workspace_members": [],
        "workspace_root": "/work",
        "target_directory": "/work/target",
        "future_field": true
    }"#;
    assert!(dagger_sdk_engine::project::decode_metadata(metadata).is_ok());

    let path = RelativeOperationPath::parse("rust-toolchain.toml").unwrap();
    let moving = ToolchainDeclaration {
        path: &path,
        bytes: b"[toolchain]\nchannel = \"stable\"\n",
    };
    assert_eq!(
        select_toolchain(&[moving]).unwrap_err().code,
        EngineDiagnosticCode::ToolchainNonReproducible
    );
}

#[test]
fn vcs_fixture_preserves_crlf_and_unrelated_rules() {
    let entries = BTreeSet::from(["generated/** linguist-generated=true".to_owned()]);
    let current = b"# caller\r\n/vendor\r\n";
    let rendered = dagger_sdk_engine::project::vcs::append_missing_lines(current, &entries);
    assert!(rendered.starts_with(current));
    assert_eq!(
        rendered,
        b"# caller\r\n/vendor\r\ngenerated/** linguist-generated=true\n"
    );
}

#[test]
fn descriptor_fixture_binds_registry_release_to_the_packaged_sdk_version() {
    let mut descriptor = fixed_model_corpus(3, true, 0).engine_source;
    assert!(descriptor.validate().is_ok());
    descriptor.rust_sdk_version = "1.0.0-beta.11".parse().unwrap();
    assert_eq!(
        descriptor.validate().unwrap_err().code,
        EngineDiagnosticCode::SdkManifestInvalid
    );
}

#[test]
fn initialization_is_dependency_gated_and_never_adopts_authored_source() {
    let corpus = fixed_model_corpus(4, true, 0);
    let module_root = RelativeOperationPath::parse("modules/caller").unwrap();
    let declaration_path =
        RelativeOperationPath::parse("modules/caller/rust-toolchain.toml").unwrap();
    let toolchain = ToolchainSelection::Declared {
        toolchain: "1.97.1".parse().unwrap(),
        declaration_path,
    };
    let manifest = b"# caller-policy\n[package]\nname = \"caller\"\nversion = \"0.1.0\"\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\nserde = \"1\"\n";
    let authored_source = b"pub fn caller_owned() {}\n";
    let rejected = dagger_sdk_engine::initialization::plan_initialization(
        dagger_sdk_engine::initialization::InitializationInputs {
            module_root: &module_root,
            package_name: "caller",
            manifest: Some(manifest),
            starter_source: Some(authored_source),
            gitignore: None,
            gitattributes: None,
            dependency: &corpus.dependency,
            toolchain: &toolchain,
            dependency_resolved: false,
            lockfile_present: false,
        },
    )
    .unwrap_err();
    assert_eq!(
        rejected.code,
        EngineDiagnosticCode::DependencyResolutionFailed
    );

    let plan = dagger_sdk_engine::initialization::plan_initialization(
        dagger_sdk_engine::initialization::InitializationInputs {
            module_root: &module_root,
            package_name: "caller",
            manifest: Some(manifest),
            starter_source: Some(authored_source),
            gitignore: Some(b"/caller-cache\n"),
            gitattributes: Some(b"README.md -text\n"),
            dependency: &corpus.dependency,
            toolchain: &toolchain,
            dependency_resolved: true,
            lockfile_present: true,
        },
    )
    .unwrap();
    assert!(!plan.starter_created);
    assert!(
        !plan
            .files
            .contains_key(&RelativeOperationPath::parse("modules/caller/src/lib.rs").unwrap())
    );
    assert!(plan.files.keys().all(|path| {
        !path.as_str().ends_with("dagger.toml") && !path.as_str().ends_with("dagger-module.toml")
    }));
    assert!(
        plan.regeneration
            .values()
            .all(|command| *command == "dagger generate")
    );
    let rendered_manifest = plan
        .files
        .get(&RelativeOperationPath::parse("modules/caller/Cargo.toml").unwrap())
        .unwrap();
    assert!(String::from_utf8_lossy(rendered_manifest).contains("# caller-policy"));
    assert!(matches!(
        plan.post_work.as_slice(),
        [PostWorkPlan::GenerateLockfile { .. }]
    ));
}

#[test]
fn runtime_typestate_requires_lock_toolchain_manifest_and_owned_binary() {
    let temporary = tempfile::tempdir().unwrap();
    let root_path = temporary.path().canonicalize().unwrap();
    fs::create_dir_all(root_path.join("workspace-5/src/bin")).unwrap();
    fs::write(root_path.join("workspace-5/Cargo.lock"), "# locked\n").unwrap();
    let source = b"fn main() {}\n";
    fs::write(
        root_path.join("workspace-5/src/bin/dagger-module.rs"),
        source,
    )
    .unwrap();
    let root = OperationRoot::open(&root_path).unwrap();
    let corpus = fixed_model_corpus(5, true, 0);
    let target = CargoTarget {
        name: "dagger-module".parse().unwrap(),
        source_path: RelativeOperationPath::parse("workspace-5/src/bin/dagger-module.rs").unwrap(),
    };
    let mut manifest = corpus.manifest;
    manifest.artifacts = BTreeMap::from([(
        target.source_path.clone(),
        ArtifactRecord {
            kind: ArtifactKind::RustSource,
            digest: format!("sha256:{:x}", Sha256::digest(source))
                .parse()
                .unwrap(),
            ownership: ArtifactOwnership::Generator,
        },
    )]);
    let promoted = dagger_sdk_engine::project::promote_runtime_project(
        &root,
        corpus.discovered,
        target,
        "1.97.1".parse().unwrap(),
        &manifest,
    )
    .unwrap();
    assert_eq!(promoted.toolchain.as_str(), "1.97.1");
}
