//! Production renderer for module-owned bindings and private entrypoint content.

use crate::diagnostic::DiagnosticSet;

use super::entrypoint;
use super::model::{CandidateArtifactKind, ContentDomain, EntrypointInput, PostWorkPlan};
use super::render::{rust_source_paths, visible_binding_artifacts};
use super::renderers::{ModuleRenderInput, RendererOutput};

pub(crate) fn render(input: ModuleRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet> {
    let generated_root = input.output.join("src/dagger_generated")?;
    let mut output = RendererOutput::new(ContentDomain::ModuleOperation);
    output.artifacts = visible_binding_artifacts(input.schema, &generated_root)?;
    let checked;
    let entrypoint = match input.entrypoint {
        Some(entrypoint) => entrypoint,
        None => {
            checked = EntrypointInput::decode_checked(&EntrypointInput::checked_bytes()?)?;
            &checked
        }
    };
    let entrypoint_path = input.output.join("src/bin/dagger-module.rs")?;
    output.insert_artifact(
        entrypoint_path,
        CandidateArtifactKind::RustSource,
        entrypoint::render_source(input.module, entrypoint)?,
    )?;
    let rust_files = rust_source_paths(&output.artifacts);
    output.vcs_generated.extend(rust_files.iter().cloned());
    output
        .post_work
        .push(PostWorkPlan::FormatRust { files: rust_files });
    output.cargo_binary = Some(super::model::CargoBinaryTarget {
        name: "dagger-module".to_owned(),
        path: super::model::RelativeOperationPath::parse("src/bin/dagger-module.rs")?,
    });
    Ok(output)
}
