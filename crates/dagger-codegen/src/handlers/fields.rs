use convert_case::{Case, Casing};
use dagger_core::introspection::{FullTypeFields, FullTypeFieldsArgs};
use genco::{prelude::rust, quote};

use super::{
    type_ref::{self, render_type_ref},
    utility::{render_description_from_field, render_description_from_input_value},
};

pub fn render_fields(fields: &Vec<FullTypeFields>) -> eyre::Result<Option<rust::Tokens>> {
    let mut collected_fields: Vec<rust::Tokens> = vec![];
    for field in fields.iter() {
        let name = field.name.as_ref().map(|n| n.to_case(Case::Snake)).unwrap();
        let output = render_field_output(field)?;
        let description = render_description_from_field(field);
        let args = match field.args.as_ref() {
            Some(a) => render_args(a),
            None => None,
        };

        let mut tkns = rust::Tokens::new();
        if let Some(desc) = &description {
            tkns.append(desc);
            tkns.push()
        }

        if let Some(args) = args.as_ref() {
            if let Some(desc) = args.description.as_ref() {
                tkns.append("/// # Arguments");
                tkns.push();
                tkns.append("///");
                tkns.push();
                tkns.append(desc);
                tkns.push();
            }
        }

        tkns.append(quote! {
            pub fn $name(
                &self,
                $(if let Some(args) = args.as_ref() => $(&args.args))
            ) -> $output {
                todo!()
            }
        });

        collected_fields.push(tkns);
    }

    Ok(Some(quote! {
        $(for field in collected_fields => $field $['\n'] )
    }))
}

struct Arg {
    name: String,
    description: Option<rust::Tokens>,
    type_: rust::Tokens,
}

struct CollectedArgs {
    description: Option<rust::Tokens>,
    args: rust::Tokens,
}

fn render_args(args: &[Option<FullTypeFieldsArgs>]) -> Option<CollectedArgs> {
    let mut collected_args: Vec<Arg> = vec![];

    for arg in args {
        if let Some(arg) = arg.as_ref().map(|a| &a.input_value) {
            let name = arg.name.clone();
            let description = render_description_from_input_value(&arg, &name);
            let t = render_type_ref(&arg.type_).unwrap();

            collected_args.push(Arg {
                name,
                description,
                type_: t,
            })
        }
    }

    if collected_args.len() > 0 {
        let mut collected_arg = CollectedArgs {
            description: Some(rust::Tokens::new()),
            args: rust::Tokens::new(),
        };

        for arg in collected_args {
            if let Some(desc) = arg.description {
                if let Some(inner_desc) = collected_arg.description.as_mut() {
                    inner_desc.append(desc);
                    inner_desc.push();
                }
            }

            collected_arg.args.append(quote! {
                $(arg.name.to_case(Case::Snake)): $(arg.type_),
            });
            collected_arg.args.push();
        }

        if let Some(desc) = collected_arg.description.as_ref() {
            if desc.is_empty() {
                collected_arg.description = None;
            }
        }

        Some(collected_arg)
    } else {
        None
    }
}

pub fn render_field_output(field: &FullTypeFields) -> eyre::Result<rust::Tokens> {
    let inner = &field.type_.as_ref().unwrap();
    type_ref::render_type_ref(&inner.type_ref)
}
