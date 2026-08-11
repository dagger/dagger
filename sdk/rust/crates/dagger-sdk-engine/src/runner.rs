//! End-to-end private operation runner built from pure projection and confined I/O.
//!
//! The runner validates every semantic identity before invoking the pure compiler,
//! performs closed post-work in an isolated tree, recomputes final digests, and exposes
//! output only through manifest-authorized failure-atomic publication.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;

use dagger_codegen::diagnostic::DiagnosticSet as CompilerDiagnosticSet;
use dagger_codegen::engine as compiler;
use dagger_codegen::target::CodegenTarget;
use sha2::{Digest as _, Sha256};

use crate::canonical::{DigestDomain, canonical_digest};
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::post_work::{
    Cancellation, current_allowlisted_environment, execute, require_convergence,
};
use crate::publication::{OperationCandidate, publish, verify_ownership};
use crate::root::validate_path_sets;
use crate::{
    ArtifactKind, ArtifactOwnership, ArtifactRecord, CandidateArtifact, EngineSourceDescriptor,
    ExactVersion, ExecutionResult, ExecutionResultKind, FormatVersion, GenerationMode,
    GeneratorIdentity, ModuleConfigFormat, OperationManifest, OperationRequest, OperationRoot,
    PostWorkPlan, PostWorkRecord, PublishedSdkDependency, RelativeOperationPath, Sha256Digest,
};

const TARGET_DESCRIPTOR: &[u8] = include_bytes!("../../../completeness/target.json");

/// Executes one validated operation and publishes its ownership manifest last.
pub async fn execute_operation(
    root: &OperationRoot,
    request: &OperationRequest,
    schema: &[u8],
    descriptor: &EngineSourceDescriptor,
    cancel: &Cancellation,
) -> Result<ExecutionResult, EngineDiagnostic> {
    validate_request(request, descriptor)?;
    let target = CodegenTarget::decode_exact(TARGET_DESCRIPTOR).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OperationInputInvalid,
            "request.target",
            "private compiler target descriptor is invalid",
        )
    })?;
    validate_compiler_target(request, &target)?;
    validate_schema_identity(&request.visible_schema, schema)?;

    let module = request
        .module
        .as_ref()
        .map(|module| {
            Ok(compiler::ModuleProjectionInput {
                name: module.name.to_string(),
                original_name: module.original_name.to_string(),
                source_subpath: compiler_path(&module.source_subpath)?,
                source_digest: module.source_digest.to_string(),
            })
        })
        .transpose()?;
    let output = compiler_path(&request.output_root)?;
    let dependency = compiler_dependency(&request.sdk_dependency);
    let entrypoint = request
        .entrypoint_type_defs
        .as_ref()
        .map(|input| {
            let bytes = root.read(&input.path)?;
            if digest(&bytes) != input.digest {
                return Err(diagnostic(
                    EngineDiagnosticCode::OperationInputInvalid,
                    input.path.as_str(),
                    "entrypoint TypeDef bytes differ from the request digest",
                ));
            }
            compiler::EntrypointInput::decode_checked(&bytes).map_err(|_| {
                diagnostic(
                    EngineDiagnosticCode::OperationInputInvalid,
                    input.path.as_str(),
                    "entrypoint TypeDef differs from the checked protocol input",
                )
            })
        })
        .transpose()?;
    let projected = compiler::project_operation(compiler::OperationProjectionRequest {
        target: &target,
        operation: request.operation,
        visible_schema_json: schema,
        module: module.as_ref(),
        output: &output,
        sdk_dependency: &dependency,
        entrypoint: entrypoint.as_ref(),
    })
    .map_err(compiler_diagnostic)?;
    let mut vcs_generated = projected
        .vcs_generated()
        .iter()
        .map(engine_path)
        .collect::<Result<BTreeSet<_>, _>>()?;
    let vcs_ignored = projected
        .vcs_ignored()
        .iter()
        .map(engine_path)
        .collect::<Result<BTreeSet<_>, _>>()?;

    let temporary = tempfile::tempdir().map_err(|_| generation_failed())?;
    for (path, artifact) in projected.artifacts() {
        let path = engine_path(path)?;
        let absolute = path.join_lexically(temporary.path());
        if let Some(parent) = absolute.parent() {
            fs::create_dir_all(parent).map_err(|_| generation_failed())?;
        }
        fs::write(absolute, &artifact.content).map_err(|_| generation_failed())?;
    }
    let staging_root = OperationRoot::open(temporary.path())?;
    if projected.projection_pass_limit() > 2 {
        return Err(diagnostic(
            EngineDiagnosticCode::PostWorkRejected,
            "operation.projection",
            "pure compiler requested more than two projection passes",
        ));
    }
    let mut post_work_records = Vec::new();
    for work in projected.post_work() {
        let compiler::PostWorkPlan::FormatRust { files } = work;
        let files = files
            .iter()
            .map(engine_path)
            .collect::<Result<BTreeSet<_>, _>>()?;
        let plan = PostWorkPlan::FormatRust {
            toolchain: request.target.rust_toolchain.clone(),
            files: files.clone(),
        };
        let environment = current_allowlisted_environment();
        let outcome = execute(&staging_root, &plan, &environment, cancel).await?;
        if !outcome.success {
            let coordinate = first_failing_format_path(&staging_root, &plan, &environment, cancel)
                .await?
                .unwrap_or_else(|| "post-work.rustfmt".to_owned());
            return Err(diagnostic(
                EngineDiagnosticCode::FormatFailed,
                &coordinate,
                "pinned rustfmt rejected generated Rust source",
            ));
        }
        let first_digest = digest_path_set(&staging_root, &files)?;
        let convergence = execute(&staging_root, &plan, &environment, cancel).await?;
        if !convergence.success {
            return Err(diagnostic(
                EngineDiagnosticCode::FormatFailed,
                "post-work.rustfmt",
                "pinned rustfmt rejected its convergence pass",
            ));
        }
        let second_digest = digest_path_set(&staging_root, &files)?;
        require_convergence(&[first_digest, second_digest.clone()])?;
        post_work_records.push(PostWorkRecord {
            plan,
            result_digest: second_digest,
        });
    }

    let artifacts = projected
        .artifacts()
        .iter()
        .map(|(path, artifact)| {
            let path = engine_path(path)?;
            let content = staging_root.read(&path)?;
            let kind = match artifact.kind {
                compiler::CandidateArtifactKind::RustSource => ArtifactKind::RustSource,
                compiler::CandidateArtifactKind::CargoManifest => ArtifactKind::CargoManifest,
                compiler::CandidateArtifactKind::ControlManifest => ArtifactKind::ControlManifest,
            };
            Ok((
                path,
                CandidateArtifact {
                    kind,
                    content,
                    ownership: ArtifactOwnership::Generator,
                },
            ))
        })
        .collect::<Result<BTreeMap<_, _>, EngineDiagnostic>>()?;
    let artifact_records = artifacts
        .iter()
        .map(|(path, artifact)| {
            (
                path.clone(),
                ArtifactRecord {
                    kind: artifact.kind,
                    digest: digest(&artifact.content),
                    ownership: artifact.ownership,
                },
            )
        })
        .collect();
    let manifest_path = RelativeOperationPath::parse(&format!(
        "{}/.dagger/rust/operation-manifest.json",
        request.output_root.as_str()
    ))
    .map_err(|_| generation_failed())?;
    vcs_generated.insert(manifest_path.clone());
    validate_path_sets(&vcs_generated, &vcs_ignored)?;
    let previous_bytes = root
        .exists(&manifest_path)
        .then(|| root.read(&manifest_path))
        .transpose()?;
    let previous = previous_bytes
        .as_deref()
        .map(crate::decode_canonical::<OperationManifest>)
        .transpose()
        .map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::OperationManifestStale,
                manifest_path.as_str(),
                "prior operation manifest is not canonical or is incompatible",
            )
        })?;
    let previous_paths = previous
        .as_ref()
        .map(|manifest| manifest.artifacts.keys().cloned().collect::<BTreeSet<_>>())
        .unwrap_or_default();
    let removed = previous_paths
        .difference(&artifacts.keys().cloned().collect())
        .cloned()
        .collect();
    let manifest = OperationManifest {
        format_version: crate::FormatVersion,
        operation: request.operation,
        mode: if request
            .module
            .as_ref()
            .is_some_and(|module| module.config_format == ModuleConfigFormat::Legacy)
        {
            GenerationMode::LegacyRuntimeCodegen
        } else {
            GenerationMode::CheckedGenerated
        },
        target: request.target.clone(),
        input_digest: canonical_digest(DigestDomain::OperationRequest, request)
            .map_err(|_| generation_failed())?,
        visible_schema_digest: request.visible_schema.digest.clone(),
        module_source_digest: request
            .module
            .as_ref()
            .map(|module| module.source_digest.clone()),
        sdk_dependency: request.sdk_dependency.clone(),
        output_root: request.output_root.clone(),
        artifacts: artifact_records,
        post_work: post_work_records,
        generator: GeneratorIdentity {
            version: ExactVersion::new(env!("CARGO_PKG_VERSION").to_owned())
                .expect("package version must be exact semantic version"),
            engine_source_digest: canonical_digest(DigestDomain::EngineSource, descriptor)
                .map_err(|_| generation_failed())?,
        },
    };
    let candidate = OperationCandidate {
        artifacts,
        removed,
        manifest,
        manifest_path: manifest_path.clone(),
        previous_manifest_digest: previous_bytes.as_deref().map(digest),
    };
    let publication = verify_ownership(root, previous.as_ref(), &candidate)?;
    let _published = publish(root, publication)?;
    Ok(ExecutionResult {
        format_version: FormatVersion,
        kind: ExecutionResultKind::Generation,
        output_root: request.output_root.clone(),
        operation_manifest: Some(manifest_path),
        vcs_generated,
        vcs_ignored,
    })
}

async fn first_failing_format_path(
    root: &OperationRoot,
    plan: &PostWorkPlan,
    environment: &BTreeMap<String, String>,
    cancel: &Cancellation,
) -> Result<Option<String>, EngineDiagnostic> {
    let PostWorkPlan::FormatRust { toolchain, files } = plan else {
        return Ok(None);
    };
    // Formatter stderr may contain caller-authored source. A failure-only replay of
    // one normalized generated path at a time identifies the repair coordinate
    // without forwarding process output across the private adapter boundary.
    for file in files {
        let single = PostWorkPlan::FormatRust {
            toolchain: toolchain.clone(),
            files: BTreeSet::from([file.clone()]),
        };
        if !execute(root, &single, environment, cancel).await?.success {
            return Ok(Some(file.to_string()));
        }
    }
    Ok(None)
}

// Engine operations receive the core schema plus module-scoped extensions. The pure
// compiler validates every reviewed core coordinate before rendering; this boundary
// separately proves that the mounted bytes are exactly those named by the request.
fn validate_schema_identity(
    input: &crate::SchemaInput,
    schema: &[u8],
) -> Result<(), EngineDiagnostic> {
    if digest(schema) != input.digest {
        return Err(diagnostic(
            EngineDiagnosticCode::OperationInputInvalid,
            "request.visible_schema.digest",
            "schema bytes differ from the operation request digest",
        ));
    }
    Ok(())
}

fn compiler_diagnostic(diagnostics: CompilerDiagnosticSet) -> EngineDiagnostic {
    let primary = &diagnostics.diagnostics()[0];
    EngineDiagnostic::new(
        EngineDiagnosticCode::GenerationFailed,
        Some(
            primary
                .coordinate
                .as_ref()
                .map_or("visible-schema", |coordinate| coordinate.as_str()),
        ),
        format!("{}: {}", primary.code, primary.message),
    )
}

fn validate_compiler_target(
    request: &OperationRequest,
    compiler_target: &CodegenTarget,
) -> Result<(), EngineDiagnostic> {
    let target = &request.target;
    if target.repository.as_str() != format!("https://{}", compiler_target.dagger_repository())
        || target.dagger_revision.as_str() != compiler_target.dagger_revision().as_str()
        || target.engine_version.as_str() != compiler_target.engine_version().to_string()
        || target.rust_sdk_version.as_str() != compiler_target.rust_sdk_version().to_string()
        || target.rust_toolchain.as_str() != compiler_target.rust_version().to_string()
        || target.core_schema_digest.as_str() != compiler_target.schema_digest().to_string()
    {
        return Err(diagnostic(
            EngineDiagnosticCode::OperationInputInvalid,
            "request.target",
            "operation target differs from the checked private compiler target",
        ));
    }
    Ok(())
}

/// Validates descriptor/request agreement and operation-specific input shape.
pub fn validate_request(
    request: &OperationRequest,
    descriptor: &EngineSourceDescriptor,
) -> Result<(), EngineDiagnostic> {
    descriptor.validate()?;
    let target = &request.target;
    if target.repository != descriptor.repository
        || target.dagger_revision != descriptor.dagger_revision
        || target.engine_version != descriptor.engine_version
        || target.rust_sdk_version != descriptor.rust_sdk_version
        || target.rust_toolchain != descriptor.rust_toolchain
        || target.core_schema_digest != descriptor.core_schema_digest
        || request.sdk_dependency != descriptor.sdk_dependency
    {
        return Err(diagnostic(
            EngineDiagnosticCode::OperationInputInvalid,
            "request.target",
            "operation request differs from the packaged immutable engine descriptor",
        ));
    }
    let module_required = matches!(
        request.operation,
        compiler::OperationKind::GenerateModule
            | compiler::OperationKind::GenerateClient
            | compiler::OperationKind::GenerateEntrypoint
    );
    if request.module.is_some() != module_required {
        return Err(diagnostic(
            EngineDiagnosticCode::OperationInputInvalid,
            "request.module",
            "operation has a missing or forbidden module input",
        ));
    }
    let entrypoint_required = request.operation == compiler::OperationKind::GenerateEntrypoint;
    if request.entrypoint_type_defs.is_some() != entrypoint_required {
        return Err(diagnostic(
            EngineDiagnosticCode::OperationInputInvalid,
            "request.entrypoint_type_defs",
            "operation has a missing or forbidden entrypoint TypeDef input",
        ));
    }
    Ok(())
}

fn compiler_path(
    path: &RelativeOperationPath,
) -> Result<compiler::RelativeOperationPath, EngineDiagnostic> {
    compiler::RelativeOperationPath::parse(path.as_str()).map_err(|_| generation_failed())
}

fn engine_path(
    path: &compiler::RelativeOperationPath,
) -> Result<RelativeOperationPath, EngineDiagnostic> {
    RelativeOperationPath::parse(path.as_str()).map_err(|_| generation_failed())
}

fn compiler_dependency(dependency: &PublishedSdkDependency) -> compiler::PublishedSdkDependency {
    match dependency {
        PublishedSdkDependency::Registry {
            registry,
            exact_version,
            ..
        } => compiler::PublishedSdkDependency::Registry {
            registry: registry.to_string(),
            exact_version: exact_version.to_string(),
        },
        PublishedSdkDependency::Git { url, revision, .. } => {
            compiler::PublishedSdkDependency::Git {
                url: url.to_string(),
                revision: revision.to_string(),
            }
        }
    }
}

fn digest_path_set(
    root: &OperationRoot,
    paths: &BTreeSet<RelativeOperationPath>,
) -> Result<Sha256Digest, EngineDiagnostic> {
    let mut hasher = Sha256::new();
    for path in paths {
        hasher.update(path.as_str().as_bytes());
        hasher.update([0]);
        hasher.update(root.read(path)?);
        hasher.update([0]);
    }
    format!("sha256:{:x}", hasher.finalize())
        .parse()
        .map_err(|_| generation_failed())
}

fn digest(bytes: &[u8]) -> Sha256Digest {
    format!("sha256:{:x}", Sha256::digest(bytes))
        .parse()
        .expect("SHA-256 formatting must satisfy the digest scalar")
}

fn generation_failed() -> EngineDiagnostic {
    diagnostic(
        EngineDiagnosticCode::GenerationFailed,
        "operation",
        "operation candidate could not be constructed",
    )
}

fn diagnostic(code: EngineDiagnosticCode, coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}

#[cfg(test)]
mod tests {
    use super::{digest, validate_schema_identity};
    use crate::{RelativeOperationPath, SchemaInput};

    #[test]
    fn mounted_visible_schema_is_bound_by_digest_without_requiring_core_only_bytes() {
        let schema = br#"{"__schema":{"types":[{"name":"ModuleExtension"}]}}"#;
        let input = SchemaInput {
            path: RelativeOperationPath::parse("schema.json").expect("fixture path must parse"),
            digest: digest(schema),
        };

        validate_schema_identity(&input, schema)
            .expect("named bytes must pass identity validation");
        assert!(validate_schema_identity(&input, b"{}").is_err());
    }
}
