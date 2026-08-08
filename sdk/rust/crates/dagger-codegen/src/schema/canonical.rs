//! Ordered, validated GraphQL schema values accepted by projection.
//!
//! The model retains wire names, source coordinates, documentation, defaults, and
//! directive metadata. Raw transport optionality and wrapper spellings cannot cross
//! this boundary.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

use serde::{Deserialize, Serialize};

use crate::target::CodegenTarget;

use super::defaults::ConstValue;

/// A GraphQL name that has passed lexical validation.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct SchemaName(String);

impl SchemaName {
    /// Borrows the exact GraphQL Wire_Name.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl TryFrom<&str> for SchemaName {
    type Error = ();

    fn try_from(value: &str) -> Result<Self, Self::Error> {
        let mut bytes = value.bytes();
        let Some(first) = bytes.next() else {
            return Err(());
        };
        if !(first == b'_' || first.is_ascii_alphabetic())
            || !bytes.all(|byte| byte == b'_' || byte.is_ascii_alphanumeric())
        {
            return Err(());
        }
        Ok(Self(value.to_owned()))
    }
}

impl fmt::Display for SchemaName {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

/// An exact public coordinate in the authoritative schema.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct SchemaCoordinate(String);

impl SchemaCoordinate {
    /// Creates an internal semantic coordinate that cannot be confused with a field.
    pub(crate) fn semantic(value: impl Into<String>) -> Self {
        Self(format!("semantic:{}", value.into()))
    }

    /// Creates the query-root coordinate.
    #[must_use]
    pub fn query_root() -> Self {
        Self("schema.query".to_owned())
    }

    /// Creates a named-type coordinate.
    #[must_use]
    pub fn named_type(type_name: &SchemaName) -> Self {
        Self(type_name.to_string())
    }

    /// Creates a field coordinate.
    #[must_use]
    pub fn field(type_name: &SchemaName, field_name: &SchemaName) -> Self {
        Self(format!("{type_name}.{field_name}"))
    }

    /// Creates a field-argument coordinate.
    #[must_use]
    pub fn argument(
        type_name: &SchemaName,
        field_name: &SchemaName,
        argument_name: &SchemaName,
    ) -> Self {
        Self(format!("{type_name}.{field_name}({argument_name}:)"))
    }

    /// Creates an input-object field coordinate.
    #[must_use]
    pub fn input_field(type_name: &SchemaName, field_name: &SchemaName) -> Self {
        Self(format!("{type_name}.{field_name}"))
    }

    /// Creates an enum-value coordinate.
    #[must_use]
    pub fn enum_value(type_name: &SchemaName, value_name: &SchemaName) -> Self {
        Self(format!("{type_name}.{value_name}"))
    }

    /// Creates a directive-definition coordinate.
    #[must_use]
    pub fn directive(name: &SchemaName) -> Self {
        Self(format!("@{name}"))
    }

    /// Creates a directive-argument coordinate.
    #[must_use]
    pub fn directive_argument(name: &SchemaName, argument_name: &SchemaName) -> Self {
        Self(format!("@{name}({argument_name}:)"))
    }

    /// Borrows the normalized coordinate text.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for SchemaCoordinate {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

/// A recursively wrapped GraphQL type with nullability at every level.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct TypeUse {
    /// Whether this exact wrapper level accepts or returns null.
    pub nullable: bool,
    /// The named leaf or recursively wrapped list at this level.
    pub shape: TypeShape,
}

/// The structural shape of a canonical GraphQL type use.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum TypeShape {
    /// A reference to a named schema definition.
    Named(SchemaName),
    /// A list whose element retains independent nullability and shape.
    List(Box<TypeUse>),
}

/// A validated directive application with arguments sorted by Wire_Name.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct DirectiveApplication {
    /// Exact directive Wire_Name.
    pub name: SchemaName,
    /// Encoded argument literals retained for later directive policy projection.
    pub arguments: BTreeMap<SchemaName, Option<String>>,
}

/// Canonical deprecation metadata.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct Deprecation {
    /// The schema-authored deprecation reason, when supplied.
    pub reason: Option<String>,
}

/// A field argument or directive-definition argument.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct ArgumentDefinition {
    /// Exact argument Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Canonical recursive input type.
    pub type_use: TypeUse,
    /// Parsed default retained for documentation and semantic fingerprints.
    pub default: Option<ConstValue>,
    /// Validated directive applications.
    pub directives: Vec<DirectiveApplication>,
    /// Validated legacy/directive deprecation metadata.
    pub deprecation: Option<Deprecation>,
}

/// A canonical object or interface field.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct FieldDefinition {
    /// Exact field Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Arguments sorted by exact Wire_Name.
    pub arguments: BTreeMap<SchemaName, ArgumentDefinition>,
    /// Canonical recursive result type.
    pub type_use: TypeUse,
    /// Validated directive applications.
    pub directives: Vec<DirectiveApplication>,
    /// Validated legacy/directive deprecation metadata.
    pub deprecation: Option<Deprecation>,
}

/// A canonical input-object field.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct InputFieldDefinition {
    /// Exact field Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Canonical recursive input type.
    pub type_use: TypeUse,
    /// Parsed default retained for documentation and semantic fingerprints.
    pub default: Option<ConstValue>,
    /// Validated directive applications.
    pub directives: Vec<DirectiveApplication>,
    /// Validated legacy/directive deprecation metadata.
    pub deprecation: Option<Deprecation>,
}

/// A canonical enum value.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct EnumValueDefinition {
    /// Exact enum-value Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Validated directive applications.
    pub directives: Vec<DirectiveApplication>,
    /// Validated legacy/directive deprecation metadata.
    pub deprecation: Option<Deprecation>,
}

/// A custom or built-in scalar definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ScalarDefinition {
    /// Exact scalar Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Validated type-level directive applications.
    pub directives: Vec<DirectiveApplication>,
}

/// A canonical object definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct ObjectDefinition {
    /// Exact object Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Fields sorted by exact Wire_Name.
    pub fields: BTreeMap<SchemaName, FieldDefinition>,
    /// Implemented interface names sorted by exact Wire_Name.
    pub interfaces: BTreeSet<SchemaName>,
    /// Validated type-level directive applications.
    pub directives: Vec<DirectiveApplication>,
}

/// A canonical interface definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct InterfaceDefinition {
    /// Exact interface Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Fields sorted by exact Wire_Name.
    pub fields: BTreeMap<SchemaName, FieldDefinition>,
    /// Interfaces implemented by this interface, sorted by exact Wire_Name.
    pub interfaces: BTreeSet<SchemaName>,
    /// Concrete possible types sorted by exact Wire_Name.
    pub possible_types: BTreeSet<SchemaName>,
    /// Validated type-level directive applications.
    pub directives: Vec<DirectiveApplication>,
}

/// A canonical enum definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct EnumDefinition {
    /// Exact enum Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Values sorted by exact Wire_Name.
    pub values: BTreeMap<SchemaName, EnumValueDefinition>,
    /// Validated type-level directive applications.
    pub directives: Vec<DirectiveApplication>,
}

/// A canonical input-object definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct InputObjectDefinition {
    /// Exact input-object Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Input fields sorted by exact Wire_Name.
    pub fields: BTreeMap<SchemaName, InputFieldDefinition>,
    /// Validated type-level directive applications.
    pub directives: Vec<DirectiveApplication>,
}

/// One supported public named-type definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub enum TypeDefinition {
    /// A scalar definition.
    Scalar(ScalarDefinition),
    /// An object definition.
    Object(ObjectDefinition),
    /// An interface definition.
    Interface(InterfaceDefinition),
    /// An enum definition.
    Enum(EnumDefinition),
    /// An input-object definition.
    InputObject(InputObjectDefinition),
}

impl TypeDefinition {
    /// Returns the exact Wire_Name shared by every supported definition.
    #[must_use]
    pub fn name(&self) -> &SchemaName {
        match self {
            Self::Scalar(definition) => &definition.name,
            Self::Object(definition) => &definition.name,
            Self::Interface(definition) => &definition.name,
            Self::Enum(definition) => &definition.name,
            Self::InputObject(definition) => &definition.name,
        }
    }
}

/// A canonical directive definition.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct DirectiveDefinition {
    /// Exact directive Wire_Name.
    pub name: SchemaName,
    /// Exact source coordinate.
    pub coordinate: SchemaCoordinate,
    /// Schema-authored documentation.
    pub description: Option<String>,
    /// Valid application locations sorted by introspection spelling.
    pub locations: BTreeSet<String>,
    /// Arguments sorted by exact Wire_Name.
    pub arguments: BTreeMap<SchemaName, ArgumentDefinition>,
}

/// Exact counts of public schema coordinates validated for a target.
#[derive(Clone, Copy, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
pub struct CoordinateInventory {
    /// Query-root coordinates.
    pub query_roots: usize,
    /// Public named-type coordinates.
    pub named_types: usize,
    /// Scalar definitions.
    pub scalars: usize,
    /// Object definitions.
    pub objects: usize,
    /// Interface definitions.
    pub interfaces: usize,
    /// Enum definitions.
    pub enums: usize,
    /// Input-object definitions.
    pub input_objects: usize,
    /// Object and interface fields.
    pub fields: usize,
    /// Field arguments.
    pub arguments: usize,
    /// Input-object fields.
    pub input_fields: usize,
    /// Enum values.
    pub enum_values: usize,
    /// Object-to-interface implementation edges.
    pub interface_edges: usize,
    /// Directive definitions.
    pub directives: usize,
    /// Directive-definition arguments.
    pub directive_arguments: usize,
}

/// A complete ordered schema accepted by semantic projection.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CanonicalSchema {
    target: CodegenTarget,
    query: SchemaName,
    types: BTreeMap<SchemaName, TypeDefinition>,
    directives: BTreeMap<SchemaName, DirectiveDefinition>,
    inventory: CoordinateInventory,
}

impl CanonicalSchema {
    /// Constructs a complete canonical schema after all validation phases succeed.
    pub(crate) fn new(
        target: CodegenTarget,
        query: SchemaName,
        types: BTreeMap<SchemaName, TypeDefinition>,
        directives: BTreeMap<SchemaName, DirectiveDefinition>,
        inventory: CoordinateInventory,
    ) -> Self {
        Self {
            target,
            query,
            types,
            directives,
            inventory,
        }
    }

    /// Returns the exact target identity validated with this schema.
    #[must_use]
    pub const fn target(&self) -> &CodegenTarget {
        &self.target
    }

    /// Returns the query-root Wire_Name.
    #[must_use]
    pub const fn query(&self) -> &SchemaName {
        &self.query
    }

    /// Returns public named definitions in deterministic Wire_Name order.
    #[must_use]
    pub const fn types(&self) -> &BTreeMap<SchemaName, TypeDefinition> {
        &self.types
    }

    /// Returns directive definitions in deterministic Wire_Name order.
    #[must_use]
    pub const fn directives(&self) -> &BTreeMap<SchemaName, DirectiveDefinition> {
        &self.directives
    }

    /// Returns the validated exact coordinate inventory.
    #[must_use]
    pub const fn inventory(&self) -> CoordinateInventory {
        self.inventory
    }
}
