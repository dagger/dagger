use dagger_sdk::core::introspection::{FullType, FullTypeFields, FullTypeFieldsArgs};
use genco::prelude::rust;
use genco::quote;

use crate::functions::CommonFunctions;
use crate::rust::functions::{
    field_options_struct_name, format_function, format_name, format_optional_args,
    format_struct_comment, format_struct_name,
};
use crate::utility::OptionExt;

pub fn render_object(funcs: &CommonFunctions, t: &FullType) -> eyre::Result<rust::Tokens> {
    let selection = rust::import("crate::querybuilder", "Selection");
    let session_proc = rust::import("crate::core::cli_session", "DaggerSessionProc");
    let graphql_client = rust::import("crate::core::graphql_client", "DynGraphQLClient");
    let arc = rust::import("std::sync", "Arc");

    Ok(quote! {
        #[derive(Clone)]
        pub struct $(t.name.pipe(|s| format_name(s))) {
            pub proc: Option<$arc<$session_proc>>,
            pub selection: $selection,
            pub graphql_client: $graphql_client
        }

        $(t.fields.pipe(|f| render_optional_args(funcs, f)))

        impl $(t.name.pipe(|s| format_name(s))) {
            $(t.fields.pipe(|f| render_functions(funcs, f)))
        }
    })
}

fn render_optional_args(
    funcs: &CommonFunctions,
    fields: &[FullTypeFields],
) -> Option<rust::Tokens> {
    let rendered_fields = fields
        .iter()
        .filter_map(|f| render_optional_arg(funcs, f))
        .collect::<Vec<_>>();

    if rendered_fields.is_empty() {
        None
    } else {
        Some(quote! {
            $(for field in rendered_fields join ($['\r']) => $field)
        })
    }
}

fn render_optional_arg(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    let output_type = field_options_struct_name(field);
    let fields = format_optional_args(funcs, field);

    let builder = rust::import("derive_builder", "Builder");
    let _phantom_data = rust::import("std::marker", "PhantomData");

    if let Some((fields, contains_lifetime)) = fields {
        Some(quote! {
            #[derive($builder, Debug, PartialEq)]
            pub struct $output_type$(if contains_lifetime => <'a>) {
                //#[builder(default, setter(skip))]
                //pub marker: $(phantom_data)<&'a ()>,
                $fields
            }
        })
    } else {
        None
    }
}

pub fn render_optional_field_args(
    funcs: &CommonFunctions,
    args: &[&FullTypeFieldsArgs],
) -> Option<(rust::Tokens, bool)> {
    if args.is_empty() {
        return None;
    }
    let mut contains_lifetime = false;
    let rendered_args = args.iter().map(|a| &a.input_value).map(|a| {
        let type_ = funcs.format_immutable_input_type(&a.type_);
        if type_.contains("str") {
            contains_lifetime = true;
        }
        quote! {
            $(a.description.pipe(|d| format_struct_comment(d)))
            #[builder(setter(into, strip_option), default)]
            pub $(format_struct_name(&a.name)): Option<$(type_)>,
        }
    });

    Some((
        quote! {
            $(for arg in rendered_args join ($['\r']) => $arg)
        },
        contains_lifetime,
    ))
}

fn render_functions(funcs: &CommonFunctions, fields: &[FullTypeFields]) -> Option<rust::Tokens> {
    let rendered_functions = fields
        .iter()
        .map(|f| render_function(funcs, f))
        .collect::<Vec<_>>();

    if !rendered_functions.is_empty() {
        Some(quote! {
            $(for func in rendered_functions join ($['\r']) => $func)
        })
    } else {
        None
    }
}

fn render_function(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    Some(quote! {
        $(format_function(funcs, field))
    })
}
