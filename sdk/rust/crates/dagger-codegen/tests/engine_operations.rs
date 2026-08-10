//! Fixed operation-matrix, production-renderer, and client-metadata regressions.

mod support;

use dagger_codegen::diagnostic::DiagnosticCode;
use dagger_codegen::engine::{
    BASELINE_CLIENT_GENERATION_JSON, ClientGenerationMetadata, ContentDomain, EntrypointInput,
    ModuleProjectionInput, OperationKind, OperationProjectionRequest, ProductionRenderers,
    PublishedSdkDependency, RelativeOperationPath, project_operation, project_operation_with,
};
use dagger_codegen::target::CodegenTarget;

use support::{CORE_SCHEMA_BYTES, TARGET_BYTES, VisibleSchemaCase, visible_schema};

fn target() -> CodegenTarget {
    CodegenTarget::decode_exact(TARGET_BYTES).expect("checked target must decode")
}

fn module() -> ModuleProjectionInput {
    ModuleProjectionInput {
        name: "rust-probe".to_owned(),
        original_name: "RustProbe".to_owned(),
        source_subpath: RelativeOperationPath::parse("modules/rust-probe")
            .expect("fixture path must parse"),
        source_digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            .to_owned(),
    }
}

fn dependency() -> PublishedSdkDependency {
    PublishedSdkDependency::Registry {
        registry: "crates-io".to_owned(),
        exact_version: "1.0.0-beta.10".to_owned(),
    }
}

fn entrypoint() -> EntrypointInput {
    EntrypointInput::decode_checked(
        &EntrypointInput::checked_bytes().expect("checked TypeDef must encode"),
    )
    .expect("checked TypeDef must decode")
}

#[test]
fn production_renderers_emit_bounded_operation_specific_artifacts() {
    let target = target();
    let module = module();
    let dependency = dependency();
    let entrypoint = entrypoint();
    let output = RelativeOperationPath::parse("candidate").expect("fixture path must parse");
    let schema = visible_schema(VisibleSchemaCase::CompatibleExtension, 0);

    let cases = [
        (OperationKind::GenerateLibrary, None, None),
        (OperationKind::GenerateModule, Some(&module), None),
        (OperationKind::GenerateClient, Some(&module), None),
        (
            OperationKind::GenerateEntrypoint,
            Some(&module),
            Some(&entrypoint),
        ),
    ];
    for (operation, module, entrypoint) in cases {
        let plan = project_operation(OperationProjectionRequest {
            target: &target,
            operation,
            visible_schema_json: &schema,
            module,
            output: &output,
            sdk_dependency: &dependency,
            entrypoint,
        })
        .expect("valid operation fixture must render");

        assert_eq!(plan.operation(), operation);
        assert_eq!(plan.projection_pass_limit(), 1);
        assert!(
            plan.artifacts()
                .keys()
                .all(|path| path.as_str().starts_with("candidate/"))
        );
        match operation {
            OperationKind::GenerateLibrary => {
                assert_eq!(plan.content_domain(), ContentDomain::VisibleSchemaBindings);
                assert!(
                    plan.artifacts().contains_key(
                        &RelativeOperationPath::parse("candidate/rust_mode.rs")
                            .expect("expected path must parse")
                    )
                );
                assert!(plan.client_generation().is_none());
            }
            OperationKind::GenerateModule => {
                assert_eq!(plan.content_domain(), ContentDomain::ModuleOperation);
                assert!(
                    plan.artifacts().contains_key(
                        &RelativeOperationPath::parse("candidate/src/bin/dagger-module.rs")
                            .expect("expected path must parse")
                    )
                );
                assert!(
                    !plan
                        .artifacts()
                        .keys()
                        .any(|path| { path.as_str().ends_with("operation-manifest.json") })
                );
                let binary = plan
                    .cargo_binary()
                    .expect("module operation must declare its Cargo target");
                assert_eq!(binary.name, "dagger-module");
                assert_eq!(binary.path.as_str(), "src/bin/dagger-module.rs");
            }
            OperationKind::GenerateClient => {
                assert_eq!(plan.content_domain(), ContentDomain::EngineHookBaseline);
                let manifest = plan
                    .artifacts()
                    .get(
                        &RelativeOperationPath::parse("candidate/Cargo.toml")
                            .expect("expected path must parse"),
                    )
                    .expect("client baseline must contain Cargo.toml");
                let manifest =
                    std::str::from_utf8(&manifest.content).expect("Cargo manifest must be UTF-8");
                assert!(manifest.contains("content-domain = \"engine-hook-baseline\""));
                assert!(manifest.contains("version = \"=1.0.0-beta.10\""));
                assert_eq!(
                    plan.client_generation()
                        .expect("client output must carry metadata")
                        .required_host_files
                        .len(),
                    0
                );
            }
            OperationKind::GenerateEntrypoint => {
                assert_eq!(plan.content_domain(), ContentDomain::ProtocolProbe);
                assert_eq!(plan.artifacts().len(), 1);
                let source = &plan
                    .artifacts()
                    .values()
                    .next()
                    .expect("entrypoint artifact must exist")
                    .content;
                let source = std::str::from_utf8(source).expect("entrypoint must be UTF-8");
                assert!(source.contains("Builder::new_current_thread"));
                assert!(source.contains("current_function_call"));
                assert!(source.contains("return_value"));
                assert_eq!(
                    plan.cargo_binary()
                        .expect("entrypoint operation must declare its Cargo target")
                        .name,
                    "dagger-module"
                );
            }
        }
    }
}

#[test]
fn operation_matrix_rejects_missing_and_forbidden_inputs_before_rendering() {
    let target = target();
    let module = module();
    let dependency = dependency();
    let entrypoint = entrypoint();
    let output = RelativeOperationPath::parse("candidate").expect("fixture path must parse");
    let renderer = ProductionRenderers::baseline();

    for (operation, module, entrypoint, expected) in [
        (
            OperationKind::GenerateModule,
            None,
            None,
            DiagnosticCode::OperationInputMissing,
        ),
        (
            OperationKind::GenerateClient,
            None,
            None,
            DiagnosticCode::OperationInputMissing,
        ),
        (
            OperationKind::GenerateEntrypoint,
            Some(&module),
            None,
            DiagnosticCode::OperationInputMissing,
        ),
        (
            OperationKind::GenerateLibrary,
            None,
            Some(&entrypoint),
            DiagnosticCode::OperationInputForbidden,
        ),
    ] {
        let diagnostics = project_operation_with(
            &renderer,
            OperationProjectionRequest {
                target: &target,
                operation,
                visible_schema_json: CORE_SCHEMA_BYTES,
                module,
                output: &output,
                sdk_dependency: &dependency,
                entrypoint,
            },
        )
        .expect_err("invalid operation matrix entry must fail");
        assert!(diagnostics.contains(expected));
    }
}

#[test]
fn client_generation_metadata_accepts_only_unique_normalized_relative_paths() {
    let finite = ClientGenerationMetadata::try_new(["Cargo.toml", "src/lib.rs"])
        .expect("finite normalized set must validate");
    assert_eq!(finite.required_host_files.len(), 2);
    assert!(ClientGenerationMetadata::try_new(std::iter::empty()).is_ok());
    for invalid in [
        "/Cargo.toml",
        "../Cargo.toml",
        "src/./lib.rs",
        "src//lib.rs",
    ] {
        let diagnostics =
            ClientGenerationMetadata::try_new([invalid]).expect_err("non-canonical path must fail");
        assert!(diagnostics.contains(DiagnosticCode::RequiredHostFileInvalid));
    }
    let duplicate = ClientGenerationMetadata::try_new(["Cargo.toml", "Cargo.toml"])
        .expect_err("duplicate required file must fail");
    assert!(duplicate.contains(DiagnosticCode::RequiredHostFileInvalid));
    for invalid_json in [
        br#"{"format_version":2,"required_host_files":[]}"#.as_slice(),
        br#"{"format_version":1,"required_host_files":["/Cargo.toml"]}"#.as_slice(),
        br#"{"format_version":1,"required_host_files":["Cargo.toml","Cargo.toml"]}"#.as_slice(),
    ] {
        assert!(serde_json::from_slice::<ClientGenerationMetadata>(invalid_json).is_err());
    }

    let packaged = BASELINE_CLIENT_GENERATION_JSON;
    assert_eq!(
        packaged.strip_suffix(b"\n").unwrap_or(packaged),
        ClientGenerationMetadata::baseline()
            .encode()
            .expect("baseline metadata must encode")
    );
}

#[test]
fn unknown_operation_selector_is_rejected_as_typed_input() {
    let diagnostics = OperationKind::decode("generate-everything")
        .expect_err("unknown operation selector must fail");
    assert!(diagnostics.contains(DiagnosticCode::OperationUnknown));
}
