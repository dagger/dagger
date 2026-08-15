//! Exact code-generation target identity and schema digest verification.
//!
//! The generator accepts one reviewed descriptor. Keeping construction private makes
//! it impossible for a caller to pair arbitrary schema bytes with an invented target.

use std::fmt;

use semver::Version;
use serde::ser::SerializeStruct;
use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};

/// Maximum accepted canonical target descriptor size.
pub const MAX_TARGET_BYTES: usize = 16 * 1024;

/// Maximum accepted introspection snapshot size.
pub const MAX_SCHEMA_BYTES: usize = 8 * 1024 * 1024;

// These anchors turn target movement into an explicit reviewed generator change. A
// structurally valid descriptor is insufficient when it names a different authority.
const APPROVED_CONTRACT_FORMAT_VERSION: &str = "1.0.0";
const APPROVED_DAGGER_REPOSITORY: &str = "github.com/dagger/dagger";
const APPROVED_DAGGER_REVISION: &str = "501b57e0476dee5881b99a064c3c04173134ecc7";
const APPROVED_ENGINE_VERSION: &str = "v1.0.0-beta.11.rust.1";
const APPROVED_RUST_EDITION: &str = "2024";
const APPROVED_RUST_SDK_VERSION: &str = "1.0.0-beta.11.rust.1";
const APPROVED_RUST_VERSION: &str = "1.97.1";
const APPROVED_SCHEMA_DIGEST: &str =
    "sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306";
const APPROVED_SCHEMA_VERSION: &str = "v1.0.0";

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct TargetDescriptor {
    contract_format_version: String,
    dagger_repository: String,
    dagger_revision: String,
    engine_version: String,
    rust_edition: String,
    rust_sdk_version: String,
    rust_version: String,
    schema_digest: String,
    schema_version: String,
}

/// A validated lowercase Git object identifier.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct Revision(String);

impl Revision {
    /// Borrows the canonical hexadecimal revision.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// A validated SHA-256 digest.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub struct Sha256Digest([u8; 32]);

impl Sha256Digest {
    /// Returns the digest bytes.
    #[must_use]
    pub const fn as_bytes(&self) -> &[u8; 32] {
        &self.0
    }
}

impl fmt::Display for Sha256Digest {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("sha256:")?;
        for byte in self.0 {
            write!(formatter, "{byte:02x}")?;
        }
        Ok(())
    }
}

/// The Rust language edition selected by the reviewed target.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub enum RustEdition {
    /// Rust edition 2024.
    Edition2024,
}

impl RustEdition {
    /// Returns the Cargo spelling of this edition.
    #[must_use]
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Edition2024 => "2024",
        }
    }
}

/// All immutable identities needed to reproduce the approved generated SDK.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CodegenTarget {
    contract_format_version: Version,
    dagger_repository: String,
    dagger_revision: Revision,
    engine_version: Version,
    rust_edition: RustEdition,
    rust_sdk_version: Version,
    rust_version: Version,
    schema_digest: Sha256Digest,
    schema_version: Version,
}

impl Serialize for CodegenTarget {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        let mut state = serializer.serialize_struct("CodegenTarget", 9)?;
        state.serialize_field(
            "contract_format_version",
            &self.contract_format_version.to_string(),
        )?;
        state.serialize_field("dagger_repository", &self.dagger_repository)?;
        state.serialize_field("dagger_revision", self.dagger_revision.as_str())?;
        state.serialize_field("engine_version", &format!("v{}", self.engine_version))?;
        state.serialize_field("rust_edition", self.rust_edition.as_str())?;
        state.serialize_field("rust_sdk_version", &self.rust_sdk_version.to_string())?;
        state.serialize_field("rust_version", &self.rust_version.to_string())?;
        state.serialize_field("schema_digest", &self.schema_digest.to_string())?;
        state.serialize_field("schema_version", &format!("v{}", self.schema_version))?;
        state.end()
    }
}

impl CodegenTarget {
    /// Decodes and validates the repository's one approved target descriptor.
    pub fn decode_exact(bytes: &[u8]) -> Result<Self, DiagnosticSet> {
        if bytes.len() > MAX_TARGET_BYTES {
            return Err(target_error("target descriptor exceeds its 16 KiB bound"));
        }
        let descriptor: TargetDescriptor = serde_json::from_slice(bytes)
            .map_err(|_| target_error("target descriptor is not canonical target JSON"))?;

        let expected = [
            (
                "contract_format_version",
                descriptor.contract_format_version.as_str(),
                APPROVED_CONTRACT_FORMAT_VERSION,
            ),
            (
                "dagger_repository",
                descriptor.dagger_repository.as_str(),
                APPROVED_DAGGER_REPOSITORY,
            ),
            (
                "dagger_revision",
                descriptor.dagger_revision.as_str(),
                APPROVED_DAGGER_REVISION,
            ),
            (
                "engine_version",
                descriptor.engine_version.as_str(),
                APPROVED_ENGINE_VERSION,
            ),
            (
                "rust_edition",
                descriptor.rust_edition.as_str(),
                APPROVED_RUST_EDITION,
            ),
            (
                "rust_sdk_version",
                descriptor.rust_sdk_version.as_str(),
                APPROVED_RUST_SDK_VERSION,
            ),
            (
                "rust_version",
                descriptor.rust_version.as_str(),
                APPROVED_RUST_VERSION,
            ),
            (
                "schema_digest",
                descriptor.schema_digest.as_str(),
                APPROVED_SCHEMA_DIGEST,
            ),
            (
                "schema_version",
                descriptor.schema_version.as_str(),
                APPROVED_SCHEMA_VERSION,
            ),
        ];
        let diagnostics: Vec<_> = expected
            .into_iter()
            .filter(|(_, actual, approved)| actual != approved)
            .map(|(field, _, _)| {
                Diagnostic::new(
                    DiagnosticCode::TargetIdentityInvalid,
                    Some(DiagnosticCoordinate::new(format!("target.{field}"))),
                    "value differs from the reviewed exact target",
                )
            })
            .collect();
        if let Some(diagnostics) = DiagnosticSet::new(diagnostics) {
            return Err(diagnostics);
        }

        let dagger_revision = parse_revision("dagger_revision", &descriptor.dagger_revision)?;
        let schema_digest = parse_digest(&descriptor.schema_digest)?;

        Ok(Self {
            contract_format_version: parse_version(
                "contract_format_version",
                &descriptor.contract_format_version,
            )?,
            dagger_repository: descriptor.dagger_repository,
            dagger_revision,
            engine_version: parse_version("engine_version", &descriptor.engine_version)?,
            rust_edition: RustEdition::Edition2024,
            rust_sdk_version: parse_version("rust_sdk_version", &descriptor.rust_sdk_version)?,
            rust_version: parse_version("rust_version", &descriptor.rust_version)?,
            schema_digest,
            schema_version: parse_version("schema_version", &descriptor.schema_version)?,
        })
    }

    /// Verifies bounded schema bytes against the target digest.
    pub fn verify_schema(&self, bytes: &[u8]) -> Result<(), DiagnosticSet> {
        if bytes.len() > MAX_SCHEMA_BYTES {
            return Err(DiagnosticSet::one(Diagnostic::new(
                DiagnosticCode::SchemaRootInvalid,
                Some(DiagnosticCoordinate::new("schema")),
                "schema snapshot exceeds its 8 MiB bound",
            )));
        }
        let actual = Sha256Digest(Sha256::digest(bytes).into());
        if actual != self.schema_digest {
            return Err(DiagnosticSet::one(Diagnostic::new(
                DiagnosticCode::SchemaDigestMismatch,
                Some(DiagnosticCoordinate::new("target.schema_digest")),
                "schema bytes do not match the reviewed target digest",
            )));
        }
        Ok(())
    }

    /// Returns the Dagger source revision.
    #[must_use]
    pub const fn dagger_revision(&self) -> &Revision {
        &self.dagger_revision
    }

    /// Returns the engine/API version.
    #[must_use]
    pub const fn engine_version(&self) -> &Version {
        &self.engine_version
    }

    /// Returns the checked schema version.
    #[must_use]
    pub const fn schema_version(&self) -> &Version {
        &self.schema_version
    }

    /// Returns the checked schema digest.
    #[must_use]
    pub const fn schema_digest(&self) -> Sha256Digest {
        self.schema_digest
    }

    /// Returns the Rust SDK package version.
    #[must_use]
    pub const fn rust_sdk_version(&self) -> &Version {
        &self.rust_sdk_version
    }

    /// Returns the selected Rust language edition.
    #[must_use]
    pub const fn rust_edition(&self) -> RustEdition {
        self.rust_edition
    }

    /// Returns the selected Rust toolchain version.
    #[must_use]
    pub const fn rust_version(&self) -> &Version {
        &self.rust_version
    }

    /// Returns the target contract format version.
    #[must_use]
    pub const fn contract_format_version(&self) -> &Version {
        &self.contract_format_version
    }

    /// Returns the Dagger repository identity.
    #[must_use]
    pub fn dagger_repository(&self) -> &str {
        &self.dagger_repository
    }
}

fn parse_revision(field: &str, source: &str) -> Result<Revision, DiagnosticSet> {
    let valid = source.len() == 40
        && source
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte));
    if valid {
        Ok(Revision(source.to_owned()))
    } else {
        Err(target_field_error(
            field,
            "revision must be 40 lowercase hexadecimal digits",
        ))
    }
}

fn parse_version(field: &str, source: &str) -> Result<Version, DiagnosticSet> {
    let normalized = match source.strip_prefix('v') {
        Some(version) => version,
        None => source,
    };
    Version::parse(normalized)
        .map_err(|_| target_field_error(field, "version must be a valid semantic version"))
}

fn parse_digest(source: &str) -> Result<Sha256Digest, DiagnosticSet> {
    let Some(hexadecimal) = source.strip_prefix("sha256:") else {
        return Err(target_field_error(
            "schema_digest",
            "digest must use the sha256 prefix",
        ));
    };
    if hexadecimal.len() != 64 {
        return Err(target_field_error(
            "schema_digest",
            "SHA-256 digest must contain 64 lowercase hexadecimal digits",
        ));
    }
    let mut bytes = [0_u8; 32];
    for (index, pair) in hexadecimal.as_bytes().chunks_exact(2).enumerate() {
        let high = decode_hex(pair[0]);
        let low = decode_hex(pair[1]);
        let (Some(high), Some(low)) = (high, low) else {
            return Err(target_field_error(
                "schema_digest",
                "SHA-256 digest must contain 64 lowercase hexadecimal digits",
            ));
        };
        bytes[index] = (high << 4) | low;
    }
    Ok(Sha256Digest(bytes))
}

const fn decode_hex(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        _ => None,
    }
}

fn target_error(message: &str) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        DiagnosticCode::TargetIdentityInvalid,
        Some(DiagnosticCoordinate::new("target")),
        message,
    ))
}

fn target_field_error(field: &str, message: &str) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        DiagnosticCode::TargetIdentityInvalid,
        Some(DiagnosticCoordinate::new(format!("target.{field}"))),
        message,
    ))
}
