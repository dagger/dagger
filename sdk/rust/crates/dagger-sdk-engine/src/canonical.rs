//! Canonical JSON encoding and domain-separated engine-operation identities.
//!
//! Wire models are compared across Rust, the Go ABI adapter, engine build metadata,
//! and completeness evidence. This module gives each valid value one byte spelling:
//! lexical object keys, two-space indentation, LF endings, and one terminal newline.

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::{Map, Value};
use sha2::{Digest as _, Sha256};
use thiserror::Error;

use crate::scalar::Sha256Digest;

/// Semantic namespace included in an engine-integration digest.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DigestDomain {
    /// Complete operation request identity.
    OperationRequest,
    /// Published generated-artifact ownership manifest.
    OperationManifest,
    /// Immutable engine source and SDK dependency descriptor.
    EngineSource,
    /// Runtime build input and resulting binary provenance.
    RuntimeProvenance,
    /// Packaged private engine asset manifest.
    PackagedAssets,
}

impl DigestDomain {
    const fn prefix(self) -> &'static [u8] {
        match self {
            Self::OperationRequest => b"dagger-rust-engine-operation-request-v1\0",
            Self::OperationManifest => b"dagger-rust-engine-operation-manifest-v1\0",
            Self::EngineSource => b"dagger-rust-engine-source-v1\0",
            Self::RuntimeProvenance => b"dagger-rust-engine-runtime-provenance-v1\0",
            Self::PackagedAssets => b"dagger-rust-engine-packaged-assets-v1\0",
        }
    }
}

/// Failure to encode, decode, or verify a canonical wire model.
#[derive(Debug, Error)]
pub enum CanonicalError {
    /// A typed value could not be converted to JSON.
    #[error("could not encode canonical JSON")]
    Encode(#[source] serde_json::Error),
    /// JSON did not satisfy the strict typed boundary.
    #[error("could not decode canonical JSON")]
    Decode(#[source] serde_json::Error),
    /// JSON was typed but retained a second byte spelling for the same meaning.
    #[error("document is valid JSON but not in canonical form")]
    NonCanonical,
}

/// Serializes a value using the canonical engine-operation JSON representation.
pub fn canonical_bytes<T: Serialize>(value: &T) -> Result<Vec<u8>, CanonicalError> {
    let mut value = serde_json::to_value(value).map_err(CanonicalError::Encode)?;
    canonicalize_value(&mut value);
    let mut bytes = serde_json::to_vec_pretty(&value).map_err(CanonicalError::Encode)?;
    bytes.push(b'\n');
    Ok(bytes)
}

/// Computes a canonical, domain-separated lowercase SHA-256 identity.
pub fn canonical_digest<T: Serialize>(
    domain: DigestDomain,
    value: &T,
) -> Result<Sha256Digest, CanonicalError> {
    let bytes = canonical_bytes(value)?;
    let mut hasher = Sha256::new();
    // The NUL-terminated versioned prefix prevents identical JSON in different
    // artifact roles from becoming substitutable evidence.
    hasher.update(domain.prefix());
    hasher.update(bytes);
    let hex = hasher
        .finalize()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    Ok(Sha256Digest::new(format!("sha256:{hex}"))
        .expect("SHA-256 formatting always satisfies the validated digest grammar"))
}

/// Decodes bytes only when they already use the canonical representation.
pub fn decode_canonical<T>(bytes: &[u8]) -> Result<T, CanonicalError>
where
    T: DeserializeOwned + Serialize,
{
    let value = serde_json::from_slice(bytes).map_err(CanonicalError::Decode)?;
    if canonical_bytes(&value)? != bytes {
        return Err(CanonicalError::NonCanonical);
    }
    Ok(value)
}

fn canonicalize_value(value: &mut Value) {
    match value {
        Value::Object(object) => {
            let previous = std::mem::take(object);
            let mut entries = previous.into_iter().collect::<Vec<_>>();
            entries.sort_unstable_by(|(left, _), (right, _)| left.cmp(right));

            let mut canonical = Map::new();
            for (key, mut value) in entries {
                canonicalize_value(&mut value);
                canonical.insert(key, value);
            }
            *object = canonical;
        }
        Value::Array(values) => {
            // Array order is semantic. Set-like model fields use BTreeSet before
            // reaching this byte boundary.
            for value in values {
                canonicalize_value(value);
            }
        }
        Value::Null | Value::Bool(_) | Value::Number(_) | Value::String(_) => {}
    }
}
