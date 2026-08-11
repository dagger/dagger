//! Strict engine-independent call values shared by generated registries and fixtures.
//!
//! These values carry only semantic call data. Active clients, sessions, transports,
//! cancellation handles, credentials, and result sinks remain call-local runtime
//! ownership and cannot enter the durable envelope.

use std::fmt;

use serde::{Deserialize, Deserializer, Serialize, Serializer};

const CALL_FORMAT_VERSION: u32 = 1;

/// Validated Dagger parent, function, or argument wire name.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct ModuleWireName(String);

impl ModuleWireName {
    /// Validates one canonical Dagger identifier.
    pub fn new(value: impl Into<String>) -> Result<Self, &'static str> {
        let value = value.into();
        let mut characters = value.chars();
        let valid_start = characters
            .next()
            .is_some_and(|character| character == '_' || character.is_ascii_alphabetic());
        if valid_start
            && characters.all(|character| character == '_' || character.is_ascii_alphanumeric())
        {
            Ok(Self(value))
        } else {
            Err("module wire name is invalid")
        }
    }

    /// Borrows the canonical wire spelling.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for ModuleWireName {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl Serialize for ModuleWireName {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for ModuleWireName {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        Self::new(String::deserialize(deserializer)?).map_err(serde::de::Error::custom)
    }
}

/// One number-preserving JSON value crossing the module call boundary.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(transparent)]
pub struct ModuleJson(serde_json::Value);

impl ModuleJson {
    /// Wraps an already-decoded JSON value.
    #[must_use]
    pub const fn new(value: serde_json::Value) -> Self {
        Self(value)
    }

    /// Borrows the exact JSON value.
    #[must_use]
    pub const fn as_json(&self) -> &serde_json::Value {
        &self.0
    }

    /// Consumes this wrapper.
    #[must_use]
    pub fn into_json(self) -> serde_json::Value {
        self.0
    }
}

/// One named input retained in engine order until duplicate validation completes.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct NamedModuleArgument {
    /// Exact argument wire name.
    pub name: ModuleWireName,
    /// Exact supplied JSON value, including explicit null.
    pub value: ModuleJson,
}

/// Immutable engine-independent input to one generated dispatch attempt.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct CallEnvelope {
    format_version: u32,
    /// Optional parent wire name; constructors have no parent value.
    pub parent_name: Option<ModuleWireName>,
    /// Selected function wire name, or empty only for registration.
    pub function_name: Option<ModuleWireName>,
    /// Exact parent JSON when an instance function is selected.
    pub parent: Option<ModuleJson>,
    /// Named arguments before completeness and duplicate validation.
    pub arguments: Vec<NamedModuleArgument>,
}

impl CallEnvelope {
    /// Constructs a current-format call envelope.
    #[must_use]
    pub fn new(
        parent_name: Option<ModuleWireName>,
        function_name: Option<ModuleWireName>,
        parent: Option<ModuleJson>,
        arguments: Vec<NamedModuleArgument>,
    ) -> Self {
        Self {
            format_version: CALL_FORMAT_VERSION,
            parent_name,
            function_name,
            parent,
            arguments,
        }
    }

    /// Returns the strict wire format version.
    #[must_use]
    pub const fn format_version(&self) -> u32 {
        self.format_version
    }
}

impl<'de> Deserialize<'de> for CallEnvelope {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Raw {
            format_version: u32,
            parent_name: Option<ModuleWireName>,
            function_name: Option<ModuleWireName>,
            parent: Option<ModuleJson>,
            arguments: Vec<NamedModuleArgument>,
        }

        let raw = Raw::deserialize(deserializer)?;
        if raw.format_version != CALL_FORMAT_VERSION {
            return Err(serde::de::Error::custom(
                "unsupported module call format version",
            ));
        }
        Ok(Self::new(
            raw.parent_name,
            raw.function_name,
            raw.parent,
            raw.arguments,
        ))
    }
}
