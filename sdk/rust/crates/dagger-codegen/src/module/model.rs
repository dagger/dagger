//! Strict canonical values shared by source compilation and generated module assets.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::num::NonZeroU32;

use serde::{Deserialize, Deserializer, Serialize, Serializer};
use sha2::{Digest as _, Sha256};

/// Current canonical module document format.
pub const MODULE_FORMAT_VERSION: u32 = 1;
/// Current procedural/source authoring ABI.
pub const AUTHORING_ABI_VERSION: u32 = 1;

/// Strict current module wire-format version.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct FormatVersion(u32);

impl FormatVersion {
    /// Returns the only format version accepted by this implementation.
    #[must_use]
    pub const fn current() -> Self {
        Self(MODULE_FORMAT_VERSION)
    }
}

impl Serialize for FormatVersion {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_u32(self.0)
    }
}

impl<'de> Deserialize<'de> for FormatVersion {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = u32::deserialize(deserializer)?;
        if value == MODULE_FORMAT_VERSION {
            Ok(Self(value))
        } else {
            Err(serde::de::Error::custom(
                "unsupported module format version",
            ))
        }
    }
}

macro_rules! validated_string {
    ($name:ident, $doc:literal, $validator:expr) => {
        #[doc = $doc]
        #[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
        pub struct $name(String);

        impl $name {
            /// Validates and constructs the canonical value.
            pub fn new(value: impl Into<String>) -> Result<Self, String> {
                let value = value.into();
                ($validator)(&value)?;
                Ok(Self(value))
            }

            /// Borrows the canonical spelling.
            #[must_use]
            pub fn as_str(&self) -> &str {
                &self.0
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(&self.0)
            }
        }

        impl Serialize for $name {
            fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
            where
                S: Serializer,
            {
                serializer.serialize_str(&self.0)
            }
        }

        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
            where
                D: Deserializer<'de>,
            {
                let value = String::deserialize(deserializer)?;
                Self::new(value).map_err(serde::de::Error::custom)
            }
        }
    };
}

fn non_empty(value: &str) -> Result<(), String> {
    if value.is_empty() || value.contains('\0') {
        Err("value must be non-empty and contain no NUL".to_owned())
    } else {
        Ok(())
    }
}

fn identifier(value: &str) -> Result<(), String> {
    let mut characters = value.chars();
    let valid_start = characters
        .next()
        .is_some_and(|character| character == '_' || character.is_ascii_alphabetic());
    if valid_start
        && characters.all(|character| character == '_' || character.is_ascii_alphanumeric())
    {
        Ok(())
    } else {
        Err("value must be a canonical identifier".to_owned())
    }
}

fn package_path(value: &str) -> Result<(), String> {
    if value.is_empty()
        || value.starts_with('/')
        || value.contains('\\')
        || value.split('/').any(|component| {
            component.is_empty()
                || component == "."
                || component == ".."
                || component.contains('\0')
        })
    {
        Err("path must be normalized and package-relative".to_owned())
    } else {
        Ok(())
    }
}

fn rust_symbol(value: &str) -> Result<(), String> {
    if value.starts_with("crate::")
        && value
            .split("::")
            .all(|component| component == "crate" || identifier(component).is_ok())
    {
        Ok(())
    } else {
        Err("Rust symbol must be a canonical crate-relative path".to_owned())
    }
}

validated_string!(
    PackageName,
    "Validated Cargo package name.",
    |value: &str| {
        if !value.is_empty()
            && value.chars().all(|character| {
                character.is_ascii_alphanumeric() || matches!(character, '-' | '_')
            })
        {
            Ok(())
        } else {
            Err("package name is invalid".to_owned())
        }
    }
);
validated_string!(
    ModuleSourcePath,
    "Normalized package-relative source path.",
    package_path
);
validated_string!(
    RustSymbol,
    "Canonical crate-relative Rust item or member symbol.",
    rust_symbol
);
validated_string!(WireName, "Validated Dagger wire identifier.", identifier);
validated_string!(
    GeneratedAssetPath,
    "Normalized generated module asset path.",
    package_path
);
validated_string!(
    TargetValue,
    "Non-empty immutable exact-target coordinate.",
    non_empty
);
validated_string!(
    CapabilityId,
    "Stable completeness capability identity.",
    non_empty
);
validated_string!(
    EvidenceId,
    "Stable evidence observation identity.",
    non_empty
);

/// Validated lowercase SHA-256 digest with an explicit algorithm prefix.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct Sha256Digest(String);

impl Sha256Digest {
    /// Parses a canonical `sha256:` digest.
    pub fn new(value: impl Into<String>) -> Result<Self, String> {
        let value = value.into();
        let Some(hex) = value.strip_prefix("sha256:") else {
            return Err("digest must use the sha256 algorithm prefix".to_owned());
        };
        if hex.len() != 64
            || !hex
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        {
            return Err("digest must contain 64 lowercase hexadecimal digits".to_owned());
        }
        Ok(Self(value))
    }

    /// Constructs a canonical digest from exactly 32 bytes.
    pub fn from_bytes(bytes: [u8; 32]) -> Result<Self, String> {
        let hex = bytes
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        Self::new(format!("sha256:{hex}"))
    }

    /// Hashes arbitrary bytes without assigning an artifact domain.
    pub fn hash_bytes(bytes: &[u8]) -> Self {
        Self::from_bytes(Sha256::digest(bytes).into())
            .expect("SHA-256 formatting satisfies the digest grammar")
    }

    /// Borrows the canonical digest spelling.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for Sha256Digest {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl Serialize for Sha256Digest {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for Sha256Digest {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        Self::new(String::deserialize(deserializer)?).map_err(serde::de::Error::custom)
    }
}

/// Versioned syntactic ABI shared by source analysis and procedural macros.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct AuthoringAbi(u32);

impl AuthoringAbi {
    /// Returns the only ABI accepted by this implementation.
    #[must_use]
    pub const fn current() -> Self {
        Self(AUTHORING_ABI_VERSION)
    }
}

impl Serialize for AuthoringAbi {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_u32(self.0)
    }
}

impl<'de> Deserialize<'de> for AuthoringAbi {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = u32::deserialize(deserializer)?;
        if value == AUTHORING_ABI_VERSION {
            Ok(Self(value))
        } else {
            Err(serde::de::Error::custom("unsupported authoring ABI"))
        }
    }
}

/// Immutable target identity required by every durable authoring artifact.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleTarget {
    /// Exact Dagger engine revision.
    pub dagger_revision: TargetValue,
    /// Exact engine version.
    pub engine_version: TargetValue,
    /// Exact Rust SDK version.
    pub rust_sdk_version: TargetValue,
    /// Exact Rust compiler version.
    pub rust_toolchain: TargetValue,
    /// Rust edition selected for generated code.
    pub rust_edition: TargetValue,
    /// Checked visible-schema digest.
    pub visible_schema_digest: Sha256Digest,
}

/// Selected package identity without a host filesystem coordinate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModulePackage {
    /// Cargo package name.
    pub name: PackageName,
    /// Crate root within the package.
    pub crate_root: ModuleSourcePath,
    /// Declared package edition.
    pub edition: TargetValue,
}

/// Explicit Cargo/rustc configuration used for source discovery.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CfgEnvironment {
    /// Target triple components and other key/value cfgs.
    pub values: BTreeMap<String, BTreeSet<String>>,
    /// Enabled Cargo features.
    pub features: BTreeSet<String>,
}

/// One immutable UTF-8 source document in a snapshot.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SourceDocument {
    /// Normalized package-relative path.
    pub path: ModuleSourcePath,
    /// Exact UTF-8 source text consumed by the pure compiler.
    pub contents: String,
    /// SHA-256 of `contents` for provenance without host paths.
    pub digest: Sha256Digest,
}

impl SourceDocument {
    /// Constructs a document while deriving its content digest.
    pub fn new(path: ModuleSourcePath, contents: impl Into<String>) -> Self {
        let contents = contents.into();
        let digest = Sha256Digest::hash_bytes(contents.as_bytes());
        Self {
            path,
            contents,
            digest,
        }
    }
}

/// Complete immutable input to pure source discovery.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSourceSnapshot {
    /// Strict document format version.
    pub format_version: FormatVersion,
    /// Selected package identity.
    pub package: ModulePackage,
    /// Explicit target/feature configuration.
    pub cfg: CfgEnvironment,
    /// Documents keyed by their same normalized path.
    pub documents: BTreeMap<ModuleSourcePath, SourceDocument>,
    /// Canonical snapshot digest recorded by the confined builder.
    pub digest: Sha256Digest,
}

/// One-based authored coordinate used as the primary repair location.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SourceCoordinate {
    /// Package-relative source path.
    pub path: ModuleSourcePath,
    /// One-based line.
    pub line: NonZeroU32,
    /// One-based UTF-8 column.
    pub column: NonZeroU32,
}

/// One generated location mapped back to its authored repair coordinate.
///
/// Generated locations are retained for compiler tooling, but diagnostics use
/// `authored` as their primary coordinate so users are never sent to generated code
/// for a repair.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratedCoordinate {
    /// Generator-owned package-relative path.
    pub path: GeneratedAssetPath,
    /// One-based generated line.
    pub line: NonZeroU32,
    /// One-based generated UTF-8 column.
    pub column: NonZeroU32,
    /// Most specific authored token responsible for the generated location.
    pub authored: SourceCoordinate,
}

/// Serializable 128-bit authoring fingerprint.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct AuthoringFingerprintValue(String);

impl AuthoringFingerprintValue {
    /// Formats one const-generic fingerprint as 32 lowercase hex digits.
    #[must_use]
    pub fn from_u128(value: u128) -> Self {
        Self(format!("{value:032x}"))
    }

    /// Returns the const-generic value.
    pub fn as_u128(&self) -> Result<u128, String> {
        u128::from_str_radix(&self.0, 16).map_err(|_| "authoring fingerprint is invalid".to_owned())
    }
}

impl Serialize for AuthoringFingerprintValue {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&self.0)
    }
}

impl<'de> Deserialize<'de> for AuthoringFingerprintValue {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        if value.len() == 32
            && value
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        {
            Ok(Self(value))
        } else {
            Err(serde::de::Error::custom("authoring fingerprint is invalid"))
        }
    }
}

/// Kind-specific exported type descriptor.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "kebab-case", deny_unknown_fields)]
pub enum LocalTypeKind {
    /// Stateful local object.
    Object { root: bool },
    /// Local interface contract.
    Interface,
    /// Unit-variant enum contract.
    Enum,
    /// Transparent scalar newtype.
    Scalar { representation: RustSymbol },
}

/// One exported local type in the canonical descriptor.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LocalTypeDescriptor {
    /// Crate-relative authored symbol.
    pub rust_symbol: RustSymbol,
    /// Exact projected wire name.
    pub wire_name: WireName,
    /// Kind-specific shape.
    pub kind: LocalTypeKind,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
    /// Shared-grammar fingerprint.
    pub fingerprint: AuthoringFingerprintValue,
}

/// One exported constructor or function.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FunctionDescriptor {
    /// Owning object/interface symbol.
    pub parent: RustSymbol,
    /// Authored method symbol.
    pub rust_symbol: RustSymbol,
    /// Exact projected function name.
    pub wire_name: WireName,
    /// Canonically ordered argument wire names.
    pub arguments: Vec<WireName>,
    /// Shared-grammar fingerprint.
    pub fingerprint: AuthoringFingerprintValue,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
}

/// One closed parent/function dispatch coordinate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DispatchCoordinate {
    /// Parent wire name.
    pub parent: WireName,
    /// Function wire name.
    pub function: WireName,
}

/// Canonical source-to-registration model.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleDescriptor {
    /// Strict descriptor format version.
    pub format_version: FormatVersion,
    /// Shared source/macro ABI.
    pub authoring_abi: AuthoringAbi,
    /// Exact target identity.
    pub target: ModuleTarget,
    /// Selected package.
    pub package: ModulePackage,
    /// Root object symbol.
    pub root: RustSymbol,
    /// Types in canonical wire-name order.
    pub types: Vec<LocalTypeDescriptor>,
    /// Functions in canonical dispatch order.
    pub functions: Vec<FunctionDescriptor>,
    /// Closed dispatch coordinates.
    pub dispatch: Vec<DispatchCoordinate>,
    /// Immutable source input digest.
    pub source_digest: Sha256Digest,
    /// Generator identity.
    pub generator_digest: Sha256Digest,
    /// Descriptor identity.
    pub digest: Sha256Digest,
}

/// Engine registration projection derived from one descriptor.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RegistrationProjection {
    /// Strict projection format version.
    pub format_version: FormatVersion,
    /// Source descriptor identity.
    pub descriptor_digest: Sha256Digest,
    /// Type definitions by wire name.
    pub types: BTreeMap<WireName, serde_json::Value>,
}

/// Introspection projection derived from the same descriptor.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleIntrospection {
    /// Strict projection format version.
    pub format_version: FormatVersion,
    /// Source descriptor identity.
    pub descriptor_digest: Sha256Digest,
    /// Introspection types by wire name.
    pub types: BTreeMap<WireName, serde_json::Value>,
}

/// One generated file owned by the module generator.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratedAsset {
    /// Generator-owned path.
    pub path: GeneratedAssetPath,
    /// Content digest.
    pub digest: Sha256Digest,
}

/// Complete generated ownership and provenance manifest.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GeneratedModuleAssets {
    /// Strict manifest format version.
    pub format_version: FormatVersion,
    /// Exact target identity.
    pub target: ModuleTarget,
    /// Source descriptor identity.
    pub descriptor_digest: Sha256Digest,
    /// Assets keyed by the same canonical path.
    pub assets: BTreeMap<GeneratedAssetPath, GeneratedAsset>,
    /// Manifest identity.
    pub digest: Sha256Digest,
}
