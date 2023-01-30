use convert_case::{Case, Casing};
use genco::{prelude::rust, quote};
use graphql_introspection_query::introspection_response::FullTypeFields;

use super::{
    type_ref,
    utility::{render_description, render_description_from_field},
};

pub fn render_fields(fields: &Vec<FullTypeFields>) -> eyre::Result<Option<rust::Tokens>> {
    let mut collected_fields: Vec<rust::Tokens> = vec![];
    for field in fields.iter() {
        let name = field.name.as_ref().map(|n| n.to_case(Case::Snake)).unwrap();
        let output = render_field_output(field)?;
        let description = render_description_from_field(field);

        collected_fields.push(quote! {
            $(if description.is_some() => $description)
            pub fn $name(&self) -> $output {
                todo!()
            }
        })
    }

    Ok(Some(quote! {
        $(for field in collected_fields => $field $['\n'] )
    }))
}

pub fn render_field_output(field: &FullTypeFields) -> eyre::Result<rust::Tokens> {
    let inner = &field.type_.as_ref().unwrap();
    type_ref::render_type_ref(&inner.type_ref)
}
