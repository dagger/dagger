//! Thin operation adapter over the pure standalone-client compiler and renderer.

use crate::client::{
    CargoPackageName, ClientCompilationInput, ClientProjectIdentity, ClientSchemaSurface,
    RustIdentifier, compile_client, render_client, render_client_at,
};
use crate::diagnostic::DiagnosticSet;

use super::model::{ClientRenderIdentity, ContentDomain, PostWorkPlan};
use super::renderers::{ClientRenderInput, RendererOutput};

pub(crate) fn render(
    input: ClientRenderInput<'_>,
    metadata: &super::metadata::ClientGenerationMetadata,
    selected_project: Option<&ClientProjectIdentity>,
    generated_client_root: Option<&super::RelativeOperationPath>,
) -> Result<RendererOutput, DiagnosticSet> {
    let project = selected_project
        .cloned()
        .map_or_else(baseline_project_identity, Ok)?;
    let plan = compile_client(ClientCompilationInput {
        target: input.target,
        visible_schema: input.schema,
        module: input.module,
        project: &project,
    })?;
    let rendered = match generated_client_root {
        Some(root) => render_client_at(&plan, input.output, root)?,
        None => render_client(&plan, input.output)?,
    };
    let mut output = RendererOutput::new(ContentDomain::StandaloneClient);
    output.artifacts = rendered.artifacts;
    output
        .vcs_generated
        .extend(output.artifacts.keys().cloned());
    output.post_work.push(PostWorkPlan::FormatRust {
        files: rendered.rust_sources,
    });
    output.client_generation = Some(metadata.clone());
    let (namespace, module_root_wire_name) = match (&plan.names, &plan.surface) {
        (Some(names), ClientSchemaSurface::BoundModule(surface)) => (
            Some(crate::client::ClientNamespaceRecord {
                namespace: names.namespace.clone(),
                extension_trait: names.extension_trait.clone(),
                root_type: names.root_type.clone(),
            }),
            Some(surface.root.field_wire_name.as_str().to_owned()),
        ),
        _ => (None, None),
    };
    output.client_render = Some(ClientRenderIdentity {
        project,
        namespace,
        module_root_wire_name,
        binding_catalog_digest: rendered.catalog.digest.as_str().to_owned(),
        binding_count: (rendered.catalog.core.len() + rendered.catalog.generated.len()) as u64,
    });
    Ok(output)
}

fn baseline_project_identity() -> Result<ClientProjectIdentity, DiagnosticSet> {
    let package_name = CargoPackageName::new("dagger-rust-client").map_err(|_| {
        DiagnosticSet::one(super::model::operation_diagnostic(
            crate::diagnostic::DiagnosticCode::GeneratedProvenanceInvalid,
            "client.package",
            "default standalone-client package identity is invalid",
        ))
    })?;
    let crate_name = RustIdentifier::new("dagger_rust_client").map_err(|_| {
        DiagnosticSet::one(super::model::operation_diagnostic(
            crate::diagnostic::DiagnosticCode::GeneratedProvenanceInvalid,
            "client.crate",
            "default standalone-client crate identity is invalid",
        ))
    })?;
    Ok(ClientProjectIdentity {
        package_name,
        crate_name,
    })
}
