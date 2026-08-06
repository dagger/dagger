//! Lossless public values and private wire codecs for raw GraphQL execution.
//!
//! A response may contain partial data and ordered GraphQL errors simultaneously.
//! [`ResponseData`] therefore distinguishes an absent `data` member from explicit
//! JSON null, and decoding never turns a non-empty `errors` list into an early error
//! which would discard the rest of the response.

use std::error::Error;
use std::fmt;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

use crate::errors::{
    RequestEncodingError, RequestEncodingErrorKind, RequestError, ResponseDecodingError,
    ResponseDecodingErrorKind,
};

/// A caller-authored GraphQL document and its optional execution inputs.
#[derive(Clone, PartialEq)]
pub struct RawRequest {
    query: String,
    variables: Option<Value>,
    operation_name: Option<String>,
}

impl RawRequest {
    /// Creates a request for `query` without variables or an operation name.
    pub fn new(query: impl Into<String>) -> Self {
        Self {
            query: query.into(),
            variables: None,
            operation_name: None,
        }
    }

    /// Adds an explicitly present JSON variables value.
    #[must_use]
    pub fn with_variables(mut self, variables: Value) -> Self {
        self.variables = Some(variables);
        self
    }

    /// Adds the operation selected from a multi-operation document.
    #[must_use]
    pub fn with_operation_name(mut self, operation_name: impl Into<String>) -> Self {
        self.operation_name = Some(operation_name.into());
        self
    }

    /// Returns the GraphQL document without semantic rewriting.
    pub fn query(&self) -> &str {
        &self.query
    }

    /// Returns the explicitly present variables value.
    pub fn variables(&self) -> Option<&Value> {
        self.variables.as_ref()
    }

    /// Returns the selected GraphQL operation name.
    pub fn operation_name(&self) -> Option<&str> {
        self.operation_name.as_deref()
    }

    #[cfg_attr(not(test), allow(dead_code))]
    pub(crate) fn encode_wire(&self) -> Result<Vec<u8>, RequestError> {
        serde_json::to_vec(&WireRequestRef {
            query: &self.query,
            variables: self.variables.as_ref(),
            operation_name: self.operation_name.as_deref(),
        })
        .map_err(|error| {
            RequestError::RequestEncoding(RequestEncodingError::with_source(
                RequestEncodingErrorKind::Json,
                error,
            ))
        })
    }

    #[cfg(test)]
    pub(crate) fn decode_wire(bytes: &[u8]) -> Result<Self, RequestError> {
        let wire: WireRequest = serde_json::from_slice(bytes).map_err(|error| {
            RequestError::RequestEncoding(RequestEncodingError::with_source(
                RequestEncodingErrorKind::Json,
                error,
            ))
        })?;
        Ok(Self {
            query: wire.query,
            variables: wire.variables,
            operation_name: wire.operation_name,
        })
    }
}

impl fmt::Debug for RawRequest {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("RawRequest")
            .field("query_len", &self.query.len())
            .field("variables_present", &self.variables.is_some())
            .field("operation_name_present", &self.operation_name.is_some())
            .finish()
    }
}

#[derive(Serialize)]
struct WireRequestRef<'a> {
    query: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    variables: Option<&'a Value>,
    #[serde(rename = "operationName", skip_serializing_if = "Option::is_none")]
    operation_name: Option<&'a str>,
}

#[cfg(test)]
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WireRequest {
    query: String,
    // `Option<Value>` ordinarily maps both an absent member and JSON null to None.
    // Wrapping every present value preserves the caller-visible distinction.
    #[serde(default, deserialize_with = "deserialize_present_value")]
    variables: Option<Value>,
    #[serde(rename = "operationName", default)]
    operation_name: Option<String>,
}

#[cfg(test)]
fn deserialize_present_value<'de, D>(deserializer: D) -> Result<Option<Value>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Value::deserialize(deserializer).map(Some)
}

/// Presence-aware GraphQL response data.
#[derive(Clone, Debug, PartialEq)]
pub enum ResponseData {
    /// The response object did not contain a `data` member.
    Absent,
    /// The response explicitly contained `"data": null`.
    Null,
    /// The response contained a non-null JSON value, including partial data.
    ///
    /// A caller-created `Value(Value::Null)` has the same wire meaning as [`Self::Null`]
    /// and is canonicalized to that variant when decoded.
    Value(Value),
}

impl ResponseData {
    pub(crate) const fn kind_name(&self) -> &'static str {
        match self {
            Self::Absent => "absent",
            Self::Null => "null",
            Self::Value(_) => "value",
        }
    }
}

/// One source location reported for a GraphQL error.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct GraphQlLocation {
    line: u32,
    column: u32,
}

impl GraphQlLocation {
    /// Creates a GraphQL source coordinate.
    pub const fn new(line: u32, column: u32) -> Self {
        Self { line, column }
    }

    /// Returns the reported source line.
    pub const fn line(&self) -> u32 {
        self.line
    }

    /// Returns the reported source column.
    pub const fn column(&self) -> u32 {
        self.column
    }
}

/// A typed segment in the path from response root to a GraphQL error.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum GraphQlPathSegment {
    /// An object field name.
    Field(String),
    /// A zero-based list index.
    Index(u64),
}

/// One GraphQL protocol error, preserving ordered locations, path, and extensions.
#[derive(Clone, Debug, PartialEq)]
pub struct GraphQlError {
    message: String,
    locations: Vec<GraphQlLocation>,
    path: Vec<GraphQlPathSegment>,
    extensions: Option<Map<String, Value>>,
}

impl GraphQlError {
    /// Creates an error with no locations, path, or extensions.
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            locations: Vec::new(),
            path: Vec::new(),
            extensions: None,
        }
    }

    /// Replaces the ordered source locations.
    #[must_use]
    pub fn with_locations(mut self, locations: Vec<GraphQlLocation>) -> Self {
        self.locations = locations;
        self
    }

    /// Replaces the ordered field/index path.
    #[must_use]
    pub fn with_path(mut self, path: Vec<GraphQlPathSegment>) -> Self {
        self.path = path;
        self
    }

    /// Adds an explicitly present extensions object.
    #[must_use]
    pub fn with_extensions(mut self, extensions: Map<String, Value>) -> Self {
        self.extensions = Some(extensions);
        self
    }

    /// Returns the engine-authored error message.
    pub fn message(&self) -> &str {
        &self.message
    }

    /// Returns locations in engine order.
    pub fn locations(&self) -> &[GraphQlLocation] {
        &self.locations
    }

    /// Returns field and list-index segments in engine order.
    pub fn path(&self) -> &[GraphQlPathSegment] {
        &self.path
    }

    /// Returns the optional protocol extensions object.
    pub fn extensions(&self) -> Option<&Map<String, Value>> {
        self.extensions.as_ref()
    }
}

/// Complete raw GraphQL response, including partial data and ordered errors.
#[derive(Clone, PartialEq)]
pub struct RawResponse {
    data: ResponseData,
    errors: Vec<GraphQlError>,
    extensions: Option<Map<String, Value>>,
}

impl RawResponse {
    /// Creates a response with the selected data presence and no errors/extensions.
    pub fn new(data: ResponseData) -> Self {
        Self {
            data,
            errors: Vec::new(),
            extensions: None,
        }
    }

    /// Replaces the ordered GraphQL error list.
    #[must_use]
    pub fn with_errors(mut self, errors: Vec<GraphQlError>) -> Self {
        self.errors = errors;
        self
    }

    /// Adds an explicitly present response extensions object.
    #[must_use]
    pub fn with_extensions(mut self, extensions: Map<String, Value>) -> Self {
        self.extensions = Some(extensions);
        self
    }

    /// Returns presence-aware response data.
    pub const fn data(&self) -> &ResponseData {
        &self.data
    }

    /// Returns GraphQL errors in engine order.
    pub fn errors(&self) -> &[GraphQlError] {
        &self.errors
    }

    /// Returns the optional response extensions object.
    pub fn extensions(&self) -> Option<&Map<String, Value>> {
        self.extensions.as_ref()
    }

    #[cfg_attr(not(test), allow(dead_code))]
    pub(crate) fn encode_wire(&self) -> Result<Vec<u8>, ResponseDecodingError> {
        let mut object = Map::new();
        match &self.data {
            ResponseData::Absent => {}
            ResponseData::Null => {
                object.insert("data".into(), Value::Null);
            }
            ResponseData::Value(value) => {
                object.insert("data".into(), value.clone());
            }
        }
        if !self.errors.is_empty() {
            let errors = self
                .errors
                .iter()
                .map(graphql_error_to_value)
                .collect::<Vec<_>>();
            object.insert("errors".into(), Value::Array(errors));
        }
        if let Some(extensions) = &self.extensions {
            object.insert("extensions".into(), Value::Object(extensions.clone()));
        }
        serde_json::to_vec(&Value::Object(object)).map_err(|error| {
            ResponseDecodingError::with_source(ResponseDecodingErrorKind::Json, error)
        })
    }

    #[cfg_attr(not(test), allow(dead_code))]
    pub(crate) fn decode_wire(bytes: &[u8]) -> Result<Self, RequestError> {
        let value: Value = serde_json::from_slice(bytes).map_err(|error| {
            RequestError::ResponseDecoding(ResponseDecodingError::with_source(
                ResponseDecodingErrorKind::Json,
                error,
            ))
        })?;
        let Value::Object(mut object) = value else {
            return Err(invalid_response_shape("response root is not an object"));
        };

        let data = match object.remove("data") {
            None => ResponseData::Absent,
            Some(Value::Null) => ResponseData::Null,
            Some(value) => ResponseData::Value(value),
        };

        let errors = match object.remove("errors") {
            None => Vec::new(),
            Some(Value::Array(errors)) => errors
                .into_iter()
                .map(graphql_error_from_value)
                .collect::<Result<Vec<_>, _>>()?,
            Some(_) => return Err(invalid_response_shape("errors is not an array")),
        };

        let extensions = match object.remove("extensions") {
            None | Some(Value::Null) => None,
            Some(Value::Object(extensions)) => Some(extensions),
            Some(_) => return Err(invalid_response_shape("extensions is not an object")),
        };

        Ok(Self {
            data,
            errors,
            extensions,
        })
    }
}

impl fmt::Debug for RawResponse {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("RawResponse")
            .field("data_kind", &self.data.kind_name())
            .field("error_count", &self.errors.len())
            .field("extensions_present", &self.extensions.is_some())
            .finish()
    }
}

#[derive(Deserialize)]
struct WireGraphQlError {
    message: String,
    #[serde(default)]
    locations: Option<Vec<WireLocation>>,
    #[serde(default)]
    path: Option<Vec<Value>>,
    #[serde(default)]
    extensions: Option<Map<String, Value>>,
}

#[derive(Deserialize)]
struct WireLocation {
    line: u32,
    column: u32,
}

fn graphql_error_from_value(value: Value) -> Result<GraphQlError, RequestError> {
    let wire: WireGraphQlError = serde_json::from_value(value).map_err(|error| {
        RequestError::ResponseDecoding(ResponseDecodingError::with_source(
            ResponseDecodingErrorKind::InvalidShape,
            error,
        ))
    })?;
    let path = wire
        .path
        .unwrap_or_default()
        .into_iter()
        .map(|segment| match segment {
            Value::String(field) => Ok(GraphQlPathSegment::Field(field)),
            Value::Number(index) => index
                .as_u64()
                .map(GraphQlPathSegment::Index)
                .ok_or_else(|| invalid_response_shape("error path index is not an integer")),
            _ => Err(invalid_response_shape(
                "error path segment has an invalid type",
            )),
        })
        .collect::<Result<Vec<_>, _>>()?;
    Ok(GraphQlError {
        message: wire.message,
        locations: wire
            .locations
            .unwrap_or_default()
            .into_iter()
            .map(|location| GraphQlLocation::new(location.line, location.column))
            .collect(),
        path,
        extensions: wire.extensions,
    })
}

fn graphql_error_to_value(error: &GraphQlError) -> Value {
    let mut object = Map::new();
    object.insert("message".into(), Value::String(error.message.clone()));
    if !error.locations.is_empty() {
        object.insert(
            "locations".into(),
            Value::Array(
                error
                    .locations
                    .iter()
                    .map(|location| {
                        let mut value = Map::new();
                        value.insert("line".into(), Value::from(location.line));
                        value.insert("column".into(), Value::from(location.column));
                        Value::Object(value)
                    })
                    .collect(),
            ),
        );
    }
    if !error.path.is_empty() {
        object.insert(
            "path".into(),
            Value::Array(
                error
                    .path
                    .iter()
                    .map(|segment| match segment {
                        GraphQlPathSegment::Field(field) => Value::String(field.clone()),
                        GraphQlPathSegment::Index(index) => Value::from(*index),
                    })
                    .collect(),
            ),
        );
    }
    if let Some(extensions) = &error.extensions {
        object.insert("extensions".into(), Value::Object(extensions.clone()));
    }
    Value::Object(object)
}

fn invalid_response_shape(detail: &'static str) -> RequestError {
    RequestError::ResponseDecoding(ResponseDecodingError::with_source(
        ResponseDecodingErrorKind::InvalidShape,
        WireShapeError(detail),
    ))
}

#[derive(Debug)]
struct WireShapeError(&'static str);

impl fmt::Display for WireShapeError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.0)
    }
}

impl Error for WireShapeError {}
