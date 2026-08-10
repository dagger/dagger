//! Valid-first generators shared by canonical engine-model properties.
//!
//! One generated corpus contains every wire model and each nested scalar family. This
//! keeps future properties on the same valid construction path while allowing them to
//! mutate a single contract boundary after generation.

#![allow(dead_code)]

use std::collections::{BTreeMap, BTreeSet};

use dagger_sdk_engine::*;
use proptest::prelude::*;

#[derive(Clone, Debug)]
pub struct ModelCorpus {
    pub target: TargetIdentity,
    pub schema: SchemaInput,
    pub module: ModuleOperationInput,
    pub dependency: PublishedSdkDependency,
    pub request: OperationRequest,
    pub candidate: CandidateArtifact,
    pub post_work: PostWorkPlan,
    pub plan: OperationPlan,
    pub post_work_record: PostWorkRecord,
    pub generator: GeneratorIdentity,
    pub artifact_record: ArtifactRecord,
    pub manifest: OperationManifest,
    pub engine_source: EngineSourceDescriptor,
    pub cargo_package: CargoPackage,
    pub toolchain: ToolchainSelection,
    pub discovered: DiscoveredCargoProject,
    pub cargo_target: CargoTarget,
    pub runtime_project: RuntimeCargoProject,
    pub provenance_input: RuntimeProvenanceInput,
    pub provenance: RuntimeProvenance,
    pub runtime_policy: RuntimePolicy,
    pub runtime_request: RuntimeVerificationRequest,
    pub runtime_plan: RuntimeBuildPlan,
    pub asset: PackagedAsset,
    pub asset_manifest: PackagedAssetManifest,
    pub evidence: EngineEvidenceSubject,
}

pub fn model_corpus() -> impl Strategy<Value = ModelCorpus> {
    (
        any::<u8>(),
        any::<bool>(),
        0_u8..4,
        proptest::collection::vec(any::<u8>(), 0..24),
    )
        .prop_map(|(seed, use_registry, operation, content)| {
            build_corpus(seed, use_registry, operation, content)
        })
}

/// Builds one deterministic valid corpus for filesystem and identity properties.
pub fn fixed_model_corpus(seed: u8, use_registry: bool, operation: u8) -> ModelCorpus {
    build_corpus(seed, use_registry, operation, vec![seed])
}

fn build_corpus(seed: u8, use_registry: bool, operation: u8, content: Vec<u8>) -> ModelCorpus {
    let root = path(&format!("workspace-{seed}"));
    let manifest_path = path(&format!("{root}/Cargo.toml"));
    let source_path = path(&format!("{root}/src/lib.rs"));
    let output_root = path(&format!("{root}/src/dagger_generated"));
    let schema = SchemaInput {
        path: path(&format!("{root}/schema.json")),
        digest: digest(seed, 1),
    };
    let target = TargetIdentity {
        format_version: FormatVersion,
        repository: value("https://github.com/dagger/dagger"),
        dagger_revision: value(&format!("{seed:040x}")),
        engine_version: value("1.0.0-beta.10"),
        rust_sdk_version: value("1.0.0-beta.10"),
        rust_toolchain: value("1.97.1"),
        core_schema_digest: digest(seed, 2),
    };
    let module = ModuleOperationInput {
        name: value(&format!("module-{seed}")),
        original_name: value(&format!("module-{seed}")),
        source_subpath: source_path.clone(),
        config_format: if use_registry {
            ModuleConfigFormat::Current
        } else {
            ModuleConfigFormat::Legacy
        },
        source_digest: digest(seed, 3),
    };
    let dependency = if use_registry {
        PublishedSdkDependency::Registry {
            registry: value("crates-io"),
            package: value("dagger-sdk"),
            exact_version: value("1.0.0-beta.10"),
        }
    } else {
        PublishedSdkDependency::Git {
            url: value("https://github.com/dagger/dagger"),
            revision: value(&format!("{:040x}", u16::from(seed) + 1)),
            package: value("dagger-sdk"),
        }
    };
    let operation = match operation {
        0 => OperationKind::GenerateLibrary,
        1 => OperationKind::GenerateModule,
        2 => OperationKind::GenerateClient,
        _ => OperationKind::GenerateEntrypoint,
    };
    let request = OperationRequest {
        format_version: FormatVersion,
        operation,
        target: target.clone(),
        visible_schema: schema.clone(),
        module: Some(module.clone()),
        sdk_dependency: dependency.clone(),
        output_root: output_root.clone(),
        entrypoint_type_defs: (operation == OperationKind::GenerateEntrypoint).then(|| {
            SchemaInput {
                path: path(&format!("{root}/entrypoint-type-defs.json")),
                digest: digest(seed, 4),
            }
        }),
    };
    let candidate = CandidateArtifact {
        kind: ArtifactKind::RustSource,
        content,
        ownership: ArtifactOwnership::Generator,
    };
    let mut rust_files = BTreeSet::new();
    rust_files.insert(source_path.clone());
    let post_work = PostWorkPlan::FormatRust {
        toolchain: value("1.97.1"),
        files: rust_files,
    };
    let mut candidate_artifacts = BTreeMap::new();
    candidate_artifacts.insert(source_path.clone(), candidate.clone());
    let mut vcs_generated = BTreeSet::new();
    vcs_generated.insert(source_path.clone());
    let mut vcs_ignored = BTreeSet::new();
    vcs_ignored.insert(path(&format!("{root}/target")));
    let plan = OperationPlan {
        target: target.clone(),
        operation,
        visible_schema_digest: schema.digest.clone(),
        artifacts: candidate_artifacts,
        vcs_generated,
        vcs_ignored,
        post_work: vec![post_work.clone()],
        projection_pass_limit: ProjectionPassLimit::Two,
    };
    let post_work_record = PostWorkRecord {
        plan: post_work.clone(),
        result_digest: digest(seed, 5),
    };
    let generator = GeneratorIdentity {
        version: value("1.0.0-beta.10"),
        engine_source_digest: digest(seed, 6),
    };
    let artifact_record = ArtifactRecord {
        kind: ArtifactKind::RustSource,
        digest: digest(seed, 7),
        ownership: ArtifactOwnership::Generator,
    };
    let mut artifact_records = BTreeMap::new();
    artifact_records.insert(source_path.clone(), artifact_record.clone());
    let manifest = OperationManifest {
        format_version: FormatVersion,
        operation,
        mode: if use_registry {
            GenerationMode::CheckedGenerated
        } else {
            GenerationMode::LegacyRuntimeCodegen
        },
        target: target.clone(),
        input_digest: digest(seed, 8),
        visible_schema_digest: schema.digest.clone(),
        module_source_digest: Some(module.source_digest.clone()),
        sdk_dependency: dependency.clone(),
        output_root: output_root.clone(),
        artifacts: artifact_records,
        post_work: vec![post_work_record.clone()],
        generator: generator.clone(),
    };
    let engine_source = EngineSourceDescriptor {
        format_version: FormatVersion,
        repository: target.repository.clone(),
        dagger_revision: target.dagger_revision.clone(),
        engine_version: target.engine_version.clone(),
        rust_sdk_version: target.rust_sdk_version.clone(),
        rust_toolchain: target.rust_toolchain.clone(),
        sdk_dependency: dependency.clone(),
        core_schema_digest: target.core_schema_digest.clone(),
        packaged_asset_manifest_digest: digest(seed, 9),
    };
    let cargo_package = CargoPackage {
        package_id: value(&format!("cargo-package-{seed}")),
        name: value(&format!("package-{seed}")),
        manifest_path: manifest_path.clone(),
        package_root: root.clone(),
    };
    let toolchain = if use_registry {
        ToolchainSelection::Declared {
            toolchain: value("1.97.1"),
            declaration_path: path(&format!("{root}/rust-toolchain.toml")),
        }
    } else {
        ToolchainSelection::TargetDefault {
            toolchain: value("1.97.1"),
        }
    };
    let discovered = DiscoveredCargoProject {
        workspace_root: root.clone(),
        target_package: cargo_package.clone(),
        lockfile: Some(path(&format!("{root}/Cargo.lock"))),
        toolchain: toolchain.clone(),
    };
    let cargo_target = CargoTarget {
        name: value("dagger-module"),
        source_path: path(&format!("{root}/src/bin/dagger-module.rs")),
    };
    let runtime_project = RuntimeCargoProject {
        discovered: discovered.clone(),
        target_binary: cargo_target.clone(),
        lockfile: path(&format!("{root}/Cargo.lock")),
        toolchain: value("1.97.1"),
        operation_manifest_digest: digest(seed, 10),
    };
    let provenance_input = RuntimeProvenanceInput {
        format_version: FormatVersion,
        engine_source: engine_source.clone(),
        toolchain: value("1.97.1"),
        base_image_digest: digest(seed, 11),
        lockfile_digest: digest(seed, 12),
        module_source_digest: module.source_digest.clone(),
        operation_manifest_digest: runtime_project.operation_manifest_digest.clone(),
        target: value("x86_64-unknown-linux-gnu"),
        mode: if use_registry {
            RuntimeCodegenMode::CheckedGenerated
        } else {
            RuntimeCodegenMode::LegacyRuntimeCodegen
        },
    };
    let provenance = RuntimeProvenance {
        input: provenance_input.clone(),
        binary_digest: digest(seed, 13),
    };
    let runtime_policy = RuntimePolicy {
        format_version: FormatVersion,
        build_image: value(
            "rust:1.97.1-bookworm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        ),
        runtime_base_image: value(
            "gcr.io/distroless/cc-debian12:nonroot@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        ),
        runtime_base_digest: value(
            "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        ),
        linux_amd64_target: value("x86_64-unknown-linux-gnu"),
        linux_arm64_target: value("aarch64-unknown-linux-gnu"),
        cargo_target_dir: value("/var/lib/dagger/rust/target"),
        runtime_binary_path: value("/var/lib/dagger/rust/target/release/dagger-module"),
        runtime_install_path: value("/usr/local/bin/dagger-module"),
        provenance_install_path: value("/usr/local/share/dagger/rust/runtime-provenance.json"),
    };
    let runtime_request = RuntimeVerificationRequest {
        format_version: FormatVersion,
        target: target.clone(),
        module: module.clone(),
        mode: provenance_input.mode,
        operation_manifest: path(&format!("{root}/.dagger/rust/operation-manifest.json")),
        base_image_digest: runtime_policy.runtime_base_digest.clone(),
        rust_target: provenance_input.target.clone(),
    };
    let runtime_plan = RuntimeBuildPlan {
        format_version: FormatVersion,
        project: runtime_project.clone(),
        mode: provenance_input.mode,
        manifest: manifest.clone(),
        cargo_args: dagger_sdk_engine::runtime::runtime_cargo_arguments(
            &runtime_project,
            &provenance_input.target,
        ),
        binary_relative_path: path("release/dagger-module"),
        provenance_input: provenance_input.clone(),
    };
    let asset = PackagedAsset {
        path: path("usr/local/bin/dagger-rust-engine"),
        digest: digest(seed, 14),
        executable: true,
    };
    let mut assets = BTreeMap::new();
    assets.insert(asset.path.clone(), asset.clone());
    let asset_manifest = PackagedAssetManifest {
        format_version: FormatVersion,
        assets,
    };
    let mut operation_input_digests = BTreeSet::new();
    operation_input_digests.insert(manifest.input_digest.clone());
    let mut operation_manifest_digests = BTreeSet::new();
    operation_manifest_digests.insert(runtime_project.operation_manifest_digest.clone());
    let evidence = EngineEvidenceSubject {
        target: target.clone(),
        engine_source_digest: generator.engine_source_digest.clone(),
        packaged_assets_digest: engine_source.packaged_asset_manifest_digest.clone(),
        sdk_dependency: dependency.clone(),
        rust_toolchain: value("1.97.1"),
        operation_input_digests,
        operation_manifest_digests,
    };

    ModelCorpus {
        target,
        schema,
        module,
        dependency,
        request,
        candidate,
        post_work,
        plan,
        post_work_record,
        generator,
        artifact_record,
        manifest,
        engine_source,
        cargo_package,
        toolchain,
        discovered,
        cargo_target,
        runtime_project,
        provenance_input,
        provenance,
        runtime_policy,
        runtime_request,
        runtime_plan,
        asset,
        asset_manifest,
        evidence,
    }
}

fn digest(seed: u8, domain: u8) -> Sha256Digest {
    value(&format!(
        "sha256:{:064x}",
        (u16::from(seed) << 8) | u16::from(domain)
    ))
}

fn path(value: &str) -> RelativeOperationPath {
    RelativeOperationPath::parse(value).unwrap()
}

fn value<T: std::str::FromStr>(value: &str) -> T
where
    T::Err: std::fmt::Debug,
{
    value.parse().unwrap()
}
