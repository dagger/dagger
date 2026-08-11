//! Canonical JSON and domain-separated identities for module-authoring artifacts.

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::{Map, Value};
use sha2::{Digest as _, Sha256};
use thiserror::Error;

use super::model::Sha256Digest;

/// Semantic namespace included in a module-authoring digest.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DigestDomain {
    /// Immutable selected-package source snapshot.
    SourceSnapshot,
    /// Complete canonical module descriptor.
    ModuleDescriptor,
    /// Registration projection derived from a descriptor.
    Registration,
    /// Introspection projection derived from a descriptor.
    Introspection,
    /// Complete generated-asset ownership manifest.
    GeneratedAssets,
    /// One module call envelope.
    CallEnvelope,
    /// One completeness scope or evidence observation.
    Evidence,
}

impl DigestDomain {
    const fn prefix(self) -> &'static [u8] {
        match self {
            Self::SourceSnapshot => b"dagger-rust-module-source-snapshot-v1\0",
            Self::ModuleDescriptor => b"dagger-rust-module-descriptor-v1\0",
            Self::Registration => b"dagger-rust-module-registration-v1\0",
            Self::Introspection => b"dagger-rust-module-introspection-v1\0",
            Self::GeneratedAssets => b"dagger-rust-module-generated-assets-v1\0",
            Self::CallEnvelope => b"dagger-rust-module-call-envelope-v1\0",
            Self::Evidence => b"dagger-rust-module-evidence-v1\0",
        }
    }
}

/// Failure to encode, decode, or validate canonical module JSON.
#[derive(Debug, Error)]
pub enum CanonicalError {
    /// A typed value could not be converted to JSON.
    #[error("could not encode canonical module JSON")]
    Encode(#[source] serde_json::Error),
    /// JSON did not satisfy the strict typed boundary.
    #[error("could not decode canonical module JSON")]
    Decode(#[source] serde_json::Error),
    /// JSON retained a second byte spelling for the same typed value.
    #[error("module document is not canonical JSON")]
    NonCanonical,
}

/// Serializes a value with lexical object keys, two-space indentation, and one LF.
pub fn canonical_bytes<T: Serialize>(value: &T) -> Result<Vec<u8>, CanonicalError> {
    let mut value = serde_json::to_value(value).map_err(CanonicalError::Encode)?;
    canonicalize_value(&mut value);
    let mut bytes = serde_json::to_vec_pretty(&value).map_err(CanonicalError::Encode)?;
    bytes.push(b'\n');
    Ok(bytes)
}

/// Computes a lowercase SHA-256 identity in one explicit semantic domain.
pub fn canonical_digest<T: Serialize>(
    domain: DigestDomain,
    value: &T,
) -> Result<Sha256Digest, CanonicalError> {
    let mut hasher = Sha256::new();
    // Versioned NUL-terminated domains prevent identical JSON from being substituted
    // across descriptor, call, asset, and evidence roles.
    hasher.update(domain.prefix());
    hasher.update(canonical_bytes(value)?);
    Ok(Sha256Digest::from_bytes(hasher.finalize().into())
        .expect("SHA-256 formatting always satisfies the validated digest grammar"))
}

/// Decodes a typed value only when its bytes already use the canonical representation.
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
            // Array order remains semantic. Set-like fields use BTreeSet before this
            // byte boundary rather than being silently reordered here.
            for value in values {
                canonicalize_value(value);
            }
        }
        Value::Null | Value::Bool(_) | Value::Number(_) | Value::String(_) => {}
    }
}
