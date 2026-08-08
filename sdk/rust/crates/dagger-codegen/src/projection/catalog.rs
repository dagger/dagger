//! Semantic binding accounting and domain-separated implementation fingerprints.
//!
//! Catalog entries describe the meaning of generated bindings rather than source
//! spans. Consequently formatting can change an artifact digest without falsely
//! claiming that a binding's implementation contract changed.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};

use crate::schema::canonical::SchemaCoordinate;
use crate::target::CodegenTarget;

/// A lowercase SHA-256 digest of a canonical semantic value.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct SemanticDigest(String);

impl SemanticDigest {
    /// Hashes a serializable value in the code-generator fingerprint domain.
    pub fn for_value<T: Serialize>(value: &T) -> Result<Self, serde_json::Error> {
        let encoded = serde_json::to_vec(value)?;
        let mut hasher = Sha256::new();
        hasher.update(b"dagger-rust-codegen-semantic-v1\0");
        hasher.update(encoded);
        let digest = hasher.finalize();
        let hexadecimal = digest
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        Ok(Self(format!("sha256:{hexadecimal}")))
    }

    /// Borrows the canonical `sha256:<hex>` spelling.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for SemanticDigest {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.0)
    }
}

/// The disposition assigned to one semantic binding.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum CatalogDisposition {
    /// The binding will be emitted into the generated client.
    Emitted,
    /// The binding is intentionally supplied by the handwritten runtime.
    RuntimeProvided,
    /// The coordinate is represented by an explicit metadata policy.
    PolicyRecorded,
}

/// The generated or handwritten role played by a binding.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum BindingKind {
    /// The client-owned query entry point.
    QueryRoot,
    /// A handwritten scalar contract.
    Scalar,
    /// A generated object handle.
    ObjectHandle,
    /// A generated interface trait.
    InterfaceTrait,
    /// A generated interface client handle.
    InterfaceClient,
    /// An object-to-interface implementation.
    InterfaceImplementation,
    /// A generated enum type.
    Enum,
    /// A canonical enum variant.
    EnumVariant,
    /// An enum Wire_Name accepted as an alias.
    EnumAlias,
    /// A generated input-object type.
    InputObject,
    /// A generated input-object field.
    InputField,
    /// A target-private named type retained as a no-symbol policy.
    TargetPrivateType,
    /// A target-private field retained as a no-symbol policy.
    TargetPrivateField,
    /// A generated field operation.
    FieldOperation,
    /// A generated field argument.
    Argument,
    /// A directive definition policy.
    DirectivePolicy,
    /// A validated directive-definition argument.
    DirectiveArgument,
}

/// The authority domain required to verify a binding.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum EvidenceScope {
    /// The pinned engine schema defines the wire coordinate and shape.
    EngineSchema,
    /// The pinned Go SDK defines observable compatibility behaviour.
    GoSdk,
    /// Rust policy defines the public language shape.
    RustPolicy,
    /// The handwritten Rust runtime supplies execution behaviour.
    RustRuntime,
}

/// A stable key that cannot fall back to a name-only compatibility match.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct BindingKey {
    /// Exact authoritative schema coordinate, when the binding is schema-owned.
    pub wire_coordinate: Option<SchemaCoordinate>,
    /// Exact generated public symbol path, when the binding owns one.
    pub rust_symbol: Option<String>,
    /// Semantic role that distinguishes multiple bindings for one coordinate.
    pub binding_kind: BindingKind,
}

/// One complete semantic projection decision.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct BindingDescriptor {
    /// Stable binding identity.
    pub key: BindingKey,
    /// Whether source, runtime support, or metadata owns the result.
    pub disposition: CatalogDisposition,
    /// Rust signature or policy shape used by the eventual renderer.
    pub rust_signature: String,
    /// Recursive wrappers, arguments, directives, and execution strategy as JSON.
    pub semantic_shape: serde_json::Value,
    /// Fingerprint of the complete semantic descriptor excluding this field.
    pub implementation_fingerprint: SemanticDigest,
    /// Evidence domains required before completeness may claim this binding.
    pub required_evidence: BTreeSet<EvidenceScope>,
}

#[derive(Serialize)]
struct FingerprintInput<'a> {
    key: &'a BindingKey,
    disposition: CatalogDisposition,
    rust_signature: &'a str,
    semantic_shape: &'a serde_json::Value,
    required_evidence: &'a BTreeSet<EvidenceScope>,
}

impl BindingDescriptor {
    /// Constructs a descriptor and fingerprints every semantic field atomically.
    pub fn new(
        key: BindingKey,
        disposition: CatalogDisposition,
        rust_signature: String,
        semantic_shape: serde_json::Value,
        required_evidence: BTreeSet<EvidenceScope>,
    ) -> Result<Self, serde_json::Error> {
        let implementation_fingerprint = SemanticDigest::for_value(&FingerprintInput {
            key: &key,
            disposition,
            rust_signature: &rust_signature,
            semantic_shape: &semantic_shape,
            required_evidence: &required_evidence,
        })?;
        Ok(Self {
            key,
            disposition,
            rust_signature,
            semantic_shape,
            implementation_fingerprint,
            required_evidence,
        })
    }
}

/// Exhaustive semantic bindings in deterministic key order.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct ProjectionCatalog {
    target: CodegenTarget,
    bindings: BTreeMap<BindingKey, BindingDescriptor>,
}

impl ProjectionCatalog {
    /// Creates a catalog after rejecting duplicate semantic keys.
    pub(crate) fn from_bindings(
        target: CodegenTarget,
        bindings: impl IntoIterator<Item = BindingDescriptor>,
    ) -> Result<Self, BindingKey> {
        let mut ordered = BTreeMap::new();
        for binding in bindings {
            let key = binding.key.clone();
            if ordered.insert(key.clone(), binding).is_some() {
                return Err(key);
            }
        }
        Ok(Self {
            target,
            bindings: ordered,
        })
    }

    /// Returns the exact target identity that gates every catalog binding.
    #[must_use]
    pub const fn target(&self) -> &CodegenTarget {
        &self.target
    }

    /// Borrows descriptors in stable semantic-key order.
    #[must_use]
    pub const fn bindings(&self) -> &BTreeMap<BindingKey, BindingDescriptor> {
        &self.bindings
    }
}

/// Compatibility entry used by shared test strategies established before projection.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct CatalogEntry {
    /// Stable schema identity of the accounted element.
    pub schema_id: String,
    /// Projection decision for the element.
    pub disposition: CatalogDisposition,
    /// Stable reason explaining non-emission or special handling.
    pub reason: Option<String>,
}
