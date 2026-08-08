//! GraphQL constant parsing for schema defaults and directive arguments.

use std::collections::BTreeMap;

use graphql_parser::query::{Definition, OperationDefinition, Selection, Value};
use serde::{Deserialize, Serialize};

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate};

use super::canonical::{SchemaCoordinate, SchemaName};

/// A finite GraphQL floating-point literal represented by stable IEEE-754 bits.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct FiniteF64(u64);

impl FiniteF64 {
    /// Returns the represented finite value.
    #[must_use]
    pub fn get(self) -> f64 {
        f64::from_bits(self.0)
    }
}

/// A parsed, normalized GraphQL constant value.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum ConstValue {
    /// The GraphQL null literal.
    Null,
    /// A Boolean literal.
    Boolean(bool),
    /// A signed integer literal.
    Int(i64),
    /// A finite floating-point literal.
    Float(FiniteF64),
    /// A string literal.
    String(String),
    /// An enum Wire_Name literal.
    Enum(SchemaName),
    /// A recursively normalized list literal.
    List(Vec<ConstValue>),
    /// An object literal sorted by input-field Wire_Name.
    Object(BTreeMap<SchemaName, ConstValue>),
}

/// Parses one GraphQL constant without interpreting it as a Rust default.
pub(crate) fn parse_const(
    coordinate: &SchemaCoordinate,
    source: &str,
) -> Result<ConstValue, Diagnostic> {
    if !literal_nesting_within_bound(source) {
        return Err(default_error(
            coordinate,
            "default exceeds the maximum nesting depth of 64",
        ));
    }
    // graphql-parser exposes the language grammar through document parsers. Embedding
    // the literal in a fixed synthetic field delegates every lexical corner case to
    // that grammar without maintaining a partial default-value parser here.
    let document_source = format!("query __DaggerDefault {{ value(input: {source}) }}");
    let document = graphql_parser::parse_query::<String>(&document_source)
        .map_err(|_| default_error(coordinate, "default is not a GraphQL constant literal"))?;
    let Some(Definition::Operation(OperationDefinition::Query(operation))) =
        document.definitions.first()
    else {
        return Err(default_error(
            coordinate,
            "default could not be isolated from its parser envelope",
        ));
    };
    let Some(Selection::Field(field)) = operation.selection_set.items.first() else {
        return Err(default_error(
            coordinate,
            "default could not be isolated from its parser envelope",
        ));
    };
    let Some((_, value)) = field.arguments.first() else {
        return Err(default_error(
            coordinate,
            "default could not be isolated from its parser envelope",
        ));
    };
    convert_value(coordinate, value, 0)
}

fn convert_value(
    coordinate: &SchemaCoordinate,
    value: &Value<'_, String>,
    depth: usize,
) -> Result<ConstValue, Diagnostic> {
    if depth > 64 {
        return Err(default_error(
            coordinate,
            "default exceeds the maximum nesting depth of 64",
        ));
    }
    match value {
        Value::Variable(_) => Err(default_error(
            coordinate,
            "schema defaults cannot contain variables",
        )),
        Value::Int(number) => number
            .as_i64()
            .map(ConstValue::Int)
            .ok_or_else(|| default_error(coordinate, "integer default is outside i64 range")),
        Value::Float(number) if number.is_finite() => {
            Ok(ConstValue::Float(FiniteF64(number.to_bits())))
        }
        Value::Float(_) => Err(default_error(
            coordinate,
            "floating-point default must be finite",
        )),
        Value::String(value) => Ok(ConstValue::String(value.clone())),
        Value::Boolean(value) => Ok(ConstValue::Boolean(*value)),
        Value::Null => Ok(ConstValue::Null),
        Value::Enum(value) => SchemaName::try_from(value.as_str())
            .map(ConstValue::Enum)
            .map_err(|()| default_error(coordinate, "enum default has an invalid Wire_Name")),
        Value::List(values) => values
            .iter()
            .map(|value| convert_value(coordinate, value, depth + 1))
            .collect::<Result<Vec<_>, _>>()
            .map(ConstValue::List),
        Value::Object(values) => values
            .iter()
            .map(|(name, value)| {
                let name = SchemaName::try_from(name.as_str()).map_err(|()| {
                    default_error(coordinate, "input-object default has an invalid field name")
                })?;
                Ok((name, convert_value(coordinate, value, depth + 1)?))
            })
            .collect::<Result<BTreeMap<_, _>, _>>()
            .map(ConstValue::Object),
    }
}

fn literal_nesting_within_bound(source: &str) -> bool {
    let mut depth = 0_usize;
    let mut in_string = false;
    let mut escaped = false;
    for character in source.chars() {
        if in_string {
            if escaped {
                escaped = false;
            } else if character == '\\' {
                escaped = true;
            } else if character == '"' {
                in_string = false;
            }
            continue;
        }
        match character {
            '"' => in_string = true,
            '[' | '{' => {
                depth += 1;
                if depth > 64 {
                    return false;
                }
            }
            ']' | '}' => depth = depth.saturating_sub(1),
            _ => {}
        }
    }
    true
}

fn default_error(coordinate: &SchemaCoordinate, message: &str) -> Diagnostic {
    Diagnostic::new(
        DiagnosticCode::SchemaDefaultInvalid,
        Some(DiagnosticCoordinate::new(coordinate.as_str())),
        message,
    )
}
