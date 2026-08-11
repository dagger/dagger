//! Shared syntactic metadata grammar consumed by all authoring attributes.

use std::collections::BTreeMap;

use proc_macro2::TokenStream;
use quote::ToTokens;
use syn::parse::Parser;
use syn::{Attribute, Error, Result, Visibility};

const MARKERS: &[&str] = &[
    "constructor",
    "context",
    "field",
    "function",
    "root",
    "state",
];

const VALUES: &[&str] = &[
    "cache",
    "default",
    "default_address",
    "default_path",
    "deprecated",
    "doc",
    "ignore",
    "rename",
    "role",
    "target",
];

/// Canonically ordered metadata retained in authoring fingerprints.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub(crate) struct Metadata(BTreeMap<String, String>);

impl Metadata {
    pub(crate) fn parse_args(tokens: TokenStream) -> Result<Self> {
        let mut metadata = Self::default();
        let parser = syn::meta::parser(|nested| metadata.parse_nested(nested));
        parser.parse2(tokens)?;
        Ok(metadata)
    }

    pub(crate) fn take_from(attributes: &mut Vec<Attribute>) -> Result<Self> {
        let mut metadata = Self::default();
        let mut retained = Vec::with_capacity(attributes.len());
        for attribute in attributes.drain(..) {
            if attribute.path().is_ident("dagger") {
                attribute.parse_nested_meta(|nested| metadata.parse_nested(nested))?;
            } else {
                retained.push(attribute);
            }
        }
        *attributes = retained;
        Ok(metadata)
    }

    fn parse_nested(&mut self, nested: syn::meta::ParseNestedMeta<'_>) -> Result<()> {
        let Some(ident) = nested.path.get_ident() else {
            return Err(nested.error("Dagger metadata names must be single identifiers"));
        };
        let name = ident.to_string();
        if self.0.contains_key(&name) {
            return Err(nested.error(format!("duplicate Dagger metadata `{name}`")));
        }
        for (left, right) in [("field", "state"), ("constructor", "function")] {
            if (name == left && self.has(right)) || (name == right && self.has(left)) {
                return Err(
                    nested.error(format!("Dagger metadata `{left}` conflicts with `{right}`"))
                );
            }
        }
        let context_compatible = matches!(name.as_str(), "context" | "doc" | "deprecated");
        if (name == "context"
            && self
                .0
                .keys()
                .any(|existing| !matches!(existing.as_str(), "context" | "doc" | "deprecated")))
            || (self.has("context") && !context_compatible)
        {
            return Err(nested.error("a Dagger context parameter cannot also carry value metadata"));
        }

        let value = if MARKERS.contains(&name.as_str()) {
            if nested.input.peek(syn::Token![=]) || nested.input.peek(syn::token::Paren) {
                return Err(nested.error(format!("Dagger marker `{name}` accepts no value")));
            }
            "true".to_owned()
        } else if VALUES.contains(&name.as_str()) {
            if nested.input.peek(syn::Token![=]) {
                let value = nested.value()?.parse::<syn::Expr>()?;
                canonical_tokens(&value)
            } else if nested.input.peek(syn::token::Paren) {
                let content;
                syn::parenthesized!(content in nested.input);
                canonical_tokens(&content.parse::<TokenStream>()?)
            } else {
                return Err(nested.error(format!("Dagger metadata `{name}` requires a value")));
            }
        } else {
            return Err(nested.error(format!("unknown Dagger metadata `{name}`")));
        };
        self.0.insert(name, value);
        Ok(())
    }

    pub(crate) fn has(&self, name: &str) -> bool {
        self.0.contains_key(name)
    }

    pub(crate) fn canonical(&self) -> String {
        self.0
            .iter()
            .map(|(name, value)| format!("{}:{name}={value}", name.len()))
            .collect::<Vec<_>>()
            .join("|")
    }
}

pub(crate) fn require_export_visibility(visibility: &Visibility) -> Result<()> {
    match visibility {
        Visibility::Public(_) => Ok(()),
        Visibility::Restricted(restricted) if restricted.path.is_ident("crate") => Ok(()),
        _ => Err(Error::new_spanned(
            visibility,
            "a Dagger-exported type must be `pub(crate)` or `pub`",
        )),
    }
}

pub(crate) fn canonical_tokens(tokens: &impl ToTokens) -> String {
    tokens.to_token_stream().to_string()
}

#[cfg(test)]
mod tests {
    use quote::quote;

    use super::Metadata;

    #[test]
    fn metadata_is_order_independent_and_duplicate_strict() {
        let left = Metadata::parse_args(quote!(rename = "hello", root)).expect("valid metadata");
        let right = Metadata::parse_args(quote!(root, rename = "hello")).expect("valid metadata");
        assert_eq!(left.canonical(), right.canonical());
        assert!(Metadata::parse_args(quote!(root, root)).is_err());
        assert!(Metadata::parse_args(quote!(unknown)).is_err());
    }

    #[test]
    fn every_shared_metadata_spelling_is_explicitly_recognized() {
        for marker in [
            quote!(constructor),
            quote!(context),
            quote!(field),
            quote!(function),
            quote!(root),
            quote!(state),
        ] {
            assert!(Metadata::parse_args(marker).is_ok());
        }
        for value in [
            quote!(cache = "never"),
            quote!(default = false),
            quote!(default_address = "tcp://service:8080"),
            quote!(default_path = "/src"),
            quote!(deprecated = "use replacement"),
            quote!(doc = "documentation"),
            quote!(ignore = ["target"]),
            quote!(rename = "wireName"),
            quote!(role = "check"),
            quote!(target = "linux/amd64"),
        ] {
            assert!(Metadata::parse_args(value).is_ok());
        }
        assert!(Metadata::parse_args(quote!(field, state)).is_err());
        assert!(Metadata::parse_args(quote!(constructor, function)).is_err());
        assert!(Metadata::parse_args(quote!(context, default = false)).is_err());
    }
}
