//! Rejection-atomic conversion from bounded introspection input to canonical schema.

use std::collections::{BTreeMap, BTreeSet};

use crate::diagnostic::{
    Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet, RelatedCoordinate,
};
use crate::target::CodegenTarget;
use crate::target::MAX_SCHEMA_BYTES;

use super::SchemaCompatibilityMode;
use super::canonical::{
    ArgumentDefinition, CanonicalSchema, CoordinateInventory, Deprecation, DirectiveApplication,
    DirectiveDefinition, EnumDefinition, EnumValueDefinition, FieldDefinition,
    InputFieldDefinition, InputObjectDefinition, InterfaceDefinition, ObjectDefinition,
    ScalarDefinition, SchemaCoordinate, SchemaName, TypeDefinition, TypeShape, TypeUse,
};
use super::defaults::{ConstValue, parse_const};
use super::raw::{
    self, FullType, FullTypeField, InputValue, IntrospectionResponse, TypeKind, TypeRef,
    UnknownFields,
};

const MAX_WRAPPER_DEPTH: usize = 64;
const MAX_DEFAULT_DEPTH: usize = 64;

// Count closure is a final tripwire, not a substitute for coordinate validation. It
// prevents an otherwise valid omission and addition from quietly redefining the target.
const EXACT_INVENTORY: CoordinateInventory = CoordinateInventory {
    query_roots: 1,
    named_types: 111,
    scalars: 8,
    objects: 78,
    interfaces: 3,
    enums: 18,
    input_objects: 4,
    fields: 720,
    arguments: 611,
    input_fields: 14,
    enum_values: 84,
    interface_edges: 91,
    directives: 12,
    directive_arguments: 14,
};

type TypeIndex<'a> = BTreeMap<SchemaName, &'a FullType>;
type DirectiveIndex<'a> = BTreeMap<SchemaName, &'a raw::SchemaDirective>;

/// Verifies, decodes, and canonicalizes the checked schema snapshot.
pub fn decode_and_validate(
    target: &CodegenTarget,
    schema_bytes: &[u8],
) -> Result<CanonicalSchema, DiagnosticSet> {
    decode_and_validate_with_mode(target, schema_bytes, SchemaCompatibilityMode::ExactTarget)
}

/// Decodes a schema under either exact-target or manifest-backed extension policy.
pub fn decode_and_validate_with_mode(
    target: &CodegenTarget,
    schema_bytes: &[u8],
    mode: SchemaCompatibilityMode<'_>,
) -> Result<CanonicalSchema, DiagnosticSet> {
    match mode {
        SchemaCompatibilityMode::ExactTarget => target.verify_schema(schema_bytes)?,
        SchemaCompatibilityMode::ExactCoreWithExtensions(_) => {
            if schema_bytes.len() > MAX_SCHEMA_BYTES {
                return Err(DiagnosticSet::one(Diagnostic::new(
                    DiagnosticCode::SchemaRootInvalid,
                    Some(DiagnosticCoordinate::new("schema")),
                    "visible schema exceeds its 8 MiB bound",
                )));
            }
        }
    }
    let response: IntrospectionResponse = serde_json::from_slice(schema_bytes).map_err(|_| {
        DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::SchemaRootInvalid,
            Some(DiagnosticCoordinate::new("schema")),
            "schema snapshot is not a supported introspection JSON envelope",
        ))
    })?;

    let compatibility = match mode {
        SchemaCompatibilityMode::ExactTarget => None,
        SchemaCompatibilityMode::ExactCoreWithExtensions(manifest) => Some(manifest),
    };
    let schema = validate_response_mode(
        target,
        &response,
        matches!(mode, SchemaCompatibilityMode::ExactTarget),
        compatibility,
    )?;
    if let SchemaCompatibilityMode::ExactCoreWithExtensions(manifest) = mode {
        manifest.verify(&schema)?;
    }
    Ok(schema)
}

#[cfg(test)]
fn validate_response(
    target: &CodegenTarget,
    response: &IntrospectionResponse,
) -> Result<CanonicalSchema, DiagnosticSet> {
    validate_response_mode(target, response, true, None)
}

fn validate_response_mode(
    target: &CodegenTarget,
    response: &IntrospectionResponse,
    exact_inventory: bool,
    compatibility: Option<&super::CoreCoordinateManifest>,
) -> Result<CanonicalSchema, DiagnosticSet> {
    let mut diagnostics = Vec::new();
    let container = match response {
        IntrospectionResponse::FullResponse(response) => {
            report_unknown(&response.unknown_fields, "response", &mut diagnostics);
            &response.data
        }
        IntrospectionResponse::Schema(container) => container,
    };
    report_unknown(
        &container.unknown_fields,
        "schema-envelope",
        &mut diagnostics,
    );
    let expected_schema_version = format!("v{}", target.schema_version());
    if container.schema_version.as_deref() != Some(expected_schema_version.as_str()) {
        diagnostics.push(Diagnostic::new(
            DiagnosticCode::TargetIdentityInvalid,
            Some(DiagnosticCoordinate::new("schema.__schemaVersion")),
            "schema version does not match the reviewed target",
        ));
    }
    let Some(schema) = container.schema.as_ref() else {
        diagnostics.push(Diagnostic::new(
            DiagnosticCode::SchemaRootInvalid,
            Some(DiagnosticCoordinate::new("schema.__schema")),
            "introspection response does not contain __schema",
        ));
        return Err(diagnostic_set(diagnostics));
    };
    report_unknown(&schema.unknown_fields, "schema", &mut diagnostics);

    let type_index = collect_types(schema, &mut diagnostics);
    let directive_index = collect_directives(schema, &mut diagnostics);
    let query = validate_roots(schema, &type_index, &mut diagnostics);
    let directives =
        canonicalize_directives(schema, &type_index, &directive_index, &mut diagnostics);
    let types = canonicalize_types(
        &type_index,
        &directive_index,
        compatibility,
        &mut diagnostics,
    );
    validate_interface_consistency(&types, compatibility, &mut diagnostics);

    let inventory = inventory(&types, &directives, query.is_some());
    if exact_inventory {
        validate_exact_inventory(inventory, &mut diagnostics);
    }

    if !diagnostics.is_empty() {
        return Err(diagnostic_set(diagnostics));
    }
    let Some(query) = query else {
        return Err(DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::SchemaRootInvalid,
            Some(DiagnosticCoordinate::new("schema.query")),
            "query root was not validated",
        )));
    };
    Ok(CanonicalSchema::new(
        target.clone(),
        query,
        types,
        directives,
        inventory,
    ))
}

fn collect_types<'a>(schema: &'a raw::Schema, diagnostics: &mut Vec<Diagnostic>) -> TypeIndex<'a> {
    let Some(entries) = schema.types.as_ref() else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaRootInvalid,
            "schema.types",
            "schema does not contain a types collection",
        ));
        return BTreeMap::new();
    };
    let mut index = BTreeMap::new();
    for (position, entry) in entries.iter().enumerate() {
        let coordinate = format!("schema.types[{position}]");
        let Some(entry) = entry.as_ref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                &coordinate,
                "types collection contains a null entry",
            ));
            continue;
        };
        let definition = &entry.full_type;
        report_unknown(&definition.unknown_fields, &coordinate, diagnostics);
        let Some(name_source) = definition.name.as_deref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                &coordinate,
                "named definition does not contain a name",
            ));
            continue;
        };
        let Some(name) = parse_name(name_source, &coordinate, diagnostics) else {
            continue;
        };
        let Some(kind) = definition.kind.as_ref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                name.as_str(),
                "named definition does not contain a kind",
            ));
            continue;
        };
        if matches!(kind, TypeKind::List | TypeKind::NonNull) {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaWrapperInvalid,
                name.as_str(),
                "schema definitions cannot be wrapper kinds",
            ));
        }
        if !name.as_str().starts_with("__") && matches!(kind, TypeKind::Union | TypeKind::Other(_))
        {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaTypeUnsupported,
                name.as_str(),
                "public union or unknown named type is unsupported",
            ));
        }
        if let Some(previous) = index.insert(name.clone(), definition) {
            let previous_name = previous.name.as_deref().unwrap_or("schema.types");
            diagnostics.push(
                schema_error(
                    DiagnosticCode::SchemaReferenceInvalid,
                    name.as_str(),
                    "duplicate named definition",
                )
                .with_related(RelatedCoordinate {
                    coordinate: DiagnosticCoordinate::new(previous_name),
                    relationship: "first definition".to_owned(),
                }),
            );
        }
    }
    index
}

fn collect_directives<'a>(
    schema: &'a raw::Schema,
    diagnostics: &mut Vec<Diagnostic>,
) -> DirectiveIndex<'a> {
    let Some(entries) = schema.directives.as_ref() else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaDirectiveArgumentInvalid,
            "schema.directives",
            "schema does not contain directive definitions",
        ));
        return BTreeMap::new();
    };
    let mut index = BTreeMap::new();
    for (position, entry) in entries.iter().enumerate() {
        let coordinate = format!("schema.directives[{position}]");
        let Some(definition) = entry.as_ref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                &coordinate,
                "directive collection contains a null entry",
            ));
            continue;
        };
        report_unknown(&definition.unknown_fields, &coordinate, diagnostics);
        let Some(name_source) = definition.name.as_deref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                &coordinate,
                "directive definition does not contain a name",
            ));
            continue;
        };
        let Some(name) = parse_name(name_source, &coordinate, diagnostics) else {
            continue;
        };
        if index.insert(name.clone(), definition).is_some() {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                &SchemaCoordinate::directive(&name).to_string(),
                "duplicate directive definition",
            ));
        }
    }
    index
}

fn validate_roots(
    schema: &raw::Schema,
    type_index: &TypeIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<SchemaName> {
    if let Some(query) = &schema.query_type {
        report_unknown(&query.unknown_fields, "schema.query", diagnostics);
    }
    if schema.mutation_type.is_some() {
        if let Some(mutation) = &schema.mutation_type {
            report_unknown(&mutation.unknown_fields, "schema.mutation", diagnostics);
        }
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaTypeUnsupported,
            "schema.mutation",
            "the exact core target does not contain a mutation root",
        ));
    }
    if schema.subscription_type.is_some() {
        if let Some(subscription) = &schema.subscription_type {
            report_unknown(
                &subscription.unknown_fields,
                "schema.subscription",
                diagnostics,
            );
        }
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaTypeUnsupported,
            "schema.subscription",
            "the exact core target does not contain a subscription root",
        ));
    }
    let Some(name_source) = schema
        .query_type
        .as_ref()
        .and_then(|query| query.name.as_deref())
    else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaRootInvalid,
            "schema.query",
            "schema does not contain a named query root",
        ));
        return None;
    };
    let name = parse_name(name_source, "schema.query", diagnostics)?;
    match type_index
        .get(&name)
        .and_then(|definition| definition.kind.as_ref())
    {
        Some(TypeKind::Object) => Some(name),
        Some(_) => {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaRootInvalid,
                "schema.query",
                "query root must reference an object definition",
            ));
            None
        }
        None => {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                "schema.query",
                "query root references a missing definition",
            ));
            None
        }
    }
}

fn canonicalize_directives(
    schema: &raw::Schema,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> BTreeMap<SchemaName, DirectiveDefinition> {
    let mut canonical = BTreeMap::new();
    let Some(entries) = schema.directives.as_ref() else {
        return canonical;
    };
    for raw_definition in entries.iter().flatten() {
        let Some(name_source) = raw_definition.name.as_deref() else {
            continue;
        };
        let Ok(name) = SchemaName::try_from(name_source) else {
            continue;
        };
        let coordinate = SchemaCoordinate::directive(&name);
        let mut locations = BTreeSet::new();
        let Some(raw_locations) = raw_definition.locations.as_ref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                coordinate.as_str(),
                "directive definition does not contain locations",
            ));
            continue;
        };
        for location in raw_locations {
            let Some(location) = location else {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    coordinate.as_str(),
                    "directive locations contain a null entry",
                ));
                continue;
            };
            let location = directive_location(location);
            if !locations.insert(location) {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    coordinate.as_str(),
                    "directive definition contains a duplicate location",
                ));
            }
        }
        let mut arguments = BTreeMap::new();
        let Some(raw_arguments) = raw_definition.args.as_ref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                coordinate.as_str(),
                "directive definition does not contain an argument collection",
            ));
            continue;
        };
        for raw_argument in raw_arguments.iter().flatten() {
            let raw_input = &raw_argument.input_value;
            let Some(argument_name) = parse_name(&raw_input.name, coordinate.as_str(), diagnostics)
            else {
                continue;
            };
            let argument_coordinate = SchemaCoordinate::directive_argument(&name, &argument_name);
            if let Some(argument) = canonicalize_input_value(
                raw_input,
                argument_name.clone(),
                argument_coordinate.clone(),
                "ARGUMENT_DEFINITION",
                type_index,
                directive_index,
                diagnostics,
            ) && arguments.insert(argument_name, argument).is_some()
            {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    argument_coordinate.as_str(),
                    "duplicate directive argument",
                ));
            }
        }
        canonical.insert(
            name.clone(),
            DirectiveDefinition {
                name,
                coordinate,
                description: raw_definition.description.clone(),
                locations,
                arguments,
            },
        );
    }
    canonical
}

fn canonicalize_types(
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    compatibility: Option<&super::CoreCoordinateManifest>,
    diagnostics: &mut Vec<Diagnostic>,
) -> BTreeMap<SchemaName, TypeDefinition> {
    let mut types = BTreeMap::new();
    for (name, definition) in type_index {
        if name.as_str().starts_with("__") {
            continue;
        }
        let coordinate = SchemaCoordinate::named_type(name);
        let directives = canonicalize_applications(
            definition.directives.as_ref(),
            type_location(definition.kind.as_ref()),
            &coordinate,
            type_index,
            directive_index,
            diagnostics,
        );
        let canonical = match definition.kind.as_ref() {
            Some(TypeKind::Scalar) => Some(TypeDefinition::Scalar(ScalarDefinition {
                name: name.clone(),
                coordinate,
                description: definition.description.clone(),
                directives,
            })),
            Some(TypeKind::Object) => canonicalize_object(
                name,
                definition,
                directives,
                type_index,
                directive_index,
                compatibility,
                diagnostics,
            )
            .map(TypeDefinition::Object),
            Some(TypeKind::Interface) => canonicalize_interface(
                name,
                definition,
                directives,
                type_index,
                directive_index,
                compatibility,
                diagnostics,
            )
            .map(TypeDefinition::Interface),
            Some(TypeKind::Enum) => canonicalize_enum(
                name,
                definition,
                directives,
                type_index,
                directive_index,
                diagnostics,
            )
            .map(TypeDefinition::Enum),
            Some(TypeKind::InputObject) => canonicalize_input_object(
                name,
                definition,
                directives,
                type_index,
                directive_index,
                diagnostics,
            )
            .map(TypeDefinition::InputObject),
            _ => None,
        };
        if let Some(canonical) = canonical {
            types.insert(name.clone(), canonical);
        }
    }
    types
}

fn canonicalize_object(
    name: &SchemaName,
    definition: &FullType,
    directives: Vec<DirectiveApplication>,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    compatibility: Option<&super::CoreCoordinateManifest>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<ObjectDefinition> {
    let fields = canonicalize_fields(name, definition, type_index, directive_index, diagnostics)?;
    let interfaces = canonicalize_edges(
        name,
        definition.interfaces.as_deref().unwrap_or_default(),
        TypeKind::Interface,
        type_index,
        compatibility,
        diagnostics,
    );
    Some(ObjectDefinition {
        name: name.clone(),
        coordinate: SchemaCoordinate::named_type(name),
        description: definition.description.clone(),
        fields,
        interfaces,
        directives,
    })
}

fn canonicalize_interface(
    name: &SchemaName,
    definition: &FullType,
    directives: Vec<DirectiveApplication>,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    compatibility: Option<&super::CoreCoordinateManifest>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<InterfaceDefinition> {
    let fields = canonicalize_fields(name, definition, type_index, directive_index, diagnostics)?;
    let interfaces = canonicalize_edges(
        name,
        definition.interfaces.as_deref().unwrap_or_default(),
        TypeKind::Interface,
        type_index,
        compatibility,
        diagnostics,
    );
    let possible_types = definition
        .possible_types
        .as_deref()
        .unwrap_or_default()
        .iter()
        .filter_map(|possible| {
            canonicalize_named_edge(
                name,
                &possible.type_ref,
                TypeKind::Object,
                type_index,
                compatibility,
                diagnostics,
            )
        })
        .collect();
    Some(InterfaceDefinition {
        name: name.clone(),
        coordinate: SchemaCoordinate::named_type(name),
        description: definition.description.clone(),
        fields,
        interfaces,
        possible_types,
        directives,
    })
}

fn canonicalize_enum(
    name: &SchemaName,
    definition: &FullType,
    directives: Vec<DirectiveApplication>,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<EnumDefinition> {
    let Some(raw_values) = definition.enum_values.as_ref() else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaReferenceInvalid,
            name.as_str(),
            "enum definition does not contain values",
        ));
        return None;
    };
    let mut values = BTreeMap::new();
    for (position, raw_value) in raw_values.iter().enumerate() {
        let fallback = format!("{}[{}]", name, position);
        report_unknown(&raw_value.unknown_fields, &fallback, diagnostics);
        let Some(name_source) = raw_value.name.as_deref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                &fallback,
                "enum value does not contain a name",
            ));
            continue;
        };
        let Some(value_name) = parse_name(name_source, &fallback, diagnostics) else {
            continue;
        };
        let coordinate = SchemaCoordinate::enum_value(name, &value_name);
        let value_directives = canonicalize_applications(
            raw_value.directives.as_ref(),
            "ENUM_VALUE",
            &coordinate,
            type_index,
            directive_index,
            diagnostics,
        );
        let deprecation = validate_deprecation(
            raw_value.is_deprecated,
            raw_value.deprecation_reason.as_deref(),
            &value_directives,
            &coordinate,
            diagnostics,
        );
        let value = EnumValueDefinition {
            name: value_name.clone(),
            coordinate: coordinate.clone(),
            description: raw_value.description.clone(),
            directives: value_directives,
            deprecation,
        };
        if values.insert(value_name, value).is_some() {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                coordinate.as_str(),
                "duplicate enum value",
            ));
        }
    }
    Some(EnumDefinition {
        name: name.clone(),
        coordinate: SchemaCoordinate::named_type(name),
        description: definition.description.clone(),
        values,
        directives,
    })
}

fn canonicalize_input_object(
    name: &SchemaName,
    definition: &FullType,
    directives: Vec<DirectiveApplication>,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<InputObjectDefinition> {
    let Some(raw_fields) = definition.input_fields.as_ref() else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaReferenceInvalid,
            name.as_str(),
            "input-object definition does not contain fields",
        ));
        return None;
    };
    let mut fields = BTreeMap::new();
    for raw_field in raw_fields {
        let raw_input = &raw_field.input_value;
        let Some(field_name) = parse_name(&raw_input.name, name.as_str(), diagnostics) else {
            continue;
        };
        let coordinate = SchemaCoordinate::input_field(name, &field_name);
        let Some(argument) = canonicalize_input_value(
            raw_input,
            field_name.clone(),
            coordinate.clone(),
            "INPUT_FIELD_DEFINITION",
            type_index,
            directive_index,
            diagnostics,
        ) else {
            continue;
        };
        let field = InputFieldDefinition {
            name: argument.name,
            coordinate: argument.coordinate,
            description: argument.description,
            type_use: argument.type_use,
            default: argument.default,
            directives: argument.directives,
            deprecation: argument.deprecation,
        };
        if fields.insert(field_name, field).is_some() {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                coordinate.as_str(),
                "duplicate input-object field",
            ));
        }
    }
    Some(InputObjectDefinition {
        name: name.clone(),
        coordinate: SchemaCoordinate::named_type(name),
        description: definition.description.clone(),
        fields,
        directives,
    })
}

fn canonicalize_fields(
    owner: &SchemaName,
    definition: &FullType,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<BTreeMap<SchemaName, FieldDefinition>> {
    let Some(raw_fields) = definition.fields.as_ref() else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaReferenceInvalid,
            owner.as_str(),
            "object or interface definition does not contain fields",
        ));
        return None;
    };
    let mut fields = BTreeMap::new();
    for (position, raw_field) in raw_fields.iter().enumerate() {
        let fallback = format!("{}[{}]", owner, position);
        report_unknown(&raw_field.unknown_fields, &fallback, diagnostics);
        let Some(name_source) = raw_field.name.as_deref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                &fallback,
                "field does not contain a name",
            ));
            continue;
        };
        let Some(field_name) = parse_name(name_source, &fallback, diagnostics) else {
            continue;
        };
        let coordinate = SchemaCoordinate::field(owner, &field_name);
        let Some(type_ref) = raw_field.type_.as_ref().map(|value| &value.type_ref) else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                coordinate.as_str(),
                "field does not contain a result type",
            ));
            continue;
        };
        let Some(type_use) = canonicalize_type_ref(type_ref, &coordinate, type_index, diagnostics)
        else {
            continue;
        };
        let directives = canonicalize_applications(
            raw_field.directives.as_ref(),
            "FIELD_DEFINITION",
            &coordinate,
            type_index,
            directive_index,
            diagnostics,
        );
        let deprecation = validate_deprecation(
            raw_field.is_deprecated,
            raw_field.deprecation_reason.as_deref(),
            &directives,
            &coordinate,
            diagnostics,
        );
        let arguments = canonicalize_field_arguments(
            owner,
            &field_name,
            raw_field,
            type_index,
            directive_index,
            diagnostics,
        );
        let field = FieldDefinition {
            name: field_name.clone(),
            coordinate: coordinate.clone(),
            description: raw_field.description.clone(),
            arguments,
            type_use,
            directives,
            deprecation,
        };
        if fields.insert(field_name, field).is_some() {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                coordinate.as_str(),
                "duplicate field",
            ));
        }
    }
    Some(fields)
}

fn canonicalize_field_arguments(
    owner: &SchemaName,
    field_name: &SchemaName,
    field: &FullTypeField,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> BTreeMap<SchemaName, ArgumentDefinition> {
    let mut arguments = BTreeMap::new();
    let Some(raw_arguments) = field.args.as_ref() else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaReferenceInvalid,
            &SchemaCoordinate::field(owner, field_name).to_string(),
            "field does not contain an argument collection",
        ));
        return arguments;
    };
    for (position, raw_argument) in raw_arguments.iter().enumerate() {
        let fallback = format!("{owner}.{field_name}[{position}]");
        let Some(raw_argument) = raw_argument.as_ref() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                &fallback,
                "argument collection contains a null entry",
            ));
            continue;
        };
        let raw_input = &raw_argument.input_value;
        let Some(argument_name) = parse_name(&raw_input.name, &fallback, diagnostics) else {
            continue;
        };
        let coordinate = SchemaCoordinate::argument(owner, field_name, &argument_name);
        if let Some(argument) = canonicalize_input_value(
            raw_input,
            argument_name.clone(),
            coordinate.clone(),
            "ARGUMENT_DEFINITION",
            type_index,
            directive_index,
            diagnostics,
        ) && arguments.insert(argument_name, argument).is_some()
        {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                coordinate.as_str(),
                "duplicate field argument",
            ));
        }
    }
    arguments
}

#[allow(clippy::too_many_arguments)]
fn canonicalize_input_value(
    raw_input: &InputValue,
    name: SchemaName,
    coordinate: SchemaCoordinate,
    directive_location: &str,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<ArgumentDefinition> {
    report_unknown(&raw_input.unknown_fields, coordinate.as_str(), diagnostics);
    let type_use = canonicalize_type_ref(&raw_input.type_, &coordinate, type_index, diagnostics)?;
    let default = raw_input.default_value.as_deref().and_then(|source| {
        let value = match parse_const(&coordinate, source) {
            Ok(value) => value,
            Err(diagnostic) => {
                diagnostics.push(diagnostic);
                return None;
            }
        };
        if validate_const_type(&value, &type_use, type_index, &coordinate, 0, diagnostics) {
            Some(value)
        } else {
            None
        }
    });
    let directives = canonicalize_applications(
        raw_input.directives.as_ref(),
        directive_location,
        &coordinate,
        type_index,
        directive_index,
        diagnostics,
    );
    let deprecation = validate_deprecation(
        raw_input.is_deprecated,
        raw_input.deprecation_reason.as_deref(),
        &directives,
        &coordinate,
        diagnostics,
    );
    Some(ArgumentDefinition {
        name,
        coordinate,
        description: raw_input.description.clone(),
        type_use,
        default,
        directives,
        deprecation,
    })
}

fn canonicalize_type_ref(
    type_ref: &TypeRef,
    coordinate: &SchemaCoordinate,
    type_index: &TypeIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<TypeUse> {
    let mut active = BTreeSet::new();
    canonicalize_type_ref_inner(
        type_ref,
        coordinate,
        type_index,
        0,
        &mut active,
        diagnostics,
    )
}

fn canonicalize_type_ref_inner(
    type_ref: &TypeRef,
    coordinate: &SchemaCoordinate,
    type_index: &TypeIndex<'_>,
    depth: usize,
    active: &mut BTreeSet<usize>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<TypeUse> {
    if depth > MAX_WRAPPER_DEPTH {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaWrapperInvalid,
            coordinate.as_str(),
            "type wrapper exceeds the maximum depth of 64",
        ));
        return None;
    }
    let identity = std::ptr::from_ref(type_ref) as usize;
    if !active.insert(identity) {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaWrapperInvalid,
            coordinate.as_str(),
            "type wrapper contains a repeated active node",
        ));
        return None;
    }
    report_unknown(&type_ref.unknown_fields, coordinate.as_str(), diagnostics);
    if type_ref.interfaces.is_some()
        || type_ref.possible_types.is_some()
        || type_ref.directives.is_some()
    {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaWrapperInvalid,
            coordinate.as_str(),
            "type reference contains definition-only expansion data",
        ));
    }
    let result = match type_ref.kind.as_ref() {
        Some(TypeKind::NonNull) => {
            if type_ref.name.is_some() {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaWrapperInvalid,
                    coordinate.as_str(),
                    "NON_NULL wrapper cannot contain a name",
                ));
                None
            } else if matches!(
                type_ref
                    .of_type
                    .as_deref()
                    .and_then(|inner| inner.kind.as_ref()),
                Some(TypeKind::NonNull)
            ) {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaWrapperInvalid,
                    coordinate.as_str(),
                    "NON_NULL wrapper cannot directly contain NON_NULL",
                ));
                None
            } else if let Some(inner) = type_ref.of_type.as_deref() {
                canonicalize_type_ref_inner(
                    inner,
                    coordinate,
                    type_index,
                    depth + 1,
                    active,
                    diagnostics,
                )
                .map(|mut type_use| {
                    type_use.nullable = false;
                    type_use
                })
            } else {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaWrapperInvalid,
                    coordinate.as_str(),
                    "NON_NULL wrapper does not contain ofType",
                ));
                None
            }
        }
        Some(TypeKind::List) => {
            if type_ref.name.is_some() {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaWrapperInvalid,
                    coordinate.as_str(),
                    "LIST wrapper cannot contain a name",
                ));
                None
            } else if let Some(inner) = type_ref.of_type.as_deref() {
                canonicalize_type_ref_inner(
                    inner,
                    coordinate,
                    type_index,
                    depth + 1,
                    active,
                    diagnostics,
                )
                .map(|element| TypeUse {
                    nullable: true,
                    shape: TypeShape::List(Box::new(element)),
                })
            } else {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaWrapperInvalid,
                    coordinate.as_str(),
                    "LIST wrapper does not contain ofType",
                ));
                None
            }
        }
        Some(
            kind @ (TypeKind::Scalar
            | TypeKind::Object
            | TypeKind::Interface
            | TypeKind::Enum
            | TypeKind::InputObject),
        ) => {
            if type_ref.of_type.is_some() {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaWrapperInvalid,
                    coordinate.as_str(),
                    "named type reference cannot contain ofType",
                ));
                None
            } else if let Some(name_source) = type_ref.name.as_deref() {
                let name = parse_name(name_source, coordinate.as_str(), diagnostics)?;
                match type_index
                    .get(&name)
                    .and_then(|definition| definition.kind.as_ref())
                {
                    Some(definition_kind) if same_named_kind(kind, definition_kind) => {
                        Some(TypeUse {
                            nullable: true,
                            shape: TypeShape::Named(name),
                        })
                    }
                    Some(_) => {
                        diagnostics.push(schema_error(
                            DiagnosticCode::SchemaReferenceInvalid,
                            coordinate.as_str(),
                            "type reference kind disagrees with its definition",
                        ));
                        None
                    }
                    None => {
                        diagnostics.push(schema_error(
                            DiagnosticCode::SchemaReferenceInvalid,
                            coordinate.as_str(),
                            "type reference names a missing definition",
                        ));
                        None
                    }
                }
            } else {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaReferenceInvalid,
                    coordinate.as_str(),
                    "named type reference does not contain a name",
                ));
                None
            }
        }
        Some(TypeKind::Union | TypeKind::Other(_)) => {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaTypeUnsupported,
                coordinate.as_str(),
                "type reference uses an unsupported named kind",
            ));
            None
        }
        None => {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                coordinate.as_str(),
                "type reference does not contain a kind",
            ));
            None
        }
    };
    active.remove(&identity);
    result
}

fn canonicalize_edges(
    owner: &SchemaName,
    edges: &[raw::FullTypeInterface],
    expected_kind: TypeKind,
    type_index: &TypeIndex<'_>,
    compatibility: Option<&super::CoreCoordinateManifest>,
    diagnostics: &mut Vec<Diagnostic>,
) -> BTreeSet<SchemaName> {
    edges
        .iter()
        .filter_map(|edge| {
            canonicalize_named_edge(
                owner,
                &edge.type_ref,
                expected_kind.clone(),
                type_index,
                compatibility,
                diagnostics,
            )
        })
        .collect()
}

fn canonicalize_named_edge(
    owner: &SchemaName,
    type_ref: &TypeRef,
    expected_kind: TypeKind,
    type_index: &TypeIndex<'_>,
    compatibility: Option<&super::CoreCoordinateManifest>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<SchemaName> {
    let coordinate = SchemaCoordinate::named_type(owner);
    if type_ref.of_type.is_none()
        && type_ref
            .kind
            .as_ref()
            .is_some_and(|kind| same_named_kind(&expected_kind, kind))
        && let Some(name_source) = type_ref.name.as_deref()
        && let Some(name) = parse_name(name_source, coordinate.as_str(), diagnostics)
        && !type_index.contains_key(&name)
        && compatibility.is_some_and(|manifest| manifest.permits_missing_type(&name))
    {
        return Some(name);
    }
    let type_use = canonicalize_type_ref(type_ref, &coordinate, type_index, diagnostics)?;
    let TypeShape::Named(name) = type_use.shape else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaReferenceInvalid,
            coordinate.as_str(),
            "interface edge must be an unwrapped named reference",
        ));
        return None;
    };
    let actual_kind = type_index
        .get(&name)
        .and_then(|definition| definition.kind.as_ref());
    if actual_kind.is_some_and(|kind| same_named_kind(&expected_kind, kind)) {
        Some(name)
    } else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaReferenceInvalid,
            coordinate.as_str(),
            "interface edge references the wrong definition kind",
        ));
        None
    }
}

fn canonicalize_applications(
    raw_applications: Option<&Vec<raw::DirectiveApplication>>,
    location: &str,
    coordinate: &SchemaCoordinate,
    type_index: &TypeIndex<'_>,
    directive_index: &DirectiveIndex<'_>,
    diagnostics: &mut Vec<Diagnostic>,
) -> Vec<DirectiveApplication> {
    let mut applications = Vec::new();
    let mut names = BTreeSet::new();
    let Some(raw_applications) = raw_applications else {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaDirectiveArgumentInvalid,
            coordinate.as_str(),
            "schema coordinate does not contain a directive application collection",
        ));
        return applications;
    };
    for raw_application in raw_applications {
        report_unknown(
            &raw_application.unknown_fields,
            coordinate.as_str(),
            diagnostics,
        );
        let Some(name) = parse_name(&raw_application.name, coordinate.as_str(), diagnostics) else {
            continue;
        };
        if !names.insert(name.clone()) {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                coordinate.as_str(),
                "coordinate contains a duplicate directive application",
            ));
        }
        let Some(definition) = directive_index.get(&name).copied() else {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                coordinate.as_str(),
                "directive application references a missing definition",
            ));
            continue;
        };
        let allowed = definition
            .locations
            .as_deref()
            .unwrap_or_default()
            .iter()
            .flatten()
            .any(|candidate| directive_location(candidate) == location);
        if !allowed {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaDirectiveArgumentInvalid,
                coordinate.as_str(),
                "directive is not valid at this schema location",
            ));
        }
        let raw_definition_arguments: BTreeMap<_, _> = definition
            .args
            .as_deref()
            .unwrap_or_default()
            .iter()
            .flatten()
            .map(|argument| (argument.input_value.name.as_str(), &argument.input_value))
            .collect();
        let mut arguments = BTreeMap::new();
        for raw_argument in &raw_application.args {
            report_unknown(
                &raw_argument.unknown_fields,
                coordinate.as_str(),
                diagnostics,
            );
            let Some(argument_name) =
                parse_name(&raw_argument.name, coordinate.as_str(), diagnostics)
            else {
                continue;
            };
            let Some(argument_definition) = raw_definition_arguments
                .get(argument_name.as_str())
                .copied()
            else {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    coordinate.as_str(),
                    "directive application supplies an unknown argument",
                ));
                continue;
            };
            if let Some(source) = raw_argument.value.as_deref() {
                match parse_const(coordinate, source) {
                    Ok(value) => {
                        let argument_type = canonicalize_type_ref(
                            &argument_definition.type_,
                            coordinate,
                            type_index,
                            diagnostics,
                        );
                        if let Some(argument_type) = argument_type {
                            validate_const_type(
                                &value,
                                &argument_type,
                                type_index,
                                coordinate,
                                0,
                                diagnostics,
                            );
                        }
                    }
                    Err(mut diagnostic) => {
                        diagnostic.code = DiagnosticCode::SchemaDirectiveArgumentInvalid;
                        diagnostics.push(diagnostic);
                    }
                }
            } else {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    coordinate.as_str(),
                    "directive application argument does not contain a value",
                ));
            }
            if arguments
                .insert(argument_name, raw_argument.value.clone())
                .is_some()
            {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    coordinate.as_str(),
                    "directive application supplies an argument more than once",
                ));
            }
        }
        for (argument_name, argument_definition) in raw_definition_arguments {
            let required = matches!(argument_definition.type_.kind, Some(TypeKind::NonNull))
                && argument_definition.default_value.is_none();
            let parsed_name = SchemaName::try_from(argument_name);
            if required
                && parsed_name
                    .as_ref()
                    .is_ok_and(|argument_name| !arguments.contains_key(argument_name))
                && !allows_engine_source_map_omission(&name, argument_name, &arguments)
            {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    coordinate.as_str(),
                    "directive application omits a required argument",
                ));
            }
        }
        applications.push(DirectiveApplication { name, arguments });
    }
    applications.sort();
    applications
}

fn allows_engine_source_map_omission(
    directive_name: &SchemaName,
    argument_name: &str,
    supplied: &BTreeMap<SchemaName, Option<String>>,
) -> bool {
    if directive_name.as_str() != "sourceMap"
        || !matches!(argument_name, "filename" | "line" | "column" | "url")
    {
        return false;
    }
    let Ok(module) = SchemaName::try_from("module") else {
        return false;
    };

    // The target declares every sourceMap input non-null, but its dynamic schema
    // merger stamps module-owned types with only this discriminator
    // (core/schematool.go @ 25300124ca110612edc09c43f89cb5fad6028170). Keeping the
    // exception conditional on a valued module argument prevents it from weakening
    // ordinary GraphQL required-argument validation.
    supplied.get(&module).is_some_and(Option::is_some)
}

fn validate_deprecation(
    is_deprecated: Option<bool>,
    legacy_reason: Option<&str>,
    directives: &[DirectiveApplication],
    coordinate: &SchemaCoordinate,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<Deprecation> {
    if is_deprecated.is_none() {
        diagnostics.push(schema_error(
            DiagnosticCode::DeprecationDirectiveInvalid,
            coordinate.as_str(),
            "schema coordinate does not contain legacy deprecation state",
        ));
    }
    let directive = directives
        .iter()
        .find(|directive| directive.name.as_str() == "deprecated");
    let directive_reason = directive
        .and_then(|directive| {
            directive
                .arguments
                .iter()
                .find(|(name, _)| name.as_str() == "reason")
        })
        .and_then(|(_, value)| value.as_deref())
        .and_then(|source| parse_const(coordinate, source).ok())
        .and_then(|value| match value {
            ConstValue::String(value) => Some(value),
            _ => None,
        });
    let legacy_flag = is_deprecated == Some(true);
    if legacy_flag != directive.is_some() {
        diagnostics.push(schema_error(
            DiagnosticCode::DeprecationDirectiveInvalid,
            coordinate.as_str(),
            "legacy deprecation flag disagrees with the @deprecated application",
        ));
    }
    if legacy_reason.is_some() && !legacy_flag {
        diagnostics.push(schema_error(
            DiagnosticCode::DeprecationDirectiveInvalid,
            coordinate.as_str(),
            "deprecation reason is present while the coordinate is not deprecated",
        ));
    }
    if directive.is_some() && directive_reason.as_deref() != legacy_reason {
        diagnostics.push(schema_error(
            DiagnosticCode::DeprecationDirectiveInvalid,
            coordinate.as_str(),
            "legacy deprecation reason disagrees with the directive argument",
        ));
    }
    if legacy_flag || directive.is_some() {
        Some(Deprecation {
            reason: legacy_reason.map(ToOwned::to_owned),
        })
    } else {
        None
    }
}

fn validate_const_type(
    value: &ConstValue,
    type_use: &TypeUse,
    type_index: &TypeIndex<'_>,
    coordinate: &SchemaCoordinate,
    depth: usize,
    diagnostics: &mut Vec<Diagnostic>,
) -> bool {
    if depth > MAX_DEFAULT_DEPTH {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaDefaultInvalid,
            coordinate.as_str(),
            "default value exceeds the maximum nesting depth of 64",
        ));
        return false;
    }
    if matches!(value, ConstValue::Null) {
        if type_use.nullable {
            return true;
        }
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaDefaultInvalid,
            coordinate.as_str(),
            "null default is invalid for a non-null type",
        ));
        return false;
    }
    let valid = match &type_use.shape {
        TypeShape::List(element) => match value {
            ConstValue::List(values) => values.iter().all(|value| {
                validate_const_type(
                    value,
                    element,
                    type_index,
                    coordinate,
                    depth + 1,
                    diagnostics,
                )
            }),
            // GraphQL input coercion permits a singleton value for a list input.
            value => validate_const_type(
                value,
                element,
                type_index,
                coordinate,
                depth + 1,
                diagnostics,
            ),
        },
        TypeShape::Named(name) => {
            validate_named_const(value, name, type_index, coordinate, depth + 1, diagnostics)
        }
    };
    if !valid {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaDefaultInvalid,
            coordinate.as_str(),
            "default value does not conform to its declared GraphQL type",
        ));
    }
    valid
}

fn validate_named_const(
    value: &ConstValue,
    name: &SchemaName,
    type_index: &TypeIndex<'_>,
    coordinate: &SchemaCoordinate,
    depth: usize,
    diagnostics: &mut Vec<Diagnostic>,
) -> bool {
    let Some(definition) = type_index.get(name).copied() else {
        return false;
    };
    match definition.kind.as_ref() {
        Some(TypeKind::Scalar) => match name.as_str() {
            "Boolean" => matches!(value, ConstValue::Boolean(_)),
            "Int" => matches!(value, ConstValue::Int(_)),
            "Float" => matches!(value, ConstValue::Int(_) | ConstValue::Float(_)),
            "String" | "Platform" => matches!(value, ConstValue::String(_)),
            "ID" => matches!(value, ConstValue::String(_) | ConstValue::Int(_)),
            "JSON" => true,
            "Void" => false,
            _ => false,
        },
        Some(TypeKind::Enum) => {
            let ConstValue::Enum(value_name) = value else {
                return false;
            };
            definition.enum_values.as_deref().is_some_and(|values| {
                values
                    .iter()
                    .any(|candidate| candidate.name.as_deref() == Some(value_name.as_str()))
            })
        }
        Some(TypeKind::InputObject) => {
            let ConstValue::Object(values) = value else {
                return false;
            };
            let Some(fields) = definition.input_fields.as_deref() else {
                return false;
            };
            let field_index: BTreeMap<_, _> = fields
                .iter()
                .map(|field| (field.input_value.name.as_str(), &field.input_value))
                .collect();
            if values
                .keys()
                .any(|field| !field_index.contains_key(field.as_str()))
            {
                return false;
            }
            for (field_name, field) in &field_index {
                let Ok(field_name) = SchemaName::try_from(*field_name) else {
                    return false;
                };
                let field_coordinate = SchemaCoordinate::input_field(name, &field_name);
                let Some(field_type) =
                    canonicalize_type_ref(&field.type_, &field_coordinate, type_index, diagnostics)
                else {
                    return false;
                };
                match values.get(&field_name) {
                    Some(field_value) => {
                        if !validate_const_type(
                            field_value,
                            &field_type,
                            type_index,
                            coordinate,
                            depth,
                            diagnostics,
                        ) {
                            return false;
                        }
                    }
                    None if !field_type.nullable && field.default_value.is_none() => return false,
                    None => {}
                }
            }
            true
        }
        _ => false,
    }
}

fn validate_interface_consistency(
    types: &BTreeMap<SchemaName, TypeDefinition>,
    compatibility: Option<&super::CoreCoordinateManifest>,
    diagnostics: &mut Vec<Diagnostic>,
) {
    for (interface_name, definition) in types {
        let TypeDefinition::Interface(interface) = definition else {
            continue;
        };
        for object_name in &interface.possible_types {
            if !types.contains_key(object_name)
                && compatibility.is_some_and(|manifest| manifest.permits_missing_type(object_name))
            {
                continue;
            }
            let implements = matches!(
                types.get(object_name),
                Some(TypeDefinition::Object(object)) if object.interfaces.contains(interface_name)
            );
            if !implements {
                diagnostics.push(schema_error(
                    DiagnosticCode::SchemaReferenceInvalid,
                    interface.coordinate.as_str(),
                    "interface possible type does not declare the inverse implementation edge",
                ));
            }
        }
    }
}

fn inventory(
    types: &BTreeMap<SchemaName, TypeDefinition>,
    directives: &BTreeMap<SchemaName, DirectiveDefinition>,
    has_query: bool,
) -> CoordinateInventory {
    let mut inventory = CoordinateInventory {
        query_roots: usize::from(has_query),
        named_types: types.len(),
        directives: directives.len(),
        directive_arguments: directives
            .values()
            .map(|directive| directive.arguments.len())
            .sum(),
        ..CoordinateInventory::default()
    };
    for definition in types.values() {
        match definition {
            TypeDefinition::Scalar(_) => inventory.scalars += 1,
            TypeDefinition::Object(object) => {
                inventory.objects += 1;
                inventory.fields += object.fields.len();
                inventory.arguments += object
                    .fields
                    .values()
                    .map(|field| field.arguments.len())
                    .sum::<usize>();
                inventory.interface_edges += object.interfaces.len();
            }
            TypeDefinition::Interface(interface) => {
                inventory.interfaces += 1;
                inventory.fields += interface.fields.len();
                inventory.arguments += interface
                    .fields
                    .values()
                    .map(|field| field.arguments.len())
                    .sum::<usize>();
                inventory.interface_edges += interface.interfaces.len();
            }
            TypeDefinition::Enum(enumeration) => {
                inventory.enums += 1;
                inventory.enum_values += enumeration.values.len();
            }
            TypeDefinition::InputObject(input) => {
                inventory.input_objects += 1;
                inventory.input_fields += input.fields.len();
            }
        }
    }
    inventory
}

fn validate_exact_inventory(inventory: CoordinateInventory, diagnostics: &mut Vec<Diagnostic>) {
    let counts = [
        (
            "query roots",
            inventory.query_roots,
            EXACT_INVENTORY.query_roots,
        ),
        (
            "named types",
            inventory.named_types,
            EXACT_INVENTORY.named_types,
        ),
        ("scalars", inventory.scalars, EXACT_INVENTORY.scalars),
        ("objects", inventory.objects, EXACT_INVENTORY.objects),
        (
            "interfaces",
            inventory.interfaces,
            EXACT_INVENTORY.interfaces,
        ),
        ("enums", inventory.enums, EXACT_INVENTORY.enums),
        (
            "input objects",
            inventory.input_objects,
            EXACT_INVENTORY.input_objects,
        ),
        ("fields", inventory.fields, EXACT_INVENTORY.fields),
        ("arguments", inventory.arguments, EXACT_INVENTORY.arguments),
        (
            "input fields",
            inventory.input_fields,
            EXACT_INVENTORY.input_fields,
        ),
        (
            "enum values",
            inventory.enum_values,
            EXACT_INVENTORY.enum_values,
        ),
        (
            "interface edges",
            inventory.interface_edges,
            EXACT_INVENTORY.interface_edges,
        ),
        (
            "directives",
            inventory.directives,
            EXACT_INVENTORY.directives,
        ),
        (
            "directive arguments",
            inventory.directive_arguments,
            EXACT_INVENTORY.directive_arguments,
        ),
    ];
    for (name, actual, expected) in counts {
        if actual != expected {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                "schema.inventory",
                &format!("exact target {name} count is {actual}; expected {expected}"),
            ));
        }
    }
}

fn parse_name(
    source: &str,
    coordinate: &str,
    diagnostics: &mut Vec<Diagnostic>,
) -> Option<SchemaName> {
    match SchemaName::try_from(source) {
        Ok(name) => Some(name),
        Err(()) => {
            diagnostics.push(schema_error(
                DiagnosticCode::SchemaReferenceInvalid,
                coordinate,
                "schema Wire_Name is not a valid GraphQL name",
            ));
            None
        }
    }
}

fn report_unknown(
    unknown_fields: &UnknownFields,
    coordinate: &str,
    diagnostics: &mut Vec<Diagnostic>,
) {
    for name in unknown_fields.keys() {
        diagnostics.push(schema_error(
            DiagnosticCode::SchemaReferenceInvalid,
            coordinate,
            &format!("unrecognized schema member `{name}`"),
        ));
    }
}

fn same_named_kind(left: &TypeKind, right: &TypeKind) -> bool {
    matches!(
        (left, right),
        (TypeKind::Scalar, TypeKind::Scalar)
            | (TypeKind::Object, TypeKind::Object)
            | (TypeKind::Interface, TypeKind::Interface)
            | (TypeKind::Enum, TypeKind::Enum)
            | (TypeKind::InputObject, TypeKind::InputObject)
            | (TypeKind::Union, TypeKind::Union)
    )
}

fn type_location(kind: Option<&TypeKind>) -> &'static str {
    match kind {
        Some(TypeKind::Scalar) => "SCALAR",
        Some(TypeKind::Object) => "OBJECT",
        Some(TypeKind::Interface) => "INTERFACE",
        Some(TypeKind::Union) => "UNION",
        Some(TypeKind::Enum) => "ENUM",
        Some(TypeKind::InputObject) => "INPUT_OBJECT",
        _ => "SCHEMA",
    }
}

fn directive_location(location: &raw::DirectiveLocation) -> String {
    match location {
        raw::DirectiveLocation::Query => "QUERY",
        raw::DirectiveLocation::Mutation => "MUTATION",
        raw::DirectiveLocation::Subscription => "SUBSCRIPTION",
        raw::DirectiveLocation::Field => "FIELD",
        raw::DirectiveLocation::FragmentDefinition => "FRAGMENT_DEFINITION",
        raw::DirectiveLocation::FragmentSpread => "FRAGMENT_SPREAD",
        raw::DirectiveLocation::InlineFragment => "INLINE_FRAGMENT",
        raw::DirectiveLocation::Schema => "SCHEMA",
        raw::DirectiveLocation::Scalar => "SCALAR",
        raw::DirectiveLocation::Object => "OBJECT",
        raw::DirectiveLocation::FieldDefinition => "FIELD_DEFINITION",
        raw::DirectiveLocation::ArgumentDefinition => "ARGUMENT_DEFINITION",
        raw::DirectiveLocation::Interface => "INTERFACE",
        raw::DirectiveLocation::Union => "UNION",
        raw::DirectiveLocation::Enum => "ENUM",
        raw::DirectiveLocation::EnumValue => "ENUM_VALUE",
        raw::DirectiveLocation::InputObject => "INPUT_OBJECT",
        raw::DirectiveLocation::InputFieldDefinition => "INPUT_FIELD_DEFINITION",
        raw::DirectiveLocation::Other(value) => value,
    }
    .to_owned()
}

fn schema_error(code: DiagnosticCode, coordinate: &str, message: &str) -> Diagnostic {
    Diagnostic::new(code, Some(DiagnosticCoordinate::new(coordinate)), message)
}

fn diagnostic_set(diagnostics: Vec<Diagnostic>) -> DiagnosticSet {
    match DiagnosticSet::new(diagnostics) {
        Some(diagnostics) => diagnostics,
        None => DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::SchemaRootInvalid,
            Some(DiagnosticCoordinate::new("schema")),
            "schema validation failed without a diagnostic",
        )),
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::panic::{AssertUnwindSafe, catch_unwind};
    use std::sync::LazyLock;

    use proptest::prelude::*;
    use proptest::test_runner::{Config, FileFailurePersistence};

    use crate::diagnostic::DiagnosticCode;
    use crate::render_canonical_checkpoint;
    use crate::schema::raw::{
        DirectiveApplication as RawDirectiveApplication, FullTypeField, FullTypeFieldType,
        IntrospectionResponse, Schema, TypeKind, TypeRef,
    };
    use crate::target::CodegenTarget;

    use super::validate_response;

    const TARGET_BYTES: &[u8] = include_bytes!("../../../../completeness/target.json");
    const SCHEMA_BYTES: &[u8] = include_bytes!("../../../../completeness/snapshots/schema.json");

    static TARGET: LazyLock<CodegenTarget> = LazyLock::new(|| {
        CodegenTarget::decode_exact(TARGET_BYTES).expect("checked target must decode")
    });
    static RESPONSE: LazyLock<IntrospectionResponse> =
        LazyLock::new(|| serde_json::from_slice(SCHEMA_BYTES).expect("checked schema must decode"));

    fn property_config(name: &'static str) -> Config {
        Config {
            cases: 256,
            failure_persistence: Some(Box::new(FileFailurePersistence::Direct(name))),
            ..Config::default()
        }
    }

    proptest! {
        #![proptest_config(property_config(concat!(env!("CARGO_MANIFEST_DIR"), "/proptest-regressions/schema-validation.txt")))]

        // Feature: rust-sdk-core-codegen, Property 4: Schema validation is total and coordinate-complete
        #[test]
        fn property_04_schema_validation_total_coordinate_complete(
            mutation in 0_u8..13,
        ) {
            let mut response = RESPONSE.clone();
            let expected = mutate_malformed(&mut response, mutation);
            let result = catch_unwind(AssertUnwindSafe(|| validate_response(&TARGET, &response)));

            prop_assert!(result.is_ok());
            let validation = result.expect("validator must not panic");
            let diagnostics = validation.expect_err("malformed graph must be rejected atomically");
            prop_assert!(diagnostics.diagnostics().iter().all(|diagnostic| diagnostic.coordinate.is_some()));
            let contains_reference_diagnostic = diagnostics.diagnostics().iter().any(|diagnostic| {
                diagnostic.code == expected.0
                    && diagnostic
                        .coordinate
                        .as_ref()
                        .is_some_and(|coordinate| coordinate.as_str().contains(&expected.1))
            });
            prop_assert!(contains_reference_diagnostic);
        }
    }

    #[test]
    fn malformed_fixture_matrix_has_stable_codes_and_coordinates() {
        for mutation in 0_u8..13 {
            let mut response = RESPONSE.clone();
            let expected = mutate_malformed(&mut response, mutation);
            let diagnostics = validate_response(&TARGET, &response)
                .expect_err("fixed malformed fixture must be rejected");
            assert!(diagnostics.diagnostics().iter().any(|diagnostic| {
                diagnostic.code == expected.0
                    && diagnostic
                        .coordinate
                        .as_ref()
                        .is_some_and(|coordinate| coordinate.as_str().contains(&expected.1))
            }));
        }
    }

    proptest! {
        #![proptest_config(property_config(concat!(env!("CARGO_MANIFEST_DIR"), "/proptest-regressions/schema-order.txt")))]

        // Feature: rust-sdk-core-codegen, Property 5: Canonicalization and rendering ignore source order
        #[test]
        fn property_05_canonicalization_rendering_ignore_source_order(
            keys in any::<[u8; 10]>(),
        ) {
            let baseline = validate_response(&TARGET, &RESPONSE)
                .expect("checked schema must validate");
            let baseline_render = render_canonical_checkpoint(&TARGET, &baseline)
                .expect("checked schema must render");
            let mut permuted = RESPONSE.clone();
            permute_response(&mut permuted, keys);
            let candidate = validate_response(&TARGET, &permuted)
                .expect("permuted checked schema must validate");
            let candidate_render = render_canonical_checkpoint(&TARGET, &candidate)
                .expect("permuted checked schema must render");

            prop_assert_eq!(candidate, baseline);
            prop_assert_eq!(candidate_render, baseline_render);
        }
    }

    fn mutate_malformed(
        response: &mut IntrospectionResponse,
        mutation: u8,
    ) -> (DiagnosticCode, String) {
        let schema = schema_mut(response);
        match mutation {
            0 => {
                schema.query_type = None;
                (DiagnosticCode::SchemaRootInvalid, "schema.query".to_owned())
            }
            1 => {
                schema
                    .query_type
                    .as_mut()
                    .expect("checked schema must have a query root")
                    .name = Some("MissingQuery".to_owned());
                (
                    DiagnosticCode::SchemaReferenceInvalid,
                    "schema.query".to_owned(),
                )
            }
            2 => {
                let definitions = schema.types.as_mut().expect("checked types must exist");
                let duplicate = definitions
                    .iter()
                    .flatten()
                    .find(|definition| definition.full_type.name.as_deref() == Some("Address"))
                    .expect("checked Address definition must exist")
                    .clone();
                definitions.push(Some(duplicate));
                (DiagnosticCode::SchemaReferenceInvalid, "Address".to_owned())
            }
            3 => {
                let (_, _, field) = first_public_field(schema);
                field.type_ = Some(FullTypeFieldType {
                    type_ref: TypeRef {
                        kind: Some(TypeKind::List),
                        name: None,
                        of_type: None,
                        interfaces: None,
                        possible_types: None,
                        directives: None,
                        unknown_fields: BTreeMap::new(),
                    },
                });
                (DiagnosticCode::SchemaWrapperInvalid, "Address".to_owned())
            }
            4 => {
                let (_, _, field) = first_public_field(schema);
                let leaf = field
                    .type_
                    .as_ref()
                    .expect("checked field type must exist")
                    .type_ref
                    .clone();
                let inner = TypeRef {
                    kind: Some(TypeKind::NonNull),
                    name: None,
                    of_type: Some(Box::new(leaf)),
                    interfaces: None,
                    possible_types: None,
                    directives: None,
                    unknown_fields: BTreeMap::new(),
                };
                field.type_ = Some(FullTypeFieldType {
                    type_ref: TypeRef {
                        kind: Some(TypeKind::NonNull),
                        name: None,
                        of_type: Some(Box::new(inner)),
                        interfaces: None,
                        possible_types: None,
                        directives: None,
                        unknown_fields: BTreeMap::new(),
                    },
                });
                (DiagnosticCode::SchemaWrapperInvalid, "Address".to_owned())
            }
            5 => {
                let (_, _, field) = first_public_field(schema);
                let leaf = field
                    .type_
                    .as_ref()
                    .expect("checked field type must exist")
                    .type_ref
                    .clone();
                let mut wrapped = leaf;
                for _ in 0..66 {
                    wrapped = TypeRef {
                        kind: Some(TypeKind::List),
                        name: None,
                        of_type: Some(Box::new(wrapped)),
                        interfaces: None,
                        possible_types: None,
                        directives: None,
                        unknown_fields: BTreeMap::new(),
                    };
                }
                field.type_ = Some(FullTypeFieldType { type_ref: wrapped });
                (DiagnosticCode::SchemaWrapperInvalid, "Address".to_owned())
            }
            6 => {
                let (owner, field_name, field) = first_field_with_argument(schema);
                field
                    .args
                    .as_mut()
                    .expect("checked field arguments must exist")
                    .iter_mut()
                    .flatten()
                    .next()
                    .expect("selected field must have an argument")
                    .input_value
                    .default_value = Some("{".to_owned());
                (
                    DiagnosticCode::SchemaDefaultInvalid,
                    format!("{owner}.{field_name}"),
                )
            }
            7 => {
                let (_, _, field) = first_public_field(schema);
                field
                    .directives
                    .get_or_insert_with(Vec::new)
                    .push(RawDirectiveApplication {
                        name: "missingDirective".to_owned(),
                        args: Vec::new(),
                        unknown_fields: BTreeMap::new(),
                    });
                (
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    "Address".to_owned(),
                )
            }
            8 => {
                let definitions = schema.types.as_mut().expect("checked types must exist");
                let definition = definitions
                    .iter_mut()
                    .flatten()
                    .find(|definition| definition.full_type.name.as_deref() == Some("Address"))
                    .expect("checked Address definition must exist");
                definition.full_type.kind = Some(TypeKind::Union);
                (DiagnosticCode::SchemaTypeUnsupported, "Address".to_owned())
            }
            9 => {
                let definitions = schema.types.as_mut().expect("checked types must exist");
                let definition = definitions
                    .iter_mut()
                    .flatten()
                    .find(|definition| definition.full_type.name.as_deref() == Some("Address"))
                    .expect("checked Address definition must exist");
                definition
                    .full_type
                    .unknown_fields
                    .insert("futureMember".to_owned(), serde_json::Value::Bool(true));
                (
                    DiagnosticCode::SchemaReferenceInvalid,
                    "schema.types".to_owned(),
                )
            }
            10 => {
                schema
                    .types
                    .as_mut()
                    .expect("checked types must exist")
                    .push(None);
                (
                    DiagnosticCode::SchemaReferenceInvalid,
                    "schema.types".to_owned(),
                )
            }
            11 => {
                let (_, _, field) = first_public_field(schema);
                field.name = Some("not valid".to_owned());
                (DiagnosticCode::SchemaReferenceInvalid, "Address".to_owned())
            }
            _ => {
                let (_, _, field) = first_public_field(schema);
                field.is_deprecated = Some(true);
                field.deprecation_reason = Some("changed".to_owned());
                field.directives = Some(Vec::new());
                (
                    DiagnosticCode::DeprecationDirectiveInvalid,
                    "Address".to_owned(),
                )
            }
        }
    }

    fn first_public_field(schema: &mut Schema) -> (String, String, &mut FullTypeField) {
        let definition = schema
            .types
            .as_mut()
            .expect("checked types must exist")
            .iter_mut()
            .flatten()
            .find(|definition| definition.full_type.name.as_deref() == Some("Address"))
            .expect("checked Address definition must exist");
        let owner = definition
            .full_type
            .name
            .clone()
            .expect("checked type name must exist");
        let field = definition
            .full_type
            .fields
            .as_mut()
            .expect("checked Address fields must exist")
            .first_mut()
            .expect("checked Address must have fields");
        let name = field.name.clone().expect("checked field name must exist");
        (owner, name, field)
    }

    fn first_field_with_argument(schema: &mut Schema) -> (String, String, &mut FullTypeField) {
        for definition in schema
            .types
            .as_mut()
            .expect("checked types must exist")
            .iter_mut()
            .flatten()
        {
            let owner = definition.full_type.name.clone().unwrap_or_default();
            for field in definition.full_type.fields.as_mut().into_iter().flatten() {
                if field
                    .args
                    .as_ref()
                    .is_some_and(|arguments| !arguments.is_empty())
                {
                    let name = field.name.clone().unwrap_or_default();
                    return (owner, name, field);
                }
            }
        }
        panic!("checked schema must contain a field argument")
    }

    fn schema_mut(response: &mut IntrospectionResponse) -> &mut Schema {
        match response {
            IntrospectionResponse::FullResponse(response) => response
                .data
                .schema
                .as_mut()
                .expect("checked response must contain schema"),
            IntrospectionResponse::Schema(container) => container
                .schema
                .as_mut()
                .expect("checked response must contain schema"),
        }
    }

    fn permute_response(response: &mut IntrospectionResponse, keys: [u8; 10]) {
        let schema = schema_mut(response);
        if let Some(types) = schema.types.as_mut() {
            for definition in types.iter_mut().flatten() {
                let full_type = &mut definition.full_type;
                permute_option(&mut full_type.fields, keys[0]);
                if let Some(fields) = full_type.fields.as_mut() {
                    for field in fields {
                        permute_option(&mut field.args, keys[1]);
                        permute_applications(&mut field.directives, keys[6], keys[7]);
                        if let Some(arguments) = field.args.as_mut() {
                            for argument in arguments.iter_mut().flatten() {
                                permute_applications(
                                    &mut argument.input_value.directives,
                                    keys[6],
                                    keys[7],
                                );
                            }
                        }
                    }
                }
                permute_option(&mut full_type.input_fields, keys[2]);
                if let Some(fields) = full_type.input_fields.as_mut() {
                    for field in fields {
                        permute_applications(&mut field.input_value.directives, keys[6], keys[7]);
                    }
                }
                permute_option(&mut full_type.enum_values, keys[3]);
                if let Some(values) = full_type.enum_values.as_mut() {
                    for value in values {
                        permute_applications(&mut value.directives, keys[6], keys[7]);
                    }
                }
                permute_option(&mut full_type.interfaces, keys[4]);
                permute_option(&mut full_type.possible_types, keys[5]);
                permute_applications(&mut full_type.directives, keys[6], keys[7]);
            }
            permute(types, keys[8]);
        }
        if let Some(directives) = schema.directives.as_mut() {
            for directive in directives.iter_mut().flatten() {
                permute_option(&mut directive.locations, keys[4]);
                permute_option(&mut directive.args, keys[1]);
                if let Some(arguments) = directive.args.as_mut() {
                    for argument in arguments.iter_mut().flatten() {
                        permute_applications(
                            &mut argument.input_value.directives,
                            keys[6],
                            keys[7],
                        );
                    }
                }
            }
            permute(directives, keys[9]);
        }
    }

    fn permute_applications(
        applications: &mut Option<Vec<RawDirectiveApplication>>,
        application_key: u8,
        argument_key: u8,
    ) {
        if let Some(applications) = applications {
            for application in applications.iter_mut() {
                permute(&mut application.args, argument_key);
            }
            permute(applications, application_key);
        }
    }

    fn permute_option<T>(values: &mut Option<Vec<T>>, key: u8) {
        if let Some(values) = values {
            permute(values, key);
        }
    }

    fn permute<T>(values: &mut [T], key: u8) {
        if values.len() < 2 {
            return;
        }
        let rotation = usize::from(key) % values.len();
        values.rotate_left(rotation);
        if key & 0x80 != 0 {
            values.reverse();
        }
    }
}
