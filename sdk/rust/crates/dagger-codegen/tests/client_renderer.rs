//! Standalone-client artifact shape, documentation, and drift regressions.

mod support;

use std::collections::BTreeSet;
use std::fs;
use std::path::Path;

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
            "EngineConnection",
            "unsafe {",
            "#![allow(",
            "dagger_codegen",
            "dagger_sdk_engine",
            "dagger_sdk_completeness",
            "dagger_bootstrap",
            "std::fs",
            "std::process",
            "tokio::process",
            "todo!(",
            "unimplemented!(",
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
    assert!(client.contains("pub(in crate::dagger_client) fn from_query"));
    assert!(client.contains("generated_core_handle::<dagger_sdk::Container>()"));
    assert!(client.contains("query.select(\"id\").execute().await?"));
    assert!(client.contains("generated_reentry_builder"));
    assert!(client.contains("generated_argument_id_shape(\"item\", item.into())"));
    assert!(client.contains("generated_argument_id_shape(\"items\", items.into())"));
    assert!(client.contains("pub async fn sync(&self) -> Result<super::Client"));
    assert!(!client.contains("generated_reenter_shape"));
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
    let item = source(
        &rendered,
        "client/src/dagger_client/generated/minimal/item.rs",
    );
    assert!(item.contains("dagger_sdk::IdInput::generated_lazy(value)"));
    assert!(item.contains("pub(in crate::dagger_client) fn from_query"));
}

#[test]
fn checked_runtime_fixture_matches_the_production_renderer() {
    let rendered = render(0);
    let fixture_root =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../dagger-sdk/tests/fixtures/generated_client");
    let mut expected = BTreeSet::new();
    for (path, artifact) in &rendered.artifacts {
        let Some(relative) = path
            .as_str()
            .strip_prefix("client/src/dagger_client/")
            .filter(|relative| relative.ends_with(".rs"))
        else {
            continue;
        };
        expected.insert(relative.to_owned());
        let fixture = fixture_root.join(relative);
        if std::env::var_os("DAGGER_UPDATE_GENERATED_CLIENT_FIXTURE").is_some() {
            fs::create_dir_all(
                fixture
                    .parent()
                    .expect("generated fixture source has a parent"),
            )
            .expect("generated fixture directory is writable");
            fs::write(&fixture, &artifact.content).expect("generated fixture is writable");
        }
        let actual = fs::read_to_string(&fixture).expect("checked generated fixture must exist");
        let expected_source = std::str::from_utf8(&artifact.content)
            .expect("production renderer emits UTF-8 Rust source");
        assert_eq!(
            actual.lines().take(2).collect::<Vec<_>>(),
            expected_source.lines().take(2).collect::<Vec<_>>(),
            "{} provenance drifted from the production renderer",
            fixture.display()
        );
        assert_eq!(
            format_insensitive_tokens(&actual),
            format_insensitive_tokens(expected_source),
            "{} semantics drifted from the production renderer",
            fixture.display()
        );
    }

    let mut actual = Vec::new();
    collect_fixture_sources(&fixture_root, &fixture_root, &mut actual);
    assert_eq!(actual.into_iter().collect::<BTreeSet<_>>(), expected);
}

fn format_insensitive_tokens(source: &str) -> Vec<String> {
    fn flatten(stream: proc_macro2::TokenStream, tokens: &mut Vec<String>) {
        for token in stream {
            match token {
                proc_macro2::TokenTree::Group(group) => flatten(group.stream(), tokens),
                proc_macro2::TokenTree::Punct(punctuation) if punctuation.as_char() == ',' => {}
                token => tokens.push(token.to_string()),
            }
        }
    }

    let stream = source
        .parse::<proc_macro2::TokenStream>()
        .expect("generated fixture tokenizes");
    let mut tokens = Vec::new();
    flatten(stream, &mut tokens);
    tokens
}

fn collect_fixture_sources(root: &Path, directory: &Path, files: &mut Vec<String>) {
    let mut entries = fs::read_dir(directory)
        .expect("generated fixture directory is readable")
        .collect::<Result<Vec<_>, _>>()
        .expect("generated fixture entries are readable");
    entries.sort_by_key(fs::DirEntry::file_name);
    for entry in entries {
        let path = entry.path();
        let file_type = entry
            .file_type()
            .expect("generated fixture type is readable");
        assert!(
            !file_type.is_symlink(),
            "generated fixture cannot be a symlink"
        );
        if file_type.is_dir() {
            collect_fixture_sources(root, &path, files);
        } else if path.extension().is_some_and(|extension| extension == "rs") {
            files.push(
                path.strip_prefix(root)
                    .expect("fixture remains below its root")
                    .components()
                    .map(|component| component.as_os_str().to_string_lossy())
                    .collect::<Vec<_>>()
                    .join("/"),
            );
        }
    }
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
