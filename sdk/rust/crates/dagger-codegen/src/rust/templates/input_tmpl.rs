use dagger_sdk::core::introspection::{FullType, FullTypeInputFields};
use genco::prelude::rust;
use genco::quote;
use itertools::Itertools;

use crate::functions::CommonFunctions;
use crate::rust::functions::{format_name, format_struct_name};

pub fn render_input(funcs: &CommonFunctions, t: &FullType) -> eyre::Result<rust::Tokens> {
    let deserialize = rust::import("serde", "Deserialize");
    let serialize = rust::import("serde", "Serialize");
    Ok(quote! {
        #[derive($serialize, $deserialize, Debug, PartialEq, Clone)]
        pub struct $(format_name(t.name.as_ref().unwrap())) {
            $(render_input_fields(funcs, t.input_fields.as_ref().unwrap_or(&Vec::new())  ))
        }
    })
}

pub fn render_input_fields(
    funcs: &CommonFunctions,
    fields: &[FullTypeInputFields],
) -> Option<rust::Tokens> {
    let rendered_fields = fields
        .iter()
        .sorted_by_key(|val| &val.input_value.name)
        .map(|f| render_input_field(funcs, f));

    if rendered_fields.len() == 0 {
        None
    } else {
        Some(quote! {
            $(for field in rendered_fields join ($['\r']) => $field)
        })
    }
}

pub fn render_input_field(funcs: &CommonFunctions, field: &FullTypeInputFields) -> rust::Tokens {
    quote! {
        pub $(format_struct_name(&field.input_value.name)): $(funcs.format_output_type(&field.input_value.type_)),
    }
}
