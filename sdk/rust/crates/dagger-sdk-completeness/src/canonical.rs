//! Canonical JSON encoding and domain-separated artifact digests.
//!
//! Contract artifacts are reviewed, cached, and compared by digest across machines and
//! implementations. This module therefore owns the byte-level representation: object keys are
//! lexical, indentation is two spaces, line endings are LF, and every document ends with exactly
//! one newline. Collection semantics remain the model's responsibility; unordered collections
//! must use [`crate::model::CanonicalSet`] rather than relying on this encoder to reorder arrays.

use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::{Map, Value};
use sha2::{Digest as _, Sha256};
use thiserror::Error;

use crate::model::Digest;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
/// Semantic namespace included in a completeness-contract digest.
///
/// Domain separation prevents identical JSON in different artifact roles from sharing an
/// identity and later being substituted for one another.
pub enum DigestDomain {
    Target,
    Source,
    Capability,
    Artifact,
    RuleExpansion,
    Compatibility,
    ModuleAuthoring,
    ClientGeneration,
    ConformanceScope,
    ConformanceApplicabilityReview,
    ConformanceApplicabilityRecords,
    ConformanceAssertionCatalog,
    ConformanceFixtureRegistry,
    ConformanceCaseCatalog,
    ConformanceClosureBundle,
    ConformanceHostProfile,
    ConformanceHostPlan,
    ConformanceHostRecord,
    ConformancePlatformMatrix,
    ConformanceSecurity,
    ConformancePolicy,
}

impl DigestDomain {
    const fn prefix(self) -> &'static [u8] {
        match self {
            Self::Target => b"dagger-rust-sdk-target-v1\0",
            Self::Source => b"dagger-rust-sdk-source-v1\0",
            Self::Capability => b"dagger-rust-sdk-capability-v1\0",
            Self::Artifact => b"dagger-rust-sdk-artifact-v1\0",
            Self::RuleExpansion => b"dagger-rust-sdk-rule-expansion-v1\0",
            Self::Compatibility => b"dagger-rust-sdk-compatibility-v1\0",
            Self::ModuleAuthoring => b"dagger-rust-sdk-module-authoring-v1\0",
            Self::ClientGeneration => b"dagger-rust-sdk-client-generation-v1\0",
            Self::ConformanceScope => b"dagger-rust-sdk-conformance-scope-v1\0",
            Self::ConformanceApplicabilityReview => {
                b"dagger-rust-sdk-conformance-applicability-review-v1\0"
            }
            Self::ConformanceApplicabilityRecords => {
                b"dagger-rust-sdk-conformance-applicability-records-v1\0"
            }
            Self::ConformanceAssertionCatalog => {
                b"dagger-rust-sdk-conformance-assertion-catalog-v1\0"
            }
            Self::ConformanceFixtureRegistry => {
                b"dagger-rust-sdk-conformance-fixture-registry-v1\0"
            }
            Self::ConformanceCaseCatalog => b"dagger-rust-sdk-conformance-case-catalog-v1\0",
            Self::ConformanceClosureBundle => b"dagger-rust-sdk-conformance-closure-bundle-v1\0",
            Self::ConformanceHostProfile => b"dagger-rust-sdk-signoff-host-profile-v1\0",
            Self::ConformanceHostPlan => b"dagger-rust-sdk-signoff-host-plan-v1\0",
            Self::ConformanceHostRecord => b"dagger-rust-sdk-signoff-host-record-v1\0",
            Self::ConformancePlatformMatrix => b"dagger-rust-sdk-conformance-platform-matrix-v1\0",
            Self::ConformanceSecurity => b"dagger-rust-sdk-conformance-security-v1\0",
            Self::ConformancePolicy => b"dagger-rust-sdk-conformance-policy-v1\0",
        }
    }
}

#[derive(Debug, Error)]
/// Failure to encode, decode, or verify a canonical contract artifact.
pub enum CanonicalError {
    #[error("could not encode canonical JSON")]
    Encode(#[source] serde_json::Error),
    #[error("could not decode canonical JSON")]
    Decode(#[source] serde_json::Error),
    #[error("artifact is valid JSON but not in canonical form")]
    NonCanonical,
}

/// Serializes a value using the completeness contract's canonical JSON representation.
///
/// This function sorts object keys recursively, but deliberately preserves array order. Ordered
/// arrays such as command arguments carry meaning; set-like arrays must be represented by
/// [`crate::model::CanonicalSet`].
pub fn canonical_bytes<T: Serialize>(value: &T) -> Result<Vec<u8>, CanonicalError> {
    let mut value = serde_json::to_value(value).map_err(CanonicalError::Encode)?;
    canonicalize_value(&mut value);

    let mut bytes = serde_json::to_vec_pretty(&value).map_err(CanonicalError::Encode)?;
    bytes.push(b'\n');
    Ok(bytes)
}

/// Computes the canonical SHA-256 identity of `value` in `domain`.
///
/// The domain prefix is part of the hash input but not the serialized artifact.
pub fn canonical_digest<T: Serialize>(
    domain: DigestDomain,
    value: &T,
) -> Result<Digest, CanonicalError> {
    let bytes = canonical_bytes(value)?;
    let mut hasher = Sha256::new();
    // A NUL-terminated, versioned prefix makes cross-kind substitution impossible without
    // coupling the durable JSON schema to an artificial discriminator field.
    hasher.update(domain.prefix());
    hasher.update(bytes);
    Ok(Digest::from_sha256_output(hasher.finalize().into()))
}

/// Decodes `bytes` only when they already use the canonical representation.
///
/// Accepting semantically equivalent non-canonical JSON here would allow a single artifact to
/// acquire multiple byte identities, undermining digest comparisons and reproducible diffs.
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
            // Array order may be semantic (for example command arguments). Model unordered data
            // with CanonicalSet so it is normalized before reaching this byte-level encoder.
            for value in values {
                canonicalize_value(value);
            }
        }
        Value::Null | Value::Bool(_) | Value::Number(_) | Value::String(_) => {}
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use pretty_assertions::assert_eq;
    use serde::{Deserialize, Serialize};

    use super::*;
    use crate::model::CanonicalSet;

    #[derive(Debug, Deserialize, Eq, PartialEq, Serialize)]
    #[serde(deny_unknown_fields)]
    struct Fixture {
        name: String,
        values: CanonicalSet<String>,
        metadata: BTreeMap<String, u64>,
    }

    #[test]
    fn canonical_json_has_sorted_keys_two_spaces_and_one_newline() {
        let fixture = Fixture {
            name: "fixture".to_owned(),
            values: CanonicalSet::new(["z".to_owned(), "a".to_owned()]),
            metadata: BTreeMap::from([("z".to_owned(), 2), ("a".to_owned(), 1)]),
        };

        assert_eq!(
            String::from_utf8(canonical_bytes(&fixture).unwrap()).unwrap(),
            concat!(
                "{\n",
                "  \"metadata\": {\n",
                "    \"a\": 1,\n",
                "    \"z\": 2\n",
                "  },\n",
                "  \"name\": \"fixture\",\n",
                "  \"values\": [\n",
                "    \"a\",\n",
                "    \"z\"\n",
                "  ]\n",
                "}\n"
            )
        );
    }

    #[test]
    fn digest_domains_do_not_alias() {
        let value = "same bytes";
        let target = canonical_digest(DigestDomain::Target, &value).unwrap();
        let source = canonical_digest(DigestDomain::Source, &value).unwrap();

        assert_eq!(
            target.as_str(),
            "sha256:13ea469e81d1f764503933939dcc4833cc6b3d47b1cc1cd4399ed1e311739fd5"
        );
        assert_ne!(target, source);
    }

    #[test]
    fn decode_requires_canonical_bytes() {
        let fixture = Fixture {
            name: "fixture".to_owned(),
            values: CanonicalSet::new(["a".to_owned()]),
            metadata: BTreeMap::new(),
        };
        let canonical = canonical_bytes(&fixture).unwrap();

        assert_eq!(decode_canonical::<Fixture>(&canonical).unwrap(), fixture);
        assert!(matches!(
            decode_canonical::<Fixture>(br#"{"name":"fixture","values":["a"],"metadata":{}}"#),
            Err(CanonicalError::NonCanonical)
        ));
    }
}
