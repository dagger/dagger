use crate::functions::*;
use crate::naming::{Acronyms, Casing, Names};
use convert_case::{Case, Casing as _};
use dagger_sdk::core::introspection::{FullTypeFields, TypeRef};
use genco::prelude::rust;
use genco::quote;
use genco::tokens::{quoted, static_literal};
use itertools::Itertools;

use crate::utility::OptionExt;

use super::templates::object_tmpl::render_optional_field_args;

/// The Rust type name for a schema name, guessing its words. Used when the
/// schema has no identifier words.
pub fn format_name(s: &str) -> String {
    s.to_case(Case::Pascal)
}

/// The Rust function, argument or field name for a schema name, guessing its
/// words. Used when the schema has no identifier words.
pub fn format_struct_name(s: &str) -> String {
    escape_keyword(s.to_case(Case::Snake))
}

fn escape_keyword(s: String) -> String {
    match s.as_ref() {
        "async" => "r#async".to_string(),
        "await" => "r#await".to_string(),
        "ref" => "r#ref".to_string(),
        "enum" => "r#enum".to_string(),
        "loop" => "r#loop".to_string(),
        "mod" => "r#mod".to_string(),
        "type" => "r#type".to_string(),
        _ => s,
    }
}

/// The Rust type name for a schema type: PascalCase with capitalized
/// acronyms (`JsonValue`, `LlmTokenUsage`), from the schema's identifier
/// words when it has them.
pub fn type_name(names: &Names, s: &str) -> String {
    if let Some(name) = names.format(s, Casing::Pascal, Acronyms::Capitalized) {
        return name;
    }
    // An interface's client struct is named after the interface: FooClient.
    if let Some(name) = s
        .strip_suffix("Client")
        .and_then(|iface| names.format(iface, Casing::Pascal, Acronyms::Capitalized))
    {
        return name + "Client";
    }
    format_name(s)
}

/// The Rust name for a schema field, argument or input field: snake_case,
/// from the schema's identifier words when it has them.
pub fn member_name(names: &Names, s: &str) -> String {
    match names.format(s, Casing::Snake, Acronyms::Uppercase) {
        Some(name) => escape_keyword(name),
        None => format_struct_name(s),
    }
}

/// The Rust variant name for a schema enum value: PascalCase with
/// capitalized acronyms, like types.
pub fn variant_name(names: &Names, s: &str) -> String {
    names
        .format(s, Casing::Pascal, Acronyms::Capitalized)
        .unwrap_or_else(|| format_name(s))
}

pub fn field_options_struct_name(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<String> {
    field
        .parent_type
        .as_ref()
        .and_then(|p| p.name.as_ref().map(|n| type_name(funcs.names(), n)))
        .zip(field.name.as_ref().map(|n| type_name(funcs.names(), n)))
        .map(|(parent_name, field_name)| format!("{parent_name}{field_name}Opts"))
}

/// The options struct name the field had before identifier words, when it
/// differs from today's.
pub fn legacy_field_options_struct_name(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<String> {
    let legacy = field
        .parent_type
        .as_ref()
        .and_then(|p| p.name.as_ref().map(|n| format_name(n)))
        .zip(field.name.as_ref().map(|n| format_name(n)))
        .map(|(parent_name, field_name)| format!("{parent_name}{field_name}Opts"))?;
    if Some(&legacy) == field_options_struct_name(funcs, field).as_ref() {
        None
    } else {
        Some(legacy)
    }
}

/// The name the function had before identifier words, when it differs from
/// today's.
pub fn legacy_function_name(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<String> {
    let name = field.name.as_ref()?;
    let legacy = format_struct_name(name);
    if legacy == member_name(funcs.names(), name) {
        None
    } else {
        Some(legacy)
    }
}

pub fn format_function(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    let fn_name = field.name.pipe(|n| member_name(funcs.names(), n))?;
    let is_convert_id = funcs.convert_id(field);
    let is_async = field.type_.pipe(|t| &t.type_ref).and_then(|t| {
        if !is_convert_id
            && t.is_object()
            && (!t.is_optional() || !funcs.supports_nullable_objects())
        {
            None
        } else {
            Some(quote! {
                async
            })
        }
    });

    let signature = quote! {
        pub $(is_async.clone()) fn $(&fn_name)
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

    let output_type = render_field_output_type(funcs, field);

    // Keep the name the method had before identifier words as a deprecated
    // alias.
    let deprecated = legacy_function_name(funcs, field).map(|legacy| {
        let note = format!("use {fn_name}");
        let has_opts = matches!(&args, Some((_, _, true)));
        let required_args = format_required_function_args(funcs, field);
        let forwarded = required_arg_names(funcs, field);
        let await_ = is_async.as_ref().map(|_| quote!(.await));
        let opts_alias = if has_opts {
            let opts_args = args.as_ref().map(|(a, _, _)| a.clone());
            let mut opts_forwarded = forwarded.clone();
            opts_forwarded.push("opts".to_string());
            Some(quote! {
                #[deprecated(note = $(quoted(note.clone())))]
                pub $(is_async.clone()) fn $(&legacy)_opts$(lifecycle.clone())(
                    $opts_args
                ) -> $(&output_type) {
                    self.$(&fn_name)_opts($(for a in &opts_forwarded join (, ) => $a))$(await_.clone())
                }
            })
        } else {
            None
        };
        let plain_args = if has_opts {
            required_args
        } else {
            args.as_ref().map(|(a, _, _)| a.clone())
        };
        quote! {
            #[deprecated(note = $(quoted(note.clone())))]
            pub $(is_async.clone()) fn $(&legacy)(
                $plain_args
            ) -> $(&output_type) {
                self.$(&fn_name)($(for a in &forwarded join (, ) => $a))$(await_)
            }

            $opts_alias
        }
    });

    if let Some((args, desc, true)) = args {
        let required_args = format_required_function_args(funcs, field);
        Some(quote! {
            $(field.description.pipe(|d| format_struct_comment(d)))
            $(&desc)
            $(&signature)(
                $(required_args)
            ) -> $(&output_type) {
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

            $deprecated
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

            $deprecated
        })
    }
}

/// The Rust names of a field's required arguments, in order.
fn required_arg_names(funcs: &CommonFunctions, field: &FullTypeFields) -> Vec<String> {
    field
        .args
        .iter()
        .flatten()
        .flatten()
        .filter(|a| !a.input_value.type_.is_optional())
        .map(|a| member_name(funcs.names(), &a.input_value.name))
        .collect()
}

pub(crate) fn render_required_args(
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

                    let n = member_name(funcs.names(), &s.input_value.name);
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

fn render_optional_args(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    if let Some(args) = field.args.as_ref() {
        let args = args
            .iter()
            .filter_map(|a| {
                a.as_ref().and_then(|s| {
                    if !s.input_value.type_.is_optional() {
                        return None;
                    }

                    let n = member_name(funcs.names(), &s.input_value.name);
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

    if type_ref.is_object() {
        if funcs.supports_nullable_objects() && type_ref.is_optional() {
            let dagger_error = rust::import("crate::errors", "DaggerError");
            return quote! {
                Result<Option<$output_type>, $dagger_error>
            };
        }
        return quote! {
            $(output_type)
        };
    }

    let dagger_error = rust::import("crate::errors", "DaggerError");

    quote! {
        Result<$output_type, $dagger_error>
    }
}

/// The Rust struct an ID handle loads into: the object it names, or the
/// `FooClient` struct when the handle names an interface.
pub fn id_handle_struct(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<String> {
    let handle = funcs.id_handle_type(field)?;
    Some(if funcs.is_interface(&handle) {
        format!("{}Client", type_name(funcs.names(), &handle))
    } else {
        type_name(funcs.names(), &handle)
    })
}

/// Render the output type for a field, accounting for ConvertID.
/// When ConvertID applies, the return type is the loaded object, not ID.
fn render_field_output_type(funcs: &CommonFunctions, field: &FullTypeFields) -> rust::Tokens {
    if let Some(handle) = id_handle_struct(funcs, field) {
        let dagger_error = rust::import("crate::errors", "DaggerError");
        return quote! {
            Result<$handle, $dagger_error>
        };
    }
    render_output_type(funcs, &field.type_.as_ref().unwrap().type_ref)
}

fn render_execution(funcs: &CommonFunctions, field: &FullTypeFields) -> rust::Tokens {
    // ConvertID: field returns an ID handle. Execute the query to get the
    // ID, then load the object it names via
    // root.node(id).inline_fragment(type_name).
    if let Some(graphql_name) = funcs.id_handle_type(field) {
        let handle = id_handle_struct(funcs, field).unwrap_or_default();
        return quote! {
            let id: Id = query.execute(self.graphql_client.clone()).await?;
            Ok($(&handle) {
                proc: self.proc.clone(),
                selection: query.root().select("node").arg("id", &id.0).inline_fragment($(quoted(&graphql_name))),
                graphql_client: self.graphql_client.clone(),
            })
        };
    }

    if let Some(true) = field.type_.pipe(|t| {
        funcs.supports_nullable_objects() && t.type_ref.is_object() && t.type_ref.is_optional()
    }) {
        let type_ref = &field.type_.as_ref().unwrap().type_ref;
        let output_type = funcs.format_output_type(type_ref);
        let graphql_name = type_ref.get_non_null().name.clone().unwrap_or_default();
        return quote! {
            let query = query.select("id");
            let id: Option<Id> = query.execute(self.graphql_client.clone()).await?;
            Ok(id.map(|id| $(output_type) {
                proc: self.proc.clone(),
                selection: query.root().select("node").arg("id", &id.0).inline_fragment($(quoted(graphql_name))),
                graphql_client: self.graphql_client.clone(),
            }))
        };
    }

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
        let elem_ref = field
            .type_
            .as_ref()
            .unwrap()
            .type_ref
            .get_non_null()
            .get_list_item()
            .get_non_null();
        let output_type = funcs.format_output_type(elem_ref);
        let elem_graphql_name = elem_ref.name.clone().unwrap_or_default();
        return quote! {
            let query = query.select("id");
            let ids: Vec<Id> =
                query.execute(self.graphql_client.clone()).await?;
            Ok(ids
                .into_iter()
                .map(|id| $(&output_type) {
                    proc: self.proc.clone(),
                    selection: crate::querybuilder::query()
                        .select("node")
                        .arg("id", &id.0)
                        .inline_fragment($(quoted(elem_graphql_name))),
                    graphql_client: self.graphql_client.clone(),
                })
                .collect())
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

                    let n = member_name(funcs.names(), &s.input_value.name);
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

                    if s.input_value.type_.is_id() {
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
            let field_name = field_options_struct_name(funcs, field);
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
                    let n = member_name(funcs.names(), &s.input_value.name);

                    if s.input_value.type_.is_id() {
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
