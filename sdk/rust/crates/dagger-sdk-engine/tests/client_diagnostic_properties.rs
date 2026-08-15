//! Total diagnostic and client-boundary security properties.

use std::collections::BTreeSet;

use dagger_codegen::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use dagger_sdk_engine::{
    ClientBoundaryArtifactKind, EngineDiagnostic, EngineDiagnosticCode, validate_client_boundary,
};
use proptest::prelude::*;

fn property_config() -> ProptestConfig {
    ProptestConfig {
        cases: 256,
        ..ProptestConfig::default()
    }
}

proptest! {
    #![proptest_config(property_config())]

    // Every producer retains one stable primary code and only a safe, deterministic coordinate.
    #[test]
    fn property_20_diagnostics_total_stable_ordered_safely_located(
        seed in any::<u16>(),
        reverse in any::<bool>(),
        hostile in any::<bool>(),
    ) {
        let engine_code = EngineDiagnosticCode::ALL[usize::from(seed) % EngineDiagnosticCode::ALL.len()];
        let generator_code = DiagnosticCode::ALL[usize::from(seed) % DiagnosticCode::ALL.len()];
        let coordinate = if hostile {
            "/Users/developer/project\u{1b}[31m?token=secret"
        } else if seed % 3 == 0 {
            "Query.container(id:)"
        } else if seed % 3 == 1 {
            "workspace/client/src/lib.rs"
        } else {
            "dependencies.dagger-sdk"
        };

        let engine = EngineDiagnostic::new(
            engine_code,
            Some(coordinate),
            "rejected\u{1b}[31m Authorization: Bearer secret",
        );
        prop_assert_eq!(engine.code, engine_code);
        prop_assert!(!engine.render().contains(char::from(27)));
        prop_assert!(!engine.render().contains("secret"));
        if hostile {
            prop_assert_eq!(engine.coordinate.as_deref(), Some("[REDACTED]"));
        } else {
            prop_assert_eq!(engine.coordinate.as_deref(), Some(coordinate));
        }

        let compiler = Diagnostic::new(
            generator_code,
            Some(DiagnosticCoordinate::new(coordinate)),
            "invalid schema coordinate",
        );
        let mut discovered = vec![compiler.clone(), compiler.clone()];
        if reverse {
            discovered.reverse();
        }
        let set = DiagnosticSet::new(discovered).expect("one diagnostic remains");
        prop_assert_eq!(set.diagnostics(), &[compiler]);
        prop_assert!(!set.to_string().contains(char::from(27)));
    }

    // No credential, host path, local SDK path, private crate, unsafe block, or global client survives.
    #[test]
    fn property_21_credentials_host_identity_never_cross_client_boundary(
        seed in any::<u16>(),
        prefix in "[A-Za-z0-9_-]{0,24}",
    ) {
        let (kind, hostile) = match seed % 8 {
            0 => (ClientBoundaryArtifactKind::Request, format!(r#"{{"module":"https://user:{prefix}@example.invalid/repo"}}"#)),
            1 => (ClientBoundaryArtifactKind::Environment, format!("DAGGER_SESSION_TOKEN={prefix}secret")),
            2 => (ClientBoundaryArtifactKind::Dependency, "dagger-sdk = { path = \"../../sdk\" }".to_owned()),
            3 => (ClientBoundaryArtifactKind::Diagnostic, format!("Authorization: Bearer {prefix}secret")),
            4 => (ClientBoundaryArtifactKind::GeneratedRust, "pub unsafe fn cross_boundary() {}".to_owned()),
            5 => (ClientBoundaryArtifactKind::GeneratedRust, "static CLIENT: OnceLock<Client> = OnceLock::new();".to_owned()),
            6 => (ClientBoundaryArtifactKind::GeneratedManifest, "dagger-codegen = \"1\"".to_owned()),
            _ => (ClientBoundaryArtifactKind::Control, format!("/Users/{prefix}/dagger")),
        };
        prop_assert!(validate_client_boundary(kind, hostile.as_bytes()).is_err());

        let rendered = EngineDiagnostic::new(
            EngineDiagnosticCode::OperationInputInvalid,
            Some(hostile),
            "Bearer secret",
        ).render();
        prop_assert!(!rendered.contains("Bearer secret"));
        prop_assert!(!rendered.contains("/Users/"));
    }
}

#[test]
fn diagnostic_taxonomies_have_unique_stable_wire_codes() {
    let engine = EngineDiagnosticCode::ALL
        .iter()
        .map(ToString::to_string)
        .collect::<BTreeSet<_>>();
    let generator = DiagnosticCode::ALL
        .iter()
        .map(ToString::to_string)
        .collect::<BTreeSet<_>>();
    assert_eq!(engine.len(), EngineDiagnosticCode::ALL.len());
    assert_eq!(generator.len(), DiagnosticCode::ALL.len());
}
