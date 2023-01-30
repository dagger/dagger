use dagger_core::introspection::FullType;
use genco::{prelude::rust, quote};

use crate::predicates::is_object_type;

use super::{fields, input_field, utility::render_description, Handler};

pub struct Object;

impl Handler for Object {
    fn predicate(&self, t: &FullType) -> bool {
        is_object_type(t)
    }

    fn render(&self, t: &FullType) -> eyre::Result<rust::Tokens> {
        let name = t
            .name
            .as_ref()
            .ok_or(eyre::anyhow!("could not find name"))?;
        let description = render_description(t);

        let input = rust::import("dagger_core", "Input");

        let fields = match t.fields.as_ref() {
            Some(i) => fields::render_fields(i)?,
            None => None,
        };

        let input_fields = match t.input_fields.as_ref() {
            Some(i) => input_field::render_input_fields(i)?,
            None => None,
        };

        let out = quote! {
            $(if description.is_some() => $description)
            pub struct $name {
                $(if input_fields.is_some() => $input_fields)
            }

            impl $name {
                $(if fields.is_some() => $fields)
            }

            impl $input for $name {}
        };

        Ok(out)
    }
}

#[cfg(test)]
mod tests {
    use dagger_core::introspection::{
        FullType, FullTypeFields, FullTypeFieldsType, TypeRef, __TypeKind,
    };
    use pretty_assertions::assert_eq;

    use crate::handlers::Handler;

    use super::Object;

    #[test]
    fn can_render_object() {
        let t: FullType = FullType {
            kind: Some(__TypeKind::OBJECT),
            name: Some("CacheVolume".into()),
            description: Some("A directory whose contents persists across sessions".into()),
            fields: Some(vec![FullTypeFields {
                name: Some("id".into()),
                description: None,
                args: None,
                type_: Some(FullTypeFieldsType {
                    type_ref: TypeRef {
                        kind: Some(__TypeKind::NON_NULL),
                        name: None,
                        of_type: Some(Box::new(TypeRef {
                            kind: Some(__TypeKind::SCALAR),
                            name: Some("CacheID".into()),
                            of_type: None,
                        })),
                    },
                }),
                is_deprecated: Some(false),
                deprecation_reason: None,
            }]),
            input_fields: None,
            interfaces: None,
            enum_values: None,
            possible_types: None,
        };
        let expected = r#"use dagger_core::Input;


/// A directory whose contents persists across sessions
pub struct CacheVolume {}

impl CacheVolume {
    pub fn id(
        &self,
    ) -> Option<CacheID> {
        todo!()
    }
}

impl Input for CacheVolume {}
"#;
        let handler = Object {};
        let obj = handler.render(&t).unwrap();
        let actual = obj.to_file_string().unwrap();

        assert_eq!(actual, expected);
    }
}
