//! Observable-boundary policy for standalone-client generation.
//!
//! Typed models prevent most unsafe values from being constructed. This final byte
//! audit protects the serialization and rendering boundaries where otherwise-valid
//! fields are combined, and therefore catches credential material, host identity,
//! ambient SDK paths, private implementation dependencies, and unsafe/global client
//! state before those bytes can be published or retained as evidence.

use crate::{EngineDiagnostic, EngineDiagnosticCode};

/// Observable byte domain crossing the standalone-client boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ClientBoundaryArtifactKind {
    /// Canonical operation or preflight request.
    Request,
    /// Explicitly admitted process environment.
    Environment,
    /// Immutable dependency descriptor or Cargo declaration.
    Dependency,
    /// Safely rendered diagnostic.
    Diagnostic,
    /// Generated Rust source or documentation.
    GeneratedRust,
    /// Generated Cargo or project policy.
    GeneratedManifest,
    /// Checkpoint, provenance, or completeness evidence.
    Evidence,
}

/// Rejects bytes which would expose private identity or weaken generated Rust policy.
pub fn validate_client_boundary(
    kind: ClientBoundaryArtifactKind,
    bytes: &[u8],
) -> Result<(), EngineDiagnostic> {
    let value = std::str::from_utf8(bytes).map_err(|_| boundary_error())?;
    let lower = value.to_ascii_lowercase();
    if contains_secret(&lower)
        || contains_credential_url(value)
        || contains_host_path(value)
        || matches!(
            kind,
            ClientBoundaryArtifactKind::GeneratedManifest | ClientBoundaryArtifactKind::Dependency
        ) && contains_ambient_or_private_dependency(&lower)
        || matches!(kind, ClientBoundaryArtifactKind::GeneratedRust)
            && contains_unsafe_or_global_client(&lower)
    {
        return Err(boundary_error());
    }
    Ok(())
}

fn contains_secret(value: &str) -> bool {
    [
        "authorization:",
        "authorization=",
        "bearer ",
        concat!("session", "_token"),
        "session-token",
        concat!("dagger_session", "_token"),
        "token=",
        "password=",
        "passwd=",
    ]
    .iter()
    .any(|marker| value.contains(marker))
}

fn contains_credential_url(value: &str) -> bool {
    let mut remaining = value;
    while let Some(scheme) = remaining.find("://") {
        let authority = &remaining[scheme + 3..];
        let end = authority
            .find(|character: char| {
                character == '/'
                    || character.is_whitespace()
                    || matches!(character, '"' | '\'' | '}' | ']')
            })
            .unwrap_or(authority.len());
        if authority[..end].contains('@') {
            return true;
        }
        remaining = &authority[end..];
    }
    false
}

fn contains_host_path(value: &str) -> bool {
    let lower = value.to_ascii_lowercase();
    lower.contains("/users/")
        || lower.contains("/home/")
        || lower.contains("/private/var/")
        || lower.contains("\\users\\")
        || lower
            .as_bytes()
            .windows(3)
            .any(|window| window[0].is_ascii_alphabetic() && window[1..] == *b":\\")
}

fn contains_ambient_or_private_dependency(value: &str) -> bool {
    value.contains("path =")
        || value.contains("path=")
        || [
            "dagger-codegen",
            "dagger-sdk-engine",
            "dagger-sdk-completeness",
            "dagger-bootstrap",
        ]
        .iter()
        .any(|package| value.contains(package))
}

fn contains_unsafe_or_global_client(value: &str) -> bool {
    [
        "unsafe ",
        "unsafe{",
        "unsafe\n",
        "unsafe\t",
        "static mut",
        "oncelock<client",
        "lazylock<client",
    ]
    .iter()
    .any(|marker| value.contains(marker))
}

fn boundary_error() -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::DiagnosticRedactionFailed,
        Some("client-boundary"),
        "standalone-client output crossed a private identity or source-policy boundary",
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn immutable_credential_free_repository_url_is_admitted() {
        validate_client_boundary(
            ClientBoundaryArtifactKind::Dependency,
            br#"git = "https://github.com/dagger/dagger", rev = "25300124ca110612edc09c43f89cb5fad6028170""#,
        )
        .unwrap();
    }
}
