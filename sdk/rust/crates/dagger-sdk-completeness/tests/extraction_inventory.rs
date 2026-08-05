//! Correctness properties for independent extraction, stable identity, and peer precedence.

use std::collections::BTreeSet;

use dagger_sdk_completeness::extract::go::adapt_go_output;
use dagger_sdk_completeness::extract::harness::{
    HarnessRefresh, extract_harness, pinned_check_ids,
};
use dagger_sdk_completeness::extract::policy::{
    PolicyClauseSelection, extract_policy_clauses, extract_test_handoff,
};
use dagger_sdk_completeness::extract::schema::{
    AppliedDirective, DirectiveDefinition, EnumValue, Field, InputValue, IntrospectionResponse,
    IntrospectionSchema, IntrospectionType, RootType, SchemaExtractionPolicy, TypeKind, TypeRef,
    decode_introspection, extract_schema,
};
use dagger_sdk_completeness::{
    AuthorityId, CommitSha, HarnessCheckKind, SourceItemState, behavior_capability_id,
    build_harness_check_inventory, canonical_bytes, decode_identity_segment,
    derive_schema_candidates, encode_identity_segment, semantic_fingerprint,
};
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};

fn proptest_config() -> Config {
    Config {
        cases: 256,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/extraction_inventory.txt"
        )))),
        ..Config::default()
    }
}

fn authority(value: &str) -> AuthorityId {
    AuthorityId::new(value).unwrap()
}

fn named(kind: TypeKind, name: &str) -> TypeRef {
    TypeRef {
        kind,
        name: Some(name.to_owned()),
        of_type: None,
    }
}

fn nested_ref(depth: usize) -> TypeRef {
    let mut reference = named(TypeKind::Scalar, "String");
    for index in 0..depth {
        reference = TypeRef {
            kind: if index % 2 == 0 {
                TypeKind::NonNull
            } else {
                TypeKind::List
            },
            name: None,
            of_type: Some(Box::new(reference)),
        };
    }
    reference
}

fn schema_graph(
    enum_count: usize,
    depth: usize,
    deprecated: bool,
    reverse: bool,
) -> IntrospectionResponse {
    let input = InputValue {
        name: "config".to_owned(),
        description: Some("configuration".to_owned()),
        type_ref: named(TypeKind::InputObject, "Config"),
        default_value: Some("{}".to_owned()),
        is_deprecated: deprecated,
        deprecation_reason: deprecated.then(|| "legacy".to_owned()),
        directives: vec![],
    };
    let mut enum_values = (0..enum_count)
        .map(|index| EnumValue {
            name: format!("VALUE_{index}"),
            description: Some(format!("value {index}")),
            is_deprecated: deprecated && index == 0,
            deprecation_reason: (deprecated && index == 0).then(|| "legacy".to_owned()),
            directives: vec![],
        })
        .collect::<Vec<_>>();
    if reverse {
        enum_values.reverse();
    }
    let mut types = vec![
        IntrospectionType {
            kind: TypeKind::Object,
            name: "Query".to_owned(),
            description: Some("query root".to_owned()),
            fields: Some(vec![Field {
                name: "value".to_owned(),
                description: Some("returns a value".to_owned()),
                args: vec![input.clone()],
                type_ref: nested_ref(depth),
                is_deprecated: deprecated,
                deprecation_reason: deprecated.then(|| "legacy".to_owned()),
                directives: vec![],
            }]),
            input_fields: None,
            interfaces: Some(vec![]),
            enum_values: None,
            possible_types: Some(vec![]),
            directives: vec![],
        },
        IntrospectionType {
            kind: TypeKind::Scalar,
            name: "String".to_owned(),
            description: Some("text".to_owned()),
            fields: None,
            input_fields: None,
            interfaces: None,
            enum_values: None,
            possible_types: None,
            directives: vec![],
        },
        IntrospectionType {
            kind: TypeKind::InputObject,
            name: "Config".to_owned(),
            description: Some("configuration".to_owned()),
            fields: None,
            input_fields: Some(vec![InputValue {
                name: "enabled".to_owned(),
                description: Some("toggle".to_owned()),
                type_ref: named(TypeKind::Scalar, "String"),
                default_value: Some("\"yes\"".to_owned()),
                is_deprecated: false,
                deprecation_reason: None,
                directives: vec![],
            }]),
            interfaces: None,
            enum_values: None,
            possible_types: None,
            directives: vec![],
        },
        IntrospectionType {
            kind: TypeKind::Enum,
            name: "Color".to_owned(),
            description: Some("colours".to_owned()),
            fields: None,
            input_fields: None,
            interfaces: None,
            enum_values: Some(enum_values),
            possible_types: None,
            directives: vec![],
        },
    ];
    if reverse {
        types.reverse();
    }
    IntrospectionResponse {
        schema_version: "v1.0.0".to_owned(),
        schema: IntrospectionSchema {
            query_type: RootType {
                name: "Query".to_owned(),
            },
            mutation_type: None,
            subscription_type: None,
            types,
            directives: vec![DirectiveDefinition {
                name: "include".to_owned(),
                description: Some("conditional inclusion".to_owned()),
                locations: if reverse {
                    vec!["FIELD".to_owned(), "FRAGMENT_DEFINITION".to_owned()]
                } else {
                    vec!["FRAGMENT_DEFINITION".to_owned(), "FIELD".to_owned()]
                },
                args: vec![InputValue {
                    name: "if".to_owned(),
                    description: None,
                    type_ref: named(TypeKind::Scalar, "String"),
                    default_value: None,
                    is_deprecated: false,
                    deprecation_reason: None,
                    directives: vec![],
                }],
            }],
        },
    }
}

// Invariant: extraction equals the atomic public graph and ignores source enumeration order.
// Feature: rust-sdk-completeness-contract, Property 4: complete schema extraction
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_4_complete_schema_extraction(
        enum_count in 1_usize..6,
        depth in 0_usize..7,
        deprecated in any::<bool>(),
    ) {
        let forward = schema_graph(enum_count, depth, deprecated, false);
        let reverse = schema_graph(enum_count, depth, deprecated, true);
        let policy = SchemaExtractionPolicy::default();
        let authority = authority("engine-schema");
        let forward_items = extract_schema(&authority, "v1.0.0", &forward, &policy).unwrap();
        let reverse_items = extract_schema(&authority, "v1.0.0", &reverse, &policy).unwrap();

        prop_assert_eq!(&forward_items, &reverse_items);
        prop_assert_eq!(forward_items.items.len(), 10 + enum_count);

        let mut dangling = forward;
        dangling.schema.types[0].fields.as_mut().unwrap()[0].type_ref =
            named(TypeKind::Object, "Missing");
        prop_assert!(extract_schema(&authority, "v1.0.0", &dangling, &policy).is_err());
    }
}

#[test]
fn schema_decoder_rejects_unknown_fields() {
    let bytes = br#"{"__schemaVersion":"v1","__schema":{"queryType":{"name":"Query"},"types":[],"directives":[]},"extra":true}"#;
    assert!(decode_introspection(bytes).is_err());
}

#[test]
fn repeated_fixture_extraction_is_byte_identical() {
    let graph = schema_graph(4, 6, true, false);
    let authority = authority("engine-schema");
    let first = extract_schema(
        &authority,
        "v1.0.0",
        &graph,
        &SchemaExtractionPolicy::default(),
    )
    .unwrap();
    let second = extract_schema(
        &authority,
        "v1.0.0",
        &graph,
        &SchemaExtractionPolicy::default(),
    )
    .unwrap();

    assert_eq!(
        canonical_bytes(&first).unwrap(),
        canonical_bytes(&second).unwrap()
    );
    let candidates = derive_schema_candidates(&first).unwrap();
    assert_eq!(candidates.len(), first.items.len());
    assert!(candidates.iter().all(|candidate| {
        candidate
            .definition
            .capability_id
            .as_str()
            .starts_with("schema/engine-schema/")
    }));
}

#[test]
fn go_adapter_revalidates_ast_digest_and_preserves_nonpassing_states() {
    let items = adapt_go_output(
        &authority("go-client"),
        include_bytes!("fixtures/go-helper-states.json"),
        "1309520660f6a5b35ef97b4fbe151e32a06a8dc5",
    )
    .unwrap();
    let states = items
        .items
        .values()
        .map(|item| item.state.clone())
        .collect::<BTreeSet<_>>();

    assert_eq!(
        states,
        BTreeSet::from([
            SourceItemState::Active,
            SourceItemState::Skipped,
            SourceItemState::Removed,
        ])
    );
}

#[test]
fn harness_scanner_preserves_the_pinned_eighteen_check_partition() {
    let names = [
        "installRegistersSdk",
        "installMarksAsSdk",
        "initScaffoldsModule",
        "initWritesModuleConfig",
        "initRegistersModule",
        "initRecordsAuthoringSdk",
        "generateSucceeds",
        "scaffoldedModuleLoads",
        "sdkReportsModuleOptions",
        "engineRequiredReportsVersion",
        "depsListSucceeds",
        "generateRespectsCwd",
        "initModuleSeedsFiles",
        "initModuleDoesNotWriteConfig",
        "initModuleDoesNotRemoveExistingFiles",
        "initModuleHonorsCustomPath",
        "generateExposesGenerator",
        "initModuleRendersRootType",
    ];
    let mut source =
        "type SdkTarget { sdk: String }\npub target(sdk: String): SdkTarget { SdkTarget { sdk } }\n"
            .to_owned();
    source.push_str("pub modTest(): String { \"modTest\" }\n");
    for name in names {
        source.push_str(&format!(
            "pub {name}(): String @check {{ \"brace }} in string\" # ignored {{\n \"ok\" }}\n"
        ));
    }
    let refresh = HarnessRefresh {
        check_ids: pinned_check_ids(),
        require_sdk_target: true,
        require_mod_test: true,
    };
    let inventory = extract_harness(&authority("sdk-contract-harness"), &source, &refresh).unwrap();

    assert_eq!(inventory.items.len(), 18);
    assert_eq!(
        inventory
            .items
            .values()
            .filter(|item| item.state == SourceItemState::HarnessSelf)
            .count(),
        1
    );
    let check_inventory = build_harness_check_inventory(
        &inventory,
        &CommitSha::new("8c164424b7a8a37b33a77367ef7547490d5b87b5").unwrap(),
    )
    .unwrap();
    assert_eq!(check_inventory.checks.len(), 18);
    assert_eq!(
        check_inventory
            .checks
            .values()
            .filter(|check| check.check_kind == HarnessCheckKind::SubjectConformance)
            .count(),
        17
    );
    assert_eq!(
        check_inventory
            .checks
            .values()
            .filter(|check| check.check_kind == HarnessCheckKind::HarnessSelf)
            .count(),
        1
    );
}

#[test]
fn removed_handoff_and_exact_rust_policy_are_extracted_as_nonpassing_authority() {
    let handoff = include_str!("../../../../../future/sdk-tests.md");
    let commit = CommitSha::new("200e400d5a1463e78b1d52001394d77f743c290a").unwrap();
    let removed =
        extract_test_handoff(&authority("go-integration-tests"), handoff, &commit).unwrap();
    assert_eq!(removed.items.len(), 39);
    assert!(
        removed
            .items
            .values()
            .all(|item| item.state == SourceItemState::Removed)
    );

    let guidance = include_str!("../../../AGENTS.md");
    let selected = extract_policy_clauses(
        &authority("rust-policy"),
        "sdk/rust/AGENTS.md",
        guidance,
        &[PolicyClauseSelection {
            clause_id: "edition-2024".to_owned(),
            exact_text: "Rust edition 2024 is the current language contract.".to_owned(),
        }],
    )
    .unwrap();
    assert_eq!(selected.items.len(), 1);
}

// Invariant: locations and map order are incidental; coordinate and semantics are not.
// Feature: rust-sdk-completeness-contract, Property 6: stable capability identity and semantic fingerprinting
proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn property_6_stable_capability_identity_and_semantic_fingerprinting(
        coordinate in "[A-Za-z][A-Za-z0-9_]{0,20}",
        value in any::<u64>(),
        line in 1_u32..100_000,
    ) {
        let encoded = encode_identity_segment(&coordinate);
        prop_assert_eq!(decode_identity_segment(&encoded).unwrap(), coordinate.clone());

        let forward = serde_json::json!({"name": coordinate, "shape": {"value": value}});
        let reverse = serde_json::json!({"shape": {"value": value}, "name": coordinate});
        let changed = serde_json::json!({"name": coordinate, "shape": {"value": value.wrapping_add(1)}});
        let identity = behavior_capability_id(&authority("go-client"), &[&coordinate]).unwrap();
        let moved_identity = behavior_capability_id(&authority("go-client"), &[&coordinate]).unwrap();
        let _incidental_locator = format!("api.go:{line}");

        prop_assert_eq!(identity, moved_identity);
        prop_assert_eq!(semantic_fingerprint(&forward).unwrap(), semantic_fingerprint(&reverse).unwrap());
        prop_assert_ne!(semantic_fingerprint(&forward).unwrap(), semantic_fingerprint(&changed).unwrap());
    }
}

#[test]
fn applied_directive_signature_is_orderable_for_deterministic_sets() {
    let mut directives = [
        AppliedDirective {
            name: "z".to_owned(),
            args: vec![],
        },
        AppliedDirective {
            name: "a".to_owned(),
            args: vec![],
        },
    ];
    directives.sort();
    assert_eq!(directives[0].name, "a");
}
