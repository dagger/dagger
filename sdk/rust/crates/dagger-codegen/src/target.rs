//! Pure code-generation target selection.

use serde::{Deserialize, Serialize};

/// The inputs that make generated output reproducible.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct CodegenTarget {
    /// Engine/API version whose schema is being projected.
    pub engine_version: String,
    /// Rust SDK version whose runtime surface the output targets.
    pub sdk_version: String,
    /// Rust edition used to parse emitted syntax.
    pub rust_edition: String,
}
