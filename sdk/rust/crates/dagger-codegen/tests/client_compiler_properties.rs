//! Exact client-schema scope, Core identity, typed closure, and naming properties.

mod support;

use std::collections::{BTreeMap, BTreeSet};
use std::sync::OnceLock;

use dagger_codegen::client::{
    CargoPackageName, ClientCompilationInput, ClientNameRole, ClientProjectIdentity,
    ClientSchemaSurface, RustIdentifier, compile_client,
};
use dagger_codegen::diagnostic::DiagnosticCode;
use dagger_codegen::engine::{
    ModuleProjectionInput, RelativeOperationPath, project_visible_schema,
};
use dagger_codegen::projection::catalog::BindingKind;
use dagger_codegen::projection::fields::{ArgumentPresence, FieldStrategy};
use dagger_codegen::projection::types::{TypeProjection, WrapperShape};
use dagger_codegen::target::CodegenTarget;
use proptest::prelude::*;

use support::{
    ClientSchemaCase, TARGET_BYTES, client_visible_schema, named_client_visible_schema, pure_config,
};

fn case(discriminant: u8) -> ClientSchemaCase {
    match discriminant % 10 {
        0 => ClientSchemaCase::CoreOnly,
        1 => ClientSchemaCase::Valid,
        2 => ClientSchemaCase::CoreMutation,
        3 => ClientSchemaCase::CoreOmission,
        4 => ClientSchemaCase::WrongRootName,
        5 => ClientSchemaCase::PromotedFunction,
        6 => ClientSchemaCase::MultipleRoots,
        7 => ClientSchemaCase::NullableRoot,
        8 => ClientSchemaCase::DependencyLeakage,
        _ => ClientSchemaCase::UnreachableExtension,
    }
}

fn compile_uncached(
    case: ClientSchemaCase,
    permutation: u16,
) -> Result<dagger_codegen::client::ClientBindingPlan, dagger_codegen::diagnostic::DiagnosticSet> {
    compile_schema(client_visible_schema(case, permutation), "minimal")
}

fn compile_named_uncached(
    module_name: &str,
    root_name: &str,
    permutation: u16,
) -> Result<dagger_codegen::client::ClientBindingPlan, dagger_codegen::diagnostic::DiagnosticSet> {
    compile_schema(
        named_client_visible_schema(module_name, root_name, permutation),
        module_name,
    )
}

fn compile_schema(
    schema: Vec<u8>,
    module_name: &str,
) -> Result<dagger_codegen::client::ClientBindingPlan, dagger_codegen::diagnostic::DiagnosticSet> {
    let target = CodegenTarget::decode_exact(TARGET_BYTES).expect("checked target must decode");
    let visible = project_visible_schema(&target, &schema)?;
    let module = ModuleProjectionInput {
        name: module_name.to_owned(),
        original_name: module_name.to_owned(),
        source_subpath: RelativeOperationPath::parse("module").expect("fixture path must validate"),
        source_digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            .to_owned(),
    };
    let project = ClientProjectIdentity {
        package_name: CargoPackageName::new("minimal-dagger-client")
            .expect("fixture package must validate"),
        crate_name: RustIdentifier::new("minimal_dagger_client")
            .expect("fixture crate must validate"),
    };
    compile_client(ClientCompilationInput {
        target: &target,
        visible_schema: &visible,
        module: &module,
        project: &project,
    })
}

const NAMING_CASES: &[(&str, &str, bool)] = &[
    ("minimal", "Minimal", true),
    ("async", "Async", true),
    ("type", "Type", true),
    ("myURL", "MyURL", true),
    ("core", "Core", false),
    ("generated", "Generated", false),
];

static NAMED_CASES: OnceLock<
    Vec<
        Result<
            dagger_codegen::client::ClientBindingPlan,
            dagger_codegen::diagnostic::DiagnosticSet,
        >,
    >,
> = OnceLock::new();

fn compile_named(
    index: usize,
) -> Result<dagger_codegen::client::ClientBindingPlan, dagger_codegen::diagnostic::DiagnosticSet> {
    NAMED_CASES.get_or_init(|| {
        NAMING_CASES
            .iter()
            .map(|(module, root, _)| compile_named_uncached(module, root, 0))
            .collect()
    })[index]
        .clone()
}

static COMPILED_CASES: OnceLock<
    BTreeMap<
        ClientSchemaCase,
        Result<
            dagger_codegen::client::ClientBindingPlan,
            dagger_codegen::diagnostic::DiagnosticSet,
        >,
    >,
> = OnceLock::new();

fn compile(
    case: ClientSchemaCase,
) -> Result<dagger_codegen::client::ClientBindingPlan, dagger_codegen::diagnostic::DiagnosticSet> {
    COMPILED_CASES
        .get_or_init(|| {
            [
                ClientSchemaCase::CoreOnly,
                ClientSchemaCase::Valid,
                ClientSchemaCase::CoreMutation,
                ClientSchemaCase::CoreOmission,
                ClientSchemaCase::WrongRootName,
                ClientSchemaCase::PromotedFunction,
                ClientSchemaCase::MultipleRoots,
                ClientSchemaCase::NullableRoot,
                ClientSchemaCase::DependencyLeakage,
                ClientSchemaCase::UnreachableExtension,
            ]
            .into_iter()
            .map(|case| (case, compile_uncached(case, 0)))
            .collect()
        })
        .get(&case)
        .expect("every closed fixture case must be cached")
        .clone()
}

proptest! {
    #![proptest_config(pure_config())]

    #[test]
    fn property_05_client_visible_schema_exact_core_one_module_closure(
        discriminant in any::<u8>(),
        permutation in any::<u16>(),
    ) {
        let case = case(discriminant);
        let baseline = compile(case);
        let permuted = compile(case);
        match case {
            ClientSchemaCase::CoreOnly => {
                let baseline = baseline.expect("Core-only client must compile");
                prop_assert_eq!(&baseline.surface, &ClientSchemaSurface::CoreOnly);
                prop_assert_eq!(permuted.expect("permuted Core-only client must compile"), baseline);
            }
            ClientSchemaCase::Valid => {
                let baseline = baseline.expect("Core-plus-module client must compile");
                let surface = match &baseline.surface {
                    ClientSchemaSurface::BoundModule(surface) => surface,
                    ClientSchemaSurface::CoreOnly => return Err(TestCaseError::fail("module surface missing")),
                };
                prop_assert_eq!(surface.root.field_coordinate.as_str(), "Query.minimal");
                prop_assert_eq!(surface.root.object_coordinate.as_str(), "Minimal");
                let catalog_is_exhaustive = surface.closure.iter().all(|coordinate| {
                    baseline.generated_bindings.keys().any(|key| {
                        key.wire_coordinate.as_ref() == Some(coordinate)
                    })
                });
                prop_assert!(catalog_is_exhaustive);
                let mut declaration_order = surface.closure.iter().cloned().collect::<Vec<_>>();
                if !declaration_order.is_empty() {
                    let offset = usize::from(permutation) % declaration_order.len();
                    declaration_order.rotate_left(offset);
                }
                prop_assert_eq!(
                    declaration_order.into_iter().collect::<BTreeSet<_>>(),
                    surface.closure.clone()
                );
                prop_assert_eq!(permuted.expect("permuted module client must compile"), baseline);
            }
            ClientSchemaCase::CoreMutation => {
                let baseline = baseline.expect_err("incompatible Core must fail");
                let permuted = permuted.expect_err("permuted incompatible Core must fail");
                prop_assert!(baseline.contains(DiagnosticCode::SchemaCoreCoordinateIncompatible));
                prop_assert_eq!(permuted, baseline);
            }
            ClientSchemaCase::CoreOmission => {
                let baseline = baseline.expect_err("incomplete Core must fail");
                let permuted = permuted.expect_err("permuted incomplete Core must fail");
                prop_assert!(baseline.contains(DiagnosticCode::SchemaCoreCoordinateMissing));
                prop_assert_eq!(permuted, baseline);
            }
            ClientSchemaCase::WrongRootName
            | ClientSchemaCase::PromotedFunction
            | ClientSchemaCase::MultipleRoots
            | ClientSchemaCase::NullableRoot => {
                let baseline = baseline.expect_err("invalid module root must fail");
                let permuted = permuted.expect_err("permuted invalid module root must fail");
                prop_assert!(baseline.contains(DiagnosticCode::ClientModuleRootInvalid));
                prop_assert_eq!(permuted, baseline);
            }
            ClientSchemaCase::DependencyLeakage | ClientSchemaCase::UnreachableExtension => {
                let baseline = baseline.expect_err("invalid module scope must fail");
                let permuted = permuted.expect_err("permuted invalid module scope must fail");
                prop_assert!(baseline.contains(DiagnosticCode::ClientSchemaScopeInvalid));
                prop_assert_eq!(permuted, baseline);
            }
        }
    }

    #[test]
    fn property_06_core_reused_by_identity_not_regenerated(
        permutation in any::<u16>(),
        mutation in 0_u8..4,
    ) {
        let mut plan = compile(ClientSchemaCase::Valid)
            .expect("valid client must compile");
        let expected = plan.core_bindings.clone();
        match mutation {
            0 => {}
            1 => {
                let index = usize::from(permutation) % plan.core_bindings.len();
                if let Some(key) = plan.core_bindings.keys().nth(index).cloned() {
                    plan.core_bindings.remove(&key);
                }
            }
            2 => {
                let index = usize::from(permutation) % plan.core_bindings.len();
                if let Some(key) = plan.core_bindings.keys().nth(index).cloned()
                    && let Some(binding) = plan.core_bindings.get_mut(&key)
                {
                    binding.public_path = Some("dagger_sdk::DriftedBinding".to_owned());
                }
            }
            _ => {
                plan.core_bindings.clear();
            }
        }
        if mutation == 0 {
            prop_assert_eq!(&plan.core_bindings, &expected);
            let all_core_paths_are_public = plan.core_bindings.values().all(|binding| {
                binding.public_path.as_deref().is_none_or(|path| {
                    !path.contains("crate::")
                        && (path.contains("dagger_sdk::") || path == "()")
                })
            });
            prop_assert!(all_core_paths_are_public);
            let generated_paths_are_isolated = plan.generated_bindings.values().all(|binding| {
                binding.binding.key.rust_symbol.as_deref().is_none_or(|path| {
                    !path.contains("crate::gen::") && !path.contains("SessionHandle")
                })
            });
            prop_assert!(generated_paths_are_isolated);
        } else {
            let output = dagger_codegen::client::render_client(
                &plan,
                &RelativeOperationPath::parse("client").expect("fixture path must validate"),
            );
            prop_assert!(output.is_err());
        }
    }

    #[test]
    fn property_08_generated_module_surface_exhaustive_typed_closure(
        permutation in any::<u16>(),
    ) {
        let plan = compile(ClientSchemaCase::Valid)
            .expect("valid module surface must compile");
        let surface = match &plan.surface {
            ClientSchemaSurface::BoundModule(surface) => surface,
            ClientSchemaSurface::CoreOnly => return Err(TestCaseError::fail("module surface missing")),
        };
        let mut catalog_order = plan.generated_bindings.keys().filter_map(|key| {
            key.wire_coordinate.clone()
        }).collect::<Vec<_>>();
        if !catalog_order.is_empty() {
            let offset = usize::from(permutation) % catalog_order.len();
            catalog_order.rotate_left(offset);
        }
        let catalog_coordinates = catalog_order.into_iter().collect::<BTreeSet<_>>();
        let implementation_coordinates = plan.module_implementations.iter()
            .map(|edge| edge.coordinate.clone()).collect::<BTreeSet<_>>();
        let expected = surface.closure.union(&implementation_coordinates)
            .cloned().collect::<BTreeSet<_>>();
        prop_assert_eq!(catalog_coordinates, expected);
        let all_paths_are_namespaced = plan.generated_bindings.values().all(|binding| {
            binding.binding.key.rust_symbol.as_deref().is_none_or(|path| {
                path.starts_with("crate::dagger_client::minimal::")
                    || path.starts_with("crate::dagger_client::MinimalExt::")
                    || path.starts_with("impl crate::dagger_client::minimal::")
            })
        });
        prop_assert!(
            all_paths_are_namespaced,
            "unexpected paths: {:?}",
            plan.generated_bindings
                .values()
                .filter_map(|binding| binding.binding.key.rust_symbol.as_deref())
                .filter(|path| {
                    !path.starts_with("crate::dagger_client::minimal::")
                        && !path.starts_with("crate::dagger_client::MinimalExt::")
                        && !path.starts_with("impl crate::dagger_client::minimal::")
                })
                .collect::<Vec<_>>()
        );
        let item_field_is_bound = plan.generated_bindings.keys().any(|key| {
            key.binding_kind == BindingKind::FieldOperation
                && key.wire_coordinate.as_ref().is_some_and(|coordinate| coordinate.as_str() == "Minimal.item")
        });
        prop_assert!(item_field_is_bound);
        let item_options_are_bound = plan.generated_bindings.keys().any(|key| {
            key.binding_kind == BindingKind::FieldOptions
                && key.wire_coordinate.as_ref().is_some_and(|coordinate| coordinate.as_str() == "Minimal.item")
        });
        prop_assert!(item_options_are_bound);
    }

    #[test]
    fn property_10_module_public_naming_deterministic_collision_free(
        permutation in any::<u16>(),
        discriminant in any::<u8>(),
    ) {
        let index = usize::from(discriminant) % NAMING_CASES.len();
        let (_, _, accepted) = NAMING_CASES[index];
        let result = compile_named(index);
        if !accepted {
            let diagnostics = result.expect_err("reserved client namespace must fail");
            prop_assert!(diagnostics.contains(DiagnosticCode::RustNameCollision));
            return Ok(());
        }
        let baseline = result.expect("valid client names must compile");
        let mut permuted_bindings = baseline.names.as_ref()
            .expect("module names must exist")
            .bindings.iter().collect::<Vec<_>>();
        if !permuted_bindings.is_empty() {
            let offset = usize::from(permutation) % permuted_bindings.len();
            permuted_bindings.rotate_left(offset);
        }
        let permuted_bindings = permuted_bindings.into_iter()
            .map(|(key, value)| (key.clone(), value.clone()))
            .collect::<BTreeMap<_, _>>();
        let names = baseline.names.as_ref().expect("module names must exist");
        let unique = names.bindings.values().map(|name| name.as_str()).collect::<BTreeSet<_>>();
        prop_assert_eq!(unique.len(), names.bindings.len());
        let root = match &baseline.surface {
            ClientSchemaSurface::BoundModule(surface) => &surface.root.object_coordinate,
            ClientSchemaSurface::CoreOnly => return Err(TestCaseError::fail("module surface missing")),
        };
        prop_assert_eq!(
            names.get(root, ClientNameRole::Object).map(|name| name.as_str()),
            Some("Client")
        );
        prop_assert_eq!(&permuted_bindings, &names.bindings);
        if NAMING_CASES[index].0 == "async" {
            prop_assert_eq!(names.namespace.as_str(), "r#async");
        }
    }
}

#[test]
fn valid_client_closure_contains_every_module_coordinate_once() {
    let plan = compile(ClientSchemaCase::Valid).expect("valid client must compile");
    let surface = match &plan.surface {
        ClientSchemaSurface::BoundModule(surface) => surface,
        ClientSchemaSurface::CoreOnly => panic!("fixture must have module surface"),
    };
    for coordinate in [
        "Query.minimal",
        "Minimal",
        "Minimal.id",
        "Minimal.helper",
        "Minimal.item",
        "Minimal.item(config:)",
        "Minimal.items",
        "Minimal.message",
        "Minimal.node",
        "Minimal.token",
        "Minimal.type",
        "MinimalClient",
        "MinimalClient.id",
        "MinimalConfig",
        "MinimalConfig.enabled",
        "MinimalItem",
        "MinimalItem.id",
        "MinimalItem.state",
        "MinimalNode",
        "MinimalNode.id",
        "MinimalNode.message",
        "MinimalState",
        "MinimalState.BUSY",
        "MinimalState.READY",
        "MinimalToken",
    ] {
        assert!(
            surface
                .closure
                .iter()
                .any(|candidate| candidate.as_str() == coordinate)
        );
    }
}

#[test]
fn representative_schema_declaration_permutations_are_byte_identical() {
    let baseline =
        compile_uncached(ClientSchemaCase::Valid, 0).expect("baseline schema must compile");
    for permutation in [1, 17, u16::MAX] {
        assert_eq!(
            compile_uncached(ClientSchemaCase::Valid, permutation)
                .expect("representative schema permutation must compile"),
            baseline
        );
    }
}

#[test]
fn representative_module_name_permutations_are_byte_identical() {
    for (module, root, accepted) in NAMING_CASES {
        if !accepted {
            continue;
        }
        let baseline = compile_named_uncached(module, root, 0)
            .expect("representative module name must compile");
        for permutation in [1, 17, u16::MAX] {
            assert_eq!(
                compile_named_uncached(module, root, permutation)
                    .expect("representative naming permutation must compile"),
                baseline
            );
        }
    }
}

#[test]
fn wrapper_omission_reference_preserves_nested_absence_and_explicit_values() {
    let plan = compile(ClientSchemaCase::Valid).expect("valid client must compile");
    let items = plan
        .module_fields
        .values()
        .find(|field| field.coordinate.as_str() == "Minimal.items")
        .expect("fixture must project the nested-list field");
    assert_eq!(items.return_type.signature(), "Vec<Option<MinimalItem>>");
    match &items.strategy {
        FieldStrategy::ReenterList { wrappers, .. } => match &wrappers.shape {
            WrapperShape::List(element) => {
                assert!(!wrappers.nullable);
                assert!(element.nullable);
            }
            WrapperShape::Named(_) => panic!("fixture output must retain its list wrapper"),
        },
        _ => panic!("fixture output must use list handle re-entry"),
    }

    let enabled = match plan.module_types.get(
        &dagger_codegen::schema::canonical::SchemaName::try_from("MinimalConfig")
            .expect("fixture name must validate"),
    ) {
        Some(TypeProjection::InputObject(input)) => input
            .fields
            .values()
            .find(|field| field.wire_name.as_str() == "enabled")
            .expect("fixture input must retain enabled"),
        _ => panic!("fixture input object must project"),
    };
    assert!(matches!(
        enabled.presence,
        ArgumentPresence::Omittable { .. }
    ));
    assert_eq!(enabled.presence.resolve::<bool>(None), Ok(None));
    assert_eq!(enabled.presence.resolve(Some(&false)), Ok(Some(&false)));

    let item = plan
        .generated_bindings
        .values()
        .find(|binding| {
            binding.binding.key.binding_kind == BindingKind::FieldOperation
                && binding
                    .binding
                    .key
                    .wire_coordinate
                    .as_ref()
                    .is_some_and(|coordinate| coordinate.as_str() == "Minimal.item")
        })
        .expect("fixture field must have a catalog binding");
    assert_eq!(
        item.binding.rust_signature,
        "async fn item(&self) -> Result<Option<super::Item>, dagger_sdk::QueryError>; async fn item_opts(&self, opts: ItemOpts) -> Result<Option<super::Item>, dagger_sdk::QueryError>"
    );
    let enabled_binding = plan
        .generated_bindings
        .values()
        .find(|binding| {
            binding.binding.key.binding_kind == BindingKind::InputField
                && binding
                    .binding
                    .key
                    .wire_coordinate
                    .as_ref()
                    .is_some_and(|coordinate| coordinate.as_str() == "MinimalConfig.enabled")
        })
        .expect("fixture input must have a catalog binding");
    assert_eq!(
        enabled_binding.binding.rust_signature,
        "Option<Option<bool>>"
    );
}
