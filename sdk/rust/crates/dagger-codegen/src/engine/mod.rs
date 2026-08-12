//! Pure engine operation compiler and bounded production renderer seams.
//!
//! The facade validates one complete visible schema, constructs one immutable semantic
//! plan, and invokes exactly one renderer. Filesystem access, Cargo execution, engine
//! sessions, publication, and completeness transitions remain outside this crate.

mod client;
mod library;
mod metadata;
mod model;
mod module;
mod render;
mod renderers;
mod visible;

pub use metadata::{
    BASELINE_CLIENT_GENERATION_JSON, ClientGenerationMetadata, REQUIRED_CLIENT_HOST_FILES,
};
pub use model::{
    CandidateArtifact, CandidateArtifactKind, CargoBinaryTarget, ClientRenderIdentity,
    ContentDomain, ModuleAuthoringInput, ModuleProjectionInput, OperationKind, OperationPlan,
    OperationProjectionRequest, PostWorkPlan, PublishedSdkDependency, RelativeOperationPath,
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
    client_project: Option<crate::client::ClientProjectIdentity>,
    generated_client_root: Option<RelativeOperationPath>,
}

impl ProductionRenderers {
    /// Creates production renderers from validated required-host-file metadata.
    #[must_use]
    pub const fn new(client_generation: ClientGenerationMetadata) -> Self {
        Self {
            client_generation,
            client_project: None,
            generated_client_root: None,
        }
    }

    /// Binds standalone-client rendering to an already discovered Cargo identity.
    #[must_use]
    pub fn for_client(
        project: crate::client::ClientProjectIdentity,
        generated_client_root: RelativeOperationPath,
    ) -> Self {
        Self {
            client_generation: ClientGenerationMetadata::baseline(),
            client_project: Some(project),
            generated_client_root: Some(generated_client_root),
        }
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
        client::render(
            input,
            &self.client_generation,
            self.client_project.as_ref(),
            self.generated_client_root.as_ref(),
        )
    }

    fn render_entrypoint(
        &self,
        input: EntrypointRenderInput<'_>,
    ) -> Result<RendererOutput, DiagnosticSet> {
        module::render_entrypoint(input)
    }
}

/// Projects and renders one operation through the production renderer set.
pub fn project_operation(
    request: OperationProjectionRequest<'_>,
) -> Result<OperationPlan, DiagnosticSet> {
    project_operation_with(&ProductionRenderers::baseline(), request)
}
