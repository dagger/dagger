//! Validated scalar identities shared by every engine-operation model.
//!
//! Construction rejects alternate spellings rather than normalizing them silently.
//! That keeps canonical JSON and its digest a one-to-one representation of meaning.

use std::fmt;
use std::path::{Path, PathBuf};
use std::str::FromStr;

use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use thiserror::Error;
use url::Url;

/// The sole accepted wire-format revision for current engine-operation models.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct FormatVersion;

impl FormatVersion {
    /// Returns the current numeric wire-format revision.
    #[must_use]
    pub const fn get(self) -> u32 {
        1
    }
}

impl Default for FormatVersion {
    fn default() -> Self {
        Self
    }
}

impl Serialize for FormatVersion {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_u32(self.get())
    }
}

impl<'de> Deserialize<'de> for FormatVersion {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = u32::deserialize(deserializer)?;
        if value != 1 {
            return Err(D::Error::custom("unsupported format version"));
        }
        Ok(Self)
    }
}

/// Rejection of an ambiguous, mutable, or non-portable scalar value.
#[derive(Clone, Debug, Eq, Error, PartialEq)]
#[error("invalid {kind}: {reason}")]
pub struct ValueError {
    kind: &'static str,
    reason: String,
}

impl ValueError {
    fn new(kind: &'static str, reason: impl Into<String>) -> Self {
        Self {
            kind,
            reason: reason.into(),
        }
    }
}

macro_rules! validated_string {
    ($name:ident, $kind:literal, $validator:ident, $doc:literal) => {
        #[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
        #[doc = $doc]
        pub struct $name(Box<str>);

        impl $name {
            /// Validates and constructs the canonical scalar.
            pub fn new(value: impl Into<String>) -> Result<Self, ValueError> {
                let value = value.into();
                $validator(&value).map_err(|reason| ValueError::new($kind, reason))?;
                Ok(Self(value.into_boxed_str()))
            }

            /// Borrows the canonical spelling.
            #[must_use]
            pub fn as_str(&self) -> &str {
                &self.0
            }

            /// Returns the canonical spelling.
            #[must_use]
            pub fn into_inner(self) -> String {
                self.0.into()
            }
        }

        impl AsRef<str> for $name {
            fn as_ref(&self) -> &str {
                self.as_str()
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(self.as_str())
            }
        }

        impl FromStr for $name {
            type Err = ValueError;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                Self::new(value)
            }
        }

        impl Serialize for $name {
            fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
            where
                S: Serializer,
            {
                serializer.serialize_str(self.as_str())
            }
        }

        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
            where
                D: Deserializer<'de>,
            {
                let value = String::deserialize(deserializer)?;
                Self::new(value).map_err(D::Error::custom)
            }
        }
    };
}

fn validate_non_empty(value: &str) -> Result<(), String> {
    if value.is_empty() {
        return Err("must not be empty".to_owned());
    }
    if value.trim() != value {
        return Err("must not have leading or trailing whitespace".to_owned());
    }
    if value.chars().any(char::is_control) {
        return Err("must not contain control characters".to_owned());
    }
    Ok(())
}

fn validate_sha256(value: &str) -> Result<(), String> {
    let Some(hex) = value.strip_prefix("sha256:") else {
        return Err("must use lowercase sha256:<hex> form".to_owned());
    };
    if hex.len() != 64 || !hex.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("must contain exactly 64 hexadecimal digest characters".to_owned());
    }
    if hex.bytes().any(|byte| byte.is_ascii_uppercase()) {
        return Err("must use lowercase hexadecimal".to_owned());
    }
    Ok(())
}

fn validate_revision(value: &str) -> Result<(), String> {
    if value.len() != 40 || !value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("must be a full 40-character Git revision".to_owned());
    }
    if value.bytes().any(|byte| byte.is_ascii_uppercase()) {
        return Err("must use lowercase hexadecimal".to_owned());
    }
    Ok(())
}

fn validate_semver(value: &str) -> Result<(), String> {
    let version = semver::Version::parse(value)
        .map_err(|_| "must be an exact semantic version without a leading v".to_owned())?;
    if version.to_string() != value {
        return Err("must use the canonical semantic-version spelling".to_owned());
    }
    Ok(())
}

fn validate_repository_url(value: &str) -> Result<(), String> {
    let parsed = Url::parse(value).map_err(|_| "must be an absolute HTTPS URL".to_owned())?;
    if parsed.scheme() != "https" || parsed.host_str().is_none() {
        return Err("must be an absolute HTTPS URL".to_owned());
    }
    if !parsed.username().is_empty() || parsed.password().is_some() {
        return Err("must not contain user information".to_owned());
    }
    if parsed.query().is_some() || parsed.fragment().is_some() || parsed.port().is_some() {
        return Err("must not contain a port, query, or fragment".to_owned());
    }
    if parsed.path() == "/" || parsed.path().contains("//") {
        return Err("must identify a repository path".to_owned());
    }
    if parsed.as_str() != value {
        return Err("must already use the URL parser's canonical spelling".to_owned());
    }
    Ok(())
}

fn validate_registry(value: &str) -> Result<(), String> {
    if value != "crates-io" {
        return Err("only the canonical crates-io registry is approved".to_owned());
    }
    Ok(())
}

fn validate_sdk_package(value: &str) -> Result<(), String> {
    if value != "dagger-sdk" {
        return Err("must name the public dagger-sdk package".to_owned());
    }
    Ok(())
}

fn validate_relative_path(value: &str) -> Result<(), String> {
    validate_non_empty(value)?;
    if value.starts_with('/')
        || value.ends_with('/')
        || value.contains('\\')
        || value.contains(':')
        || value.contains("//")
    {
        return Err("must be a normalized slash-separated relative path".to_owned());
    }
    if value
        .split('/')
        .any(|component| component.is_empty() || component == "." || component == "..")
    {
        return Err("must not contain empty or dot components".to_owned());
    }
    Ok(())
}

fn validate_rust_target(value: &str) -> Result<(), String> {
    validate_non_empty(value)?;
    if !value.bytes().all(|byte| {
        byte.is_ascii_lowercase() || byte.is_ascii_digit() || matches!(byte, b'-' | b'_')
    }) || value.split('-').any(str::is_empty)
    {
        return Err("must be a canonical lowercase Rust target triple".to_owned());
    }
    Ok(())
}

validated_string!(
    Sha256Digest,
    "SHA-256 digest",
    validate_sha256,
    "A lowercase SHA-256 identity in `sha256:<hex>` form."
);
validated_string!(
    FullRevision,
    "Git revision",
    validate_revision,
    "A full lowercase 40-character Git commit identity."
);
validated_string!(
    ExactVersion,
    "version",
    validate_semver,
    "An exact canonical semantic version."
);
validated_string!(
    ExactRustToolchain,
    "Rust toolchain",
    validate_semver,
    "An exact canonical Rust toolchain version."
);
validated_string!(
    CanonicalRepositoryUrl,
    "repository URL",
    validate_repository_url,
    "A credential-free canonical HTTPS repository URL."
);
validated_string!(
    CanonicalRegistry,
    "registry",
    validate_registry,
    "The canonical approved Cargo registry identity."
);
validated_string!(
    SdkPackageName,
    "SDK package",
    validate_sdk_package,
    "The sole public Rust SDK package identity."
);
validated_string!(
    StableCoordinate,
    "diagnostic coordinate",
    validate_non_empty,
    "A stable, non-secret diagnostic coordinate."
);
validated_string!(
    RustTarget,
    "Rust target",
    validate_rust_target,
    "A canonical lowercase Rust compilation target triple."
);

/// A normalized, non-empty path relative to an explicit operation-root capability.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct RelativeOperationPath(Box<str>);

impl RelativeOperationPath {
    /// Validates a lexical path without consulting ambient filesystem state.
    pub fn parse(value: &str) -> Result<Self, ValueError> {
        validate_relative_path(value)
            .map_err(|reason| ValueError::new("operation-relative path", reason))?;
        Ok(Self(value.into()))
    }

    /// Borrows the canonical slash-separated spelling.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// Joins the validated path to an explicit root without performing I/O.
    #[must_use]
    pub fn join_lexically(&self, root: &Path) -> PathBuf {
        self.as_str()
            .split('/')
            .fold(root.to_path_buf(), |path, component| path.join(component))
    }
}

impl fmt::Display for RelativeOperationPath {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.as_str())
    }
}

impl FromStr for RelativeOperationPath {
    type Err = ValueError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        Self::parse(value)
    }
}

impl Serialize for RelativeOperationPath {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(self.as_str())
    }
}

impl<'de> Deserialize<'de> for RelativeOperationPath {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        Self::parse(&value).map_err(D::Error::custom)
    }
}
