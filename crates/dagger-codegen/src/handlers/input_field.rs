use genco::{prelude::rust, quote};
use graphql_introspection_query::introspection_response::FullTypeInputFields;

use super::type_ref;

pub fn render_input_fields(
    input_fields: &Vec<FullTypeInputFields>,
) -> eyre::Result<Option<rust::Tokens>> {
    let mut fields: Vec<(String, rust::Tokens)> = vec![];
    for field in input_fields.iter() {
        fields.push((field.input_value.name.clone(), render_input_field(field)?));
    }

    Ok(Some(quote! {
        $(for (name, field) in fields => pub $name: $field, $['\n'] )
    }))
}

pub fn render_input_field(field: &FullTypeInputFields) -> eyre::Result<rust::Tokens> {
    let inner = &field.input_value.type_;
    type_ref::render_type_ref(inner)
}
