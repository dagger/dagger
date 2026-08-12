//! Thin operation adapter over the pure standalone-client compiler and renderer.

use crate::client::{
    CargoPackageName, ClientCompilationInput, ClientProjectIdentity, RustIdentifier,
    compile_client, render_client,
};
use crate::diagnostic::DiagnosticSet;

use super::model::{ContentDomain, PostWorkPlan};
use super::renderers::{ClientRenderInput, RendererOutput};

pub(crate) fn render(
    input: ClientRenderInput<'_>,
    metadata: &super::metadata::ClientGenerationMetadata,
) -> Result<RendererOutput, DiagnosticSet> {
    let project = baseline_project_identity()?;
    let plan = compile_client(ClientCompilationInput {
        target: input.target,
        visible_schema: input.schema,
        module: input.module,
        project: &project,
    })?;
    let rendered = render_client(&plan, input.output)?;
    let mut output = RendererOutput::new(ContentDomain::StandaloneClient);
    output.artifacts = rendered.artifacts;
    output
        .vcs_generated
        .extend(output.artifacts.keys().cloned());
    output.post_work.push(PostWorkPlan::FormatRust {
        files: rendered.rust_sources,
    });
    output.client_generation = Some(metadata.clone());
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
