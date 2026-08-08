//! Recursive Rust type projection and named-type construction plans.
//!
//! GraphQL nullability remains attached to every wrapper level. Argument omission is
//! handled by callers and is never conflated with a nested nullable value here.

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::directive::DirectiveProjection;
use crate::naming::{NameContext, RustNameMap};
use crate::schema::canonical::{
    CanonicalSchema, SchemaCoordinate, SchemaName, TypeDefinition, TypeShape, TypeUse,
};
use crate::schema::defaults::ConstValue;

use super::fields::{ArgumentPresence, InputEncoder};

/// A recursive Rust type selected before source rendering.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum RustType {
    /// Rust `bool`.
    Bool,
    /// Rust `f64`.
    F64,
    /// Rust `i64`.
    I64,
    /// Owned Rust `String`.
    String,
    /// Handwritten opaque `Id`.
    Id,
    /// Handwritten lossless JSON scalar.
    Json,
    /// Handwritten platform scalar.
    Platform,
    /// Idiomatic unit for GraphQL `Void`.
    Unit,
    /// A generated closed enum.
    Enum(SchemaName),
    /// A generated owned input object.
    Input(SchemaName),
    /// A generated object handle.
    Handle(SchemaName),
    /// A generated interface client handle.
    InterfaceHandle(SchemaName),
    /// A target-typed raw or lazily resolved ID input.
    IdInput(SchemaName),
    /// Explicit value absence at this wrapper level.
    Option(Box<RustType>),
    /// Ordered GraphQL list values.
    Vec(Box<RustType>),
}

impl RustType {
    /// Returns a stable source-like signature used by semantic fingerprints.
    #[must_use]
    pub fn signature(&self) -> String {
        match self {
            Self::Bool => "bool".to_owned(),
            Self::F64 => "f64".to_owned(),
            Self::I64 => "i64".to_owned(),
            Self::String => "String".to_owned(),
            Self::Id => "Id".to_owned(),
            Self::Json => "Json".to_owned(),
            Self::Platform => "Platform".to_owned(),
            Self::Unit => "()".to_owned(),
            Self::Enum(name)
            | Self::Input(name)
            | Self::Handle(name)
            | Self::InterfaceHandle(name) => name.to_string(),
            Self::IdInput(name) => format!("IdInput<{}>", name.as_str()),
            Self::Option(inner) => format!("Option<{}>", inner.signature()),
            Self::Vec(inner) => format!("Vec<{}>", inner.signature()),
        }
    }
}

/// A source-independent copy of recursive GraphQL wrapper semantics.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct WrapperPlan {
    /// Whether this exact level admits a GraphQL null.
    pub nullable: bool,
    /// Named leaf or recursively wrapped list.
    pub shape: WrapperShape,
}

/// Structural wrapper plan below one nullability level.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum WrapperShape {
    /// Exact named leaf Wire_Name.
    Named(SchemaName),
    /// Recursively projected list element.
    List(Box<WrapperPlan>),
}

impl From<&TypeUse> for WrapperPlan {
    fn from(type_use: &TypeUse) -> Self {
        let shape = match &type_use.shape {
            TypeShape::Named(name) => WrapperShape::Named(name.clone()),
            TypeShape::List(element) => WrapperShape::List(Box::new(Self::from(element.as_ref()))),
        };
        Self {
            nullable: type_use.nullable,
            shape,
        }
    }
}

/// The eight scalar policies supported by the exact target.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum ScalarKind {
    /// GraphQL `Boolean`.
    Boolean,
    /// GraphQL `Float`.
    Float,
    /// GraphQL `Int` with the target's signed 64-bit representation.
    Int,
    /// GraphQL `String`.
    String,
    /// GraphQL `ID`.
    Id,
    /// GraphQL `JSON`, represented as a JSON-encoded string.
    Json,
    /// GraphQL `Platform`.
    Platform,
    /// GraphQL `Void`, represented by JSON null.
    Void,
}

impl ScalarKind {
    /// Selects a scalar policy from an exact Wire_Name.
    #[must_use]
    pub fn from_name(name: &SchemaName) -> Option<Self> {
        match name.as_str() {
            "Boolean" => Some(Self::Boolean),
            "Float" => Some(Self::Float),
            "Int" => Some(Self::Int),
            "String" => Some(Self::String),
            "ID" => Some(Self::Id),
            "JSON" => Some(Self::Json),
            "Platform" => Some(Self::Platform),
            "Void" => Some(Self::Void),
            _ => None,
        }
    }

    /// Returns whether a JSON response value obeys this scalar's wire contract.
    #[must_use]
    pub fn accepts_wire(self, value: &serde_json::Value) -> bool {
        match self {
            Self::Boolean => value.is_boolean(),
            Self::Float => value.as_f64().is_some_and(f64::is_finite),
            Self::Int => value.as_i64().is_some(),
            Self::String | Self::Id | Self::Platform => value.is_string(),
            Self::Json => value
                .as_str()
                .is_some_and(|encoded| serde_json::from_str::<serde_json::Value>(encoded).is_ok()),
            Self::Void => value.is_null(),
        }
    }

    fn rust_type(self) -> RustType {
        match self {
            Self::Boolean => RustType::Bool,
            Self::Float => RustType::F64,
            Self::Int => RustType::I64,
            Self::String => RustType::String,
            Self::Id => RustType::Id,
            Self::Json => RustType::Json,
            Self::Platform => RustType::Platform,
            Self::Void => RustType::Unit,
        }
    }
}

/// Typed leaf failure expected from runtime decoding.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum DecodeLeaf {
    /// A built-in or handwritten scalar decoder.
    Scalar(ScalarKind),
    /// A closed enum decoder that rejects unknown Wire_Names.
    Enum(SchemaName),
    /// A generated input-value decoder.
    Input(SchemaName),
    /// An object identifier probe or inline object value.
    Object(SchemaName),
    /// An interface identifier probe.
    Interface(SchemaName),
}

/// Typed runtime failures required by a projected decoding plan.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum DecodeFailure {
    /// JSON null appeared at a non-null wrapper level.
    NonNullViolation,
    /// A scalar value violated its exact wire representation.
    InvalidScalarWire(ScalarKind),
    /// A closed enum received an unknown Wire_Name.
    UnknownEnumValue(SchemaName),
    /// A list member failed its recursively projected decoder.
    InvalidListElement,
    /// An object/interface response lacked the identifier required by its strategy.
    InvalidObjectIdentifier(SchemaName),
}

/// Complete decoding shape for a projected output.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct DecodePlan {
    /// Recursive nullability/list contract checked by the decoder.
    pub wrappers: WrapperPlan,
    /// Named-leaf decoder and typed failure domain.
    pub leaf: DecodeLeaf,
    /// Closed set of typed failures the runtime decoder must preserve.
    pub failures: BTreeSet<DecodeFailure>,
}

/// A scalar supplied by the handwritten runtime.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ScalarProjection {
    /// Exact scalar coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact scalar Wire_Name.
    pub wire_name: SchemaName,
    /// Rust scalar policy.
    pub scalar: ScalarKind,
}

/// One object-to-interface implementation edge.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct InterfaceImplementationProjection {
    /// Implementing object or interface.
    pub implementor: SchemaName,
    /// Implemented interface.
    pub interface: SchemaName,
    /// Stable semantic edge coordinate.
    pub coordinate: SchemaCoordinate,
}

/// One generated object handle plan.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ObjectProjection {
    /// Exact type coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact type Wire_Name.
    pub wire_name: SchemaName,
    /// Public Rust handle identifier.
    pub rust_name: String,
    /// Generated module identifier.
    pub module_name: String,
    /// Whether the object exposes a non-null `ID` field.
    pub has_id: bool,
    /// Declared interface implementations.
    pub interfaces: BTreeSet<SchemaName>,
    /// Exact field coordinates owned by the handle.
    pub fields: BTreeSet<SchemaCoordinate>,
}

/// One generated interface trait and concrete client handle plan.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct InterfaceProjection {
    /// Exact type coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact type Wire_Name.
    pub wire_name: SchemaName,
    /// Public trait identifier.
    pub trait_name: String,
    /// Public concrete client-handle identifier.
    pub client_name: String,
    /// Generated module identifier.
    pub module_name: String,
    /// Whether the interface exposes a non-null `ID` field.
    pub has_id: bool,
    /// Exact concrete possible types.
    pub possible_types: BTreeSet<SchemaName>,
    /// Exact field coordinates owned by the interface.
    pub fields: BTreeSet<SchemaCoordinate>,
}

/// One canonical Rust enum variant.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct EnumVariantProjection {
    /// Exact canonical enum-value coordinate.
    pub coordinate: SchemaCoordinate,
    /// Canonical Wire_Name used for encoding.
    pub wire_name: SchemaName,
    /// Public Rust variant identifier.
    pub rust_name: String,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Caller-visible deprecation reason.
    pub deprecation: Option<String>,
    /// Caller-visible experimental note.
    pub experimental: Option<String>,
}

/// One alternate enum Wire_Name accepted during decoding.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct EnumAliasProjection {
    /// Exact alias coordinate.
    pub coordinate: SchemaCoordinate,
    /// Alias Wire_Name accepted during decoding.
    pub wire_name: SchemaName,
    /// Canonical sibling Wire_Name used for encoding.
    pub canonical_wire_name: SchemaName,
    /// Rust variant shared with the canonical value.
    pub rust_name: String,
}

/// One closed generated enum plan.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct EnumProjection {
    /// Exact type coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact enum Wire_Name.
    pub wire_name: SchemaName,
    /// Public Rust enum identifier.
    pub rust_name: String,
    /// Canonical variants keyed by canonical Wire_Name.
    pub variants: BTreeMap<SchemaName, EnumVariantProjection>,
    /// Decode aliases keyed by alias Wire_Name.
    pub aliases: BTreeMap<SchemaName, EnumAliasProjection>,
}

impl EnumProjection {
    /// Resolves a canonical or aliased Wire_Name to its Rust variant.
    #[must_use]
    pub fn decode_variant(&self, wire_name: &str) -> Option<&EnumVariantProjection> {
        if let Some(variant) = self
            .variants
            .iter()
            .find_map(|(name, variant)| (name.as_str() == wire_name).then_some(variant))
        {
            return Some(variant);
        }
        let canonical = self.aliases.iter().find_map(|(name, alias)| {
            (name.as_str() == wire_name).then_some(&alias.canonical_wire_name)
        })?;
        self.variants.get(canonical)
    }
}

/// One owned input-object field construction plan.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct InputFieldProjection {
    /// Exact field coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact field Wire_Name used by serialization.
    pub wire_name: SchemaName,
    /// Public Rust field identifier.
    pub rust_name: String,
    /// Consuming setter identifier for an omittable field.
    pub setter_name: Option<String>,
    /// Recursive Rust value type, excluding an omittable outer carrier.
    pub rust_type: RustType,
    /// Compile-time-required or explicitly omittable construction path.
    pub presence: ArgumentPresence,
    /// Recursive exact-wire serializer.
    pub encoder: InputEncoder,
    /// Parsed engine default retained only for docs and fingerprints.
    pub engine_default: Option<ConstValue>,
}

/// One generated owned input-object plan.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct InputObjectProjection {
    /// Exact type coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact input-object Wire_Name.
    pub wire_name: SchemaName,
    /// Public Rust type identifier.
    pub rust_name: String,
    /// Generated module identifier.
    pub module_name: String,
    /// Public constructor identifier.
    pub constructor_name: String,
    /// Fields in exact Wire_Name order.
    pub fields: BTreeMap<SchemaName, InputFieldProjection>,
}

/// A target-private schema type retained for completeness without a Rust symbol.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct TargetPrivateTypeProjection {
    /// Exact type coordinate.
    pub coordinate: SchemaCoordinate,
    /// Exact target-private Wire_Name.
    pub wire_name: SchemaName,
    /// Exact field coordinates contained by the no-symbol policy.
    pub fields: BTreeSet<SchemaCoordinate>,
}

/// Total named-type projection for one public schema definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum TypeProjection {
    /// Handwritten scalar contract.
    Scalar(ScalarProjection),
    /// Generated object handle.
    Object(ObjectProjection),
    /// Generated interface trait and client.
    Interface(InterfaceProjection),
    /// Generated closed enum.
    Enum(EnumProjection),
    /// Generated owned input object.
    InputObject(InputObjectProjection),
    /// Single-underscore engine metadata excluded by the definitive Go generator.
    TargetPrivate(TargetPrivateTypeProjection),
}

/// Projects an output type with every nullable wrapper represented explicitly.
pub fn project_output_type(
    schema: &CanonicalSchema,
    type_use: &TypeUse,
) -> Result<(RustType, DecodePlan), Diagnostic> {
    project_type(schema, type_use, false, None)
}

/// Projects an input type and optionally lets an outer options field carry omission.
pub fn project_input_type(
    schema: &CanonicalSchema,
    type_use: &TypeUse,
    omit_outer_option: bool,
    expected_type: Option<&SchemaName>,
) -> Result<RustType, Diagnostic> {
    project_type(schema, type_use, omit_outer_option, expected_type).map(|(rust_type, _)| rust_type)
}

fn project_type(
    schema: &CanonicalSchema,
    type_use: &TypeUse,
    omit_outer_option: bool,
    expected_type: Option<&SchemaName>,
) -> Result<(RustType, DecodePlan), Diagnostic> {
    let leaf = named_leaf(type_use);
    let definition = schema.types().get(leaf).ok_or_else(|| {
        projection_error(
            DiagnosticCode::SchemaTypeUnsupported,
            &SchemaCoordinate::named_type(leaf),
            "projected type leaf has no canonical definition",
        )
    })?;
    let decode_leaf = match definition {
        TypeDefinition::Scalar(_) => {
            DecodeLeaf::Scalar(ScalarKind::from_name(leaf).ok_or_else(|| {
                projection_error(
                    DiagnosticCode::SchemaTypeUnsupported,
                    &SchemaCoordinate::named_type(leaf),
                    "scalar has no registered Rust wire policy",
                )
            })?)
        }
        TypeDefinition::Enum(_) => DecodeLeaf::Enum(leaf.clone()),
        TypeDefinition::InputObject(_) => DecodeLeaf::Input(leaf.clone()),
        TypeDefinition::Object(_) => DecodeLeaf::Object(leaf.clone()),
        TypeDefinition::Interface(_) => DecodeLeaf::Interface(leaf.clone()),
    };
    let rust_type = project_wrappers(schema, type_use, omit_outer_option, expected_type)?;
    let mut failures = BTreeSet::new();
    if contains_non_null(type_use) {
        failures.insert(DecodeFailure::NonNullViolation);
    }
    if contains_list(type_use) {
        failures.insert(DecodeFailure::InvalidListElement);
    }
    match &decode_leaf {
        DecodeLeaf::Scalar(scalar) => {
            failures.insert(DecodeFailure::InvalidScalarWire(*scalar));
        }
        DecodeLeaf::Enum(name) => {
            failures.insert(DecodeFailure::UnknownEnumValue(name.clone()));
        }
        DecodeLeaf::Object(name) | DecodeLeaf::Interface(name) => {
            failures.insert(DecodeFailure::InvalidObjectIdentifier(name.clone()));
        }
        DecodeLeaf::Input(_) => {}
    }
    Ok((
        rust_type,
        DecodePlan {
            wrappers: WrapperPlan::from(type_use),
            leaf: decode_leaf,
            failures,
        },
    ))
}

fn project_wrappers(
    schema: &CanonicalSchema,
    type_use: &TypeUse,
    omit_this_option: bool,
    expected_type: Option<&SchemaName>,
) -> Result<RustType, Diagnostic> {
    let mut projected = match &type_use.shape {
        TypeShape::List(element) => RustType::Vec(Box::new(project_wrappers(
            schema,
            element,
            false,
            expected_type,
        )?)),
        TypeShape::Named(name) => project_named(schema, name, expected_type)?,
    };
    // Void's represented null is the successful value rather than optional absence.
    if type_use.nullable && !omit_this_option && !matches!(projected, RustType::Unit) {
        projected = RustType::Option(Box::new(projected));
    }
    Ok(projected)
}

fn project_named(
    schema: &CanonicalSchema,
    name: &SchemaName,
    expected_type: Option<&SchemaName>,
) -> Result<RustType, Diagnostic> {
    if let Some(target) = expected_type {
        if name.as_str() != "ID" {
            return Err(projection_error(
                DiagnosticCode::ExpectedTypeInvalid,
                &SchemaCoordinate::named_type(name),
                "expectedType may only replace an ID leaf",
            ));
        }
        return Ok(RustType::IdInput(target.clone()));
    }
    match schema.types().get(name) {
        Some(TypeDefinition::Scalar(_)) => ScalarKind::from_name(name)
            .map(ScalarKind::rust_type)
            .ok_or_else(|| {
                projection_error(
                    DiagnosticCode::SchemaTypeUnsupported,
                    &SchemaCoordinate::named_type(name),
                    "scalar has no registered Rust wire policy",
                )
            }),
        Some(TypeDefinition::Enum(_)) => Ok(RustType::Enum(name.clone())),
        Some(TypeDefinition::InputObject(_)) => Ok(RustType::Input(name.clone())),
        Some(TypeDefinition::Object(_)) => Ok(RustType::Handle(name.clone())),
        Some(TypeDefinition::Interface(_)) => Ok(RustType::InterfaceHandle(name.clone())),
        None => Err(projection_error(
            DiagnosticCode::SchemaReferenceInvalid,
            &SchemaCoordinate::named_type(name),
            "named type disappeared after canonical validation",
        )),
    }
}

/// Returns the exact named leaf below an arbitrary recursive wrapper graph.
#[must_use]
pub fn named_leaf(type_use: &TypeUse) -> &SchemaName {
    match &type_use.shape {
        TypeShape::Named(name) => name,
        TypeShape::List(element) => named_leaf(element),
    }
}

/// Returns whether a recursive type contains any list wrapper.
#[must_use]
pub fn contains_list(type_use: &TypeUse) -> bool {
    match &type_use.shape {
        TypeShape::Named(_) => false,
        TypeShape::List(_) => true,
    }
}

fn contains_non_null(type_use: &TypeUse) -> bool {
    !type_use.nullable
        || match &type_use.shape {
            TypeShape::Named(_) => false,
            TypeShape::List(element) => contains_non_null(element),
        }
}

pub(crate) fn project_named_types(
    schema: &CanonicalSchema,
    names: &RustNameMap,
    directives: &DirectiveProjection,
) -> Result<
    (
        BTreeMap<SchemaName, TypeProjection>,
        Vec<InterfaceImplementationProjection>,
    ),
    DiagnosticSet,
> {
    let mut projections = BTreeMap::new();
    let mut implementations = Vec::new();
    let mut diagnostics = Vec::new();
    for (name, definition) in schema.types() {
        if name.as_str().starts_with('_') {
            let (coordinate, fields) = match definition {
                TypeDefinition::Object(object) => (
                    object.coordinate.clone(),
                    object
                        .fields
                        .values()
                        .map(|field| field.coordinate.clone())
                        .collect(),
                ),
                TypeDefinition::Interface(interface) => (
                    interface.coordinate.clone(),
                    interface
                        .fields
                        .values()
                        .map(|field| field.coordinate.clone())
                        .collect(),
                ),
                _ => {
                    diagnostics.push(projection_error(
                        DiagnosticCode::SchemaTypeUnsupported,
                        &SchemaCoordinate::named_type(name),
                        "target-private non-object type has no reviewed containment policy",
                    ));
                    continue;
                }
            };
            projections.insert(
                name.clone(),
                TypeProjection::TargetPrivate(TargetPrivateTypeProjection {
                    coordinate,
                    wire_name: name.clone(),
                    fields,
                }),
            );
            continue;
        }
        let projection: Result<TypeProjection, Diagnostic> = (|| match definition {
            TypeDefinition::Scalar(scalar) => ScalarKind::from_name(name)
                .map(|kind| {
                    TypeProjection::Scalar(ScalarProjection {
                        coordinate: scalar.coordinate.clone(),
                        wire_name: name.clone(),
                        scalar: kind,
                    })
                })
                .ok_or_else(|| {
                    projection_error(
                        DiagnosticCode::SchemaTypeUnsupported,
                        &scalar.coordinate,
                        "scalar has no registered Rust projection",
                    )
                }),
            TypeDefinition::Object(object) => {
                for interface in &object.interfaces {
                    implementations.push(InterfaceImplementationProjection {
                        implementor: name.clone(),
                        interface: interface.clone(),
                        coordinate: implementation_coordinate(name, interface),
                    });
                }
                Ok(TypeProjection::Object(ObjectProjection {
                    coordinate: object.coordinate.clone(),
                    wire_name: name.clone(),
                    rust_name: required_name(names, &object.coordinate, NameContext::Handle)?,
                    module_name: required_name(names, &object.coordinate, NameContext::Module)?,
                    has_id: has_id_field(schema, name),
                    interfaces: object.interfaces.clone(),
                    fields: object
                        .fields
                        .values()
                        .map(|field| field.coordinate.clone())
                        .collect(),
                }))
            }
            TypeDefinition::Interface(interface) => {
                for parent in &interface.interfaces {
                    implementations.push(InterfaceImplementationProjection {
                        implementor: name.clone(),
                        interface: parent.clone(),
                        coordinate: implementation_coordinate(name, parent),
                    });
                }
                Ok(TypeProjection::Interface(InterfaceProjection {
                    coordinate: interface.coordinate.clone(),
                    wire_name: name.clone(),
                    trait_name: required_name(names, &interface.coordinate, NameContext::Trait)?,
                    client_name: required_name(names, &interface.coordinate, NameContext::Handle)?,
                    module_name: required_name(names, &interface.coordinate, NameContext::Module)?,
                    has_id: has_id_field(schema, name),
                    possible_types: interface.possible_types.clone(),
                    fields: interface
                        .fields
                        .values()
                        .map(|field| field.coordinate.clone())
                        .collect(),
                }))
            }
            TypeDefinition::Enum(enumeration) => {
                project_enum(enumeration, names, directives).map(TypeProjection::Enum)
            }
            TypeDefinition::InputObject(input) => {
                let fields = input
                    .fields
                    .values()
                    .map(|field| {
                        let presence =
                            ArgumentPresence::for_input(&field.type_use, field.default.as_ref());
                        let omit_outer = matches!(presence, ArgumentPresence::Omittable { .. });
                        let projected = InputFieldProjection {
                            coordinate: field.coordinate.clone(),
                            wire_name: field.name.clone(),
                            rust_name: required_name(names, &field.coordinate, NameContext::Field)?,
                            setter_name: omit_outer
                                .then(|| {
                                    required_name(names, &field.coordinate, NameContext::Setter)
                                })
                                .transpose()?,
                            rust_type: project_input_type(
                                schema,
                                &field.type_use,
                                omit_outer,
                                None,
                            )?,
                            presence,
                            encoder: InputEncoder::for_type(schema, &field.type_use, None)?,
                            engine_default: field.default.clone(),
                        };
                        Ok((field.name.clone(), projected))
                    })
                    .collect::<Result<BTreeMap<_, _>, Diagnostic>>()?;
                Ok(TypeProjection::InputObject(InputObjectProjection {
                    coordinate: input.coordinate.clone(),
                    wire_name: name.clone(),
                    rust_name: required_name(names, &input.coordinate, NameContext::Type)?,
                    module_name: required_name(names, &input.coordinate, NameContext::Module)?,
                    constructor_name: required_name(
                        names,
                        &input.coordinate,
                        NameContext::Constructor,
                    )?,
                    fields,
                }))
            }
        })();
        match projection {
            Ok(projection) => {
                projections.insert(name.clone(), projection);
            }
            Err(error) => diagnostics.push(error),
        }
    }
    implementations.sort();
    if let Some(diagnostics) = DiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    Ok((projections, implementations))
}

fn project_enum(
    enumeration: &crate::schema::canonical::EnumDefinition,
    names: &RustNameMap,
    directives: &DirectiveProjection,
) -> Result<EnumProjection, Diagnostic> {
    let mut variants = BTreeMap::new();
    let mut aliases = BTreeMap::new();
    for value in enumeration.values.values() {
        if let Some(canonical) = directives.enum_alias(&value.coordinate) {
            let canonical_definition = enumeration.values.get(canonical).ok_or_else(|| {
                projection_error(
                    DiagnosticCode::SchemaDirectiveArgumentInvalid,
                    &value.coordinate,
                    "enum alias target disappeared after directive validation",
                )
            })?;
            aliases.insert(
                value.name.clone(),
                EnumAliasProjection {
                    coordinate: value.coordinate.clone(),
                    wire_name: value.name.clone(),
                    canonical_wire_name: canonical.clone(),
                    rust_name: required_name(
                        names,
                        &canonical_definition.coordinate,
                        NameContext::Variant,
                    )?,
                },
            );
        } else {
            variants.insert(
                value.name.clone(),
                EnumVariantProjection {
                    coordinate: value.coordinate.clone(),
                    wire_name: value.name.clone(),
                    rust_name: required_name(names, &value.coordinate, NameContext::Variant)?,
                    description: value.description.clone(),
                    deprecation: directives
                        .deprecation_reason(&value.coordinate)
                        .map(str::to_owned),
                    experimental: directives
                        .experimental_reason(&value.coordinate)
                        .map(str::to_owned),
                },
            );
        }
    }
    Ok(EnumProjection {
        coordinate: enumeration.coordinate.clone(),
        wire_name: enumeration.name.clone(),
        rust_name: required_name(names, &enumeration.coordinate, NameContext::Type)?,
        variants,
        aliases,
    })
}

/// Returns whether a public object or interface exposes a non-null `ID` field.
#[must_use]
pub fn has_id_field(schema: &CanonicalSchema, name: &SchemaName) -> bool {
    let fields = match schema.types().get(name) {
        Some(TypeDefinition::Object(object)) => &object.fields,
        Some(TypeDefinition::Interface(interface)) => &interface.fields,
        _ => return false,
    };
    fields.values().any(|field| {
        field.name.as_str() == "id"
            && !field.type_use.nullable
            && matches!(&field.type_use.shape, TypeShape::Named(name) if name.as_str() == "ID")
    })
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
                "complete name map is missing a required generated identifier",
            )
        })
}

fn implementation_coordinate(implementor: &SchemaName, interface: &SchemaName) -> SchemaCoordinate {
    // GraphQL introspection has no standalone edge coordinate, so the semantic form is
    // domain-separated from fields and remains stable for catalog joins.
    SchemaCoordinate::semantic(format!("{implementor} implements {interface}"))
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
