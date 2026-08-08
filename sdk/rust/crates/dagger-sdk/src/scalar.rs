//! Handwritten representations of Dagger's opaque public scalar values.
//!
//! These types preserve the engine's wire text exactly. They deliberately expose no
//! GraphQL quoting helpers: argument encoding remains owned by the query runtime, so a
//! scalar cannot bypass its typed serialization and failure boundary.

use std::fmt;

use serde::{Deserialize, Serialize};

use crate::IntoID;
use crate::errors::QueryError;

macro_rules! opaque_string_scalar {
    ($name:ident, $summary:literal) => {
        #[doc = $summary]
        #[derive(Clone, Debug, Deserialize, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize)]
        #[serde(transparent)]
        pub struct $name(String);

        impl $name {
            /// Wraps an owned wire value without interpreting or normalizing it.
            #[must_use]
            pub fn new(value: impl Into<String>) -> Self {
                Self(value.into())
            }

            /// Borrows the exact wire value.
            #[must_use]
            pub fn as_str(&self) -> &str {
                &self.0
            }

            /// Returns the exact owned wire value.
            #[must_use]
            pub fn into_inner(self) -> String {
                self.0
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(&self.0)
            }
        }

        impl From<&str> for $name {
            fn from(value: &str) -> Self {
                Self::new(value)
            }
        }

        impl From<String> for $name {
            fn from(value: String) -> Self {
                Self::new(value)
            }
        }

        impl From<$name> for String {
            fn from(value: $name) -> Self {
                value.into_inner()
            }
        }
    };
}

opaque_string_scalar!(Id, "An opaque Dagger object identifier.");
opaque_string_scalar!(
    Json,
    "A lossless JSON scalar containing the engine's exact JSON-encoded text."
);
opaque_string_scalar!(
    Platform,
    "An opaque Dagger platform value retaining its exact engine spelling."
);

impl IntoID<Id> for Id {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<Id, QueryError>> + Send>> {
        Box::pin(async move { Ok(self) })
    }
}
