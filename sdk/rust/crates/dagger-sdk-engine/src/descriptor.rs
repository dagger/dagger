//! Validation and Cargo rendering for immutable packaged-engine coordinates.
//!
//! Scalar construction makes mutable dependency shapes unrepresentable. This module
//! adds the cross-field checks which bind the selected public SDK source to the exact
//! engine repository, revision, and release carried by the packaged descriptor.

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
    /// Verifies that the public dependency cannot resolve to a different SDK target.
    pub fn validate(&self) -> Result<(), EngineDiagnostic> {
        match &self.sdk_dependency {
            PublishedSdkDependency::Registry { exact_version, .. } => {
                if exact_version != &self.rust_sdk_version {
                    return Err(EngineDiagnostic::new(
                        EngineDiagnosticCode::SdkManifestInvalid,
                        Some("descriptor.sdk_dependency.exact_version"),
                        "registry dependency version differs from the packaged Rust SDK version",
                    ));
                }
            }
            PublishedSdkDependency::Git { url, revision, .. } => {
                if url != &self.repository || revision != &self.dagger_revision {
                    return Err(EngineDiagnostic::new(
                        EngineDiagnosticCode::SdkManifestInvalid,
                        Some("descriptor.sdk_dependency"),
                        "Git dependency must use the packaged repository and full Dagger revision",
                    ));
                }
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
