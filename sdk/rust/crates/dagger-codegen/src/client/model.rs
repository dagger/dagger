//! Canonical data-only records for standalone-client projection.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

use serde::{Deserialize, Deserializer, Serialize, Serializer};

use crate::directive::DirectiveProjection;
use crate::projection::catalog::{BindingDescriptor, BindingKey, SemanticDigest};
use crate::projection::fields::FieldProjection;
use crate::projection::types::{InterfaceImplementationProjection, TypeProjection};
use crate::schema::canonical::{CanonicalSchema, SchemaCoordinate, SchemaName};
use crate::target::CodegenTarget;

macro_rules! validated_client_string {
    ($name:ident, $doc:literal, $validator:ident) => {
        #[doc = $doc]
        #[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
        pub struct $name(Box<str>);

        impl $name {
            /// Validates and constructs the canonical identity.
            pub fn new(value: impl Into<String>) -> Result<Self, String> {
                let value = value.into();
                $validator(&value)?;
                Ok(Self(value.into_boxed_str()))
            }

            /// Borrows the canonical spelling.
            #[must_use]
            pub fn as_str(&self) -> &str {
                &self.0
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(self.as_str())
            }
        }

        impl std::str::FromStr for $name {
            type Err = String;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                Self::new(value)
            }
        }

        impl AsRef<str> for $name {
            fn as_ref(&self) -> &str {
                self.as_str()
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
                Self::new(value).map_err(serde::de::Error::custom)
            }
        }
    };
}

fn validate_cargo_package(value: &str) -> Result<(), String> {
    if value.is_empty()
        || value.len() > 64
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
    {
        return Err("Cargo package name must be 1-64 ASCII identifier bytes".to_owned());
    }
    validate_rust_identifier(&value.replace('-', "_"))
        .map_err(|_| "Cargo package name must normalize to a Rust identifier".to_owned())
}

fn validate_rust_identifier(value: &str) -> Result<(), String> {
    let (raw, spelling) = value
        .strip_prefix("r#")
        .map_or((false, value), |spelling| (true, spelling));
    let mut bytes = spelling.bytes();
    let valid_shape = value.len() <= 66
        && bytes
            .next()
            .is_some_and(|first| first == b'_' || first.is_ascii_alphabetic())
        && bytes.all(|byte| byte == b'_' || byte.is_ascii_alphanumeric());
    if !valid_shape {
        return Err("Rust identifier must be 1-64 ASCII identifier bytes".to_owned());
    }
    if matches!(
        spelling,
        "Self"
            | "as"
            | "async"
            | "await"
            | "become"
            | "box"
            | "break"
            | "const"
            | "continue"
            | "crate"
            | "do"
            | "dyn"
            | "else"
            | "enum"
            | "extern"
            | "false"
            | "final"
            | "fn"
            | "for"
            | "gen"
            | "if"
            | "impl"
            | "in"
            | "let"
            | "loop"
            | "macro"
            | "match"
            | "mod"
            | "move"
            | "override"
            | "priv"
            | "pub"
            | "ref"
            | "return"
            | "self"
            | "static"
            | "struct"
            | "super"
            | "trait"
            | "true"
            | "try"
            | "type"
            | "typeof"
            | "union"
            | "unsafe"
            | "unsized"
            | "use"
            | "virtual"
            | "where"
            | "while"
            | "yield"
    ) {
        if raw && !matches!(spelling, "Self" | "crate" | "self" | "super") {
            return Ok(());
        }
        return Err("Rust identifier must not be a reserved path keyword".to_owned());
    }
    if raw {
        return Err("raw Rust identifier must escape a reserved keyword".to_owned());
    }
    Ok(())
}

validated_client_string!(
    CargoPackageName,
    "A Cargo package name whose normalized spelling is a valid Rust crate identifier.",
    validate_cargo_package
);
validated_client_string!(
    RustIdentifier,
    "A bounded ASCII Rust identifier, including `r#keyword` when required.",
    validate_rust_identifier
);

/// Semantic role of one generated identifier in the selected-module namespace.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientNameRole {
    /// Generated transparent custom-scalar wrapper.
    CustomScalar,
    /// Generated object handle.
    Object,
    /// Generated interface trait.
    InterfaceTrait,
    /// Generated interface client handle.
    InterfaceClient,
    /// Generated enum.
    Enum,
    /// Generated input object.
    InputObject,
    /// Generated field-options type.
    Options,
}

/// Stable coordinate/role key for one planned generated identifier.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientNameKey {
    /// Schema coordinate whose public API owns the identifier.
    pub coordinate: SchemaCoordinate,
    /// Namespace role that distinguishes multiple identifiers for one coordinate.
    pub role: ClientNameRole,
}

/// Complete collision-checked public naming decision for one selected module.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientNamePlan {
    /// Exact selected Query field Wire_Name.
    pub module_wire_name: SchemaName,
    /// Public module namespace beneath `dagger_client`.
    pub namespace: RustIdentifier,
    /// Local extension trait implemented for the shared SDK client and query builder.
    pub extension_trait: RustIdentifier,
    /// Namespaced root handle, always `Client`.
    pub root_type: RustIdentifier,
    /// Every type-level generated identifier in stable coordinate/role order.
    pub bindings: BTreeMap<ClientNameKey, RustIdentifier>,
}

impl ClientNamePlan {
    /// Returns one planned identifier without falling back to a name-only lookup.
    #[must_use]
    pub fn get(
        &self,
        coordinate: &SchemaCoordinate,
        role: ClientNameRole,
    ) -> Option<&RustIdentifier> {
        self.bindings.get(&ClientNameKey {
            coordinate: coordinate.clone(),
            role,
        })
    }
}

/// Cargo identity selected before generated source is rendered.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientProjectIdentity {
    /// Exact Cargo package spelling retained in the project manifest.
    pub package_name: CargoPackageName,
    /// Rust crate spelling used by generated examples and documentation.
    pub crate_name: RustIdentifier,
}

/// The single namespaced module root installed on the canonical Core query type.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleRoot {
    /// Exact `Query.<module>` field coordinate.
    pub field_coordinate: SchemaCoordinate,
    /// Case-sensitive GraphQL field name selected by generated queries.
    pub field_wire_name: SchemaName,
    /// Case-sensitive GraphQL object name returned by the root field.
    pub object_wire_name: SchemaName,
    /// Exact root-object coordinate in the client-visible schema.
    pub object_coordinate: SchemaCoordinate,
}

/// Collision-checked public namespace roles emitted for one bound module.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClientNamespaceRecord {
    /// Snake-case module namespace below `dagger_client`.
    pub namespace: RustIdentifier,
    /// Public local extension trait implemented for the shared Core client values.
    pub extension_trait: RustIdentifier,
    /// Namespaced generated root type; this is normally `Client`.
    pub root_type: RustIdentifier,
}

/// Complete non-Core closure reachable from the selected module root.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModuleSurfacePlan {
    /// The sole root through which the module surface is reachable.
    pub root: ModuleRoot,
    /// Every and only selected-module coordinate reachable from that root.
    pub closure: BTreeSet<SchemaCoordinate>,
    /// Public namespace roles planned as one collision domain.
    pub namespace: ClientNamespaceRecord,
}

/// Observable surface supplied to a standalone client renderer.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(
    tag = "surface",
    content = "module",
    rename_all = "kebab-case",
    deny_unknown_fields
)]
pub enum ClientSchemaSurface {
    /// Complete Core bindings with no observable module runtime root.
    CoreOnly,
    /// Complete Core bindings plus exactly one selected-module closure.
    BoundModule(ModuleSurfacePlan),
}

/// Exact checked Core binding reused from the public `dagger-sdk` catalog.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct CoreBindingReference {
    /// Original checked-Core semantic key.
    pub key: BindingKey,
    /// Public `dagger_sdk` path or primitive spelling supplied by the checked SDK.
    pub public_path: Option<String>,
    /// Exact checked-Core implementation fingerprint.
    pub implementation_fingerprint: SemanticDigest,
}

/// Ownership domain of one standalone-client catalog binding.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ClientBindingSource {
    /// Reused by identity from the checked public SDK.
    CoreSdk,
    /// Emitted into the selected module namespace.
    SelectedModule,
}

/// One semantic binding in the standalone-client catalog.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct ClientBindingDescriptor {
    /// Whether the checked SDK or the generated module subtree owns the binding.
    pub source: ClientBindingSource,
    /// Complete semantic descriptor and implementation fingerprint.
    pub binding: BindingDescriptor,
}

/// Exhaustive catalog paired with one rendered standalone client.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct ClientBindingCatalog {
    /// Exact target owning both Core and selected-module bindings.
    pub target: CodegenTarget,
    /// Semantic identity of the complete visible schema.
    pub visible_schema_digest: SemanticDigest,
    /// Every checked Core binding in its original catalog order.
    pub core: Vec<CoreBindingReference>,
    /// Every selected-module binding in generated public-path order.
    pub generated: Vec<ClientBindingDescriptor>,
    /// Domain-separated identity of all preceding catalog fields.
    pub digest: SemanticDigest,
}

/// Complete pure compiler result consumed by the standalone-client renderer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientBindingPlan {
    /// Exact checked target.
    pub target: CodegenTarget,
    /// Semantic identity of the complete visible schema.
    pub visible_schema_digest: SemanticDigest,
    /// Complete canonical schema retained for documentation and exact field rendering.
    pub schema: CanonicalSchema,
    /// Checked directive policies retained for public stability documentation.
    pub directives: DirectiveProjection,
    /// Cargo/crate identity selected before rendering.
    pub project: ClientProjectIdentity,
    /// Observable Core-only or Core-plus-module schema surface.
    pub surface: ClientSchemaSurface,
    /// Exact checked Core catalog reused by identity.
    pub core_bindings: BTreeMap<BindingKey, CoreBindingReference>,
    /// Collision-checked selected-module names, absent for Core-only clients.
    pub names: Option<ClientNamePlan>,
    /// Selected-module type projections in Wire_Name order.
    pub module_types: BTreeMap<SchemaName, TypeProjection>,
    /// Selected-module field projections in coordinate order.
    pub module_fields: BTreeMap<SchemaCoordinate, FieldProjection>,
    /// Selected-module interface edges in canonical order.
    pub module_implementations: Vec<InterfaceImplementationProjection>,
    /// Re-keyed module bindings whose paths belong to this generated project.
    pub generated_bindings: BTreeMap<BindingKey, ClientBindingDescriptor>,
    /// Exhaustive Core-plus-module semantic catalog.
    pub catalog: ClientBindingCatalog,
}
