//! Format identifiers from the words the engine parsed them into.
//!
//! The engine parses every schema name into words and writes them to the
//! schema JSON under `__identifiers`. Formatting those words, instead of
//! guessing at word boundaries, keeps acronyms and dictionary terms intact:
//! `prerequisiteSHAs` becomes `prerequisite_shas`, not `prerequisite_sh_as`.
//!
//! The rules mirror `engine/naming` and are checked against its shared test
//! vectors (`engine/naming/testdata/vectors.json`).

use dagger_sdk::core::introspection::{IdentifierWord, Identifiers};

/// A convention for joining words into an identifier.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Casing {
    /// Every word capitalized: `HTTPClient`.
    Pascal,
    /// First word lowercase, the rest capitalized: `httpClient`.
    Camel,
    /// Lowercase, joined with `_`: `http_client`.
    Snake,
    /// Uppercase, joined with `_`: `HTTP_CLIENT`.
    ScreamingSnake,
    /// Lowercase, joined with `-`: `http-client`.
    Kebab,
    /// Lowercase, no separator: `httpclient`.
    Flat,
}

/// How acronyms and terms are written where a word starts with a capital.
/// Only affects [`Casing::Pascal`] and [`Casing::Camel`].
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Acronyms {
    /// `HTTPClient`, `IPv6Address`, `GitHubRepo`.
    Uppercase,
    /// `HttpClient`, `Ipv6Address`, `GitHubRepo`.
    Capitalized,
}

const KIND_WORD: &str = "WORD";
const KIND_ACRONYM: &str = "ACRONYM";
const KIND_TERM: &str = "TERM";

fn upper_first(s: &str) -> String {
    let mut chars = s.chars();
    match chars.next() {
        Some(first) => first.to_uppercase().chain(chars).collect(),
        None => String::new(),
    }
}

fn lower(w: &IdentifierWord) -> String {
    format!("{}{}", w.text, w.suffix).to_lowercase()
}

fn upper(w: &IdentifierWord) -> String {
    format!("{}{}", w.text, w.suffix).to_uppercase()
}

/// The word's form where it starts with a capital, suffix included.
fn title(w: &IdentifierWord, acronyms: Acronyms) -> String {
    let text = if w.kind == KIND_WORD || acronyms == Acronyms::Capitalized {
        if w.capitalized.is_empty() {
            upper_first(&w.text.to_lowercase())
        } else {
            w.capitalized.clone()
        }
    } else {
        upper_first(&w.text)
    };
    text + &w.suffix
}

/// Format an identifier's words in a casing.
pub fn format_words(words: &[IdentifierWord], casing: Casing, acronyms: Acronyms) -> String {
    match casing {
        Casing::Pascal => words.iter().map(|w| title(w, acronyms)).collect(),
        Casing::Camel => match words.split_first() {
            Some((first, rest)) => {
                lower(first) + &rest.iter().map(|w| title(w, acronyms)).collect::<String>()
            }
            None => String::new(),
        },
        Casing::Snake => words.iter().map(lower).collect::<Vec<_>>().join("_"),
        Casing::ScreamingSnake => words.iter().map(upper).collect::<Vec<_>>().join("_"),
        Casing::Kebab => words.iter().map(lower).collect::<Vec<_>>().join("-"),
        Casing::Flat => words.iter().map(lower).collect(),
    }
}

/// The words of a schema's names, when the engine provided them.
#[derive(Clone, Debug, Default)]
pub struct Names {
    identifiers: Option<Identifiers>,
}

impl Names {
    pub fn new(identifiers: Option<Identifiers>) -> Self {
        Self { identifiers }
    }

    /// The words of `name`, or `None` when the schema has none for it: the
    /// schema predates identifier words, the name is an introspection name,
    /// or a word has a kind this formatter doesn't know. Callers fall back
    /// to their own conversion then.
    pub fn words(&self, name: &str) -> Option<&[IdentifierWord]> {
        let words = self.identifiers.as_ref()?.get(name)?;
        if words.is_empty()
            || !words
                .iter()
                .all(|w| matches!(w.kind.as_str(), KIND_WORD | KIND_ACRONYM | KIND_TERM))
        {
            return None;
        }
        Some(words)
    }

    /// Format `name` from its words, or `None` when it has none.
    pub fn format(&self, name: &str, casing: Casing, acronyms: Acronyms) -> Option<String> {
        self.words(name)
            .map(|words| format_words(words, casing, acronyms))
    }
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use dagger_sdk::core::introspection::IdentifierWord;
    use pretty_assertions::assert_eq;

    use super::{format_words, Acronyms, Casing, Names};

    /// Locate the engine's shared test vectors: `$DAGGER_NAMING_VECTORS`, or
    /// `engine/naming/testdata/vectors.json` in the repository.
    fn vectors_path() -> PathBuf {
        if let Ok(path) = std::env::var("DAGGER_NAMING_VECTORS") {
            return PathBuf::from(path);
        }
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../../../engine/naming/testdata/vectors.json")
    }

    const FORMATS: &[(&str, Casing, Acronyms)] = &[
        ("PASCAL", Casing::Pascal, Acronyms::Uppercase),
        ("PASCAL_CAPITALIZED", Casing::Pascal, Acronyms::Capitalized),
        ("CAMEL", Casing::Camel, Acronyms::Uppercase),
        ("CAMEL_CAPITALIZED", Casing::Camel, Acronyms::Capitalized),
        ("SNAKE", Casing::Snake, Acronyms::Uppercase),
        (
            "SCREAMING_SNAKE",
            Casing::ScreamingSnake,
            Acronyms::Uppercase,
        ),
        ("KEBAB", Casing::Kebab, Acronyms::Uppercase),
        ("FLAT", Casing::Flat, Acronyms::Uppercase),
    ];

    #[test]
    fn vectors() {
        let path = vectors_path();
        let data = std::fs::read_to_string(&path)
            .unwrap_or_else(|err| panic!("read naming vectors {}: {err}", path.display()));
        let vectors: Vec<serde_json::Value> = serde_json::from_str(&data).unwrap();

        let mut checked = 0;
        let mut failures = Vec::new();
        for vector in &vectors {
            let input = vector["input"].as_str().unwrap();
            // Vectors for names that don't parse have no words.
            let Some(words) = vector.get("words").filter(|w| !w.is_null()) else {
                continue;
            };
            let words: Vec<IdentifierWord> = serde_json::from_value(words.clone()).unwrap();
            for (key, casing, acronyms) in FORMATS {
                let Some(expected) = vector["formats"][key].as_str() else {
                    continue;
                };
                let got = format_words(&words, *casing, *acronyms);
                if got != expected {
                    failures.push(format!("{input} {key}: got {got:?}, want {expected:?}"));
                }
                checked += 1;
            }
        }
        assert!(checked > 0, "no vectors in {}", path.display());
        assert!(failures.is_empty(), "{}", failures.join("\n"));
    }

    fn word(kind: &str, text: &str, suffix: &str, capitalized: &str) -> IdentifierWord {
        IdentifierWord {
            kind: kind.into(),
            text: text.into(),
            suffix: suffix.into(),
            capitalized: capitalized.into(),
        }
    }

    #[test]
    fn format_words_cases() {
        let list_prs = [
            word("WORD", "list", "", "List"),
            word("ACRONYM", "PR", "s", "Pr"),
        ];
        assert_eq!(
            format_words(&list_prs, Casing::Camel, Acronyms::Uppercase),
            "listPRs"
        );
        assert_eq!(
            format_words(&list_prs, Casing::Pascal, Acronyms::Capitalized),
            "ListPrs"
        );
        assert_eq!(
            format_words(&list_prs, Casing::ScreamingSnake, Acronyms::Uppercase),
            "LIST_PRS"
        );
        assert_eq!(
            format_words(&list_prs, Casing::Snake, Acronyms::Uppercase),
            "list_prs"
        );
        // A missing capitalized form falls back to the first letter uppercased.
        assert_eq!(
            format_words(
                &[word("ACRONYM", "HTTPX", "", "")],
                Casing::Pascal,
                Acronyms::Capitalized
            ),
            "Httpx"
        );
        assert_eq!(format_words(&[], Casing::Camel, Acronyms::Uppercase), "");
    }

    #[test]
    fn names_fall_back_when_words_are_missing() {
        let none = Names::new(None);
        assert_eq!(
            none.format("httpClient", Casing::Snake, Acronyms::Uppercase),
            None
        );

        let names = Names::new(Some(
            [
                (
                    "httpClient".to_string(),
                    vec![
                        word("ACRONYM", "HTTP", "", "Http"),
                        word("WORD", "client", "", "Client"),
                    ],
                ),
                ("empty".to_string(), vec![]),
                (
                    "future".to_string(),
                    vec![word("SOMETHING", "future", "", "Future")],
                ),
            ]
            .into_iter()
            .collect(),
        ));
        assert_eq!(
            names.format("httpClient", Casing::Pascal, Acronyms::Capitalized),
            Some("HttpClient".to_string())
        );
        assert_eq!(
            names.format("missing", Casing::Snake, Acronyms::Uppercase),
            None
        );
        assert_eq!(
            names.format("empty", Casing::Snake, Acronyms::Uppercase),
            None
        );
        assert_eq!(
            names.format("future", Casing::Snake, Acronyms::Uppercase),
            None
        );
    }
}
