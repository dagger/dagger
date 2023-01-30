use dagger_core::introspection::{FullType, FullTypeFields, InputValue};
use genco::{prelude::*, quote};

pub fn render_description(t: &FullType) -> Option<rust::Tokens> {
    if let Some(description) = t.description.as_ref() {
        let lines = description.split('\n');
        let output: rust::Tokens = quote! {
            $(for line in lines => $(format!("\n/// {line}")))
        };

        return Some(output);
    }

    None
}

pub fn render_description_from_field(t: &FullTypeFields) -> Option<rust::Tokens> {
    if let Some(description) = t.description.as_ref() {
        let lines = description.split('\n');
        let output: rust::Tokens = quote! {
            $(for line in lines => $(format!("\n/// {line}")))
        };

        return Some(output);
    }

    None
}

pub fn render_description_from_input_value(t: &InputValue, name: &String) -> Option<rust::Tokens> {
    if let Some(description) = t.description.as_ref() {
        if description == "" {
            return None;
        }
        let lines = description.split('\n').collect::<Vec<&str>>();
        let mut output = rust::Tokens::new();

        if let Some(line) = lines.first() {
            output.append(quote! {
                $(format!("/// * `{name}` - {line}"))
            });
            output.push();
        }

        for line in lines {
            output.append(quote! {
                $(format!("///   {line}"))
            });
            output.push();
        }

        return Some(output);
    }

    None
}
