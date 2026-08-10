//! Total operation dispatch across four independently replaceable renderer seams.

use std::collections::{BTreeMap, BTreeSet};

use crate::diagnostic::{DiagnosticCode, DiagnosticSet};
use crate::target::CodegenTarget;

use super::metadata::ClientGenerationMetadata;
use super::model::{
    CandidateArtifact, CandidateArtifactKind, CargoBinaryTarget, ContentDomain, EntrypointInput,
    ModuleProjectionInput, OperationKind, OperationPlan, OperationProjectionRequest, PostWorkPlan,
    PublishedSdkDependency, RelativeOperationPath, operation_diagnostic,
};
use super::visible::{VisibleSchemaPlan, project_visible_schema};

/// Inputs common to the reusable binding renderer.
pub struct LibraryRenderInput<'a> {
    /// Exact target inherited from the visible-schema plan.
    pub target: &'a CodegenTarget,
    /// Single immutable schema plan shared by all renderers.
    pub schema: &'a VisibleSchemaPlan,
    /// Optional module identity retained by library generation without mutation.
    pub module: Option<&'a ModuleProjectionInput>,
    /// Normalized generated-library root.
    pub output: &'a RelativeOperationPath,
    /// Immutable public SDK dependency available to renderer policy.
    pub sdk_dependency: &'a PublishedSdkDependency,
}

/// Inputs required by module binding and private-entrypoint rendering.
pub struct ModuleRenderInput<'a> {
    /// Exact target inherited from the visible-schema plan.
    pub target: &'a CodegenTarget,
    /// Single immutable schema plan shared by all renderers.
    pub schema: &'a VisibleSchemaPlan,
    /// Exact engine-scoped module source identity.
    pub module: &'a ModuleProjectionInput,
    /// Normalized module project root.
    pub output: &'a RelativeOperationPath,
    /// Immutable public SDK dependency planned for Cargo adoption.
    pub sdk_dependency: &'a PublishedSdkDependency,
    /// Optional checked probe document admitted only for the private fixture.
    pub entrypoint: Option<&'a EntrypointInput>,
}

/// Inputs required by the bounded standalone-client baseline.
pub struct ClientRenderInput<'a> {
    /// Exact target inherited from the visible-schema plan.
    pub target: &'a CodegenTarget,
    /// Single immutable schema plan shared by all renderers.
    pub schema: &'a VisibleSchemaPlan,
    /// Exact engine-scoped module source identity.
    pub module: &'a ModuleProjectionInput,
    /// Normalized standalone-client root.
    pub output: &'a RelativeOperationPath,
    /// Immutable public SDK dependency rendered into Cargo metadata.
    pub sdk_dependency: &'a PublishedSdkDependency,
}

/// Inputs required by the checked private protocol entrypoint.
pub struct EntrypointRenderInput<'a> {
    /// Exact target inherited from the visible-schema plan.
    pub target: &'a CodegenTarget,
    /// Single immutable schema plan shared by all renderers.
    pub schema: &'a VisibleSchemaPlan,
    /// Exact engine-scoped module source identity.
    pub module: &'a ModuleProjectionInput,
    /// Normalized entrypoint output root.
    pub output: &'a RelativeOperationPath,
    /// Immutable public SDK dependency available to entrypoint policy.
    pub sdk_dependency: &'a PublishedSdkDependency,
    /// Strictly checked private protocol TypeDef.
    pub entrypoint: &'a EntrypointInput,
}

/// Deterministic renderer output before an operation plan is assembled.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RendererOutput {
    /// Complete candidate artifact set in canonical path order.
    pub artifacts: BTreeMap<RelativeOperationPath, CandidateArtifact>,
    /// Closed post-render work requested by the renderer.
    pub post_work: Vec<PostWorkPlan>,
    /// Paths that require generated VCS treatment.
    pub vcs_generated: BTreeSet<RelativeOperationPath>,
    /// Paths that require ignore treatment.
    pub vcs_ignored: BTreeSet<RelativeOperationPath>,
    /// Bounded evidence domain represented by this content.
    pub content_domain: ContentDomain,
    /// Rust-owned required-host-file policy, present only for client generation.
    pub client_generation: Option<ClientGenerationMetadata>,
    /// Cargo binary amendment, present only when an entrypoint is generated.
    pub cargo_binary: Option<CargoBinaryTarget>,
}

impl RendererOutput {
    /// Creates an empty output in one explicit evidence domain.
    #[must_use]
    pub fn new(content_domain: ContentDomain) -> Self {
        Self {
            artifacts: BTreeMap::new(),
            post_work: Vec::new(),
            vcs_generated: BTreeSet::new(),
            vcs_ignored: BTreeSet::new(),
            content_domain,
            client_generation: None,
            cargo_binary: None,
        }
    }

    /// Inserts one owned artifact and rejects path aliasing immediately.
    pub fn insert_artifact(
        &mut self,
        path: RelativeOperationPath,
        kind: CandidateArtifactKind,
        content: Vec<u8>,
    ) -> Result<(), DiagnosticSet> {
        if self
            .artifacts
            .insert(path.clone(), CandidateArtifact { kind, content })
            .is_some()
        {
            return Err(DiagnosticSet::one(operation_diagnostic(
                DiagnosticCode::OperationArtifactCollision,
                path.as_str(),
                "renderer emitted the same artifact path more than once",
            )));
        }
        Ok(())
    }
}

/// Four renderer seams over one already-validated visible-schema plan.
pub trait OperationRenderer {
    /// Renders reusable visible-schema binding artifacts.
    fn render_library(
        &self,
        input: LibraryRenderInput<'_>,
    ) -> Result<RendererOutput, DiagnosticSet>;

    /// Renders module-owned bindings and private runtime glue.
    fn render_module(&self, input: ModuleRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet>;

    /// Renders the bounded standalone-client baseline.
    fn render_client(&self, input: ClientRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet>;

    /// Renders the checked private protocol-probe entrypoint.
    fn render_entrypoint(
        &self,
        input: EntrypointRenderInput<'_>,
    ) -> Result<RendererOutput, DiagnosticSet>;
}

/// Borrowed operation inputs after visible-schema validation.
pub struct PreparedOperationRequest<'a> {
    /// Exact checked target.
    pub target: &'a CodegenTarget,
    /// Closed operation selector.
    pub operation: OperationKind,
    /// Already validated single visible-schema plan.
    pub schema: &'a VisibleSchemaPlan,
    /// Exact scoped module identity where permitted or required.
    pub module: Option<&'a ModuleProjectionInput>,
    /// Normalized output identity.
    pub output: &'a RelativeOperationPath,
    /// Immutable public SDK dependency.
    pub sdk_dependency: &'a PublishedSdkDependency,
    /// Checked TypeDef input where permitted or required.
    pub entrypoint: Option<&'a EntrypointInput>,
}

/// Validates visible schema and dispatches through the supplied renderer exactly once.
pub fn project_operation_with<R: OperationRenderer>(
    renderer: &R,
    request: OperationProjectionRequest<'_>,
) -> Result<OperationPlan, DiagnosticSet> {
    validate_operation_inputs(request.operation, request.module, request.entrypoint)?;
    let schema = project_visible_schema(request.target, request.visible_schema_json)?;
    dispatch_prepared_operation(
        renderer,
        PreparedOperationRequest {
            target: request.target,
            operation: request.operation,
            schema: &schema,
            module: request.module,
            output: request.output,
            sdk_dependency: request.sdk_dependency,
            entrypoint: request.entrypoint,
        },
    )
}

/// Dispatches an already-projected plan; useful for deterministic recording tests.
pub fn dispatch_prepared_operation<R: OperationRenderer>(
    renderer: &R,
    request: PreparedOperationRequest<'_>,
) -> Result<OperationPlan, DiagnosticSet> {
    validate_operation_inputs(request.operation, request.module, request.entrypoint)?;
    if request.schema.projection().target() != request.target {
        return Err(DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::TargetIdentityInvalid,
            "operation.target",
            "visible schema plan belongs to a different target",
        )));
    }
    let output = match request.operation {
        OperationKind::GenerateLibrary => renderer.render_library(LibraryRenderInput {
            target: request.target,
            schema: request.schema,
            module: request.module,
            output: request.output,
            sdk_dependency: request.sdk_dependency,
        })?,
        OperationKind::GenerateModule => renderer.render_module(ModuleRenderInput {
            target: request.target,
            schema: request.schema,
            module: required_module(request.module)?,
            output: request.output,
            sdk_dependency: request.sdk_dependency,
            entrypoint: request.entrypoint,
        })?,
        OperationKind::GenerateClient => renderer.render_client(ClientRenderInput {
            target: request.target,
            schema: request.schema,
            module: required_module(request.module)?,
            output: request.output,
            sdk_dependency: request.sdk_dependency,
        })?,
        OperationKind::GenerateEntrypoint => renderer.render_entrypoint(EntrypointRenderInput {
            target: request.target,
            schema: request.schema,
            module: required_module(request.module)?,
            output: request.output,
            sdk_dependency: request.sdk_dependency,
            entrypoint: required_entrypoint(request.entrypoint)?,
        })?,
    };
    validate_renderer_metadata(request.operation, &output)?;
    Ok(OperationPlan {
        target: request.target.clone(),
        operation: request.operation,
        schema: request.schema.clone(),
        module: request.module.cloned(),
        output: request.output.clone(),
        sdk_dependency: request.sdk_dependency.clone(),
        entrypoint: request.entrypoint.cloned(),
        artifacts: output.artifacts,
        post_work: output.post_work,
        vcs_generated: output.vcs_generated,
        vcs_ignored: output.vcs_ignored,
        projection_pass_limit: 1,
        content_domain: output.content_domain,
        client_generation: output.client_generation,
        cargo_binary: output.cargo_binary,
    })
}

fn validate_operation_inputs(
    operation: OperationKind,
    module: Option<&ModuleProjectionInput>,
    entrypoint: Option<&EntrypointInput>,
) -> Result<(), DiagnosticSet> {
    let module_required = !matches!(operation, OperationKind::GenerateLibrary);
    if module_required && module.is_none() {
        return Err(DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationInputMissing,
            &format!("operation.{}.module", operation.as_str()),
            "selected operation requires module input",
        )));
    }
    let entrypoint_required = operation == OperationKind::GenerateEntrypoint;
    let entrypoint_allowed = matches!(
        operation,
        OperationKind::GenerateModule | OperationKind::GenerateEntrypoint
    );
    if entrypoint_required && entrypoint.is_none() {
        return Err(DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationInputMissing,
            &format!("operation.{}.entrypoint", operation.as_str()),
            "selected operation requires checked entrypoint input",
        )));
    }
    if !entrypoint_allowed && entrypoint.is_some() {
        return Err(DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationInputForbidden,
            &format!("operation.{}.entrypoint", operation.as_str()),
            "selected operation forbids entrypoint input",
        )));
    }
    Ok(())
}

fn validate_renderer_metadata(
    operation: OperationKind,
    output: &RendererOutput,
) -> Result<(), DiagnosticSet> {
    let metadata_matches = match operation {
        OperationKind::GenerateClient => output.client_generation.is_some(),
        _ => output.client_generation.is_none(),
    };
    if !metadata_matches {
        return Err(DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationInputForbidden,
            "operation.client-generation",
            "client-generation metadata belongs only to GenerateClient output",
        )));
    }
    let binary_matches = match operation {
        OperationKind::GenerateModule | OperationKind::GenerateEntrypoint => {
            output.cargo_binary.is_some()
        }
        OperationKind::GenerateLibrary | OperationKind::GenerateClient => {
            output.cargo_binary.is_none()
        }
    };
    if binary_matches {
        Ok(())
    } else {
        Err(DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationInputMissing,
            "operation.cargo-binary",
            "operation renderer returned an incompatible Cargo binary amendment",
        )))
    }
}

fn required_module(
    module: Option<&ModuleProjectionInput>,
) -> Result<&ModuleProjectionInput, DiagnosticSet> {
    module.ok_or_else(|| {
        DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationInputMissing,
            "operation.module",
            "selected operation requires module input",
        ))
    })
}

fn required_entrypoint(
    entrypoint: Option<&EntrypointInput>,
) -> Result<&EntrypointInput, DiagnosticSet> {
    entrypoint.ok_or_else(|| {
        DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationInputMissing,
            "operation.entrypoint",
            "selected operation requires checked entrypoint input",
        ))
    })
}
