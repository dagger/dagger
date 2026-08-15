//! Correctness properties for the source-free semantic projection plan.

use std::sync::OnceLock;

use dagger_codegen::diagnostic::DiagnosticCode;
use dagger_codegen::directive::DirectivePolicy;
use dagger_codegen::projection::catalog::{BindingKind, SemanticDigest};
use dagger_codegen::projection::fields::{
    ArgumentPresence, FieldLeafClass, FieldStrategy, FieldStrategyInput, FieldStrategyKind,
    select_field_strategy,
};
use dagger_codegen::projection::types::{
    EnumProjection, RustType, ScalarKind, TypeProjection, project_input_type, project_output_type,
};
use dagger_codegen::schema::canonical::{SchemaName, TypeShape, TypeUse};
use dagger_codegen::schema::defaults::ConstValue;
use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{CoreProjectionRequest, ProjectionPlan, project_core};
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const TARGET: &[u8] = include_bytes!("../../../codegen/target.json");
const SCHEMA: &[u8] = include_bytes!("../../../codegen/schema.json");

fn exact_plan() -> &'static ProjectionPlan {
    static PLAN: OnceLock<ProjectionPlan> = OnceLock::new();
    PLAN.get_or_init(|| {
        let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
        project_core(CoreProjectionRequest {
            target: &target,
            schema_json: SCHEMA,
        })
        .expect("checked target must project completely")
    })
}

fn property_config() -> Config {
    Config {
        cases: 256,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/projection.txt"
        )))),
        ..Config::default()
    }
}

fn wrapper_strategy() -> impl Strategy<Value = TypeUse> {
    any::<bool>()
        .prop_map(|nullable| TypeUse {
            nullable,
            shape: TypeShape::Named(
                SchemaName::try_from("String").expect("String is a valid GraphQL name"),
            ),
        })
        .prop_recursive(8, 128, 2, |element| {
            (any::<bool>(), element).prop_map(|(nullable, element)| TypeUse {
                nullable,
                shape: TypeShape::List(Box::new(element)),
            })
        })
}

fn reference_string_type(type_use: &TypeUse, omit_outer: bool) -> RustType {
    let mut projected = match &type_use.shape {
        TypeShape::Named(_) => RustType::String,
        TypeShape::List(element) => RustType::Vec(Box::new(reference_string_type(element, false))),
    };
    if type_use.nullable && !omit_outer {
        projected = RustType::Option(Box::new(projected));
    }
    projected
}

fn enums(plan: &ProjectionPlan) -> Vec<&EnumProjection> {
    plan.named_types()
        .values()
        .filter_map(|projection| {
            if let TypeProjection::Enum(enumeration) = projection {
                Some(enumeration)
            } else {
                None
            }
        })
        .collect()
}

proptest! {
    #![proptest_config(property_config())]

    // Feature: rust-sdk-core-codegen, Property 6: Recursive wrappers preserve independent absence
    #[test]
    fn property_06_recursive_wrappers_preserve_independent_absence(
        type_use in wrapper_strategy(),
        has_default in any::<bool>(),
    ) {
        let plan = exact_plan();
        let (output, decode) = project_output_type(plan.schema(), &type_use)
            .expect("generated String wrapper must project");
        prop_assert_eq!(&output, &reference_string_type(&type_use, false));
        prop_assert_eq!(&decode.wrappers, &(&type_use).into());

        let default = has_default.then(|| ConstValue::String("engine".to_owned()));
        let presence = ArgumentPresence::for_input(&type_use, default.as_ref());
        let omit_outer = presence.is_omittable();
        let input = project_input_type(plan.schema(), &type_use, omit_outer, None)
            .expect("generated String input wrapper must project");
        prop_assert_eq!(input, reference_string_type(&type_use, omit_outer));
        prop_assert_eq!(
            matches!(presence, ArgumentPresence::Required),
            !type_use.nullable && !has_default,
        );
    }

    // Feature: rust-sdk-core-codegen, Property 7: Scalar projection and decoding are exact
    #[test]
    fn property_07_scalar_projection_decoding_exact(
        scalar_index in any::<usize>(),
        valid in any::<bool>(),
        text in ".{0,32}",
        integer in any::<i64>(),
        boolean in any::<bool>(),
    ) {
        let scalars = [
            ScalarKind::Boolean,
            ScalarKind::Float,
            ScalarKind::Int,
            ScalarKind::String,
            ScalarKind::Id,
            ScalarKind::Json,
            ScalarKind::Platform,
            ScalarKind::Void,
            ScalarKind::Custom,
        ];
        let scalar = scalars[scalar_index % scalars.len()];
        let value = if valid {
            match scalar {
                ScalarKind::Boolean => serde_json::json!(boolean),
                ScalarKind::Float => serde_json::json!(integer as f64 / 2.0),
                ScalarKind::Int => serde_json::json!(integer),
                ScalarKind::String
                | ScalarKind::Id
                | ScalarKind::Platform
                | ScalarKind::Custom => {
                    serde_json::Value::String(text.clone())
                }
                ScalarKind::Json => serde_json::Value::String(
                    serde_json::to_string(&serde_json::json!({"value": text}))
                        .expect("property JSON value must serialize"),
                ),
                ScalarKind::Void => serde_json::Value::Null,
            }
        } else {
            match scalar {
                ScalarKind::Boolean => serde_json::json!("false"),
                ScalarKind::Float => serde_json::json!("1.5"),
                ScalarKind::Int => serde_json::json!(1.5),
                ScalarKind::String
                | ScalarKind::Id
                | ScalarKind::Platform
                | ScalarKind::Custom => {
                    serde_json::json!({"not": "a string"})
                }
                ScalarKind::Json => serde_json::json!("{") ,
                ScalarKind::Void => serde_json::json!(false),
            }
        };
        prop_assert_eq!(scalar.accepts_wire(&value), valid);
    }

    // Feature: rust-sdk-core-codegen, Property 8: Named-type and field projection is exhaustive
    #[test]
    fn property_08_named_type_field_projection_exhaustive(
        field_index in any::<usize>(),
        type_index in any::<usize>(),
        list in any::<bool>(),
        nullable in any::<bool>(),
        has_id in any::<bool>(),
        expected_type_self in any::<bool>(),
        target_private in any::<bool>(),
        leaf_index in 0_u8..3,
    ) {
        let plan = exact_plan();
        let fields = plan.fields().values().collect::<Vec<_>>();
        let field = fields[field_index % fields.len()];
        prop_assert_eq!(plan.fields().get(&field.coordinate), Some(field));
        let field_is_cataloged = plan.catalog().bindings().keys().any(|key| {
            key.wire_coordinate.as_ref() == Some(&field.coordinate)
                && matches!(
                    key.binding_kind,
                    BindingKind::FieldOperation | BindingKind::TargetPrivateField
                )
        });
        prop_assert!(field_is_cataloged);
        let strategy_is_total = matches!(
            field.strategy,
            FieldStrategy::LazyHandle { .. }
                | FieldStrategy::NullableHandle { .. }
                | FieldStrategy::ReenterList { .. }
                | FieldStrategy::ExecuteValue { .. }
                | FieldStrategy::ExpectedTypeSelf { .. }
                | FieldStrategy::TargetPrivate
        );
        prop_assert!(strategy_is_total);

        let types = plan.named_types().iter().collect::<Vec<_>>();
        let (name, projection) = types[type_index % types.len()];
        prop_assert_eq!(plan.named_types().get(name), Some(projection));

        let leaf = match leaf_index {
            0 => FieldLeafClass::Value,
            1 => FieldLeafClass::Object { has_id },
            _ => FieldLeafClass::Interface { has_id },
        };
        let input = FieldStrategyInput {
            list,
            nullable,
            leaf,
            expected_type_self,
            target_private,
        };
        let expected = if target_private {
            Ok(FieldStrategyKind::TargetPrivate)
        } else if expected_type_self {
            Ok(FieldStrategyKind::ExpectedTypeSelf)
        } else {
            match leaf {
                FieldLeafClass::Value => Ok(FieldStrategyKind::ExecuteValue),
                FieldLeafClass::Object { .. } | FieldLeafClass::Interface { .. }
                    if list && has_id => Ok(FieldStrategyKind::ReenterList),
                FieldLeafClass::Object { .. } | FieldLeafClass::Interface { .. }
                    if list => Err(DiagnosticCode::ListReentryTypeInvalid),
                FieldLeafClass::Object { .. } | FieldLeafClass::Interface { .. }
                    if nullable && has_id => Ok(FieldStrategyKind::NullableHandle),
                FieldLeafClass::Object { .. } | FieldLeafClass::Interface { .. }
                    if nullable => Err(DiagnosticCode::ObjectHandleMappingInvalid),
                FieldLeafClass::Object { .. } | FieldLeafClass::Interface { .. } => {
                    Ok(FieldStrategyKind::LazyHandle)
                }
            }
        };
        prop_assert_eq!(select_field_strategy(input), expected);
    }

    // Feature: rust-sdk-core-codegen, Property 13: Argument omission is distinct from zero-like values
    #[test]
    fn property_13_argument_omission_distinct_zero_like_values(
        argument_index in any::<usize>(),
        supplied in any::<bool>(),
        zero_kind in 0_u8..6,
    ) {
        let plan = exact_plan();
        let arguments = plan.fields().values()
            .flat_map(|field| field.arguments.iter())
            .collect::<Vec<_>>();
        let argument = arguments[argument_index % arguments.len()];
        let concrete = match zero_kind {
            0 => serde_json::json!(false),
            1 => serde_json::json!(0),
            2 => serde_json::json!(0.0),
            3 => serde_json::json!(""),
            4 => serde_json::json!([]),
            _ => serde_json::json!("Default"),
        };
        let result = argument.emitted_argument(supplied.then_some(&concrete));
        match (&argument.presence, supplied) {
            (ArgumentPresence::Required, false) => prop_assert!(result.is_err()),
            (_, false) => prop_assert_eq!(result.expect("omittable absence must resolve"), None),
            (_, true) => {
                let (wire_name, value) = result
                    .expect("concrete input must resolve")
                    .expect("concrete input must emit");
                prop_assert_eq!(wire_name, &argument.wire_name);
                prop_assert_eq!(value, &concrete);
            }
        }
    }

    // Feature: rust-sdk-core-codegen, Property 17: Enum mapping preserves canonical wire values and aliases
    #[test]
    fn property_17_enum_mapping_preserves_canonical_values_aliases(
        enum_index in any::<usize>(),
        value_index in any::<usize>(),
        unknown_suffix in "[A-Za-z0-9]{1,16}",
    ) {
        let enumerations = enums(exact_plan());
        let enumeration = enumerations[enum_index % enumerations.len()];
        let coordinates = enumeration
            .variants
            .keys()
            .map(|name| (name, name))
            .chain(enumeration.aliases.iter().map(|(name, alias)| (name, &alias.canonical_wire_name)))
            .collect::<Vec<_>>();
        let (wire_name, canonical) = coordinates[value_index % coordinates.len()];
        let decoded = enumeration
            .decode_variant(wire_name.as_str())
            .expect("canonical and alias Wire_Names must decode");
        prop_assert_eq!(&decoded.wire_name, canonical);
        prop_assert_eq!(
            enumeration.decode_variant(&format!("__UNKNOWN_{unknown_suffix}")),
            None,
        );
    }

    // Feature: rust-sdk-core-codegen, Property 18: Input objects preserve requiredness and concrete values
    #[test]
    fn property_18_input_objects_preserve_requiredness_concrete_values(
        field_index in any::<usize>(),
        supplied in any::<bool>(),
        zero in any::<i64>(),
    ) {
        let fields = exact_plan().named_types().values().filter_map(|projection| {
            if let TypeProjection::InputObject(input) = projection {
                Some(input.fields.values())
            } else {
                None
            }
        }).flatten().collect::<Vec<_>>();
        let field = fields[field_index % fields.len()];
        let value = serde_json::json!(zero);
        let result = field.presence.resolve(supplied.then_some(&value));
        match (&field.presence, supplied) {
            (ArgumentPresence::Required, false) => prop_assert!(result.is_err()),
            (_, false) => prop_assert_eq!(result.expect("optional field must omit"), None),
            (_, true) => prop_assert_eq!(
                result.expect("concrete field must resolve"),
                Some(&value),
            ),
        }
    }

    // Feature: rust-sdk-core-codegen, Property 19: Directive projection is explicit and drift-sensitive
    #[test]
    fn property_19_directive_projection_explicit_drift_sensitive(
        directive_index in any::<usize>(),
        suffix in "[A-Za-z0-9]{1,16}",
        byte_index in any::<usize>(),
    ) {
        let plan = exact_plan();
        let records = plan.directives().records().values().collect::<Vec<_>>();
        let record = records[directive_index % records.len()];
        if record.policy == DirectivePolicy::TargetInactive {
            prop_assert!(!record.policy.accepts_applications());
            prop_assert!(record.applications.is_empty());
        } else {
            prop_assert!(record.policy.accepts_applications());
        }

        let definition = plan.schema().directives().get(&record.name)
            .expect("policy record must retain its canonical definition");
        let mut changed = definition.clone();
        changed.description = Some(format!("changed-{suffix}"));
        let changed_fingerprint = SemanticDigest::for_value(&changed)
            .expect("changed directive definition must fingerprint");
        prop_assert_ne!(&changed_fingerprint, &record.definition_fingerprint);

        let target = CodegenTarget::decode_exact(TARGET).expect("checked target must decode");
        let mut changed_schema = SCHEMA.to_vec();
        let changed_index = byte_index % changed_schema.len();
        changed_schema[changed_index] ^= 1;
        let drift_rejected = target.verify_schema(&changed_schema).is_err();
        prop_assert!(drift_rejected);
    }
}
