//! Credential-safe structured application errors returned by authored module functions.

use std::collections::BTreeMap;
use std::error::Error;
use std::fmt;

use serde::Serialize;
use serde_json::Value;

use crate::QueryError;

const MAX_DETAIL_NAME_BYTES: usize = 128;
const MAX_DETAIL_COUNT: usize = 64;
const MAX_DETAILS_BYTES: usize = 8 * 1024;

/// One canonical JSON value attached to a module application error.
///
/// Values are intentionally owned. Generated adapters validate their target mapping
/// before publication; this type only provides a bounded, deterministic public
/// construction surface.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
#[serde(transparent)]
pub struct ModuleErrorDetail(Value);

impl ModuleErrorDetail {
    /// Constructs a detail from an owned JSON value.
    #[must_use]
    pub const fn new(value: Value) -> Self {
        Self(value)
    }

    /// Returns the underlying JSON value for generated error projection.
    #[must_use]
    pub const fn as_json(&self) -> &Value {
        &self.0
    }

    /// Consumes the detail and returns its JSON value.
    #[must_use]
    pub fn into_json(self) -> Value {
        self.0
    }
}

impl From<Value> for ModuleErrorDetail {
    fn from(value: Value) -> Self {
        Self::new(value)
    }
}

impl From<String> for ModuleErrorDetail {
    fn from(value: String) -> Self {
        Self::new(Value::String(value))
    }
}

impl From<&str> for ModuleErrorDetail {
    fn from(value: &str) -> Self {
        value.to_owned().into()
    }
}

impl From<bool> for ModuleErrorDetail {
    fn from(value: bool) -> Self {
        Self::new(Value::Bool(value))
    }
}

impl From<i64> for ModuleErrorDetail {
    fn from(value: i64) -> Self {
        Self::new(Value::Number(value.into()))
    }
}

impl From<u64> for ModuleErrorDetail {
    fn from(value: u64) -> Self {
        Self::new(Value::Number(value.into()))
    }
}

/// Failure to construct a bounded structured [`ModuleError`].
#[non_exhaustive]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ModuleErrorBuildError {
    /// A detail name was empty or exceeded the documented bound.
    InvalidDetailName,
    /// The number of distinct details exceeded the documented bound.
    TooManyDetails,
    /// Canonical JSON detail content exceeded the documented byte bound.
    DetailsTooLarge,
}

impl fmt::Display for ModuleErrorBuildError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::InvalidDetailName => "the module error detail name is invalid",
            Self::TooManyDetails => "the module error has too many details",
            Self::DetailsTooLarge => "the module error details are too large",
        })
    }
}

impl Error for ModuleErrorBuildError {}

/// An intentional application failure returned by authored module code.
///
/// `Display` renders only the caller-selected message. Details remain available
/// through typed inspection and are projected in sorted key order by generated code.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleError {
    message: String,
    details: BTreeMap<String, ModuleErrorDetail>,
}

impl ModuleError {
    /// Constructs an application error without structured details.
    #[must_use]
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            details: BTreeMap::new(),
        }
    }

    /// Returns the intentional application-facing message.
    #[must_use]
    pub fn message(&self) -> &str {
        &self.message
    }

    /// Iterates over details in canonical key order.
    pub fn details(&self) -> impl ExactSizeIterator<Item = (&str, &ModuleErrorDetail)> {
        self.details
            .iter()
            .map(|(name, detail)| (name.as_str(), detail))
    }

    /// Adds or replaces one bounded structured detail.
    ///
    /// Validation occurs before `self` is changed, so a failure leaves no partially
    /// constructed error value observable to the caller.
    pub fn with_detail(
        mut self,
        name: impl Into<String>,
        value: ModuleErrorDetail,
    ) -> Result<Self, ModuleErrorBuildError> {
        let name = name.into();
        if name.is_empty() || name.len() > MAX_DETAIL_NAME_BYTES {
            return Err(ModuleErrorBuildError::InvalidDetailName);
        }
        if !self.details.contains_key(&name) && self.details.len() == MAX_DETAIL_COUNT {
            return Err(ModuleErrorBuildError::TooManyDetails);
        }

        let previous = self.details.insert(name.clone(), value);
        let encoded = serde_json::to_vec(&self.details)
            .map_err(|_| ModuleErrorBuildError::DetailsTooLarge)?;
        if encoded.len() > MAX_DETAILS_BYTES {
            if let Some(previous) = previous {
                self.details.insert(name, previous);
            } else {
                self.details.remove(&name);
            }
            return Err(ModuleErrorBuildError::DetailsTooLarge);
        }
        Ok(self)
    }
}

impl fmt::Display for ModuleError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl Error for ModuleError {}

impl From<String> for ModuleError {
    fn from(message: String) -> Self {
        Self::new(message)
    }
}

impl From<&str> for ModuleError {
    fn from(message: &str) -> Self {
        Self::new(message)
    }
}

impl From<QueryError> for ModuleError {
    fn from(error: QueryError) -> Self {
        if let QueryError::GraphQl { response } = &error
            && let [graphql_error] = response.errors()
        {
            let message = graphql_error.message().to_owned();
            let mut module_error = Self::new(message.clone());
            if let Some(extensions) = graphql_error.extensions() {
                for (name, value) in extensions {
                    let Ok(updated) = module_error
                        .clone()
                        .with_detail(name.clone(), ModuleErrorDetail::new(value.clone()))
                    else {
                        // An application error must never evade the public bounds just
                        // because an engine extension set is unexpectedly large.
                        return Self::new(message);
                    };
                    module_error = updated;
                }
            }
            return module_error;
        }

        // Other query failures deliberately retain their credential-safe ordinary
        // display instead of importing transport bodies or opaque causes.
        Self::new(error.to_string())
    }
}

#[cfg(test)]
mod tests {
    use serde_json::{Map, json};

    use crate::QueryError;
    use crate::graphql::{GraphQlError, RawResponse, ResponseData};

    use super::{ModuleError, ModuleErrorBuildError, ModuleErrorDetail};

    #[test]
    fn details_are_sorted_and_display_is_only_the_message() {
        let error = ModuleError::new("not found")
            .with_detail("zeta", ModuleErrorDetail::from(2_i64))
            .expect("first detail is bounded")
            .with_detail("alpha", ModuleErrorDetail::from(true))
            .expect("second detail is bounded");

        assert_eq!(error.to_string(), "not found");
        assert_eq!(
            error.details().map(|(name, _)| name).collect::<Vec<_>>(),
            ["alpha", "zeta"]
        );
    }

    #[test]
    fn invalid_detail_name_is_rejected() {
        assert_eq!(
            ModuleError::new("invalid").with_detail("", ModuleErrorDetail::from(false)),
            Err(ModuleErrorBuildError::InvalidDetailName)
        );
    }

    #[test]
    fn one_graphql_error_retains_its_message_and_sorted_extensions() {
        let response = RawResponse::new(ResponseData::Null).with_errors(vec![
            GraphQlError::new("application failed").with_extensions(Map::from_iter([
                ("zeta".to_owned(), json!(2)),
                ("alpha".to_owned(), json!(true)),
            ])),
        ]);
        let error = ModuleError::from(QueryError::GraphQl { response });

        assert_eq!(error.message(), "application failed");
        assert_eq!(
            error.details().map(|(name, _)| name).collect::<Vec<_>>(),
            ["alpha", "zeta"]
        );
    }
}
