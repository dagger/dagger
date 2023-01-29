use genco::prelude::rust;
use genco::prelude::*;
use graphql_introspection_query::introspection_response::{FullType, FullTypeInputFields, TypeRef};

use crate::predicates::{
    is_custom_scalar_type_ref, is_input_object_type, is_list_type, is_required_type_ref,
    is_scalar_type_ref,
};

use super::{utility::render_description, Handler};

pub struct Input;
impl Input {
    fn render_input_fields(
        &self,
        input_fields: &Vec<FullTypeInputFields>,
    ) -> eyre::Result<Option<rust::Tokens>> {
        let mut fields: Vec<(String, rust::Tokens)> = vec![];
        for field in input_fields.iter() {
            fields.push((
                field.input_value.name.clone(),
                self.render_input_field(field)?,
            ));
        }

        Ok(Some(quote! {
            $(for (name, field) in fields => pub $name: $field $['\n'] )
        }))
    }

    fn render_input_field(&self, field: &FullTypeInputFields) -> eyre::Result<rust::Tokens> {
        let inner = &field.input_value.type_;
        self.render_type_ref(inner)
    }

    fn render_type_ref(&self, inner: &TypeRef) -> eyre::Result<rust::Tokens> {
        let extract_of_type = |t: &TypeRef| -> Option<TypeRef> {
            return t.clone().of_type.map(|t| *t);
        };

        if !is_required_type_ref(inner) {
            if let Some(inner_of_type) = extract_of_type(inner) {
                let inner_field = self.render_type_ref(&inner_of_type)?;
                return Ok(quote! {
                    Option<$inner_field>
                });
            }
        }

        if is_list_type(&inner) {
            if let Some(inner_of_type) = extract_of_type(inner) {
                let inner_field = self.render_type_ref(&inner_of_type)?;
                return Ok(quote! {
                    Vec<$inner_field>
                });
            }
        }

        if is_custom_scalar_type_ref(&inner) {
            if let Some(inner_of_type) = extract_of_type(inner) {
                let inner_field = self.render_type_ref(&inner_of_type)?;
                return Ok(quote! {
                    $inner_field
                });
            }
        }

        if is_scalar_type_ref(&inner) {
            let name = match inner.name.as_ref().map(|s| s.as_str()) {
                Some("ID") => "ID",
                Some("Int") => "Int",
                Some("String") => "String",
                Some("Float") => "Float",
                Some("Boolean") => "Boolean",
                Some("Date") => "Date",
                Some("DateTime") => "DateTime",
                Some("Time") => "Time",
                Some("Decimal") => "Decimal",
                Some(n) => n,
                _ => eyre::bail!("missing type in the end of chain"),
            };

            return Ok(quote! {
                $name
            });
        }

        eyre::bail!("could not determine type")
    }
}

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

        let input = rust::import("dagger_core", "Input");

        let fields = match t.input_fields.as_ref() {
            Some(i) => self.render_input_fields(i)?,
            None => None,
        };

        let out = quote! {
            $(if description.is_some() => $description)
            pub struct $name {
                $(if fields.is_some() => $fields)
            }

            impl $input for $name {}
        };

        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use graphql_introspection_query::introspection_response::{
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
    pub name: Option<String>

    pub value: Option<String>
}

impl Input for BuildArg {}
"#;

        let output = input.render(&t).unwrap();

        assert_eq!(output.to_file_string().unwrap(), expected);
    }
}
