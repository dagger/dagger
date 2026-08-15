//! Shared valid-first generators and deterministic recording doubles for codegen tests.

#![allow(dead_code)]

use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};

use dagger_codegen::engine::ModuleAuthoringInput;
use dagger_codegen::module::{
    CfgEnvironment, FormatVersion as ModuleFormatVersion, ModulePackage, ModuleSourcePath,
    ModuleSourceSnapshot, PackageName, Sha256Digest as ModuleDigest, SourceDocument, TargetValue,
    source_snapshot_digest,
};
use dagger_codegen::projection::catalog::{CatalogDisposition, CatalogEntry};
use dagger_codegen::schema::raw::{
    DirectiveApplication, DirectiveApplicationArgument, FullType, TypeKind, TypeRef,
};
use dagger_codegen::target::CodegenTarget;
use proptest::prelude::*;
use proptest::test_runner::{Config, FileFailurePersistence};
use serde_json::{Value, json};

pub(crate) const PURE_CASES: u32 = 256;
pub(crate) const FILESYSTEM_CASES: u32 = 128;
pub(crate) const TARGET_BYTES: &[u8] = include_bytes!("../../../../codegen/target.json");
pub(crate) const CORE_SCHEMA_BYTES: &[u8] = include_bytes!("../../../../codegen/schema.json");

pub(crate) fn module_authoring_input() -> ModuleAuthoringInput {
    let path = ModuleSourcePath::new("src/lib.rs").expect("fixture source path must validate");
    let source = r#"
#[dagger_sdk::object(root)]
pub struct FixtureRoot {
    #[dagger(field)]
    value: String,
}

#[dagger_sdk::methods]
impl FixtureRoot {
    #[dagger(constructor)]
    pub fn new() -> FixtureRoot { FixtureRoot { value: "fixture".to_owned() } }

    #[dagger(function, cache = "never", role = "check")]
    pub fn value(&self) -> String { self.value.clone() }

    #[dagger(function)]
    pub fn directory(
        &self,
        #[dagger(default_path = ".", ignore = ["target"])] source: dagger_sdk::Directory,
    ) -> dagger_sdk::Directory { source }
}
"#;
    let mut snapshot = ModuleSourceSnapshot {
        format_version: ModuleFormatVersion::current(),
        package: ModulePackage {
            name: PackageName::new("fixture").expect("fixture package must validate"),
            crate_root: path.clone(),
            edition: TargetValue::new("2024").expect("fixture edition must validate"),
        },
        cfg: CfgEnvironment {
            values: BTreeMap::new(),
            features: BTreeSet::new(),
        },
        documents: BTreeMap::from([(path.clone(), SourceDocument::new(path, source))]),
        digest: ModuleDigest::hash_bytes(b"pending fixture snapshot"),
    };
    snapshot.digest = source_snapshot_digest(&snapshot).expect("fixture snapshot must hash");
    ModuleAuthoringInput {
        source: snapshot,
        generator_digest: ModuleDigest::hash_bytes(b"fixture module generator"),
        sdk_dependency_alias: "dagger_sdk".to_owned(),
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum VisibleSchemaCase {
    ExactCore,
    CompatibleExtension,
    EngineModuleExtension,
    CoreMutation,
    CoreOmission,
    UnresolvedReference,
    RustNameCollision,
}

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub(crate) enum ClientSchemaCase {
    CoreOnly,
    Valid,
    CoreMutation,
    CoreOmission,
    WrongRootName,
    PromotedFunction,
    MultipleRoots,
    NullableRoot,
    DependencyLeakage,
    UnreachableExtension,
}

pub(crate) fn client_visible_schema(case: ClientSchemaCase, permutation: u16) -> Vec<u8> {
    let mut document: Value =
        serde_json::from_slice(CORE_SCHEMA_BYTES).expect("checked schema fixture must decode");
    if case == ClientSchemaCase::CoreOnly {
        permute_schema(&mut document, permutation);
        return serde_json::to_vec(&document).expect("schema fixture must encode");
    }

    add_client_module_types(&mut document);
    match case {
        ClientSchemaCase::CoreOnly | ClientSchemaCase::Valid => {}
        ClientSchemaCase::CoreMutation => {
            let container = schema_array_mut(&mut document, "types")
                .iter_mut()
                .find(|definition| definition["name"] == "Container")
                .expect("checked schema must contain Container");
            container["description"] = json!("incompatible checked-Core mutation");
        }
        ClientSchemaCase::CoreOmission => {
            let query = schema_array_mut(&mut document, "types")
                .iter_mut()
                .find(|definition| definition["name"] == "Query")
                .expect("checked schema must contain Query");
            query["fields"]
                .as_array_mut()
                .expect("Query fields must be an array")
                .retain(|field| field["name"] != "address");
        }
        ClientSchemaCase::WrongRootName => {
            rename_query_field(&mut document, "minimal", "different")
        }
        ClientSchemaCase::PromotedFunction => {
            let query = schema_array_mut(&mut document, "types")
                .iter_mut()
                .find(|definition| definition["name"] == "Query")
                .expect("checked schema must contain Query");
            let field = query["fields"]
                .as_array_mut()
                .and_then(|fields| fields.iter_mut().find(|field| field["name"] == "minimal"))
                .expect("client fixture must contain module root");
            field["type"] = scalar_type("String", false);
        }
        ClientSchemaCase::MultipleRoots => {
            add_query_object_field(&mut document, "second", "Minimal")
        }
        ClientSchemaCase::NullableRoot => {
            let query = schema_array_mut(&mut document, "types")
                .iter_mut()
                .find(|definition| definition["name"] == "Query")
                .expect("checked schema must contain Query");
            let field = query["fields"]
                .as_array_mut()
                .and_then(|fields| fields.iter_mut().find(|field| field["name"] == "minimal"))
                .expect("client fixture must contain module root");
            field["type"] = json!({"kind": "OBJECT", "name": "Minimal"});
        }
        ClientSchemaCase::DependencyLeakage => {
            add_object_type(
                &mut document,
                "DependencyThing",
                vec![object_field("id", scalar_type("ID", false), vec![])],
            );
            add_object_field_to_type(
                &mut document,
                "Minimal",
                object_field("dependency", object_type("DependencyThing", false), vec![]),
            );
        }
        ClientSchemaCase::UnreachableExtension => {
            add_object_type(
                &mut document,
                "MinimalUnused",
                vec![object_field("id", scalar_type("ID", false), vec![])],
            );
        }
    }
    permute_schema(&mut document, permutation);
    serde_json::to_vec(&document).expect("schema fixture must encode")
}

pub(crate) fn named_client_visible_schema(
    module_name: &str,
    root_name: &str,
    permutation: u16,
) -> Vec<u8> {
    let mut document: Value =
        serde_json::from_slice(&client_visible_schema(ClientSchemaCase::Valid, 0))
            .expect("client schema fixture must decode");
    rewrite_client_fixture_names(&mut document, module_name, root_name);
    permute_schema(&mut document, permutation);
    serde_json::to_vec(&document).expect("named client schema fixture must encode")
}

fn rewrite_client_fixture_names(value: &mut Value, module_name: &str, root_name: &str) {
    match value {
        Value::Array(values) => {
            for value in values {
                rewrite_client_fixture_names(value, module_name, root_name);
            }
        }
        Value::Object(values) => {
            for value in values.values_mut() {
                rewrite_client_fixture_names(value, module_name, root_name);
            }
        }
        Value::String(value) if value == "minimal" => *value = module_name.to_owned(),
        Value::String(value) if value.starts_with("\"Minimal") && value.ends_with('"') => {
            let suffix = value
                .strip_prefix("\"Minimal")
                .and_then(|value| value.strip_suffix('"'))
                .expect("matched directive value retains its quotes");
            *value = format!("\"{root_name}{suffix}\"");
        }
        Value::String(value) if value.starts_with("Minimal") => {
            *value = format!("{root_name}{}", &value["Minimal".len()..]);
        }
        _ => {}
    }
}

fn add_client_module_types(document: &mut Value) {
    add_scalar_type(document, "MinimalToken");
    add_enum_type(document, "MinimalState", &["READY", "BUSY"]);
    add_input_type(
        document,
        "MinimalConfig",
        vec![input_field(
            "enabled",
            scalar_type("Boolean", true),
            Some("true"),
        )],
    );
    add_object_type(
        document,
        "MinimalItem",
        vec![
            object_field("id", scalar_type("ID", false), vec![]),
            object_field("state", enum_type("MinimalState", false), vec![]),
        ],
    );
    add_object_type(
        document,
        "MinimalClient",
        vec![object_field("id", scalar_type("ID", false), vec![])],
    );
    add_interface_to_object(document, "MinimalItem", "MinimalNode");
    add_interface_type(document, "MinimalNode", "MinimalItem");
    add_object_type(
        document,
        "Minimal",
        vec![
            object_field("id", scalar_type("ID", false), vec![]),
            expected_type_field("sync", "Minimal"),
            object_field("message", scalar_type("String", false), vec![]),
            object_field("type", scalar_type("String", false), vec![]),
            object_field("token", scalar_type("MinimalToken", false), vec![]),
            object_field("helper", object_type("MinimalClient", false), vec![]),
            object_field("container", object_type("Container", false), vec![]),
            object_field("maybeContainer", object_type("Container", true), vec![]),
            object_field("node", interface_type("MinimalNode", false), vec![]),
            object_field(
                "item",
                object_type("MinimalItem", true),
                vec![argument(
                    "config",
                    input_object_type("MinimalConfig", true),
                    None,
                )],
            ),
            object_field(
                "items",
                list_type(object_type("MinimalItem", true), false),
                vec![],
            ),
            object_field(
                "useItem",
                scalar_type("String", false),
                vec![typed_id_argument("item", "MinimalItem", false)],
            ),
            object_field(
                "useItems",
                scalar_type("String", false),
                vec![typed_id_list_argument("items", "MinimalItem")],
            ),
            object_field(
                "search",
                scalar_type("String", false),
                vec![
                    argument("enabled", scalar_type("Boolean", true), Some("true")),
                    argument("count", scalar_type("Int", true), None),
                    argument("label", scalar_type("String", true), None),
                    typed_id_argument("item", "MinimalItem", true),
                ],
            ),
        ],
    );
    add_query_object_field(document, "minimal", "Minimal");
}

fn add_interface_to_object(document: &mut Value, object: &str, interface: &str) {
    let object = schema_array_mut(document, "types")
        .iter_mut()
        .find(|definition| definition["name"] == object)
        .expect("client fixture object must exist");
    object["interfaces"]
        .as_array_mut()
        .expect("object interfaces must be an array")
        .push(json!({"kind": "INTERFACE", "name": interface}));
}

fn add_interface_type(document: &mut Value, name: &str, possible_type: &str) {
    schema_array_mut(document, "types").push(json!({
        "kind": "INTERFACE",
        "name": name,
        "description": format!("Client fixture interface {name}."),
        "fields": [
            object_field("id", scalar_type("ID", false), vec![]),
            object_field("message", scalar_type("String", false), vec![])
        ],
        "inputFields": [],
        "interfaces": [],
        "enumValues": [],
        "possibleTypes": [{"kind": "OBJECT", "name": possible_type}],
        "directives": []
    }));
}

fn add_scalar_type(document: &mut Value, name: &str) {
    schema_array_mut(document, "types").push(json!({
        "kind": "SCALAR",
        "name": name,
        "description": format!("Client fixture scalar {name}."),
        "fields": [],
        "inputFields": [],
        "interfaces": [],
        "enumValues": [],
        "possibleTypes": [],
        "directives": []
    }));
}

fn add_query_object_field(document: &mut Value, field: &str, object: &str) {
    let query = schema_array_mut(document, "types")
        .iter_mut()
        .find(|definition| definition["name"] == "Query")
        .expect("checked schema must contain Query");
    query["fields"]
        .as_array_mut()
        .expect("Query fields must be an array")
        .push(object_field(field, object_type(object, false), vec![]));
}

fn rename_query_field(document: &mut Value, from: &str, to: &str) {
    let query = schema_array_mut(document, "types")
        .iter_mut()
        .find(|definition| definition["name"] == "Query")
        .expect("checked schema must contain Query");
    let field = query["fields"]
        .as_array_mut()
        .and_then(|fields| fields.iter_mut().find(|field| field["name"] == from))
        .expect("client fixture must contain module root");
    field["name"] = json!(to);
}

fn add_object_field_to_type(document: &mut Value, owner: &str, field: Value) {
    let object = schema_array_mut(document, "types")
        .iter_mut()
        .find(|definition| definition["name"] == owner)
        .expect("client fixture object must exist");
    object["fields"]
        .as_array_mut()
        .expect("object fields must be an array")
        .push(field);
}

fn add_object_type(document: &mut Value, name: &str, fields: Vec<Value>) {
    schema_array_mut(document, "types").push(json!({
        "kind": "OBJECT",
        "name": name,
        "description": format!("Client fixture object {name}."),
        "fields": fields,
        "inputFields": [],
        "interfaces": [],
        "enumValues": [],
        "possibleTypes": [],
        "directives": []
    }));
}

fn add_enum_type(document: &mut Value, name: &str, values: &[&str]) {
    schema_array_mut(document, "types").push(json!({
        "kind": "ENUM",
        "name": name,
        "description": format!("Client fixture enum {name}."),
        "fields": [],
        "inputFields": [],
        "interfaces": [],
        "enumValues": values.iter().map(|value| json!({
            "name": value,
            "description": format!("Client fixture value {value}."),
            "isDeprecated": false,
            "deprecationReason": null,
            "directives": []
        })).collect::<Vec<_>>(),
        "possibleTypes": [],
        "directives": []
    }));
}

fn add_input_type(document: &mut Value, name: &str, fields: Vec<Value>) {
    schema_array_mut(document, "types").push(json!({
        "kind": "INPUT_OBJECT",
        "name": name,
        "description": format!("Client fixture input {name}."),
        "fields": [],
        "inputFields": fields,
        "interfaces": [],
        "enumValues": [],
        "possibleTypes": [],
        "directives": []
    }));
}

fn object_field(name: &str, type_ref: Value, args: Vec<Value>) -> Value {
    json!({
        "name": name,
        "description": format!("Client fixture field {name}."),
        "type": type_ref,
        "args": args,
        "isDeprecated": false,
        "deprecationReason": null,
        "directives": []
    })
}

fn expected_type_field(name: &str, target: &str) -> Value {
    let mut field = object_field(name, scalar_type("ID", false), vec![]);
    field["directives"] = json!([{
        "name": "expectedType",
        "args": [{"name": "name", "value": format!("\"{target}\"")}]
    }]);
    field
}

fn argument(name: &str, type_ref: Value, default: Option<&str>) -> Value {
    json!({
        "name": name,
        "description": format!("Client fixture argument {name}."),
        "type": type_ref,
        "defaultValue": default,
        "isDeprecated": false,
        "deprecationReason": null,
        "directives": []
    })
}

fn typed_id_argument(name: &str, target: &str, nullable: bool) -> Value {
    let mut argument = argument(name, scalar_type("ID", nullable), None);
    argument["directives"] = json!([{
        "name": "expectedType",
        "args": [{"name": "name", "value": format!("\"{target}\"")}]
    }]);
    argument
}

fn typed_id_list_argument(name: &str, target: &str) -> Value {
    let mut argument = argument(name, list_type(scalar_type("ID", true), false), None);
    argument["directives"] = json!([{
        "name": "expectedType",
        "args": [{"name": "name", "value": format!("\"{target}\"")}]
    }]);
    argument
}

fn input_field(name: &str, type_ref: Value, default: Option<&str>) -> Value {
    argument(name, type_ref, default)
}

fn scalar_type(name: &str, nullable: bool) -> Value {
    named_type("SCALAR", name, nullable)
}

fn enum_type(name: &str, nullable: bool) -> Value {
    named_type("ENUM", name, nullable)
}

fn object_type(name: &str, nullable: bool) -> Value {
    named_type("OBJECT", name, nullable)
}

fn interface_type(name: &str, nullable: bool) -> Value {
    named_type("INTERFACE", name, nullable)
}

fn input_object_type(name: &str, nullable: bool) -> Value {
    named_type("INPUT_OBJECT", name, nullable)
}

fn named_type(kind: &str, name: &str, nullable: bool) -> Value {
    let leaf = json!({"kind": kind, "name": name});
    if nullable {
        leaf
    } else {
        json!({"kind": "NON_NULL", "ofType": leaf})
    }
}

fn list_type(element: Value, nullable: bool) -> Value {
    let list = json!({"kind": "LIST", "ofType": element});
    if nullable {
        list
    } else {
        json!({"kind": "NON_NULL", "ofType": list})
    }
}

pub(crate) fn visible_schema(case: VisibleSchemaCase, permutation: u16) -> Vec<u8> {
    let mut document: Value =
        serde_json::from_slice(CORE_SCHEMA_BYTES).expect("checked schema fixture must decode");
    match case {
        VisibleSchemaCase::ExactCore => {}
        VisibleSchemaCase::CompatibleExtension => {
            add_extension(&mut document, "RustMode", true, None)
        }
        VisibleSchemaCase::EngineModuleExtension => {
            add_extension(&mut document, "RustMode", true, Some("rust-probe"))
        }
        VisibleSchemaCase::CoreMutation => {
            let types = schema_array_mut(&mut document, "types");
            let container = types
                .iter_mut()
                .find(|definition| definition["name"] == "Container")
                .expect("checked schema must contain Container");
            container["description"] = json!("incompatible reviewed core mutation");
        }
        VisibleSchemaCase::CoreOmission => {
            let query = schema_array_mut(&mut document, "types")
                .iter_mut()
                .find(|definition| definition["name"] == "Query")
                .expect("checked schema must contain Query");
            query["fields"]
                .as_array_mut()
                .expect("Query fields must be an array")
                .retain(|field| field["name"] != "address");
        }
        VisibleSchemaCase::UnresolvedReference => {
            add_query_field(&mut document, "rustMode", "MissingRustMode", None)
        }
        VisibleSchemaCase::RustNameCollision => {
            add_extension(&mut document, "RustMode", true, None);
            add_extension(&mut document, "Rust_Mode", false, None);
        }
    }
    permute_schema(&mut document, permutation);
    serde_json::to_vec(&document).expect("schema fixture must encode")
}

pub(crate) fn module_visible_schema(permutation: u16) -> Vec<u8> {
    let mut document: Value =
        serde_json::from_slice(CORE_SCHEMA_BYTES).expect("checked schema fixture must decode");
    for hidden in [
        "Host",
        "HostID",
        "Engine",
        "EngineID",
        "EngineCache",
        "EngineCacheID",
        "EngineCacheEntry",
        "EngineCacheEntryID",
        "EngineCacheEntrySet",
        "EngineCacheEntrySetID",
    ] {
        scrub_type(&mut document, hidden);
    }
    for hidden in [
        "Query.currentWorkspace",
        "Query.engineVolume",
        "Query.sshfsVolume",
        "Address.volume",
    ] {
        scrub_field(&mut document, hidden);
    }
    permute_schema(&mut document, permutation);
    serde_json::to_vec(&document).expect("module-visible schema fixture must encode")
}

fn scrub_type(document: &mut Value, hidden: &str) {
    let definitions = std::mem::take(schema_array_mut(document, "types"));
    *schema_array_mut(document, "types") = definitions
        .into_iter()
        .filter_map(|mut definition| {
            let kind = definition["kind"].as_str().unwrap_or_default();
            let name = definition["name"].as_str().unwrap_or_default().to_owned();
            if kind == "SCALAR" {
                return (name != hidden).then_some(definition);
            }
            retain_without_type(&mut definition, "fields", hidden, field_references_type);
            retain_without_type(
                &mut definition,
                "inputFields",
                hidden,
                input_value_references_type,
            );
            if let Some(values) = definition
                .get_mut("enumValues")
                .and_then(Value::as_array_mut)
            {
                values.retain(|value| value["name"].as_str() != Some(hidden));
            }
            let empty = ["fields", "inputFields", "enumValues"].iter().all(|key| {
                definition
                    .get(*key)
                    .and_then(Value::as_array)
                    .is_none_or(Vec::is_empty)
            });
            (name != hidden && !empty).then_some(definition)
        })
        .collect();
}

fn retain_without_type(
    definition: &mut Value,
    collection: &str,
    hidden: &str,
    references: fn(&Value, &str) -> bool,
) {
    if let Some(values) = definition.get_mut(collection).and_then(Value::as_array_mut) {
        values.retain(|value| !references(value, hidden));
    }
}

fn field_references_type(field: &Value, hidden: &str) -> bool {
    type_ref_references(&field["type"], hidden)
        || field["args"].as_array().is_some_and(|arguments| {
            arguments
                .iter()
                .any(|arg| input_value_references_type(arg, hidden))
        })
}

fn input_value_references_type(value: &Value, hidden: &str) -> bool {
    type_ref_references(&value["type"], hidden)
}

fn type_ref_references(type_ref: &Value, hidden: &str) -> bool {
    type_ref["name"].as_str() == Some(hidden)
        || type_ref
            .get("ofType")
            .is_some_and(|nested| type_ref_references(nested, hidden))
}

fn scrub_field(document: &mut Value, hidden: &str) {
    let Some((type_name, field_name)) = hidden.split_once('.') else {
        return;
    };
    let Some(definition) = schema_array_mut(document, "types")
        .iter_mut()
        .find(|definition| definition["name"].as_str() == Some(type_name))
    else {
        return;
    };
    if let Some(fields) = definition.get_mut("fields").and_then(Value::as_array_mut) {
        fields.retain(|field| field["name"].as_str() != Some(field_name));
    }
}

fn add_extension(document: &mut Value, name: &str, add_field: bool, module: Option<&str>) {
    let directives = module.map_or_else(|| json!([]), source_map_directives);
    schema_array_mut(document, "types").push(json!({
        "kind": "ENUM",
        "name": name,
        "description": "Operation-scoped Rust fixture enum.",
        "enumValues": [{
            "name": "READY",
            "description": "Ready for the operation.",
            "isDeprecated": false,
            "deprecationReason": null,
            "directives": []
        }],
        "interfaces": [],
        "possibleTypes": [],
        "directives": directives
    }));
    if add_field {
        add_query_field(document, "rustMode", name, module);
    }
}

fn add_query_field(document: &mut Value, field_name: &str, type_name: &str, module: Option<&str>) {
    let query = schema_array_mut(document, "types")
        .iter_mut()
        .find(|definition| definition["name"] == "Query")
        .expect("checked schema must contain Query");
    query["fields"]
        .as_array_mut()
        .expect("Query fields must be an array")
        .push(json!({
            "name": field_name,
            "description": "Operation-scoped Rust fixture field.",
            "type": {"kind": "NON_NULL", "ofType": {"kind": "ENUM", "name": type_name}},
            "args": [],
            "isDeprecated": false,
            "deprecationReason": null,
            "directives": module.map_or_else(|| json!([]), source_map_directives)
        }));
}

fn source_map_directives(module: &str) -> Value {
    json!([{
        "name": "sourceMap",
        "args": [{
            "name": "module",
            "value": serde_json::to_string(module).expect("fixture module must encode")
        }]
    }])
}

fn permute_schema(document: &mut Value, permutation: u16) {
    let types = schema_array_mut(document, "types");
    if !types.is_empty() {
        let offset = usize::from(permutation) % types.len();
        types.rotate_left(offset);
    }
    for definition in types {
        for collection in ["fields", "inputFields", "enumValues"] {
            let Some(values) = definition.get_mut(collection).and_then(Value::as_array_mut) else {
                continue;
            };
            if !values.is_empty() {
                let offset = usize::from(permutation.rotate_left(3)) % values.len();
                values.rotate_left(offset);
            }
        }
    }
    let directives = schema_array_mut(document, "directives");
    if !directives.is_empty() {
        let offset = usize::from(permutation.rotate_left(7)) % directives.len();
        directives.rotate_left(offset);
    }
}

fn schema_array_mut<'a>(document: &'a mut Value, name: &str) -> &'a mut Vec<Value> {
    document["__schema"][name]
        .as_array_mut()
        .expect("checked schema collection must be an array")
}

pub(crate) fn pure_config() -> Config {
    Config {
        cases: PURE_CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/foundations.txt"
        )))),
        ..Config::default()
    }
}

pub(crate) fn filesystem_config() -> Config {
    Config {
        cases: FILESYSTEM_CASES,
        failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/proptest-regressions/filesystem.txt"
        )))),
        ..Config::default()
    }
}

pub(crate) fn name_strategy() -> impl Strategy<Value = String> {
    "[A-Za-z_][A-Za-z0-9_]{0,31}"
}

pub(crate) fn default_literal_strategy() -> impl Strategy<Value = String> {
    prop_oneof![
        Just("null".to_owned()),
        any::<bool>().prop_map(|value| value.to_string()),
        any::<i32>().prop_map(|value| value.to_string()),
        "[A-Za-z0-9 _-]{0,24}".prop_map(|value| format!("\"{value}\"")),
    ]
}

pub(crate) fn target_strategy() -> impl Strategy<Value = CodegenTarget> {
    Just(
        CodegenTarget::decode_exact(include_bytes!("../../../../codegen/target.json"))
            .expect("checked target must decode"),
    )
}

pub(crate) fn wrapper_strategy() -> impl Strategy<Value = TypeRef> {
    let leaf = name_strategy().prop_map(|name| TypeRef {
        kind: Some(TypeKind::Scalar),
        name: Some(name),
        of_type: None,
        interfaces: None,
        possible_types: None,
        directives: None,
        unknown_fields: BTreeMap::new(),
    });
    leaf.prop_recursive(8, 64, 2, |inner| {
        prop_oneof![
            inner.clone().prop_map(|inner| TypeRef {
                kind: Some(TypeKind::List),
                name: None,
                of_type: Some(Box::new(inner)),
                interfaces: None,
                possible_types: None,
                directives: None,
                unknown_fields: BTreeMap::new(),
            }),
            inner.prop_map(|inner| TypeRef {
                kind: Some(TypeKind::NonNull),
                name: None,
                of_type: Some(Box::new(inner)),
                interfaces: None,
                possible_types: None,
                directives: None,
                unknown_fields: BTreeMap::new(),
            }),
        ]
    })
}

pub(crate) fn directive_strategy() -> impl Strategy<Value = DirectiveApplication> {
    (
        name_strategy(),
        prop::collection::vec((name_strategy(), default_literal_strategy()), 0..4),
    )
        .prop_map(|(name, arguments)| DirectiveApplication {
            name,
            args: arguments
                .into_iter()
                .map(|(name, value)| DirectiveApplicationArgument {
                    name,
                    value: Some(value),
                    unknown_fields: BTreeMap::new(),
                })
                .collect(),
            unknown_fields: BTreeMap::new(),
        })
}

pub(crate) fn raw_schema_fragment_strategy() -> impl Strategy<Value = FullType> {
    (
        name_strategy(),
        prop::option::of(".{0,32}"),
        prop::collection::vec(directive_strategy(), 0..3),
    )
        .prop_map(|(name, description, _directives)| FullType {
            kind: Some(TypeKind::Scalar),
            name: Some(name),
            description,
            fields: None,
            input_fields: None,
            interfaces: None,
            enum_values: None,
            possible_types: None,
            directives: None,
            unknown_fields: BTreeMap::new(),
        })
}

pub(crate) fn canonical_schema_fragment_strategy() -> impl Strategy<Value = String> {
    name_strategy().prop_filter("canonical fragments exclude introspection names", |name| {
        !name.starts_with("__")
    })
}

pub(crate) fn catalog_strategy() -> impl Strategy<Value = Vec<CatalogEntry>> {
    prop::collection::btree_map(
        name_strategy(),
        prop_oneof![
            Just(CatalogDisposition::Emitted),
            Just(CatalogDisposition::RuntimeProvided),
            Just(CatalogDisposition::PolicyRecorded),
        ],
        0..24,
    )
    .prop_map(|entries| {
        entries
            .into_iter()
            .map(|(schema_id, disposition)| CatalogEntry {
                schema_id,
                disposition,
                reason: None,
            })
            .collect()
    })
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct ArtifactSet(pub BTreeMap<PathBuf, Vec<u8>>);

pub(crate) fn artifact_set_strategy() -> impl Strategy<Value = ArtifactSet> {
    prop::collection::btree_map(
        "[a-z][a-z0-9_-]{0,12}\\.rs",
        prop::collection::vec(any::<u8>(), 0..64),
        0..8,
    )
    .prop_map(|entries| {
        ArtifactSet(
            entries
                .into_iter()
                .map(|(path, bytes)| (path.into(), bytes))
                .collect(),
        )
    })
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct CapabilityMapping {
    pub capability_id: String,
    pub schema_id: String,
}

pub(crate) fn capability_mapping_strategy() -> impl Strategy<Value = CapabilityMapping> {
    (name_strategy(), name_strategy()).prop_map(|(capability_id, schema_id)| CapabilityMapping {
        capability_id: format!("schema/engine-schema/{capability_id}"),
        schema_id,
    })
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct EvidenceRecord {
    pub subject: String,
    pub digest: [u8; 32],
}

pub(crate) fn evidence_record_strategy() -> impl Strategy<Value = EvidenceRecord> {
    (name_strategy(), any::<[u8; 32]>())
        .prop_map(|(subject, digest)| EvidenceRecord { subject, digest })
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingSelection {
    pub fields: Vec<String>,
}

impl RecordingSelection {
    pub(crate) fn select(&mut self, wire_name: impl Into<String>) {
        self.fields.push(wire_name.into());
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingSession {
    pub executions: Vec<Vec<String>>,
}

impl RecordingSession {
    pub(crate) fn execute(&mut self, selection: &RecordingSelection) {
        self.executions.push(selection.fields.clone());
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingFormatter {
    pub candidates: Vec<Vec<u8>>,
}

impl RecordingFormatter {
    pub(crate) fn format(&mut self, candidate: &[u8]) -> Vec<u8> {
        self.candidates.push(candidate.to_vec());
        candidate.to_vec()
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct RecordingFilesystem {
    pub files: BTreeMap<PathBuf, Vec<u8>>,
    pub events: Vec<String>,
}

impl RecordingFilesystem {
    pub(crate) fn write(&mut self, path: impl Into<PathBuf>, bytes: Vec<u8>) {
        let path = path.into();
        self.events.push(format!("write:{}", path.display()));
        self.files.insert(path, bytes);
    }

    pub(crate) fn read(&mut self, path: &Path) -> Option<Vec<u8>> {
        self.events.push(format!("read:{}", path.display()));
        self.files.get(path).cloned()
    }
}

#[derive(Clone, Debug, Default)]
pub(crate) struct RecordingPublicationLock {
    held: Arc<AtomicBool>,
    acquisitions: Arc<AtomicUsize>,
}

impl RecordingPublicationLock {
    pub(crate) fn try_acquire(&self) -> Option<RecordingPublicationGuard> {
        self.held
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .ok()
            .map(|_| {
                self.acquisitions.fetch_add(1, Ordering::Relaxed);
                RecordingPublicationGuard {
                    held: Arc::clone(&self.held),
                }
            })
    }

    pub(crate) fn acquisitions(&self) -> usize {
        self.acquisitions.load(Ordering::Relaxed)
    }
}

#[derive(Debug)]
pub(crate) struct RecordingPublicationGuard {
    held: Arc<AtomicBool>,
}

impl Drop for RecordingPublicationGuard {
    fn drop(&mut self) {
        self.held.store(false, Ordering::Release);
    }
}
