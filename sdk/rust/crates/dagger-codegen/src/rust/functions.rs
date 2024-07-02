use crate::functions::*;
use convert_case::{Case, Casing};
use dagger_sdk::core::introspection::{FullTypeFields, TypeRef};
use genco::prelude::rust;
use genco::quote;
use genco::tokens::{quoted, static_literal};
use itertools::Itertools;

use crate::utility::OptionExt;

use super::templates::object_tmpl::render_optional_field_args;

pub fn format_name(s: &str) -> String {
    s.to_case(Case::Pascal)
}

pub fn format_struct_name(s: &str) -> String {
    let s = s.to_case(Case::Snake);
    match s.as_ref() {
        "ref" => "r#ref".to_string(),
        "enum" => "r#enum".to_string(),
        _ => s,
    }
}

pub fn field_options_struct_name(field: &FullTypeFields) -> Option<String> {
    field
        .parent_type
        .as_ref()
        .and_then(|p| p.name.as_ref().map(|n| format_name(n)))
        .zip(field.name.as_ref().map(|n| format_name(n)))
        .map(|(parent_name, field_name)| format!("{parent_name}{field_name}Opts"))
}

pub fn format_function(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    let is_async = field.type_.pipe(|t| &t.type_ref).pipe(|t| {
        if t.is_object() || t.is_list_of_objects() {
            None
        } else {
            Some(quote! {
                async
            })
        }
    });

    let signature = quote! {
        pub $(is_async) fn $(field.name.pipe(|n | format_struct_name(n)))
    };

    let lifecycle = format_optional_args(funcs, field)
        .pipe(|(_, contains_lifecycle)| contains_lifecycle)
        .and_then(|c| {
            if *c {
                Some(quote! {
                    <'a>
                })
            } else {
                None
            }
        });

    let args = format_function_args(funcs, field, lifecycle.as_ref());

    let output_type = field
        .type_
        .pipe(|t| &t.type_ref)
        .pipe(|t| render_output_type(funcs, t));

    if let Some((args, desc, true)) = args {
        let required_args = format_required_function_args(funcs, field);
        Some(quote! {
            $(field.description.pipe(|d| format_struct_comment(d)))
            $(&desc)
            $(&signature)(
                $(required_args)
            ) -> $(output_type.as_ref()) {
                let mut query = self.selection.select($(quoted(field.name.as_ref())));

                $(render_required_args(funcs, field))

                $(render_execution(funcs, field))
            }

            $(field.description.pipe(|d| format_struct_comment(d)))
            $(&desc)
            $(&signature)_opts$(lifecycle)(
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
            $(field.description.pipe(|d| format_struct_comment(d)))
            $(if let Some((_, desc, _)) = &args => $desc)
            $(signature)(
                $(if let Some((args, _, _)) = &args => $args)
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
            .iter()
            .filter_map(|a| {
                a.as_ref().and_then(|s| {
                    if s.input_value.type_.is_optional() {
                        return None;
                    }

                    let n = format_struct_name(&s.input_value.name);
                    let name = &s.input_value.name;

                    if s.input_value.type_.is_scalar() {
                        if let Scalar::String =
                            Scalar::from(&*s.input_value.type_.of_type.as_ref().unwrap().clone())
                        {
                            return Some(quote! {
                                query = query.arg($(quoted(name)), $(&n).into());
                            });
                        }
                    }


                    if s.input_value.type_.is_list() {
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

                        if inner.is_scalar() {
                            if let Scalar::String =
                                Scalar::from(&*inner.of_type.as_ref().unwrap().clone())
                            {
                                return Some(quote! {
                                    query = query.arg($(quoted(name)), $(&n).into_iter().map(|i| i.into()).collect::<Vec<String>>());
                                });
                            }
                        }
                    }

                    if s.input_value.type_.is_id() {
                        return Some(quote!{
                            query = query.arg_lazy(
                                $(quoted(name)),
                                Box::new(move || {
                                    let $(&n) = $(&n).clone();
                                    Box::pin(async move { $(&n).into_id().await.unwrap().quote() })
                                }),
                            );
                        })
                    }

                    Some(quote! {
                        query = query.arg($(quoted(name)), $(n));
                    })
                })
            })
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
            .iter()
            .filter_map(|a| {
                a.as_ref().and_then(|s| {
                    if !s.input_value.type_.is_optional() {
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
            .collect::<Vec<_>>();

        if args.is_empty() {
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

    if type_ref.is_object() || type_ref.is_list_of_objects() {
        return quote! {
            $(output_type)
        };
    }

    let dagger_error = rust::import("crate::errors", "DaggerError");

    quote! {
        Result<$output_type, $dagger_error>
    }
}

fn render_execution(funcs: &CommonFunctions, field: &FullTypeFields) -> rust::Tokens {
    if let Some(true) = field.type_.pipe(|t| t.type_ref.is_object()) {
        let output_type = funcs.format_output_type(&field.type_.as_ref().unwrap().type_ref);
        return quote! {
            $(output_type) {
                proc: self.proc.clone(),
                selection: query,
                graphql_client: self.graphql_client.clone(),
            }
        };
    }

    if let Some(true) = field.type_.pipe(|t| t.type_ref.is_list_of_objects()) {
        let output_type = funcs.format_output_type(
            field
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
            vec![$(output_type) {
                proc: self.proc.clone(),
                selection: query,
                graphql_client: self.graphql_client.clone(),
            }]
        };
    }

    quote! {
        query.execute(self.graphql_client.clone()).await
    }
}

fn format_function_args(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
    lifecycle: Option<&rust::Tokens>,
) -> Option<(rust::Tokens, rust::Tokens, bool)> {
    let mut argument_description = Vec::new();
    if let Some(args) = field.args.as_ref() {
        let args = args
            .iter()
            .filter_map(|a| {
                a.as_ref().and_then(|s| {
                    if s.input_value.type_.is_optional() {
                        return None;
                    }

                    let t = funcs.format_input_type(&s.input_value.type_);

                    let n = format_struct_name(&s.input_value.name);
                    if let Some(desc) = s.input_value.description.as_ref().and_then(|d| {
                        if !d.is_empty() {
                            Some(write_comment_line(&format!("* `{n}` - {}", d)))
                        } else {
                            None
                        }
                    }) {
                        argument_description.push(quote! {
                            $(desc)
                        });
                    }

                    if t.ends_with("Id") {
                        let into_id = rust::import("crate::id", "IntoID");
                        Some(quote! {
                            $(n): impl $(into_id)<$(t)>,
                        })
                    } else {
                        Some(quote! {
                            $(n): $(t),
                        })
                    }
                })
            })
            .collect::<Vec<_>>();
        let required_args = quote! {
            &self,
            $(for arg in args join ($['\r']) => $arg)
        };

        if type_field_has_optional(field) {
            let field_name = field_options_struct_name(field);
            argument_description.push(quote! {
                $(field_name.pipe(|_| write_comment_line("* `opt` - optional argument, see inner type for documentation, use <func>_opts to use")))
            });

            let description = if !argument_description.is_empty() {
                Some(quote! {
                    $(static_literal("///"))$['\r']
                    $(static_literal("/// # Arguments"))$['\r']
                    $(static_literal("///"))$['\r']
                    $(for arg_desc in argument_description join ($['\r']) => $arg_desc)


                })
            } else {
                None
            };

            Some((
                quote! {
                    $(required_args)
                    opts: $(field_name)$(lifecycle)
                },
                description.unwrap_or_default(),
                true,
            ))
        } else {
            let description = if !argument_description.is_empty() {
                Some(quote! {
                    $(static_literal("///"))$['\r']
                    $(static_literal("/// # Arguments"))$['\r']
                    $(static_literal("///"))$['\r']
                    $(for arg_desc in argument_description join ($['\r']) => $arg_desc)


                })
            } else {
                None
            };

            Some((required_args, description.unwrap_or_default(), false))
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
            .iter()
            .filter_map(|a| {
                a.as_ref().and_then(|s| {
                    if s.input_value.type_.is_optional() {
                        return None;
                    }

                    let t = funcs.format_input_type(&s.input_value.type_);
                    let n = format_struct_name(&s.input_value.name);

                    if t.ends_with("Id") {
                        let into_id = rust::import("crate::id", "IntoID");
                        Some(quote! {
                            $(n): impl $(into_id)<$(t)>,
                        })
                    } else {
                        Some(quote! {
                            $(n): $(t),
                        })
                    }
                })
            })
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

pub fn format_optional_args(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<(rust::Tokens, bool)> {
    field
        .args
        .pipe(|t| t.iter().flatten().collect::<Vec<_>>())
        .map(|t| {
            t.into_iter()
                .filter(|t| t.input_value.type_.is_optional())
                .sorted_by_key(|val| &val.input_value.name)
                .collect::<Vec<_>>()
        })
        .pipe(|t| render_optional_field_args(funcs, t))
        .flatten()
}

pub fn write_comment_line(content: &str) -> Option<rust::Tokens> {
    let cnt = content.trim();
    if cnt.is_empty() {
        return None;
    }

    let mut tokens = rust::Tokens::new();

    for line in content.split('\n') {
        tokens.append(format!("/// {}", line.trim()));
        tokens.push();
    }

    Some(tokens)
}

pub fn format_struct_comment(desc: &str) -> Option<rust::Tokens> {
    let lines = desc.trim().split("\n");

    let formatted_lines = lines
        .into_iter()
        .map(write_comment_line)
        .collect::<Vec<_>>();

    if !formatted_lines.is_empty() {
        Some(quote! {
            $(for line in formatted_lines join($['\r']) => $line)
        })
    } else {
        None
    }
}
