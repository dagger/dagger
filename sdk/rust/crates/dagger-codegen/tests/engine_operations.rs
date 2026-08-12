//! Fixed operation-matrix, production-renderer, and client-metadata regressions.

mod support;

use dagger_codegen::diagnostic::DiagnosticCode;
use dagger_codegen::engine::{
    BASELINE_CLIENT_GENERATION_JSON, ClientGenerationMetadata, ContentDomain,
    ModuleProjectionInput, OperationKind, OperationProjectionRequest, ProductionRenderers,
    PublishedSdkDependency, REQUIRED_CLIENT_HOST_FILES, RelativeOperationPath, project_operation,
    project_operation_with,
};
use dagger_codegen::target::CodegenTarget;

use support::{
    CORE_SCHEMA_BYTES, ClientSchemaCase, TARGET_BYTES, VisibleSchemaCase, client_visible_schema,
    module_authoring_input, module_visible_schema, visible_schema,
};

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

fn client_module() -> ModuleProjectionInput {
    ModuleProjectionInput {
        name: "minimal".to_owned(),
        original_name: "Minimal".to_owned(),
        source_subpath: RelativeOperationPath::parse("modules/minimal")
            .expect("fixture path must parse"),
        source_digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
            .to_owned(),
    }
}

#[test]
fn production_renderers_emit_bounded_operation_specific_artifacts() {
    let target = target();
    let module = module();
    let client_module = client_module();
    let dependency = dependency();
    let authoring = module_authoring_input();
    let output = RelativeOperationPath::parse("candidate").expect("fixture path must parse");
    for operation in [
        OperationKind::GenerateLibrary,
        OperationKind::GenerateModule,
        OperationKind::GenerateClient,
        OperationKind::GenerateEntrypoint,
    ] {
        let schema = if operation == OperationKind::GenerateClient {
            client_visible_schema(ClientSchemaCase::Valid, 0)
        } else {
            visible_schema(VisibleSchemaCase::EngineModuleExtension, 0)
        };
        let selected_module = match operation {
            OperationKind::GenerateLibrary => None,
            OperationKind::GenerateClient => Some(&client_module),
            OperationKind::GenerateModule | OperationKind::GenerateEntrypoint => Some(&module),
        };
        let selected_authoring = matches!(
            operation,
            OperationKind::GenerateModule | OperationKind::GenerateEntrypoint
        )
        .then_some(&authoring);
        let plan = project_operation(OperationProjectionRequest {
            target: &target,
            operation,
            visible_schema_json: &schema,
            module: selected_module,
            output: &output,
            sdk_dependency: &dependency,
            authoring: selected_authoring,
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
                let registration = plan
                    .artifacts()
                    .get(
                        &RelativeOperationPath::parse(
                            "candidate/src/dagger_generated/module_registration.rs",
                        )
                        .expect("registration path must parse"),
                    )
                    .expect("module operation must contain active registration");
                let registration = std::str::from_utf8(&registration.content)
                    .expect("registration source must be UTF-8");
                for expected in [
                    "with_cache_policy(dagger_sdk::FunctionCachePolicy::Never)",
                    ".with_check()",
                    ".with_default_path(\".\")",
                    ".with_ignore(vec![\"target\"])",
                ] {
                    assert!(
                        registration.contains(expected),
                        "registration omitted {expected}"
                    );
                }
                let binary = plan
                    .cargo_binary()
                    .expect("module operation must declare its Cargo target");
                assert_eq!(binary.name, "dagger-module");
                assert_eq!(binary.path.as_str(), "src/bin/dagger-module.rs");
            }
            OperationKind::GenerateClient => {
                assert_eq!(plan.content_domain(), ContentDomain::StandaloneClient);
                assert!(plan.artifacts().keys().any(|path| {
                    path.as_str() == "candidate/src/dagger_client/generated/binding-catalog.json"
                }));
                assert!(
                    !plan
                        .artifacts()
                        .keys()
                        .any(|path| path.as_str().ends_with("Cargo.toml"))
                );
                assert_eq!(
                    plan.client_generation()
                        .expect("client output must carry metadata")
                        .required_host_files
                        .len(),
                    REQUIRED_CLIENT_HOST_FILES.len()
                );
            }
            OperationKind::GenerateEntrypoint => {
                assert_eq!(plan.content_domain(), ContentDomain::ModuleEntrypoint);
                assert_eq!(plan.artifacts().len(), 1);
                let source = &plan
                    .artifacts()
                    .values()
                    .next()
                    .expect("entrypoint artifact must exist")
                    .content;
                let source = std::str::from_utf8(source).expect("entrypoint must be UTF-8");
                assert!(source.contains("Builder::new_current_thread"));
                assert!(source.contains("run_module_entrypoint"));
                assert!(source.contains("GeneratedDispatchRegistry"));
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
fn source_map_required_argument_compatibility_is_engine_specific() {
    let target = target();
    let dependency = dependency();
    let output = RelativeOperationPath::parse("candidate").expect("fixture path must parse");
    let schema = visible_schema(VisibleSchemaCase::EngineModuleExtension, 0);

    project_operation(OperationProjectionRequest {
        target: &target,
        operation: OperationKind::GenerateLibrary,
        visible_schema_json: &schema,
        module: None,
        output: &output,
        sdk_dependency: &dependency,
        authoring: None,
    })
    .expect("engine-authored module-only source maps must project");

    let mut invalid = Vec::new();
    for mutation in 0..3 {
        let mut document: serde_json::Value =
            serde_json::from_slice(&schema).expect("source-map fixture must decode");
        let extension = document["__schema"]["types"]
            .as_array_mut()
            .expect("fixture types must be an array")
            .iter_mut()
            .find(|definition| definition["name"] == "RustMode")
            .expect("fixture must contain the module extension");
        match mutation {
            0 => extension["directives"][0]["args"] = serde_json::json!([]),
            1 => {
                extension["directives"][0]["args"][0]["value"] = serde_json::Value::Null;
            }
            _ => {
                extension["directives"] = serde_json::json!([{
                    "name": "expectedType",
                    "args": []
                }])
            }
        }
        invalid.push(serde_json::to_vec(&document).expect("source-map mutation must encode"));
    }
    for schema in invalid {
        let diagnostics = project_operation(OperationProjectionRequest {
            target: &target,
            operation: OperationKind::GenerateLibrary,
            visible_schema_json: &schema,
            module: None,
            output: &output,
            sdk_dependency: &dependency,
            authoring: None,
        })
        .expect_err("required directive arguments outside the engine exception must fail");
        assert!(diagnostics.contains(DiagnosticCode::SchemaDirectiveArgumentInvalid));
    }
}

#[test]
fn module_visibility_accepts_only_the_target_introspection_scrub() {
    let target = target();
    let module = module();
    let dependency = dependency();
    let output = RelativeOperationPath::parse("candidate").expect("fixture path must parse");
    let schema = module_visible_schema(0);

    let plan = project_operation(OperationProjectionRequest {
        target: &target,
        operation: OperationKind::GenerateModule,
        visible_schema_json: &schema,
        module: Some(&module),
        output: &output,
        sdk_dependency: &dependency,
        authoring: Some(&module_authoring_input()),
    })
    .expect("the exact target module scrub must remain compatible");
    for (path, artifact) in plan.artifacts() {
        if artifact.kind == dagger_codegen::engine::CandidateArtifactKind::RustSource {
            let source = std::str::from_utf8(&artifact.content)
                .unwrap_or_else(|_| panic!("{} must be UTF-8", path.as_str()));
            syn::parse_file(source)
                .unwrap_or_else(|error| panic!("{} must parse: {error}", path.as_str()));
        }
    }
    let directory = tempfile::tempdir().expect("formatter fixture root must be created");
    let mut rust_paths = Vec::new();
    for (path, artifact) in plan.artifacts() {
        if artifact.kind != dagger_codegen::engine::CandidateArtifactKind::RustSource {
            continue;
        }
        let destination = directory.path().join(path.as_str());
        std::fs::create_dir_all(
            destination
                .parent()
                .expect("generated artifact must have a parent"),
        )
        .expect("generated artifact parent must be created");
        std::fs::write(&destination, &artifact.content)
            .expect("generated artifact must be written");
        rust_paths.push(path.as_str().to_owned());
    }
    let formatted = std::process::Command::new("rustfmt")
        .arg("+1.97.1")
        .args(["--edition", "2024"])
        .args(&rust_paths)
        .current_dir(directory.path())
        .output()
        .expect("pinned rustfmt must start");
    assert!(
        formatted.status.success(),
        "module-visible artifacts must format: {}",
        String::from_utf8_lossy(&formatted.stderr)
    );

    let diagnostics = project_operation(OperationProjectionRequest {
        target: &target,
        operation: OperationKind::GenerateClient,
        visible_schema_json: &schema,
        module: Some(&module),
        output: &output,
        sdk_dependency: &dependency,
        authoring: None,
    })
    .expect_err("client generation requires the complete client-visible core");
    assert!(
        diagnostics.contains(DiagnosticCode::SchemaCoreCoordinateMissing)
            || diagnostics.contains(DiagnosticCode::SchemaReferenceInvalid)
    );

    let mut unexpected: serde_json::Value =
        serde_json::from_slice(&schema).expect("module-visible fixture must decode");
    let query = unexpected["__schema"]["types"]
        .as_array_mut()
        .expect("fixture types must be an array")
        .iter_mut()
        .find(|definition| definition["name"] == "Query")
        .expect("fixture must contain Query");
    query["fields"]
        .as_array_mut()
        .expect("Query fields must be an array")
        .retain(|field| field["name"] != "address");
    let unexpected = serde_json::to_vec(&unexpected).expect("fixture must encode");
    let diagnostics = project_operation(OperationProjectionRequest {
        target: &target,
        operation: OperationKind::GenerateModule,
        visible_schema_json: &unexpected,
        module: Some(&module),
        output: &output,
        sdk_dependency: &dependency,
        authoring: Some(&module_authoring_input()),
    })
    .expect_err("an unrelated module-schema omission must remain incompatible");
    assert!(diagnostics.contains(DiagnosticCode::SchemaCoreCoordinateMissing));
}

#[test]
fn operation_matrix_rejects_missing_and_forbidden_inputs_before_rendering() {
    let target = target();
    let module = module();
    let dependency = dependency();
    let authoring = module_authoring_input();
    let output = RelativeOperationPath::parse("candidate").expect("fixture path must parse");
    let renderer = ProductionRenderers::baseline();

    for (operation, module, authoring, expected) in [
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
            Some(&authoring),
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
                authoring,
            },
        )
        .expect_err("invalid operation matrix entry must fail");
        assert!(diagnostics.contains(expected));
    }
}

#[test]
fn client_generation_metadata_accepts_only_the_reviewed_canonical_set() {
    let finite = ClientGenerationMetadata::try_new(REQUIRED_CLIENT_HOST_FILES)
        .expect("reviewed finite set must validate");
    assert_eq!(
        finite
            .required_host_files
            .iter()
            .map(RelativeOperationPath::as_str)
            .collect::<Vec<_>>(),
        REQUIRED_CLIENT_HOST_FILES
    );
    for invalid in [
        Vec::new(),
        vec!["**/Cargo.toml"],
        REQUIRED_CLIENT_HOST_FILES.into_iter().rev().collect(),
        vec![
            "**/.gitattributes",
            "**/Cargo.toml",
            "**/Cargo.toml",
            "**/rust-toolchain",
            "**/rust-toolchain.toml",
            "**/src/lib.rs",
        ],
        vec![
            "**/.gitattributes",
            "**/Cargo.lock",
            "**/README.md",
            "**/rust-toolchain",
            "**/rust-toolchain.toml",
            "**/src/lib.rs",
        ],
    ] {
        let diagnostics = ClientGenerationMetadata::try_new(invalid)
            .expect_err("alternate host projection must fail");
        assert!(diagnostics.contains(DiagnosticCode::RequiredHostFileInvalid));
        assert_eq!(
            diagnostics.diagnostics()[0]
                .coordinate
                .as_ref()
                .map(|coordinate| coordinate.as_str()),
            Some("client-generation.required-host-files")
        );
    }
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
