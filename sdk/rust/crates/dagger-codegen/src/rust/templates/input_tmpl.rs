use dagger_sdk::core::introspection::{FullType, FullTypeInputFields};
use genco::prelude::rust;
use genco::quote;
use genco::tokens::quoted;
use itertools::Itertools;

use crate::functions::CommonFunctions;
use crate::rust::functions::{format_struct_name, member_name, type_name};

pub fn render_input(funcs: &CommonFunctions, t: &FullType) -> eyre::Result<rust::Tokens> {
    let deserialize = rust::import("serde", "Deserialize");
    let serialize = rust::import("serde", "Serialize");
    Ok(quote! {
        #[derive($serialize, $deserialize, Debug, PartialEq, Clone)]
        pub struct $(type_name(funcs.names(), t.name.as_ref().unwrap())) {
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
    let name = member_name(funcs.names(), &field.input_value.name);
    // serde derives the field's wire name from its Rust name. Keep the wire
    // name it had before engine-formatted names.
    let legacy = format_struct_name(&field.input_value.name);
    let rename = if name != legacy {
        Some(quote! {
            #[serde(rename = $(quoted(legacy.trim_start_matches("r#").to_string())))]
        })
    } else {
        None
    };
    quote! {
        $rename
        pub $name: $(funcs.format_output_type(&field.input_value.type_)),
    }
}
