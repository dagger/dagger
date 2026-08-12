//! Strict engine-independent call values shared by generated registries and fixtures.
//!
//! These values carry only semantic call data. Active clients, sessions, transports,
//! cancellation handles, credentials, and result sinks remain call-local runtime
//! ownership and cannot enter the durable envelope.

use std::fmt;

use serde::{Deserialize, Deserializer, Serialize, Serializer};

const CALL_FORMAT_VERSION: u32 = 1;
const MAX_CALL_ID_BYTES: usize = 128;

/// Validated non-empty Dagger parent or argument wire name.
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

/// Validated Dagger function wire name, including the constructor sentinel.
///
/// The engine represents a constructor invocation with an empty function name. Keeping
/// that value in a distinct type prevents registration's empty parent name from being
/// confused with constructor selection after the adapter boundary.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct ModuleFunctionName(String);

impl ModuleFunctionName {
    /// Validates an ordinary function name or the empty constructor sentinel.
    pub fn new(value: impl Into<String>) -> Result<Self, &'static str> {
        let value = value.into();
        if value.is_empty() || ModuleWireName::new(value.clone()).is_ok() {
            Ok(Self(value))
        } else {
            Err("module function name is invalid")
        }
    }

    /// Borrows the exact function spelling; empty means constructor invocation.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// Returns whether the engine selected the constructor.
    #[must_use]
    pub fn is_constructor(&self) -> bool {
        self.0.is_empty()
    }
}

impl fmt::Display for ModuleFunctionName {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl Serialize for ModuleFunctionName {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for ModuleFunctionName {
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

    /// Decodes exactly one complete JSON value.
    pub fn decode(bytes: &[u8]) -> Result<Self, serde_json::Error> {
        serde_json::from_slice(bytes).map(Self)
    }
}

/// Typed registration or invocation selection.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "kebab-case", deny_unknown_fields)]
pub enum CallSelector {
    /// Empty engine parent name requesting complete registration.
    Registration,
    /// Non-empty parent plus an ordinary function or constructor invocation.
    Invocation {
        /// Exact selected parent wire name.
        parent_wire_name: ModuleWireName,
        /// Exact selected function name; empty means the constructor.
        function_wire_name: ModuleFunctionName,
    },
}

/// Call-local identity and its unambiguous selected branch.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct CallIdentity {
    call_id: String,
    selector: CallSelector,
}

impl CallIdentity {
    /// Constructs one bounded local identity.
    pub fn new(call_id: impl Into<String>, selector: CallSelector) -> Result<Self, &'static str> {
        let call_id = call_id.into();
        if call_id.is_empty() || call_id.len() > MAX_CALL_ID_BYTES || call_id.contains('\0') {
            return Err("module call identity is invalid");
        }
        Ok(Self { call_id, selector })
    }

    /// Borrows the call-local diagnostic identity.
    #[must_use]
    pub fn call_id(&self) -> &str {
        &self.call_id
    }

    /// Borrows the typed branch and invocation coordinate.
    #[must_use]
    pub const fn selector(&self) -> &CallSelector {
        &self.selector
    }
}

impl<'de> Deserialize<'de> for CallIdentity {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Raw {
            call_id: String,
            selector: CallSelector,
        }

        let raw = Raw::deserialize(deserializer)?;
        Self::new(raw.call_id, raw.selector).map_err(serde::de::Error::custom)
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
    /// Local identity and the already-decoded registration/invocation branch.
    pub identity: CallIdentity,
    /// Exact parent JSON when an instance function is selected.
    pub parent: Option<ModuleJson>,
    /// Named arguments before completeness and duplicate validation.
    pub arguments: Vec<NamedModuleArgument>,
}

impl CallEnvelope {
    /// Constructs a current-format call envelope.
    #[must_use]
    pub fn new(
        identity: CallIdentity,
        parent: Option<ModuleJson>,
        arguments: Vec<NamedModuleArgument>,
    ) -> Self {
        Self {
            format_version: CALL_FORMAT_VERSION,
            identity,
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
            identity: CallIdentity,
            parent: Option<ModuleJson>,
            arguments: Vec<NamedModuleArgument>,
        }

        let raw = Raw::deserialize(deserializer)?;
        if raw.format_version != CALL_FORMAT_VERSION {
            return Err(serde::de::Error::custom(
                "unsupported module call format version",
            ));
        }
        Ok(Self::new(raw.identity, raw.parent, raw.arguments))
    }
}
