//! Exact-target renderer structure, provenance, and documentation verification.

use std::collections::BTreeSet;

use dagger_codegen::projection::fields::{ArgumentPresence, FieldStrategy};
use dagger_codegen::projection::types::TypeProjection;
use dagger_codegen::render::verification::{RequiredOmissionCase, RequiredOmissionKind};
use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{CoreProjectionRequest, project_core, render_core};
use quote::ToTokens;

const TARGET: &[u8] = include_bytes!("../../../completeness/target.json");
const SCHEMA: &[u8] = include_bytes!("../../../completeness/snapshots/schema.json");

fn render() -> dagger_codegen::RenderedCandidate {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked target must project");
    render_core(&plan).expect("checked target must render")
}

#[test]
fn exact_target_renders_one_parsed_module_per_emitted_named_type() {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked target must project");
    let candidate = render_core(&plan).expect("checked target must render");
    let emitted_types = plan
        .named_types()
        .values()
        .filter(|projection| {
            matches!(
                projection,
                TypeProjection::Object(_)
                    | TypeProjection::Interface(_)
                    | TypeProjection::Enum(_)
                    | TypeProjection::InputObject(_)
            )
        })
        .count();

    assert_eq!(candidate.artifacts().len(), emitted_types + 3);
    assert!(
        candidate
            .artifacts()
            .contains_key("crates/dagger-sdk/src/gen/mod.rs")
    );
    assert!(
        candidate
            .artifacts()
            .contains_key("crates/dagger-sdk/tests/core_reachability.rs")
    );
    assert!(
        candidate
            .artifacts()
            .contains_key("crates/dagger-sdk/tests/core_projection.rs")
    );
    assert_eq!(
        dagger_codegen::render::LEGACY_GENERATED_PREDECESSOR,
        "crates/dagger-sdk/src/gen.rs"
    );
    assert!(
        !candidate
            .artifacts()
            .contains_key(dagger_codegen::render::LEGACY_GENERATED_PREDECESSOR)
    );

    for (path, bytes) in candidate.artifacts() {
        let source = std::str::from_utf8(bytes).expect("generated source must be UTF-8");
        syn::parse_file(source).unwrap_or_else(|error| panic!("{path} must parse: {error}"));
        assert!(
            source.starts_with("//! "),
            "{path} must start with module rustdoc"
        );
        assert!(source.contains("// @generated {\"format\":\"dagger-rust-client-v1\""));
        assert!(source.contains(target.dagger_revision().as_str()));
        assert!(source.contains(&target.schema_digest().to_string()));
        assert!(!source.contains("#![allow(missing_docs)]"));
        assert!(!source.contains("#![allow(rustdoc::"));
        assert!(!source.contains("cargo fix"));
    }
}

#[test]
fn parsed_public_surface_equals_generated_reachability_and_catalog_coverage() {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked target must project");
    let candidate = render_core(&plan).expect("checked target must render");
    let verification = candidate.verification();

    assert_eq!(
        verification.public_symbols(),
        verification.referenced_symbols()
    );
    assert_eq!(
        verification.binding_tests().keys().collect::<BTreeSet<_>>(),
        plan.catalog().bindings().keys().collect::<BTreeSet<_>>()
    );
    assert_eq!(verification.query_cases().len(), 720);
    assert_eq!(
        verification
            .query_cases()
            .values()
            .map(|field| field.arguments.len())
            .sum::<usize>(),
        611
    );
    let mut expected_omissions = BTreeSet::new();
    for field in plan.fields().values() {
        if matches!(field.strategy, FieldStrategy::TargetPrivate) {
            continue;
        }
        for argument in &field.arguments {
            if argument.presence == ArgumentPresence::Required {
                expected_omissions.insert(RequiredOmissionCase {
                    coordinate: argument.coordinate.clone(),
                    kind: RequiredOmissionKind::MethodArgument,
                    public_name: format!("{}::{}", field.owner, field.rust_name),
                });
            }
        }
    }
    for projection in plan.named_types().values() {
        if let TypeProjection::InputObject(input) = projection {
            for field in input.fields.values() {
                if field.presence == ArgumentPresence::Required {
                    expected_omissions.insert(RequiredOmissionCase {
                        coordinate: field.coordinate.clone(),
                        kind: RequiredOmissionKind::InputField,
                        public_name: format!("{}::{}", input.rust_name, input.constructor_name),
                    });
                }
            }
        }
    }
    assert_eq!(verification.omission_cases(), &expected_omissions);
}

#[test]
fn every_rendered_operation_uses_exact_wire_names_and_its_selected_runtime_boundary() {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked target must project");
    let candidate = render_core(&plan).expect("checked target must render");

    for field in plan.fields().values() {
        if matches!(field.strategy, FieldStrategy::TargetPrivate) {
            continue;
        }
        let (module_name, handle_name) = match plan
            .named_types()
            .get(&field.owner)
            .expect("field owner must have a projection")
        {
            TypeProjection::Object(object) => (&object.module_name, &object.rust_name),
            TypeProjection::Interface(interface) => {
                (&interface.module_name, &interface.client_name)
            }
            _ => panic!("public field owner must be an object or interface"),
        };
        let file_name = module_name.strip_prefix("r#").unwrap_or(module_name);
        let path = format!("crates/dagger-sdk/src/gen/{file_name}.rs");
        let source = std::str::from_utf8(
            candidate
                .artifacts()
                .get(&path)
                .unwrap_or_else(|| panic!("{path} must be rendered")),
        )
        .expect("rendered module must be UTF-8");
        let file = syn::parse_file(source).expect("rendered module must parse");
        let method_name = field
            .options_method_name
            .as_deref()
            .unwrap_or(&field.rust_name);
        let method = file
            .items
            .iter()
            .filter_map(|item| {
                let syn::Item::Impl(implementation) = item else {
                    return None;
                };
                if implementation.trait_.is_some()
                    || impl_name(&implementation.self_ty).as_deref() != Some(handle_name.as_str())
                {
                    return None;
                }
                implementation.items.iter().find_map(|item| {
                    let syn::ImplItem::Fn(method) = item else {
                        return None;
                    };
                    (method.sig.ident == method_name).then_some(method)
                })
            })
            .next()
            .unwrap_or_else(|| panic!("{path} must contain {handle_name}::{method_name}"));
        let body = method
            .block
            .to_token_stream()
            .to_string()
            .split_whitespace()
            .collect::<String>();
        assert!(
            body.contains(&format!("select(\"{}\")", field.wire_name)),
            "{} must select exact Wire_Name {}",
            field.coordinate,
            field.wire_name
        );
        for argument in &field.arguments {
            assert!(
                body.contains(&format!("arg(\"{}\"", argument.wire_name))
                    || body.contains(&format!("arg_id_input(\"{}\"", argument.wire_name)),
                "{} must encode exact argument Wire_Name {}",
                field.coordinate,
                argument.wire_name
            );
        }
        match &field.strategy {
            FieldStrategy::LazyHandle { .. } => assert!(!body.contains(".execute")),
            FieldStrategy::NullableHandle { .. } | FieldStrategy::ReenterList { .. } => {
                assert!(body.contains("execute_reentry"));
            }
            FieldStrategy::ExecuteValue { .. } => assert!(body.contains("query.execute")),
            FieldStrategy::ExpectedTypeSelf { .. } => {
                assert!(body.contains("query.execute"));
                assert!(body.contains("query::reenter"));
            }
            FieldStrategy::TargetPrivate => unreachable!("filtered above"),
        }
    }
}

#[test]
fn every_public_generated_item_has_local_documentation() {
    let candidate = render();
    for (path, bytes) in candidate.artifacts() {
        if !path.starts_with("crates/dagger-sdk/src/gen/") || path.ends_with("/mod.rs") {
            continue;
        }
        let source = std::str::from_utf8(bytes).expect("generated source must be UTF-8");
        let file = syn::parse_file(source).expect("generated source must parse");
        for item in &file.items {
            match item {
                syn::Item::Struct(item) if public(&item.vis) => {
                    assert_doc(path, &item.ident.to_string(), &item.attrs);
                    if let syn::Fields::Named(fields) = &item.fields {
                        for field in &fields.named {
                            if public(&field.vis) {
                                assert_doc(
                                    path,
                                    &field.ident.as_ref().expect("named field").to_string(),
                                    &field.attrs,
                                );
                            }
                        }
                    }
                }
                syn::Item::Enum(item) if public(&item.vis) => {
                    assert_doc(path, &item.ident.to_string(), &item.attrs);
                    for variant in &item.variants {
                        assert_doc(path, &variant.ident.to_string(), &variant.attrs);
                    }
                }
                syn::Item::Trait(item) if public(&item.vis) => {
                    assert_doc(path, &item.ident.to_string(), &item.attrs);
                    for member in &item.items {
                        if let syn::TraitItem::Fn(method) = member {
                            assert_doc(path, &method.sig.ident.to_string(), &method.attrs);
                        }
                    }
                }
                syn::Item::Impl(item) if item.trait_.is_none() => {
                    for member in &item.items {
                        if let syn::ImplItem::Fn(method) = member
                            && public(&method.vis)
                        {
                            assert_doc(path, &method.sig.ident.to_string(), &method.attrs);
                        }
                    }
                }
                _ => {}
            }
        }
    }
}

fn public(visibility: &syn::Visibility) -> bool {
    matches!(visibility, syn::Visibility::Public(_))
}

fn impl_name(self_type: &syn::Type) -> Option<String> {
    let syn::Type::Path(path) = self_type else {
        return None;
    };
    path.path
        .segments
        .last()
        .map(|segment| segment.ident.to_string())
}

fn assert_doc(path: &str, item: &str, attributes: &[syn::Attribute]) {
    assert!(
        attributes
            .iter()
            .any(|attribute| attribute.path().is_ident("doc")),
        "{path}: public `{item}` must have local rustdoc"
    );
}
