use convert_case::{Case, Casing};
use dagger_sdk::core::introspection::FullType;
use genco::prelude::rust;
use genco::quote;
use itertools::Itertools;

pub fn format_name(s: &str) -> String {
    s.to_case(Case::Pascal)
}

fn render_enum_values(values: &FullType) -> Option<rust::Tokens> {
    let values = values
        .enum_values
        .as_ref()
        .into_iter()
        .flat_map(|values| {
            values
                .iter()
                .filter_map(|val| val.name.as_ref().map(|n| (format_name(n), val)))
                .sorted_by_key(|(name, _)| name.clone())
                .dedup_by(|(a, _), (b, _)| a == b)
                .map(|(name, val)| {
                    quote! {
                        #[serde(rename = $(val.name.as_ref().map(|n| format!("\"{}\"", n))))]
                        $(name),
                    }
                })
        })
        .collect::<Vec<_>>();

    let mut tokens = rust::Tokens::new();
    for val in values {
        tokens.append(val);
        tokens.push();
    }

    Some(tokens)
}

pub fn render_enum(t: &FullType) -> eyre::Result<rust::Tokens> {
    let serialize = rust::import("serde", "Serialize");
    let deserialize = rust::import("serde", "Deserialize");

    Ok(quote! {
        #[derive($serialize, $deserialize, Clone, PartialEq, Debug)]
        pub enum $(t.name.as_ref()) {
            $(render_enum_values(t))
        }
    })
}
