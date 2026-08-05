//! Strict, SDK-independent extraction of canonical GraphQL introspection responses.
//!
//! These types mirror the canonical engine query instead of reusing generated Rust bindings.
//! Schema drift therefore remains observable even when assessed bindings would normalize it.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

use crate::diagnostic::{
    ContractDiagnostic, DiagnosticCode, DiagnosticCollector, DiagnosticSet, Validation,
};
use crate::inventory::{encode_identity_segment, semantic_fingerprint};
use crate::model::{
    AuthorityId, SourceItem, SourceItemId, SourceItemInventory, SourceItemKind, SourceItemState,
    SourceLocator,
};

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Exact top-level response returned by the canonical introspection query.
pub struct IntrospectionResponse {
    #[serde(rename = "__schemaVersion")]
    pub schema_version: String,
    #[serde(rename = "__schema")]
    pub schema: IntrospectionSchema,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
/// Roots, named types, and directive definitions in one introspection graph.
pub struct IntrospectionSchema {
    pub query_type: RootType,
    pub mutation_type: Option<RootType>,
    pub subscription_type: Option<RootType>,
    pub types: Vec<IntrospectionType>,
    pub directives: Vec<DirectiveDefinition>,
}

#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Named schema root reference.
pub struct RootType {
    pub name: String,
}

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
/// Closed set of GraphQL introspection type kinds.
pub enum TypeKind {
    #[serde(rename = "SCALAR")]
    Scalar,
    #[serde(rename = "OBJECT")]
    Object,
    #[serde(rename = "INTERFACE")]
    Interface,
    #[serde(rename = "UNION")]
    Union,
    #[serde(rename = "ENUM")]
    Enum,
    #[serde(rename = "INPUT_OBJECT")]
    InputObject,
    #[serde(rename = "LIST")]
    List,
    #[serde(rename = "NON_NULL")]
    NonNull,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
/// One named type and every property requested by the canonical query.
pub struct IntrospectionType {
    pub kind: TypeKind,
    pub name: String,
    pub description: Option<String>,
    pub fields: Option<Vec<Field>>,
    pub input_fields: Option<Vec<InputValue>>,
    pub interfaces: Option<Vec<TypeRef>>,
    pub enum_values: Option<Vec<EnumValue>>,
    pub possible_types: Option<Vec<TypeRef>>,
    pub directives: Vec<AppliedDirective>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
/// Public object or interface field.
pub struct Field {
    pub name: String,
    pub description: Option<String>,
    pub args: Vec<InputValue>,
    #[serde(rename = "type")]
    pub type_ref: TypeRef,
    pub is_deprecated: bool,
    pub deprecation_reason: Option<String>,
    pub directives: Vec<AppliedDirective>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
/// Field argument, directive argument, or input-object field.
pub struct InputValue {
    pub name: String,
    pub description: Option<String>,
    #[serde(rename = "type")]
    pub type_ref: TypeRef,
    pub default_value: Option<String>,
    pub is_deprecated: bool,
    pub deprecation_reason: Option<String>,
    pub directives: Vec<AppliedDirective>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
/// One public enum member.
pub struct EnumValue {
    pub name: String,
    pub description: Option<String>,
    pub is_deprecated: bool,
    pub deprecation_reason: Option<String>,
    pub directives: Vec<AppliedDirective>,
}

#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
/// Complete recursive list/nullability or named GraphQL type reference.
pub struct TypeRef {
    pub kind: TypeKind,
    pub name: Option<String>,
    pub of_type: Option<Box<TypeRef>>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Public directive definition.
pub struct DirectiveDefinition {
    pub name: String,
    pub description: Option<String>,
    pub locations: Vec<String>,
    pub args: Vec<InputValue>,
}

#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Directive applied to a schema element.
pub struct AppliedDirective {
    pub name: String,
    pub args: Vec<AppliedDirectiveArgument>,
}

#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Evaluated argument on an applied directive.
pub struct AppliedDirectiveArgument {
    pub name: String,
    pub value: Option<String>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Reviewed names omitted from the public schema inventory.
pub struct SchemaExtractionPolicy {
    /// Exact named types excluded as meta-types or reviewed non-public elements.
    pub excluded_type_names: BTreeSet<String>,
}

/// Decodes strict introspection JSON, rejecting unknown object fields and enum variants.
pub fn decode_introspection(bytes: &[u8]) -> Result<IntrospectionResponse, DiagnosticSet> {
    serde_json::from_slice(bytes).map_err(|error| {
        one_diagnostic(
            DiagnosticCode::CapabilitySignatureInvalid,
            "engine-schema",
            format!("canonical introspection response is invalid: {error}"),
        )
    })
}

/// Extracts every public atomic schema item after validating the relationship graph.
pub fn extract_schema(
    authority_id: &AuthorityId,
    expected_schema_version: &str,
    response: &IntrospectionResponse,
    policy: &SchemaExtractionPolicy,
) -> Validation<SourceItemInventory> {
    let mut diagnostics = DiagnosticCollector::default();
    if response.schema_version != expected_schema_version {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::SchemaVersionMismatch,
            authority_id.to_string(),
            None,
            "introspection schema version differs from the immutable target",
        ));
    }

    let mut types = BTreeMap::<&str, &IntrospectionType>::new();
    for schema_type in &response.schema.types {
        if schema_type.name.is_empty()
            || matches!(schema_type.kind, TypeKind::List | TypeKind::NonNull)
        {
            diagnostics.push(signature_error(
                &schema_type.name,
                "top-level schema types must be named non-wrapper types",
            ));
            continue;
        }
        if types.insert(&schema_type.name, schema_type).is_some() {
            diagnostics.push(signature_error(
                &schema_type.name,
                "named schema type is duplicated",
            ));
        }
    }
    for excluded in &policy.excluded_type_names {
        if !types.contains_key(excluded.as_str()) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityExclusionInvalid,
                excluded,
                None,
                "reviewed schema exclusion is stale",
            ));
        }
    }
    for name in types.keys().filter(|name| name.starts_with("__")) {
        if !policy.excluded_type_names.contains(*name) {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::AuthorityExclusionInvalid,
                *name,
                None,
                "GraphQL meta-type requires an exact reviewed exclusion",
            ));
        }
    }

    validate_roots(&response.schema, &types, policy, &mut diagnostics);
    validate_types(
        &types,
        policy,
        &response.schema.directives,
        &mut diagnostics,
    );

    let mut items = BTreeMap::new();
    emit_root(
        authority_id,
        "query",
        &response.schema.query_type,
        &mut items,
        &mut diagnostics,
    );
    for (kind, root) in [
        ("mutation", response.schema.mutation_type.as_ref()),
        ("subscription", response.schema.subscription_type.as_ref()),
    ] {
        if let Some(root) = root {
            emit_root(authority_id, kind, root, &mut items, &mut diagnostics);
        }
    }
    for schema_type in types
        .values()
        .filter(|item| !policy.excluded_type_names.contains(&item.name))
    {
        emit_type(authority_id, schema_type, &mut items, &mut diagnostics);
    }
    for directive in &response.schema.directives {
        emit_directive(authority_id, directive, &mut items, &mut diagnostics);
    }
    diagnostics.finish(SourceItemInventory { items })
}

fn validate_roots(
    schema: &IntrospectionSchema,
    types: &BTreeMap<&str, &IntrospectionType>,
    policy: &SchemaExtractionPolicy,
    diagnostics: &mut DiagnosticCollector,
) {
    for (kind, root) in [
        ("query", Some(&schema.query_type)),
        ("mutation", schema.mutation_type.as_ref()),
        ("subscription", schema.subscription_type.as_ref()),
    ] {
        let Some(root) = root else { continue };
        match types.get(root.name.as_str()) {
            Some(schema_type)
                if schema_type.kind == TypeKind::Object
                    && !policy.excluded_type_names.contains(&root.name) => {}
            _ => diagnostics.push(signature_error(
                kind,
                "schema root must resolve to a public object type",
            )),
        }
    }
}

fn validate_types(
    types: &BTreeMap<&str, &IntrospectionType>,
    policy: &SchemaExtractionPolicy,
    definitions: &[DirectiveDefinition],
    diagnostics: &mut DiagnosticCollector,
) {
    let mut directive_arguments = BTreeMap::<String, BTreeSet<String>>::new();
    for directive in definitions {
        let argument_names = directive
            .args
            .iter()
            .map(|argument| argument.name.clone())
            .collect::<BTreeSet<_>>();
        if directive.name.is_empty()
            || directive_arguments
                .insert(directive.name.clone(), argument_names)
                .is_some()
        {
            diagnostics.push(signature_error(
                &directive.name,
                "directive definition must have one unique non-empty name",
            ));
        }
        if has_duplicate_names(directive.args.iter().map(|arg| arg.name.as_str())) {
            diagnostics.push(signature_error(
                &directive.name,
                "directive arguments must have unique names",
            ));
        }
        if has_duplicate_names(directive.locations.iter().map(String::as_str))
            || directive
                .locations
                .iter()
                .any(|location| !is_directive_location(location))
        {
            diagnostics.push(signature_error(
                &directive.name,
                "directive locations must be unique standard GraphQL locations",
            ));
        }
        for argument in &directive.args {
            validate_type_ref(&argument.type_ref, types, policy, diagnostics);
        }
    }

    for schema_type in types.values() {
        if policy.excluded_type_names.contains(&schema_type.name) {
            continue;
        }
        let fields = schema_type.fields.as_deref().unwrap_or_default();
        let inputs = schema_type.input_fields.as_deref().unwrap_or_default();
        let enums = schema_type.enum_values.as_deref().unwrap_or_default();
        if has_duplicate_names(fields.iter().map(|item| item.name.as_str()))
            || has_duplicate_names(inputs.iter().map(|item| item.name.as_str()))
            || has_duplicate_names(enums.iter().map(|item| item.name.as_str()))
        {
            diagnostics.push(signature_error(
                &schema_type.name,
                "schema child elements must have unique names within their kind",
            ));
        }
        if !shape_matches_kind(schema_type) {
            diagnostics.push(signature_error(
                &schema_type.name,
                "schema child collections do not match the declared type kind",
            ));
        }
        for field in fields {
            validate_type_ref(&field.type_ref, types, policy, diagnostics);
            if has_duplicate_names(field.args.iter().map(|arg| arg.name.as_str())) {
                diagnostics.push(signature_error(
                    format!("{}.{}", schema_type.name, field.name),
                    "field arguments must have unique names",
                ));
            }
            for argument in &field.args {
                validate_type_ref(&argument.type_ref, types, policy, diagnostics);
                validate_directives(&argument.directives, &directive_arguments, diagnostics);
            }
            validate_directives(&field.directives, &directive_arguments, diagnostics);
        }
        for input in inputs {
            validate_type_ref(&input.type_ref, types, policy, diagnostics);
            validate_directives(&input.directives, &directive_arguments, diagnostics);
        }
        for relationship in schema_type.interfaces.as_deref().unwrap_or_default() {
            validate_type_ref(relationship, types, policy, diagnostics);
            if relationship.kind != TypeKind::Interface {
                diagnostics.push(signature_error(
                    &schema_type.name,
                    "implemented interface relationship must name an interface",
                ));
            }
        }
        for relationship in schema_type.possible_types.as_deref().unwrap_or_default() {
            validate_type_ref(relationship, types, policy, diagnostics);
            if relationship.kind != TypeKind::Object {
                diagnostics.push(signature_error(
                    &schema_type.name,
                    "possible-type relationship must name an object",
                ));
            }
        }
        validate_directives(&schema_type.directives, &directive_arguments, diagnostics);
        for value in enums {
            validate_directives(&value.directives, &directive_arguments, diagnostics);
        }
    }
}

fn shape_matches_kind(schema_type: &IntrospectionType) -> bool {
    let fields = schema_type
        .fields
        .as_ref()
        .is_some_and(|items| !items.is_empty());
    let inputs = schema_type
        .input_fields
        .as_ref()
        .is_some_and(|items| !items.is_empty());
    let enums = schema_type
        .enum_values
        .as_ref()
        .is_some_and(|items| !items.is_empty());
    match schema_type.kind {
        TypeKind::Object => {
            !inputs
                && !enums
                && schema_type
                    .possible_types
                    .as_ref()
                    .is_none_or(Vec::is_empty)
        }
        TypeKind::Interface => !inputs && !enums,
        TypeKind::Union => {
            !fields
                && !inputs
                && !enums
                && schema_type.interfaces.as_ref().is_none_or(Vec::is_empty)
        }
        TypeKind::InputObject => {
            !fields
                && !enums
                && schema_type.interfaces.as_ref().is_none_or(Vec::is_empty)
                && schema_type
                    .possible_types
                    .as_ref()
                    .is_none_or(Vec::is_empty)
        }
        TypeKind::Enum => {
            !fields
                && !inputs
                && schema_type.interfaces.as_ref().is_none_or(Vec::is_empty)
                && schema_type
                    .possible_types
                    .as_ref()
                    .is_none_or(Vec::is_empty)
        }
        TypeKind::Scalar => {
            !fields
                && !inputs
                && !enums
                && schema_type.interfaces.as_ref().is_none_or(Vec::is_empty)
                && schema_type
                    .possible_types
                    .as_ref()
                    .is_none_or(Vec::is_empty)
        }
        TypeKind::List | TypeKind::NonNull => false,
    }
}

fn validate_type_ref(
    type_ref: &TypeRef,
    types: &BTreeMap<&str, &IntrospectionType>,
    policy: &SchemaExtractionPolicy,
    diagnostics: &mut DiagnosticCollector,
) {
    match type_ref.kind {
        TypeKind::List | TypeKind::NonNull => {
            if type_ref.name.is_some() || type_ref.of_type.is_none() {
                diagnostics.push(signature_error(
                    "type-ref",
                    "wrapper TypeRef requires ofType and no name",
                ));
            }
            if let Some(nested) = &type_ref.of_type {
                validate_type_ref(nested, types, policy, diagnostics);
            }
        }
        kind => {
            let Some(name) = type_ref.name.as_deref() else {
                diagnostics.push(signature_error("type-ref", "named TypeRef requires a name"));
                return;
            };
            if type_ref.of_type.is_some()
                || policy.excluded_type_names.contains(name)
                || types
                    .get(name)
                    .is_none_or(|schema_type| schema_type.kind != kind)
            {
                diagnostics.push(signature_error(
                    name,
                    "TypeRef has a dangling or kind-incompatible named relationship",
                ));
            }
        }
    }
}

fn validate_directives(
    directives: &[AppliedDirective],
    definitions: &BTreeMap<String, BTreeSet<String>>,
    diagnostics: &mut DiagnosticCollector,
) {
    for directive in directives {
        let Some(arguments) = definitions.get(&directive.name) else {
            diagnostics.push(signature_error(
                &directive.name,
                "applied directive has no public definition",
            ));
            continue;
        };
        if has_duplicate_names(directive.args.iter().map(|arg| arg.name.as_str())) {
            diagnostics.push(signature_error(
                &directive.name,
                "applied directive argument names must be unique",
            ));
        }
        for argument in &directive.args {
            if !arguments.contains(&argument.name) {
                diagnostics.push(signature_error(
                    &directive.name,
                    "applied directive argument has no public definition",
                ));
            }
        }
    }
}

fn emit_root(
    authority: &AuthorityId,
    kind: &str,
    root: &RootType,
    items: &mut BTreeMap<SourceItemId, SourceItem>,
    diagnostics: &mut DiagnosticCollector,
) {
    emit_item(
        authority,
        "schema-root",
        &[kind],
        format!("schema:{kind}"),
        json!({"root_kind": kind, "type": root.name}),
        false,
        items,
        diagnostics,
    );
}

fn emit_type(
    authority: &AuthorityId,
    schema_type: &IntrospectionType,
    items: &mut BTreeMap<SourceItemId, SourceItem>,
    diagnostics: &mut DiagnosticCollector,
) {
    emit_item(
        authority,
        "schema-type",
        &[type_kind_name(schema_type.kind), &schema_type.name],
        format!("schema:type:{}", schema_type.name),
        json!({
            "kind": schema_type.kind,
            "name": schema_type.name,
            "description": schema_type.description,
            "interfaces": sorted(schema_type.interfaces.as_deref().unwrap_or_default()),
            "possible_types": sorted(schema_type.possible_types.as_deref().unwrap_or_default()),
            "directives": sorted(&schema_type.directives),
        }),
        false,
        items,
        diagnostics,
    );

    let mut fields = schema_type
        .fields
        .as_deref()
        .unwrap_or_default()
        .iter()
        .collect::<Vec<_>>();
    fields.sort_by(|left, right| left.name.cmp(&right.name));
    for field in fields {
        emit_item(
            authority,
            "schema-field",
            &[&schema_type.name, &field.name],
            format!("schema:{}.{}", schema_type.name, field.name),
            json!({
                "parent": schema_type.name,
                "name": field.name,
                "description": field.description,
                "type": field.type_ref,
                "deprecated": field.is_deprecated,
                "deprecation_reason": field.deprecation_reason,
                "directives": sorted(&field.directives),
            }),
            field.is_deprecated,
            items,
            diagnostics,
        );
        let mut arguments = field.args.iter().collect::<Vec<_>>();
        arguments.sort_by(|left, right| left.name.cmp(&right.name));
        for argument in arguments {
            emit_input(
                authority,
                "schema-argument",
                &[&schema_type.name, &field.name, &argument.name],
                format!(
                    "schema:{}.{}({})",
                    schema_type.name, field.name, argument.name
                ),
                json!({"parent_type": schema_type.name, "parent_field": field.name}),
                argument,
                items,
                diagnostics,
            );
        }
    }

    let mut inputs = schema_type
        .input_fields
        .as_deref()
        .unwrap_or_default()
        .iter()
        .collect::<Vec<_>>();
    inputs.sort_by(|left, right| left.name.cmp(&right.name));
    for input in inputs {
        emit_input(
            authority,
            "schema-input-field",
            &[&schema_type.name, &input.name],
            format!("schema:{}:{}", schema_type.name, input.name),
            json!({"parent_input": schema_type.name}),
            input,
            items,
            diagnostics,
        );
    }

    let mut values = schema_type
        .enum_values
        .as_deref()
        .unwrap_or_default()
        .iter()
        .collect::<Vec<_>>();
    values.sort_by(|left, right| left.name.cmp(&right.name));
    for value in values {
        emit_item(
            authority,
            "schema-enum-value",
            &[&schema_type.name, &value.name],
            format!("schema:{}:{}", schema_type.name, value.name),
            json!({
                "parent_enum": schema_type.name,
                "name": value.name,
                "description": value.description,
                "deprecated": value.is_deprecated,
                "deprecation_reason": value.deprecation_reason,
                "directives": sorted(&value.directives),
            }),
            value.is_deprecated,
            items,
            diagnostics,
        );
    }
}

#[allow(clippy::too_many_arguments)]
fn emit_input(
    authority: &AuthorityId,
    kind: &str,
    coordinate: &[&str],
    locator: String,
    parent: Value,
    input: &InputValue,
    items: &mut BTreeMap<SourceItemId, SourceItem>,
    diagnostics: &mut DiagnosticCollector,
) {
    emit_item(
        authority,
        kind,
        coordinate,
        locator,
        json!({
            "parent": parent,
            "name": input.name,
            "description": input.description,
            "type": input.type_ref,
            "default": input.default_value,
            "deprecated": input.is_deprecated,
            "deprecation_reason": input.deprecation_reason,
            "directives": sorted(&input.directives),
        }),
        input.is_deprecated,
        items,
        diagnostics,
    );
}

fn emit_directive(
    authority: &AuthorityId,
    directive: &DirectiveDefinition,
    items: &mut BTreeMap<SourceItemId, SourceItem>,
    diagnostics: &mut DiagnosticCollector,
) {
    let mut locations = directive.locations.clone();
    locations.sort();
    locations.dedup();
    emit_item(
        authority,
        "schema-directive",
        &[&directive.name],
        format!("schema:directive:@{}", directive.name),
        json!({
            "name": directive.name,
            "description": directive.description,
            "locations": locations,
        }),
        false,
        items,
        diagnostics,
    );
    let mut arguments = directive.args.iter().collect::<Vec<_>>();
    arguments.sort_by(|left, right| left.name.cmp(&right.name));
    for argument in arguments {
        emit_input(
            authority,
            "schema-directive-argument",
            &[&directive.name, &argument.name],
            format!("schema:directive:@{}({})", directive.name, argument.name),
            json!({"parent_directive": directive.name}),
            argument,
            items,
            diagnostics,
        );
    }
}

#[allow(clippy::too_many_arguments)]
fn emit_item(
    authority: &AuthorityId,
    kind: &str,
    coordinate: &[&str],
    locator: String,
    signature: Value,
    deprecated: bool,
    items: &mut BTreeMap<SourceItemId, SourceItem>,
    diagnostics: &mut DiagnosticCollector,
) {
    let id = format!(
        "source/{authority}/{kind}/{}",
        coordinate
            .iter()
            .map(|segment| encode_identity_segment(segment))
            .collect::<Vec<_>>()
            .join("/")
    );
    let (Ok(source_item_id), Ok(item_kind), Ok(locator)) = (
        SourceItemId::new(id),
        SourceItemKind::new(kind),
        SourceLocator::new(locator),
    ) else {
        diagnostics.push(signature_error(
            "schema-source-item",
            "schema item identity, kind, or locator is invalid",
        ));
        return;
    };
    let fingerprint = match semantic_fingerprint(&signature) {
        Ok(fingerprint) => fingerprint,
        Err(errors) => {
            diagnostics.extend(errors.into_inner());
            return;
        }
    };
    let item = SourceItem {
        source_item_id: source_item_id.clone(),
        authority_id: authority.clone(),
        item_kind,
        locator,
        semantic_signature: signature,
        fingerprint,
        state: if deprecated {
            SourceItemState::Deprecated
        } else {
            SourceItemState::Active
        },
    };
    if items.insert(source_item_id.clone(), item).is_some() {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::CapabilityDuplicate,
            source_item_id.to_string(),
            None,
            "schema extraction produced a duplicate source identity",
        ));
    }
}

fn sorted<T: Clone + Ord>(values: &[T]) -> Vec<T> {
    let mut values = values.to_vec();
    values.sort();
    values
}

fn has_duplicate_names<'a>(names: impl IntoIterator<Item = &'a str>) -> bool {
    let mut seen = BTreeSet::new();
    names
        .into_iter()
        .any(|name| name.is_empty() || !seen.insert(name))
}

fn is_directive_location(location: &str) -> bool {
    matches!(
        location,
        "QUERY"
            | "MUTATION"
            | "SUBSCRIPTION"
            | "FIELD"
            | "FRAGMENT_DEFINITION"
            | "FRAGMENT_SPREAD"
            | "INLINE_FRAGMENT"
            | "VARIABLE_DEFINITION"
            | "SCHEMA"
            | "SCALAR"
            | "OBJECT"
            | "FIELD_DEFINITION"
            | "ARGUMENT_DEFINITION"
            | "INTERFACE"
            | "UNION"
            | "ENUM"
            | "ENUM_VALUE"
            | "INPUT_OBJECT"
            | "INPUT_FIELD_DEFINITION"
    )
}

fn type_kind_name(kind: TypeKind) -> &'static str {
    match kind {
        TypeKind::Scalar => "scalar",
        TypeKind::Object => "object",
        TypeKind::Interface => "interface",
        TypeKind::Union => "union",
        TypeKind::Enum => "enum",
        TypeKind::InputObject => "input-object",
        TypeKind::List => "list",
        TypeKind::NonNull => "non-null",
    }
}

fn signature_error(subject: impl Into<String>, detail: impl Into<String>) -> ContractDiagnostic {
    ContractDiagnostic::new(
        DiagnosticCode::CapabilitySignatureInvalid,
        subject,
        None,
        detail,
    )
}

fn one_diagnostic(
    code: DiagnosticCode,
    subject: impl Into<String>,
    detail: impl Into<String>,
) -> DiagnosticSet {
    DiagnosticSet::new([ContractDiagnostic::new(code, subject, None, detail)])
        .expect("one diagnostic always forms a non-empty set")
}
