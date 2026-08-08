//! Deterministic GraphQL-to-Rust naming and generated-namespace collision checks.
//!
//! Wire names remain authoritative data. This module only chooses Rust source names;
//! callers must continue to select and serialize with the retained wire spelling.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

use crate::diagnostic::{
    Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet, RelatedCoordinate,
};
use crate::schema::canonical::{SchemaCoordinate, SchemaName};

/// The syntactic namespace in which a generated identifier is used.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum NameContext {
    /// A public type name.
    Type,
    /// A public interface trait name.
    Trait,
    /// A public object or interface handle name.
    Handle,
    /// An enum variant name.
    Variant,
    /// A generated method name.
    Method,
    /// A generated method argument name.
    Argument,
    /// A generated value field name.
    Field,
    /// A generated module name.
    Module,
    /// A field-specific options type.
    Options,
    /// The method accepting a field-specific options value.
    OptionsMethod,
    /// An input-object setter name.
    Setter,
    /// An input-object constructor name.
    Constructor,
    /// A generated verification helper name.
    TestHelper,
}

impl NameContext {
    fn case(self) -> IdentifierCase {
        match self {
            Self::Type | Self::Trait | Self::Handle | Self::Variant | Self::Options => {
                IdentifierCase::UpperCamel
            }
            Self::Method
            | Self::Argument
            | Self::Field
            | Self::Module
            | Self::OptionsMethod
            | Self::Setter
            | Self::Constructor
            | Self::TestHelper => IdentifierCase::Snake,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum IdentifierCase {
    UpperCamel,
    Snake,
}

/// How a Rust-safe identifier is represented in source.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub enum IdentifierToken {
    /// An ordinary Rust identifier.
    Plain,
    /// A keyword represented with Rust's `r#` syntax.
    Raw,
    /// A path-sensitive keyword represented with a stable suffix.
    Suffixed,
}

/// One Rust identifier paired permanently with its exact GraphQL Wire_Name.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct RustName {
    /// Exact source Wire_Name used by selection or serialization.
    pub source: SchemaName,
    /// Valid Rust 2024 spelling, including `r#` when required.
    pub identifier: String,
    /// Syntactic representation selected for the identifier.
    pub token_form: IdentifierToken,
}

/// A coordinate and use context that uniquely identify one generated name.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct NameKey {
    /// Exact schema coordinate owning the name.
    pub coordinate: SchemaCoordinate,
    /// Rust namespace role of the name.
    pub context: NameContext,
}

/// The complete, collision-checked Rust name map.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
pub struct RustNameMap {
    entries: BTreeMap<NameKey, RustName>,
}

impl RustNameMap {
    /// Borrows every mapped name in deterministic coordinate/context order.
    #[must_use]
    pub const fn entries(&self) -> &BTreeMap<NameKey, RustName> {
        &self.entries
    }

    /// Returns the mapped name for one coordinate/context pair.
    #[must_use]
    pub fn get(&self, coordinate: &SchemaCoordinate, context: NameContext) -> Option<&RustName> {
        self.entries.get(&NameKey {
            coordinate: coordinate.clone(),
            context,
        })
    }
}

/// Maps one GraphQL name without reserving it in a generated namespace.
#[must_use]
pub fn rust_name(source: &SchemaName, context: NameContext) -> RustName {
    rust_name_from_candidate(source, source.as_str(), context)
}

pub(crate) fn rust_name_from_candidate(
    source: &SchemaName,
    candidate: &str,
    context: NameContext,
) -> RustName {
    let words = tokenize(candidate);
    let mut identifier = match context.case() {
        IdentifierCase::UpperCamel => words
            .iter()
            .map(|word| {
                let mut characters = word.chars();
                let Some(first) = characters.next() else {
                    return String::new();
                };
                format!("{}{}", first.to_ascii_uppercase(), characters.as_str())
            })
            .collect::<String>(),
        IdentifierCase::Snake => words.join("_"),
    };

    if identifier.is_empty() {
        identifier.push_str(match context.case() {
            IdentifierCase::UpperCamel => "Underscore",
            IdentifierCase::Snake => "_value",
        });
    }
    if identifier
        .as_bytes()
        .first()
        .is_some_and(u8::is_ascii_digit)
    {
        identifier.insert(0, '_');
    }

    let (identifier, token_form) = if is_path_keyword(&identifier) {
        let suffix = match context.case() {
            IdentifierCase::UpperCamel => "Value",
            IdentifierCase::Snake => "_value",
        };
        (format!("{identifier}{suffix}"), IdentifierToken::Suffixed)
    } else if is_keyword(&identifier) {
        (format!("r#{identifier}"), IdentifierToken::Raw)
    } else {
        (identifier, IdentifierToken::Plain)
    };

    RustName {
        source: source.clone(),
        identifier,
        token_form,
    }
}

#[derive(Default)]
pub(crate) struct NameRegistry {
    entries: BTreeMap<NameKey, RustName>,
    reservations: BTreeMap<(String, String), SchemaCoordinate>,
    diagnostics: Vec<Diagnostic>,
}

impl NameRegistry {
    pub(crate) fn reserve_fixed(
        &mut self,
        namespace: impl Into<String>,
        identifier: impl Into<String>,
        owner: &str,
    ) {
        let namespace = namespace.into();
        let identifier = identifier.into();
        let coordinate = SchemaCoordinate::semantic(owner);
        self.reservations
            .entry((namespace, identifier))
            .or_insert(coordinate);
    }

    pub(crate) fn reserve(
        &mut self,
        coordinate: &SchemaCoordinate,
        source: &SchemaName,
        candidate: &str,
        context: NameContext,
        namespace: impl Into<String>,
    ) {
        let name = rust_name_from_candidate(source, candidate, context);
        let namespace = namespace.into();
        let reservation = (namespace, name.identifier.clone());
        if let Some(previous) = self.reservations.get(&reservation) {
            if previous != coordinate {
                self.diagnostics.push(
                    Diagnostic::new(
                        DiagnosticCode::RustNameCollision,
                        Some(DiagnosticCoordinate::new(coordinate.as_str())),
                        format!(
                            "Rust identifier `{}` is already reserved in this generated namespace",
                            name.identifier
                        ),
                    )
                    .with_related(RelatedCoordinate {
                        coordinate: DiagnosticCoordinate::new(previous.as_str()),
                        relationship: "first coordinate projecting to this identifier".to_owned(),
                    }),
                );
            }
        } else {
            self.reservations.insert(reservation, coordinate.clone());
        }
        self.entries.insert(
            NameKey {
                coordinate: coordinate.clone(),
                context,
            },
            name,
        );
    }

    pub(crate) fn record_alias(
        &mut self,
        coordinate: &SchemaCoordinate,
        source: &SchemaName,
        canonical_candidate: &str,
        context: NameContext,
    ) {
        self.entries.insert(
            NameKey {
                coordinate: coordinate.clone(),
                context,
            },
            rust_name_from_candidate(source, canonical_candidate, context),
        );
    }

    pub(crate) fn finish(self) -> Result<RustNameMap, DiagnosticSet> {
        if let Some(diagnostics) = DiagnosticSet::new(self.diagnostics) {
            return Err(diagnostics);
        }
        Ok(RustNameMap {
            entries: self.entries,
        })
    }
}

fn tokenize(source: &str) -> Vec<String> {
    source
        .split('_')
        .filter(|segment| !segment.is_empty())
        .flat_map(tokenize_segment)
        .map(|word| word.to_ascii_lowercase())
        .collect()
}

fn tokenize_segment(segment: &str) -> Vec<String> {
    let characters = segment.chars().collect::<Vec<_>>();
    if characters.is_empty() {
        return Vec::new();
    }
    let mut words = Vec::new();
    let mut start = 0;
    for index in 1..characters.len() {
        let previous = characters[index - 1];
        let current = characters[index];
        let next = characters.get(index + 1).copied();
        let case_boundary = previous.is_ascii_lowercase() && current.is_ascii_uppercase();
        let acronym_boundary = previous.is_ascii_uppercase()
            && current.is_ascii_uppercase()
            && next.is_some_and(|next| next.is_ascii_lowercase());
        let digit_boundary = previous.is_ascii_digit() != current.is_ascii_digit();
        if case_boundary || acronym_boundary || digit_boundary {
            words.push(characters[start..index].iter().collect());
            start = index;
        }
    }
    words.push(characters[start..].iter().collect());
    words
}

fn is_path_keyword(identifier: &str) -> bool {
    matches!(identifier, "self" | "Self" | "super" | "crate")
}

fn is_keyword(identifier: &str) -> bool {
    matches!(
        identifier,
        "as" | "async"
            | "await"
            | "break"
            | "const"
            | "continue"
            | "dyn"
            | "else"
            | "enum"
            | "extern"
            | "false"
            | "fn"
            | "for"
            | "if"
            | "impl"
            | "in"
            | "let"
            | "loop"
            | "match"
            | "mod"
            | "move"
            | "mut"
            | "pub"
            | "ref"
            | "return"
            | "static"
            | "struct"
            | "trait"
            | "true"
            | "type"
            | "unsafe"
            | "use"
            | "where"
            | "while"
            | "abstract"
            | "become"
            | "box"
            | "do"
            | "final"
            | "gen"
            | "macro"
            | "override"
            | "priv"
            | "try"
            | "typeof"
            | "unsized"
            | "virtual"
            | "yield"
    )
}

#[cfg(test)]
mod tests {
    use super::{IdentifierToken, NameContext, NameRegistry, rust_name};
    use crate::diagnostic::DiagnosticCode;
    use crate::schema::canonical::{SchemaCoordinate, SchemaName};
    use proptest::prelude::*;
    use proptest::test_runner::{Config, FileFailurePersistence};

    #[test]
    fn acronym_digit_and_keyword_boundaries_are_stable() {
        let cases = [
            (
                "JSONValue",
                NameContext::Type,
                "JsonValue",
                IdentifierToken::Plain,
            ),
            (
                "HTTP2Server",
                NameContext::Method,
                "http_2_server",
                IdentifierToken::Plain,
            ),
            ("type", NameContext::Method, "r#type", IdentifierToken::Raw),
            (
                "self",
                NameContext::Method,
                "self_value",
                IdentifierToken::Suffixed,
            ),
            (
                "Self",
                NameContext::Type,
                "SelfValue",
                IdentifierToken::Suffixed,
            ),
        ];
        for (source, context, expected, token) in cases {
            let source = SchemaName::try_from(source).expect("test name must be valid");
            let projected = rust_name(&source, context);
            assert_eq!(projected.identifier, expected);
            assert_eq!(projected.token_form, token);
        }
    }

    fn naming_config() -> Config {
        Config {
            cases: 1_024,
            failure_persistence: Some(Box::new(FileFailurePersistence::Direct(concat!(
                env!("CARGO_MANIFEST_DIR"),
                "/proptest-regressions/rust-naming.txt"
            )))),
            ..Config::default()
        }
    }

    proptest! {
        #![proptest_config(naming_config())]

        // Feature: rust-sdk-core-codegen, Property 20: Rust naming is valid, exact, and collision-free
        #[test]
        fn property_20_rust_naming_valid_exact_collision_free(
            source in "[A-Za-z_][A-Za-z0-9_]{0,47}",
            context in prop_oneof![
                Just(NameContext::Type),
                Just(NameContext::Variant),
                Just(NameContext::Method),
                Just(NameContext::Argument),
                Just(NameContext::Module),
            ],
            first in "[a-z]{1,8}",
            second in "[a-z]{1,8}",
        ) {
            let wire = SchemaName::try_from(source.as_str())
                .expect("generated GraphQL name must satisfy its lexical strategy");
            let projected = rust_name(&wire, context);
            prop_assert_eq!(&projected.source, &wire);
            prop_assert!(syn::parse_str::<syn::Ident>(&projected.identifier).is_ok());

            let underscored = SchemaName::try_from(format!("{first}_{second}").as_str())
                .expect("generated collision name must be valid");
            let mut characters = second.chars();
            let initial = characters
                .next()
                .expect("generated second collision word is non-empty")
                .to_ascii_uppercase();
            let transitioned = SchemaName::try_from(
                format!("{first}{initial}{}", characters.as_str()).as_str(),
            )
            .expect("generated collision name must be valid");
            let first_coordinate = SchemaCoordinate::named_type(&underscored);
            let second_coordinate = SchemaCoordinate::named_type(&transitioned);
            let mut registry = NameRegistry::default();
            registry.reserve(
                &first_coordinate,
                &underscored,
                underscored.as_str(),
                NameContext::Method,
                "property",
            );
            registry.reserve(
                &second_coordinate,
                &transitioned,
                transitioned.as_str(),
                NameContext::Method,
                "property",
            );
            let diagnostics = registry.finish().expect_err("normalization collision must fail");
            prop_assert!(diagnostics.contains(DiagnosticCode::RustNameCollision));
            let diagnostic = &diagnostics.diagnostics()[0];
            prop_assert_eq!(
                diagnostic.coordinate.as_ref().map(|coordinate| coordinate.as_str()),
                Some(second_coordinate.as_str()),
            );
            prop_assert_eq!(diagnostic.related[0].coordinate.as_str(), first_coordinate.as_str());
        }
    }
}
