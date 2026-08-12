//! Standalone-client artifact shape, documentation, and drift regressions.

mod support;

use std::collections::BTreeSet;

use dagger_codegen::client::{
    CargoPackageName, ClientCompilationInput, ClientProjectIdentity, RustIdentifier,
    compile_client, render_client,
};
use dagger_codegen::engine::{
    ContentDomain, ModuleProjectionInput, OperationKind, OperationProjectionRequest,
    PublishedSdkDependency, RelativeOperationPath, project_operation, project_visible_schema,
};
use dagger_codegen::target::CodegenTarget;

use support::{ClientSchemaCase, TARGET_BYTES, client_visible_schema};

fn inputs(
    permutation: u16,
) -> (
    CodegenTarget,
    Vec<u8>,
    ModuleProjectionInput,
    ClientProjectIdentity,
) {
    let target = CodegenTarget::decode_exact(TARGET_BYTES).expect("checked target must decode");
    let module = ModuleProjectionInput {
        name: "minimal".to_owned(),
        original_name: "Minimal fixture".to_owned(),
        source_subpath: RelativeOperationPath::parse("module").expect("fixture path must validate"),
        source_digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            .to_owned(),
    };
    let project = ClientProjectIdentity {
        package_name: CargoPackageName::new("adopted-client")
            .expect("fixture package must validate"),
        crate_name: RustIdentifier::new("adopted_client").expect("fixture crate must validate"),
    };
    (
        target,
        client_visible_schema(ClientSchemaCase::Valid, permutation),
        module,
        project,
    )
}

fn render(permutation: u16) -> dagger_codegen::client::RenderedClient {
    let (target, schema, module, project) = inputs(permutation);
    let visible = project_visible_schema(&target, &schema).expect("fixture schema must project");
    let plan = compile_client(ClientCompilationInput {
        target: &target,
        visible_schema: &visible,
        module: &module,
        project: &project,
    })
    .expect("fixture client must compile");
    render_client(
        &plan,
        &RelativeOperationPath::parse("client").expect("fixture path must validate"),
    )
    .expect("fixture client must render")
}

#[test]
fn production_renderer_emits_only_the_standalone_generated_subtree() {
    let rendered = render(0);
    let paths = rendered
        .artifacts
        .keys()
        .map(|path| path.as_str())
        .collect::<BTreeSet<_>>();
    assert_eq!(
        paths,
        BTreeSet::from([
            "client/examples/dagger-client-quickstart.rs",
            "client/src/dagger_client/generated/binding-catalog.json",
            "client/src/dagger_client/generated/minimal/client.rs",
            "client/src/dagger_client/generated/minimal/config.rs",
            "client/src/dagger_client/generated/minimal/item.rs",
            "client/src/dagger_client/generated/minimal/minimal_client.rs",
            "client/src/dagger_client/generated/minimal/mod.rs",
            "client/src/dagger_client/generated/minimal/node.rs",
            "client/src/dagger_client/generated/minimal/state.rs",
            "client/src/dagger_client/generated/minimal/token.rs",
            "client/src/dagger_client/generated/mod.rs",
            "client/src/dagger_client/mod.rs",
        ])
    );
    assert!(paths.iter().all(|path| !path.ends_with("Cargo.toml")));
    assert!(paths.iter().all(|path| !path.ends_with("src/lib.rs")));
    assert_eq!(rendered.rust_sources.len(), 11);
    assert_eq!(
        rendered.catalog.generated.len(),
        rendered
            .catalog
            .generated
            .iter()
            .map(|binding| &binding.binding.key)
            .collect::<BTreeSet<_>>()
            .len()
    );
}

#[test]
fn generated_sources_are_documented_safe_and_credential_free() {
    let rendered = render(0);
    for (path, artifact) in &rendered.artifacts {
        if !path.as_str().ends_with(".rs") {
            continue;
        }
        let source = std::str::from_utf8(&artifact.content).expect("Rust source must be UTF-8");
        let parsed = syn::parse_file(source).expect("generated source must parse");
        assert_public_docs(&parsed.items, path.as_str());
        assert!(source.starts_with("//!"));
        for forbidden in [
            "https://user:token@",
            "authorization",
            "/Users/",
            "SessionHandle",
            "unsafe {",
            "EngineHookBaseline",
            "engine-hook-baseline",
            "TODO",
            "Feature ",
            "Task ",
            "Property ",
        ] {
            assert!(
                !source.contains(forbidden),
                "{} contains forbidden text {forbidden}",
                path.as_str()
            );
        }
    }
    let top = source(&rendered, "client/src/dagger_client/mod.rs");
    assert!(top.contains("pub use dagger_sdk as core;"));
    assert!(top.contains("pub trait MinimalExt"));
    assert!(top.contains("select(\"minimal\")"));
    assert!(top.contains("pub use super::MinimalExt as _;"));
    let client = source(
        &rendered,
        "client/src/dagger_client/generated/minimal/client.rs",
    );
    assert!(client.contains("pub async fn r#type"));
    assert!(client.contains("pub fn helper(&self) -> super::MinimalClient"));
    let quickstart = source(&rendered, "client/examples/dagger-client-quickstart.rs");
    assert!(quickstart.contains("use adopted_client::dagger_client;"));
    let config = source(
        &rendered,
        "client/src/dagger_client/generated/minimal/config.rs",
    );
    assert!(config.contains("pub enabled: Option<Option<bool>>"));
    assert!(config.contains("pub fn with_enabled(mut self, value: bool)"));
    assert!(config.contains("pub fn with_enabled_null(mut self)"));
    assert!(client.contains("config: Option<Option<super::Config>>"));
    assert!(client.contains("pub fn with_config(mut self, value: super::Config)"));
    assert!(client.contains("pub fn with_config_null(mut self)"));
    let scalar = source(
        &rendered,
        "client/src/dagger_client/generated/minimal/token.rs",
    );
    assert!(scalar.contains("pub struct Token("));
}

fn assert_public_docs(items: &[syn::Item], path: &str) {
    for item in items {
        match item {
            syn::Item::Enum(item) if is_public(&item.vis) => {
                assert!(has_doc(&item.attrs), "{path}: public enum needs rustdoc");
                for variant in &item.variants {
                    assert!(
                        has_doc(&variant.attrs),
                        "{path}: enum variant needs rustdoc"
                    );
                }
            }
            syn::Item::Struct(item) if is_public(&item.vis) => {
                assert!(has_doc(&item.attrs), "{path}: public struct needs rustdoc");
                for field in &item.fields {
                    if is_public(&field.vis) {
                        assert!(has_doc(&field.attrs), "{path}: public field needs rustdoc");
                    }
                }
            }
            syn::Item::Trait(item) if is_public(&item.vis) => {
                assert!(has_doc(&item.attrs), "{path}: public trait needs rustdoc");
                for member in &item.items {
                    if let syn::TraitItem::Fn(method) = member {
                        assert!(has_doc(&method.attrs), "{path}: trait method needs rustdoc");
                    }
                }
            }
            syn::Item::Fn(item) if is_public(&item.vis) => {
                assert!(
                    has_doc(&item.attrs),
                    "{path}: public function needs rustdoc"
                );
            }
            syn::Item::Mod(item) if is_public(&item.vis) => {
                assert!(has_doc(&item.attrs), "{path}: public module needs rustdoc");
                if let Some((_, items)) = &item.content {
                    assert_public_docs(items, path);
                }
            }
            syn::Item::Impl(item) if item.trait_.is_none() => {
                for member in &item.items {
                    if let syn::ImplItem::Fn(method) = member
                        && is_public(&method.vis)
                    {
                        assert!(
                            has_doc(&method.attrs),
                            "{path}: public method needs rustdoc"
                        );
                    }
                }
            }
            _ => {}
        }
    }
}

fn has_doc(attributes: &[syn::Attribute]) -> bool {
    attributes
        .iter()
        .any(|attribute| attribute.path().is_ident("doc"))
}

fn is_public(visibility: &syn::Visibility) -> bool {
    matches!(visibility, syn::Visibility::Public(_))
}

#[test]
fn declaration_permutations_render_byte_identically() {
    let baseline = render(0);
    for permutation in [1, 17, u16::MAX] {
        assert_eq!(render(permutation), baseline);
    }
}

#[test]
fn shared_projection_renderer_remains_total_for_module_custom_scalars() {
    let (target, schema, _, _) = inputs(0);
    let visible = project_visible_schema(&target, &schema).expect("fixture schema must project");
    let rendered = dagger_codegen::render_core(visible.projection())
        .expect("shared renderer must account for admitted custom scalars");
    let scalar = rendered
        .artifacts()
        .values()
        .filter_map(|content| std::str::from_utf8(content).ok())
        .find(|source| source.contains("pub struct MinimalToken"))
        .expect("shared renderer must emit the custom scalar binding");
    assert!(scalar.contains("serde"));
    assert!(scalar.contains("transparent"));
}

#[test]
fn operation_adapter_uses_the_standalone_content_domain() {
    let (target, schema, module, _) = inputs(0);
    let output = RelativeOperationPath::parse("client").expect("fixture path must validate");
    let dependency = PublishedSdkDependency::Registry {
        registry: "crates-io".to_owned(),
        exact_version: "1.0.0-beta.10".to_owned(),
    };
    let plan = project_operation(OperationProjectionRequest {
        target: &target,
        operation: OperationKind::GenerateClient,
        visible_schema_json: &schema,
        module: Some(&module),
        output: &output,
        sdk_dependency: &dependency,
        authoring: None,
    })
    .expect("production client operation must render");
    assert_eq!(plan.content_domain(), ContentDomain::StandaloneClient);
    assert!(
        plan.artifacts()
            .keys()
            .all(|path| !path.as_str().ends_with("Cargo.toml"))
    );
}

fn source<'a>(rendered: &'a dagger_codegen::client::RenderedClient, path: &str) -> &'a str {
    let artifact = rendered
        .artifacts
        .iter()
        .find(|(candidate, _)| candidate.as_str() == path)
        .map(|(_, artifact)| artifact)
        .expect("fixture artifact must exist");
    std::str::from_utf8(&artifact.content).expect("fixture source must be UTF-8")
}
