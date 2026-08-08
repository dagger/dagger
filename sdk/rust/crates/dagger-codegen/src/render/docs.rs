//! Rustdoc normalization for schema-authored generated API documentation.
//!
//! Schema text is untrusted input even after structural schema validation. This module
//! preserves its prose and code while preventing HTML, accidental intra-doc links,
//! control text, and unclosed fences from changing the meaning of the generated crate.

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate};
use crate::schema::canonical::SchemaCoordinate;

/// Sanitizes one optional schema description or supplies the precise generated contract.
pub(crate) fn documentation(
    coordinate: &SchemaCoordinate,
    description: Option<&str>,
    fallback: &str,
) -> Result<String, Diagnostic> {
    let source = description
        .filter(|value| !value.trim().is_empty())
        .unwrap_or(fallback);
    sanitize(coordinate, source)
}

/// Sanitizes one schema-authored documentation fragment without losing paragraph shape.
pub(crate) fn sanitize(coordinate: &SchemaCoordinate, source: &str) -> Result<String, Diagnostic> {
    if source
        .chars()
        .any(|character| character.is_control() && !matches!(character, '\n' | '\r' | '\t'))
    {
        return Err(Diagnostic::new(
            DiagnosticCode::GeneratedDocumentationInvalid,
            Some(DiagnosticCoordinate::new(coordinate.as_str())),
            "schema documentation contains unsupported control text",
        ));
    }

    let normalized = source.replace("\r\n", "\n").replace('\r', "\n");
    let escaped = escape_untrusted_markup(&normalized);
    let mut rendered = linkify_bare_urls(&escaped);
    if !rendered.matches("```").count().is_multiple_of(2) {
        rendered.push_str("\n```");
    }
    Ok(rendered.trim().to_owned())
}

fn escape_untrusted_markup(source: &str) -> String {
    source
        .chars()
        .flat_map(|character| match character {
            '<' => "&lt;".chars().collect::<Vec<_>>(),
            '>' => "&gt;".chars().collect::<Vec<_>>(),
            '[' => "\\[".chars().collect::<Vec<_>>(),
            ']' => "\\]".chars().collect::<Vec<_>>(),
            _ => vec![character],
        })
        .collect()
}

fn linkify_bare_urls(source: &str) -> String {
    source
        .split_inclusive(char::is_whitespace)
        .map(|segment| {
            let split = segment.find(char::is_whitespace).unwrap_or(segment.len());
            let (word, whitespace) = segment.split_at(split);
            let candidate = word.trim_end_matches(['.', ',', ';', ':']);
            let (candidate, punctuation) = word.split_at(candidate.len());
            if candidate.starts_with("https://") || candidate.starts_with("http://") {
                format!("[{candidate}]({candidate}){punctuation}{whitespace}")
            } else {
                segment.to_owned()
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use proptest::prelude::*;

    use super::sanitize;
    use crate::schema::canonical::{SchemaCoordinate, SchemaName};

    fn coordinate() -> SchemaCoordinate {
        SchemaCoordinate::named_type(
            &SchemaName::try_from("GeneratedDocs").expect("test name must be valid"),
        )
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(256))]

        // Feature: rust-sdk-core-codegen, Property 21: Generated documentation is complete and warning-free
        #[test]
        fn property_21_generated_documentation_complete_warning_free(
            paragraphs in prop::collection::vec("[A-Za-z0-9 _./:<>{}\\[\\]-]{0,48}", 0..8),
            close_fence in any::<bool>(),
            deprecated in prop::option::of("[A-Za-z0-9 _-]{1,24}"),
            experimental in prop::option::of("[A-Za-z0-9 _-]{1,24}"),
        ) {
            let mut source = paragraphs.join("\n\n");
            source.push_str("\nhttps://docs.dagger.io/example");
            source.push_str("\n```rust\nlet value = <T>::default();");
            if close_fence {
                source.push_str("\n```");
            }
            if let Some(reason) = deprecated {
                source.push_str(&format!("\n\nDeprecated: {reason}"));
            }
            if let Some(reason) = experimental {
                source.push_str(&format!("\n\nExperimental: {reason}"));
            }

            let rendered = sanitize(&coordinate(), &source).expect("generated prose must sanitize");
            prop_assert!(!rendered.is_empty());
            prop_assert!(!rendered.contains('<'));
            prop_assert!(!rendered.contains('>'));
            prop_assert!(rendered.contains(
                "[https://docs.dagger.io/example](https://docs.dagger.io/example)"
            ));
            prop_assert_eq!(rendered.matches("```").count() % 2, 0);
        }
    }

    #[test]
    fn rejects_unsupported_control_text() {
        let error = sanitize(&coordinate(), "unsafe\u{0007}text")
            .expect_err("control text must be rejected");
        assert_eq!(
            error.code,
            crate::diagnostic::DiagnosticCode::GeneratedDocumentationInvalid
        );
    }
}
