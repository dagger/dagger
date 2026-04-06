use dagger_sdk::core::introspection::{FullType, FullTypeFields};
use genco::prelude::rust;
use genco::quote;

use crate::functions::{CommonFunctions, TypeRefExt};
use crate::rust::functions::{format_name, format_struct_comment, format_struct_name};
use crate::utility::OptionExt;

use super::object_tmpl::{render_loadable_impl, render_object_without_loadable};

/// Render an interface type as:
///   1. A Rust trait with async method signatures
///   2. A concrete `FooClient` struct (same shape as objects) for query-building
///   3. `impl Foo for FooClient`
pub fn render_interface(funcs: &CommonFunctions, t: &FullType) -> eyre::Result<rust::Tokens> {
    let trait_tokens = render_trait(funcs, t);

    // Rename the type to FooClient for the struct, so it doesn't
    // collide with the trait name.
    let original_graphql_name = t.name.as_deref().unwrap_or_default();
    let client_name = t.name.as_ref().map(|n| format!("{n}Client"));
    let mut client_type = t.clone();
    client_type.name = client_name;
    // Also patch parent_type on fields so Opts structs get the right prefix.
    let parent_snapshot = client_type.clone();
    if let Some(fields) = client_type.fields.as_mut() {
        for field in fields.iter_mut() {
            field.parent_type = Some(parent_snapshot.clone());
        }
    }
    let client_tokens = render_object_without_loadable(funcs, &client_type)?;

    // Override the GraphQL name for Loadable: the Rust struct is
    // NodeClient but the GraphQL type is Node.
    let loadable_tokens = render_loadable_impl(&client_type, Some(original_graphql_name));

    let trait_impl_tokens = render_trait_impl_for_client(funcs, t);

    Ok(quote! {
        $trait_tokens

        $client_tokens

        $loadable_tokens

        $trait_impl_tokens
    })
}

/// Generate `pub trait Foo { async fn id(&self) -> Result<Id, DaggerError>; ... }`
fn render_trait(funcs: &CommonFunctions, t: &FullType) -> rust::Tokens {
    let trait_name = t.name.pipe(|s| format_name(s)).unwrap_or_default();
    let dagger_error = rust::import("crate::errors", "DaggerError");

    let methods = t
        .fields
        .as_ref()
        .map(|fields| render_trait_methods(funcs, fields, &dagger_error))
        .unwrap_or_default();

    quote! {
        $(t.description.pipe(|d| format_struct_comment(d)))
        pub trait $(&trait_name) {
            $methods
        }
    }
}

/// Generate trait method signatures from interface fields.
fn render_trait_methods(
    funcs: &CommonFunctions,
    fields: &[FullTypeFields],
    dagger_error: &rust::Import,
) -> rust::Tokens {
    let methods: Vec<rust::Tokens> = fields
        .iter()
        .filter_map(|f| render_trait_method(funcs, f, dagger_error))
        .collect();

    quote! {
        $(for m in methods join ($['\r']) => $m)
    }
}

/// Generate a single trait method signature.
fn render_trait_method(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
    dagger_error: &rust::Import,
) -> Option<rust::Tokens> {
    let name = field.name.as_ref()?;
    let fn_name = format_struct_name(name);
    let type_ref = &field.type_.as_ref()?.type_ref;
    let output_type = funcs.format_output_type(type_ref);

    let is_object = type_ref.is_object() || type_ref.is_list_of_objects();

    // Build argument list (required args only for trait signatures)
    let args = render_trait_method_args(funcs, field);

    if is_object {
        Some(quote! {
            $(field.description.pipe(|d| format_struct_comment(d)))
            fn $fn_name(&self$(if let Some(a) = &args => , $a)) -> $output_type;
        })
    } else {
        Some(quote! {
            $(field.description.pipe(|d| format_struct_comment(d)))
            fn $fn_name(&self$(if let Some(a) = &args => , $a)) -> impl core::future::Future<Output = Result<$output_type, $dagger_error>> + Send;
        })
    }
}

/// Render required argument list for a trait method signature.
fn render_trait_method_args(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<rust::Tokens> {
    let args = field.args.as_ref()?;
    let required: Vec<rust::Tokens> = args
        .iter()
        .filter_map(|a| {
            let a = a.as_ref()?;
            if a.input_value.type_.is_optional() {
                return None;
            }
            let n = format_struct_name(&a.input_value.name);
            let t = funcs.format_input_type(&a.input_value.type_);

            if a.input_value.type_.is_id() {
                let into_id = rust::import("crate::id", "IntoID");
                Some(quote! { $n: impl $into_id<$t> })
            } else {
                Some(quote! { $n: $t })
            }
        })
        .collect();

    if required.is_empty() {
        None
    } else {
        Some(quote! {
            $(for arg in required join (, ) => $arg)
        })
    }
}

/// Generate `impl Foo for FooClient { ... }` that delegates to the
/// struct's inherent methods.
fn render_trait_impl_for_client(funcs: &CommonFunctions, t: &FullType) -> rust::Tokens {
    let iface_name = t.name.pipe(|s| format_name(s)).unwrap_or_default();
    let client_name = format!("{}Client", &iface_name);

    let methods = t
        .fields
        .as_ref()
        .map(|fields| render_trait_impl_methods(funcs, fields))
        .unwrap_or_default();

    quote! {
        impl $(&iface_name) for $client_name {
            $methods
        }
    }
}

/// Generate forwarding methods for `impl Foo for FooClient`.
fn render_trait_impl_methods(funcs: &CommonFunctions, fields: &[FullTypeFields]) -> rust::Tokens {
    let methods: Vec<rust::Tokens> = fields
        .iter()
        .filter_map(|f| render_trait_impl_method(funcs, f))
        .collect();

    quote! {
        $(for m in methods join ($['\r']) => $m)
    }
}

/// Generate a single forwarding method.
fn render_trait_impl_method(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<rust::Tokens> {
    let name = field.name.as_ref()?;
    let fn_name = format_struct_name(name);
    let type_ref = &field.type_.as_ref()?.type_ref;
    let output_type = funcs.format_output_type(type_ref);
    let dagger_error = rust::import("crate::errors", "DaggerError");

    let is_object = type_ref.is_object() || type_ref.is_list_of_objects();

    let (arg_sig, _arg_pass) = render_trait_impl_arg_parts(funcs, field);

    if is_object {
        Some(quote! {
            fn $fn_name(&self$(if let Some(a) = &arg_sig => , $a)) -> $(&output_type) {
                // Delegate to inherent method on the struct
                let query = self.selection.select($(genco::tokens::quoted(name)));
                $(render_required_arg_setters(funcs, field))
                $(&output_type) {
                    proc: self.proc.clone(),
                    selection: query,
                    graphql_client: self.graphql_client.clone(),
                }
            }
        })
    } else {
        Some(quote! {
            fn $fn_name(&self$(if let Some(a) = &arg_sig => , $a)) -> impl core::future::Future<Output = Result<$output_type, $dagger_error>> + Send {
                let query = self.selection.select($(genco::tokens::quoted(name)));
                $(render_required_arg_setters(funcs, field))
                let graphql_client = self.graphql_client.clone();
                async move {
                    query.execute(graphql_client).await
                }
            }
        })
    }
}

/// Split args into (signature tokens, pass-through tokens) for trait impl.
fn render_trait_impl_arg_parts(
    funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> (Option<rust::Tokens>, Option<rust::Tokens>) {
    let args = match field.args.as_ref() {
        Some(a) => a,
        None => return (None, None),
    };

    let required: Vec<(rust::Tokens, rust::Tokens)> = args
        .iter()
        .filter_map(|a| {
            let a = a.as_ref()?;
            if a.input_value.type_.is_optional() {
                return None;
            }
            let n = format_struct_name(&a.input_value.name);
            let t = funcs.format_input_type(&a.input_value.type_);

            let sig = if a.input_value.type_.is_id() {
                let into_id = rust::import("crate::id", "IntoID");
                quote! { $(&n): impl $into_id<$t> }
            } else {
                quote! { $(&n): $t }
            };
            let pass = quote! { $(&n) };
            Some((sig, pass))
        })
        .collect();

    if required.is_empty() {
        (None, None)
    } else {
        let sigs: Vec<rust::Tokens> = required.iter().map(|(s, _)| s.clone()).collect();
        let passes: Vec<rust::Tokens> = required.iter().map(|(_, p)| p.clone()).collect();
        (
            Some(quote! { $(for s in sigs join (, ) => $s) }),
            Some(quote! { $(for p in passes join (, ) => $p) }),
        )
    }
}

/// Render query.arg(...) calls for required args in trait impl methods.
fn render_required_arg_setters(
    _funcs: &CommonFunctions,
    field: &FullTypeFields,
) -> Option<rust::Tokens> {
    let args = field.args.as_ref()?;
    let setters: Vec<rust::Tokens> = args
        .iter()
        .filter_map(|a| {
            let a = a.as_ref()?;
            if a.input_value.type_.is_optional() {
                return None;
            }
            let n = format_struct_name(&a.input_value.name);
            let name = &a.input_value.name;

            if a.input_value.type_.is_id() {
                Some(quote! {
                    let query = query.arg_lazy(
                        $(genco::tokens::quoted(name)),
                        Box::new(move || {
                            let $(&n) = $(&n).clone();
                            Box::pin(async move { $(&n).into_id().await.unwrap().quote() })
                        }),
                    );
                })
            } else {
                Some(quote! {
                    let query = query.arg($(genco::tokens::quoted(name)), $(&n));
                })
            }
        })
        .collect();

    if setters.is_empty() {
        None
    } else {
        Some(quote! {
            $(for s in setters join ($['\r']) => $s)
        })
    }
}

/// Generate `impl InterfaceName for ObjectName { ... }` for an object
/// that declares an interface. This generates forwarding methods that
/// delegate to the object struct's inherent methods.
pub fn render_interface_impl_for_object(
    funcs: &CommonFunctions,
    object_type: &FullType,
    iface_type: &FullType,
) -> rust::Tokens {
    let object_name = object_type
        .name
        .pipe(|s| format_name(s))
        .unwrap_or_default();
    let iface_name = iface_type.name.pipe(|s| format_name(s)).unwrap_or_default();

    let methods = iface_type
        .fields
        .as_ref()
        .map(|fields| render_trait_impl_methods_for_object(funcs, fields))
        .unwrap_or_default();

    quote! {
        impl $iface_name for $object_name {
            $methods
        }
    }
}

/// Generate forwarding methods for `impl Interface for Object`.
/// These forward to the object's inherent methods which already exist.
fn render_trait_impl_methods_for_object(
    funcs: &CommonFunctions,
    fields: &[FullTypeFields],
) -> rust::Tokens {
    let methods: Vec<rust::Tokens> = fields
        .iter()
        .filter_map(|f| render_trait_impl_method(funcs, f))
        .collect();

    quote! {
        $(for m in methods join ($['\r']) => $m)
    }
}
