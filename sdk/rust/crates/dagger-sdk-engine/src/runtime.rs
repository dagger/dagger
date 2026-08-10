//! Reproducible runtime verification and post-build provenance finalization.
//!
//! This module promotes a discovered Cargo project only after committed generation,
//! the selected binary target, exact toolchain, and locked dependency graph agree.
//! It emits a data-only build plan for the Go adapter and hashes the final stripped
//! binary without accepting caller-controlled commands, targets, or provenance edits.

use std::collections::BTreeSet;
use std::fs;
use std::path::Path;

use sha2::{Digest as _, Sha256};

use crate::canonical::decode_canonical;
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::post_work::{Cancellation, CommandSpec, current_allowlisted_environment, execute_fixed};
use crate::project::toolchain::ToolchainDeclaration;
use crate::project::{discover_project, promote_runtime_project, toolchain_declarations};
use crate::{
    ArtifactOwnership, CargoTarget, EngineSourceDescriptor, ExactRustToolchain, FormatVersion,
    GenerationMode, OperationManifest, OperationRoot, RelativeOperationPath, RuntimeBuildPlan,
    RuntimeCodegenMode, RuntimePolicy, RuntimeProvenance, RuntimeProvenanceInput,
    RuntimeVerificationRequest, Sha256Digest,
};

const CARGO: &str = "/usr/local/cargo/bin/cargo";
const FIXED_CARGO_TARGET_DIR: &str = "/var/lib/dagger/rust/target";
const FIXED_RUNTIME_BINARY_PATH: &str = "/var/lib/dagger/rust/target/release/dagger-module";
const FIXED_RUNTIME_INSTALL_PATH: &str = "/usr/local/bin/dagger-module";
const FIXED_PROVENANCE_INSTALL_PATH: &str = "/usr/local/share/dagger/rust/runtime-provenance.json";
const MAX_RUNTIME_BINARY_BYTES: u64 = 256 * 1024 * 1024;

/// Validates the immutable build/runtime image and path policy packaged with the SDK.
pub fn validate_runtime_policy(policy: &RuntimePolicy) -> Result<(), EngineDiagnostic> {
    if !policy.build_image.as_str().contains("@sha256:")
        || !policy.runtime_base_image.as_str().contains("@sha256:")
    {
        return Err(runtime_error(
            EngineDiagnosticCode::SdkManifestInvalid,
            "runtime-policy.image",
            "runtime images must use complete immutable SHA-256 references",
        ));
    }
    let runtime_digest = policy
        .runtime_base_image
        .as_str()
        .rsplit_once('@')
        .map(|(_, digest)| digest);
    if runtime_digest != Some(policy.runtime_base_digest.as_str()) {
        return Err(runtime_error(
            EngineDiagnosticCode::SdkManifestInvalid,
            "runtime-policy.runtime-base-image",
            "runtime base reference and recorded digest differ",
        ));
    }
    for (coordinate, observed, expected) in [
        (
            "runtime-policy.cargo-target-dir",
            policy.cargo_target_dir.as_str(),
            FIXED_CARGO_TARGET_DIR,
        ),
        (
            "runtime-policy.runtime-binary-path",
            policy.runtime_binary_path.as_str(),
            FIXED_RUNTIME_BINARY_PATH,
        ),
        (
            "runtime-policy.runtime-install-path",
            policy.runtime_install_path.as_str(),
            FIXED_RUNTIME_INSTALL_PATH,
        ),
        (
            "runtime-policy.provenance-install-path",
            policy.provenance_install_path.as_str(),
            FIXED_PROVENANCE_INSTALL_PATH,
        ),
    ] {
        if observed != expected {
            return Err(runtime_error(
                EngineDiagnosticCode::SdkManifestInvalid,
                coordinate,
                "runtime path differs from the closed adapter contract",
            ));
        }
    }
    Ok(())
}

/// Verifies committed generation and returns the sole admitted Cargo build plan.
pub async fn verify_runtime(
    root: &OperationRoot,
    request: &RuntimeVerificationRequest,
    descriptor: &EngineSourceDescriptor,
    policy: &RuntimePolicy,
    cancel: &Cancellation,
) -> Result<RuntimeBuildPlan, EngineDiagnostic> {
    descriptor.validate()?;
    validate_runtime_policy(policy)?;
    validate_request_target(request, descriptor, policy)?;

    let manifest_bytes = root.read(&request.operation_manifest).map_err(|_| {
        runtime_error(
            EngineDiagnosticCode::GeneratedMissing,
            request.operation_manifest.as_str(),
            "generated ownership manifest is missing; run `dagger generate`",
        )
    })?;
    let manifest: OperationManifest = decode_canonical(&manifest_bytes).map_err(|_| {
        runtime_error(
            EngineDiagnosticCode::GeneratedStale,
            request.operation_manifest.as_str(),
            "generated ownership manifest is invalid; run `dagger generate`",
        )
    })?;
    validate_operation_manifest(request, descriptor, &manifest)?;
    verify_generated_artifacts(root, &manifest)?;

    let manifest_path = join(&request.module.source_subpath, "Cargo.toml")?;
    let declarations = toolchain_declarations(root, &request.module.source_subpath)?;
    let borrowed = declarations
        .iter()
        .map(|(path, bytes)| ToolchainDeclaration {
            path,
            bytes: bytes.as_slice(),
        })
        .collect::<Vec<_>>();
    let discovered = discover_project(
        root,
        &request.module.source_subpath,
        &manifest_path,
        &borrowed,
        cancel,
    )
    .await?;
    let lockfile = discovered.lockfile.clone().ok_or_else(|| {
        runtime_error(
            EngineDiagnosticCode::LockfileMissing,
            "Cargo.lock",
            "checked runtime requires Cargo.lock; run `dagger generate`",
        )
    })?;
    let toolchain = selected_toolchain(&discovered.toolchain).clone();
    if toolchain != request.target.rust_toolchain {
        return Err(runtime_error(
            EngineDiagnosticCode::ToolchainNonReproducible,
            "runtime.toolchain",
            "selected toolchain differs from the immutable engine target",
        ));
    }

    let locked = execute_fixed(
        root,
        &CommandSpec {
            executable: CARGO,
            arguments: crate::project::metadata_arguments(&manifest_path, true),
        },
        &current_allowlisted_environment(),
        cancel,
        EngineDiagnosticCode::LockfileStale,
        "Cargo.lock",
    )
    .await?;
    if !locked.success {
        return Err(runtime_error(
            EngineDiagnosticCode::LockfileStale,
            lockfile.as_str(),
            "Cargo.lock is stale for the selected manifest; run `dagger generate`",
        ));
    }

    let target_source = join(&request.module.source_subpath, "src/bin/dagger-module.rs")?;
    let target = CargoTarget {
        name: "dagger-module"
            .parse()
            .expect("fixed runtime target name is canonical"),
        source_path: target_source,
    };
    let project = promote_runtime_project(root, discovered, target, toolchain.clone(), &manifest)
        .map_err(|error| match error.code {
        EngineDiagnosticCode::CargoManifestMissing => runtime_error(
            EngineDiagnosticCode::LockfileMissing,
            "Cargo.lock",
            "checked runtime requires Cargo.lock; run `dagger generate`",
        ),
        EngineDiagnosticCode::OwnershipConflict => runtime_error(
            EngineDiagnosticCode::RuntimeTargetInvalid,
            "runtime.target.dagger-module",
            "generated dagger-module target is absent or not manifest-owned",
        ),
        _ => error,
    })?;
    let lockfile_digest = bytes_digest(&root.read(&project.lockfile)?);
    let operation_manifest_digest = project.operation_manifest_digest.clone();
    let cargo_args = runtime_cargo_arguments(&project, &request.rust_target);
    let provenance_input = RuntimeProvenanceInput {
        format_version: FormatVersion,
        engine_source: descriptor.clone(),
        toolchain,
        base_image_digest: request.base_image_digest.clone(),
        lockfile_digest,
        module_source_digest: request.module.source_digest.clone(),
        operation_manifest_digest,
        target: request.rust_target.clone(),
        mode: request.mode,
    };
    Ok(RuntimeBuildPlan {
        format_version: FormatVersion,
        project,
        mode: request.mode,
        manifest,
        cargo_args,
        binary_relative_path: RelativeOperationPath::parse("release/dagger-module")
            .expect("fixed runtime binary path is canonical"),
        provenance_input,
    })
}

/// Completes runtime provenance only after the fixed post-strip binary exists.
pub fn finalize_runtime(
    plan: &RuntimeBuildPlan,
    binary: &Path,
    policy: &RuntimePolicy,
) -> Result<RuntimeProvenance, EngineDiagnostic> {
    validate_runtime_policy(policy)?;
    if plan.format_version != FormatVersion
        || plan.binary_relative_path.as_str() != "release/dagger-module"
        || plan.cargo_args != runtime_cargo_arguments(&plan.project, &plan.provenance_input.target)
        || binary != Path::new(policy.runtime_binary_path.as_str())
    {
        return Err(runtime_error(
            EngineDiagnosticCode::RuntimeTargetInvalid,
            "runtime.binary",
            "runtime plan does not select the fixed engine-owned binary",
        ));
    }
    let metadata = fs::symlink_metadata(binary).map_err(|_| {
        runtime_error(
            EngineDiagnosticCode::RuntimeTargetInvalid,
            "runtime.binary",
            "compiled runtime binary is missing",
        )
    })?;
    if metadata.file_type().is_symlink()
        || !metadata.file_type().is_file()
        || metadata.len() > MAX_RUNTIME_BINARY_BYTES
    {
        return Err(runtime_error(
            EngineDiagnosticCode::RuntimeTargetInvalid,
            "runtime.binary",
            "compiled runtime binary must be a bounded regular non-symlink file",
        ));
    }
    let bytes = fs::read(binary).map_err(|_| {
        runtime_error(
            EngineDiagnosticCode::RuntimeTargetInvalid,
            "runtime.binary",
            "compiled runtime binary could not be read",
        )
    })?;
    Ok(RuntimeProvenance {
        input: plan.provenance_input.clone(),
        binary_digest: bytes_digest(&bytes),
    })
}

/// Produces bounded diagnostic text that cannot retain caller-provided secret bytes.
#[must_use]
pub fn redact_runtime_output(output: &[u8], secrets: &[&[u8]]) -> Vec<u8> {
    const MAX: usize = 256 * 1024;
    let retained = &output[..output.len().min(MAX)];
    let contains_secret = secrets
        .iter()
        .filter(|secret| !secret.is_empty())
        .any(|secret| {
            retained
                .windows(secret.len())
                .any(|window| window == *secret)
        });
    let text = String::from_utf8_lossy(retained);
    let contains_marker = [
        "https://",
        "http://",
        "Authorization:",
        "Bearer ",
        "token=",
        "DAGGER_SESSION_TOKEN",
        "CARGO_REGISTRIES_",
    ]
    .iter()
    .any(|marker| text.contains(marker));
    if contains_secret || contains_marker {
        b"[REDACTED]".to_vec()
    } else {
        retained.to_vec()
    }
}

/// Returns whether adapter-added runtime files and mounts stay on their declared sides.
#[must_use]
pub fn runtime_boundary_is_clean(
    final_paths: &BTreeSet<String>,
    final_mounts: &BTreeSet<String>,
    policy: &RuntimePolicy,
) -> bool {
    let expected = BTreeSet::from([
        policy.runtime_install_path.to_string(),
        policy.provenance_install_path.to_string(),
    ]);
    final_paths == &expected && final_mounts.is_empty()
}

/// Selects the runtime generation state from the presence of the legacy schema input.
#[must_use]
pub const fn runtime_codegen_mode(schema_present: bool) -> RuntimeCodegenMode {
    if schema_present {
        RuntimeCodegenMode::LegacyRuntimeCodegen
    } else {
        RuntimeCodegenMode::CheckedGenerated
    }
}

fn validate_request_target(
    request: &RuntimeVerificationRequest,
    descriptor: &EngineSourceDescriptor,
    policy: &RuntimePolicy,
) -> Result<(), EngineDiagnostic> {
    let target = &request.target;
    if target.repository != descriptor.repository
        || target.dagger_revision != descriptor.dagger_revision
        || target.engine_version != descriptor.engine_version
        || target.rust_sdk_version != descriptor.rust_sdk_version
        || target.rust_toolchain != descriptor.rust_toolchain
        || target.core_schema_digest != descriptor.core_schema_digest
        || request.base_image_digest != policy.runtime_base_digest
        || (request.rust_target != policy.linux_amd64_target
            && request.rust_target != policy.linux_arm64_target)
    {
        return Err(runtime_error(
            EngineDiagnosticCode::OperationInputInvalid,
            "runtime.target",
            "runtime request differs from packaged immutable target policy",
        ));
    }
    Ok(())
}

fn validate_operation_manifest(
    request: &RuntimeVerificationRequest,
    descriptor: &EngineSourceDescriptor,
    manifest: &OperationManifest,
) -> Result<(), EngineDiagnostic> {
    let expected_mode = match request.mode {
        RuntimeCodegenMode::CheckedGenerated => GenerationMode::CheckedGenerated,
        RuntimeCodegenMode::LegacyRuntimeCodegen => GenerationMode::LegacyRuntimeCodegen,
    };
    for (matches, coordinate) in [
        (manifest.mode == expected_mode, "runtime.manifest.mode"),
        (manifest.target == request.target, "runtime.manifest.target"),
        (
            manifest.sdk_dependency == descriptor.sdk_dependency,
            "runtime.manifest.sdk-dependency",
        ),
        (
            manifest.module_source_digest.as_ref() == Some(&request.module.source_digest),
            "runtime.manifest.module-source-digest",
        ),
        (
            manifest.output_root == request.module.source_subpath,
            "runtime.manifest.output-root",
        ),
    ] {
        if !matches {
            return Err(runtime_error(
                EngineDiagnosticCode::GeneratedStale,
                coordinate,
                "generated ownership manifest differs from runtime inputs; run `dagger generate`",
            ));
        }
    }
    Ok(())
}

fn verify_generated_artifacts(
    root: &OperationRoot,
    manifest: &OperationManifest,
) -> Result<(), EngineDiagnostic> {
    for (path, record) in &manifest.artifacts {
        if record.ownership != ArtifactOwnership::Generator {
            return Err(runtime_error(
                EngineDiagnosticCode::GeneratedStale,
                path.as_str(),
                "generated ownership record is invalid; run `dagger generate`",
            ));
        }
        let bytes = root.read(path).map_err(|_| {
            runtime_error(
                EngineDiagnosticCode::GeneratedMissing,
                path.as_str(),
                "generated artifact is missing; run `dagger generate`",
            )
        })?;
        if bytes_digest(&bytes) != record.digest {
            return Err(runtime_error(
                EngineDiagnosticCode::GeneratedStale,
                path.as_str(),
                "generated artifact is stale; run `dagger generate`",
            ));
        }
    }
    Ok(())
}

/// Returns the complete Cargo vector owned by runtime verification.
#[must_use]
pub fn runtime_cargo_arguments(
    project: &crate::RuntimeCargoProject,
    target: &crate::RustTarget,
) -> Vec<String> {
    vec![
        "build".to_owned(),
        "--manifest-path".to_owned(),
        project.discovered.target_package.manifest_path.to_string(),
        "--package".to_owned(),
        project.discovered.target_package.name.to_string(),
        "--bin".to_owned(),
        "dagger-module".to_owned(),
        "--release".to_owned(),
        "--locked".to_owned(),
        "--target".to_owned(),
        target.to_string(),
        "--target-dir".to_owned(),
        FIXED_CARGO_TARGET_DIR.to_owned(),
    ]
}

fn selected_toolchain(selection: &crate::ToolchainSelection) -> &ExactRustToolchain {
    match selection {
        crate::ToolchainSelection::Declared { toolchain, .. }
        | crate::ToolchainSelection::TargetDefault { toolchain } => toolchain,
    }
}

fn join(
    root: &RelativeOperationPath,
    suffix: &str,
) -> Result<RelativeOperationPath, EngineDiagnostic> {
    RelativeOperationPath::parse(&format!("{}/{suffix}", root.as_str())).map_err(|_| {
        runtime_error(
            EngineDiagnosticCode::OutputPathEscape,
            root.as_str(),
            "runtime path is not canonical",
        )
    })
}

fn bytes_digest(bytes: &[u8]) -> Sha256Digest {
    format!("sha256:{:x}", Sha256::digest(bytes))
        .parse()
        .expect("SHA-256 formatting must satisfy the digest scalar")
}

fn runtime_error(code: EngineDiagnosticCode, coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}
