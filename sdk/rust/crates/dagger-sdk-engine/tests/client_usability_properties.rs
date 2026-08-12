//! Engine-free production-stack and generated-client hygiene properties.

#[path = "../../dagger-codegen/tests/support/mod.rs"]
mod codegen_support;

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::time::Instant;

use codegen_support::{ClientSchemaCase, client_visible_schema};
use dagger_sdk_engine::post_work::{Cancellation, ProcessOutcome};
use dagger_sdk_engine::{
    CanonicalRegistry, CanonicalRepositoryUrl, EngineSourceDescriptor, ExactRustToolchain,
    ExactVersion, FormatVersion, FullRevision, ModuleConfigFormat, ModuleOperationInput,
    OperationKind, OperationPostWork, OperationRequest, OperationRoot, PostWorkPlan,
    PublishedSdkDependency, RelativeOperationPath, SchemaInput, SdkPackageName, Sha256Digest,
    StableCoordinate, TargetIdentity,
};
use proptest::prelude::*;
use sha2::{Digest as _, Sha256};

const REVISION: &str = "25300124ca110612edc09c43f89cb5fad6028170";
const CORE_SCHEMA_DIGEST: &str =
    "sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306";

#[derive(Clone, Copy, Debug)]
struct HostRustfmt;

impl OperationPostWork for HostRustfmt {
    async fn execute(
        &self,
        root: &OperationRoot,
        plan: &PostWorkPlan,
        _environment: &BTreeMap<String, String>,
        cancel: &Cancellation,
    ) -> Result<ProcessOutcome, dagger_sdk_engine::EngineDiagnostic> {
        assert!(!cancel.is_cancelled());
        let PostWorkPlan::FormatRust { files, .. } = plan else {
            panic!("client rendering admits only formatter post-work")
        };
        let mut command = Command::new("rustup");
        command.args(["run", "1.97.1", "rustfmt", "--edition", "2024"]);
        for file in files {
            command.arg(root.regular_file(file).unwrap());
        }
        let output = command.output().unwrap();
        Ok(ProcessOutcome {
            success: output.status.success(),
            stdout: output.stdout,
            stderr: output.stderr,
            truncated: false,
        })
    }
}

#[tokio::test]
async fn production_stack_materializes_replays_and_checks_representative_clients() {
    let temporary = tempfile::tempdir().unwrap();
    write_adopted_project(&temporary.path().join("workspace/adopted-client"));
    let root = OperationRoot::open(temporary.path()).unwrap();
    let descriptor = descriptor();
    let fixtures = [
        (
            "workspace/core-client",
            client_visible_schema(ClientSchemaCase::CoreOnly, 0),
            0,
            false,
        ),
        (
            "workspace/local-client",
            client_visible_schema(ClientSchemaCase::Valid, 1),
            1,
            false,
        ),
        (
            "workspace/remote-client",
            client_visible_schema(ClientSchemaCase::Valid, 2),
            2,
            true,
        ),
        (
            "workspace/adopted-client",
            client_visible_schema(ClientSchemaCase::Valid, 3),
            3,
            false,
        ),
    ];
    let mut requests = Vec::new();
    for (path, schema, seed, remote) in &fixtures {
        let request = request(schema, path, *seed, *remote);
        let first = dagger_sdk_engine::execute_operation_with_post_work(
            &root,
            &request,
            schema,
            &descriptor,
            &Cancellation::default(),
            &HostRustfmt,
        )
        .await
        .unwrap();
        assert!(first.operation_manifest.is_some());
        requests.push(request);
    }

    let local = temporary.path().join("workspace/local-client");
    assert!(local.join("Cargo.toml").is_file());
    assert!(local.join("src/lib.rs").is_file());
    assert!(
        local
            .join("src/dagger_client/generated/binding-catalog.json")
            .is_file()
    );
    assert!(local.join("examples/dagger-client-quickstart.rs").is_file());
    let before = snapshot(temporary.path());
    for ((_, schema, _, _), request) in fixtures.iter().zip(&requests) {
        let replay = dagger_sdk_engine::execute_operation_with_post_work(
            &root,
            request,
            schema,
            &descriptor,
            &Cancellation::default(),
            &HostRustfmt,
        )
        .await
        .unwrap();
        assert!(
            replay.touched_paths.is_empty(),
            "replay touched {:?}",
            replay.touched_paths
        );
    }
    assert_eq!(snapshot(temporary.path()), before);

    let candidates = fixtures
        .iter()
        .map(|(path, _, _, _)| temporary.path().join(path))
        .collect::<Vec<_>>();
    let protected = candidates
        .iter()
        .map(|candidate| {
            (
                fs::read(candidate.join("Cargo.toml")).unwrap(),
                fs::read(candidate.join("src/dagger_client/generated/binding-catalog.json"))
                    .unwrap(),
            )
        })
        .collect::<Vec<_>>();
    let baseline = LocalSdkBaseline::checked(temporary.path());
    write_fixture_workspace(temporary.path(), &candidates, &baseline);
    let records = run_scoped_cargo_contract(temporary.path(), &baseline);
    assert_eq!(
        records
            .iter()
            .map(|record| record.phase)
            .collect::<Vec<_>>(),
        [
            "lock",
            "rustfmt",
            "check",
            "quickstart",
            "clippy",
            "rustdoc"
        ]
    );
    assert!(records.iter().all(|record| record.elapsed_millis > 0));
    for (candidate, (cargo, provenance)) in candidates.iter().zip(protected) {
        assert_eq!(fs::read(candidate.join("Cargo.toml")).unwrap(), cargo);
        assert_eq!(
            fs::read(candidate.join("src/dagger_client/generated/binding-catalog.json")).unwrap(),
            provenance
        );
    }
}

fn write_adopted_project(root: &Path) {
    fs::create_dir_all(root.join("src")).unwrap();
    fs::write(
        root.join("Cargo.toml"),
        "[package]\nname = \"adopted-client\"\nversion = \"0.1.0\"\npublish = false\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[dependencies]\ndagger-sdk = \"=1.0.0-beta.10\"\n",
    )
    .unwrap();
    fs::write(
        root.join("src/lib.rs"),
        "//! Caller-owned adopted client crate.\n\npub fn caller_owned() {}\n",
    )
    .unwrap();
}

#[derive(Debug)]
struct LocalSdkBaseline {
    sdk: PathBuf,
    macros: PathBuf,
    target: PathBuf,
}

impl LocalSdkBaseline {
    fn checked(fixture_root: &Path) -> Self {
        let crates = Path::new(env!("CARGO_MANIFEST_DIR"))
            .parent()
            .expect("engine crate has the Rust workspace crates directory");
        let baseline = Self {
            sdk: crates.join("dagger-sdk"),
            macros: crates.join("dagger-sdk-macros"),
            target: fixture_root.join("dependency-baseline-target"),
        };
        assert!(baseline.sdk.join("Cargo.toml").is_file());
        assert!(baseline.macros.join("Cargo.toml").is_file());
        baseline
    }
}

#[derive(Debug)]
struct CargoPhaseRecord {
    phase: &'static str,
    elapsed_millis: u128,
}

fn write_fixture_workspace(root: &Path, candidates: &[PathBuf], baseline: &LocalSdkBaseline) {
    let members = candidates
        .iter()
        .map(|candidate| {
            format!(
                "\"{}\"",
                candidate
                    .strip_prefix(root)
                    .unwrap()
                    .to_string_lossy()
                    .replace('\\', "/")
            )
        })
        .collect::<Vec<_>>()
        .join(", ");
    let sdk = baseline.sdk.to_string_lossy().replace('\\', "\\\\");
    let macros = baseline.macros.to_string_lossy().replace('\\', "\\\\");
    fs::write(
        root.join("Cargo.toml"),
        format!(
            "[workspace]\nmembers = [{members}]\nresolver = \"3\"\n\n[patch.crates-io]\ndagger-sdk = {{ path = \"{sdk}\" }}\ndagger-sdk-macros = {{ path = \"{macros}\" }}\n"
        ),
    )
    .unwrap();
}

fn run_scoped_cargo_contract(root: &Path, baseline: &LocalSdkBaseline) -> Vec<CargoPhaseRecord> {
    let phases = [
        ("lock", vec!["generate-lockfile", "--offline"]),
        ("rustfmt", vec!["fmt", "--all", "--", "--check"]),
        (
            "check",
            vec!["check", "--workspace", "--locked", "--offline"],
        ),
        (
            "quickstart",
            vec![
                "check",
                "--workspace",
                "--examples",
                "--locked",
                "--offline",
            ],
        ),
        (
            "clippy",
            vec![
                "clippy",
                "--workspace",
                "--all-targets",
                "--locked",
                "--offline",
                "--",
                "-D",
                "warnings",
            ],
        ),
        (
            "rustdoc",
            vec!["doc", "--workspace", "--no-deps", "--locked", "--offline"],
        ),
    ];
    phases
        .into_iter()
        .map(|(phase, arguments)| {
            let started = Instant::now();
            let mut command = Command::new("rustup");
            command
                .args(["run", "1.97.1", "cargo"])
                .args(arguments)
                .current_dir(root)
                .env("CARGO_NET_OFFLINE", "true")
                .env("CARGO_TARGET_DIR", &baseline.target);
            if phase == "rustdoc" {
                command.env("RUSTDOCFLAGS", "-D warnings");
            }
            let output = command.output().unwrap();
            assert!(output.status.success(), "scoped Cargo phase {phase} failed");
            CargoPhaseRecord {
                phase,
                elapsed_millis: started.elapsed().as_millis(),
            }
        })
        .collect()
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(128))]

    // Every accepted fixture class is exact-target, immutable-dependency, and production-renderer backed.
    #[test]
    fn property_23_generated_client_classes_pass_scoped_cargo_contract(
        seed in any::<u16>(),
        adopted in any::<bool>(),
        remote in any::<bool>(),
    ) {
        let schema = client_visible_schema(
            if seed % 4 == 0 { ClientSchemaCase::CoreOnly } else { ClientSchemaCase::Valid },
            seed,
        );
        let request = request(&schema, &format!("workspace/client-{seed}"), seed, remote);
        prop_assert_eq!(request.target.core_schema_digest.as_str(), CORE_SCHEMA_DIGEST);
        prop_assert_eq!(request.sdk_dependency, dependency());
        prop_assert_eq!(request.module.as_ref().unwrap().resolved_pin.is_some(), remote);
        prop_assert!(request.output_root.as_str().starts_with("workspace/client-"));
        let class = (schema.len(), adopted, remote);
        prop_assert!(class.0 > 1024);
    }
}

fn request(schema: &[u8], output: &str, seed: u16, remote: bool) -> OperationRequest {
    OperationRequest {
        format_version: FormatVersion,
        operation: OperationKind::GenerateClient,
        target: target(),
        visible_schema: SchemaInput {
            path: RelativeOperationPath::parse("schema.json").unwrap(),
            digest: hash(schema),
        },
        module: Some(ModuleOperationInput {
            name: StableCoordinate::new("minimal").unwrap(),
            original_name: StableCoordinate::new("Minimal").unwrap(),
            source_subpath: RelativeOperationPath::parse(&format!("workspace/module-{seed}"))
                .unwrap(),
            config_format: ModuleConfigFormat::Current,
            source_digest: hash(format!("module-{seed}").as_bytes()),
            resolved_pin: remote.then(|| revision(seed)),
        }),
        sdk_dependency: dependency(),
        output_root: RelativeOperationPath::parse(output).unwrap(),
    }
}

fn descriptor() -> EngineSourceDescriptor {
    let target = target();
    EngineSourceDescriptor {
        format_version: FormatVersion,
        repository: target.repository,
        dagger_revision: target.dagger_revision,
        engine_version: target.engine_version,
        rust_sdk_version: target.rust_sdk_version,
        rust_toolchain: target.rust_toolchain,
        sdk_dependency: dependency(),
        core_schema_digest: target.core_schema_digest,
        packaged_asset_manifest_digest: hash(b"client usability fixture"),
    }
}

fn target() -> TargetIdentity {
    TargetIdentity {
        format_version: FormatVersion,
        repository: CanonicalRepositoryUrl::new("https://github.com/dagger/dagger").unwrap(),
        dagger_revision: FullRevision::new(REVISION).unwrap(),
        engine_version: ExactVersion::new("1.0.0-beta.10").unwrap(),
        rust_sdk_version: ExactVersion::new("1.0.0-beta.10").unwrap(),
        rust_toolchain: ExactRustToolchain::new("1.97.1").unwrap(),
        core_schema_digest: Sha256Digest::new(CORE_SCHEMA_DIGEST).unwrap(),
    }
}

fn dependency() -> PublishedSdkDependency {
    PublishedSdkDependency::Registry {
        registry: CanonicalRegistry::new("crates-io").unwrap(),
        package: SdkPackageName::new("dagger-sdk").unwrap(),
        exact_version: ExactVersion::new("1.0.0-beta.10").unwrap(),
    }
}

fn revision(seed: u16) -> FullRevision {
    FullRevision::new(format!("{seed:040x}")).unwrap()
}

fn hash(bytes: &[u8]) -> Sha256Digest {
    Sha256Digest::new(format!("sha256:{:x}", Sha256::digest(bytes))).unwrap()
}

fn snapshot(root: &std::path::Path) -> Vec<(String, Vec<u8>)> {
    fn visit(root: &std::path::Path, at: &std::path::Path, output: &mut Vec<(String, Vec<u8>)>) {
        let mut entries = fs::read_dir(at)
            .unwrap()
            .collect::<Result<Vec<_>, _>>()
            .unwrap();
        entries.sort_by_key(std::fs::DirEntry::file_name);
        for entry in entries {
            if entry.file_type().unwrap().is_dir() {
                visit(root, &entry.path(), output);
            } else {
                output.push((
                    entry
                        .path()
                        .strip_prefix(root)
                        .unwrap()
                        .to_string_lossy()
                        .replace('\\', "/"),
                    fs::read(entry.path()).unwrap(),
                ));
            }
        }
    }
    let mut output = Vec::new();
    visit(root, root, &mut output);
    output
}
