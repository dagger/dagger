//! Transitional Rust client projection implemented with syntax tokens.

use std::collections::BTreeMap;

use convert_case::{Case, Casing};
use itertools::Itertools;
use proc_macro2::{Ident, Span, TokenStream};
use quote::{ToTokens, format_ident, quote};

use crate::diagnostic::{CodegenError, DiagnosticKind};
use crate::schema::raw::{
    DirectivesExt, FullType, FullTypeField, FullTypeFieldArgument, Schema, TypeKind, TypeRef,
};

/// Projects the current generated-client surface into validated Rust syntax.
#[derive(Clone, Copy, Debug, Default)]
pub struct RustGenerator;

impl RustGenerator {
    /// Renders a raw schema after its parent links have been populated.
    pub(crate) fn render(self, schema: &Schema) -> Result<TokenStream, CodegenError> {
        let mut definitions = TokenStream::new();
        let types = schema
            .types
            .as_deref()
            .unwrap_or_default()
            .iter()
            .flatten()
            .map(|entry| &entry.full_type)
            .filter(|definition| {
                definition
                    .name
                    .as_deref()
                    .is_some_and(|name| !name.starts_with('_'))
            })
            .collect::<Vec<_>>();
        let interfaces = types
            .iter()
            .copied()
            .filter(|definition| definition.kind.as_ref() == Some(&TypeKind::Interface))
            .filter_map(|definition| definition.name.as_deref().map(|name| (name, definition)))
            .collect::<BTreeMap<_, _>>();

        for kind in [
            TypeKind::Scalar,
            TypeKind::InputObject,
            TypeKind::Interface,
            TypeKind::Object,
            TypeKind::Enum,
        ] {
            for definition in types
                .iter()
                .copied()
                .filter(|definition| definition.kind.as_ref() == Some(&kind))
                .sorted_by_key(|definition| definition.name.as_deref().unwrap_or_default())
            {
                let rendered = match kind {
                    TypeKind::Scalar => self.render_scalar(definition)?,
                    TypeKind::InputObject => self.render_input(definition)?,
                    TypeKind::Interface => self.render_interface(definition)?,
                    TypeKind::Object => {
                        let object = self.render_object(definition, true)?;
                        let interface_impls = definition
                            .interfaces
                            .as_deref()
                            .unwrap_or_default()
                            .iter()
                            .filter_map(|reference| reference.type_ref.name.as_deref())
                            .filter_map(|name| interfaces.get(name).copied())
                            .map(|interface| {
                                self.render_object_interface_impl(definition, interface)
                            })
                            .collect::<Result<Vec<_>, _>>()?;
                        quote! { #object #(#interface_impls)* }
                    }
                    TypeKind::Enum => self.render_enum(definition)?,
                    _ => TokenStream::new(),
                };
                definitions.extend(rendered);
            }
        }

        Ok(quote! {
            #![allow(clippy::needless_lifetimes)]
            #![allow(missing_docs)]
            #![allow(unused_mut)]

            use crate::errors::QueryError;
            use crate::id::IntoID;
            use crate::lifecycle::SessionHandle;
            use crate::loadable::private::Sealed;
            use crate::query::Selection;
            use crate::{Id, IdInput, Json, Platform};
            use derive_builder::Builder;
            use serde::{Deserialize, Serialize};

            #definitions
        })
    }

    fn render_scalar(self, definition: &FullType) -> Result<TokenStream, CodegenError> {
        let wire_name = required_name(definition)?;
        if matches!(
            wire_name,
            "String"
                | "Float"
                | "Int"
                | "Boolean"
                | "DateTime"
                | "ID"
                | "JSON"
                | "Platform"
                | "Void"
        ) {
            return Ok(TokenStream::new());
        }
        let name = type_ident(wire_name);

        if wire_name.ends_with("ID") && wire_name != "ID" {
            return Ok(quote! { pub type #name = Id; });
        }

        Ok(quote! {
            #[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
            pub struct #name(pub String);

            impl From<&str> for #name {
                fn from(value: &str) -> Self { Self(value.to_string()) }
            }

            impl From<String> for #name {
                fn from(value: String) -> Self { Self(value) }
            }
        })
    }

    fn render_enum(self, definition: &FullType) -> Result<TokenStream, CodegenError> {
        let name = type_ident(required_name(definition)?);
        let variants = definition
            .enum_values
            .as_deref()
            .unwrap_or_default()
            .iter()
            .filter_map(|value| value.name.as_deref())
            .map(|wire_name| (wire_name, type_ident(wire_name)))
            .sorted_by_key(|(_, name)| name.to_string())
            .dedup_by(|(_, left), (_, right)| left == right)
            .map(|(wire_name, name)| quote! { #[serde(rename = #wire_name)] #name, });

        Ok(quote! {
            #[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
            pub enum #name { #(#variants)* }
        })
    }

    fn render_input(self, definition: &FullType) -> Result<TokenStream, CodegenError> {
        let name = type_ident(required_name(definition)?);
        let fields = definition
            .input_fields
            .as_deref()
            .unwrap_or_default()
            .iter()
            .sorted_by_key(|field| field.input_value.name.as_str())
            .map(|field| {
                let field_name = value_ident(&field.input_value.name);
                let field_type = rust_type(&field.input_value.type_, TypePosition::Output)?;
                Ok(quote! { pub #field_name: #field_type, })
            })
            .collect::<Result<Vec<_>, CodegenError>>()?;

        Ok(quote! {
            #[derive(Serialize, Deserialize, Debug, PartialEq, Clone)]
            pub struct #name { #(#fields)* }
        })
    }

    fn render_object(
        self,
        definition: &FullType,
        include_loadable: bool,
    ) -> Result<TokenStream, CodegenError> {
        let wire_name = required_name(definition)?;
        let name = type_ident(wire_name);
        let option_structs = self.render_option_structs(definition)?;
        let methods = definition
            .fields
            .as_deref()
            .unwrap_or_default()
            .iter()
            .map(|field| self.render_method(field))
            .collect::<Result<Vec<_>, _>>()?;
        let into_id = has_id_field(definition).then(|| {
            quote! {
                impl IntoID<Id> for #name {
                    fn into_id(self) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<Id, QueryError>> + Send>> {
                        Box::pin(async move { self.id().await })
                    }
                }

                impl From<#name> for IdInput<#name> {
                    fn from(value: #name) -> Self { IdInput::lazy(value) }
                }
            }
        });
        let loadable = (include_loadable && has_id_field(definition))
            .then(|| render_loadable(&name, wire_name));

        Ok(quote! {
            #[derive(Clone)]
            pub struct #name {
                pub(crate) session: SessionHandle,
                pub(crate) selection: Selection,
            }

            #option_structs
            #into_id
            #loadable

            impl #name { #(#methods)* }
        })
    }

    fn render_interface(self, definition: &FullType) -> Result<TokenStream, CodegenError> {
        let wire_name = required_name(definition)?;
        let trait_name = type_ident(wire_name);
        let client_wire_name = format!("{wire_name}Client");
        let client_name = type_ident(&client_wire_name);
        let mut client = definition.clone();
        client.name = Some(client_wire_name);
        let parent = client.clone();
        for field in client.fields.as_mut().into_iter().flatten() {
            field.parent_type = Some(parent.clone());
        }

        let trait_methods = definition
            .fields
            .as_deref()
            .unwrap_or_default()
            .iter()
            .map(|field| self.render_trait_method(field))
            .collect::<Result<Vec<_>, _>>()?;
        let impl_methods = definition
            .fields
            .as_deref()
            .unwrap_or_default()
            .iter()
            .map(|field| self.render_trait_impl_method(field))
            .collect::<Result<Vec<_>, _>>()?;
        let client_tokens = self.render_object(&client, false)?;
        let loadable = has_id_field(&client).then(|| render_loadable(&client_name, wire_name));
        let docs = doc_attributes(definition.description.as_deref());

        Ok(quote! {
            #(#docs)*
            pub trait #trait_name { #(#trait_methods)* }

            #client_tokens
            #loadable

            impl #trait_name for #client_name { #(#impl_methods)* }
        })
    }

    fn render_option_structs(self, definition: &FullType) -> Result<TokenStream, CodegenError> {
        let structs = definition
            .fields
            .as_deref()
            .unwrap_or_default()
            .iter()
            .filter(|field| optional_arguments(field).next().is_some())
            .map(|field| {
                let name = options_ident(field)?;
                let optional = optional_arguments(field).collect::<Vec<_>>();
                let has_lifetime = optional.iter().any(|argument| {
                    rust_type(&argument.input_value.type_, TypePosition::ImmutableInput)
                        .is_ok_and(|tokens| tokens.to_string().contains("'a"))
                });
                let lifetime = has_lifetime.then(|| quote! { <'a> });
                let fields = optional
                    .into_iter()
                    .sorted_by_key(|argument| argument.input_value.name.as_str())
                    .map(|argument| {
                        let docs = doc_attributes(argument.input_value.description.as_deref());
                        let name = value_ident(&argument.input_value.name);
                        let value_type =
                            rust_type(&argument.input_value.type_, TypePosition::ImmutableInput)?;
                        Ok(quote! {
                            #(#docs)*
                            #[builder(setter(into, strip_option), default)]
                            pub #name: Option<#value_type>,
                        })
                    })
                    .collect::<Result<Vec<_>, CodegenError>>()?;
                Ok(quote! {
                    #[derive(Builder, Debug, PartialEq)]
                    pub struct #name #lifetime { #(#fields)* }
                })
            })
            .collect::<Result<Vec<_>, CodegenError>>()?;
        Ok(quote! { #(#structs)* })
    }

    fn render_method(self, field: &FullTypeField) -> Result<TokenStream, CodegenError> {
        let field_name = field
            .name
            .as_deref()
            .ok_or_else(|| schema_error("field is missing its name"))?;
        let name = value_ident(field_name);
        let opts_name = format_ident!("{}_opts", name);
        let required = required_signature_arguments(field)?;
        let docs = method_docs(field);
        let output = field_output_type(field)?;
        let is_async = convert_id(field) || !field_returns_object(field)?;
        let async_token = is_async.then(|| quote! { async });
        let base_body = method_body(field, false)?;

        if optional_arguments(field).next().is_some() {
            let option_type = options_ident(field)?;
            let has_lifetime = optional_arguments(field).any(|argument| {
                rust_type(&argument.input_value.type_, TypePosition::ImmutableInput)
                    .is_ok_and(|tokens| tokens.to_string().contains("'a"))
            });
            let lifetime = has_lifetime.then(|| quote! { <'a> });
            let option_body = method_body(field, true)?;
            Ok(quote! {
                #(#docs)*
                pub #async_token fn #name(&self, #(#required),*) -> #output { #base_body }

                #(#docs)*
                pub #async_token fn #opts_name #lifetime(
                    &self,
                    #(#required,)*
                    opts: #option_type #lifetime,
                ) -> #output { #option_body }
            })
        } else {
            Ok(quote! {
                #(#docs)*
                pub #async_token fn #name(&self, #(#required),*) -> #output { #base_body }
            })
        }
    }

    fn render_trait_method(self, field: &FullTypeField) -> Result<TokenStream, CodegenError> {
        let name = value_ident(required_field_name(field)?);
        let docs = doc_attributes(field.description.as_deref());
        let required = required_signature_arguments(field)?;
        let return_type = return_type_ref(field)?;
        let output = rust_type(return_type, TypePosition::Output)?;
        if field_returns_object(field)? {
            Ok(quote! { #(#docs)* fn #name(&self, #(#required),*) -> #output; })
        } else {
            Ok(quote! {
                #(#docs)*
                fn #name(&self, #(#required),*) -> impl core::future::Future<Output = Result<#output, QueryError>> + Send;
            })
        }
    }

    fn render_trait_impl_method(self, field: &FullTypeField) -> Result<TokenStream, CodegenError> {
        let name = value_ident(required_field_name(field)?);
        let required = required_signature_arguments(field)?;
        let return_type = return_type_ref(field)?;
        let output = rust_type(return_type, TypePosition::Output)?;
        let body = method_body(field, false)?;
        if field_returns_object(field)? {
            Ok(quote! { fn #name(&self, #(#required),*) -> #output { #body } })
        } else {
            // Trait methods use RPITIT rather than `async fn`, so the query is built
            // synchronously and only execution is captured by the returned future.
            let setup = method_setup(field, false)?;
            Ok(quote! {
                fn #name(&self, #(#required),*) -> impl core::future::Future<Output = Result<#output, QueryError>> + Send {
                    #setup
                    let session = self.session.clone();
                    async move { query.execute(&session).await }
                }
            })
        }
    }

    fn render_object_interface_impl(
        self,
        object: &FullType,
        interface: &FullType,
    ) -> Result<TokenStream, CodegenError> {
        let object_name = type_ident(required_name(object)?);
        let interface_name = type_ident(required_name(interface)?);
        let interface_client_name = type_ident(&format!("{}Client", required_name(interface)?));
        let methods = interface
            .fields
            .as_deref()
            .unwrap_or_default()
            .iter()
            .map(|field| self.render_trait_impl_method(field))
            .collect::<Result<Vec<_>, _>>()?;
        let id_input = has_id_field(object).then(|| {
            quote! {
                impl From<#object_name> for IdInput<#interface_client_name> {
                    fn from(value: #object_name) -> Self { IdInput::lazy(value) }
                }
            }
        });
        Ok(quote! {
            impl #interface_name for #object_name { #(#methods)* }
            #id_input
        })
    }
}

fn render_loadable(name: &Ident, graphql_name: &str) -> TokenStream {
    quote! {
        impl Sealed for #name {
            fn graphql_type() -> &'static str { #graphql_name }
            fn from_query(session: SessionHandle, selection: Selection) -> Self {
                Self { session, selection }
            }
        }
    }
}

fn method_body(field: &FullTypeField, include_optional: bool) -> Result<TokenStream, CodegenError> {
    let setup = method_setup(field, include_optional)?;
    let execution = method_execution(field)?;
    Ok(quote! { #setup #execution })
}

fn method_setup(
    field: &FullTypeField,
    include_optional: bool,
) -> Result<TokenStream, CodegenError> {
    let wire_name = required_field_name(field)?;
    let required = required_argument_setup(field)?;
    let optional = include_optional
        .then(|| optional_argument_setup(field))
        .transpose()?;
    Ok(quote! {
        let mut query = self.selection.select(#wire_name);
        #required
        #optional
    })
}

fn method_execution(field: &FullTypeField) -> Result<TokenStream, CodegenError> {
    if convert_id(field) {
        let parent = field
            .parent_type
            .as_ref()
            .and_then(|value| value.name.as_deref())
            .ok_or_else(|| schema_error("converted ID field is missing its parent"))?;
        return Ok(quote! {
            let id: Id = query.execute(&self.session).await?;
            Ok(crate::query::reenter(&self.session, id, #parent))
        });
    }

    let type_ref = return_type_ref(field)?;
    if is_object(type_ref) {
        let output = rust_type(type_ref, TypePosition::Output)?;
        return Ok(quote! { #output { session: self.session.clone(), selection: query } });
    }
    if is_list_of_objects(type_ref) {
        let element = list_element(type_ref)
            .ok_or_else(|| schema_error("object list is missing its element type"))?;
        let output = rust_type(element, TypePosition::Output)?;
        let graphql_name = strip_non_null(element)
            .name
            .as_deref()
            .ok_or_else(|| schema_error("object list element is missing its name"))?;
        return Ok(quote! {
            let query = query.select("id");
            query.execute_reentry::<#output, Vec<Id>>(&self.session, #graphql_name).await
        });
    }
    Ok(quote! { query.execute(&self.session).await })
}

fn required_signature_arguments(field: &FullTypeField) -> Result<Vec<TokenStream>, CodegenError> {
    required_arguments(field)
        .map(|argument| {
            let name = value_ident(&argument.input_value.name);
            let value_type = rust_type(&argument.input_value.type_, TypePosition::Input)?;
            if is_id(&argument.input_value.type_) {
                Ok(quote! { #name: impl IntoID<#value_type> })
            } else {
                Ok(quote! { #name: #value_type })
            }
        })
        .collect()
}

fn required_argument_setup(field: &FullTypeField) -> Result<TokenStream, CodegenError> {
    let statements = required_arguments(field)
        .map(|argument| {
            let name = value_ident(&argument.input_value.name);
            let wire_name = &argument.input_value.name;
            if is_string(&argument.input_value.type_) {
                return Ok(quote! { query = query.arg(#wire_name, #name.into()); });
            }
            if is_list_of_strings(&argument.input_value.type_) {
                return Ok(quote! {
                    query = query.arg(
                        #wire_name,
                        #name.into_iter().map(|item| item.into()).collect::<Vec<String>>(),
                    );
                });
            }
            if is_id(&argument.input_value.type_) {
                return Ok(quote! {
                    query = query.arg_id_input(#wire_name, IdInput::<Id>::lazy(#name));
                });
            }
            Ok(quote! { query = query.arg(#wire_name, #name); })
        })
        .collect::<Result<Vec<_>, CodegenError>>()?;
    Ok(quote! { #(#statements)* })
}

fn optional_argument_setup(field: &FullTypeField) -> Result<TokenStream, CodegenError> {
    let statements = optional_arguments(field)
        .map(|argument| {
            let name = value_ident(&argument.input_value.name);
            let wire_name = &argument.input_value.name;
            quote! {
                if let Some(#name) = opts.#name { query = query.arg(#wire_name, #name); }
            }
        })
        .collect::<Vec<_>>();
    Ok(quote! { #(#statements)* })
}

fn method_docs(field: &FullTypeField) -> Vec<TokenStream> {
    let mut docs = doc_attributes(field.description.as_deref());
    let arguments = required_arguments(field)
        .filter_map(|argument| {
            argument
                .input_value
                .description
                .as_deref()
                .filter(|description| !description.is_empty())
                .map(|description| format!("* `{}` - {description}", argument.input_value.name))
        })
        .collect::<Vec<_>>();
    if !arguments.is_empty() {
        docs.extend(doc_attributes(Some("\n# Arguments\n")));
        docs.extend(arguments.iter().flat_map(|line| doc_attributes(Some(line))));
    }
    docs
}

fn doc_attributes(description: Option<&str>) -> Vec<TokenStream> {
    description
        .into_iter()
        .flat_map(str::lines)
        .map(|line| line.trim())
        .filter(|line| !line.is_empty())
        .map(|line| quote! { #[doc = #line] })
        .collect()
}

fn options_ident(field: &FullTypeField) -> Result<Ident, CodegenError> {
    let parent = field
        .parent_type
        .as_ref()
        .and_then(|value| value.name.as_deref())
        .ok_or_else(|| schema_error("optional argument field is missing its parent"))?;
    Ok(type_ident(&format!(
        "{}{}Opts",
        parent.to_case(Case::Pascal),
        required_field_name(field)?.to_case(Case::Pascal)
    )))
}

fn field_output_type(field: &FullTypeField) -> Result<TokenStream, CodegenError> {
    if convert_id(field) {
        let parent = field
            .parent_type
            .as_ref()
            .and_then(|value| value.name.as_deref())
            .ok_or_else(|| schema_error("converted ID field is missing its parent"))?;
        let parent = type_ident(parent);
        return Ok(quote! { Result<#parent, QueryError> });
    }
    let type_ref = return_type_ref(field)?;
    let output = rust_type(type_ref, TypePosition::Output)?;
    if is_object(type_ref) {
        Ok(output)
    } else {
        Ok(quote! { Result<#output, QueryError> })
    }
}

fn rust_type(type_ref: &TypeRef, position: TypePosition) -> Result<TokenStream, CodegenError> {
    match type_ref.kind.as_ref() {
        Some(TypeKind::NonNull) => rust_type(
            type_ref
                .of_type
                .as_deref()
                .ok_or_else(|| schema_error("NON_NULL wrapper is missing its inner type"))?,
            position,
        ),
        Some(TypeKind::List) => {
            let inner = rust_type(
                type_ref
                    .of_type
                    .as_deref()
                    .ok_or_else(|| schema_error("LIST wrapper is missing its inner type"))?,
                position,
            )?;
            Ok(quote! { Vec<#inner> })
        }
        Some(TypeKind::Scalar) => match type_ref.name.as_deref() {
            Some("Int") => Ok(quote! { isize }),
            Some("Float") => Ok(quote! { f64 }),
            Some("String") if position == TypePosition::Input => Ok(quote! { impl Into<String> }),
            Some("String") if position == TypePosition::ImmutableInput => Ok(quote! { &'a str }),
            Some("String") => Ok(quote! { String }),
            Some("Boolean") => Ok(quote! { bool }),
            Some("Void") if position == TypePosition::Output => Ok(quote! { () }),
            Some(name) => {
                let name = type_ident(name);
                Ok(quote! { #name })
            }
            None => Err(schema_error("scalar reference is missing its name")),
        },
        Some(TypeKind::Interface) => {
            let name = type_ref
                .name
                .as_deref()
                .ok_or_else(|| schema_error("interface reference is missing its name"))?;
            let name = type_ident(&format!("{name}Client"));
            Ok(quote! { #name })
        }
        Some(TypeKind::Object | TypeKind::Enum | TypeKind::InputObject) => {
            let name = type_ref
                .name
                .as_deref()
                .ok_or_else(|| schema_error("named reference is missing its name"))?;
            let name = type_ident(name);
            Ok(quote! { #name })
        }
        Some(TypeKind::Union) => Err(schema_error("union projection is not supported")),
        Some(TypeKind::Other(kind)) => Err(schema_error(format!("unsupported type kind {kind}"))),
        None => Err(schema_error("type reference is missing its kind")),
    }
}

fn required_name(definition: &FullType) -> Result<&str, CodegenError> {
    definition
        .name
        .as_deref()
        .ok_or_else(|| schema_error("type definition is missing its name"))
}

fn required_field_name(field: &FullTypeField) -> Result<&str, CodegenError> {
    field
        .name
        .as_deref()
        .ok_or_else(|| schema_error("field is missing its name"))
}

fn return_type_ref(field: &FullTypeField) -> Result<&TypeRef, CodegenError> {
    field
        .type_
        .as_ref()
        .map(|value| &value.type_ref)
        .ok_or_else(|| schema_error("field is missing its return type"))
}

fn required_arguments(field: &FullTypeField) -> impl Iterator<Item = &FullTypeFieldArgument> {
    field
        .args
        .as_deref()
        .unwrap_or_default()
        .iter()
        .flatten()
        .filter(|argument| !is_optional(&argument.input_value.type_))
}

fn optional_arguments(field: &FullTypeField) -> impl Iterator<Item = &FullTypeFieldArgument> {
    field
        .args
        .as_deref()
        .unwrap_or_default()
        .iter()
        .flatten()
        .filter(|argument| is_optional(&argument.input_value.type_))
}

fn is_optional(type_ref: &TypeRef) -> bool {
    type_ref.kind.as_ref() != Some(&TypeKind::NonNull)
}

fn strip_non_null(type_ref: &TypeRef) -> &TypeRef {
    if type_ref.kind.as_ref() == Some(&TypeKind::NonNull) {
        type_ref.of_type.as_deref().unwrap_or(type_ref)
    } else {
        type_ref
    }
}

fn list_element(type_ref: &TypeRef) -> Option<&TypeRef> {
    let value = strip_non_null(type_ref);
    (value.kind.as_ref() == Some(&TypeKind::List))
        .then_some(value.of_type.as_deref())
        .flatten()
}

fn is_object(type_ref: &TypeRef) -> bool {
    matches!(
        strip_non_null(type_ref).kind.as_ref(),
        Some(TypeKind::Object | TypeKind::Interface)
    )
}

fn is_list_of_objects(type_ref: &TypeRef) -> bool {
    list_element(type_ref).is_some_and(is_object)
}

fn field_returns_object(field: &FullTypeField) -> Result<bool, CodegenError> {
    return_type_ref(field).map(is_object)
}

fn is_string(type_ref: &TypeRef) -> bool {
    let value = strip_non_null(type_ref);
    value.kind.as_ref() == Some(&TypeKind::Scalar) && value.name.as_deref() == Some("String")
}

fn is_list_of_strings(type_ref: &TypeRef) -> bool {
    list_element(type_ref).is_some_and(is_string)
}

fn is_id(type_ref: &TypeRef) -> bool {
    let value = strip_non_null(type_ref);
    value.kind.as_ref() == Some(&TypeKind::Scalar) && value.name.as_deref() == Some("ID")
}

fn convert_id(field: &FullTypeField) -> bool {
    if field.name.as_deref() == Some("id") {
        return false;
    }
    let Some(type_ref) = field.type_.as_ref().map(|value| &value.type_ref) else {
        return false;
    };
    let value = strip_non_null(type_ref);
    if value.kind.as_ref() != Some(&TypeKind::Scalar) || value.name.as_deref() != Some("ID") {
        return false;
    }
    let Some(expected) = field.directives.expected_type() else {
        return false;
    };
    field
        .parent_type
        .as_ref()
        .and_then(|parent| parent.name.as_deref())
        == Some(expected.as_str())
}

fn has_id_field(definition: &FullType) -> bool {
    definition
        .fields
        .as_deref()
        .unwrap_or_default()
        .iter()
        .any(|field| field.name.as_deref() == Some("id"))
}

fn type_ident(value: &str) -> Ident {
    format_ident!("{}", value.to_case(Case::Pascal))
}

fn value_ident(value: &str) -> Ident {
    let value = value.to_case(Case::Snake);
    match value.as_str() {
        "ref" | "enum" | "loop" | "mod" | "type" => Ident::new_raw(&value, Span::call_site()),
        _ => format_ident!("{value}"),
    }
}

fn schema_error(message: impl Into<String>) -> CodegenError {
    CodegenError::new(DiagnosticKind::Schema, message)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum TypePosition {
    Input,
    ImmutableInput,
    Output,
}

/// Converts a validated syntax file into deterministic candidate text.
pub(crate) fn candidate_text(file: &syn::File) -> String {
    file.to_token_stream().to_string()
}
