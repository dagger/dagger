//! Validated schema values accepted by projection.
//!
//! The initial canonical model is deliberately small. Validation work can enrich it
//! without changing the raw wire contract or renderer boundary.

use serde::{Deserialize, Serialize};

/// A schema name that has passed GraphQL-name validation.
#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
pub struct SchemaName(String);

impl SchemaName {
    /// Borrows the validated name.
    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}
