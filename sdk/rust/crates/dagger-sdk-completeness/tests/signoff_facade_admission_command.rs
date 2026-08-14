//! Engine-free process boundary for pre-target facade admission.

#![cfg(unix)]

use std::env;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::OnceLock;

use dagger_sdk_completeness::*;
use serde_json::Value;

fn repository_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../..")
        .canonicalize()
        .unwrap()
}

fn checked_artifact(root: &Path, path: &str) -> Vec<u8> {
    fs::read(root.join("sdk/rust/completeness").join(path)).unwrap()
}

fn checked_policy(root: &Path) -> (CaseCatalog, FacadeRouteRegistry) {
    let ledger: ResolvedLedger =
        decode_canonical(&checked_artifact(root, "artifacts/ledger.json")).unwrap();
    let reviewed: ReviewedConformanceScope =
        decode_canonical(&checked_artifact(root, "conformance-scope.json")).unwrap();
    let applicability: ConformanceScopeInput =
        decode_canonical(&checked_artifact(root, "conformance-applicability.json")).unwrap();
    let scope = derive_conformance_scope(&ledger, &reviewed, applicability).unwrap();
    let assertions: AssertionCatalogInput =
        decode_canonical(&checked_artifact(root, "conformance-assertions.json")).unwrap();
    let fixtures: FixtureRegistryInput =
        decode_canonical(&checked_artifact(root, "conformance-fixtures.json")).unwrap();
    let cases: CaseCatalogInput =
        decode_canonical(&checked_artifact(root, "conformance-cases.json")).unwrap();
    let candidates: RustFirstConformanceManifestInput = decode_canonical(&checked_artifact(
        root,
        "conformance-scenario-candidates.json",
    ))
    .unwrap();
    let registrations: RustScenarioRegistryInput = decode_canonical(&checked_artifact(
        root,
        "conformance-scenario-realizations.json",
    ))
    .unwrap();
    let assertions = compile_assertion_catalog(&scope, assertions).unwrap();
    let fixtures = compile_fixture_registry(fixtures).unwrap();
    let catalog = compile_case_catalog(&scope, &assertions, &fixtures, cases).unwrap();
    let observable =
        compile_observable_fixture_program_registry(&assertions, &fixtures, &catalog).unwrap();
    let runner =
        fs::read(root.join("toolchains/rust-sdk-dev/testdata/scenario_conformance.rs")).unwrap();
    let scenarios = compile_rust_scenario_registry(
        registrations,
        &candidates,
        &catalog,
        &Digest::sha256(runner),
    )
    .unwrap();
    let routes = compile_facade_route_registry(&catalog, &observable, &scenarios).unwrap();
    (catalog, routes)
}

fn commit(byte: u8) -> CommitSha {
    CommitSha::new(format!("{byte:02x}").repeat(20)).unwrap()
}

fn native(platform: PlatformDescriptor, source: &Digest) -> NativePlatformObservation {
    NativePlatformObservation {
        format_version: ConformanceFormatVersion::V1,
        platform,
        runner_digest: Digest::sha256("facade fixture runner"),
        toolchain_digest: Digest::sha256("facade fixture toolchain"),
        rust_version: SemverVersion::new("1.97.1").unwrap(),
        source_digest: source.clone(),
        lockfiles_digest: Digest::sha256("facade fixture lockfiles"),
        test_digest: Digest::sha256("facade fixture native tests"),
        link_mechanism: NativeLinkMechanism::PosixSymlink,
        domains: CanonicalSet::new(required_native_platform_domains()),
        outcome: NativeJobOutcome::Passed,
        native_execution: true,
        dagger_invocations: 0,
        engine_starts: 0,
        docker_invocations: 0,
        other_sdk_invocations: 0,
    }
}

fn process(binary: &Path) -> Command {
    let mut command = Command::new(binary);
    command.stdout(Stdio::null()).stderr(Stdio::null());
    command
}

struct Fixture {
    _temp: tempfile::TempDir,
    repository: PathBuf,
    binary: PathBuf,
    plan: PathBuf,
    bundle: PathBuf,
    catalog: PathBuf,
    closure: PathBuf,
    platform: PathBuf,
    profile: PathBuf,
    preflight: PathBuf,
    fake_path: std::ffi::OsString,
    target_marker: PathBuf,
    case_count: usize,
}

impl Fixture {
    fn new() -> Self {
        let repository = repository_root();
        let binary = PathBuf::from(env!("CARGO_BIN_EXE_dagger-rust-sdk-signoff"));
        let temp = tempfile::tempdir().unwrap();
        let (catalog, routes) = checked_policy(&repository);
        let focused_source = match catalog.subject() {
            SubjectIdentity::Revision(revision) => Digest::sha256(revision.as_str()),
            SubjectIdentity::SourceDigest(digest) => digest.clone(),
        };
        let platform_set = assemble_supported_native_platform_set(
            catalog.target_digest().clone(),
            vec![
                native(PlatformDescriptor::linux_amd64(), &focused_source),
                native(
                    PlatformDescriptor {
                        operating_system: OperatingSystem::Macos,
                        architecture: Architecture::Arm64,
                    },
                    &focused_source,
                ),
            ],
        )
        .unwrap();
        let platform = temp.path().join("platform.json");
        fs::write(&platform, canonical_bytes(&platform_set).unwrap()).unwrap();

        let rust_security = temp.path().join("rust-security.json");
        assert!(
            process(&binary)
                .args([
                    "rust-security-report",
                    "--root",
                    repository.to_str().unwrap(),
                    "--output",
                    rust_security.to_str().unwrap(),
                ])
                .status()
                .unwrap()
                .success()
        );
        let closure = temp.path().join("closure.json");
        let closure_markdown = temp.path().join("closure.md");
        assert!(
            process(&binary)
                .args([
                    "implementation-closure",
                    "--root",
                    repository.to_str().unwrap(),
                    "--platform",
                    platform.to_str().unwrap(),
                    "--rust-security",
                    rust_security.to_str().unwrap(),
                    "--output",
                    closure.to_str().unwrap(),
                    "--markdown-output",
                    closure_markdown.to_str().unwrap(),
                ])
                .status()
                .unwrap()
                .success()
        );

        let subject_revision = match catalog.subject() {
            SubjectIdentity::Revision(revision) => revision.clone(),
            SubjectIdentity::SourceDigest(_) => commit(0x22),
        };
        let seed = ArtifactPlanSeed {
            format_version: ConformanceFormatVersion::V1,
            target_descriptor_digest: catalog.target_digest().clone(),
            target_revision: commit(0x11),
            subject: SubjectRevisionObservation {
                repository: "https://github.com/iw/dagger".to_owned(),
                revision: subject_revision.clone(),
                focused_source_digest: focused_source.clone(),
                workspace_focused_source_digest: focused_source,
                reachable: true,
                clean: true,
                immutable: true,
            },
            platform: catalog.platform().clone(),
            engine_input_digest: Digest::sha256("facade engine input"),
            cli_input_digest: Digest::sha256("facade cli input"),
            go_runtime_digest: Digest::sha256("facade go runtime"),
            rust_manifest_digest: Digest::sha256("facade rust manifest"),
            toolchain_digests: required_artifact_toolchains()
                .into_iter()
                .map(|role| (role, Digest::sha256(format!("facade toolchain {role:?}"))))
                .collect(),
            component_provenance: required_artifact_components()
                .into_iter()
                .map(|component| {
                    (
                        component,
                        CanonicalSet::new([ProvenanceId::new(
                            format!("facade/{component:?}").to_ascii_lowercase(),
                        )
                        .unwrap()]),
                    )
                })
                .collect(),
        };
        let rust_dependency = RustSdkDependencyDescriptor {
            source: RustSdkDependencySource::Git,
            package: "dagger-sdk".to_owned(),
            url: "https://github.com/iw/dagger".to_owned(),
            revision: subject_revision,
        };
        let rust_dependency_descriptor_digest = rust_dependency.direct_digest().unwrap();
        let build_plan = seal_artifact_build_plan(
            seed,
            Digest::sha256("facade Rust descriptor"),
            rust_dependency,
            rust_dependency_descriptor_digest,
            required_artifact_components()
                .into_iter()
                .map(|component| {
                    (
                        component,
                        Digest::sha256(format!("facade component {component:?}")),
                    )
                })
                .collect(),
        )
        .unwrap();
        let payload = b"facade admission exact retained OCI payload".to_vec();
        let manifest = artifact_manifest_for_payload(&build_plan, &payload).unwrap();
        let provenance = ArtifactProvenanceDocument {
            format_version: ConformanceFormatVersion::V1,
            components: build_plan
                .components
                .iter()
                .map(|(component, record)| (*component, record.provenance.clone()))
                .collect(),
            toolchain_digests: build_plan.toolchain_digests.clone(),
        };
        let verified = assemble_artifact_bundle(manifest, provenance, payload).unwrap();
        let case_count = catalog.cases().len();
        let build_plan_path = temp.path().join("build-plan.json");
        fs::write(&build_plan_path, canonical_bytes(&build_plan).unwrap()).unwrap();
        let bundle = temp.path().join("artifact.tar");
        fs::write(&bundle, verified.bytes()).unwrap();
        let plan = temp.path().join("run-plan.json");
        let catalog = repository.join("sdk/rust/completeness/conformance-cases.json");
        let profile = repository.join("sdk/rust/completeness/signoff-host-profile.json");
        let preflight =
            repository.join("sdk/rust/completeness/evidence/signoff-host-preflight.json");
        assert_eq!(routes.physical_executions(), 74);
        assert!(
            process(&binary)
                .args([
                    "run-plan",
                    "--root",
                    repository.to_str().unwrap(),
                    "--build-plan",
                    build_plan_path.to_str().unwrap(),
                    "--bundle",
                    bundle.to_str().unwrap(),
                    "--closure",
                    closure.to_str().unwrap(),
                    "--platform",
                    platform.to_str().unwrap(),
                    "--host-profile",
                    profile.to_str().unwrap(),
                    "--preflight",
                    preflight.to_str().unwrap(),
                    "--output",
                    plan.to_str().unwrap(),
                ])
                .status()
                .unwrap()
                .success()
        );

        let target_marker = temp.path().join("target-work-was-invoked");
        let fake_bin = temp.path().join("fake-bin");
        fs::create_dir(&fake_bin).unwrap();
        let script = format!(
            "#!/bin/sh\nprintf target-work > '{}'\nexit 97\n",
            target_marker.display()
        );
        for program in ["dagger", "dagger-engine", "docker", "cargo", "go", "git"] {
            let path = fake_bin.join(program);
            fs::write(&path, &script).unwrap();
            fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
        }
        let current_path = env::var_os("PATH").unwrap_or_default();
        let fake_path =
            env::join_paths(std::iter::once(fake_bin).chain(env::split_paths(&current_path)))
                .unwrap();

        Self {
            _temp: temp,
            repository,
            binary,
            plan,
            bundle,
            catalog,
            closure,
            platform,
            profile,
            preflight,
            fake_path,
            target_marker,
            case_count,
        }
    }

    fn admit(
        &self,
        root: &Path,
        plan: &Path,
        closure: &Path,
        platform: &Path,
        output: &Path,
    ) -> bool {
        process(&self.binary)
            .env("PATH", &self.fake_path)
            .args([
                "facade-admit",
                "--root",
                root.to_str().unwrap(),
                "--plan",
                plan.to_str().unwrap(),
                "--bundle",
                self.bundle.to_str().unwrap(),
                "--catalog",
                self.catalog.to_str().unwrap(),
                "--closure",
                closure.to_str().unwrap(),
                "--platform",
                platform.to_str().unwrap(),
                "--host-profile",
                self.profile.to_str().unwrap(),
                "--preflight",
                self.preflight.to_str().unwrap(),
                "--output",
                output.to_str().unwrap(),
            ])
            .status()
            .unwrap()
            .success()
    }

    fn path(&self, name: &str) -> PathBuf {
        self._temp.path().join(name)
    }
}

fn mutate_json(path: &Path, output: &Path, mutate: impl FnOnce(&mut Value)) {
    let mut value: Value = decode_canonical(&fs::read(path).unwrap()).unwrap();
    mutate(&mut value);
    fs::write(output, canonical_bytes(&value).unwrap()).unwrap();
}

fn copy_policy_root(repository: &Path, output: &Path) {
    const FILES: &[&str] = &[
        "sdk/rust/completeness/artifacts/ledger.json",
        "sdk/rust/completeness/conformance-scope.json",
        "sdk/rust/completeness/conformance-applicability.json",
        "sdk/rust/completeness/conformance-assertions.json",
        "sdk/rust/completeness/conformance-fixtures.json",
        "sdk/rust/completeness/conformance-cases.json",
        "sdk/rust/completeness/conformance-scenario-candidates.json",
        "sdk/rust/completeness/conformance-scenario-realizations.json",
        "toolchains/rust-sdk-dev/testdata/scenario_conformance.rs",
    ];
    for relative in FILES {
        let destination = output.join(relative);
        fs::create_dir_all(destination.parent().unwrap()).unwrap();
        fs::copy(repository.join(relative), destination).unwrap();
    }
}

fn projection_digest(value: &Value) -> Digest {
    let mut body = value.clone();
    body.as_object_mut().unwrap().remove("projection_digest");
    canonical_digest(
        DigestDomain::ConformancePolicy,
        &("facade-admission-projection", body),
    )
    .unwrap()
}

#[test]
fn pre_target_admission_reconstructs_policy_and_rejects_every_mutated_domain() {
    static FIXTURE: OnceLock<Fixture> = OnceLock::new();
    let fixture = FIXTURE.get_or_init(Fixture::new);
    let admitted = fixture.path("facade-admission.json");
    assert!(fixture.admit(
        &fixture.repository,
        &fixture.plan,
        &fixture.closure,
        &fixture.platform,
        &admitted,
    ));
    let bytes = fs::read(&admitted).unwrap();
    let projection: Value = decode_canonical(&bytes).unwrap();
    assert_eq!(
        projection["routes"].as_array().unwrap().len(),
        fixture.case_count
    );
    assert_eq!(projection["expected_case_executions"], 74);
    assert_eq!(projection["maximum_concurrency"], 8);
    let first_route = &projection["routes"].as_array().unwrap()[0];
    for field in [
        "program",
        "fixture_digest",
        "boundary",
        "execution_selector",
        "executed",
        "timeout",
        "retry",
        "network",
        "concurrency_class",
    ] {
        assert!(first_route.get(field).is_some(), "route omitted {field}");
    }
    let standalone = projection["routes"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|route| route["program"]["program"] == "standalone-client")
        .collect::<Vec<_>>();
    assert_eq!(standalone.len(), 5);
    assert_eq!(
        standalone
            .iter()
            .filter(|route| route["executed"] == true)
            .count(),
        2,
        "the pinned-remote policy requires a separate physical execution"
    );
    let pinned_remote = standalone
        .iter()
        .find(|route| route["program"]["case"] == "pinned-remote-client")
        .unwrap();
    assert_eq!(pinned_remote["timeout"], 600_000);
    assert_eq!(pinned_remote["network"], "network/immutable-remote");
    assert_eq!(pinned_remote["retry"]["maximum_attempts"], 2);
    assert_eq!(pinned_remote["executed"], true);
    assert_eq!(
        projection["projection_digest"].as_str().unwrap(),
        projection_digest(&projection).as_str()
    );

    let unknown_plan = fixture.path("unknown-plan.json");
    mutate_json(&fixture.plan, &unknown_plan, |value| {
        value
            .as_object_mut()
            .unwrap()
            .insert("unknown_field".to_owned(), Value::Bool(true));
    });
    assert!(!fixture.admit(
        &fixture.repository,
        &unknown_plan,
        &fixture.closure,
        &fixture.platform,
        &fixture.path("unknown-output.json"),
    ));

    let closure = fixture.path("mutated-closure.json");
    mutate_json(&fixture.closure, &closure, |value| {
        let children = value["child_closures"].as_object_mut().unwrap();
        children.values_mut().next().unwrap()["closure_digest"] =
            Value::String(Digest::sha256("mutated child closure").as_str().to_owned());
    });
    assert!(!fixture.admit(
        &fixture.repository,
        &fixture.plan,
        &closure,
        &fixture.platform,
        &fixture.path("closure-output.json"),
    ));

    let platform = fixture.path("mutated-platform.json");
    mutate_json(&fixture.platform, &platform, |value| {
        let observations = value["native_observations"].as_object_mut().unwrap();
        let linux = observations.remove("linux").unwrap();
        observations.insert("windows".to_owned(), linux);
    });
    assert!(!fixture.admit(
        &fixture.repository,
        &fixture.plan,
        &fixture.closure,
        &platform,
        &fixture.path("platform-output.json"),
    ));

    let platform_job = fixture.path("mutated-platform-job.json");
    mutate_json(&fixture.platform, &platform_job, |value| {
        value["native_observations"]["linux"]["outcome"] = Value::String("failed".to_owned());
    });
    assert!(!fixture.admit(
        &fixture.repository,
        &fixture.plan,
        &fixture.closure,
        &platform_job,
        &fixture.path("platform-job-output.json"),
    ));

    let artifact_plan = fixture.path("mutated-artifact-plan.json");
    mutate_json(&fixture.plan, &artifact_plan, |value| {
        value["artifact_plan"]["materialization"]["import"]["payload_digest"] =
            Value::String(Digest::sha256("substituted artifact").as_str().to_owned());
    });
    assert!(!fixture.admit(
        &fixture.repository,
        &artifact_plan,
        &fixture.closure,
        &fixture.platform,
        &fixture.path("artifact-output.json"),
    ));

    let policy_root = fixture.path("mutated-policy-root");
    copy_policy_root(&fixture.repository, &policy_root);
    let catalog = policy_root.join("sdk/rust/completeness/conformance-cases.json");
    let mutated_catalog = fixture.path("mutated-catalog.json");
    mutate_json(&catalog, &mutated_catalog, |value| {
        value["cases"][0]["program"]["behaviour"] = Value::String("git".to_owned());
    });
    fs::copy(mutated_catalog, catalog).unwrap();
    assert!(!fixture.admit(
        &policy_root,
        &fixture.plan,
        &fixture.closure,
        &fixture.platform,
        &fixture.path("catalog-output.json"),
    ));

    let row_policy_root = fixture.path("mutated-row-policy-root");
    copy_policy_root(&fixture.repository, &row_policy_root);
    let catalog = row_policy_root.join("sdk/rust/completeness/conformance-cases.json");
    let mutated_catalog = fixture.path("mutated-catalog-row.json");
    mutate_json(&catalog, &mutated_catalog, |value| {
        let duplicate = value["cases"][1]["id"].clone();
        value["cases"][0]["id"] = duplicate;
    });
    fs::copy(mutated_catalog, catalog).unwrap();
    assert!(!fixture.admit(
        &row_policy_root,
        &fixture.plan,
        &fixture.closure,
        &fixture.platform,
        &fixture.path("catalog-row-output.json"),
    ));

    assert!(
        !fixture.target_marker.exists(),
        "pre-target admission must reject without invoking any target-capable process"
    );
}
