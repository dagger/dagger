//! Existing-session validation and external-resource ownership.
//!
//! Process values remain native until validated. The resulting credential has no
//! general string-conversion trait, and the resource marker makes external ownership
//! explicit so lifecycle code has no engine-shutdown operation available to call.

use std::ffi::OsString;
use std::fmt;
use std::sync::Arc;

use crate::errors::{ExistingSessionError, ExistingSessionErrorKind};

/// Raw existing-session inputs captured from one process snapshot.
#[derive(Clone, Eq, PartialEq)]
pub(crate) struct ExistingSessionInput {
    pub(crate) port: OsString,
    pub(crate) token: Option<OsString>,
}

impl fmt::Debug for ExistingSessionInput {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ExistingSessionInput")
            .field("port_present", &!self.port.is_empty())
            .field("token_present", &self.token.is_some())
            .finish()
    }
}

/// Validated existing-session values and their external ownership marker.
#[derive(Clone)]
pub(crate) struct ValidatedExistingSession {
    port: u16,
    token: SecretString,
    resource: ExistingSessionResource,
}

impl ValidatedExistingSession {
    pub(crate) const fn port(&self) -> u16 {
        self.port
    }

    pub(crate) fn token(&self) -> &SecretString {
        &self.token
    }

    pub(crate) const fn resource(&self) -> ExistingSessionResource {
        self.resource
    }
}

impl fmt::Debug for ValidatedExistingSession {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ValidatedExistingSession")
            .field("port", &self.port)
            .field("token", &self.token)
            .field("resource", &self.resource)
            .finish()
    }
}

/// Credential text exposed only at authentication and redaction boundaries.
#[derive(Clone)]
pub(crate) struct SecretString(Arc<str>);

impl SecretString {
    pub(crate) fn new(value: impl Into<Arc<str>>) -> Option<Self> {
        let value = value.into();
        (!value.is_empty()).then_some(Self(value))
    }

    pub(crate) fn expose_for_authentication(&self) -> &str {
        &self.0
    }

    pub(crate) fn expose_for_redaction(&self) -> &[u8] {
        self.0.as_bytes()
    }
}

impl fmt::Debug for SecretString {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("SecretString([REDACTED])")
    }
}

/// Externally owned session state with no engine-shutdown capability.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct ExistingSessionResource;

impl ExistingSessionResource {
    pub(crate) const fn close(self) {}

    pub(crate) const fn abort(self) {}
}

/// Validates raw native session inputs without formatting either value.
pub(crate) fn validate_existing_session(
    input: ExistingSessionInput,
) -> Result<ValidatedExistingSession, ExistingSessionError> {
    let port = input
        .port
        .to_str()
        .ok_or_else(|| ExistingSessionError::new(ExistingSessionErrorKind::NonNativePort))?;
    let port = port
        .parse::<u32>()
        .ok()
        .filter(|port| (1..=u32::from(u16::MAX)).contains(port))
        .map(|port| port as u16)
        .ok_or_else(|| ExistingSessionError::new(ExistingSessionErrorKind::InvalidPort))?;
    let token = input
        .token
        .ok_or_else(|| ExistingSessionError::new(ExistingSessionErrorKind::MissingToken))?;
    let token = token
        .to_str()
        .ok_or_else(|| ExistingSessionError::new(ExistingSessionErrorKind::NonNativeToken))?;
    if token.is_empty() {
        return Err(ExistingSessionError::new(
            ExistingSessionErrorKind::EmptyToken,
        ));
    }

    Ok(ValidatedExistingSession {
        port,
        token: SecretString(Arc::from(token)),
        resource: ExistingSessionResource,
    })
}
