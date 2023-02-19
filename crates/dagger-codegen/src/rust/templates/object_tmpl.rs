use dagger_core::introspection::{FullType, FullTypeFields, FullTypeFieldsArgs};
use genco::prelude::rust;
use genco::quote;

use crate::functions::{type_ref_is_optional, CommonFunctions};
use crate::rust::functions::{
    field_options_struct_name, format_function, format_name, format_struct_name,
};
use crate::utility::OptionExt;

pub fn render_object(funcs: &CommonFunctions, t: &FullType) -> eyre::Result<rust::Tokens> {
    let selection = rust::import("crate::querybuilder", "Selection");
    let child = rust::import("std::process", "Child");
    let conn = rust::import("dagger_core::connect_params", "ConnectParams");
    let arc = rust::import("std::sync", "Arc");

    Ok(quote! {
        pub struct $(t.name.pipe(|s| format_name(s))) {
            pub proc: $arc<$child>,
            pub selection: $selection,
            pub conn: $conn,
        }

        $(t.fields.pipe(|f| render_optional_args(funcs, f)))

        impl $(t.name.pipe(|s| format_name(s))) {
            $(t.fields.pipe(|f| render_functions(funcs, f)))
        }
    })
}

fn render_optional_args(
    funcs: &CommonFunctions,
    fields: &Vec<FullTypeFields>,
) -> Option<rust::Tokens> {
    let rendered_fields = fields
        .iter()
        .map(|f| render_optional_arg(funcs, f))
        .flatten()
        .collect::<Vec<_>>();

    if rendered_fields.len() == 0 {
        None
    } else {
        Some(quote! {
            $(for field in rendered_fields join ($['\r']) => $field)
        })
    }
}

fn render_optional_arg(funcs: &CommonFunctions, field: &FullTypeFields) -> Option<rust::Tokens> {
    let output_type = field_options_struct_name(field);
    let fields = field
        .args
        .pipe(|t| t.into_iter().flatten().collect::<Vec<_>>())
        .map(|t| {
            t.into_iter()
                .filter(|t| type_ref_is_optional(&t.input_value.type_))
                .collect::<Vec<_>>()
        })
        .pipe(|t| render_optional_field_args(funcs, t))
        .flatten();

    let builder = rust::import("derive_builder", "Builder");
    let phantom_data = rust::import("std::marker", "PhantomData");

    if let Some(fields) = fields {
        Some(quote! {
            #[derive($builder, Debug, PartialEq)]
            pub struct $output_type<'a> {
                //#[builder(default, setter(skip))]
                //pub marker: $(phantom_data)<&'a ()>,
                $fields
            }
        })
    } else {
        None
    }
}

fn render_optional_field_args(
    funcs: &CommonFunctions,
    args: &Vec<&FullTypeFieldsArgs>,
) -> Option<rust::Tokens> {
    if args.len() == 0 {
        return None;
    }
    let rendered_args = args.into_iter().map(|a| &a.input_value).map(|a| {
        quote! {
            #[builder(setter(into, strip_option))]
            pub $(format_struct_name(&a.name)): Option<$(funcs.format_immutable_input_type(&a.type_))>,
        }
    });

    Some(quote! {
        $(for arg in rendered_args join ($['\r']) => $arg)
    })
}

fn render_functions(funcs: &CommonFunctions, fields: &Vec<FullTypeFields>) -> Option<rust::Tokens> {
    let rendered_functions = fields
        .iter()
        .map(|f| render_function(funcs, f))
        .collect::<Vec<_>>();

    if rendered_functions.len() > 0 {
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
