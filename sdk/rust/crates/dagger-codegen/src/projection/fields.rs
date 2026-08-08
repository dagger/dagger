//! Total field, argument, and execution-strategy projection.
//!
//! Every canonical field receives exactly one strategy. A strategy contains enough
//! information for later rendering and runtime wiring without reopening raw schema or
//! guessing whether an omitted argument was a concrete zero-like value.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::directive::DirectiveProjection;
use crate::naming::{NameContext, RustNameMap, rust_name, rust_name_from_candidate};
use crate::schema::canonical::{
    CanonicalSchema, FieldDefinition, SchemaCoordinate, SchemaName, TypeDefinition, TypeShape,
    TypeUse,
};
use crate::schema::defaults::ConstValue;

use super::types::{
    DecodePlan, RustType, WrapperPlan, contains_list, has_id_field, named_leaf, project_input_type,
    project_output_type,
};

/// Whether a caller must supply an input or may omit it entirely.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum ArgumentPresence {
    /// A non-null input without an engine default is a direct constructor or method input.
    Required,
    /// The containing options/input value carries a distinct absence state.
    Omittable {
        /// Parsed engine default retained for documentation and fingerprints only.
        engine_default: Option<ConstValue>,
    },
}

/// A required input was absent while constructing a projected document or value.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum InputPresenceError {
    /// Required inputs have no omission state.
    RequiredOmitted,
}

impl ArgumentPresence {
    /// Derives requiredness from the outer wrapper and schema default.
    #[must_use]
    pub fn for_input(type_use: &TypeUse, default: Option<&ConstValue>) -> Self {
        if !type_use.nullable && default.is_none() {
            Self::Required
        } else {
            Self::Omittable {
                engine_default: default.cloned(),
            }
        }
    }

    /// Returns whether absence must omit the Wire_Name rather than synthesize a value.
    #[must_use]
    pub const fn is_omittable(&self) -> bool {
        matches!(self, Self::Omittable { .. })
    }

    /// Applies the omission contract without interpreting concrete values.
    pub fn resolve<'a, T>(
        &self,
        supplied: Option<&'a T>,
    ) -> Result<Option<&'a T>, InputPresenceError> {
        match (self, supplied) {
            (Self::Required, None) => Err(InputPresenceError::RequiredOmitted),
            (_, supplied) => Ok(supplied),
        }
    }
}

/// Recursive argument serialization policy.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum InputEncoder {
    /// Built-in or handwritten scalar value.
    Value,
    /// Closed enum using its canonical Wire_Name.
    Enum,
    /// Owned input object using exact field Wire_Names.
    InputObject,
    /// Raw or lazily resolved ID constrained to one target type.
    TypedId { target: SchemaName },
    /// Ordered recursive list encoder.
    List(Box<InputEncoder>),
}

impl InputEncoder {
    pub(crate) fn for_type(
        schema: &CanonicalSchema,
        type_use: &TypeUse,
        expected_type: Option<&SchemaName>,
    ) -> Result<Self, Diagnostic> {
        match &type_use.shape {
            TypeShape::List(element) => Self::for_type(schema, element, expected_type)
                .map(|encoder| Self::List(Box::new(encoder))),
            TypeShape::Named(name) => {
                if let Some(target) = expected_type {
                    if name.as_str() != "ID" {
                        return Err(projection_error(
                            DiagnosticCode::ExpectedTypeInvalid,
                            &SchemaCoordinate::named_type(name),
                            "expectedType may only replace an ID input leaf",
                        ));
                    }
                    return Ok(Self::TypedId {
                        target: target.clone(),
                    });
                }
                match schema.types().get(name) {
                    Some(TypeDefinition::Scalar(_)) => Ok(Self::Value),
                    Some(TypeDefinition::Enum(_)) => Ok(Self::Enum),
                    Some(TypeDefinition::InputObject(_)) => Ok(Self::InputObject),
                    Some(TypeDefinition::Object(_) | TypeDefinition::Interface(_)) => {
                        Err(projection_error(
                            DiagnosticCode::SchemaArgumentUnmapped,
                            &SchemaCoordinate::named_type(name),
                            "object inputs require an expectedType ID contract",
                        ))
                    }
                    None => Err(projection_error(
                        DiagnosticCode::SchemaReferenceInvalid,
                        &SchemaCoordinate::named_type(name),
                        "input encoder leaf has no canonical definition",
                    )),
                }
            }
        }
    }

    /// Returns whether document construction must resolve lazy identifiers first.
    #[must_use]
    pub fn contains_lazy_id(&self) -> bool {
        match self {
            Self::TypedId { .. } => true,
            Self::List(element) => element.contains_lazy_id(),
            Self::Value | Self::Enum | Self::InputObject => false,
        }
    }
}

/// One exact field-argument API plan.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ArgumentProjection {
    /// Exact argument coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact argument Wire_Name used in the GraphQL document.
    pub wire_name: SchemaName,
    /// Rust source identifier.
    pub rust_name: String,
    /// Recursive Rust value type excluding an omittable outer carrier.
    pub rust_type: RustType,
    /// Direct required parameter or options-field omission policy.
    pub presence: ArgumentPresence,
    /// Recursive value/ID serializer.
    pub encoder: InputEncoder,
    /// Caller-visible deprecation reason.
    pub deprecation: Option<String>,
    /// Caller-visible experimental stability note.
    pub experimental: Option<String>,
}

impl ArgumentProjection {
    /// Returns the exact Wire_Name/value pair to emit, or omission for absent options.
    pub fn emitted_argument<'a, T>(
        &'a self,
        supplied: Option<&'a T>,
    ) -> Result<Option<(&'a SchemaName, &'a T)>, InputPresenceError> {
        self.presence
            .resolve(supplied)
            .map(|value| value.map(|value| (&self.wire_name, value)))
    }
}

/// One total field execution strategy.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum FieldStrategy {
    /// Extend selection and return a non-null object/interface handle without I/O.
    LazyHandle { target: SchemaName },
    /// Execute an ID probe before returning an optional handle.
    NullableHandle {
        /// Returned object or interface type.
        target: SchemaName,
        /// Complete output wrappers.
        wrappers: WrapperPlan,
        /// Exact target ID field used by the probe.
        id_probe: SchemaCoordinate,
    },
    /// Execute ordered IDs, then re-enter one handle for every returned element.
    ReenterList {
        /// Returned object or interface type.
        target: SchemaName,
        /// Complete list and element wrappers.
        wrappers: WrapperPlan,
        /// Exact target ID field selected for every element.
        id_path: SchemaCoordinate,
    },
    /// Execute and decode a scalar, enum, input value, or Void.
    ExecuteValue { output: RustType },
    /// Execute an ID and reconstruct the declared parent handle.
    ExpectedTypeSelf {
        /// Expected parent object or interface.
        parent: SchemaName,
        /// Exact ID-returning field coordinate.
        id_path: SchemaCoordinate,
    },
    /// Single-underscore engine metadata retained without a public Rust operation.
    TargetPrivate,
}

/// Named-leaf facts needed to select a total field strategy.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum FieldLeafClass {
    /// Scalar, enum, input, or Void value.
    Value,
    /// Object leaf and whether it exposes the required ID surface.
    Object { has_id: bool },
    /// Interface leaf and whether it exposes the required ID surface.
    Interface { has_id: bool },
}

/// Minimal validated field shape used by the strategy reference model.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct FieldStrategyInput {
    /// Whether any recursive wrapper is a list.
    pub list: bool,
    /// Whether the outermost field value is nullable.
    pub nullable: bool,
    /// Named-leaf category and ID capability.
    pub leaf: FieldLeafClass,
    /// Whether a validated self-return `expectedType` policy applies.
    pub expected_type_self: bool,
    /// Whether the definitive Go generator contains this target-private field.
    pub target_private: bool,
}

/// Discriminant selected before target coordinates are attached to a strategy.
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum FieldStrategyKind {
    /// Lazy non-null handle.
    LazyHandle,
    /// Nullable handle resolved through an ID probe.
    NullableHandle,
    /// Ordered list handles reconstructed from IDs.
    ReenterList,
    /// Executing scalar-like value.
    ExecuteValue,
    /// Expected-type self-return ID.
    ExpectedTypeSelf,
    /// Target-private no-symbol policy.
    TargetPrivate,
}

/// Selects one strategy kind or the exact diagnostic required by a lossy shape.
pub fn select_field_strategy(
    input: FieldStrategyInput,
) -> Result<FieldStrategyKind, DiagnosticCode> {
    if input.target_private {
        return Ok(FieldStrategyKind::TargetPrivate);
    }
    if input.expected_type_self {
        return Ok(FieldStrategyKind::ExpectedTypeSelf);
    }
    match input.leaf {
        FieldLeafClass::Value => Ok(FieldStrategyKind::ExecuteValue),
        FieldLeafClass::Object { has_id } | FieldLeafClass::Interface { has_id }
            if input.list && has_id =>
        {
            Ok(FieldStrategyKind::ReenterList)
        }
        FieldLeafClass::Object { has_id: false } | FieldLeafClass::Interface { has_id: false }
            if input.list =>
        {
            Err(DiagnosticCode::ListReentryTypeInvalid)
        }
        FieldLeafClass::Object { has_id } | FieldLeafClass::Interface { has_id }
            if input.nullable && has_id =>
        {
            Ok(FieldStrategyKind::NullableHandle)
        }
        FieldLeafClass::Object { has_id: false } | FieldLeafClass::Interface { has_id: false }
            if input.nullable =>
        {
            Err(DiagnosticCode::ObjectHandleMappingInvalid)
        }
        FieldLeafClass::Object { .. } | FieldLeafClass::Interface { .. } => {
            Ok(FieldStrategyKind::LazyHandle)
        }
    }
}

/// One complete generated operation plan.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct FieldProjection {
    /// Exact field coordinate.
    pub coordinate: SchemaCoordinate,
    /// Owning object or interface Wire_Name.
    pub owner: SchemaName,
    /// Exact field Wire_Name used by selection.
    pub wire_name: SchemaName,
    /// Ordinary Rust method identifier.
    pub rust_name: String,
    /// `_opts` method identifier when omittable arguments exist.
    pub options_method_name: Option<String>,
    /// Field-specific owned options type when omission is supported.
    pub options_type_name: Option<String>,
    /// Arguments in exact Wire_Name order.
    pub arguments: Vec<ArgumentProjection>,
    /// Wrapper-correct Rust output.
    pub return_type: RustType,
    /// Typed wrapper/leaf decoding policy.
    pub decode: DecodePlan,
    /// Complete execution strategy.
    pub strategy: FieldStrategy,
    /// Whether all lazy IDs must resolve before any request can be sent.
    pub all_or_nothing_arguments: bool,
    /// Caller-visible deprecation reason.
    pub deprecation: Option<String>,
    /// Caller-visible experimental stability note.
    pub experimental: Option<String>,
}

pub(crate) fn project_fields(
    schema: &CanonicalSchema,
    names: &RustNameMap,
    directives: &DirectiveProjection,
) -> Result<BTreeMap<SchemaCoordinate, FieldProjection>, DiagnosticSet> {
    let mut fields = BTreeMap::new();
    let mut diagnostics = Vec::new();
    for (owner, definition) in schema.types() {
        let definitions = match definition {
            TypeDefinition::Object(object) => Some(&object.fields),
            TypeDefinition::Interface(interface) => Some(&interface.fields),
            _ => None,
        };
        let Some(definitions) = definitions else {
            continue;
        };
        for field in definitions.values() {
            let projected = if owner.as_str().starts_with('_') {
                project_target_private_field(schema, owner, field, directives)
            } else {
                project_field(schema, owner, field, names, directives)
            };
            match projected {
                Ok(projection) => {
                    fields.insert(field.coordinate.clone(), projection);
                }
                Err(error) => diagnostics.push(error),
            }
        }
    }
    if let Some(diagnostics) = DiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    Ok(fields)
}

fn project_target_private_field(
    schema: &CanonicalSchema,
    owner: &SchemaName,
    field: &FieldDefinition,
    directives: &DirectiveProjection,
) -> Result<FieldProjection, Diagnostic> {
    let arguments = field
        .arguments
        .values()
        .map(|argument| {
            let presence =
                ArgumentPresence::for_input(&argument.type_use, argument.default.as_ref());
            let expected = directives.expected_type(&argument.coordinate);
            Ok(ArgumentProjection {
                coordinate: argument.coordinate.clone(),
                wire_name: argument.name.clone(),
                rust_name: rust_name(&argument.name, NameContext::Argument).identifier,
                rust_type: project_input_type(
                    schema,
                    &argument.type_use,
                    presence.is_omittable(),
                    expected,
                )?,
                presence,
                encoder: InputEncoder::for_type(schema, &argument.type_use, expected)?,
                deprecation: directives
                    .deprecation_reason(&argument.coordinate)
                    .map(str::to_owned),
                experimental: directives
                    .experimental_reason(&argument.coordinate)
                    .map(str::to_owned),
            })
        })
        .collect::<Result<Vec<_>, Diagnostic>>()?;
    let has_options = arguments
        .iter()
        .any(|argument| argument.presence.is_omittable());
    let (return_type, decode) = project_output_type(schema, &field.type_use)?;
    Ok(FieldProjection {
        coordinate: field.coordinate.clone(),
        owner: owner.clone(),
        wire_name: field.name.clone(),
        rust_name: rust_name(&field.name, NameContext::Method).identifier,
        options_method_name: has_options.then(|| {
            rust_name_from_candidate(
                &field.name,
                &format!("{}_opts", field.name.as_str()),
                NameContext::OptionsMethod,
            )
            .identifier
        }),
        options_type_name: has_options.then(|| {
            rust_name_from_candidate(
                &field.name,
                &format!("{}_{}_opts", owner.as_str(), field.name.as_str()),
                NameContext::Options,
            )
            .identifier
        }),
        all_or_nothing_arguments: arguments
            .iter()
            .any(|argument| argument.encoder.contains_lazy_id()),
        arguments,
        return_type,
        decode,
        strategy: FieldStrategy::TargetPrivate,
        deprecation: directives
            .deprecation_reason(&field.coordinate)
            .map(str::to_owned),
        experimental: directives
            .experimental_reason(&field.coordinate)
            .map(str::to_owned),
    })
}

fn project_field(
    schema: &CanonicalSchema,
    owner: &SchemaName,
    field: &FieldDefinition,
    names: &RustNameMap,
    directives: &DirectiveProjection,
) -> Result<FieldProjection, Diagnostic> {
    let arguments = field
        .arguments
        .values()
        .map(|argument| {
            let presence =
                ArgumentPresence::for_input(&argument.type_use, argument.default.as_ref());
            let expected = directives.expected_type(&argument.coordinate);
            if let Some(target) = expected
                && !has_id_field(schema, target)
            {
                return Err(projection_error(
                    DiagnosticCode::ExpectedTypeInvalid,
                    &argument.coordinate,
                    "expectedType input target has no compatible ID surface",
                ));
            }
            let omit_outer = presence.is_omittable();
            Ok(ArgumentProjection {
                coordinate: argument.coordinate.clone(),
                wire_name: argument.name.clone(),
                rust_name: required_name(names, &argument.coordinate, NameContext::Argument)?,
                rust_type: project_input_type(schema, &argument.type_use, omit_outer, expected)?,
                presence,
                encoder: InputEncoder::for_type(schema, &argument.type_use, expected)?,
                deprecation: directives
                    .deprecation_reason(&argument.coordinate)
                    .map(str::to_owned),
                experimental: directives
                    .experimental_reason(&argument.coordinate)
                    .map(str::to_owned),
            })
        })
        .collect::<Result<Vec<_>, Diagnostic>>()?;
    let has_options = arguments
        .iter()
        .any(|argument| argument.presence.is_omittable());
    let (mut return_type, decode) = project_output_type(schema, &field.type_use)?;
    let leaf = named_leaf(&field.type_use);
    let expected_parent = directives.expected_type(&field.coordinate);
    if let Some(parent) = expected_parent {
        if leaf.as_str() != "ID" || parent != owner || !has_id_field(schema, parent) {
            return Err(projection_error(
                DiagnosticCode::ExpectedTypeInvalid,
                &field.coordinate,
                "self-return expectedType must name its ID-bearing owner",
            ));
        }
        return_type = match schema.types().get(parent) {
            Some(TypeDefinition::Object(_)) => RustType::Handle(parent.clone()),
            Some(TypeDefinition::Interface(_)) => RustType::InterfaceHandle(parent.clone()),
            _ => {
                return Err(projection_error(
                    DiagnosticCode::ExpectedTypeInvalid,
                    &field.coordinate,
                    "self-return expectedType target is not a handle type",
                ));
            }
        };
    }
    let leaf_class = match schema.types().get(leaf) {
        Some(TypeDefinition::Object(_)) => FieldLeafClass::Object {
            has_id: has_id_field(schema, leaf),
        },
        Some(TypeDefinition::Interface(_)) => FieldLeafClass::Interface {
            has_id: has_id_field(schema, leaf),
        },
        Some(_) => FieldLeafClass::Value,
        None => {
            return Err(projection_error(
                DiagnosticCode::SchemaFieldUnmapped,
                &field.coordinate,
                "field output leaf has no canonical definition",
            ));
        }
    };
    let strategy_kind = select_field_strategy(FieldStrategyInput {
        list: contains_list(&field.type_use),
        nullable: field.type_use.nullable,
        leaf: leaf_class,
        expected_type_self: expected_parent.is_some(),
        target_private: false,
    })
    .map_err(|code| {
        projection_error(
            code,
            &field.coordinate,
            "field shape lacks the ID surface required by its execution strategy",
        )
    })?;
    let strategy = match strategy_kind {
        FieldStrategyKind::ExpectedTypeSelf => FieldStrategy::ExpectedTypeSelf {
            parent: expected_parent.cloned().ok_or_else(|| {
                projection_error(
                    DiagnosticCode::ExpectedTypeInvalid,
                    &field.coordinate,
                    "expected-type strategy lost its validated parent",
                )
            })?,
            id_path: field.coordinate.clone(),
        },
        FieldStrategyKind::ReenterList => FieldStrategy::ReenterList {
            target: leaf.clone(),
            wrappers: WrapperPlan::from(&field.type_use),
            id_path: SchemaCoordinate::field(leaf, &id_name()),
        },
        FieldStrategyKind::NullableHandle => FieldStrategy::NullableHandle {
            target: leaf.clone(),
            wrappers: WrapperPlan::from(&field.type_use),
            id_probe: SchemaCoordinate::field(leaf, &id_name()),
        },
        FieldStrategyKind::LazyHandle => FieldStrategy::LazyHandle {
            target: leaf.clone(),
        },
        FieldStrategyKind::ExecuteValue => FieldStrategy::ExecuteValue {
            output: return_type.clone(),
        },
        FieldStrategyKind::TargetPrivate => {
            return Err(projection_error(
                DiagnosticCode::SchemaFieldUnmapped,
                &field.coordinate,
                "public field selected a target-private strategy",
            ));
        }
    };
    let all_or_nothing_arguments = arguments
        .iter()
        .any(|argument| argument.encoder.contains_lazy_id());
    Ok(FieldProjection {
        coordinate: field.coordinate.clone(),
        owner: owner.clone(),
        wire_name: field.name.clone(),
        rust_name: required_name(names, &field.coordinate, NameContext::Method)?,
        options_method_name: has_options
            .then(|| required_name(names, &field.coordinate, NameContext::OptionsMethod))
            .transpose()?,
        options_type_name: has_options
            .then(|| required_name(names, &field.coordinate, NameContext::Options))
            .transpose()?,
        arguments,
        return_type,
        decode,
        strategy,
        all_or_nothing_arguments,
        deprecation: directives
            .deprecation_reason(&field.coordinate)
            .map(str::to_owned),
        experimental: directives
            .experimental_reason(&field.coordinate)
            .map(str::to_owned),
    })
}

fn id_name() -> SchemaName {
    SchemaName::try_from("id").expect("`id` is a statically valid GraphQL name")
}

fn required_name(
    names: &RustNameMap,
    coordinate: &SchemaCoordinate,
    context: NameContext,
) -> Result<String, Diagnostic> {
    names
        .get(coordinate, context)
        .map(|name| name.identifier.clone())
        .ok_or_else(|| {
            projection_error(
                DiagnosticCode::RustNameInvalid,
                coordinate,
                "complete name map is missing a field identifier",
            )
        })
}

fn projection_error(
    code: DiagnosticCode,
    coordinate: &SchemaCoordinate,
    message: &str,
) -> Diagnostic {
    Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate.as_str())),
        message,
    )
}
