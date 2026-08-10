//! Production renderer for reusable operation-scoped binding artifacts.

use crate::diagnostic::DiagnosticSet;

use super::model::{ContentDomain, PostWorkPlan};
use super::render::{rust_source_paths, visible_binding_artifacts};
use super::renderers::{LibraryRenderInput, RendererOutput};

pub(crate) fn render(input: LibraryRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet> {
    let artifacts = visible_binding_artifacts(input.schema, input.output)?;
    let rust_files = rust_source_paths(&artifacts);
    let mut output = RendererOutput::new(ContentDomain::VisibleSchemaBindings);
    output.artifacts = artifacts;
    output.vcs_generated.extend(rust_files.iter().cloned());
    output
        .post_work
        .push(PostWorkPlan::FormatRust { files: rust_files });
    Ok(output)
}
