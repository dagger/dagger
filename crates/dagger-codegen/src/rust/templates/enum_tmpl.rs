use dagger_core::introspection::FullType;
use genco::prelude::rust;
use genco::quote;

fn render_enum_values(values: &FullType) -> Option<rust::Tokens> {
    let values = values
        .enum_values
        .as_ref()
        .into_iter()
        .map(|values| {
            values
                .into_iter()
                .map(|val| quote! { $(val.name.as_ref()), })
        })
        .flatten()
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

    Ok(quote! {
        #[derive($serialize, Clone, PartialEq, Debug)]
        pub enum $(t.name.as_ref()) {
            $(render_enum_values(t))
        }
    })
}
