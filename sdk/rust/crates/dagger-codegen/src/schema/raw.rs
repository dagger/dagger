//! Raw GraphQL introspection response types.
//!
//! These types preserve the nullable wire shape of introspection. They intentionally do
//! not validate names, references, or wrapper structure; canonicalization owns those
//! decisions and reports diagnostics before projection begins.

use serde::{Deserialize, Deserializer, Serialize};

/// A location at which a GraphQL directive may be applied.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DirectiveLocation {
    /// A query operation.
    Query,
    /// A mutation operation.
    Mutation,
    /// A subscription operation.
    Subscription,
    /// A selected field.
    Field,
    /// A fragment definition.
    FragmentDefinition,
    /// A fragment spread.
    FragmentSpread,
    /// An inline fragment.
    InlineFragment,
    /// A schema definition.
    Schema,
    /// A scalar definition.
    Scalar,
    /// An object definition.
    Object,
    /// A field definition.
    FieldDefinition,
    /// An argument definition.
    ArgumentDefinition,
    /// An interface definition.
    Interface,
    /// A union definition.
    Union,
    /// An enum definition.
    Enum,
    /// An enum value.
    EnumValue,
    /// An input-object definition.
    InputObject,
    /// An input-field definition.
    InputFieldDefinition,
    /// A location introduced by a newer GraphQL implementation.
    Other(String),
}

impl Serialize for DirectiveLocation {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(match self {
            Self::Query => "QUERY",
            Self::Mutation => "MUTATION",
            Self::Subscription => "SUBSCRIPTION",
            Self::Field => "FIELD",
            Self::FragmentDefinition => "FRAGMENT_DEFINITION",
            Self::FragmentSpread => "FRAGMENT_SPREAD",
            Self::InlineFragment => "INLINE_FRAGMENT",
            Self::Schema => "SCHEMA",
            Self::Scalar => "SCALAR",
            Self::Object => "OBJECT",
            Self::FieldDefinition => "FIELD_DEFINITION",
            Self::ArgumentDefinition => "ARGUMENT_DEFINITION",
            Self::Interface => "INTERFACE",
            Self::Union => "UNION",
            Self::Enum => "ENUM",
            Self::EnumValue => "ENUM_VALUE",
            Self::InputObject => "INPUT_OBJECT",
            Self::InputFieldDefinition => "INPUT_FIELD_DEFINITION",
            Self::Other(value) => value,
        })
    }
}

impl<'de> Deserialize<'de> for DirectiveLocation {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let value = <String>::deserialize(deserializer)?;
        Ok(match value.as_str() {
            "QUERY" => Self::Query,
            "MUTATION" => Self::Mutation,
            "SUBSCRIPTION" => Self::Subscription,
            "FIELD" => Self::Field,
            "FRAGMENT_DEFINITION" => Self::FragmentDefinition,
            "FRAGMENT_SPREAD" => Self::FragmentSpread,
            "INLINE_FRAGMENT" => Self::InlineFragment,
            "SCHEMA" => Self::Schema,
            "SCALAR" => Self::Scalar,
            "OBJECT" => Self::Object,
            "FIELD_DEFINITION" => Self::FieldDefinition,
            "ARGUMENT_DEFINITION" => Self::ArgumentDefinition,
            "INTERFACE" => Self::Interface,
            "UNION" => Self::Union,
            "ENUM" => Self::Enum,
            "ENUM_VALUE" => Self::EnumValue,
            "INPUT_OBJECT" => Self::InputObject,
            "INPUT_FIELD_DEFINITION" => Self::InputFieldDefinition,
            _ => Self::Other(value),
        })
    }
}

/// The GraphQL kind represented by a type reference or definition.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum TypeKind {
    /// A scalar definition.
    Scalar,
    /// An object definition.
    Object,
    /// An interface definition.
    Interface,
    /// A union definition.
    Union,
    /// An enum definition.
    Enum,
    /// An input-object definition.
    InputObject,
    /// A list wrapper.
    List,
    /// A non-null wrapper.
    NonNull,
    /// A kind introduced by a newer GraphQL implementation.
    Other(String),
}

impl Serialize for TypeKind {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(match self {
            Self::Scalar => "SCALAR",
            Self::Object => "OBJECT",
            Self::Interface => "INTERFACE",
            Self::Union => "UNION",
            Self::Enum => "ENUM",
            Self::InputObject => "INPUT_OBJECT",
            Self::List => "LIST",
            Self::NonNull => "NON_NULL",
            Self::Other(value) => value,
        })
    }
}

impl<'de> Deserialize<'de> for TypeKind {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let value = <String>::deserialize(deserializer)?;
        Ok(match value.as_str() {
            "SCALAR" => Self::Scalar,
            "OBJECT" => Self::Object,
            "INTERFACE" => Self::Interface,
            "UNION" => Self::Union,
            "ENUM" => Self::Enum,
            "INPUT_OBJECT" => Self::InputObject,
            "LIST" => Self::List,
            "NON_NULL" => Self::NonNull,
            _ => Self::Other(value),
        })
    }
}

/// A complete type definition returned by introspection.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullType {
    /// The definition kind.
    pub kind: Option<TypeKind>,
    /// The GraphQL name, absent on wrapper references.
    pub name: Option<String>,
    /// User-authored schema documentation.
    pub description: Option<String>,
    /// Object or interface fields.
    pub fields: Option<Vec<FullTypeField>>,
    /// Input-object fields.
    pub input_fields: Option<Vec<FullTypeInputField>>,
    /// Interfaces implemented by this type.
    pub interfaces: Option<Vec<FullTypeInterface>>,
    /// Values belonging to an enum.
    pub enum_values: Option<Vec<FullTypeEnumValue>>,
    /// Possible concrete types of an abstract type.
    pub possible_types: Option<Vec<FullTypePossibleType>>,
}

/// One argument accepted by a field.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeFieldArgument {
    /// The input-value wire fields.
    #[serde(flatten)]
    pub input_value: InputValue,
}

/// The type reference attached to a field.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeFieldType {
    /// The referenced type and wrappers.
    #[serde(flatten)]
    pub type_ref: TypeRef,
}

/// An object or interface field definition.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeField {
    /// The field name.
    pub name: Option<String>,
    /// User-authored field documentation.
    pub description: Option<String>,
    /// Arguments accepted by the field.
    pub args: Option<Vec<Option<FullTypeFieldArgument>>>,
    /// The field return type.
    #[serde(rename = "type")]
    pub type_: Option<FullTypeFieldType>,
    /// Whether use of the field is deprecated.
    pub is_deprecated: Option<bool>,
    /// The schema-provided deprecation reason.
    pub deprecation_reason: Option<String>,
    /// Directives applied to the field.
    #[serde(default)]
    pub directives: Option<Vec<DirectiveApplication>>,
    /// Owning type populated after decoding for legacy projection helpers.
    #[serde(skip)]
    pub parent_type: Option<FullType>,
}

/// An input-object field definition.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeInputField {
    /// The input-value wire fields.
    #[serde(flatten)]
    pub input_value: InputValue,
}

/// An implemented interface reference.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeInterface {
    /// The referenced interface type.
    #[serde(flatten)]
    pub type_ref: TypeRef,
}

/// An enum value definition.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeEnumValue {
    /// The enum value name.
    pub name: Option<String>,
    /// User-authored value documentation.
    pub description: Option<String>,
    /// Whether use of the value is deprecated.
    pub is_deprecated: Option<bool>,
    /// The schema-provided deprecation reason.
    pub deprecation_reason: Option<String>,
}

/// A possible concrete type reference.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypePossibleType {
    /// The referenced concrete type.
    #[serde(flatten)]
    pub type_ref: TypeRef,
}

/// A field or argument input value.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InputValue {
    /// The input name.
    pub name: String,
    /// User-authored input documentation.
    pub description: Option<String>,
    /// The accepted input type.
    #[serde(rename = "type")]
    pub type_: TypeRef,
    /// The GraphQL literal used when the input is omitted.
    pub default_value: Option<String>,
    /// Directives applied to the input.
    #[serde(default)]
    pub directives: Option<Vec<DirectiveApplication>>,
}

/// A recursively wrapped reference to a GraphQL type.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TypeRef {
    /// The definition or wrapper kind.
    pub kind: Option<TypeKind>,
    /// The referenced name for non-wrapper kinds.
    pub name: Option<String>,
    /// The wrapped type for list and non-null kinds.
    pub of_type: Option<Box<TypeRef>>,
}

/// The named query root.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaQueryType {
    /// The root type name.
    pub name: Option<String>,
}

/// The named mutation root.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaMutationType {
    /// The root type name.
    pub name: Option<String>,
}

/// The named subscription root.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaSubscriptionType {
    /// The root type name.
    pub name: Option<String>,
}

/// A type entry in the introspection response.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaType {
    /// The complete type definition.
    #[serde(flatten)]
    pub full_type: FullType,
}

/// One argument declared by a directive definition.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaDirectiveArgument {
    /// The input-value wire fields.
    #[serde(flatten)]
    pub input_value: InputValue,
}

/// A directive definition returned by introspection.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaDirective {
    /// The directive name.
    pub name: Option<String>,
    /// User-authored directive documentation.
    pub description: Option<String>,
    /// Locations at which the directive is valid.
    pub locations: Option<Vec<Option<DirectiveLocation>>>,
    /// Arguments accepted by the directive.
    pub args: Option<Vec<Option<SchemaDirectiveArgument>>>,
}

/// The raw schema carried by an introspection response.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Schema {
    /// The query root reference.
    pub query_type: Option<SchemaQueryType>,
    /// The mutation root reference.
    pub mutation_type: Option<SchemaMutationType>,
    /// The subscription root reference.
    pub subscription_type: Option<SchemaSubscriptionType>,
    /// All definitions known to the schema.
    pub types: Option<Vec<Option<SchemaType>>>,
    /// All directive definitions known to the schema.
    pub directives: Option<Vec<Option<SchemaDirective>>>,
}

/// A directive application attached to a schema element.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DirectiveApplication {
    /// The directive name.
    pub name: String,
    /// Arguments supplied to the directive.
    #[serde(default)]
    pub args: Vec<DirectiveApplicationArgument>,
}

/// One argument supplied to a directive application.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DirectiveApplicationArgument {
    /// The argument name.
    pub name: String,
    /// The encoded argument value.
    pub value: Option<String>,
}

/// Accessors for directives consumed by projection.
pub trait DirectivesExt {
    /// Returns the decoded `name` argument of `@expectedType` when present.
    fn expected_type(&self) -> Option<String>;
}

impl DirectivesExt for Option<Vec<DirectiveApplication>> {
    fn expected_type(&self) -> Option<String> {
        let directives = self.as_ref()?;
        let directive = directives.iter().find(|item| item.name == "expectedType")?;
        let argument = directive.args.iter().find(|item| item.name == "name")?;
        serde_json::from_str::<String>(argument.value.as_ref()?).ok()
    }
}

/// The `__schema` envelope inside an introspection response.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct SchemaContainer {
    /// The decoded schema, if the server supplied one.
    #[serde(rename = "__schema")]
    pub schema: Option<Schema>,
}

/// A GraphQL response carrying `data` around the schema envelope.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct FullResponse<T> {
    /// The response payload.
    pub data: T,
}

/// Either accepted introspection response envelope.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum IntrospectionResponse {
    /// A normal GraphQL response with a `data` envelope.
    FullResponse(FullResponse<SchemaContainer>),
    /// A schema envelope supplied directly by a fixture or tool.
    Schema(SchemaContainer),
}

impl IntrospectionResponse {
    /// Borrows the schema envelope regardless of response form.
    #[must_use]
    pub fn as_schema(&self) -> &SchemaContainer {
        match self {
            Self::FullResponse(response) => &response.data,
            Self::Schema(schema) => schema,
        }
    }

    /// Consumes the response and returns its schema envelope.
    #[must_use]
    pub fn into_schema(self) -> SchemaContainer {
        match self {
            Self::FullResponse(response) => response.data,
            Self::Schema(schema) => schema,
        }
    }
}
