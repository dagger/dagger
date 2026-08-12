//! Total and lossless dispatch properties for the pure operation facade.

mod support;

use std::cell::RefCell;
use std::collections::BTreeMap;
use std::sync::LazyLock;

use dagger_codegen::diagnostic::{DiagnosticCode, DiagnosticSet};
use dagger_codegen::engine::{
    CandidateArtifact, CandidateArtifactKind, ClientGenerationMetadata, ClientRenderInput,
    ContentDomain, EntrypointRenderInput, LibraryRenderInput, ModuleAuthoringInput,
    ModuleProjectionInput, ModuleRenderInput, OperationKind, OperationRenderer,
    PreparedOperationRequest, PublishedSdkDependency, RelativeOperationPath, RendererOutput,
    VisibleSchemaPlan, dispatch_prepared_operation, project_visible_schema,
};
use dagger_codegen::target::CodegenTarget;
use proptest::prelude::*;

use support::{CORE_SCHEMA_BYTES, TARGET_BYTES, module_authoring_input, pure_config};

static TARGET: LazyLock<CodegenTarget> = LazyLock::new(|| {
    CodegenTarget::decode_exact(TARGET_BYTES).expect("checked target must decode")
});
static SCHEMA: LazyLock<VisibleSchemaPlan> = LazyLock::new(|| {
    project_visible_schema(&TARGET, CORE_SCHEMA_BYTES).expect("checked schema must project")
});

#[derive(Clone, Debug, Eq, PartialEq)]
struct RecordedCall {
    operation: OperationKind,
    target_address: usize,
    schema_address: usize,
    module: Option<ModuleProjectionInput>,
    output: RelativeOperationPath,
    sdk_dependency: PublishedSdkDependency,
    authoring: Option<ModuleAuthoringInput>,
}

struct RecordingRenderer {
    calls: RefCell<Vec<RecordedCall>>,
    output: RendererOutput,
}

impl RecordingRenderer {
    fn new(output: RendererOutput) -> Self {
        Self {
            calls: RefCell::new(Vec::new()),
            output,
        }
    }

    fn record(&self, call: RecordedCall) -> RendererOutput {
        let operation = call.operation;
        self.calls.borrow_mut().push(call);
        let mut rendered = self.output.clone();
        if operation == OperationKind::GenerateClient {
            rendered.client_generation = Some(ClientGenerationMetadata::baseline());
        }
        if matches!(
            operation,
            OperationKind::GenerateModule | OperationKind::GenerateEntrypoint
        ) {
            rendered.cargo_binary = Some(dagger_codegen::engine::CargoBinaryTarget {
                name: "dagger-module".to_owned(),
                path: RelativeOperationPath::parse("src/bin/dagger-module.rs")
                    .expect("recorded binary path must parse"),
            });
        }
        rendered
    }
}

impl OperationRenderer for RecordingRenderer {
    fn render_library(
        &self,
        input: LibraryRenderInput<'_>,
    ) -> Result<RendererOutput, DiagnosticSet> {
        Ok(self.record(RecordedCall {
            operation: OperationKind::GenerateLibrary,
            target_address: input.target as *const CodegenTarget as usize,
            schema_address: input.schema as *const VisibleSchemaPlan as usize,
            module: input.module.cloned(),
            output: input.output.clone(),
            sdk_dependency: input.sdk_dependency.clone(),
            authoring: None,
        }))
    }

    fn render_module(&self, input: ModuleRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet> {
        Ok(self.record(RecordedCall {
            operation: OperationKind::GenerateModule,
            target_address: input.target as *const CodegenTarget as usize,
            schema_address: input.schema as *const VisibleSchemaPlan as usize,
            module: Some(input.module.clone()),
            output: input.output.clone(),
            sdk_dependency: input.sdk_dependency.clone(),
            authoring: Some(input.authoring.clone()),
        }))
    }

    fn render_client(&self, input: ClientRenderInput<'_>) -> Result<RendererOutput, DiagnosticSet> {
        Ok(self.record(RecordedCall {
            operation: OperationKind::GenerateClient,
            target_address: input.target as *const CodegenTarget as usize,
            schema_address: input.schema as *const VisibleSchemaPlan as usize,
            module: Some(input.module.clone()),
            output: input.output.clone(),
            sdk_dependency: input.sdk_dependency.clone(),
            authoring: None,
        }))
    }

    fn render_entrypoint(
        &self,
        input: EntrypointRenderInput<'_>,
    ) -> Result<RendererOutput, DiagnosticSet> {
        Ok(self.record(RecordedCall {
            operation: OperationKind::GenerateEntrypoint,
            target_address: input.target as *const CodegenTarget as usize,
            schema_address: input.schema as *const VisibleSchemaPlan as usize,
            module: Some(input.module.clone()),
            output: input.output.clone(),
            sdk_dependency: input.sdk_dependency.clone(),
            authoring: Some(input.authoring.clone()),
        }))
    }
}

fn operation(discriminant: u8) -> OperationKind {
    match discriminant % 4 {
        0 => OperationKind::GenerateLibrary,
        1 => OperationKind::GenerateModule,
        2 => OperationKind::GenerateClient,
        _ => OperationKind::GenerateEntrypoint,
    }
}

proptest! {
    #![proptest_config(pure_config())]

    // A selector either forwards every value to one renderer or invokes no renderer at all.
    #[test]
    fn property_12_operation_dispatch_total_lossless(
        discriminant in any::<u8>(),
        unknown in any::<bool>(),
        module_present in any::<bool>(),
        authoring_present in any::<bool>(),
        module_name in "[a-z][a-z0-9-]{0,15}",
        exact_version in "[1-9][0-9]{0,2}\\.[0-9]{1,3}\\.[0-9]{1,3}",
        artifacts in prop::collection::btree_map(
            "[a-z][a-z0-9_-]{0,10}\\.rs",
            prop::collection::vec(any::<u8>(), 0..64),
            0..8,
        ),
    ) {
        let operation = operation(discriminant);
        let output_root = RelativeOperationPath::parse("candidate")
            .expect("fixture output must parse");
        let module = ModuleProjectionInput {
            name: module_name.clone(),
            original_name: module_name,
            source_subpath: RelativeOperationPath::parse("modules/selected")
                .expect("fixture module path must parse"),
            source_digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
                .to_owned(),
        };
        let dependency = PublishedSdkDependency::Registry {
            registry: "crates-io".to_owned(),
            exact_version,
        };
        let authoring = module_authoring_input();
        let mut output = RendererOutput::new(ContentDomain::VisibleSchemaBindings);
        for (name, content) in artifacts {
            output
                .insert_artifact(
                    output_root.join(&name).expect("generated artifact path must parse"),
                    CandidateArtifactKind::RustSource,
                    content,
                )
                .expect("generated artifact set must be unique");
        }
        let expected_artifacts: BTreeMap<RelativeOperationPath, CandidateArtifact> =
            output.artifacts.clone();
        let renderer = RecordingRenderer::new(output);

        if unknown {
            let diagnostics = OperationKind::decode("unknown-operation")
                .expect_err("unknown selector must fail");
            prop_assert!(diagnostics.contains(DiagnosticCode::OperationUnknown));
            prop_assert!(renderer.calls.borrow().is_empty());
            return Ok(());
        }

        let module_input = module_present.then_some(&module);
        let authoring_input = authoring_present.then_some(&authoring);
        let result = dispatch_prepared_operation(
            &renderer,
            PreparedOperationRequest {
                target: &TARGET,
                operation,
                schema: &SCHEMA,
                module: module_input,
                output: &output_root,
                sdk_dependency: &dependency,
                authoring: authoring_input,
            },
        );
        let valid = match operation {
            OperationKind::GenerateLibrary => !authoring_present,
            OperationKind::GenerateModule => module_present && authoring_present,
            OperationKind::GenerateClient => module_present && !authoring_present,
            OperationKind::GenerateEntrypoint => module_present && authoring_present,
        };
        if !valid {
            let diagnostics = result.expect_err("invalid selector/input pair must fail");
            prop_assert!(
                diagnostics.contains(DiagnosticCode::OperationInputMissing)
                    || diagnostics.contains(DiagnosticCode::OperationInputForbidden)
            );
            prop_assert!(renderer.calls.borrow().is_empty());
            return Ok(());
        }

        let plan = result.expect("valid selector/input pair must dispatch");
        prop_assert_eq!(plan.artifacts(), &expected_artifacts);
        prop_assert_eq!(plan.module(), module_input);
        prop_assert_eq!(plan.output(), &output_root);
        prop_assert_eq!(plan.sdk_dependency(), &dependency);
        prop_assert_eq!(plan.authoring(), authoring_input);
        let calls = renderer.calls.borrow();
        prop_assert_eq!(calls.len(), 1);
        let call = &calls[0];
        prop_assert_eq!(call.operation, operation);
        prop_assert_eq!(call.target_address, &*TARGET as *const CodegenTarget as usize);
        prop_assert_eq!(call.schema_address, &*SCHEMA as *const VisibleSchemaPlan as usize);
        prop_assert_eq!(call.module.as_ref(), module_input);
        prop_assert_eq!(&call.output, &output_root);
        prop_assert_eq!(&call.sdk_dependency, &dependency);
        prop_assert_eq!(call.authoring.as_ref(), authoring_input);
    }
}
