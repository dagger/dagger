//! Validation and Cargo rendering for immutable packaged-engine coordinates.
//!
//! Scalar construction makes mutable dependency shapes unrepresentable. This module
//! adds the cross-field checks which bind registry packages to the exact SDK release
//! while allowing an independently immutable Git source for fork-built engines.

use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::{EngineSourceDescriptor, PublishedSdkDependency};

impl PublishedSdkDependency {
    /// Renders the exact Cargo value admitted by the immutable dependency model.
    #[must_use]
    pub fn cargo_value(&self) -> String {
        match self {
            Self::Registry { exact_version, .. } => format!("={exact_version}"),
            Self::Git { url, revision, .. } => {
                format!("{{ git = \"{url}\", rev = \"{revision}\" }}")
            }
        }
    }
}

impl EngineSourceDescriptor {
    /// Verifies cross-field invariants not already guaranteed by the scalar types.
    pub fn validate(&self) -> Result<(), EngineDiagnostic> {
        if let PublishedSdkDependency::Registry { exact_version, .. } = &self.sdk_dependency {
            if exact_version != &self.rust_sdk_version {
                return Err(EngineDiagnostic::new(
                    EngineDiagnosticCode::SdkManifestInvalid,
                    Some("descriptor.sdk_dependency.exact_version"),
                    "registry dependency version differs from the packaged Rust SDK version",
                ));
            }
        }
        if self.repository.as_str().ends_with(".git") {
            return Err(EngineDiagnostic::new(
                EngineDiagnosticCode::SdkManifestInvalid,
                Some("descriptor.repository"),
                "repository identity must omit the transport-only .git suffix",
            ));
        }
        Ok(())
    }
}
