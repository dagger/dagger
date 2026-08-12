//! Standalone-client project, amendment, and initialization correctness properties.

mod support;

use std::cell::Cell;
use std::collections::{BTreeMap, BTreeSet};
use std::fs;

use dagger_sdk_engine::client::project::{
    AuthoredFile, ClientDocumentationState, ClientProjectRequest, ClientProjectSnapshot,
    reconcile_client_project,
};
use dagger_sdk_engine::project::toolchain::{ToolchainDeclaration, select_toolchain};
use dagger_sdk_engine::publication::{
    AuthoredPublicationCandidate, OperationCandidate, PublicationCheckpoint, PublicationObserver,
    publish, publish_with, verify_authored_publication, verify_ownership,
};
use dagger_sdk_engine::*;
use proptest::prelude::*;
use sha2::{Digest as _, Sha256};
use support::fixed_model_corpus;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn property_11_cargo_creation_adoption_preserve_caller_policy(
        seed in any::<u16>(),
        existing in any::<bool>(),
        library_exists in any::<bool>(),
        comment in "[a-z]{0,24}",
    ) {
        let package = format!("client-{seed}");
        let manifest = existing.then(|| format!(
            "# {comment}\n[package]\nname = \"{package}\"\nversion = \"0.7.0\"\n\n[dependencies]\nserde = \"1\"\n\n[profile.dev]\nopt-level = 1\n"
        ).into_bytes());
        let snapshot = snapshot(seed, manifest, library_exists.then(|| b"pub fn authored() {}\n".to_vec()), None);
        let request = project_request(&package, registry_dependency(), ClientDocumentationState::Generated);
        let first = reconcile_client_project(&snapshot, &request).unwrap();
        let second = reconcile_client_project(&snapshot, &request).unwrap();
        prop_assert_eq!(&first, &second);
        let cargo = amended_file(&first, &path(&format!("client-{seed}/Cargo.toml")));
        let cargo_text = String::from_utf8(cargo).unwrap();
        prop_assert!(cargo_text.contains("publish = false"));
        prop_assert!(cargo_text.contains("edition = \"2024\""));
        prop_assert!(cargo_text.contains("rust-version = \"1.97.1\""));
        if existing {
            prop_assert!(cargo_text.contains("serde = \"1\""));
            prop_assert!(cargo_text.contains("[profile.dev]"));
            let expected_comment = format!("# {comment}");
            prop_assert!(cargo_text.contains(&expected_comment));
            prop_assert!(cargo_text.contains("version = \"0.7.0\""));
        } else {
            prop_assert!(cargo_text.contains("version = \"0.1.0\""));
        }
    }

    #[test]
    fn property_12_sdk_dependency_exact_immutable_fixture_independent(
        seed in any::<u16>(),
        use_git in any::<bool>(),
        mutation in 0_u8..8,
        fixture_state in any::<u64>(),
    ) {
        let package = format!("client-{seed}");
        let dependency = if use_git { git_dependency(seed) } else { registry_dependency() };
        let declaration = dependency_declaration(&dependency, mutation);
        let manifest = format!(
            "[package]\nname = \"{package}\"\nversion = \"0.1.0\"\npublish = false\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\ndagger-sdk = {declaration}\n"
        ).into_bytes();
        let snapshot = snapshot(seed, Some(manifest), Some(Vec::new()), None);
        let request = project_request(&package, dependency, ClientDocumentationState::Generated);
        let first = reconcile_client_project(&snapshot, &request);
        let second = reconcile_client_project(&snapshot, &request);
        prop_assert_eq!(first.is_ok(), mutation == 0, "fixture state {}", fixture_state);
        prop_assert_eq!(&first, &second);
        if let Ok(plan) = first {
            let cargo = amended_file(&plan, &path(&format!("client-{seed}/Cargo.toml")));
            prop_assert_eq!(cargo, amended_file(&second.unwrap(), &path(&format!("client-{seed}/Cargo.toml"))));
        }
    }

    #[test]
    fn property_13_toolchain_lockfile_reproducible_without_resolution(
        seed in any::<u16>(),
        policy in 0_u8..4,
        lockfile in proptest::collection::vec(any::<u8>(), 0..96),
    ) {
        let declaration_path = path(&format!("client-{seed}/rust-toolchain.toml"));
        let bytes = match policy {
            0 => b"[toolchain]\nchannel = \"1.97.1\"\n".to_vec(),
            1 => b"[toolchain]\nchannel = \"1.98.0\"\n".to_vec(),
            2 => b"[toolchain]\nchannel = \"1.96.0\"\n".to_vec(),
            _ => b"[toolchain]\nchannel = \"stable\"\n".to_vec(),
        };
        let result = select_toolchain(&[ToolchainDeclaration { path: &declaration_path, bytes: &bytes }]);
        prop_assert_eq!(result.is_ok(), policy < 2);
        let selected = result.unwrap_or(ToolchainSelection::TargetDefault { toolchain: "1.97.1".parse().unwrap() });
        let mut project = snapshot(seed, None, None, Some(hash(&lockfile)));
        project.toolchain = selected;
        let before = project.lockfile_digest.clone();
        let plan = reconcile_client_project(
            &project,
            &project_request(&format!("client-{seed}"), registry_dependency(), ClientDocumentationState::Initialized),
        ).unwrap();
        prop_assert_eq!(project.lockfile_digest, before);
        prop_assert!(plan.created_files.keys().all(|path| !path.as_str().ends_with("Cargo.lock")));
    }

    #[test]
    fn property_14_generated_manifest_exhaustive_generation_deterministic(
        seed in any::<u8>(),
        library_exists in any::<bool>(),
        prose in "[a-z ]{0,32}",
    ) {
        let package = format!("client-{seed}");
        let project = snapshot(seed.into(), None, library_exists.then(Vec::new), Some(hash(prose.as_bytes())));
        let request = project_request(&package, registry_dependency(), ClientDocumentationState::Generated);
        let left = reconcile_client_project(&project, &request).unwrap();
        let right = reconcile_client_project(&project, &request).unwrap();
        prop_assert_eq!(&left, &right);
        let mut manifest = fixed_model_corpus(seed, true, 2).manifest;
        manifest.operation = OperationKind::GenerateClient;
        manifest.amendments = amendment_records(&left);
        let encoded = canonical_bytes(&manifest).unwrap();
        prop_assert_eq!(encoded, canonical_bytes(&manifest).unwrap());
        prop_assert_eq!(manifest.amendments.len(), left.amendments.len());
        for (coordinate, amendment) in &left.amendments {
            let record = manifest.amendments.get(coordinate).unwrap();
            prop_assert_eq!(&record.semantic_digest, &amendment.next_semantic_digest);
        }
    }

    #[test]
    fn property_15_regeneration_changes_only_proven_ownership(
        seed in any::<u16>(),
        authored_prefix in "[a-z ]{0,48}",
        appended_edit in "[a-z ]{0,48}",
    ) {
        let package = format!("client-{seed}");
        let readme = format!("{authored_prefix}\n").into_bytes();
        let first_snapshot = snapshot(seed, None, Some(Vec::new()), None).with_readme(readme);
        let request = project_request(&package, registry_dependency(), ClientDocumentationState::Generated);
        let first = reconcile_client_project(&first_snapshot, &request).unwrap();
        let readme_path = path(&format!("client-{seed}/README.md"));
        let mut authored = amended_file(&first, &readme_path);
        authored.extend_from_slice(format!("\n{appended_edit}\n").as_bytes());
        let second_snapshot = snapshot(seed, Some(amended_file(&first, &path(&format!("client-{seed}/Cargo.toml")))), Some(amended_file(&first, &path(&format!("client-{seed}/src/lib.rs")))), None).with_readme(authored);
        let second = reconcile_client_project(&second_snapshot, &request).unwrap();
        let next_readme = String::from_utf8(amended_file(&second, &readme_path)).unwrap();
        let expected_prefix = format!("{authored_prefix}\n");
        let expected_suffix = format!("\n{appended_edit}\n");
        prop_assert!(next_readme.starts_with(&expected_prefix));
        prop_assert!(next_readme.ends_with(&expected_suffix));
        let key = StableCoordinate::new("docs.dagger-client-quickstart-v1").unwrap();
        let coordinate = AmendmentCoordinate::new(readme_path, key);
        prop_assert_eq!(&first.amendments[&coordinate].next_semantic_digest, &second.amendments[&coordinate].next_semantic_digest);
    }

    #[test]
    fn property_16_client_mutations_confined_failure_atomic(
        seed in any::<u16>(),
        old in proptest::collection::vec(any::<u8>(), 0..32),
        new in proptest::collection::vec(any::<u8>(), 0..32),
        fault in 0_u8..5,
    ) {
        prop_assume!(old != new);
        let temporary = tempfile::tempdir().unwrap();
        let root = OperationRoot::open(temporary.path()).unwrap();
        let file = path(&format!("client-{seed}/README.md"));
        fs::create_dir_all(temporary.path().join(format!("client-{seed}"))).unwrap();
        fs::write(file.join_lexically(temporary.path()), &old).unwrap();
        let candidate = AuthoredPublicationCandidate {
            files: BTreeMap::from([(file.clone(), (Some(hash(&old)), new.clone()))]),
        };
        let plan = verify_authored_publication(&root, &candidate).unwrap().unwrap();
        let observer = FaultObserver::new(fault);
        let result = publish_with(&root, plan, &observer);
        let visible = fs::read(file.join_lexically(temporary.path())).unwrap();
        if fault < 4 {
            prop_assert!(result.is_err());
            prop_assert_eq!(visible, old);
        } else {
            prop_assert!(result.is_ok());
            prop_assert_eq!(visible, new);
        }
    }

    #[test]
    fn property_02_client_initialization_confined_conservative_idempotent(
        seed in any::<u16>(),
        existing_library in any::<bool>(),
        prose in "[a-z ]{0,48}",
    ) {
        let package = format!("client-{seed}");
        let project = snapshot(seed, None, existing_library.then(|| b"pub fn authored() {}\n".to_vec()), None).with_readme(prose.clone().into_bytes());
        let request = project_request(&package, registry_dependency(), ClientDocumentationState::Initialized);
        let left = reconcile_client_project(&project, &request).unwrap();
        let right = reconcile_client_project(&project, &request).unwrap();
        prop_assert_eq!(&left, &right);
        let prefix = format!("client-{seed}/");
        prop_assert!(left.amendments.keys().all(|coordinate| coordinate.file().as_str().starts_with(&prefix)));
        prop_assert!(left.created_files.keys().all(|path| path.as_str().starts_with(&prefix)));
        prop_assert!(left.amendments.keys().all(|coordinate| !coordinate.file().as_str().contains("dagger_client")));
        let readme = String::from_utf8(amended_file(&left, &path(&format!("client-{seed}/README.md")))).unwrap();
        prop_assert!(readme.contains("dagger generate"));
        prop_assert!(!readme.contains("Bindings are generated"));
    }

    #[test]
    fn property_03_initial_generation_obeys_engine_scope_switch(
        seed in any::<u16>(),
        generate in any::<bool>(),
    ) {
        let package = format!("client-{seed}");
        let project = snapshot(seed, None, None, None);
        let state = if generate { ClientDocumentationState::Generated } else { ClientDocumentationState::Initialized };
        let plan = reconcile_client_project(&project, &project_request(&package, registry_dependency(), state)).unwrap();
        let library_path = path(&format!("client-{seed}/src/lib.rs"));
        let library = plan.amendments.iter().find(|(coordinate, _)| coordinate.file() == &library_path).map(|(_, amendment)| amendment.complete_file_bytes.as_slice());
        prop_assert_eq!(library.is_some(), generate);
        if let Some(library) = library {
            prop_assert!(String::from_utf8_lossy(library).contains("pub mod dagger_client;"));
        }
        prop_assert!(plan.amendments.keys().all(|coordinate| !coordinate.file().as_str().contains("operation-manifest")));
    }
}

#[test]
fn client_initializer_executes_without_schema_manifest_or_lockfile() {
    let corpus = fixed_model_corpus(19, true, 2);
    let temporary = tempfile::tempdir().unwrap();
    let root = OperationRoot::open(temporary.path()).unwrap();
    let result =
        execute_client_initialization(&root, &corpus.client_initialization, &corpus.engine_source)
            .unwrap();
    assert_eq!(result.kind, ExecutionResultKind::ClientInitialization);
    assert!(result.operation_manifest.is_none());
    assert!(!result.touched_paths.is_empty());
    assert!(!root.exists(&path(&format!(
        "{}/Cargo.lock",
        corpus.client_initialization.client_root
    ))));
    assert!(!root.exists(&path(&format!(
        "{}/src/dagger_client/mod.rs",
        corpus.client_initialization.client_root
    ))));
    let replay =
        execute_client_initialization(&root, &corpus.client_initialization, &corpus.engine_source)
            .unwrap();
    assert!(replay.touched_paths.is_empty());
}

#[test]
fn generated_artifacts_and_authored_amendments_publish_as_one_owned_transaction() {
    let corpus = fixed_model_corpus(23, true, 2);
    let temporary = tempfile::tempdir().unwrap();
    let client_root = path("client-23");
    fs::create_dir_all(temporary.path().join("client-23/src")).unwrap();
    fs::write(
        temporary.path().join("client-23/Cargo.toml"),
        b"[package]\nname = \"client-23\"\nversion = \"0.4.0\"\n\n[dependencies]\nserde = \"1\"\n",
    )
    .unwrap();
    fs::write(
        temporary.path().join("client-23/src/lib.rs"),
        b"pub fn authored() {}\n",
    )
    .unwrap();
    fs::write(
        temporary.path().join("client-23/README.md"),
        b"Caller documentation.\n",
    )
    .unwrap();
    fs::write(
        temporary.path().join("client-23/rust-toolchain.toml"),
        b"[toolchain]\nchannel = \"1.97.1\"\n",
    )
    .unwrap();
    let root = OperationRoot::open(temporary.path()).unwrap();
    let snapshot = discover_client_project(&root, &client_root).unwrap();
    let project = reconcile_client_project(
        &snapshot,
        &project_request(
            "client-23",
            registry_dependency(),
            ClientDocumentationState::Generated,
        ),
    )
    .unwrap();
    let generated_path = path("client-23/src/dagger_client/mod.rs");
    let generated = b"//! Generated client root.\n".to_vec();
    let artifacts = BTreeMap::from([(
        generated_path.clone(),
        CandidateArtifact {
            kind: ArtifactKind::RustSource,
            content: generated.clone(),
            ownership: ArtifactOwnership::Generator,
        },
    )]);
    let mut manifest = corpus.manifest;
    manifest.operation = OperationKind::GenerateClient;
    manifest.output_root = client_root;
    manifest.artifacts = BTreeMap::from([(
        generated_path.clone(),
        ArtifactRecord {
            kind: ArtifactKind::RustSource,
            digest: hash(&generated),
            ownership: ArtifactOwnership::Generator,
        },
    )]);
    manifest.amendments = amendment_records(&project);
    manifest.post_work.clear();
    let manifest_path = path("client-23/.dagger/rust/operation-manifest.json");
    let mut candidate = OperationCandidate {
        artifacts,
        amendments: project.amendments,
        created_files: project.created_files,
        retained_previous_artifacts: BTreeSet::new(),
        removed: BTreeSet::new(),
        manifest: manifest.clone(),
        manifest_path: manifest_path.clone(),
        previous_manifest_digest: None,
    };
    let publication = verify_ownership(&root, None, &candidate).unwrap();
    let outcome = publish(&root, publication).unwrap();
    candidate.previous_manifest_digest = Some(outcome.manifest_digest.clone());
    assert!(
        outcome
            .changes
            .iter()
            .any(|change| change.path == generated_path)
    );
    assert!(
        String::from_utf8(root.read(&path("client-23/README.md")).unwrap())
            .unwrap()
            .starts_with("Caller documentation.\n")
    );
    assert_eq!(
        decode_canonical::<OperationManifest>(&root.read(&manifest_path).unwrap()).unwrap(),
        manifest
    );

    let cargo_path = path("client-23/Cargo.toml");
    let cargo = String::from_utf8(root.read(&cargo_path).unwrap())
        .unwrap()
        .replace("publish = false", "publish = true");
    fs::write(cargo_path.join_lexically(temporary.path()), cargo).unwrap();
    let diagnostic = verify_ownership(&root, Some(&manifest), &candidate).unwrap_err();
    assert_eq!(
        diagnostic.code,
        EngineDiagnosticCode::OperationManifestStale
    );
}

#[test]
fn custom_library_root_selects_the_actual_module_and_vcs_subtree() {
    let temporary = tempfile::tempdir().unwrap();
    fs::create_dir_all(temporary.path().join("client/custom")).unwrap();
    fs::write(
        temporary.path().join("client/Cargo.toml"),
        b"[package]\nname = \"custom-client\"\nversion = \"0.1.0\"\n\n[lib]\npath = \"custom/api.rs\"\n",
    )
    .unwrap();
    fs::write(
        temporary.path().join("client/custom/api.rs"),
        b"pub fn authored() {}\n",
    )
    .unwrap();
    fs::write(
        temporary.path().join("client/rust-toolchain.toml"),
        b"[toolchain]\nchannel = \"1.97.1\"\n",
    )
    .unwrap();
    let root = OperationRoot::open(temporary.path()).unwrap();
    let snapshot = discover_client_project(&root, &path("client")).unwrap();
    assert_eq!(snapshot.library_path, path("client/custom/api.rs"));
    assert_eq!(
        snapshot.generated_client_root,
        path("client/custom/dagger_client")
    );
    let plan = reconcile_client_project(
        &snapshot,
        &project_request(
            "custom-client",
            registry_dependency(),
            ClientDocumentationState::Generated,
        ),
    )
    .unwrap();
    let attributes =
        String::from_utf8(amended_file(&plan, &path("client/.gitattributes"))).unwrap();
    assert!(attributes.contains("custom/dagger_client/** linguist-generated=true"));
    assert!(
        String::from_utf8(amended_file(&plan, &snapshot.library_path))
            .unwrap()
            .contains("pub mod dagger_client;")
    );
}

trait SnapshotReadme {
    fn with_readme(self, bytes: Vec<u8>) -> Self;
}

impl SnapshotReadme for ClientProjectSnapshot {
    fn with_readme(mut self, bytes: Vec<u8>) -> Self {
        let readme_path = path(&format!("{}/README.md", self.root));
        self.readme = Some(authored(readme_path, bytes));
        self
    }
}

fn snapshot(
    seed: u16,
    manifest: Option<Vec<u8>>,
    library: Option<Vec<u8>>,
    lockfile_digest: Option<Sha256Digest>,
) -> ClientProjectSnapshot {
    let root = path(&format!("client-{seed}"));
    let manifest_path = path(&format!("{root}/Cargo.toml"));
    let library_path = path(&format!("{root}/src/lib.rs"));
    let package_name = manifest.as_ref().map(|bytes| {
        let source = String::from_utf8_lossy(bytes);
        source
            .lines()
            .find_map(|line| {
                line.trim()
                    .strip_prefix("name = \"")
                    .and_then(|name| name.strip_suffix('"'))
            })
            .unwrap()
            .to_owned()
    });
    ClientProjectSnapshot {
        root: root.clone(),
        manifest: manifest.map(|bytes| authored(manifest_path, bytes)),
        package_name,
        library_path: library_path.clone(),
        generated_client_root: path(&format!("{root}/src/dagger_client")),
        library_root: library.map(|bytes| authored(library_path, bytes)),
        readme: None,
        gitattributes: None,
        gitignore: None,
        lockfile_digest,
        toolchain: ToolchainSelection::Declared {
            toolchain: "1.97.1".parse().unwrap(),
            declaration_path: path(&format!("{root}/rust-toolchain.toml")),
        },
    }
}

fn project_request(
    package: &str,
    sdk_dependency: PublishedSdkDependency,
    documentation: ClientDocumentationState,
) -> ClientProjectRequest {
    ClientProjectRequest {
        identity: ClientProjectIdentity {
            package_name: CargoPackageName::new(package.to_owned()).unwrap(),
            crate_name: RustIdentifier::new(package.replace('-', "_")).unwrap(),
        },
        sdk_dependency,
        documentation,
    }
}

fn registry_dependency() -> PublishedSdkDependency {
    PublishedSdkDependency::Registry {
        registry: CanonicalRegistry::new("crates-io".to_owned()).unwrap(),
        package: SdkPackageName::new("dagger-sdk".to_owned()).unwrap(),
        exact_version: ExactVersion::new("1.0.0-beta.10".to_owned()).unwrap(),
    }
}

fn git_dependency(seed: u16) -> PublishedSdkDependency {
    PublishedSdkDependency::Git {
        url: CanonicalRepositoryUrl::new("https://github.com/dagger/dagger".to_owned()).unwrap(),
        revision: FullRevision::new(format!("{seed:040x}")).unwrap(),
        package: SdkPackageName::new("dagger-sdk".to_owned()).unwrap(),
    }
}

fn dependency_declaration(dependency: &PublishedSdkDependency, mutation: u8) -> String {
    match dependency {
        PublishedSdkDependency::Registry { exact_version, .. } => match mutation {
            0 => format!("{{ version = \"={exact_version}\", registry = \"crates-io\" }}"),
            1 => "\"*\"".to_owned(),
            2 => "\"^1\"".to_owned(),
            3 => "{ path = \"../dagger-sdk\" }".to_owned(),
            4 => "{ workspace = true }".to_owned(),
            5 => "{ version = \"=9.9.9\" }".to_owned(),
            6 => "{ git = \"https://github.com/dagger/dagger\" }".to_owned(),
            _ => format!("{{ version = \"={exact_version}\", registry = \"private\" }}"),
        },
        PublishedSdkDependency::Git { url, revision, .. } => match mutation {
            0 => format!("{{ git = \"{url}\", rev = \"{revision}\" }}"),
            1 => format!("{{ git = \"{url}\", branch = \"main\" }}"),
            2 => format!("{{ git = \"{url}\", tag = \"v1\" }}"),
            3 => "{ path = \"../dagger-sdk\" }".to_owned(),
            4 => "{ workspace = true }".to_owned(),
            5 => format!("{{ git = \"{url}\", rev = \"{:040x}\" }}", 999_u16),
            6 => format!("{{ git = \"{url}\" }}"),
            _ => format!("{{ git = \"https://github.com/other/dagger\", rev = \"{revision}\" }}"),
        },
    }
}

fn amendment_records(plan: &ClientProjectPlan) -> BTreeMap<AmendmentCoordinate, AmendmentRecord> {
    plan.amendments
        .iter()
        .map(|(coordinate, amendment)| {
            (
                coordinate.clone(),
                AmendmentRecord {
                    kind: amendment.kind,
                    file: coordinate.file().clone(),
                    coordinate: coordinate.semantic_key().clone(),
                    semantic_digest: amendment.next_semantic_digest.clone(),
                },
            )
        })
        .collect()
}

fn amended_file(plan: &ClientProjectPlan, path: &RelativeOperationPath) -> Vec<u8> {
    plan.amendments
        .iter()
        .find(|(coordinate, _)| coordinate.file() == path)
        .map(|(_, amendment)| amendment.complete_file_bytes.clone())
        .or_else(|| plan.created_files.get(path).cloned())
        .unwrap()
}

fn authored(path: RelativeOperationPath, bytes: Vec<u8>) -> AuthoredFile {
    AuthoredFile {
        path,
        digest: hash(&bytes),
        bytes,
    }
}

fn path(value: &str) -> RelativeOperationPath {
    RelativeOperationPath::parse(value).unwrap()
}

fn hash(bytes: &[u8]) -> Sha256Digest {
    format!("sha256:{:x}", Sha256::digest(bytes))
        .parse()
        .unwrap()
}

struct FaultObserver {
    fault: u8,
    fired: Cell<bool>,
}

impl FaultObserver {
    fn new(fault: u8) -> Self {
        Self {
            fault,
            fired: Cell::new(false),
        }
    }
}

impl PublicationObserver for FaultObserver {
    fn checkpoint(
        &self,
        checkpoint: PublicationCheckpoint,
        _path: &RelativeOperationPath,
    ) -> Result<(), EngineDiagnostic> {
        let selected = match checkpoint {
            PublicationCheckpoint::Staged => 0,
            PublicationCheckpoint::ManifestLast => 1,
            PublicationCheckpoint::BackedUp => 2,
            PublicationCheckpoint::Published => 3,
            PublicationCheckpoint::Rollback => 5,
        };
        if selected == self.fault && !self.fired.replace(true) {
            Err(EngineDiagnostic::new(
                EngineDiagnosticCode::PublicationFailed,
                Some("fault"),
                "injected publication failure",
            ))
        } else {
            Ok(())
        }
    }
}
