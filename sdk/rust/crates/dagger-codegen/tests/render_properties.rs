//! Correctness properties for rendered API ownership and exhaustive verification.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::OnceLock;

use dagger_codegen::projection::fields::{ArgumentPresence, FieldStrategy};
use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{
    CoreProjectionRequest, ProjectionPlan, RenderedCandidate, project_core, render_core,
};
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
        .expect("checked target must project")
    })
}

fn candidate() -> &'static RenderedCandidate {
    static CANDIDATE: OnceLock<RenderedCandidate> = OnceLock::new();
    CANDIDATE.get_or_init(|| render_core(exact_plan()).expect("checked target must render"))
}

fn property_config() -> Config {
    Config {
        cases: 256,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/render.txt"
        )))),
        ..Config::default()
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct OptionsModel(BTreeMap<String, serde_json::Value>);

type EncodedArgument = (String, serde_json::Value);

#[derive(Clone, Debug, Eq, PartialEq)]
enum EncodingOutcome {
    Success {
        encoded: Vec<EncodedArgument>,
        events: Vec<&'static str>,
    },
    Failure {
        events: Vec<&'static str>,
    },
}

fn encode_options(
    field: &dagger_codegen::projection::fields::FieldProjection,
    options: &OptionsModel,
    fail_before_transport: bool,
) -> EncodingOutcome {
    let mut encoded = Vec::new();
    let mut events = vec!["construct"];
    for argument in &field.arguments {
        if argument.presence == ArgumentPresence::Required {
            continue;
        }
        if let Some(value) = options.0.get(argument.rust_name.as_str()) {
            if fail_before_transport {
                events.push("encoding-error");
                return EncodingOutcome::Failure { events };
            }
            encoded.push((argument.wire_name.to_string(), value.clone()));
        }
    }
    events.push("transport");
    EncodingOutcome::Success { encoded, events }
}

proptest! {
    #![proptest_config(property_config())]

    // Feature: rust-sdk-core-codegen, Property 14: Options are owned, wire-exact, and reusable
    #[test]
    fn property_14_options_owned_wire_exact_reusable(
        field_index in any::<usize>(),
        supplied_bits in prop::collection::vec(any::<bool>(), 0..32),
        zero_kind in 0_u8..6,
        fail_encoding in any::<bool>(),
    ) {
        let option_fields = exact_plan()
            .fields()
            .values()
            .filter(|field| field.options_type_name.is_some())
            .collect::<Vec<_>>();
        let field = option_fields[field_index % option_fields.len()];
        let concrete = match zero_kind {
            0 => serde_json::json!(false),
            1 => serde_json::json!(0),
            2 => serde_json::json!(0.0),
            3 => serde_json::json!(""),
            4 => serde_json::json!([]),
            _ => serde_json::json!("Default"),
        };
        let optional = field
            .arguments
            .iter()
            .filter(|argument| argument.presence.is_omittable())
            .collect::<Vec<_>>();
        let state = optional
            .iter()
            .enumerate()
            .filter(|(index, _)| supplied_bits.get(*index).copied().unwrap_or(false))
            .map(|(_, argument)| (argument.rust_name.clone(), concrete.clone()))
            .collect::<BTreeMap<_, _>>();
        let options = OptionsModel(state);
        let before = options.clone();

        let first = encode_options(field, &options, fail_encoding);
        let second = encode_options(field, &options, fail_encoding);
        prop_assert_eq!(&options, &before);
        prop_assert_eq!(&first, &second);

        match first {
            EncodingOutcome::Failure { events } => {
                prop_assert!(fail_encoding);
                prop_assert_eq!(events, vec!["construct", "encoding-error"]);
            }
            EncodingOutcome::Success { encoded, events } => {
                prop_assert_eq!(events, vec!["construct", "transport"]);
                let expected = optional
                    .iter()
                    .filter_map(|argument| {
                        options.0.get(argument.rust_name.as_str()).map(|value| {
                            (argument.wire_name.to_string(), value.clone())
                        })
                    })
                    .collect::<Vec<_>>();
                prop_assert_eq!(encoded, expected);
            }
        }

        let options_name = field.options_type_name.as_deref().expect("options type");
        let compact_sources = candidate()
            .artifacts()
            .iter()
            .filter(|(path, _)| path.starts_with("crates/dagger-sdk/src/gen/"))
            .map(|(_, bytes)| String::from_utf8_lossy(bytes).split_whitespace().collect::<String>())
            .collect::<String>();
        let struct_pattern = format!("pubstruct{}", options_name);
        let borrow_pattern = format!("opts:&{}", options_name);
        let lifetime_pattern = format!("struct{}<", options_name);
        prop_assert!(compact_sources.contains(&struct_pattern));
        prop_assert!(compact_sources.contains(&borrow_pattern));
        prop_assert!(!compact_sources.contains(&lifetime_pattern));
    }

    // Feature: rust-sdk-core-codegen, Property 22: The supported public surface respects release policy
    #[test]
    fn property_22_supported_public_surface_respects_release_policy(
        symbol_index in any::<usize>(),
        remove_symbol in any::<bool>(),
        add_unknown in any::<bool>(),
    ) {
        let expected = candidate().verification().public_symbols().clone();
        let mut observed = candidate().verification().referenced_symbols().clone();
        if remove_symbol && !observed.is_empty() {
            let symbol = observed.iter().nth(symbol_index % observed.len())
                .expect("non-empty symbol set")
                .clone();
            observed.remove(&symbol);
        }
        if add_unknown {
            observed.insert(format!("dagger_sdk::__Unknown{symbol_index}"));
        }
        prop_assert_eq!(observed == expected, !remove_symbol && !add_unknown);
        prop_assert_eq!(exact_plan().target().rust_version().to_string(), "1.97.1");
        prop_assert_eq!(exact_plan().target().rust_edition().as_str(), "2024");
    }

    // Feature: rust-sdk-core-codegen, Property 28: Query projection covers every wire coordinate
    #[test]
    fn property_28_query_projection_covers_every_wire_coordinate(
        field_index in any::<usize>(),
        supplied in any::<bool>(),
        concrete_kind in 0_u8..6,
    ) {
        let expected_fields = exact_plan().fields().keys().cloned().collect::<BTreeSet<_>>();
        let observed_fields = candidate()
            .verification()
            .query_cases()
            .keys()
            .cloned()
            .collect::<BTreeSet<_>>();
        prop_assert_eq!(&observed_fields, &expected_fields);
        prop_assert_eq!(observed_fields.len(), 720);

        let expected_arguments = exact_plan()
            .fields()
            .values()
            .flat_map(|field| field.arguments.iter().map(|argument| argument.coordinate.clone()))
            .collect::<BTreeSet<_>>();
        let observed_arguments = candidate()
            .verification()
            .query_cases()
            .values()
            .flat_map(|field| field.arguments.iter().map(|argument| argument.coordinate.clone()))
            .collect::<BTreeSet<_>>();
        prop_assert_eq!(&observed_arguments, &expected_arguments);
        prop_assert_eq!(observed_arguments.len(), 611);

        let fields = exact_plan().fields().values().collect::<Vec<_>>();
        let field = fields[field_index % fields.len()];
        let observed = candidate().verification().query_cases().get(&field.coordinate)
            .expect("every field must have a generated projection case");
        prop_assert_eq!(observed.field_wire_name.as_str(), field.wire_name.as_str());
        prop_assert_eq!(&observed.strategy, &field.strategy);
        prop_assert_eq!(
            matches!(observed.strategy, FieldStrategy::TargetPrivate),
            matches!(field.strategy, FieldStrategy::TargetPrivate),
        );

        let concrete = match concrete_kind {
            0 => serde_json::json!(false),
            1 => serde_json::json!(0),
            2 => serde_json::json!(0.0),
            3 => serde_json::json!(""),
            4 => serde_json::json!([]),
            _ => serde_json::json!("Default"),
        };
        for argument in &field.arguments {
            let emitted = argument.emitted_argument(supplied.then_some(&concrete));
            match (&argument.presence, supplied) {
                (ArgumentPresence::Required, false) => prop_assert!(emitted.is_err()),
                (_, false) => prop_assert_eq!(emitted.expect("omittable absence"), None),
                (_, true) => {
                    let (wire_name, value) = emitted
                        .expect("concrete input")
                        .expect("concrete input must emit");
                    prop_assert_eq!(wire_name, &argument.wire_name);
                    prop_assert_eq!(value, &concrete);
                }
            }
        }
    }
}
