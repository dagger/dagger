use convert_case::{Case, Casing};
use proc_macro::TokenStream;
use quote::{format_ident, quote};
use syn::{parse_macro_input, FnArg, ImplItem, ItemImpl, Pat, ReturnType, Type};

/// Marks an impl block as a Dagger module, generating the `DaggerModule` trait implementation.
///
/// Methods annotated with `#[dagger_function]` become Dagger functions.
///
/// # Example
/// ```ignore
/// use dagger_sdk::*;
///
/// #[derive(Default)]
/// pub struct MyModule;
///
/// #[dagger_module]
/// impl MyModule {
///     #[dagger_function]
///     fn hello(&self) -> String {
///         "Hello!".to_string()
///     }
///
///     #[dagger_function]
///     async fn build(&self, source: Directory) -> eyre::Result<Container> {
///         Ok(dag().container().from("alpine"))
///     }
/// }
/// ```
#[proc_macro_attribute]
pub fn dagger_module(_attr: TokenStream, item: TokenStream) -> TokenStream {
    let input = parse_macro_input!(item as ItemImpl);
    match expand_module(input) {
        Ok(ts) => ts.into(),
        Err(e) => e.to_compile_error().into(),
    }
}

/// Marks a method as a Dagger function (used inside a `#[dagger_module]` impl block).
/// This is a marker attribute — the actual code generation happens in `#[dagger_module]`.
#[proc_macro_attribute]
pub fn dagger_function(_attr: TokenStream, item: TokenStream) -> TokenStream {
    // Pass through — the dagger_module macro handles everything
    item
}

fn expand_module(input: ItemImpl) -> syn::Result<proc_macro2::TokenStream> {
    let self_ty = &input.self_ty;

    // Extract the type name as a string
    let type_name = match self_ty.as_ref() {
        Type::Path(tp) => tp
            .path
            .segments
            .last()
            .map(|s| s.ident.to_string())
            .unwrap_or_default(),
        _ => return Err(syn::Error::new_spanned(self_ty, "expected a named type")),
    };

    // Collect dagger_function methods
    let mut functions = Vec::new();

    for item in &input.items {
        if let ImplItem::Fn(method) = item {
            let has_attr = method
                .attrs
                .iter()
                .any(|a| a.path().is_ident("dagger_function"));

            if !has_attr {
                continue;
            }

            let method_name = &method.sig.ident;
            let is_async = method.sig.asyncness.is_some();

            // Extract doc comment
            let doc = extract_doc_comment(&method.attrs);

            // Extract arguments (skip &self)
            let mut args_info = Vec::new();
            for arg in method.sig.inputs.iter().skip(1) {
                if let FnArg::Typed(pat_ty) = arg {
                    let arg_name = match pat_ty.pat.as_ref() {
                        Pat::Ident(pi) => pi.ident.to_string(),
                        _ => continue,
                    };
                    let arg_type = &pat_ty.ty;
                    args_info.push((arg_name, arg_type.clone()));
                }
            }

            // Extract return type
            let ret_type = &method.sig.output;

            functions.push(FunctionInfo {
                method_name: method_name.clone(),
                is_async,
                doc,
                args: args_info,
                ret_type: ret_type.clone(),
            });
        }
    }

    // Generate the DaggerModule impl
    let fn_defs = functions.iter().map(|f| generate_function_def(f));
    let fn_count = functions.len();

    // Strip #[dagger_function] attributes from the original impl for clean output
    let mut clean_impl = input.clone();
    for item in &mut clean_impl.items {
        if let ImplItem::Fn(method) = item {
            method
                .attrs
                .retain(|a| !a.path().is_ident("dagger_function"));
        }
    }

    let expanded = quote! {
        #clean_impl

        impl ::dagger_sdk::module::DaggerModule for #self_ty {
            fn name(&self) -> &str {
                #type_name
            }

            fn functions(&self) -> Vec<::dagger_sdk::module::ModuleFunction> {
                let mut fns = Vec::with_capacity(#fn_count);
                #(fns.push(#fn_defs);)*
                fns
            }
        }
    };

    Ok(expanded)
}

struct FunctionInfo {
    method_name: syn::Ident,
    is_async: bool,
    doc: String,
    args: Vec<(String, Box<Type>)>,
    ret_type: ReturnType,
}

fn generate_function_def(func: &FunctionInfo) -> proc_macro2::TokenStream {
    let method_name = &func.method_name;
    // Convert snake_case method name to camelCase for Dagger API
    let dagger_name = func.method_name.to_string().to_case(Case::Camel);
    let doc = &func.doc;

    // Generate argument definitions
    let arg_defs: Vec<_> = func
        .args
        .iter()
        .map(|(name, ty)| {
            let camel_name = name.to_case(Case::Camel);
            let type_kind = type_to_kind(ty);
            quote! {
                ::dagger_sdk::module::FunctionArg {
                    name: #camel_name.to_string(),
                    description: String::new(),
                    type_kind: #type_kind,
                    optional: false,
                    default_value: None,
                }
            }
        })
        .collect();

    // Generate the return type kind
    let ret_kind = match &func.ret_type {
        ReturnType::Default => quote! { ::dagger_sdk::module::TypeKind::Void },
        ReturnType::Type(_, ty) => return_type_to_kind(ty),
    };

    // Generate arg extraction in the handler
    let arg_extractions: Vec<_> = func
        .args
        .iter()
        .map(|(name, ty)| {
            let ident = format_ident!("{}", name);
            let camel_name = name.to_case(Case::Camel);
            let extraction = arg_extraction_expr(&camel_name, ty);
            quote! { let #ident = #extraction; }
        })
        .collect();

    let arg_idents: Vec<_> = func
        .args
        .iter()
        .map(|(name, _)| format_ident!("{}", name))
        .collect();

    // Generate the method call
    let is_async = func.is_async;
    let method_call = if is_async {
        quote! { instance.#method_name(#(#arg_idents),*).await }
    } else {
        quote! { instance.#method_name(#(#arg_idents),*) }
    };

    // Wrap the result based on return type
    let result_expr = return_value_expr(&func.ret_type);

    quote! {
        ::dagger_sdk::module::ModuleFunction {
            name: #dagger_name.to_string(),
            description: #doc.to_string(),
            args: vec![#(#arg_defs),*],
            return_type: #ret_kind,
            handler: Box::new(|client, _parent, args| {
                Box::pin(async move {
                    let instance = Self::default();
                    #(#arg_extractions)*
                    let result = #method_call;
                    #result_expr
                })
            }),
        }
    }
}

/// Map a Rust type to a TypeKind expression.
fn type_to_kind(ty: &Type) -> proc_macro2::TokenStream {
    match ty {
        Type::Path(tp) => {
            let seg = tp.path.segments.last().unwrap();
            let name = seg.ident.to_string();
            match name.as_str() {
                "String" => quote! { ::dagger_sdk::module::TypeKind::String },
                "str" => quote! { ::dagger_sdk::module::TypeKind::String },
                "i32" | "i64" | "isize" => {
                    quote! { ::dagger_sdk::module::TypeKind::Integer }
                }
                "f32" | "f64" => quote! { ::dagger_sdk::module::TypeKind::Float },
                "bool" => quote! { ::dagger_sdk::module::TypeKind::Boolean },
                "Option" => {
                    if let syn::PathArguments::AngleBracketed(args) = &seg.arguments {
                        if let Some(syn::GenericArgument::Type(inner)) = args.args.first() {
                            let inner_kind = type_to_kind(inner);
                            return quote! { ::dagger_sdk::module::TypeKind::Optional(Box::new(#inner_kind)) };
                        }
                    }
                    quote! { ::dagger_sdk::module::TypeKind::String }
                }
                "Vec" => {
                    if let syn::PathArguments::AngleBracketed(args) = &seg.arguments {
                        if let Some(syn::GenericArgument::Type(inner)) = args.args.first() {
                            let inner_kind = type_to_kind(inner);
                            return quote! { ::dagger_sdk::module::TypeKind::List(Box::new(#inner_kind)) };
                        }
                    }
                    quote! { ::dagger_sdk::module::TypeKind::String }
                }
                // Dagger object types
                "Container" | "Directory" | "File" | "Secret" | "Service" | "Socket"
                | "CacheVolume" | "Module" | "ModuleSource" | "GitRepository" | "GitRef"
                | "Terminal" => {
                    quote! { ::dagger_sdk::module::TypeKind::Object(#name.to_string()) }
                }
                _ => {
                    // Check if it's a qualified dagger_sdk type
                    let full_path = tp
                        .path
                        .segments
                        .iter()
                        .map(|s| s.ident.to_string())
                        .collect::<Vec<_>>()
                        .join("::");
                    if full_path.contains("dagger_sdk") {
                        quote! { ::dagger_sdk::module::TypeKind::Object(#name.to_string()) }
                    } else {
                        // Default to string for unknown types
                        quote! { ::dagger_sdk::module::TypeKind::String }
                    }
                }
            }
        }
        Type::Reference(tr) => type_to_kind(&tr.elem),
        _ => quote! { ::dagger_sdk::module::TypeKind::String },
    }
}

/// Map a return type (possibly Result<T, E>) to a TypeKind expression.
fn return_type_to_kind(ty: &Type) -> proc_macro2::TokenStream {
    match ty {
        Type::Path(tp) => {
            let seg = tp.path.segments.last().unwrap();
            let name = seg.ident.to_string();
            if name == "Result" {
                // Extract the Ok type from Result<T, E>
                if let syn::PathArguments::AngleBracketed(args) = &seg.arguments {
                    if let Some(syn::GenericArgument::Type(inner)) = args.args.first() {
                        return type_to_kind(inner);
                    }
                }
            }
            type_to_kind(ty)
        }
        _ => type_to_kind(ty),
    }
}

/// Generate an expression to extract a function argument from the args map.
fn arg_extraction_expr(camel_name: &str, ty: &Type) -> proc_macro2::TokenStream {
    match ty {
        Type::Path(tp) => {
            let seg = tp.path.segments.last().unwrap();
            let name = seg.ident.to_string();
            match name.as_str() {
                "String" => quote! {
                    args.get(#camel_name)
                        .and_then(|v| v.as_str())
                        .unwrap_or_default()
                        .to_string()
                },
                "i32" => quote! {
                    args.get(#camel_name)
                        .and_then(|v| v.as_i64())
                        .unwrap_or_default() as i32
                },
                "i64" | "isize" => quote! {
                    args.get(#camel_name)
                        .and_then(|v| v.as_i64())
                        .unwrap_or_default()
                },
                "f64" => quote! {
                    args.get(#camel_name)
                        .and_then(|v| v.as_f64())
                        .unwrap_or_default()
                },
                "bool" => quote! {
                    args.get(#camel_name)
                        .and_then(|v| v.as_bool())
                        .unwrap_or_default()
                },
                // Dagger object types are passed as ID strings
                "Container" | "Directory" | "File" | "Secret" | "Service" | "Socket"
                | "CacheVolume" => {
                    let load_fn = format_ident!("load_{}_from_id", name.to_case(Case::Snake));
                    let id_type = format_ident!("{}Id", name);
                    quote! {
                        {
                            let id_str = args.get(#camel_name)
                                .and_then(|v| v.as_str())
                                .unwrap_or_default()
                                .to_string();
                            client.#load_fn(::dagger_sdk::#id_type::from(id_str))
                        }
                    }
                }
                "Option" => {
                    // For Option types, check if the arg exists
                    if let syn::PathArguments::AngleBracketed(args_generic) = &seg.arguments {
                        if let Some(syn::GenericArgument::Type(inner)) = args_generic.args.first() {
                            let inner_extract = arg_extraction_expr(camel_name, inner);
                            return quote! {
                                if args.contains_key(#camel_name) {
                                    Some(#inner_extract)
                                } else {
                                    None
                                }
                            };
                        }
                    }
                    quote! { None }
                }
                _ => quote! {
                    args.get(#camel_name)
                        .and_then(|v| v.as_str())
                        .unwrap_or_default()
                        .to_string()
                },
            }
        }
        _ => quote! {
            args.get(#camel_name)
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_string()
        },
    }
}

/// Generate the expression that converts the method's return value to serde_json::Value.
fn return_value_expr(ret_type: &ReturnType) -> proc_macro2::TokenStream {
    match ret_type {
        ReturnType::Default => {
            quote! { Ok(::serde_json::Value::Null) }
        }
        ReturnType::Type(_, ty) => {
            match ty.as_ref() {
                Type::Path(tp) => {
                    let seg = tp.path.segments.last().unwrap();
                    let name = seg.ident.to_string();

                    if name == "Result" {
                        // Result<T, E> — unwrap with ? then convert the inner T
                        if let syn::PathArguments::AngleBracketed(args) = &seg.arguments {
                            if let Some(syn::GenericArgument::Type(inner)) = args.args.first() {
                                let inner_conv = value_conversion_expr(inner, quote! { val });
                                return quote! {
                                    let val = result?;
                                    #inner_conv
                                };
                            }
                        }
                        quote! { result.map(|v| ::serde_json::Value::String(format!("{:?}", v))) }
                    } else {
                        let conv = value_conversion_expr(ty, quote! { result });
                        quote! { #conv }
                    }
                }
                _ => quote! { Ok(::serde_json::Value::String(format!("{:?}", result))) },
            }
        }
    }
}

/// Generate an expression to convert a typed value to serde_json::Value.
fn value_conversion_expr(ty: &Type, val: proc_macro2::TokenStream) -> proc_macro2::TokenStream {
    match ty {
        Type::Path(tp) => {
            let seg = tp.path.segments.last().unwrap();
            let name = seg.ident.to_string();
            match name.as_str() {
                "String" => quote! { Ok(::serde_json::Value::String(#val)) },
                "i32" | "i64" | "isize" => {
                    quote! { Ok(::serde_json::Value::Number(::serde_json::Number::from(#val as i64))) }
                }
                "f64" => {
                    quote! { Ok(::serde_json::Value::Number(::serde_json::Number::from_f64(#val).unwrap())) }
                }
                "bool" => quote! { Ok(::serde_json::Value::Bool(#val)) },
                // Dagger objects — return their ID
                "Container" | "Directory" | "File" | "Secret" | "Service" | "Socket"
                | "CacheVolume" => {
                    quote! {
                        let id = #val.id().await?;
                        Ok(::serde_json::Value::String(id.0))
                    }
                }
                _ => quote! { Ok(::serde_json::Value::String(format!("{:?}", #val))) },
            }
        }
        _ => quote! { Ok(::serde_json::Value::String(format!("{:?}", #val))) },
    }
}

fn extract_doc_comment(attrs: &[syn::Attribute]) -> String {
    let mut docs = Vec::new();
    for attr in attrs {
        if attr.path().is_ident("doc") {
            if let syn::Meta::NameValue(nv) = &attr.meta {
                if let syn::Expr::Lit(syn::ExprLit {
                    lit: syn::Lit::Str(s),
                    ..
                }) = &nv.value
                {
                    docs.push(s.value().trim().to_string());
                }
            }
        }
    }
    docs.join("\n")
}
