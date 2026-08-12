//! Canonical data-only records for standalone-client projection.

use std::collections::BTreeSet;
use std::fmt;

use serde::{Deserialize, Deserializer, Serialize, Serializer};

use crate::schema::canonical::{SchemaCoordinate, SchemaName};

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
    let mut bytes = value.bytes();
    let valid_shape = value.len() <= 64
        && bytes
            .next()
            .is_some_and(|first| first == b'_' || first.is_ascii_alphabetic())
        && bytes.all(|byte| byte == b'_' || byte.is_ascii_alphanumeric());
    if !valid_shape {
        return Err("Rust identifier must be 1-64 ASCII identifier bytes".to_owned());
    }
    if matches!(
        value,
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
        return Err("Rust identifier must not be a reserved keyword".to_owned());
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
    "A bounded, non-keyword ASCII Rust identifier.",
    validate_rust_identifier
);

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
