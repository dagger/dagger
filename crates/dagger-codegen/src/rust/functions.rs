use convert_case::{Case, Casing};
use dagger_core::introspection::{FullTypeFields, TypeRef};
use genco::prelude::rust;
use genco::quote;
use genco::tokens::quoted;

use crate::functions::{
    type_field_has_optional, type_ref_is_list, type_ref_is_list_of_objects, type_ref_is_object,
    type_ref_is_optional, type_ref_is_scalar, CommonFunctions, Scalar,
};
use crate::utility::OptionExt;

pub fn format_name(s: &str) -> String {
    s.to_case(Case::Pascal)
}

pub fn format_struct_name(s: &str) -> String {
    s.to_case(Case::Snake)
}

pub fn field_options_struct_name(field: &FullTypeFields) -> Option<String> {
    field
        .parent_type
        .as_ref()
        .map(|p| p.name.as_ref().map(|n| format_name(n)))
        .flatten()
        .zip(field.name.as_ref().map(|n| format_name(n)))
        .map(|(parent_name, field_name)| format!("{parent_name}{field_name}Opts"))
}

pub fn format_function(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    let signature = quote! {
        pub fn $(field.name.pipe(|n | format_struct_name(n)))
    };
    let args = format_function_args(funcs, field);

    let output_type = field
        .type_
        .pipe(|t| &t.type_ref)
        .pipe(|t| render_output_type(funcs, t));

    if let Some((args, true)) = args {
        let required_args = format_required_function_args(funcs, field);
        Some(quote! {
            $(&signature)(
                $(required_args)
            ) -> $(output_type.as_ref()) {
                let mut query = self.selection.select($(quoted(field.name.as_ref())));

                $(render_required_args(funcs, field))

                $(render_execution(funcs, field))
            }

            $(&signature)_opts(
                $args
            ) -> $(output_type) {
                let mut query = self.selection.select($(quoted(field.name.as_ref())));

                $(render_required_args(funcs, field))
                $(render_optional_args(funcs, field))

                $(render_execution(funcs, field))
            }
        })
    } else {
        Some(quote! {
            $(signature)(
                $(if let Some((args, _)) = args => $args)
            ) -> $(output_type) {
                let mut query = self.selection.select($(quoted(field.name.as_ref())));

                $(render_required_args(funcs, field))
                $(render_optional_args(funcs, field))

                $(render_execution(funcs, field))
            }
        })
    }
}

fn render_required_args(_funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    if let Some(args) = field.args.as_ref() {
        let args = args
            .into_iter()
            .map(|a| {
                a.as_ref().and_then(|s| {
                    if type_ref_is_optional(&s.input_value.type_) {
                        return None;
                    }

                    let n = format_struct_name(&s.input_value.name);
                    let name = &s.input_value.name;

                    if type_ref_is_scalar(&s.input_value.type_) {
                        if let Scalar::String =
                            Scalar::from(&*s.input_value.type_.of_type.as_ref().unwrap().clone())
                        {
                            return Some(quote! {
                                query = query.arg($(quoted(name)), $(&n).into());
                            });
                        }
                    }

                    if type_ref_is_list(&s.input_value.type_) {
                        let inner = *s
                            .input_value
                            .type_
                            .of_type
                            .as_ref()
                            .unwrap()
                            .clone()
                            .of_type
                            .as_ref()
                            .unwrap()
                            .clone();
                        println!("type: {:?}", inner);
                        if type_ref_is_scalar(&inner) {
                            if let Scalar::String =
                                Scalar::from(&*inner.of_type.as_ref().unwrap().clone())
                            {
                                return Some(quote! {
                                    query = query.arg($(quoted(name)), $(&n).into_iter().map(|i| i.into()).collect::<Vec<String>>());
                                });
                            }
                        }
                    }

                    Some(quote! {
                        query = query.arg($(quoted(name)), $(n));
                    })
                })
            })
            .flatten()
            .collect::<Vec<_>>();
        let required_args = quote! {
            $(for arg in args join ($['\r']) => $arg)
        };

        Some(required_args)
    } else {
        None
    }
}

fn render_optional_args(_funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    if let Some(args) = field.args.as_ref() {
        let args = args
            .into_iter()
            .map(|a| {
                a.as_ref().and_then(|s| {
                    if !type_ref_is_optional(&s.input_value.type_) {
                        return None;
                    }

                    let n = format_struct_name(&s.input_value.name);
                    let name = &s.input_value.name;

                    Some(quote! {
                        if let Some($(&n)) = opts.$(&n) {
                            query = query.arg($(quoted(name)), $(&n));
                        }
                    })
                })
            })
            .flatten()
            .collect::<Vec<_>>();

        if args.len() == 0 {
            return None;
        }

        let required_args = quote! {
            $(for arg in args join ($['\r']) => $arg)
        };

        Some(required_args)
    } else {
        None
    }
}

fn render_output_type(funcs: &CommonFunctions, type_ref: &TypeRef) -> rust::Tokens {
    let output_type = funcs.format_output_type(type_ref);

    if type_ref_is_object(type_ref) || type_ref_is_list_of_objects(type_ref) {
        return quote! {
            $(output_type)
        };
    }

    quote! {
        eyre::Result<$output_type>
    }
}

fn render_execution(funcs: &CommonFunctions, field: &FullTypeFields) -> rust::Tokens {
    if let Some(true) = field.type_.pipe(|t| type_ref_is_object(&t.type_ref)) {
        let output_type = funcs.format_output_type(&field.type_.as_ref().unwrap().type_ref);
        return quote! {
            return $(output_type) {
                proc: self.proc.clone(),
                selection: query,
                conn: self.conn.clone(),
            }
        };
    }

    if let Some(true) = field
        .type_
        .pipe(|t| type_ref_is_list_of_objects(&t.type_ref))
    {
        let output_type = funcs.format_output_type(
            &field
                .type_
                .as_ref()
                .unwrap()
                .type_ref
                .of_type
                .as_ref()
                .unwrap()
                .of_type
                .as_ref()
                .unwrap(),
        );
        return quote! {
            return vec![$(output_type) {
                proc: self.proc.clone(),
                selection: query,
                conn: self.conn.clone(),
            }]
        };
    }

    let graphql_client = rust::import("crate::client", "graphql_client");

    quote! {
        query.execute(&$graphql_client(&self.conn))
    }
}

fn format_function_args(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<(rust::Tokens, bool)> {
    if let Some(args) = field.args.as_ref() {
        let args = args
            .into_iter()
            .map(|a| {
                a.as_ref().and_then(|s| {
                    if type_ref_is_optional(&s.input_value.type_) {
                        return None;
                    }

                    let t = funcs.format_input_type(&s.input_value.type_);
                    let n = format_struct_name(&s.input_value.name);

                    Some(quote! {
                        $(n): $(t),
                    })
                })
            })
            .flatten()
            .collect::<Vec<_>>();
        let required_args = quote! {
            &self,
            $(for arg in args join ($['\r']) => $arg)
        };

        if type_field_has_optional(field) {
            Some((
                quote! {
                    $(required_args)
                    opts: $(field_options_struct_name(field))
                },
                true,
            ))
        } else {
            Some((required_args, false))
        }
    } else {
        None
    }
}

fn format_required_function_args(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<rust::Tokens> {
    if let Some(args) = field.args.as_ref() {
        let args = args
            .into_iter()
            .map(|a| {
                a.as_ref().and_then(|s| {
                    if type_ref_is_optional(&s.input_value.type_) {
                        return None;
                    }

                    let t = funcs.format_input_type(&s.input_value.type_);
                    let n = format_struct_name(&s.input_value.name);

                    Some(quote! {
                        $(n): $(t),
                    })
                })
            })
            .flatten()
            .collect::<Vec<_>>();
        let required_args = quote! {
            &self,
            $(for arg in args join ($['\r']) => $arg)
        };

        Some(required_args)
    } else {
        None
    }
}
