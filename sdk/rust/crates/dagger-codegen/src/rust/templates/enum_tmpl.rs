use dagger_sdk::core::introspection::FullType;
use genco::prelude::rust;
use genco::quote;
use itertools::Itertools;

use crate::functions::CommonFunctions;
use crate::rust::functions::{type_name, variant_name};

fn render_enum_values(funcs: &CommonFunctions, values: &FullType) -> Option<rust::Tokens> {
    let values = values
        .enum_values
        .as_ref()
        .into_iter()
        .flat_map(|values| {
            values
                .iter()
                .filter_map(|val| {
                    val.name
                        .as_ref()
                        .map(|n| (variant_name(funcs.names(), n), val))
                })
                .sorted_by_key(|(name, _)| name.clone())
                .dedup_by(|(a, _), (b, _)| a == b)
                .map(|(name, val)| {
                    // The variant serializes as the schema's value, whatever
                    // its Rust name.
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

pub fn render_enum(funcs: &CommonFunctions, t: &FullType) -> eyre::Result<rust::Tokens> {
    let serialize = rust::import("serde", "Serialize");
    let deserialize = rust::import("serde", "Deserialize");

    Ok(quote! {
        #[derive($serialize, $deserialize, Clone, PartialEq, Debug)]
        pub enum $(type_name(funcs.names(), t.name.as_ref().unwrap())) {
            $(render_enum_values(funcs, t))
        }
    })
}
