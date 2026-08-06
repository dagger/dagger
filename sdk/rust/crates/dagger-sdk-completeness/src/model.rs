//! Durable vocabulary for the Rust SDK completeness contract.
//!
//! These types are the interchange boundary between extraction, classification, conformance, and
//! reporting. They reject ambiguous scalar spellings at construction, reject unknown object
//! fields at decode, and normalize semantically unordered collections. Consequently, a value that
//! reaches canonical encoding has one portable meaning and does not retain machine-local paths or
//! mutable authority references.

use std::collections::BTreeMap;
use std::fmt;
use std::ops::Deref;
use std::str::FromStr;

use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use sha2::{Digest as _, Sha256};
use thiserror::Error;

use crate::diagnostic::ContractDiagnostic;

/// JSON value embedded in a durable artifact after its surrounding model has been validated.
pub type CanonicalValue = serde_json::Value;

#[derive(Clone, Debug, Eq, Error, PartialEq)]
#[error("invalid {kind}: {reason}")]
/// Rejection of an ambiguous or non-portable scalar value.
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

macro_rules! string_newtype {
    ($name:ident, $kind:literal, $validator:ident, $doc:literal) => {
        #[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
        #[doc = $doc]
        pub struct $name(String);

        impl $name {
            /// Validates and constructs the durable scalar.
            pub fn new(value: impl Into<String>) -> Result<Self, ValueError> {
                let value = value.into();
                $validator(&value).map_err(|reason| ValueError::new($kind, reason))?;
                Ok(Self(value))
            }

            /// Borrows the validated canonical spelling.
            pub fn as_str(&self) -> &str {
                &self.0
            }

            /// Returns the validated canonical spelling.
            pub fn into_inner(self) -> String {
                self.0
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

        impl TryFrom<String> for $name {
            type Error = ValueError;

            fn try_from(value: String) -> Result<Self, Self::Error> {
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

fn validate_non_empty_text(value: &str) -> Result<(), String> {
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

fn validate_canonical_id(value: &str) -> Result<(), String> {
    validate_non_empty_text(value)?;
    if value.starts_with('/') || value.ends_with('/') || value.contains("//") {
        return Err("must contain non-empty relative segments".to_owned());
    }

    let bytes = value.as_bytes();
    let mut index = 0;
    while index < bytes.len() {
        let byte = bytes[index];
        if byte.is_ascii_lowercase()
            || byte.is_ascii_digit()
            || matches!(byte, b'-' | b'_' | b'.' | b'/')
        {
            index += 1;
            continue;
        }
        if byte == b'%'
            && index + 2 < bytes.len()
            && bytes[index + 1].is_ascii_hexdigit()
            && bytes[index + 2].is_ascii_hexdigit()
            && !bytes[index + 1].is_ascii_lowercase()
            && !bytes[index + 2].is_ascii_lowercase()
        {
            index += 3;
            continue;
        }
        return Err(format!(
            "contains non-canonical byte at offset {index}; use lowercase ASCII or percent encoding"
        ));
    }

    if value
        .split('/')
        .any(|segment| segment == "." || segment == "..")
    {
        return Err("must not contain dot segments".to_owned());
    }
    Ok(())
}

fn validate_commit_sha(value: &str) -> Result<(), String> {
    if value.len() != 40 || !value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("must be a full 40-character hexadecimal Git commit".to_owned());
    }
    if value.bytes().any(|byte| byte.is_ascii_uppercase()) {
        return Err("must use lowercase hexadecimal".to_owned());
    }
    Ok(())
}

fn validate_digest(value: &str) -> Result<(), String> {
    let Some(hex) = value.strip_prefix("sha256:") else {
        return Err("must use the sha256:<hex> form".to_owned());
    };
    if hex.len() != 64 || !hex.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err("must contain exactly 64 hexadecimal digest characters".to_owned());
    }
    if hex.bytes().any(|byte| byte.is_ascii_uppercase()) {
        return Err("must use lowercase hexadecimal".to_owned());
    }
    Ok(())
}

fn validate_repository_id(value: &str) -> Result<(), String> {
    validate_non_empty_text(value)?;
    let parts = value.split('/').collect::<Vec<_>>();
    if parts.len() != 3 || parts[0] != "github.com" {
        return Err("must use github.com/<owner>/<repository>".to_owned());
    }
    if parts[1..].iter().any(|part| {
        part.is_empty()
            || !part.bytes().all(|byte| {
                byte.is_ascii_lowercase()
                    || byte.is_ascii_digit()
                    || matches!(byte, b'-' | b'_' | b'.')
            })
    }) {
        return Err("owner and repository must use canonical lowercase GitHub names".to_owned());
    }
    Ok(())
}

fn validate_relative_path(value: &str) -> Result<(), String> {
    validate_non_empty_text(value)?;
    if value.starts_with('/')
        || value.ends_with('/')
        || value.contains('\\')
        || value.contains(':')
        || value.contains("//")
    {
        return Err("must be a canonical repository-relative path".to_owned());
    }
    if value
        .split('/')
        .any(|component| component.is_empty() || component == "." || component == "..")
    {
        return Err("must not contain empty or dot path components".to_owned());
    }
    Ok(())
}

fn validate_source_locator(value: &str) -> Result<(), String> {
    validate_non_empty_text(value)?;
    if value.starts_with('/') || value.starts_with("file://") || value.contains('\\') {
        return Err("must not be an absolute or machine-local locator".to_owned());
    }
    Ok(())
}

string_newtype!(
    NonEmptyText,
    "text",
    validate_non_empty_text,
    "Non-empty, trimmed text without control characters."
);
string_newtype!(
    CommitSha,
    "commit SHA",
    validate_commit_sha,
    "A full, lowercase, 40-character Git commit identity."
);
string_newtype!(
    Digest,
    "digest",
    validate_digest,
    "A lowercase SHA-256 identity in `sha256:<hex>` form."
);
string_newtype!(
    RepositoryId,
    "repository identity",
    validate_repository_id,
    "A canonical lowercase GitHub repository identity, without transport or mutable ref."
);
string_newtype!(
    RepositoryRelativePath,
    "repository-relative path",
    validate_relative_path,
    "A portable repository-relative path without dot segments or platform prefixes."
);
string_newtype!(
    SourceLocator,
    "source locator",
    validate_source_locator,
    "A portable locator within a pinned source, never an absolute machine-local path."
);
string_newtype!(
    AuthorityId,
    "authority ID",
    validate_canonical_id,
    "Stable identifier for a completeness authority."
);
string_newtype!(
    CapabilityId,
    "capability ID",
    validate_canonical_id,
    "Stable identifier for one normalized SDK capability."
);
string_newtype!(
    CapabilityKind,
    "capability kind",
    validate_canonical_id,
    "Canonical classification of a capability's semantic shape."
);
string_newtype!(
    CheckId,
    "check ID",
    validate_canonical_id,
    "Stable identifier for a conformance-harness check."
);
string_newtype!(
    EvidenceId,
    "evidence ID",
    validate_canonical_id,
    "Stable identifier for an evidence record."
);
string_newtype!(
    ExtractorId,
    "extractor ID",
    validate_canonical_id,
    "Stable identifier for the logic that extracted an authority source."
);
string_newtype!(
    ExecutableId,
    "executable ID",
    validate_canonical_id,
    "Portable executable identity allowed in a recorded command."
);
string_newtype!(
    RuleId,
    "rule ID",
    validate_canonical_id,
    "Stable identifier for a deterministic classification rule."
);
string_newtype!(
    ScenarioId,
    "scenario ID",
    validate_canonical_id,
    "Stable identifier for a behavioural conformance scenario."
);
string_newtype!(
    SourceItemId,
    "source item ID",
    validate_canonical_id,
    "Stable identifier for an extracted item within an authority source."
);
string_newtype!(
    SourceItemKind,
    "source item kind",
    validate_canonical_id,
    "Canonical semantic class assigned by a source extractor."
);

impl Digest {
    /// Computes a direct SHA-256 identity for raw bytes.
    pub fn sha256(bytes: impl AsRef<[u8]>) -> Self {
        Self::from_sha256_output(Sha256::digest(bytes.as_ref()).into())
    }

    pub(crate) fn from_sha256_output(hash: [u8; 32]) -> Self {
        Self(format!("sha256:{}", encode_lower_hex(&hash)))
    }
}

fn encode_lower_hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut encoded = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        encoded.push(char::from(HEX[usize::from(byte >> 4)]));
        encoded.push(char::from(HEX[usize::from(byte & 0x0f)]));
    }
    encoded
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(transparent)]
/// Canonical digest of an immutable [`TargetDescriptor`].
pub struct TargetDigest(pub Digest);

impl TargetDigest {
    /// Wraps a digest that was computed in the target domain.
    pub fn new(digest: Digest) -> Self {
        Self(digest)
    }

    /// Borrows the underlying SHA-256 identity.
    pub fn digest(&self) -> &Digest {
        &self.0
    }
}

impl fmt::Display for TargetDigest {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        fmt::Display::fmt(&self.0, formatter)
    }
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
/// Dagger release version with the durable `v<semver>` spelling.
///
/// Construction accepts an optional leading `v`; serialization always includes exactly one.
pub struct DaggerVersion(semver::Version);

impl DaggerVersion {
    /// Parses a Dagger version and normalizes its prefix.
    pub fn new(value: impl AsRef<str>) -> Result<Self, ValueError> {
        let value = value.as_ref();
        if value.trim() != value || value.is_empty() {
            return Err(ValueError::new(
                "Dagger version",
                "must not be empty or surrounded by whitespace",
            ));
        }
        let raw = value.strip_prefix('v').unwrap_or(value);
        let version = semver::Version::parse(raw)
            .map_err(|error| ValueError::new("Dagger version", error.to_string()))?;
        Ok(Self(version))
    }

    /// Borrows the parsed semantic version without the Dagger display prefix.
    pub fn version(&self) -> &semver::Version {
        &self.0
    }
}

impl fmt::Display for DaggerVersion {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "v{}", self.0)
    }
}

impl FromStr for DaggerVersion {
    type Err = ValueError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        Self::new(value)
    }
}

impl Serialize for DaggerVersion {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for DaggerVersion {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        Self::new(value).map_err(D::Error::custom)
    }
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
/// Strict semantic version whose durable spelling never has a leading `v`.
pub struct SemverVersion(semver::Version);

impl SemverVersion {
    /// Parses a semantic version, rejecting Dagger's `v`-prefixed convention.
    pub fn new(value: impl AsRef<str>) -> Result<Self, ValueError> {
        let value = value.as_ref();
        if value.starts_with('v') {
            return Err(ValueError::new(
                "semantic version",
                "must not use a leading v",
            ));
        }
        let version = semver::Version::parse(value)
            .map_err(|error| ValueError::new("semantic version", error.to_string()))?;
        Ok(Self(version))
    }

    /// Borrows the parsed semantic version.
    pub fn version(&self) -> &semver::Version {
        &self.0
    }
}

impl fmt::Display for SemverVersion {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        fmt::Display::fmt(&self.0, formatter)
    }
}

impl FromStr for SemverVersion {
    type Err = ValueError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        Self::new(value)
    }
}

impl Serialize for SemverVersion {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for SemverVersion {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let value = String::deserialize(deserializer)?;
        Self::new(value).map_err(D::Error::custom)
    }
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
/// Rust language edition recorded as part of an immutable SDK target.
pub enum RustEdition {
    #[serde(rename = "2018")]
    Edition2018,
    #[serde(rename = "2021")]
    Edition2021,
    #[serde(rename = "2024")]
    Edition2024,
}

impl fmt::Display for RustEdition {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Edition2018 => "2018",
            Self::Edition2021 => "2021",
            Self::Edition2024 => "2024",
        })
    }
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
/// A semantically unordered collection with one sorted, duplicate-free representation.
///
/// Canonical encoding preserves ordinary array order because some arrays are ordered. This type
/// makes set semantics explicit and normalizes them before hashing or serialization.
pub struct CanonicalSet<T>(Vec<T>);

impl<T: Ord> CanonicalSet<T> {
    /// Sorts and deduplicates `values` into their canonical representation.
    pub fn new(values: impl IntoIterator<Item = T>) -> Self {
        let mut values = values.into_iter().collect::<Vec<_>>();
        // Input enumeration can vary with filesystem, map, or extractor traversal. Normalizing at
        // construction prevents those irrelevant differences from changing artifact digests.
        values.sort_unstable();
        values.dedup();
        Self(values)
    }

    /// Returns the sorted, duplicate-free elements.
    pub fn into_inner(self) -> Vec<T> {
        self.0
    }
}

impl<T> CanonicalSet<T> {
    /// Borrows the sorted, duplicate-free elements.
    pub fn as_slice(&self) -> &[T] {
        &self.0
    }

    /// Reports whether this set contains no elements.
    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    /// Returns the number of distinct elements.
    pub fn len(&self) -> usize {
        self.0.len()
    }
}

impl<T> Default for CanonicalSet<T> {
    fn default() -> Self {
        Self(Vec::new())
    }
}

impl<T> Deref for CanonicalSet<T> {
    type Target = [T];

    fn deref(&self) -> &Self::Target {
        self.as_slice()
    }
}

impl<T: Ord> From<Vec<T>> for CanonicalSet<T> {
    fn from(values: Vec<T>) -> Self {
        Self::new(values)
    }
}

impl<T> IntoIterator for CanonicalSet<T> {
    type IntoIter = std::vec::IntoIter<T>;
    type Item = T;

    fn into_iter(self) -> Self::IntoIter {
        self.0.into_iter()
    }
}

impl<T: Serialize> Serialize for CanonicalSet<T> {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        self.0.serialize(serializer)
    }
}

impl<'de, T> Deserialize<'de> for CanonicalSet<T>
where
    T: Deserialize<'de> + Ord,
{
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        Vec::<T>::deserialize(deserializer).map(Self::new)
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Complete immutable identity of one Rust SDK completeness assessment target.
///
/// The descriptor pins every authority needed to interpret a result. Mutable labels are optional
/// metadata and never replace full repository revisions or content digests.
pub struct TargetDescriptor {
    pub contract_format_version: SemverVersion,
    pub dagger_repository: RepositoryId,
    pub dagger_revision: CommitSha,
    pub engine_version: DaggerVersion,
    pub schema_version: NonEmptyText,
    pub schema_digest: Digest,
    pub go_sdk_repository: RepositoryId,
    pub go_sdk_revision: CommitSha,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub go_sdk_version_label: Option<NonEmptyText>,
    pub sdk_contract_repository: RepositoryId,
    pub sdk_contract_revision: CommitSha,
    pub sdk_contract_cli_version: DaggerVersion,
    pub rust_sdk_version: SemverVersion,
    pub rust_edition: RustEdition,
    pub rust_version: SemverVersion,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub previous_target: Option<TargetDigest>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Role an authority plays in defining or assessing SDK completeness.
pub enum AuthorityClass {
    EngineSchema,
    GoClient,
    GoEngineSdk,
    GoCodegen,
    GoIntegrationTests,
    SdkContractHarness,
    RustPolicy,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Includes all extractable items under a repository-relative path.
pub struct PathSourceSelector {
    pub path: RepositoryRelativePath,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Includes one symbol or semantic locator within a repository-relative path.
pub struct SymbolSourceSelector {
    pub path: RepositoryRelativePath,
    pub locator: SourceLocator,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Portable selection boundary for authority extraction.
pub enum SourceSelector {
    Path(PathSourceSelector),
    Symbol(SymbolSourceSelector),
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Explicit, reviewable removal from an authority source selection.
pub struct SourceExclusion {
    pub selector: SourceSelector,
    pub rationale: NonEmptyText,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Versioned identity of the logic used to interpret selected source bytes.
pub struct ExtractorIdentity {
    pub extractor_id: ExtractorId,
    pub version: SemverVersion,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Pinned repository slice from which one class of authoritative facts is extracted.
pub struct AuthoritySource {
    pub authority_id: AuthorityId,
    pub authority_class: AuthorityClass,
    pub repository: RepositoryId,
    pub revision: CommitSha,
    pub include: CanonicalSet<SourceSelector>,
    pub exclude: CanonicalSet<SourceExclusion>,
    pub extractor: ExtractorIdentity,
    pub source_digest: Digest,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Authority sources keyed by their stable identities.
pub struct AuthorityRegistry {
    pub authorities: BTreeMap<AuthorityId, AuthoritySource>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Lifecycle disposition assigned to an extracted source item.
pub enum SourceItemState {
    Active,
    Deprecated,
    Skipped,
    Removed,
    HarnessSelf,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// One normalized semantic fact extracted from a pinned authority.
pub struct SourceItem {
    pub source_item_id: SourceItemId,
    pub authority_id: AuthorityId,
    pub item_kind: SourceItemKind,
    pub locator: SourceLocator,
    pub semantic_signature: CanonicalValue,
    pub fingerprint: Digest,
    pub state: SourceItemState,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Extracted source items keyed by stable identity.
pub struct SourceItemInventory {
    pub items: BTreeMap<SourceItemId, SourceItem>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
/// Assessed implementation state of one canonical capability.
///
/// Spellings are deliberately compatible with the approved ledger format rather than Rust's enum
/// naming conventions.
pub enum Status {
    #[serde(rename = "Missing")]
    Missing,
    #[serde(rename = "Partial")]
    Partial,
    #[serde(rename = "Implemented")]
    Implemented,
    #[serde(rename = "Idiomatic_Equivalent")]
    IdiomaticEquivalent,
    #[serde(rename = "Inapplicable")]
    Inapplicable,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Authority-level stability of a capability, independent of implementation status.
pub enum Stability {
    Stable,
    Experimental,
    Internal,
    NotApplicable,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
/// Delivery feature that owns closure of a capability gap.
pub enum FeatureId {
    #[serde(rename = "feature-2")]
    Feature2,
    #[serde(rename = "feature-3")]
    Feature3,
    #[serde(rename = "feature-4")]
    Feature4,
    #[serde(rename = "feature-5")]
    Feature5,
    #[serde(rename = "feature-6")]
    Feature6,
    #[serde(rename = "feature-7")]
    Feature7,
    #[serde(rename = "feature-8")]
    Feature8,
    #[serde(rename = "feature-9")]
    Feature9,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Claim category supported by an evidence record.
pub enum EvidenceKind {
    Authority,
    Implementation,
    Verification,
    Decision,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Operating system included in a conformance evidence scope.
pub enum OperatingSystem {
    Linux,
    Macos,
    Windows,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Processor architecture included in a conformance evidence scope.
pub enum Architecture {
    Amd64,
    Arm64,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Portable operating-system and architecture pair for scoped evidence.
pub struct Platform {
    pub operating_system: OperatingSystem,
    pub architecture: Architecture,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Reproducible command description recorded without machine-local paths.
///
/// Argument order is semantic and is therefore preserved; environment entries are keyed for
/// deterministic serialization.
pub struct CommandSpec {
    pub program: ExecutableId,
    pub args: Vec<String>,
    pub working_directory: RepositoryRelativePath,
    pub environment: BTreeMap<String, String>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Recorded or expected outcome of a verification command.
pub enum CheckOutcome {
    Passed,
    Failed,
    Skipped,
    Error,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Expected command disposition plus the assertion it proves.
pub struct ExpectedOutcome {
    pub outcome: CheckOutcome,
    pub assertion: NonEmptyText,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Reviewable claim anchored to immutable source and optional execution evidence.
///
/// An execution target is required when the evidence depends on observed behaviour rather than
/// source alone; later validators enforce that lifecycle rule.
pub struct EvidenceReference {
    pub evidence_id: EvidenceId,
    pub evidence_kind: EvidenceKind,
    pub repository: RepositoryId,
    pub revision: CommitSha,
    pub path: RepositoryRelativePath,
    pub locator: SourceLocator,
    pub claim: NonEmptyText,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub command: Option<CommandSpec>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub expected_outcome: Option<ExpectedOutcome>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub execution_target: Option<TargetDigest>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub platform_scope: CanonicalSet<Platform>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub proved_capability_ids: CanonicalSet<CapabilityId>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Authority-derived definition of one capability before Rust classification.
pub struct CapabilityDefinition {
    pub capability_id: CapabilityId,
    pub authority_id: AuthorityId,
    pub capability_kind: CapabilityKind,
    pub source_item_ids: CanonicalSet<SourceItemId>,
    pub source_anchors: CanonicalSet<EvidenceReference>,
    pub summary: NonEmptyText,
    pub semantic_signature: CanonicalValue,
    pub capability_fingerprint: Digest,
    pub stability: Stability,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Capability definitions keyed by stable capability identity.
pub struct CapabilityDefinitions {
    pub capabilities: BTreeMap<CapabilityId, CapabilityDefinition>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Canonical capability inventory produced from all pinned authority sources.
pub struct CanonicalInventory {
    pub capabilities: BTreeMap<CapabilityId, CapabilityDefinition>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Rust implementation classification applied to a capability or rule expansion.
pub struct ClassificationValues {
    pub status: Status,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gap: Option<NonEmptyText>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub owner_feature: Option<FeatureId>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub implementation_evidence: CanonicalSet<EvidenceId>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub verification_evidence: CanonicalSet<EvidenceId>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub decision_evidence: CanonicalSet<EvidenceId>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Conjunctive selector used to classify an explicitly bounded capability set.
pub struct ClassificationSelector {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub authority_id: Option<AuthorityId>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub capability_kind: Option<CapabilityKind>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stability: Option<Stability>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source_item_kind: Option<SourceItemKind>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub capability_id_prefix: Option<CapabilityId>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Review fence for the exact capability expansion a rule is allowed to affect.
///
/// A digest supports large expansions while an explicit set keeps smaller rules readable. Either
/// form makes authority drift fail closed rather than silently changing classification scope.
pub enum ExpectedSet {
    CapabilityIds(CanonicalSet<CapabilityId>),
    Digest(Digest),
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Deterministic bulk classification with explicit, capability-local overrides.
pub struct ClassificationRule {
    pub rule_id: RuleId,
    pub authority_id: AuthorityId,
    pub selector: ClassificationSelector,
    pub expected_capability_ids: ExpectedSet,
    pub classification: ClassificationValues,
    pub overrides: BTreeMap<CapabilityId, ClassificationValues>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Human-reviewed exact classifications and bounded classification rules.
pub struct ClassificationInput {
    pub exact: BTreeMap<CapabilityId, ClassificationValues>,
    pub rules: BTreeMap<RuleId, ClassificationRule>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Fully resolved capability definition, Rust status, ownership, and evidence.
pub struct CapabilityRecord {
    pub capability_id: CapabilityId,
    pub authority_id: AuthorityId,
    pub capability_kind: CapabilityKind,
    pub source_item_ids: CanonicalSet<SourceItemId>,
    pub source_anchors: CanonicalSet<EvidenceReference>,
    pub summary: NonEmptyText,
    pub semantic_signature: CanonicalValue,
    pub capability_fingerprint: Digest,
    pub status: Status,
    pub stability: Stability,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gap: Option<NonEmptyText>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub owner_feature: Option<FeatureId>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub implementation_evidence: CanonicalSet<EvidenceId>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub verification_evidence: CanonicalSet<EvidenceId>,
    #[serde(default, skip_serializing_if = "CanonicalSet::is_empty")]
    pub decision_evidence: CanonicalSet<EvidenceId>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Deterministic assessment ledger after classification rules and overrides resolve.
pub struct ResolvedLedger {
    pub capabilities: BTreeMap<CapabilityId, CapabilityRecord>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Evidence records keyed by stable identity for cross-artifact integrity checks.
pub struct EvidenceRegistry {
    pub evidence: BTreeMap<EvidenceId, EvidenceReference>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Whether a harness check assesses the Rust SDK or the harness's own integrity.
pub enum HarnessCheckKind {
    SubjectConformance,
    HarnessSelf,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Pinned mapping from one `sdk-sdk` check to the capabilities it can prove.
///
/// Harness-self checks deliberately carry no subject capability proof; integrity validation keeps
/// that negative scope from being counted as Rust conformance.
pub struct HarnessCheckMapping {
    pub check_id: CheckId,
    pub check_kind: HarnessCheckKind,
    pub harness_revision: CommitSha,
    pub source_locator: SourceLocator,
    /// Fingerprint of the complete public check declaration at `harness_revision`.
    pub source_fingerprint: Digest,
    pub capability_ids: CanonicalSet<CapabilityId>,
    pub execution_target: TargetDigest,
    /// Exact CLI bytes selected to execute this mapping.
    pub cli_artifact_digest: Digest,
    /// Exact Rust workspace or module artifact assessed by the check.
    pub verified_artifact_digest: Digest,
    pub platform_scope: CanonicalSet<Platform>,
    pub invocation: CommandSpec,
    pub expected_outcome: ExpectedOutcome,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub verification_evidence: Option<EvidenceId>,
    pub limitations: CanonicalSet<NonEmptyText>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Conformance-harness mappings keyed by stable check identity.
pub struct HarnessMappings {
    pub checks: BTreeMap<CheckId, HarnessCheckMapping>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Immutable result of executing one mapped harness check against one target.
pub struct HarnessCheckResult {
    pub check_id: CheckId,
    pub check_kind: HarnessCheckKind,
    pub harness_revision: CommitSha,
    pub target: TargetDigest,
    pub cli_artifact_digest: Digest,
    pub verified_artifact_digest: Digest,
    pub platform: Platform,
    pub outcome: CheckOutcome,
    /// Assertion identity copied from the mapping, not inferred from process output.
    pub assertion: NonEmptyText,
    /// Exact subject scope claimed by this result.
    pub capability_ids: CanonicalSet<CapabilityId>,
    pub stdout_digest: Digest,
    pub stderr_digest: Digest,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Approved `sdk-sdk` entry point used to execute a conformance scenario.
pub enum HarnessAdapter {
    SdkTarget,
    ModTest,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Behavioural scenario that translates authoritative semantics into observable conformance.
pub struct ConformanceScenario {
    pub scenario_id: ScenarioId,
    pub source_anchors: CanonicalSet<EvidenceReference>,
    pub observable_behavior: CanonicalValue,
    pub capability_ids: CanonicalSet<CapabilityId>,
    pub harness_adapter: HarnessAdapter,
    pub invocation: CommandSpec,
    pub expected_outcome: ExpectedOutcome,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Capability record retained with the target under which its removal was assessed.
pub struct HistoricalCapabilityRecord {
    pub target: TargetDigest,
    pub capability: CapabilityRecord,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Semantic fingerprint change for a capability present in both targets.
pub struct CapabilityChange {
    pub capability_id: CapabilityId,
    pub from_fingerprint: Digest,
    pub to_fingerprint: Digest,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Content change in one pinned authority source.
pub struct AuthorityChange {
    pub authority_id: AuthorityId,
    pub from_source_digest: Digest,
    pub to_source_digest: Digest,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Addition, removal, or semantic change of a mapped harness check.
pub struct HarnessCheckChange {
    pub check_id: CheckId,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub from_fingerprint: Option<Digest>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub to_fingerprint: Option<Digest>,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Minimum release impact implied by an approved target transition.
pub enum SemverEffect {
    None,
    Additive,
    Deprecation,
    Breaking,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Reviewed public-API meaning of one capability-level transition.
///
/// Addition and removal are structural facts checked against the target diff. The remaining
/// variants make a reviewer state whether a fingerprint change is compatible, a deprecation, or
/// incompatible; a hash change alone cannot answer that Rust API question.
pub enum RustApiChangeKind {
    Added,
    Removed,
    Compatible,
    Deprecated,
    Incompatible,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Portable anchor to a migration requirement in an approved specification.
pub struct SpecReference {
    pub path: RepositoryRelativePath,
    pub locator: SourceLocator,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Feature-owned specification reference used for user-facing migration work.
pub struct OwnedSpecReference {
    pub owner_feature: FeatureId,
    pub reference: SpecReference,
}

#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Human review of the Rust API effect represented by one semantic target diff entry.
pub struct RustApiTransitionReview {
    pub capability_id: CapabilityId,
    pub change_kind: RustApiChangeKind,
    pub user_facing: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub experimental_condition: Option<SpecReference>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub migration_requirement: Option<OwnedSpecReference>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Reviewed semantic diff between two immutable completeness targets.
///
/// Removed capabilities retain their historical record so transition validation cannot reinterpret
/// them using the new authority set.
pub struct TargetTransition {
    pub from_target: TargetDigest,
    pub to_target: TargetDescriptor,
    pub added_capabilities: CanonicalSet<CapabilityId>,
    pub removed_capabilities: Vec<HistoricalCapabilityRecord>,
    pub changed_capabilities: CanonicalSet<CapabilityChange>,
    pub authority_changes: CanonicalSet<AuthorityChange>,
    pub harness_changes: CanonicalSet<HarnessCheckChange>,
    pub semver_effect: SemverEffect,
    pub migration_requirements: CanonicalSet<SpecReference>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Inclusive compatibility interval bounded by explicitly assessed targets.
pub struct InclusiveTargetRange {
    pub lower: TargetDigest,
    pub upper: TargetDigest,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
/// Exact or contiguous target set supported by one Rust SDK release.
pub enum SupportedTargets {
    Exact(CanonicalSet<TargetDigest>),
    InclusiveRange(InclusiveTargetRange),
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Evidence-backed compatibility statement for one Rust SDK version.
///
/// Range boundaries must themselves be explicit conformance targets; interpolation between them is
/// an independently validated policy decision, not an implication of semantic version strings.
pub struct CompatibilityClaim {
    pub rust_sdk_version: SemverVersion,
    pub supported_targets: SupportedTargets,
    pub range_boundaries: CanonicalSet<TargetDigest>,
    pub conformance_evidence: CanonicalSet<EvidenceId>,
    pub outside_range_capability: CapabilityId,
    pub claim_digest: Digest,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Release-facing compatibility fields projected from one validated claim.
///
/// This deliberately contains no separately editable range or evidence state. Consumers publish
/// the exact normalized target claim and its digest that passed contract validation.
pub struct ReleaseCompatibilityMetadata {
    pub rust_sdk_version: SemverVersion,
    pub supported_targets: SupportedTargets,
    pub claim_digest: Digest,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Non-blocking capability status whose acceptance is supported by decision evidence.
pub struct CompleteException {
    pub capability_id: CapabilityId,
    pub status: Status,
    pub decision_evidence: CanonicalSet<EvidenceId>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
/// Final integrity and completeness verdict derived from all contract artifacts.
///
/// Counts and sets are materialized for auditability but are not trusted inputs; report validation
/// recomputes them from the canonical inventory and resolved ledger.
pub struct CompletenessReport {
    pub contract_format_version: SemverVersion,
    pub target_descriptor: TargetDescriptor,
    pub inventory_digest: Digest,
    pub ledger_digest: Digest,
    pub integrity_verdict: bool,
    pub completeness_verdict: bool,
    pub counts_by_authority: BTreeMap<AuthorityId, u64>,
    pub counts_by_kind: BTreeMap<CapabilityKind, u64>,
    pub counts_by_status: BTreeMap<Status, u64>,
    pub counts_by_owner: BTreeMap<FeatureId, u64>,
    pub integrity_errors: Vec<ContractDiagnostic>,
    pub blocking_capabilities: CanonicalSet<CapabilityId>,
    pub complete_exceptions: Vec<CompleteException>,
}

#[cfg(test)]
mod tests {
    use pretty_assertions::assert_eq;
    use serde_json::json;

    use super::*;

    #[test]
    fn scalar_types_reject_non_canonical_values() {
        assert!(CommitSha::new("abc").is_err());
        assert!(CommitSha::new("A".repeat(40)).is_err());
        assert!(Digest::new("sha256:abc").is_err());
        assert!(RepositoryRelativePath::new("../target.json").is_err());
        assert!(RepositoryRelativePath::new("C:\\target.json").is_err());
        assert!(SourceLocator::new("/Users/example/source.rs:1").is_err());
        assert!(CapabilityId::new("Behavior/Client").is_err());
        assert!(SemverVersion::new("v1.0.0").is_err());
    }

    #[test]
    fn dagger_versions_have_one_canonical_spelling() {
        let with_prefix = DaggerVersion::new("v1.0.0-beta.9").unwrap();
        let without_prefix = DaggerVersion::new("1.0.0-beta.9").unwrap();

        assert_eq!(with_prefix, without_prefix);
        assert_eq!(
            serde_json::to_value(with_prefix).unwrap(),
            json!("v1.0.0-beta.9")
        );
    }

    #[test]
    fn canonical_sets_sort_and_deduplicate_on_construction_and_decode() {
        let values = CanonicalSet::new([3, 1, 2, 2]);
        assert_eq!(values.as_slice(), &[1, 2, 3]);

        let decoded: CanonicalSet<u8> = serde_json::from_value(json!([3, 1, 3, 2])).unwrap();
        assert_eq!(decoded.as_slice(), &[1, 2, 3]);
    }

    #[test]
    fn policy_enums_use_the_exact_durable_spellings() {
        assert_eq!(
            serde_json::to_value(Status::Missing).unwrap(),
            json!("Missing")
        );
        assert_eq!(
            serde_json::to_value(Status::IdiomaticEquivalent).unwrap(),
            json!("Idiomatic_Equivalent")
        );
        assert_eq!(
            serde_json::to_value(HarnessCheckKind::SubjectConformance).unwrap(),
            json!("subject-conformance")
        );
        assert_eq!(
            serde_json::to_value(Stability::NotApplicable).unwrap(),
            json!("not-applicable")
        );
    }

    #[test]
    fn durable_objects_reject_unknown_fields() {
        let value = json!({
            "contract_format_version": "1.0.0",
            "dagger_repository": "github.com/dagger/dagger",
            "dagger_revision": "25300124ca110612edc09c43f89cb5fad6028170",
            "engine_version": "v1.0.0-beta.9",
            "schema_version": "v1",
            "schema_digest": Digest::sha256("schema"),
            "go_sdk_repository": "github.com/dagger/dagger-go-sdk",
            "go_sdk_revision": "1309520660f6a5b35ef97b4fbe151e32a06a8dc5",
            "sdk_contract_repository": "github.com/dagger/sdk-sdk",
            "sdk_contract_revision": "8c164424b7a8a37b33a77367ef7547490d5b87b5",
            "sdk_contract_cli_version": "v1.0.0-beta.9",
            "rust_sdk_version": "1.0.0-beta.10",
            "rust_edition": "2024",
            "rust_version": "1.97.1",
            "unknown": true
        });

        assert!(serde_json::from_value::<TargetDescriptor>(value).is_err());
        assert!(
            serde_json::from_value::<SourceSelector>(json!({
                "path": {
                    "path": "sdk/go",
                    "unknown": true
                }
            }))
            .is_err()
        );
    }
}
