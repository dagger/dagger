//! Production rendering from one pure module-authoring compilation.
//!
//! The visible schema contributes only checked generated-handle bindings. Authored
//! source, descriptor construction, registration, dispatch, and entrypoint generation
//! remain inside the Rust compiler pipeline and are published as one candidate set.

use std::collections::{BTreeMap, BTreeSet};

use crate::diagnostic::{DiagnosticCode, DiagnosticSet};
use crate::module::{
    GeneratedAssetPath, GeneratedTypeBinding, GeneratedTypeKind, GeneratedTypeRegistry,
    ModuleCompilation, ModuleCompilationRequest, ModuleCompiler, ModuleTarget,
    Sha256Digest as ModuleDigest, TargetValue, WireName,
};
use crate::projection::types::TypeProjection;

use super::model::{CandidateArtifactKind, ContentDomain, PostWorkPlan, operation_diagnostic};
use super::render::{rust_source_paths, visible_binding_artifacts};
use super::renderers::{EntrypointRenderInput, ModuleRenderInput, RendererOutput};
use super::{CargoBinaryTarget, RelativeOperationPath};

pub(crate) fn render(input: ModuleRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet> {
    let compilation = compile_authoring(input.target, input.schema, input.authoring)?;
    render_compilation(input.output, compilation, false)
}

pub(crate) fn render_entrypoint(
    input: EntrypointRenderInput<'_>,
) -> Result<RendererOutput, DiagnosticSet> {
    let compilation = compile_authoring(input.target, input.schema, input.authoring)?;
    render_compilation(input.output, compilation, true)
}

fn compile_authoring(
    target: &crate::target::CodegenTarget,
    schema: &super::visible::VisibleSchemaPlan,
    authoring: &super::ModuleAuthoringInput,
) -> Result<ModuleCompilation, DiagnosticSet> {
    let visible_digest = ModuleDigest::new(schema.digest().as_str()).map_err(|_| failure())?;
    let module_target = ModuleTarget {
        dagger_revision: target_value(target.dagger_revision().as_str())?,
        engine_version: target_value(&target.engine_version().to_string())?,
        rust_sdk_version: target_value(&target.rust_sdk_version().to_string())?,
        rust_toolchain: target_value(&target.rust_version().to_string())?,
        rust_edition: target_value(target.rust_edition().as_str())?,
        visible_schema_digest: visible_digest.clone(),
    };
    let generated_types = generated_type_registry(
        schema,
        &authoring.sdk_dependency_alias,
        visible_digest.clone(),
    )?;
    let visible_type_names = schema
        .projection()
        .named_types()
        .keys()
        // `Query` is the target registration root, not a local authored type.
        .filter(|name| name.as_str() != "Query")
        .map(|name| WireName::new(name.as_str()).map_err(|_| failure()))
        .collect::<Result<BTreeSet<_>, _>>()?;
    let checked_bindings = checked_binding_assets(schema)?;

    ModuleCompiler::compile(ModuleCompilationRequest {
        target: &module_target,
        source: &authoring.source,
        generated_types: &generated_types,
        visible_type_names: &visible_type_names,
        generator_digest: &authoring.generator_digest,
        sdk_dependency_alias: &authoring.sdk_dependency_alias,
        checked_bindings: &checked_bindings,
    })
    .map_err(|_| failure())
}

fn generated_type_registry(
    schema: &super::visible::VisibleSchemaPlan,
    alias: &str,
    digest: ModuleDigest,
) -> Result<GeneratedTypeRegistry, DiagnosticSet> {
    let mut bindings = Vec::new();
    for projection in schema.projection().named_types().values() {
        let (wire_name, rust_name, kind, extension_name) = match projection {
            TypeProjection::Object(object) => (
                object.wire_name.as_str(),
                object.rust_name.as_str(),
                GeneratedTypeKind::Object,
                &object.wire_name,
            ),
            TypeProjection::Interface(interface) => (
                interface.wire_name.as_str(),
                interface.client_name.as_str(),
                GeneratedTypeKind::Interface,
                &interface.wire_name,
            ),
            _ => continue,
        };
        let rust_path = if schema.is_extension_type(extension_name) {
            format!("crate::dagger_generated::{rust_name}")
        } else {
            format!("{alias}::{rust_name}")
        };
        bindings.push(GeneratedTypeBinding {
            rust_path,
            target_name: wire_name.to_owned(),
            visible_schema_digest: digest.clone(),
            kind,
        });
    }
    GeneratedTypeRegistry::new(digest, bindings).map_err(|_| failure())
}

fn checked_binding_assets(
    schema: &super::visible::VisibleSchemaPlan,
) -> Result<BTreeMap<GeneratedAssetPath, Vec<u8>>, DiagnosticSet> {
    let root = RelativeOperationPath::parse("src/dagger_generated")?;
    visible_binding_artifacts(schema, &root)?
        .into_iter()
        .filter(|(path, artifact)| {
            artifact.kind == CandidateArtifactKind::RustSource
                && path.as_str() != "src/dagger_generated/mod.rs"
        })
        .map(|(path, artifact)| {
            GeneratedAssetPath::new(path.as_str())
                .map(|path| (path, artifact.content))
                .map_err(|_| failure())
        })
        .collect()
}

fn render_compilation(
    output_root: &RelativeOperationPath,
    compilation: ModuleCompilation,
    entrypoint_only: bool,
) -> Result<RendererOutput, DiagnosticSet> {
    let mut output = RendererOutput::new(if entrypoint_only {
        ContentDomain::ModuleEntrypoint
    } else {
        ContentDomain::ModuleOperation
    });
    for (path, content) in compilation.assets.files {
        let is_entrypoint = path.as_str() == "src/bin/dagger-module.rs";
        if entrypoint_only && !is_entrypoint {
            continue;
        }
        let destination = output_root.join(path.as_str())?;
        let kind = if path.as_str().ends_with(".rs") {
            CandidateArtifactKind::RustSource
        } else {
            CandidateArtifactKind::ControlManifest
        };
        output.insert_artifact(destination, kind, content)?;
    }
    let rust_files = rust_source_paths(&output.artifacts);
    output
        .vcs_generated
        .extend(output.artifacts.keys().cloned());
    output
        .post_work
        .push(PostWorkPlan::FormatRust { files: rust_files });
    output.cargo_binary = Some(CargoBinaryTarget {
        name: "dagger-module".to_owned(),
        path: RelativeOperationPath::parse("src/bin/dagger-module.rs")?,
    });
    Ok(output)
}

fn target_value(value: &str) -> Result<TargetValue, DiagnosticSet> {
    TargetValue::new(value).map_err(|_| failure())
}

fn failure() -> DiagnosticSet {
    DiagnosticSet::one(operation_diagnostic(
        DiagnosticCode::GeneratedProvenanceInvalid,
        "operation.authoring",
        "Rust module authoring compilation failed",
    ))
}
