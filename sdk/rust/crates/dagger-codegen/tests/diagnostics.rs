//! Stable diagnostic ordering and safe rendering regressions.

use dagger_codegen::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};

#[test]
fn diagnostics_sort_by_code_coordinate_and_message() {
    let diagnostics = DiagnosticSet::new(vec![
        Diagnostic::new(
            DiagnosticCode::SchemaWrapperInvalid,
            Some(DiagnosticCoordinate::new("Query.z")),
            "second",
        ),
        Diagnostic::new(
            DiagnosticCode::SchemaReferenceInvalid,
            Some(DiagnosticCoordinate::new("Query.b")),
            "third",
        ),
        Diagnostic::new(
            DiagnosticCode::SchemaReferenceInvalid,
            Some(DiagnosticCoordinate::new("Query.a")),
            "first",
        ),
    ])
    .expect("fixture contains diagnostics");

    let rendered = diagnostics.to_string();
    let lines: Vec<_> = rendered.lines().collect();
    assert_eq!(lines[0], "SCHEMA_REFERENCE_INVALID [Query.a]: first");
    assert_eq!(lines[1], "SCHEMA_REFERENCE_INVALID [Query.b]: third");
    assert_eq!(lines[2], "SCHEMA_WRAPPER_INVALID [Query.z]: second");
}

#[test]
fn caller_control_characters_do_not_reach_cli_rendering() {
    let diagnostics = DiagnosticSet::one(Diagnostic::new(
        DiagnosticCode::SchemaReferenceInvalid,
        Some(DiagnosticCoordinate::new("Query.\u{1b}[31mfield")),
        "bad\u{0} member\u{1b}[31m",
    ));

    let rendered = diagnostics.to_string();
    assert!(!rendered.contains('\u{1b}'));
    assert!(!rendered.contains('\u{0}'));
    assert!(rendered.contains("Query.[31mfield"));
}

#[test]
fn client_diagnostics_are_bounded_and_credential_safe() {
    let diagnostic = Diagnostic::new(
        DiagnosticCode::ClientSchemaScopeInvalid,
        Some(DiagnosticCoordinate::new(format!(
            "Query.{}",
            "x".repeat(800)
        ))),
        format!(
            "schema rejected https://user:secret@example.invalid/repo token=secret {}",
            "detail".repeat(1000)
        ),
    );
    let rendered = diagnostic.to_string();
    assert!(rendered.contains("CLIENT_SCHEMA_SCOPE_INVALID"));
    assert!(!rendered.contains("secret"));
    assert!(!rendered.contains("example.invalid"));
    assert!(rendered.len() < 5_000);
    assert!(
        diagnostic
            .coordinate
            .as_ref()
            .is_some_and(|coordinate| coordinate.as_str().chars().count() == 512)
    );
}
