//! Exact runtime-target validation for every implicit Dagger connection.
//!
//! The validator uses one constant raw query before a session becomes public. Semantic
//! version and clean revision evidence are checked separately so a known mismatch is
//! never weakened into the explicit escape hatch for genuinely unprovable provenance.

use std::sync::Arc;

use semver::Version;
use serde_json::Value;

use crate::connection::EngineConnection;
use crate::graphql::{RawRequest, ResponseData};
use crate::runtime_errors::{CompatibilityError, CompatibilityErrorKind, CompatibilityEvidenceGap};
use crate::target::{ExactTarget, exact_target};

const COMPATIBILITY_QUERY: &str = "query RustSdkCompatibility { version }";

/// Stateless exact-target validator assembled by the concrete connector.
#[derive(Clone, Debug)]
pub(crate) struct CompatibilityValidator {
    expected_version: Version,
    expected_revision_prefix: String,
}

impl CompatibilityValidator {
    pub(crate) fn exact() -> Result<Self, crate::TargetError> {
        Self::from_target(exact_target()?)
    }

    fn from_target(target: &ExactTarget) -> Result<Self, crate::TargetError> {
        let expected_revision_prefix = hex::encode(&target.revision().bytes()[..4]);
        Ok(Self {
            expected_version: target.engine_version().clone(),
            expected_revision_prefix,
        })
    }

    pub(crate) async fn validate<C>(
        &self,
        connection: &C,
        allow_unverified: bool,
    ) -> Result<(), CompatibilityError>
    where
        C: EngineConnection + ?Sized,
    {
        let response = match connection
            .execute(RawRequest::new(COMPATIBILITY_QUERY))
            .await
        {
            Ok(response) => response,
            Err(_) if allow_unverified => return Ok(()),
            Err(error) => {
                return Err(
                    self.unverified(CompatibilityEvidenceGap::Transport, Some(Arc::new(error)))
                );
            }
        };
        if !response.errors().is_empty() {
            return self.finish_unverified(CompatibilityEvidenceGap::GraphQl, allow_unverified);
        }
        let version = match response.data() {
            ResponseData::Value(Value::Object(data)) => match data.get("version") {
                Some(Value::String(version)) => version,
                None | Some(Value::Null) => {
                    return self.finish_unverified(
                        CompatibilityEvidenceGap::MissingVersion,
                        allow_unverified,
                    );
                }
                Some(_) => {
                    return self.finish_unverified(
                        CompatibilityEvidenceGap::ResponseShape,
                        allow_unverified,
                    );
                }
            },
            ResponseData::Absent | ResponseData::Null => {
                return self
                    .finish_unverified(CompatibilityEvidenceGap::MissingVersion, allow_unverified);
            }
            ResponseData::Value(_) => {
                return self
                    .finish_unverified(CompatibilityEvidenceGap::ResponseShape, allow_unverified);
            }
        };

        match self.validate_version(version) {
            Ok(()) => Ok(()),
            Err(error)
                if error.kind() == CompatibilityErrorKind::Unverified && allow_unverified =>
            {
                Ok(())
            }
            Err(error) => Err(error),
        }
    }

    fn finish_unverified(
        &self,
        gap: CompatibilityEvidenceGap,
        allow_unverified: bool,
    ) -> Result<(), CompatibilityError> {
        if allow_unverified {
            Ok(())
        } else {
            Err(self.unverified(gap, None))
        }
    }

    pub(crate) fn validate_version(&self, value: &str) -> Result<(), CompatibilityError> {
        let normalized = value.strip_prefix('v').unwrap_or(value);
        let observed = Version::parse(normalized)
            .map_err(|_| self.unverified(CompatibilityEvidenceGap::MalformedVersion, None))?;

        if observed.major != self.expected_version.major
            || observed.minor != self.expected_version.minor
            || observed.patch != self.expected_version.patch
            || observed.pre != self.expected_version.pre
        {
            return Err(CompatibilityError::mismatch(
                CompatibilityErrorKind::VersionMismatch,
                self.expected_version.clone(),
                Some(observed),
                self.expected_revision_prefix.clone(),
                None,
            ));
        }

        let metadata = observed.build.as_str().to_owned();
        if metadata.is_empty() {
            return Err(
                self.unverified_observed(CompatibilityEvidenceGap::MissingRevision, observed)
            );
        }
        if metadata
            .split(['.', '-'])
            .any(|component| component.eq_ignore_ascii_case("dirty"))
        {
            return Err(self.unverified_observed(CompatibilityEvidenceGap::DirtyRevision, observed));
        }
        if metadata.len() != 8
            || !metadata
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        {
            return Err(
                self.unverified_observed(CompatibilityEvidenceGap::UnknownRevisionFormat, observed)
            );
        }
        if metadata != self.expected_revision_prefix {
            return Err(CompatibilityError::mismatch(
                CompatibilityErrorKind::RevisionMismatch,
                self.expected_version.clone(),
                Some(observed),
                self.expected_revision_prefix.clone(),
                Some(metadata),
            ));
        }
        Ok(())
    }

    fn unverified(
        &self,
        gap: CompatibilityEvidenceGap,
        source: Option<Arc<dyn std::error::Error + Send + Sync + 'static>>,
    ) -> CompatibilityError {
        CompatibilityError::unverified(
            gap,
            self.expected_version.clone(),
            self.expected_revision_prefix.clone(),
            source,
        )
    }

    fn unverified_observed(
        &self,
        gap: CompatibilityEvidenceGap,
        observed: Version,
    ) -> CompatibilityError {
        CompatibilityError::unverified_observed(
            gap,
            self.expected_version.clone(),
            observed,
            self.expected_revision_prefix.clone(),
        )
    }

    #[cfg(test)]
    pub(crate) fn expected_revision_prefix(&self) -> &str {
        &self.expected_revision_prefix
    }
}
