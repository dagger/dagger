#![allow(non_camel_case_types)]

use serde::{Deserialize, Deserializer, Serialize};

#[derive(Clone, Debug)]
pub enum __DirectiveLocation {
    QUERY,
    MUTATION,
    SUBSCRIPTION,
    FIELD,
    FRAGMENT_DEFINITION,
    FRAGMENT_SPREAD,
    INLINE_FRAGMENT,
    SCHEMA,
    SCALAR,
    OBJECT,
    FIELD_DEFINITION,
    ARGUMENT_DEFINITION,
    INTERFACE,
    UNION,
    ENUM,
    ENUM_VALUE,
    INPUT_OBJECT,
    INPUT_FIELD_DEFINITION,
    Other(String),
}

impl Serialize for __DirectiveLocation {
    fn serialize<S: serde::Serializer>(&self, ser: S) -> Result<S::Ok, S::Error> {
        ser.serialize_str(match *self {
            __DirectiveLocation::QUERY => "QUERY",
            __DirectiveLocation::MUTATION => "MUTATION",
            __DirectiveLocation::SUBSCRIPTION => "SUBSCRIPTION",
            __DirectiveLocation::FIELD => "FIELD",
            __DirectiveLocation::FRAGMENT_DEFINITION => "FRAGMENT_DEFINITION",
            __DirectiveLocation::FRAGMENT_SPREAD => "FRAGMENT_SPREAD",
            __DirectiveLocation::INLINE_FRAGMENT => "INLINE_FRAGMENT",
            __DirectiveLocation::SCHEMA => "SCHEMA",
            __DirectiveLocation::SCALAR => "SCALAR",
            __DirectiveLocation::OBJECT => "OBJECT",
            __DirectiveLocation::FIELD_DEFINITION => "FIELD_DEFINITION",
            __DirectiveLocation::ARGUMENT_DEFINITION => "ARGUMENT_DEFINITION",
            __DirectiveLocation::INTERFACE => "INTERFACE",
            __DirectiveLocation::UNION => "UNION",
            __DirectiveLocation::ENUM => "ENUM",
            __DirectiveLocation::ENUM_VALUE => "ENUM_VALUE",
            __DirectiveLocation::INPUT_OBJECT => "INPUT_OBJECT",
            __DirectiveLocation::INPUT_FIELD_DEFINITION => "INPUT_FIELD_DEFINITION",
            __DirectiveLocation::Other(ref s) => s.as_str(),
        })
    }
}

impl<'de> Deserialize<'de> for __DirectiveLocation {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let s = <&'de str>::deserialize(deserializer)?;
        match s {
            "QUERY" => Ok(__DirectiveLocation::QUERY),
            "MUTATION" => Ok(__DirectiveLocation::MUTATION),
            "SUBSCRIPTION" => Ok(__DirectiveLocation::SUBSCRIPTION),
            "FIELD" => Ok(__DirectiveLocation::FIELD),
            "FRAGMENT_DEFINITION" => Ok(__DirectiveLocation::FRAGMENT_DEFINITION),
            "FRAGMENT_SPREAD" => Ok(__DirectiveLocation::FRAGMENT_SPREAD),
            "INLINE_FRAGMENT" => Ok(__DirectiveLocation::INLINE_FRAGMENT),
            "SCHEMA" => Ok(__DirectiveLocation::SCHEMA),
            "SCALAR" => Ok(__DirectiveLocation::SCALAR),
            "OBJECT" => Ok(__DirectiveLocation::OBJECT),
            "FIELD_DEFINITION" => Ok(__DirectiveLocation::FIELD_DEFINITION),
            "ARGUMENT_DEFINITION" => Ok(__DirectiveLocation::ARGUMENT_DEFINITION),
            "INTERFACE" => Ok(__DirectiveLocation::INTERFACE),
            "UNION" => Ok(__DirectiveLocation::UNION),
            "ENUM" => Ok(__DirectiveLocation::ENUM),
            "ENUM_VALUE" => Ok(__DirectiveLocation::ENUM_VALUE),
            "INPUT_OBJECT" => Ok(__DirectiveLocation::INPUT_OBJECT),
            "INPUT_FIELD_DEFINITION" => Ok(__DirectiveLocation::INPUT_FIELD_DEFINITION),
            _ => Ok(__DirectiveLocation::Other(s.to_string())),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub enum __TypeKind {
    SCALAR,
    OBJECT,
    INTERFACE,
    UNION,
    ENUM,
    INPUT_OBJECT,
    LIST,
    NON_NULL,
    Other(String),
}

impl Serialize for __TypeKind {
    fn serialize<S: serde::Serializer>(&self, ser: S) -> Result<S::Ok, S::Error> {
        ser.serialize_str(match *self {
            __TypeKind::SCALAR => "SCALAR",
            __TypeKind::OBJECT => "OBJECT",
            __TypeKind::INTERFACE => "INTERFACE",
            __TypeKind::UNION => "UNION",
            __TypeKind::ENUM => "ENUM",
            __TypeKind::INPUT_OBJECT => "INPUT_OBJECT",
            __TypeKind::LIST => "LIST",
            __TypeKind::NON_NULL => "NON_NULL",
            __TypeKind::Other(ref s) => s.as_str(),
        })
    }
}

impl<'de> Deserialize<'de> for __TypeKind {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let s = <&'de str>::deserialize(deserializer)?;
        match s {
            "SCALAR" => Ok(__TypeKind::SCALAR),
            "OBJECT" => Ok(__TypeKind::OBJECT),
            "INTERFACE" => Ok(__TypeKind::INTERFACE),
            "UNION" => Ok(__TypeKind::UNION),
            "ENUM" => Ok(__TypeKind::ENUM),
            "INPUT_OBJECT" => Ok(__TypeKind::INPUT_OBJECT),
            "LIST" => Ok(__TypeKind::LIST),
            "NON_NULL" => Ok(__TypeKind::NON_NULL),
            _ => Ok(__TypeKind::Other(s.to_string())),
        }
    }
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullType {
    pub kind: Option<__TypeKind>,
    pub name: Option<String>,
    pub description: Option<String>,
    pub fields: Option<Vec<FullTypeFields>>,
    pub input_fields: Option<Vec<FullTypeInputFields>>,
    pub interfaces: Option<Vec<FullTypeInterfaces>>,
    pub enum_values: Option<Vec<FullTypeEnumValues>>,
    pub possible_types: Option<Vec<FullTypePossibleTypes>>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeFieldsArgs {
    #[serde(flatten)]
    pub input_value: InputValue,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeFieldsType {
    #[serde(flatten)]
    pub type_ref: TypeRef,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeFields {
    pub name: Option<String>,
    pub description: Option<String>,
    pub args: Option<Vec<Option<FullTypeFieldsArgs>>>,
    #[serde(rename = "type")]
    pub type_: Option<FullTypeFieldsType>,
    pub is_deprecated: Option<bool>,
    pub deprecation_reason: Option<String>,

    #[serde(skip)]
    pub parent_type: Option<FullType>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeInputFields {
    #[serde(flatten)]
    pub input_value: InputValue,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeInterfaces {
    #[serde(flatten)]
    pub type_ref: TypeRef,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypeEnumValues {
    pub name: Option<String>,
    pub description: Option<String>,
    pub is_deprecated: Option<bool>,
    pub deprecation_reason: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FullTypePossibleTypes {
    #[serde(flatten)]
    pub type_ref: TypeRef,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InputValue {
    pub name: String,
    pub description: Option<String>,
    #[serde(rename = "type")]
    pub type_: InputValueType,
    pub default_value: Option<String>,
}

type InputValueType = TypeRef;

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TypeRef {
    pub kind: Option<__TypeKind>,
    pub name: Option<String>,
    pub of_type: Option<Box<TypeRef>>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaQueryType {
    pub name: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaMutationType {
    pub name: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaSubscriptionType {
    pub name: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaTypes {
    #[serde(flatten)]
    pub full_type: FullType,
}

#[allow(dead_code)]
#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaDirectivesArgs {
    #[serde(flatten)]
    input_value: InputValue,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SchemaDirectives {
    pub name: Option<String>,
    pub description: Option<String>,
    pub locations: Option<Vec<Option<__DirectiveLocation>>>,
    pub args: Option<Vec<Option<SchemaDirectivesArgs>>>,
}

#[allow(dead_code)]
#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Schema {
    pub query_type: Option<SchemaQueryType>,
    pub mutation_type: Option<SchemaMutationType>,
    pub subscription_type: Option<SchemaSubscriptionType>,
    pub types: Option<Vec<Option<SchemaTypes>>>,
    directives: Option<Vec<Option<SchemaDirectives>>>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct SchemaContainer {
    #[serde(rename = "__schema")]
    pub schema: Option<Schema>,
}

#[derive(Deserialize, Debug)]
pub struct FullResponse<T> {
    data: T,
}

#[derive(Debug, Deserialize)]
#[serde(untagged)]
pub enum IntrospectionResponse {
    FullResponse(FullResponse<SchemaContainer>),
    Schema(SchemaContainer),
}

impl IntrospectionResponse {
    pub fn as_schema(&self) -> &SchemaContainer {
        match self {
            IntrospectionResponse::FullResponse(full_response) => &full_response.data,
            IntrospectionResponse::Schema(schema) => schema,
        }
    }

    pub fn into_schema(self) -> SchemaContainer {
        match self {
            IntrospectionResponse::FullResponse(full_response) => full_response.data,
            IntrospectionResponse::Schema(schema) => schema,
        }
    }
}
