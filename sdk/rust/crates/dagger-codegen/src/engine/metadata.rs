//! Canonical metadata consumed by the engine's client-generation hook.

use std::collections::BTreeSet;

use serde::{Deserialize, Serialize};

use crate::diagnostic::{DiagnosticCode, DiagnosticSet};

use super::model::{RelativeOperationPath, operation_diagnostic};

/// Checked baseline input copied into engine packaging as `client-generation.json`.
pub const BASELINE_CLIENT_GENERATION_JSON: &[u8] =
    include_bytes!("../../assets/client-generation.json");

/// Validated required-host-file metadata derived from Rust renderer configuration.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct ClientGenerationMetadata {
    /// Wire format for packaged metadata.
    pub format_version: u32,
    /// Canonically ordered normalized paths required from the host project.
    pub required_host_files: BTreeSet<RelativeOperationPath>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawClientGenerationMetadata {
    format_version: u32,
    required_host_files: Vec<String>,
}

impl<'de> Deserialize<'de> for ClientGenerationMetadata {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let raw = RawClientGenerationMetadata::deserialize(deserializer)?;
        if raw.format_version != 1 {
            return Err(serde::de::Error::custom(
                "client-generation format_version must be 1",
            ));
        }
        Self::try_new(raw.required_host_files.iter().map(String::as_str))
            .map_err(serde::de::Error::custom)
    }
}

impl ClientGenerationMetadata {
    /// Validates a finite required-file set and rejects duplicates explicitly.
    pub fn try_new<'a>(paths: impl IntoIterator<Item = &'a str>) -> Result<Self, DiagnosticSet> {
        let mut required_host_files = BTreeSet::new();
        for path in paths {
            let parsed = RelativeOperationPath::parse(path).map_err(|_| {
                DiagnosticSet::one(operation_diagnostic(
                    DiagnosticCode::RequiredHostFileInvalid,
                    path,
                    "required host file is not a normalized relative path",
                ))
            })?;
            if !required_host_files.insert(parsed) {
                return Err(DiagnosticSet::one(operation_diagnostic(
                    DiagnosticCode::RequiredHostFileInvalid,
                    path,
                    "required host file occurs more than once",
                )));
            }
        }
        Ok(Self {
            format_version: 1,
            required_host_files,
        })
    }

    /// Returns the approved baseline with no host-file requirements.
    #[must_use]
    pub fn baseline() -> Self {
        Self {
            format_version: 1,
            required_host_files: BTreeSet::new(),
        }
    }

    /// Encodes canonical JSON without filesystem or packaging side effects.
    pub fn encode(&self) -> Result<Vec<u8>, DiagnosticSet> {
        serde_json::to_vec(self).map_err(|_| {
            DiagnosticSet::one(operation_diagnostic(
                DiagnosticCode::GeneratedProvenanceInvalid,
                "client-generation.json",
                "client-generation metadata could not be encoded",
            ))
        })
    }
}
