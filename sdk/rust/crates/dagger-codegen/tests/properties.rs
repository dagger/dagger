//! Exact-target schema compiler correctness properties.

use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{CoreProjectionRequest, project_core, render_core};
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

const TARGET: &[u8] = include_bytes!("../../../codegen/target.json");
const SCHEMA: &[u8] = include_bytes!("../../../codegen/schema.json");

fn property_config() -> Config {
    Config {
        cases: 256,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/schema-target.txt"
        )))),
        ..Config::default()
    }
}

proptest! {
    #![proptest_config(property_config())]

    // Feature: rust-sdk-core-codegen, Property 3: Target identity gates all publication
    #[test]
    fn property_03_target_identity_gates_publication(
        mutation in 0_u8..6,
        byte_index in any::<usize>(),
        replacement in 1_u8..=u8::MAX,
    ) {
        let mut target_value: serde_json::Value =
            serde_json::from_slice(TARGET).expect("checked target fixture must decode");
        let mut schema = SCHEMA.to_vec();
        match mutation {
            0 => mutate_string(&mut target_value, "dagger_revision"),
            1 => mutate_string(&mut target_value, "engine_version"),
            2 => mutate_string(&mut target_value, "rust_version"),
            3 => mutate_string(&mut target_value, "schema_digest"),
            4 => {
                target_value
                    .as_object_mut()
                    .expect("checked target must be an object")
                    .insert("scope_digest".to_owned(), serde_json::Value::String("changed".to_owned()));
            }
            _ => {
                let index = byte_index % schema.len();
                schema[index] ^= replacement;
            }
        }
        let target_bytes = serde_json::to_vec(&target_value)
            .expect("mutated target fixture must serialize");

        let candidate = CodegenTarget::decode_exact(&target_bytes).ok().and_then(|target| {
            project_core(CoreProjectionRequest {
                target: &target,
                schema_json: &schema,
            })
            .ok()
            .and_then(|plan| render_core(&plan).ok())
        });

        prop_assert!(candidate.is_none());
    }
}

fn mutate_string(target: &mut serde_json::Value, field: &str) {
    let value = target
        .get_mut(field)
        .and_then(|value| value.as_str())
        .expect("checked target field must be a string")
        .to_owned();
    target[field] = serde_json::Value::String(format!("x{value}"));
}
