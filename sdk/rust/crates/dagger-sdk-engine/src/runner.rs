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
use crate::client::project::{
    ClientDocumentationState, ClientProjectIdentityRequest, ClientProjectRequest,
    discover_client_project, reconcile_client_project, select_client_project_identity,
};
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::post_work::{
    Cancellation, current_allowlisted_environment, execute, require_convergence,
};
use crate::project::source_snapshot::{SourceSnapshotBuilder, SourceSnapshotRequest};
use crate::publication::{OperationCandidate, publish, verify_ownership};
use crate::root::validate_path_sets;
use crate::{
    AmendmentRecord, ArtifactKind, ArtifactOwnership, ArtifactRecord, CandidateArtifact,
    ClientManifestRecord, ClientNamespaceRecord, EngineSourceDescriptor, ExactVersion,
    ExecutionResult, ExecutionResultKind, FormatVersion, GenerationMode, GeneratorIdentity,
    ModuleConfigFormat, OperationManifest, OperationRequest, OperationRoot, PostWorkPlan,
    PostWorkRecord, PublishedSdkDependency, RelativeOperationPath, Sha256Digest, StableCoordinate,
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

    let client_snapshot = if request.operation == compiler::OperationKind::GenerateClient {
        Some(discover_client_project(root, &request.output_root)?)
    } else {
        None
    };
    let client_identity = client_snapshot
        .as_ref()
        .map(|snapshot| {
            let module = request.module.as_ref().ok_or_else(|| {
                diagnostic(
                    EngineDiagnosticCode::OperationInputInvalid,
                    "request.module",
                    "standalone-client generation requires one bound module",
                )
            })?;
            select_client_project_identity(ClientProjectIdentityRequest {
                existing_package_name: snapshot.package_name.as_deref(),
                client_root: &request.output_root,
                bound_module_name: &module.name,
            })
        })
        .transpose()?;

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
    let authoring = if matches!(
        request.operation,
        compiler::OperationKind::GenerateModule | compiler::OperationKind::GenerateEntrypoint
    ) {
        Some(module_authoring_input(root, request, descriptor)?)
    } else {
        None
    };
    let projection_request = compiler::OperationProjectionRequest {
        target: &target,
        operation: request.operation,
        visible_schema_json: schema,
        module: module.as_ref(),
        output: &output,
        sdk_dependency: &dependency,
        authoring: authoring.as_ref(),
    };
    let projected = if let Some(identity) = &client_identity {
        compiler::project_operation_with(
            &compiler::ProductionRenderers::for_client(
                dagger_codegen::client::ClientProjectIdentity {
                    package_name: identity.package_name.clone(),
                    crate_name: identity.crate_name.clone(),
                },
                compiler_path(
                    &client_snapshot
                        .as_ref()
                        .expect("client identity requires a discovered project")
                        .generated_client_root,
                )?,
            ),
            projection_request,
        )
    } else {
        compiler::project_operation(projection_request)
    }
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
    let mut project_plan = client_snapshot
        .as_ref()
        .zip(client_identity.as_ref())
        .map(|(snapshot, identity)| {
            reconcile_client_project(
                snapshot,
                &ClientProjectRequest {
                    identity: identity.clone(),
                    sdk_dependency: request.sdk_dependency.clone(),
                    documentation: ClientDocumentationState::Generated,
                },
            )
        })
        .transpose()?;
    let retained_previous_artifacts = match (previous.as_ref(), project_plan.as_mut()) {
        (Some(previous), Some(project)) if previous.client.is_none() => {
            let retained = authenticate_hook_baseline(previous, root, request, project)?;
            migrate_hook_baseline_project(project, &request.output_root)?;
            retained
        }
        _ => BTreeSet::new(),
    };
    let previous_paths = previous
        .as_ref()
        .map(|manifest| manifest.artifacts.keys().cloned().collect::<BTreeSet<_>>())
        .unwrap_or_default();
    let removed = previous_paths
        .difference(&artifacts.keys().cloned().collect())
        .filter(|path| !retained_previous_artifacts.contains(*path))
        .cloned()
        .collect();
    let amendments = project_plan.as_ref().map_or_else(BTreeMap::new, |project| {
        project
            .amendments
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
    });
    let client = projected
        .client_render()
        .map(|rendered| client_manifest_record(request, rendered))
        .transpose()?;
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
        amendments,
        client,
    };
    let candidate = OperationCandidate {
        artifacts,
        amendments: project_plan
            .as_ref()
            .map_or_else(BTreeMap::new, |project| project.amendments.clone()),
        created_files: project_plan
            .as_ref()
            .map_or_else(BTreeMap::new, |project| project.created_files.clone()),
        retained_previous_artifacts,
        removed,
        manifest,
        manifest_path: manifest_path.clone(),
        previous_manifest_digest: previous_bytes.as_deref().map(digest),
    };
    let publication = verify_ownership(root, previous.as_ref(), &candidate)?;
    let published = publish(root, publication)?;
    Ok(ExecutionResult {
        format_version: FormatVersion,
        kind: ExecutionResultKind::Generation,
        output_root: request.output_root.clone(),
        touched_paths: published
            .changes
            .iter()
            .map(|change| change.path.clone())
            .chain(std::iter::once(manifest_path.clone()))
            .collect(),
        operation_manifest: Some(manifest_path),
        vcs_generated,
        vcs_ignored,
    })
}

fn client_manifest_record(
    request: &OperationRequest,
    rendered: &compiler::ClientRenderIdentity,
) -> Result<ClientManifestRecord, EngineDiagnostic> {
    let module = request.module.as_ref().ok_or_else(generation_failed)?;
    let namespace = match (
        &rendered.namespace,
        rendered.module_root_wire_name.as_deref(),
    ) {
        (Some(namespace), Some(root)) => Some(ClientNamespaceRecord {
            module_root_wire_name: stable(root, "client.module-root")?,
            namespace: namespace.namespace.clone(),
            extension_trait_path: stable(
                &format!(
                    "dagger_client::{}::{}",
                    namespace.namespace, namespace.extension_trait
                ),
                "client.extension-trait",
            )?,
        }),
        (None, None) => None,
        _ => {
            return Err(diagnostic(
                EngineDiagnosticCode::GenerationFailed,
                "client.namespace",
                "client renderer returned an incomplete namespace identity",
            ));
        }
    };
    Ok(ClientManifestRecord {
        module: module.into(),
        package: crate::ClientProjectIdentity {
            package_name: rendered.project.package_name.clone(),
            crate_name: rendered.project.crate_name.clone(),
        },
        namespace,
        binding_catalog_digest: rendered
            .binding_catalog_digest
            .parse()
            .map_err(|_| generation_failed())?,
        binding_count: rendered.binding_count,
    })
}

fn authenticate_hook_baseline(
    previous: &OperationManifest,
    root: &OperationRoot,
    request: &OperationRequest,
    project: &crate::ClientProjectPlan,
) -> Result<BTreeSet<RelativeOperationPath>, EngineDiagnostic> {
    if previous.operation != compiler::OperationKind::GenerateClient
        || previous.target != request.target
        || previous.visible_schema_digest != request.visible_schema.digest
        || previous.module_source_digest
            != request
                .module
                .as_ref()
                .map(|module| module.source_digest.clone())
        || previous.sdk_dependency != request.sdk_dependency
        || previous.output_root != request.output_root
        || previous.input_digest
            != canonical_digest(DigestDomain::OperationRequest, request)
                .map_err(|_| generation_failed())?
        || !previous.amendments.is_empty()
    {
        return Err(stale_baseline("operation-manifest"));
    }
    let cargo = child_path(&request.output_root, "Cargo.toml")?;
    let library = child_path(&request.output_root, "src/lib.rs")?;
    if !previous.artifacts.contains_key(&cargo)
        || !previous.artifacts.contains_key(&library)
        || previous.artifacts.keys().any(|path| {
            path != &cargo
                && path != &library
                && !path
                    .as_str()
                    .starts_with(&format!("{}/src/dagger_generated/", request.output_root))
        })
    {
        return Err(stale_baseline("operation-manifest.artifacts"));
    }
    let cargo_bytes = root.read(&cargo)?;
    let cargo_source =
        std::str::from_utf8(&cargo_bytes).map_err(|_| stale_baseline(cargo.as_str()))?;
    let cargo_document = cargo_source
        .parse::<toml_edit::DocumentMut>()
        .map_err(|_| stale_baseline(cargo.as_str()))?;
    let metadata = cargo_document
        .get("package")
        .and_then(toml_edit::Item::as_table)
        .and_then(|package| package.get("metadata"))
        .and_then(toml_edit::Item::as_table)
        .and_then(|metadata| metadata.get("dagger"))
        .and_then(toml_edit::Item::as_table)
        .ok_or_else(|| stale_baseline(cargo.as_str()))?;
    if metadata
        .get("content-domain")
        .and_then(toml_edit::Item::as_str)
        != Some("engine-hook-baseline")
        || metadata
            .get("visible-schema-digest")
            .and_then(toml_edit::Item::as_str)
            != Some(request.visible_schema.digest.as_str())
        || metadata
            .get("module-source-digest")
            .and_then(toml_edit::Item::as_str)
            != request
                .module
                .as_ref()
                .map(|module| module.source_digest.as_str())
    {
        return Err(stale_baseline(cargo.as_str()));
    }
    let library_bytes = root.read(&library)?;
    let library_source =
        std::str::from_utf8(&library_bytes).map_err(|_| stale_baseline(library.as_str()))?;
    if !library_source.contains("pub mod dagger_generated;")
        || !library_source.contains("pub use dagger_generated::*;")
    {
        return Err(stale_baseline(library.as_str()));
    }
    let retained = BTreeSet::from([cargo, library]);
    if !retained.iter().all(|path| {
        project
            .amendments
            .keys()
            .any(|coordinate| coordinate.file() == path)
    }) {
        return Err(stale_baseline("operation-manifest.migration"));
    }
    Ok(retained)
}

fn migrate_hook_baseline_project(
    project: &mut crate::ClientProjectPlan,
    client_root: &RelativeOperationPath,
) -> Result<(), EngineDiagnostic> {
    let cargo_path = child_path(client_root, "Cargo.toml")?;
    let library_path = child_path(client_root, "src/lib.rs")?;
    let cargo_bytes = project
        .amendments
        .iter()
        .find(|(coordinate, _)| coordinate.file() == &cargo_path)
        .map(|(_, amendment)| amendment.complete_file_bytes.clone())
        .ok_or_else(|| stale_baseline(cargo_path.as_str()))?;
    let cargo_source =
        std::str::from_utf8(&cargo_bytes).map_err(|_| stale_baseline(cargo_path.as_str()))?;
    let mut cargo = cargo_source
        .parse::<toml_edit::DocumentMut>()
        .map_err(|_| stale_baseline(cargo_path.as_str()))?;
    let package = cargo
        .get_mut("package")
        .and_then(toml_edit::Item::as_table_mut)
        .ok_or_else(|| stale_baseline(cargo_path.as_str()))?;
    let metadata = package
        .get_mut("metadata")
        .and_then(toml_edit::Item::as_table_mut)
        .ok_or_else(|| stale_baseline(cargo_path.as_str()))?;
    if metadata.remove("dagger").is_none() {
        return Err(stale_baseline(cargo_path.as_str()));
    }
    if metadata.is_empty() {
        package.remove("metadata");
    }
    let cargo_bytes = cargo.to_string().into_bytes();
    let library_bytes =
        b"//! Standalone Dagger client library.\n\npub mod dagger_client;\n".to_vec();
    replace_amended_file(project, &cargo_path, cargo_bytes)?;
    replace_amended_file(project, &library_path, library_bytes)
}

fn replace_amended_file(
    project: &mut crate::ClientProjectPlan,
    path: &RelativeOperationPath,
    bytes: Vec<u8>,
) -> Result<(), EngineDiagnostic> {
    let mut found = false;
    for (coordinate, amendment) in &mut project.amendments {
        if coordinate.file() == path {
            amendment.next_semantic_digest = crate::semantic_amendment_digest(
                amendment.kind,
                coordinate.semantic_key(),
                &bytes,
            )?;
            amendment.complete_file_bytes.clone_from(&bytes);
            found = true;
        }
    }
    if found {
        Ok(())
    } else {
        Err(stale_baseline(path.as_str()))
    }
}

fn stable(value: &str, coordinate: &str) -> Result<StableCoordinate, EngineDiagnostic> {
    value.parse().map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::GenerationFailed,
            coordinate,
            "client renderer returned an invalid stable identity",
        )
    })
}

fn stale_baseline(coordinate: &str) -> EngineDiagnostic {
    diagnostic(
        EngineDiagnosticCode::OperationManifestStale,
        coordinate,
        "prior standalone-client baseline cannot be authenticated for migration",
    )
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

fn module_authoring_input(
    root: &OperationRoot,
    request: &OperationRequest,
    descriptor: &EngineSourceDescriptor,
) -> Result<compiler::ModuleAuthoringInput, EngineDiagnostic> {
    let module = request.module.as_ref().ok_or_else(|| {
        diagnostic(
            EngineDiagnosticCode::OperationInputInvalid,
            "request.module",
            "module authoring requires one selected module source",
        )
    })?;
    let manifest_path = child_path(&module.source_subpath, "Cargo.toml")?;
    let manifest_bytes = root.read(&manifest_path)?;
    let manifest_text = std::str::from_utf8(&manifest_bytes).map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestInvalid,
            manifest_path.as_str(),
            "selected Cargo manifest is not UTF-8",
        )
    })?;
    let manifest = manifest_text
        .parse::<toml_edit::DocumentMut>()
        .map_err(|_| {
            diagnostic(
                EngineDiagnosticCode::CargoManifestInvalid,
                manifest_path.as_str(),
                "selected Cargo manifest is invalid TOML",
            )
        })?;
    let package = manifest
        .get("package")
        .and_then(toml_edit::Item::as_table_like)
        .ok_or_else(|| {
            diagnostic(
                EngineDiagnosticCode::CargoManifestInvalid,
                manifest_path.as_str(),
                "selected Cargo manifest has no package table",
            )
        })?;
    let package_name = package
        .get("name")
        .and_then(toml_edit::Item::as_str)
        .ok_or_else(|| {
            diagnostic(
                EngineDiagnosticCode::CargoManifestInvalid,
                manifest_path.as_str(),
                "selected Cargo package has no name",
            )
        })?;
    let edition = package
        .get("edition")
        .and_then(toml_edit::Item::as_str)
        .ok_or_else(|| {
            diagnostic(
                EngineDiagnosticCode::CargoManifestInvalid,
                manifest_path.as_str(),
                "selected Cargo package has no explicit edition",
            )
        })?;
    let crate_root_suffix = manifest
        .get("lib")
        .and_then(toml_edit::Item::as_table_like)
        .and_then(|library| library.get("path"))
        .and_then(toml_edit::Item::as_str)
        .unwrap_or("src/lib.rs");
    let crate_root = child_path(&module.source_subpath, crate_root_suffix)?;
    let sdk_dependency_alias = sdk_dependency_alias(&manifest).ok_or_else(|| {
        diagnostic(
            EngineDiagnosticCode::CargoManifestInvalid,
            manifest_path.as_str(),
            "selected Cargo package has no dagger-sdk dependency",
        )
    })?;
    let cargo_package = crate::CargoPackage {
        package_id: crate::StableCoordinate::new(format!(
            "{package_name}@{}",
            module.source_subpath.as_str()
        ))
        .map_err(|_| generation_failed())?,
        name: crate::StableCoordinate::new(package_name).map_err(|_| generation_failed())?,
        manifest_path,
        package_root: module.source_subpath.clone(),
    };
    let source = SourceSnapshotBuilder::new().build(
        root,
        SourceSnapshotRequest {
            package: &cargo_package,
            crate_root: &crate_root,
            edition,
            // The absence of selected cfg values is itself explicit; build-script and
            // ambient host cfgs cannot silently enter authoring semantics.
            cfg: dagger_codegen::module::CfgEnvironment {
                values: BTreeMap::new(),
                features: BTreeSet::new(),
            },
        },
    )?;
    let generator_digest = dagger_codegen::module::Sha256Digest::new(
        descriptor.packaged_asset_manifest_digest.as_str(),
    )
    .map_err(|_| generation_failed())?;
    Ok(compiler::ModuleAuthoringInput {
        source,
        generator_digest,
        sdk_dependency_alias,
    })
}

fn child_path(
    root: &RelativeOperationPath,
    suffix: &str,
) -> Result<RelativeOperationPath, EngineDiagnostic> {
    RelativeOperationPath::parse(&format!(
        "{}/{}",
        root.as_str().trim_end_matches('/'),
        suffix.trim_start_matches('/')
    ))
    .map_err(|_| {
        diagnostic(
            EngineDiagnosticCode::OutputPathEscape,
            root.as_str(),
            "module authoring path is not confined",
        )
    })
}

fn sdk_dependency_alias(manifest: &toml_edit::DocumentMut) -> Option<String> {
    let dependencies = manifest.get("dependencies")?.as_table_like()?;
    dependencies.iter().find_map(|(alias, dependency)| {
        let package = dependency
            .get("package")
            .and_then(toml_edit::Item::as_str)
            .unwrap_or(alias);
        (package == "dagger-sdk").then(|| alias.replace('-', "_"))
    })
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
    use std::collections::BTreeMap;
    use std::fs;

    use super::{
        authenticate_hook_baseline, digest, migrate_hook_baseline_project, validate_schema_identity,
    };
    use crate::canonical::{DigestDomain, canonical_digest};
    use crate::client::project::{
        ClientDocumentationState, ClientProjectRequest, discover_client_project,
        reconcile_client_project,
    };
    use crate::{
        ArtifactKind, ArtifactOwnership, ArtifactRecord, CanonicalRegistry, CanonicalRepositoryUrl,
        ClientProjectIdentity, ExactRustToolchain, ExactVersion, FormatVersion, FullRevision,
        GenerationMode, GeneratorIdentity, ModuleConfigFormat, ModuleOperationInput, OperationKind,
        OperationManifest, OperationRequest, OperationRoot, PublishedSdkDependency,
        RelativeOperationPath, RustIdentifier, SchemaInput, SdkPackageName, Sha256Digest,
        StableCoordinate, TargetIdentity,
    };

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

    #[test]
    fn hook_baseline_migration_requires_exact_authenticated_identity() {
        let temporary = tempfile::tempdir().expect("temporary root must exist");
        let client = path("client");
        fs::create_dir_all(temporary.path().join("client/src/dagger_generated"))
            .expect("fixture directories must exist");
        let target = target();
        let dependency = dependency();
        let module = ModuleOperationInput {
            name: stable("module"),
            original_name: stable("Module"),
            source_subpath: path("modules/module"),
            config_format: ModuleConfigFormat::Current,
            source_digest: sha(3),
            resolved_pin: None,
        };
        let request = OperationRequest {
            format_version: FormatVersion,
            operation: OperationKind::GenerateClient,
            target: target.clone(),
            visible_schema: SchemaInput {
                path: path("schema.json"),
                digest: sha(4),
            },
            module: Some(module.clone()),
            sdk_dependency: dependency.clone(),
            output_root: client.clone(),
        };
        let cargo = format!(
            "[package]\nname = \"dagger-rust-client\"\nversion = \"0.0.0\"\npublish = false\nedition = \"2024\"\nrust-version = \"1.97.1\"\n\n[package.metadata.dagger]\ncontent-domain = \"engine-hook-baseline\"\nvisible-schema-digest = \"{}\"\nmodule-source-digest = \"{}\"\n\n[dependencies]\ndagger-sdk = {{ version = \"=1.0.0-beta.10\" }}\n",
            request.visible_schema.digest, module.source_digest
        )
        .into_bytes();
        let library = b"//! Engine-hook baseline client.\n\npub mod dagger_generated;\npub use dagger_generated::*;\n".to_vec();
        let generated = b"//! Old generated binding.\n".to_vec();
        let cargo_path = path("client/Cargo.toml");
        let library_path = path("client/src/lib.rs");
        let generated_path = path("client/src/dagger_generated/query.rs");
        for (path, bytes) in [
            (&cargo_path, &cargo),
            (&library_path, &library),
            (&generated_path, &generated),
        ] {
            fs::write(path.join_lexically(temporary.path()), bytes)
                .expect("baseline file must be written");
        }
        fs::write(
            temporary.path().join("client/rust-toolchain.toml"),
            b"[toolchain]\nchannel = \"1.97.1\"\n",
        )
        .expect("toolchain fixture must be written");
        let root = OperationRoot::open(temporary.path()).expect("operation root must open");
        let snapshot = discover_client_project(&root, &client).expect("baseline must discover");
        let mut project = reconcile_client_project(
            &snapshot,
            &ClientProjectRequest {
                identity: ClientProjectIdentity {
                    package_name: crate::CargoPackageName::new("dagger-rust-client".to_owned())
                        .expect("package name must parse"),
                    crate_name: RustIdentifier::new("dagger_rust_client".to_owned())
                        .expect("crate name must parse"),
                },
                sdk_dependency: dependency.clone(),
                documentation: ClientDocumentationState::Generated,
            },
        )
        .expect("baseline project must reconcile");
        let artifacts = BTreeMap::from([
            (
                cargo_path.clone(),
                record(ArtifactKind::CargoManifest, &cargo),
            ),
            (
                library_path.clone(),
                record(ArtifactKind::RustSource, &library),
            ),
            (
                generated_path.clone(),
                record(ArtifactKind::RustSource, &generated),
            ),
        ]);
        let previous = OperationManifest {
            format_version: FormatVersion,
            operation: OperationKind::GenerateClient,
            mode: GenerationMode::CheckedGenerated,
            target,
            input_digest: canonical_digest(DigestDomain::OperationRequest, &request)
                .expect("request digest must encode"),
            visible_schema_digest: request.visible_schema.digest.clone(),
            module_source_digest: Some(module.source_digest),
            sdk_dependency: dependency,
            output_root: client,
            artifacts,
            post_work: Vec::new(),
            generator: GeneratorIdentity {
                version: exact("1.0.0-beta.10"),
                engine_source_digest: sha(5),
            },
            amendments: BTreeMap::new(),
            client: None,
        };
        assert_eq!(
            authenticate_hook_baseline(&previous, &root, &request, &project)
                .expect("exact baseline must migrate"),
            [cargo_path, library_path].into_iter().collect()
        );
        migrate_hook_baseline_project(&mut project, &path("client"))
            .expect("authenticated baseline must migrate");
        let migrated = project
            .amendments
            .values()
            .map(|amendment| String::from_utf8_lossy(&amendment.complete_file_bytes))
            .collect::<Vec<_>>();
        assert!(
            migrated
                .iter()
                .all(|bytes| !bytes.contains("engine-hook-baseline"))
        );
        assert!(
            migrated
                .iter()
                .all(|bytes| !bytes.contains("dagger_generated"))
        );

        let mut near_match = previous;
        near_match.visible_schema_digest = sha(99);
        assert!(authenticate_hook_baseline(&near_match, &root, &request, &project).is_err());
    }

    fn target() -> TargetIdentity {
        TargetIdentity {
            format_version: FormatVersion,
            repository: CanonicalRepositoryUrl::new("https://github.com/dagger/dagger".to_owned())
                .expect("repository must parse"),
            dagger_revision: FullRevision::new(
                "25300124ca110612edc09c43f89cb5fad6028170".to_owned(),
            )
            .expect("revision must parse"),
            engine_version: exact("1.0.0-beta.10"),
            rust_sdk_version: exact("1.0.0-beta.10"),
            rust_toolchain: ExactRustToolchain::new("1.97.1".to_owned())
                .expect("toolchain must parse"),
            core_schema_digest: sha(2),
        }
    }

    fn dependency() -> PublishedSdkDependency {
        PublishedSdkDependency::Registry {
            registry: CanonicalRegistry::new("crates-io".to_owned()).expect("registry must parse"),
            package: SdkPackageName::new("dagger-sdk".to_owned()).expect("package must parse"),
            exact_version: exact("1.0.0-beta.10"),
        }
    }

    fn record(kind: ArtifactKind, bytes: &[u8]) -> ArtifactRecord {
        ArtifactRecord {
            kind,
            digest: digest(bytes),
            ownership: ArtifactOwnership::Generator,
        }
    }

    fn path(value: &str) -> RelativeOperationPath {
        RelativeOperationPath::parse(value).expect("fixture path must parse")
    }

    fn stable(value: &str) -> StableCoordinate {
        StableCoordinate::new(value.to_owned()).expect("fixture coordinate must parse")
    }

    fn exact(value: &str) -> ExactVersion {
        ExactVersion::new(value.to_owned()).expect("fixture version must parse")
    }

    fn sha(seed: u8) -> Sha256Digest {
        format!("sha256:{seed:064x}")
            .parse()
            .expect("fixture digest must parse")
    }
}
