//! Exhaustive accounting for canonical schema elements.

use serde::{Deserialize, Serialize};

/// The disposition assigned to one canonical schema element.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub enum CatalogDisposition {
    /// The element will be emitted into the generated client.
    Emitted,
    /// The element is intentionally provided by the handwritten runtime.
    RuntimeProvided,
    /// The element is excluded by a named policy.
    PolicyExcluded,
}

/// One exhaustive projection decision.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct CatalogEntry {
    /// Stable schema identity of the accounted element.
    pub schema_id: String,
    /// Projection decision for the element.
    pub disposition: CatalogDisposition,
    /// Stable reason explaining non-emission or special handling.
    pub reason: Option<String>,
}
