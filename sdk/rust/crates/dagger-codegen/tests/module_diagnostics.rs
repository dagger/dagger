//! Exhaustive module diagnostic taxonomy, ordering, mapping, and redaction properties.

use std::collections::BTreeSet;
use std::num::NonZeroU32;

use dagger_codegen::module::{
    DiagnosticSourceKind, GeneratedAssetPath, GeneratedCoordinate, ModuleDiagnostic,
    ModuleDiagnosticCode, ModuleDiagnosticSet, ModuleSourcePath, SafeDiagnosticSource,
    SourceCoordinate, WireName,
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

    // Every failure keeps one stable code, authored coordinate, safe cause, and deterministic order.
    #[test]
    fn property_23_diagnostics_typed_stable_ordered_redacted(
        left_index in 0_usize..ModuleDiagnosticCode::ALL.len(),
        right_index in 0_usize..ModuleDiagnosticCode::ALL.len(),
        left_line in 1_u32..1000,
        right_line in 1_u32..1000,
        reverse in any::<bool>(),
        source_kind in 0_u8..7,
        unsafe_kind in 0_u8..6,
        generated in any::<bool>(),
    ) {
        let left_code = ModuleDiagnosticCode::ALL[left_index];
        let right_code = ModuleDiagnosticCode::ALL[right_index];
        let left_coordinate = coordinate("src/left.rs", left_line);
        let right_coordinate = coordinate("src/right.rs", right_line);
        let cause = SafeDiagnosticSource::new(
            diagnostic_source_kind(source_kind),
            format!("bounded source fact {left_line}"),
        )
        .expect("generated safe source fact");
        let mut left = ModuleDiagnostic::new(
            left_code,
            Some(left_coordinate.clone()),
            "typed module operation failed",
            "repair the authored declaration",
        )
        .expect("static safe diagnostic")
        .with_wire_name(WireName::new("leftField").expect("valid wire name"))
        .with_cause(cause.clone());
        if generated {
            left = left.with_generated_coordinate(GeneratedCoordinate {
                path: GeneratedAssetPath::new("src/dagger_generated/dispatch.rs")
                    .expect("valid generated path"),
                line: NonZeroU32::new(41).expect("non-zero generated line"),
                column: NonZeroU32::new(7).expect("non-zero generated column"),
                authored: left_coordinate.clone(),
            });
        }
        let right = ModuleDiagnostic::new(
            right_code,
            Some(right_coordinate),
            "typed module operation failed",
            "repair the authored declaration",
        )
        .expect("static safe diagnostic");
        let input = if reverse {
            vec![right.clone(), left.clone()]
        } else {
            vec![left.clone(), right.clone()]
        };
        let diagnostics = ModuleDiagnosticSet::new(input).expect("two diagnostics form a set");
        let expected_first_is_left = (left_code, Some(&left_coordinate), left.wire_name())
            <= (right_code, right.source_coordinate(), right.wire_name());
        let expected_first = if expected_first_is_left { &left } else { &right };
        prop_assert_eq!(diagnostics.diagnostics().first(), Some(expected_first));
        prop_assert_eq!(left.code(), left_code);
        prop_assert_eq!(left.source_coordinate(), Some(&left_coordinate));
        prop_assert_eq!(left.cause(), Some(&cause));
        if generated {
            prop_assert_eq!(left.generated_coordinate().map(|value| &value.authored), Some(&left_coordinate));
        }

        let unsafe_text = sensitive_text(unsafe_kind);
        prop_assert!(
            SafeDiagnosticSource::new(DiagnosticSourceKind::Transport, unsafe_text).is_err()
        );
        prop_assert!(
            ModuleDiagnostic::new(
                ModuleDiagnosticCode::DiagnosticRedactionFailed,
                None,
                unsafe_text,
                "use typed safe facts",
            )
            .is_err()
        );
    }
}

#[test]
fn diagnostic_taxonomy_is_exhaustive_unique_and_strictly_round_trippable() {
    let mut external = BTreeSet::new();
    for code in ModuleDiagnosticCode::ALL {
        assert!(external.insert(code.external()), "duplicate external code");
        assert!(code.external().starts_with("module."));
        let encoded = serde_json::to_vec(code).expect("diagnostic code serializes");
        let decoded: ModuleDiagnosticCode =
            serde_json::from_slice(&encoded).expect("diagnostic code deserializes");
        assert_eq!(&decoded, code);
    }
    assert_eq!(external.len(), ModuleDiagnosticCode::ALL.len());
}

fn coordinate(path: &str, line: u32) -> SourceCoordinate {
    SourceCoordinate {
        path: ModuleSourcePath::new(path).expect("valid authored path"),
        line: NonZeroU32::new(line).expect("generated line is non-zero"),
        column: NonZeroU32::new(1).expect("one is non-zero"),
    }
}

fn diagnostic_source_kind(value: u8) -> DiagnosticSourceKind {
    match value % 7 {
        0 => DiagnosticSourceKind::Cargo,
        1 => DiagnosticSourceKind::Rustc,
        2 => DiagnosticSourceKind::Filesystem,
        3 => DiagnosticSourceKind::Codec,
        4 => DiagnosticSourceKind::Query,
        5 => DiagnosticSourceKind::Transport,
        _ => DiagnosticSourceKind::Publication,
    }
}

fn sensitive_text(value: u8) -> &'static str {
    match value % 6 {
        0 => "Authorization: Basic c2VjcmV0",
        1 => "Bearer session-secret",
        2 => "DAGGER_SESSION_TOKEN=secret",
        3 => "token=secret",
        4 => "password=secret",
        _ => "https://user:secret@example.invalid/path",
    }
}
