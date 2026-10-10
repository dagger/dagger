//! Format identifiers with the names the engine formatted.
//!
//! Codegen doesn't parse or format schema names itself: the online step that
//! writes the introspection JSON (`codegen introspect --names-out`) asks the
//! engine to format every schema name with `Query.formatIdentifiers`, and
//! writes the results to a names file next to it. Using those, instead of
//! guessing at word boundaries, keeps acronyms and dictionary terms intact:
//! `prerequisiteSHAs` becomes `prerequisite_shas`, not `prerequisite_sh_as`.
//!
//! A schema without `Query.formatIdentifiers` (before v1.0.0), a format the
//! file doesn't have, or a name missing from it gets the legacy converter.

use std::collections::HashMap;

use dagger_sdk::core::introspection::Schema;

/// A convention for joining words into an identifier: a value of the
/// engine's `Casing` enum.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Casing {
    /// Every word capitalized: `HTTPClient`.
    Pascal,
    /// Lowercase, joined with `_`: `http_client`.
    Snake,
}

impl Casing {
    fn as_str(self) -> &'static str {
        match self {
            Casing::Pascal => "PASCAL",
            Casing::Snake => "SNAKE",
        }
    }
}

/// How acronyms and terms are written where a word starts with a capital: a
/// value of the engine's `AcronymStyle` enum.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Acronyms {
    /// `HTTPClient`, `IPv6Address`, `GitHubRepo`.
    Uppercase,
    /// `HttpClient`, `Ipv6Address`, `GitHubRepo`.
    Capitalized,
}

impl Acronyms {
    fn as_str(self) -> &'static str {
        match self {
            Acronyms::Uppercase => "UPPERCASE",
            Acronyms::Capitalized => "CAPITALIZED",
        }
    }
}

/// The name formats Rust codegen uses, as `CASING:ACRONYMS` keys of a names
/// file: types and enum variants, then functions, arguments and fields.
pub const NAME_FORMATS: &[&str] = &["PASCAL:CAPITALIZED", "SNAKE:UPPERCASE"];

/// A names file, as `codegen introspect --names-out` writes it: each
/// `CASING:ACRONYMS` format mapped to the schema's names formatted in it.
pub type NamesFile = HashMap<String, HashMap<String, String>>;

/// The schema's names, as the engine formatted them.
#[derive(Clone, Debug, Default)]
pub struct Names {
    formats: NamesFile,
}

impl Names {
    /// Names from a names file, for a schema with `Query.formatIdentifiers`.
    /// For a schema without it, names are empty: older schemas keep the
    /// legacy converter, whatever the file has.
    pub fn new(schema: &Schema, file: NamesFile) -> Self {
        if !has_format_identifiers(schema) {
            return Self::default();
        }
        Self { formats: file }
    }

    /// `name` as the engine formatted it in a casing and acronym style, or
    /// `None` when it wasn't: the schema predates `Query.formatIdentifiers`,
    /// the format wasn't requested, or the name isn't one of the schema's.
    /// Callers fall back to their own conversion then.
    pub fn format(&self, name: &str, casing: Casing, acronyms: Acronyms) -> Option<String> {
        let key = format!("{}:{}", casing.as_str(), acronyms.as_str());
        self.formats
            .get(&key)?
            .get(name)
            .filter(|formatted| !formatted.is_empty())
            .cloned()
    }
}

/// Whether the schema has `Query.formatIdentifiers`: the gate for naming
/// with engine-formatted names.
pub fn has_format_identifiers(schema: &Schema) -> bool {
    let query = schema
        .query_type
        .as_ref()
        .and_then(|q| q.name.as_deref())
        .unwrap_or("Query");
    schema
        .types
        .iter()
        .flatten()
        .flatten()
        .filter(|t| t.full_type.name.as_deref() == Some(query))
        .flat_map(|t| t.full_type.fields.iter().flatten())
        .any(|f| f.name.as_deref() == Some("formatIdentifiers"))
}

#[cfg(test)]
mod tests {
    use dagger_sdk::core::introspection::IntrospectionResponse;
    use pretty_assertions::assert_eq;

    use super::{Acronyms, Casing, Names, NamesFile};

    fn schema(query_fields: &str) -> dagger_sdk::core::introspection::Schema {
        let json = format!(
            r#"{{"__schema": {{
              "queryType": {{"name": "Query"}},
              "mutationType": null, "subscriptionType": null,
              "types": [{{
                "kind": "OBJECT", "name": "Query", "description": null,
                "fields": [{query_fields}],
                "inputFields": null, "interfaces": [],
                "enumValues": null, "possibleTypes": null
              }}],
              "directives": []
            }}}}"#
        );
        serde_json::from_str::<IntrospectionResponse>(&json)
            .unwrap()
            .into_schema()
            .schema
            .unwrap()
    }

    fn field(name: &str) -> String {
        format!(
            r#"{{"name": "{name}", "description": null, "args": [],
                "type": {{"kind": "SCALAR", "name": "String", "ofType": null}},
                "isDeprecated": false, "deprecationReason": null}}"#
        )
    }

    fn file() -> NamesFile {
        serde_json::from_str(
            r#"{
              "PASCAL:CAPITALIZED": {"httpClient": "HttpClient", "empty": ""},
              "SNAKE:UPPERCASE": {"httpClient": "http_client"}
            }"#,
        )
        .unwrap()
    }

    #[test]
    fn names_from_the_file() {
        let names = Names::new(&schema(&field("formatIdentifiers")), file());
        assert_eq!(
            names.format("httpClient", Casing::Pascal, Acronyms::Capitalized),
            Some("HttpClient".to_string())
        );
        assert_eq!(
            names.format("httpClient", Casing::Snake, Acronyms::Uppercase),
            Some("http_client".to_string())
        );
        // A format the file doesn't have, a name it doesn't have, and an
        // empty name all fall back.
        assert_eq!(
            names.format("httpClient", Casing::Pascal, Acronyms::Uppercase),
            None
        );
        assert_eq!(
            names.format("missing", Casing::Snake, Acronyms::Uppercase),
            None
        );
        assert_eq!(
            names.format("empty", Casing::Pascal, Acronyms::Capitalized),
            None
        );
    }

    #[test]
    fn names_need_format_identifiers() {
        let names = Names::new(&schema(&field("version")), file());
        assert_eq!(
            names.format("httpClient", Casing::Snake, Acronyms::Uppercase),
            None
        );

        let names = Names::new(&schema(&field("formatIdentifiers")), NamesFile::new());
        assert_eq!(
            names.format("httpClient", Casing::Snake, Acronyms::Uppercase),
            None
        );
    }
}
