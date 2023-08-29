use std::sync::Arc;

use dagger_sdk::core::introspection::{FullType, FullTypeFields, InputValue, TypeRef, __TypeKind};
use eyre::ContextCompat;

use crate::utility::OptionExt;

pub trait FormatTypeFuncs {
    fn format_kind_list(&self, representation: &str, input: bool, immutable: bool) -> String;
    fn format_kind_scalar_string(&self, representation: &str, input: bool) -> String;
    fn format_kind_scalar_int(&self, representation: &str) -> String;
    fn format_kind_scalar_float(&self, representation: &str) -> String;
    fn format_kind_scalar_boolean(&self, representation: &str) -> String;
    fn format_kind_scalar_default(
        &self,
        representation: &str,
        ref_name: &str,
        input: bool,
    ) -> String;
    fn format_kind_object(&self, representation: &str, ref_name: &str) -> String;
    fn format_kind_input_object(&self, representation: &str, ref_name: &str) -> String;
    fn format_kind_enum(&self, representation: &str, ref_name: &str) -> String;
}

pub type DynFormatTypeFuncs = Arc<dyn FormatTypeFuncs + Send + Sync>;

pub struct CommonFunctions {
    format_type_funcs: DynFormatTypeFuncs,
}

impl CommonFunctions {
    pub fn new(funcs: DynFormatTypeFuncs) -> Self {
        Self {
            format_type_funcs: funcs,
        }
    }

    pub fn format_input_type(&self, t: &TypeRef) -> String {
        self.format_type(t, true, false)
    }

    pub fn format_output_type(&self, t: &TypeRef) -> String {
        self.format_type(t, false, false)
    }

    pub fn format_immutable_input_type(&self, t: &TypeRef) -> String {
        self.format_type(t, true, true)
    }

    fn format_type(&self, t: &TypeRef, input: bool, immutable: bool) -> String {
        let mut representation = String::new();
        let mut r = Some(t.clone());
        while r.is_some() {
            return match r.as_ref() {
                Some(rf) => match rf.kind.as_ref() {
                    Some(k) => match k {
                        __TypeKind::SCALAR => match Scalar::from(rf) {
                            Scalar::Int => self
                                .format_type_funcs
                                .format_kind_scalar_int(&mut representation),
                            Scalar::Float => self
                                .format_type_funcs
                                .format_kind_scalar_float(&mut representation),
                            Scalar::String => {
                                if immutable {
                                    "&'a str".into()
                                } else {
                                    self.format_type_funcs
                                        .format_kind_scalar_string(&mut representation, input)
                                }
                            }
                            Scalar::Boolean => self
                                .format_type_funcs
                                .format_kind_scalar_boolean(&mut representation),
                            Scalar::Default => self.format_type_funcs.format_kind_scalar_default(
                                &mut representation,
                                rf.name.as_ref().unwrap(),
                                input,
                            ),
                        },
                        __TypeKind::OBJECT => self
                            .format_type_funcs
                            .format_kind_object(&mut representation, rf.name.as_ref().unwrap()),
                        __TypeKind::ENUM => self
                            .format_type_funcs
                            .format_kind_enum(&mut representation, rf.name.as_ref().unwrap()),
                        __TypeKind::INPUT_OBJECT => {
                            self.format_type_funcs.format_kind_input_object(
                                &mut representation,
                                &rf.name.as_ref().unwrap(),
                            )
                        }
                        __TypeKind::LIST => {
                            let mut inner_type = rf
                                .of_type
                                .as_ref()
                                .map(|t| t.clone())
                                .map(|t| *t)
                                .map(|t| self.format_type(&t, input, immutable))
                                .context("could not get inner type of list")
                                .unwrap();

                            representation = self.format_type_funcs.format_kind_list(
                                &mut inner_type,
                                input,
                                immutable,
                            );

                            return representation;
                        }
                        __TypeKind::NON_NULL => {
                            r = rf.of_type.as_ref().map(|t| t.clone()).map(|t| *t);
                            continue;
                        }
                        __TypeKind::Other(_) => {
                            r = rf.of_type.as_ref().map(|t| t.clone()).map(|t| *t);
                            continue;
                        }
                        __TypeKind::INTERFACE => break,
                        __TypeKind::UNION => break,
                    },
                    None => break,
                },
                None => break,
            };
        }

        representation
    }
}

pub enum Scalar {
    Int,
    Float,
    String,
    Boolean,
    Default,
}

impl From<&TypeRef> for Scalar {
    fn from(value: &TypeRef) -> Self {
        match value.name.as_ref().map(|n| n.as_str()) {
            Some("Int") => Scalar::Int,
            Some("Float") => Scalar::Float,
            Some("String") => Scalar::String,
            Some("Boolean") => Scalar::Boolean,
            Some(_) => Scalar::Default,
            None => Scalar::Default,
        }
    }
}

#[allow(dead_code)]
pub fn get_type_from_name<'t>(types: &'t [FullType], name: &'t str) -> Option<&'t FullType> {
    types
        .into_iter()
        .find(|t| t.name.as_ref().map(|s| s.as_str()) == Some(name))
}

pub fn type_ref_is_optional(type_ref: &TypeRef) -> bool {
    type_ref
        .kind
        .pipe(|k| *k != __TypeKind::NON_NULL)
        .unwrap_or(false)
}

pub fn type_field_has_optional(field: &FullTypeFields) -> bool {
    field
        .args
        .pipe(|a| {
            a.iter()
                .map(|a| a.pipe(|a| &a.input_value))
                .flatten()
                .collect::<Vec<_>>()
        })
        .pipe(|s| input_values_has_optionals(s.as_slice()))
        .unwrap_or(false)
}

pub fn type_ref_is_scalar(type_ref: &TypeRef) -> bool {
    let mut type_ref = type_ref.clone();
    if type_ref
        .kind
        .pipe(|k| *k == __TypeKind::NON_NULL)
        .unwrap_or(false)
    {
        type_ref = *type_ref.of_type.unwrap().clone();
    }

    type_ref
        .kind
        .pipe(|k| *k == __TypeKind::SCALAR)
        .unwrap_or(false)
}

pub fn type_ref_is_enum(type_ref: &TypeRef) -> bool {
    let mut type_ref = type_ref.clone();
    if type_ref
        .kind
        .pipe(|k| *k == __TypeKind::NON_NULL)
        .unwrap_or(false)
    {
        type_ref = *type_ref.of_type.unwrap().clone();
    }

    type_ref
        .kind
        .pipe(|k| *k == __TypeKind::ENUM)
        .unwrap_or(false)
}

pub fn type_ref_is_object(type_ref: &TypeRef) -> bool {
    let mut type_ref = type_ref.clone();
    if type_ref
        .kind
        .pipe(|k| *k == __TypeKind::NON_NULL)
        .unwrap_or(false)
    {
        type_ref = *type_ref.of_type.unwrap().clone();
    }

    type_ref
        .kind
        .pipe(|k| *k == __TypeKind::OBJECT)
        .unwrap_or(false)
}

pub fn type_ref_is_list(type_ref: &TypeRef) -> bool {
    let mut type_ref = type_ref.clone();
    if type_ref
        .kind
        .pipe(|k| *k == __TypeKind::NON_NULL)
        .unwrap_or(false)
    {
        type_ref = *type_ref.of_type.unwrap().clone();
    }

    type_ref
        .kind
        .pipe(|k| *k == __TypeKind::LIST)
        .unwrap_or(false)
}

pub fn type_ref_is_id(type_ref: &TypeRef) -> bool {
    let mut type_ref = type_ref.clone();
    if type_ref
        .kind
        .pipe(|k| *k == __TypeKind::NON_NULL)
        .unwrap_or(false)
    {
        type_ref = *type_ref.of_type.unwrap().clone();
    }

    type_ref
        .name
        .map(|n| n.to_lowercase().ends_with("id"))
        .unwrap_or(false)
}

pub fn type_ref_is_list_of_objects(type_ref: &TypeRef) -> bool {
    let mut type_ref = type_ref.clone();
    if type_ref
        .kind
        .pipe(|k| *k == __TypeKind::NON_NULL)
        .unwrap_or(false)
    {
        type_ref = *type_ref.of_type.unwrap().clone();
    }

    if type_ref
        .kind
        .pipe(|k| *k == __TypeKind::LIST)
        .unwrap_or(false)
    {
        type_ref = *type_ref.of_type.unwrap().clone();
    }

    type_ref_is_object(&type_ref)
}

pub fn input_values_has_optionals(input_values: &[&InputValue]) -> bool {
    input_values
        .into_iter()
        .map(|k| type_ref_is_optional(&k.type_))
        .filter(|k| *k)
        .collect::<Vec<_>>()
        .len()
        > 0
}

#[allow(dead_code)]
pub fn input_values_is_empty(input_values: &[InputValue]) -> bool {
    input_values.len() > 0
}

#[cfg(test)]
mod test {
    use dagger_sdk::core::introspection::{FullType, InputValue, TypeRef, __TypeKind};
    use pretty_assertions::assert_eq;

    use crate::functions::{input_values_has_optionals, type_ref_is_optional};

    use super::get_type_from_name;

    #[test]
    fn get_type_from_name_has_no_item() {
        let input = vec![];
        let output = get_type_from_name(&input, "some-name");

        assert_eq!(output.is_none(), true);
    }

    #[test]
    fn get_type_from_name_has_item() {
        let name = "some-name";
        let input = vec![FullType {
            kind: None,
            name: Some(name.to_string()),
            description: None,
            fields: None,
            input_fields: None,
            interfaces: None,
            enum_values: None,
            possible_types: None,
        }];
        let output = get_type_from_name(&input, name);

        assert_eq!(output.is_some(), true);
    }

    #[test]
    fn get_type_from_name_has_item_multiple_entries() {
        let name = "some-name";
        let input = vec![
            FullType {
                kind: None,
                name: Some(name.to_string()),
                description: None,
                fields: None,
                input_fields: None,
                interfaces: None,
                enum_values: None,
                possible_types: None,
            },
            FullType {
                kind: None,
                name: Some(name.to_string()),
                description: None,
                fields: None,
                input_fields: None,
                interfaces: None,
                enum_values: None,
                possible_types: None,
            },
        ];
        let output = get_type_from_name(&input, name);

        assert_eq!(output.is_some(), true);
    }

    #[test]
    fn type_ref_is_optional_has_none() {
        let input = TypeRef {
            kind: None,
            name: None,
            of_type: None,
        };
        let output = type_ref_is_optional(&input);

        assert_eq!(output, false);
    }

    #[test]
    fn type_ref_is_optional_is_required() {
        let input = TypeRef {
            kind: Some(__TypeKind::NON_NULL),
            name: None,
            of_type: None,
        };
        let output = type_ref_is_optional(&input);

        assert_eq!(output, false);
    }

    #[test]
    fn type_ref_is_optional_is_optional() {
        let input = TypeRef {
            kind: Some(__TypeKind::OBJECT),
            name: None,
            of_type: None,
        };
        let output = type_ref_is_optional(&input);

        assert_eq!(output, true);
    }

    #[test]
    fn input_values_has_optionals_none() {
        let input = vec![];

        let output = input_values_has_optionals(&input);

        assert_eq!(output, false);
    }

    #[test]
    fn input_values_has_optionals_has_optional() {
        let input = vec![
            InputValue {
                name: "some-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::NON_NULL),
                    name: None,
                    of_type: None,
                },
                default_value: None,
            },
            InputValue {
                name: "some-other-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::OBJECT),
                    name: None,
                    of_type: None,
                },
                default_value: None,
            },
        ];

        let output = input_values_has_optionals(input.iter().collect::<Vec<_>>().as_slice());

        assert_eq!(output, true);
    }

    #[test]
    fn input_values_has_optionals_is_required() {
        let input = vec![
            InputValue {
                name: "some-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::NON_NULL),
                    name: None,
                    of_type: None,
                },
                default_value: None,
            },
            InputValue {
                name: "some-other-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::NON_NULL),
                    name: None,
                    of_type: None,
                },
                default_value: None,
            },
        ];

        let output = input_values_has_optionals(input.iter().collect::<Vec<_>>().as_slice());

        assert_eq!(output, false);
    }
}
