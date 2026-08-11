//! Pure engine operation compiler and bounded production renderer seams.
//!
//! The facade validates one complete visible schema, constructs one immutable semantic
//! plan, and invokes exactly one renderer. Filesystem access, Cargo execution, engine
//! sessions, publication, and completeness transitions remain outside this crate.

mod client;
mod entrypoint;
mod library;
mod metadata;
mod model;
mod module;
mod render;
mod renderers;
mod visible;

pub use metadata::{BASELINE_CLIENT_GENERATION_JSON, ClientGenerationMetadata};
pub use model::{
    CHECKED_ENTRYPOINT_JSON, CHECKED_ENTRYPOINT_SHA256, CandidateArtifact, CandidateArtifactKind,
    CargoBinaryTarget, ContentDomain, EntrypointInput, ModuleProjectionInput, OperationKind,
    OperationPlan, OperationProjectionRequest, PostWorkPlan, PublishedSdkDependency,
    RelativeOperationPath,
};
pub use renderers::{
    ClientRenderInput, EntrypointRenderInput, LibraryRenderInput, ModuleRenderInput,
    OperationRenderer, PreparedOperationRequest, RendererOutput, dispatch_prepared_operation,
    project_operation_with,
};
pub use visible::{VisibleSchemaPlan, project_visible_schema};

use crate::diagnostic::DiagnosticSet;

/// Production renderer set with Rust-owned client-generation metadata.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ProductionRenderers {
    client_generation: ClientGenerationMetadata,
}

impl ProductionRenderers {
    /// Creates production renderers from validated required-host-file metadata.
    #[must_use]
    pub const fn new(client_generation: ClientGenerationMetadata) -> Self {
        Self { client_generation }
    }

    /// Returns the baseline renderer configuration packaged by this checkpoint.
    #[must_use]
    pub fn baseline() -> Self {
        Self::new(ClientGenerationMetadata::baseline())
    }
}

impl OperationRenderer for ProductionRenderers {
    fn render_library(
        &self,
        input: LibraryRenderInput<'_>,
    ) -> Result<RendererOutput, DiagnosticSet> {
        library::render(input)
    }

    fn render_module(&self, input: ModuleRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet> {
        module::render(input)
    }

    fn render_client(&self, input: ClientRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet> {
        client::render(input, &self.client_generation)
    }

    fn render_entrypoint(
        &self,
        input: EntrypointRenderInput<'_>,
    ) -> Result<RendererOutput, DiagnosticSet> {
        let mut output = RendererOutput::new(ContentDomain::ProtocolProbe);
        let path = input.output.join("src/bin/dagger-module.rs")?;
        output.insert_artifact(
            path.clone(),
            CandidateArtifactKind::RustSource,
            entrypoint::render_source(input.module, input.entrypoint)?,
        )?;
        output.vcs_generated.insert(path.clone());
        output.post_work.push(PostWorkPlan::FormatRust {
            files: std::collections::BTreeSet::from([path]),
        });
        output.cargo_binary = Some(CargoBinaryTarget {
            name: "dagger-module".to_owned(),
            path: RelativeOperationPath::parse("src/bin/dagger-module.rs")?,
        });
        Ok(output)
    }
}

/// Projects and renders one operation through the production renderer set.
pub fn project_operation(
    request: OperationProjectionRequest<'_>,
) -> Result<OperationPlan, DiagnosticSet> {
    project_operation_with(&ProductionRenderers::baseline(), request)
}
