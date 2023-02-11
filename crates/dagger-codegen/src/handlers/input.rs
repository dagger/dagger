use dagger_core::introspection::FullType;
use genco::prelude::rust;
use genco::prelude::*;

use crate::predicates::is_input_object_type;

use super::{input_field::render_input_fields, utility::render_description, Handler};

pub struct Input;

impl Handler for Input {
    fn predicate(&self, t: &FullType) -> bool {
        is_input_object_type(t)
    }

    fn render(&self, t: &FullType) -> eyre::Result<rust::Tokens> {
        let name = t
            .name
            .as_ref()
            .ok_or(eyre::anyhow!("could not find name"))?;
        let description = render_description(t);

        //let input = rust::import("dagger_core", "Input");

        let fields = match t.input_fields.as_ref() {
            Some(i) => render_input_fields(i)?,
            None => None,
        };

        let out = quote! {
            $(if description.is_some() => $description)
            pub struct $name {
                $(if fields.is_some() => $fields)
            }
        };

        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use dagger_core::introspection::{
        FullType, FullTypeInputFields, InputValue, TypeRef, __TypeKind,
    };
    use pretty_assertions::assert_eq;

    use crate::handlers::Handler;

    use super::Input;

    #[test]
    fn can_gen_input() {
        let input = Input {};
        let t = FullType {
            kind: Some(__TypeKind::INPUT_OBJECT),
            name: Some("BuildArg".into()),
            description: None,
            input_fields: Some(vec![
                FullTypeInputFields {
                    input_value: InputValue {
                        name: "name".into(),
                        description: None,
                        type_: TypeRef {
                            name: None,
                            kind: Some(__TypeKind::NON_NULL),
                            of_type: Some(Box::new(TypeRef {
                                kind: Some(__TypeKind::SCALAR),
                                name: Some("String".into()),
                                of_type: None,
                            })),
                        },
                        default_value: None,
                    },
                },
                FullTypeInputFields {
                    input_value: InputValue {
                        name: "value".into(),
                        description: None,
                        type_: TypeRef {
                            name: None,
                            kind: Some(__TypeKind::NON_NULL),
                            of_type: Some(Box::new(TypeRef {
                                kind: Some(__TypeKind::SCALAR),
                                name: Some("String".into()),
                                of_type: None,
                            })),
                        },
                        default_value: None,
                    },
                },
            ]),
            interfaces: None,
            enum_values: None,
            possible_types: None,
            fields: None,
        };

        let expected = r#"use dagger_core::Input;

pub struct BuildArg {
    pub name: Option<String>,

    pub value: Option<String>,
}

impl Input for BuildArg {}
"#;

        let output = input.render(&t).unwrap();

        assert_eq!(output.to_file_string().unwrap(), expected);
    }
}
