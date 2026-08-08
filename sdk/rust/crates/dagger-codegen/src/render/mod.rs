//! Rust syntax rendering from validated projection artifacts.
//!
//! Rendering accepts the immutable semantic plan and returns parsed, in-memory source.
//! It cannot reopen schema bytes, mutate projection policy, publish files, or invoke a
//! formatter. Filesystem and process authority remain with `dagger-bootstrap`.

mod docs;
pub mod verification;

use std::collections::{BTreeMap, BTreeSet};

use proc_macro2::{Ident, TokenStream};
use quote::{ToTokens, quote};

use crate::diagnostic::{
    CodegenError, Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticKind, DiagnosticSet,
};
use crate::projection::fields::{
    ArgumentPresence, ArgumentProjection, FieldProjection, FieldStrategy,
};
use crate::projection::types::{
    EnumProjection, InputObjectProjection, InterfaceProjection, ObjectProjection, RustType,
    ScalarKind, TypeProjection,
};
use crate::schema::canonical::{
    FieldDefinition, InputFieldDefinition, SchemaCoordinate, SchemaName, TypeDefinition,
};
use crate::{ProjectionPlan, RenderedCandidate};

use self::verification::GeneratedVerification;

const GENERATOR_FORMAT: &str = "dagger-rust-client-v1";
const GENERATED_ROOT: &str = "crates/dagger-sdk/src/gen";
const GENERATED_TEST_ROOT: &str = "crates/dagger-sdk/tests";

/// The sole pre-manifest artifact that later publication may retire explicitly.
pub const LEGACY_GENERATED_PREDECESSOR: &str = "crates/dagger-sdk/src/gen.rs";

/// Parses emitted tokens for the transitional single-file generator.
pub(crate) fn validate_file(tokens: TokenStream) -> Result<syn::File, CodegenError> {
    syn::parse2(tokens)
        .map_err(|error| CodegenError::new(DiagnosticKind::Render, error.to_string()))
}

pub(crate) fn render_plan(plan: &ProjectionPlan) -> Result<RenderedCandidate, DiagnosticSet> {
    let mut artifacts = BTreeMap::new();
    let mut modules = Vec::new();
    let mut diagnostics = Vec::new();

    for projection in plan.named_types().values() {
        let (wire_name, module_name, tokens) = match render_named_type(plan, projection) {
            Ok(Some(rendered)) => rendered,
            Ok(None) => continue,
            Err(error) => {
                diagnostics.push(error);
                continue;
            }
        };
        let module = match source_ident(&module_name, &SchemaCoordinate::named_type(&wire_name)) {
            Ok(module) => module,
            Err(error) => {
                diagnostics.push(error);
                continue;
            }
        };
        let file_stem = module_name.strip_prefix("r#").unwrap_or(&module_name);
        let relative_path = format!("{file_stem}.rs");
        let path = format!("{GENERATED_ROOT}/{relative_path}");
        let module_doc = format!(
            "Generated bindings owned by the GraphQL `{}` type.",
            wire_name.as_str()
        );
        match finish_file(
            plan,
            &path,
            &SchemaCoordinate::named_type(&wire_name),
            &module_doc,
            tokens,
        ) {
            Ok(source) => {
                artifacts.insert(path, source);
                modules.push((module, relative_path));
            }
            Err(error) => diagnostics.extend(error.diagnostics().iter().cloned()),
        }
    }

    if let Some(errors) = DiagnosticSet::new(diagnostics) {
        return Err(errors);
    }

    let module_declarations = modules.iter().map(|(module, path)| {
        quote! {
            #[path = #path]
            mod #module;
        }
    });
    let mut public_reexports = modules.iter().map(|(module, _)| module).collect::<Vec<_>>();
    public_reexports.sort_by_key(ToString::to_string);
    let index_tokens = quote! {
        #(#module_declarations)*
        #(pub use #public_reexports::*;)*
    };
    let index_path = format!("{GENERATED_ROOT}/mod.rs");
    let index_source = finish_file(
        plan,
        &index_path,
        &SchemaCoordinate::query_root(),
        "Generated Dagger core-schema bindings and stable public re-exports.",
        index_tokens,
    )?;
    artifacts.insert(index_path, index_source);

    let public_symbols = collect_public_symbols(plan, &artifacts)?;
    let (reachability, referenced_symbols) = render_reachability(plan)?;
    let reachability_path = format!("{GENERATED_TEST_ROOT}/core_reachability.rs");
    artifacts.insert(
        reachability_path.clone(),
        finish_file(
            plan,
            &reachability_path,
            &SchemaCoordinate::semantic("generated-public-reachability"),
            "Compile-only reachability proof for every generated public Rust symbol.",
            reachability,
        )?,
    );

    let projection_test = render_projection_inventory(plan);
    let projection_path = format!("{GENERATED_TEST_ROOT}/core_projection.rs");
    artifacts.insert(
        projection_path.clone(),
        finish_file(
            plan,
            &projection_path,
            &SchemaCoordinate::semantic("generated-query-projection"),
            "Structured field and argument inventory for exhaustive query verification.",
            projection_test,
        )?,
    );

    let verification = verification::assemble(plan, public_symbols, referenced_symbols)?;
    Ok(RenderedCandidate::new(artifacts, verification))
}

struct RenderedNamedType {
    wire_name: SchemaName,
    module_name: String,
    tokens: TokenStream,
}

impl From<RenderedNamedType> for (SchemaName, String, TokenStream) {
    fn from(value: RenderedNamedType) -> Self {
        (value.wire_name, value.module_name, value.tokens)
    }
}

fn render_named_type(
    plan: &ProjectionPlan,
    projection: &TypeProjection,
) -> Result<Option<(SchemaName, String, TokenStream)>, Diagnostic> {
    let rendered = match projection {
        TypeProjection::Object(object) => RenderedNamedType {
            wire_name: object.wire_name.clone(),
            module_name: object.module_name.clone(),
            tokens: render_object(plan, object)?,
        },
        TypeProjection::Interface(interface) => RenderedNamedType {
            wire_name: interface.wire_name.clone(),
            module_name: interface.module_name.clone(),
            tokens: render_interface(plan, interface)?,
        },
        TypeProjection::Enum(enumeration) => RenderedNamedType {
            wire_name: enumeration.wire_name.clone(),
            module_name: module_name(plan, &enumeration.coordinate)?,
            tokens: render_enum(plan, enumeration)?,
        },
        TypeProjection::InputObject(input) => RenderedNamedType {
            wire_name: input.wire_name.clone(),
            module_name: input.module_name.clone(),
            tokens: render_input_object(plan, input)?,
        },
        TypeProjection::Scalar(_) | TypeProjection::TargetPrivate(_) => return Ok(None),
    };
    Ok(Some(rendered.into()))
}

fn module_name(plan: &ProjectionPlan, coordinate: &SchemaCoordinate) -> Result<String, Diagnostic> {
    plan.names()
        .get(coordinate, crate::naming::NameContext::Module)
        .map(|name| name.identifier.clone())
        .ok_or_else(|| render_error(coordinate, "generated type has no projected module name"))
}

fn render_enum(
    plan: &ProjectionPlan,
    enumeration: &EnumProjection,
) -> Result<TokenStream, Diagnostic> {
    let name = source_ident(&enumeration.rust_name, &enumeration.coordinate)?;
    let definition = match plan.schema().types().get(&enumeration.wire_name) {
        Some(TypeDefinition::Enum(definition)) => definition,
        _ => {
            return Err(render_error(
                &enumeration.coordinate,
                "enum projection lost its canonical definition",
            ));
        }
    };
    let attributes = public_attributes(
        &enumeration.coordinate,
        definition.description.as_deref(),
        &format!("Generated enum for GraphQL `{}`.", enumeration.wire_name),
        None,
        None,
    )?;
    let mut variants = Vec::new();
    for variant in enumeration.variants.values() {
        let variant_name = source_ident(&variant.rust_name, &variant.coordinate)?;
        let aliases = enumeration
            .aliases
            .values()
            .filter(|alias| alias.canonical_wire_name == variant.wire_name)
            .map(|alias| alias.wire_name.as_str());
        let wire_name = variant.wire_name.as_str();
        let variant_attributes = public_attributes(
            &variant.coordinate,
            variant.description.as_deref(),
            &format!("GraphQL enum value `{wire_name}`."),
            variant.deprecation.as_deref(),
            variant.experimental.as_deref(),
        )?;
        variants.push(quote! {
            #variant_attributes
            #[serde(rename = #wire_name #(, alias = #aliases)*)]
            #variant_name,
        });
    }

    Ok(quote! {
        #attributes
        #[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde::Deserialize, serde::Serialize)]
        pub enum #name {
            #(#variants)*
        }
    })
}

fn render_input_object(
    plan: &ProjectionPlan,
    input: &InputObjectProjection,
) -> Result<TokenStream, Diagnostic> {
    let name = source_ident(&input.rust_name, &input.coordinate)?;
    let constructor = source_ident(&input.constructor_name, &input.coordinate)?;
    let definition = match plan.schema().types().get(&input.wire_name) {
        Some(TypeDefinition::InputObject(definition)) => definition,
        _ => {
            return Err(render_error(
                &input.coordinate,
                "input projection lost its canonical definition",
            ));
        }
    };
    let attributes = public_attributes(
        &input.coordinate,
        definition.description.as_deref(),
        &format!("Owned GraphQL input object `{}`.", input.wire_name),
        None,
        None,
    )?;

    let mut fields = Vec::new();
    let mut required_parameters = Vec::new();
    let mut initializers = Vec::new();
    let mut setters = Vec::new();
    for field in input.fields.values() {
        let field_name = source_ident(&field.rust_name, &field.coordinate)?;
        let value_type = input_field_type_tokens(plan, &input.wire_name, &field.rust_type)?;
        let definition = definition.fields.get(&field.wire_name).ok_or_else(|| {
            render_error(
                &field.coordinate,
                "input field lost its canonical definition",
            )
        })?;
        let fallback = input_field_contract(field, definition);
        let field_attributes = public_attributes(
            &field.coordinate,
            definition.description.as_deref(),
            &fallback,
            definition
                .deprecation
                .as_ref()
                .and_then(|deprecation| deprecation.reason.as_deref()),
            plan.directives().experimental_reason(&field.coordinate),
        )?;
        let wire_name = field.wire_name.as_str();
        match field.presence {
            ArgumentPresence::Required => {
                fields.push(quote! {
                    #field_attributes
                    #[serde(rename = #wire_name)]
                    pub #field_name: #value_type,
                });
                required_parameters.push(quote! { #field_name: #value_type });
                initializers.push(quote! { #field_name });
            }
            ArgumentPresence::Omittable { .. } => {
                fields.push(quote! {
                    #field_attributes
                    #[serde(
                        rename = #wire_name,
                        default,
                        skip_serializing_if = "Option::is_none"
                    )]
                    pub #field_name: Option<#value_type>,
                });
                initializers.push(quote! { #field_name: None });
                let setter_name = field.setter_name.as_deref().ok_or_else(|| {
                    render_error(&field.coordinate, "omittable input field has no setter")
                })?;
                let setter = source_ident(setter_name, &field.coordinate)?;
                let setter_doc = docs::documentation(
                    &field.coordinate,
                    None,
                    &format!(
                        "Sets GraphQL input field `{wire_name}`; the field is omitted until this method is called."
                    ),
                )?;
                setters.push(quote! {
                    #[doc = #setter_doc]
                    #[must_use]
                    pub fn #setter(mut self, value: #value_type) -> Self {
                        self.#field_name = Some(value);
                        self
                    }
                });
            }
        }
    }
    let constructor_doc = docs::documentation(
        &input.coordinate,
        None,
        &format!(
            "Creates `{}` with every required GraphQL input field.",
            input.rust_name
        ),
    )?;

    Ok(quote! {
        #attributes
        #[derive(Clone, Debug, PartialEq, serde::Deserialize, serde::Serialize)]
        #[non_exhaustive]
        pub struct #name {
            #(#fields)*
        }

        impl #name {
            #[doc = #constructor_doc]
            #[must_use]
            pub fn #constructor(#(#required_parameters),*) -> Self {
                Self { #(#initializers),* }
            }

            #(#setters)*
        }
    })
}

fn input_field_contract(
    projection: &crate::projection::types::InputFieldProjection,
    _definition: &InputFieldDefinition,
) -> String {
    match &projection.presence {
        ArgumentPresence::Required => {
            format!("Required GraphQL input field `{}`.", projection.wire_name)
        }
        ArgumentPresence::Omittable { engine_default } => match engine_default {
            Some(default) => format!(
                "Optional GraphQL input field `{}`; omission preserves the engine default `{default:?}`.",
                projection.wire_name
            ),
            None => format!(
                "Optional GraphQL input field `{}`; `None` omits its Wire_Name.",
                projection.wire_name
            ),
        },
    }
}

fn render_object(
    plan: &ProjectionPlan,
    object: &ObjectProjection,
) -> Result<TokenStream, Diagnostic> {
    let name = source_ident(&object.rust_name, &object.coordinate)?;
    let definition = match plan.schema().types().get(&object.wire_name) {
        Some(TypeDefinition::Object(definition)) => definition,
        _ => {
            return Err(render_error(
                &object.coordinate,
                "object projection lost its canonical definition",
            ));
        }
    };
    let attributes = public_attributes(
        &object.coordinate,
        definition.description.as_deref(),
        &format!("Lazy handle for GraphQL object `{}`.", object.wire_name),
        None,
        None,
    )?;
    let options = render_options_for_owner(plan, &object.wire_name, &object.fields)?;
    let methods = render_inherent_methods(plan, &object.fields)?;
    let support = render_handle_support(plan, &object.wire_name, &name, object.has_id)?;
    let interfaces = render_declared_interface_impls(plan, &object.wire_name, &name)?;

    Ok(quote! {
        #attributes
        #[derive(Clone)]
        pub struct #name {
            pub(crate) session: crate::lifecycle::SessionHandle,
            pub(crate) selection: crate::query::Selection,
        }

        #options
        #support

        impl #name {
            #methods
        }

        #interfaces
    })
}

fn render_interface(
    plan: &ProjectionPlan,
    interface: &InterfaceProjection,
) -> Result<TokenStream, Diagnostic> {
    let trait_name = source_ident(&interface.trait_name, &interface.coordinate)?;
    let client_name = source_ident(&interface.client_name, &interface.coordinate)?;
    let definition = match plan.schema().types().get(&interface.wire_name) {
        Some(TypeDefinition::Interface(definition)) => definition,
        _ => {
            return Err(render_error(
                &interface.coordinate,
                "interface projection lost its canonical definition",
            ));
        }
    };
    let trait_attributes = public_attributes(
        &interface.coordinate,
        definition.description.as_deref(),
        &format!(
            "Generated trait for GraphQL interface `{}`.",
            interface.wire_name
        ),
        None,
        None,
    )?;
    let client_doc = docs::documentation(
        &interface.coordinate,
        None,
        &format!(
            "Lazy client handle for GraphQL interface `{}`.",
            interface.wire_name
        ),
    )?;
    let options = render_options_for_owner(plan, &interface.wire_name, &interface.fields)?;
    let trait_methods = render_trait_declarations(plan, &interface.fields)?;
    let inherent_methods = render_inherent_methods(plan, &interface.fields)?;
    let own_impl = render_trait_impl(plan, &trait_name, &client_name, &interface.fields)?;
    let support =
        render_handle_support(plan, &interface.wire_name, &client_name, interface.has_id)?;
    let parent_interfaces =
        render_declared_interface_impls(plan, &interface.wire_name, &client_name)?;

    Ok(quote! {
        #trait_attributes
        pub trait #trait_name: Clone + Send + Sync {
            #trait_methods
        }

        #[doc = #client_doc]
        #[derive(Clone)]
        pub struct #client_name {
            pub(crate) session: crate::lifecycle::SessionHandle,
            pub(crate) selection: crate::query::Selection,
        }

        #options
        #support

        impl #client_name {
            #inherent_methods
        }

        #own_impl
        #parent_interfaces
    })
}

fn render_options_for_owner(
    plan: &ProjectionPlan,
    owner: &SchemaName,
    field_coordinates: &BTreeSet<SchemaCoordinate>,
) -> Result<TokenStream, Diagnostic> {
    let mut rendered = Vec::new();
    for coordinate in field_coordinates {
        let field = plan
            .fields()
            .get(coordinate)
            .ok_or_else(|| render_error(coordinate, "type field has no operation projection"))?;
        let Some(type_name) = field.options_type_name.as_deref() else {
            continue;
        };
        let name = source_ident(type_name, coordinate)?;
        let type_doc = docs::documentation(
            coordinate,
            None,
            &format!(
                "Owned optional arguments for GraphQL operation `{}.{}`; reuse does not mutate caller state.",
                owner, field.wire_name
            ),
        )?;
        let definition = field_definition(plan, field)?;
        let mut fields = Vec::new();
        let mut setters = Vec::new();
        for argument in field
            .arguments
            .iter()
            .filter(|argument| argument.presence.is_omittable())
        {
            let field_name = source_ident(&argument.rust_name, &argument.coordinate)?;
            let value_type = rust_type_tokens(plan, &argument.rust_type)?;
            let canonical = definition
                .arguments
                .get(&argument.wire_name)
                .ok_or_else(|| {
                    render_error(
                        &argument.coordinate,
                        "option argument lost its canonical definition",
                    )
                })?;
            let field_doc = option_argument_documentation(argument, canonical)?;
            fields.push(quote! {
                #[doc = #field_doc]
                pub #field_name: Option<#value_type>,
            });
            let setter_name = source_ident(
                &format!("with_{}", argument.rust_name),
                &argument.coordinate,
            )?;
            let setter_doc = docs::documentation(
                &argument.coordinate,
                None,
                &format!(
                    "Sets GraphQL argument `{}` to a concrete value instead of omitting it.",
                    argument.wire_name
                ),
            )?;
            setters.push(quote! {
                #[doc = #setter_doc]
                #[must_use]
                pub fn #setter_name(mut self, value: #value_type) -> Self {
                    self.#field_name = Some(value);
                    self
                }
            });
        }
        rendered.push(quote! {
            #[doc = #type_doc]
            #[derive(Clone, Debug, Default)]
            #[non_exhaustive]
            pub struct #name {
                #(#fields)*
            }

            impl #name {
                #(#setters)*
            }
        });
    }
    Ok(quote! { #(#rendered)* })
}

fn option_argument_documentation(
    argument: &ArgumentProjection,
    canonical: &crate::schema::canonical::ArgumentDefinition,
) -> Result<String, Diagnostic> {
    let omission = match &argument.presence {
        ArgumentPresence::Required => "This argument is required.".to_owned(),
        ArgumentPresence::Omittable { engine_default } => match engine_default {
            Some(default) => format!(
                "`None` omits GraphQL Wire_Name `{}` and preserves engine default `{default:?}`.",
                argument.wire_name
            ),
            None => format!("`None` omits GraphQL Wire_Name `{}`.", argument.wire_name),
        },
    };
    let mut source = canonical.description.clone().unwrap_or_default();
    if !source.is_empty() {
        source.push_str("\n\n");
    }
    source.push_str(&omission);
    if let Some(reason) = argument.deprecation.as_deref() {
        source.push_str(&format!("\n\n**Deprecated:** {reason}"));
    }
    if let Some(reason) = argument.experimental.as_deref() {
        source.push_str(&format!("\n\n**Experimental:** {reason}"));
    }
    docs::documentation(&argument.coordinate, Some(&source), &omission)
}

fn render_inherent_methods(
    plan: &ProjectionPlan,
    coordinates: &BTreeSet<SchemaCoordinate>,
) -> Result<TokenStream, Diagnostic> {
    coordinates
        .iter()
        .map(|coordinate| {
            let field = plan.fields().get(coordinate).ok_or_else(|| {
                render_error(coordinate, "type field has no operation projection")
            })?;
            render_inherent_method(plan, field)
        })
        .collect::<Result<Vec<_>, _>>()
        .map(|methods| quote! { #(#methods)* })
}

fn render_inherent_method(
    plan: &ProjectionPlan,
    field: &FieldProjection,
) -> Result<TokenStream, Diagnostic> {
    let attributes = method_attributes(plan, field)?;
    let name = source_ident(&field.rust_name, &field.coordinate)?;
    let required = required_parameters(plan, field, false)?;
    let output = method_output_type(plan, field)?;
    let setup = method_setup(plan, field, false)?;
    let body = method_execution(plan, field, quote! { &self.session })?;
    let ordinary = if matches!(field.strategy, FieldStrategy::LazyHandle { .. }) {
        quote! {
            #attributes
            #[must_use]
            pub fn #name(&self #(, #required)*) -> #output {
                #setup
                #body
            }
        }
    } else {
        quote! {
            #attributes
            pub async fn #name(&self #(, #required)*) -> Result<#output, crate::QueryError> {
                #setup
                #body
            }
        }
    };

    let Some(options_method) = field.options_method_name.as_deref() else {
        return Ok(ordinary);
    };
    let options_name = field
        .options_type_name
        .as_deref()
        .ok_or_else(|| render_error(&field.coordinate, "options method has no options type"))?;
    let options_method = source_ident(options_method, &field.coordinate)?;
    let options_type = source_ident(options_name, &field.coordinate)?;
    let options_attributes = public_attributes(
        &field.coordinate,
        None,
        &format!(
            "Executes GraphQL operation `{}` with a borrowed, reusable `{options_name}` value.",
            field.wire_name
        ),
        field.deprecation.as_deref(),
        field.experimental.as_deref(),
    )?;
    let setup = method_setup(plan, field, true)?;
    let body = method_execution(plan, field, quote! { &self.session })?;
    let options = if matches!(field.strategy, FieldStrategy::LazyHandle { .. }) {
        quote! {
            #options_attributes
            #[must_use]
            pub fn #options_method(
                &self,
                #(#required,)*
                opts: &#options_type,
            ) -> #output {
                #setup
                #body
            }
        }
    } else {
        quote! {
            #options_attributes
            pub async fn #options_method(
                &self,
                #(#required,)*
                opts: &#options_type,
            ) -> Result<#output, crate::QueryError> {
                #setup
                #body
            }
        }
    };
    Ok(quote! { #ordinary #options })
}

fn render_trait_declarations(
    plan: &ProjectionPlan,
    coordinates: &BTreeSet<SchemaCoordinate>,
) -> Result<TokenStream, Diagnostic> {
    coordinates
        .iter()
        .map(|coordinate| {
            let field = plan.fields().get(coordinate).ok_or_else(|| {
                render_error(coordinate, "interface field has no operation projection")
            })?;
            render_trait_declaration(plan, field)
        })
        .collect::<Result<Vec<_>, _>>()
        .map(|methods| quote! { #(#methods)* })
}

fn render_trait_declaration(
    plan: &ProjectionPlan,
    field: &FieldProjection,
) -> Result<TokenStream, Diagnostic> {
    let attributes = method_attributes(plan, field)?;
    let name = source_ident(&field.rust_name, &field.coordinate)?;
    let required = required_parameters(plan, field, true)?;
    let output = method_output_type(plan, field)?;
    let ordinary = if matches!(field.strategy, FieldStrategy::LazyHandle { .. }) {
        quote! {
            #attributes
            #[must_use]
            fn #name(&self #(, #required)*) -> #output;
        }
    } else {
        quote! {
            #attributes
            fn #name(&self #(, #required)*)
                -> impl core::future::Future<Output = Result<#output, crate::QueryError>> + Send;
        }
    };
    let Some(options_method) = field.options_method_name.as_deref() else {
        return Ok(ordinary);
    };
    let options_method = source_ident(options_method, &field.coordinate)?;
    let options_name = field
        .options_type_name
        .as_deref()
        .ok_or_else(|| render_error(&field.coordinate, "options method has no options type"))?;
    let options_type = source_ident(options_name, &field.coordinate)?;
    let options_attributes = public_attributes(
        &field.coordinate,
        None,
        &format!(
            "Uses borrowed optional arguments for GraphQL operation `{}`.",
            field.wire_name
        ),
        field.deprecation.as_deref(),
        field.experimental.as_deref(),
    )?;
    let options = if matches!(field.strategy, FieldStrategy::LazyHandle { .. }) {
        quote! {
            #options_attributes
            #[must_use]
            fn #options_method(&self, #(#required,)* opts: &#options_type) -> #output;
        }
    } else {
        quote! {
            #options_attributes
            fn #options_method<'a>(
                &'a self,
                #(#required,)*
                opts: &'a #options_type,
            ) -> impl core::future::Future<Output = Result<#output, crate::QueryError>> + Send + 'a;
        }
    };
    Ok(quote! { #ordinary #options })
}

fn render_trait_impl(
    plan: &ProjectionPlan,
    trait_name: &Ident,
    implementor: &Ident,
    coordinates: &BTreeSet<SchemaCoordinate>,
) -> Result<TokenStream, Diagnostic> {
    let methods = coordinates
        .iter()
        .map(|coordinate| {
            let field = plan.fields().get(coordinate).ok_or_else(|| {
                render_error(coordinate, "interface field has no operation projection")
            })?;
            render_trait_impl_method(plan, field)
        })
        .collect::<Result<Vec<_>, _>>()?;
    Ok(quote! {
        impl super::#trait_name for #implementor {
            #(#methods)*
        }
    })
}

fn render_trait_impl_method(
    plan: &ProjectionPlan,
    field: &FieldProjection,
) -> Result<TokenStream, Diagnostic> {
    let name = source_ident(&field.rust_name, &field.coordinate)?;
    let required = required_parameters(plan, field, true)?;
    let output = method_output_type(plan, field)?;
    let setup = method_setup(plan, field, false)?;
    let ordinary = if matches!(field.strategy, FieldStrategy::LazyHandle { .. }) {
        let body = method_execution(plan, field, quote! { &self.session })?;
        quote! {
            fn #name(&self #(, #required)*) -> #output {
                #setup
                #body
            }
        }
    } else {
        let body = method_execution(plan, field, quote! { &session })?;
        quote! {
            fn #name(&self #(, #required)*)
                -> impl core::future::Future<Output = Result<#output, crate::QueryError>> + Send
            {
                #setup
                let session = self.session.clone();
                async move { #body }
            }
        }
    };

    let Some(options_method) = field.options_method_name.as_deref() else {
        return Ok(ordinary);
    };
    let options_method = source_ident(options_method, &field.coordinate)?;
    let options_name = field
        .options_type_name
        .as_deref()
        .ok_or_else(|| render_error(&field.coordinate, "options method has no options type"))?;
    let options_type = source_ident(options_name, &field.coordinate)?;
    let setup = method_setup(plan, field, true)?;
    let options = if matches!(field.strategy, FieldStrategy::LazyHandle { .. }) {
        let body = method_execution(plan, field, quote! { &self.session })?;
        quote! {
            fn #options_method(&self, #(#required,)* opts: &super::#options_type) -> #output {
                #setup
                #body
            }
        }
    } else {
        let body = method_execution(plan, field, quote! { &session })?;
        quote! {
            fn #options_method<'a>(
                &'a self,
                #(#required,)*
                opts: &'a super::#options_type,
            ) -> impl core::future::Future<Output = Result<#output, crate::QueryError>> + Send + 'a
            {
                #setup
                let session = self.session.clone();
                async move { #body }
            }
        }
    };
    Ok(quote! { #ordinary #options })
}

fn render_declared_interface_impls(
    plan: &ProjectionPlan,
    implementor_wire_name: &SchemaName,
    implementor: &Ident,
) -> Result<TokenStream, Diagnostic> {
    let mut implementations = Vec::new();
    for edge in plan
        .implementations()
        .iter()
        .filter(|edge| &edge.implementor == implementor_wire_name)
    {
        let interface = match plan.named_types().get(&edge.interface) {
            Some(TypeProjection::Interface(interface)) => interface,
            _ => {
                return Err(render_error(
                    &edge.coordinate,
                    "interface edge has no interface projection",
                ));
            }
        };
        let trait_name = source_ident(&interface.trait_name, &edge.coordinate)?;
        implementations.push(render_trait_impl(
            plan,
            &trait_name,
            implementor,
            &interface.fields,
        )?);
    }
    Ok(quote! { #(#implementations)* })
}

fn render_handle_support(
    plan: &ProjectionPlan,
    wire_name: &SchemaName,
    handle: &Ident,
    has_id: bool,
) -> Result<TokenStream, Diagnostic> {
    if !has_id {
        return Ok(TokenStream::new());
    }
    let mut conversions = vec![quote! {
        impl From<#handle> for crate::IdInput<#handle> {
            fn from(value: #handle) -> Self {
                crate::IdInput::lazy(value)
            }
        }
    }];
    for edge in plan
        .implementations()
        .iter()
        .filter(|edge| &edge.implementor == wire_name)
    {
        let interface = handle_type_ident(plan, &edge.interface, &edge.coordinate)?;
        conversions.push(quote! {
            impl From<#handle> for crate::IdInput<super::#interface> {
                fn from(value: #handle) -> Self {
                    crate::IdInput::lazy(value)
                }
            }
        });
    }
    let graphql_name = wire_name.as_str();
    Ok(quote! {
        impl crate::IntoID<crate::Id> for #handle {
            fn into_id(
                self,
            ) -> core::pin::Pin<
                Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
            > {
                Box::pin(async move { self.id().await })
            }
        }

        impl crate::loadable::private::Sealed for #handle {
            fn graphql_type() -> &'static str {
                #graphql_name
            }

            fn from_query(
                session: crate::lifecycle::SessionHandle,
                selection: crate::query::Selection,
            ) -> Self {
                Self { session, selection }
            }
        }

        #(#conversions)*
    })
}

fn required_parameters(
    plan: &ProjectionPlan,
    field: &FieldProjection,
    send: bool,
) -> Result<Vec<TokenStream>, Diagnostic> {
    field
        .arguments
        .iter()
        .filter(|argument| argument.presence == ArgumentPresence::Required)
        .map(|argument| {
            let name = source_ident(&argument.rust_name, &argument.coordinate)?;
            let value_type = rust_type_tokens(plan, &argument.rust_type)?;
            let send_bound = send.then(|| quote! { + Send });
            match &argument.rust_type {
                RustType::String => Ok(quote! { #name: impl Into<String> #send_bound }),
                RustType::IdInput(_) => Ok(quote! { #name: impl Into<#value_type> #send_bound }),
                _ => Ok(quote! { #name: #value_type }),
            }
        })
        .collect()
}

fn method_setup(
    plan: &ProjectionPlan,
    field: &FieldProjection,
    include_options: bool,
) -> Result<TokenStream, Diagnostic> {
    let wire_name = field.wire_name.as_str();
    let mut statements = Vec::new();
    for argument in &field.arguments {
        let name = source_ident(&argument.rust_name, &argument.coordinate)?;
        let argument_wire_name = argument.wire_name.as_str();
        match &argument.presence {
            ArgumentPresence::Required => {
                if argument.encoder.contains_lazy_id() {
                    let value = if matches!(argument.rust_type, RustType::IdInput(_)) {
                        quote! { #name.into() }
                    } else {
                        quote! { #name }
                    };
                    statements.push(quote! {
                        let query = query.arg_id_input(#argument_wire_name, #value);
                    });
                } else if matches!(argument.rust_type, RustType::String) {
                    statements.push(quote! {
                        let query = query.arg(#argument_wire_name, #name.into());
                    });
                } else {
                    statements.push(quote! {
                        let query = query.arg(#argument_wire_name, #name);
                    });
                }
            }
            ArgumentPresence::Omittable { .. } if include_options => {
                if argument.encoder.contains_lazy_id() {
                    statements.push(quote! {
                        let query = if let Some(value) = &opts.#name {
                            query.arg_id_input(#argument_wire_name, value.clone())
                        } else {
                            query
                        };
                    });
                } else {
                    statements.push(quote! {
                        let query = if let Some(value) = &opts.#name {
                            query.arg(#argument_wire_name, value)
                        } else {
                            query
                        };
                    });
                }
            }
            ArgumentPresence::Omittable { .. } => {}
        }
    }
    let _ = plan;
    Ok(quote! {
        let query = self.selection.select(#wire_name);
        #(#statements)*
    })
}

fn method_execution(
    plan: &ProjectionPlan,
    field: &FieldProjection,
    session: TokenStream,
) -> Result<TokenStream, Diagnostic> {
    match &field.strategy {
        FieldStrategy::LazyHandle { target } => {
            let target = handle_type_ident(plan, target, &field.coordinate)?;
            Ok(quote! {
                super::#target {
                    session: self.session.clone(),
                    selection: query,
                }
            })
        }
        FieldStrategy::NullableHandle { target, .. }
        | FieldStrategy::ReenterList { target, .. } => {
            let target_ident = handle_type_ident(plan, target, &field.coordinate)?;
            let id_shape = id_shape_tokens(&field.return_type)?;
            let graphql_name = target.as_str();
            Ok(quote! {
                let query = query.select("id");
                query
                    .execute_reentry::<super::#target_ident, #id_shape>(#session, #graphql_name)
                    .await
            })
        }
        FieldStrategy::ExecuteValue { .. } => Ok(quote! { query.execute(#session).await }),
        FieldStrategy::ExpectedTypeSelf { parent, .. } => {
            let parent_ident = handle_type_ident(plan, parent, &field.coordinate)?;
            let graphql_name = parent.as_str();
            Ok(quote! {
                let id: crate::Id = query.execute(#session).await?;
                Ok(crate::query::reenter::<super::#parent_ident>(#session, id, #graphql_name))
            })
        }
        FieldStrategy::TargetPrivate => Err(render_error(
            &field.coordinate,
            "target-private field reached public method rendering",
        )),
    }
}

fn method_output_type(
    plan: &ProjectionPlan,
    field: &FieldProjection,
) -> Result<TokenStream, Diagnostic> {
    if let FieldStrategy::ExpectedTypeSelf { parent, .. } = &field.strategy {
        let parent = handle_type_ident(plan, parent, &field.coordinate)?;
        return Ok(quote! { super::#parent });
    }
    rust_type_tokens(plan, &field.return_type)
}

fn method_attributes(
    plan: &ProjectionPlan,
    field: &FieldProjection,
) -> Result<TokenStream, Diagnostic> {
    let canonical = field_definition(plan, field)?;
    let mut description = canonical.description.clone().unwrap_or_default();
    if !description.is_empty() {
        description.push_str("\n\n");
    }
    description.push_str(&format!(
        "Selects GraphQL Wire_Name `{}` on `{}`.",
        field.wire_name, field.owner
    ));
    public_attributes(
        &field.coordinate,
        Some(&description),
        &description,
        field.deprecation.as_deref(),
        field.experimental.as_deref(),
    )
}

fn field_definition<'a>(
    plan: &'a ProjectionPlan,
    field: &FieldProjection,
) -> Result<&'a FieldDefinition, Diagnostic> {
    let definition = plan.schema().types().get(&field.owner).ok_or_else(|| {
        render_error(&field.coordinate, "field owner has no canonical definition")
    })?;
    let fields = match definition {
        TypeDefinition::Object(definition) => &definition.fields,
        TypeDefinition::Interface(definition) => &definition.fields,
        _ => {
            return Err(render_error(
                &field.coordinate,
                "field owner is not an object or interface",
            ));
        }
    };
    fields.get(&field.wire_name).ok_or_else(|| {
        render_error(
            &field.coordinate,
            "field projection lost its canonical definition",
        )
    })
}

fn rust_type_tokens(
    plan: &ProjectionPlan,
    rust_type: &RustType,
) -> Result<TokenStream, Diagnostic> {
    match rust_type {
        RustType::Bool => Ok(quote! { bool }),
        RustType::F64 => Ok(quote! { f64 }),
        RustType::I64 => Ok(quote! { i64 }),
        RustType::String => Ok(quote! { String }),
        RustType::Id => Ok(quote! { crate::Id }),
        RustType::Json => Ok(quote! { crate::Json }),
        RustType::Platform => Ok(quote! { crate::Platform }),
        RustType::Unit => Ok(quote! { () }),
        RustType::Enum(name) | RustType::Input(name) | RustType::Handle(name) => {
            let name = handle_or_value_type_ident(plan, name)?;
            Ok(quote! { super::#name })
        }
        RustType::InterfaceHandle(name) => {
            let name = handle_type_ident(plan, name, &SchemaCoordinate::named_type(name))?;
            Ok(quote! { super::#name })
        }
        RustType::IdInput(name) => {
            let target = handle_type_ident(plan, name, &SchemaCoordinate::named_type(name))?;
            Ok(quote! { crate::IdInput<super::#target> })
        }
        RustType::Option(inner) => {
            let inner = rust_type_tokens(plan, inner)?;
            Ok(quote! { Option<#inner> })
        }
        RustType::Vec(inner) => {
            let inner = rust_type_tokens(plan, inner)?;
            Ok(quote! { Vec<#inner> })
        }
    }
}

fn input_field_type_tokens(
    plan: &ProjectionPlan,
    owner: &SchemaName,
    rust_type: &RustType,
) -> Result<TokenStream, Diagnostic> {
    input_field_type_tokens_inner(plan, owner, rust_type, false)
}

fn input_field_type_tokens_inner(
    plan: &ProjectionPlan,
    owner: &SchemaName,
    rust_type: &RustType,
    behind_vec: bool,
) -> Result<TokenStream, Diagnostic> {
    match rust_type {
        RustType::Input(target)
            if !behind_vec && input_reaches(plan, target, owner, &mut BTreeSet::new()) =>
        {
            let target = handle_or_value_type_ident(plan, target)?;
            Ok(quote! { Box<super::#target> })
        }
        RustType::Option(inner) => {
            let inner = input_field_type_tokens_inner(plan, owner, inner, behind_vec)?;
            Ok(quote! { Option<#inner> })
        }
        RustType::Vec(inner) => {
            let inner = input_field_type_tokens_inner(plan, owner, inner, true)?;
            Ok(quote! { Vec<#inner> })
        }
        _ => rust_type_tokens(plan, rust_type),
    }
}

fn input_reaches(
    plan: &ProjectionPlan,
    current: &SchemaName,
    target: &SchemaName,
    visited: &mut BTreeSet<SchemaName>,
) -> bool {
    if current == target {
        return true;
    }
    if !visited.insert(current.clone()) {
        return false;
    }
    let Some(TypeProjection::InputObject(input)) = plan.named_types().get(current) else {
        return false;
    };
    input.fields.values().any(|field| {
        direct_input_dependencies(&field.rust_type)
            .into_iter()
            .any(|dependency| input_reaches(plan, dependency, target, visited))
    })
}

fn direct_input_dependencies(rust_type: &RustType) -> Vec<&SchemaName> {
    match rust_type {
        RustType::Input(name) => vec![name],
        RustType::Option(inner) => direct_input_dependencies(inner),
        RustType::Vec(_) => Vec::new(),
        _ => Vec::new(),
    }
}

fn id_shape_tokens(rust_type: &RustType) -> Result<TokenStream, Diagnostic> {
    match rust_type {
        RustType::Handle(_) | RustType::InterfaceHandle(_) => Ok(quote! { crate::Id }),
        RustType::Option(inner) => {
            let inner = id_shape_tokens(inner)?;
            Ok(quote! { Option<#inner> })
        }
        RustType::Vec(inner) => {
            let inner = id_shape_tokens(inner)?;
            Ok(quote! { Vec<#inner> })
        }
        _ => Err(render_error(
            &SchemaCoordinate::semantic("id-reentry-shape"),
            "re-entry output contains a non-handle leaf",
        )),
    }
}

fn handle_or_value_type_ident(
    plan: &ProjectionPlan,
    name: &SchemaName,
) -> Result<Ident, Diagnostic> {
    let projection = plan.named_types().get(name).ok_or_else(|| {
        render_error(
            &SchemaCoordinate::named_type(name),
            "named Rust type has no projection",
        )
    })?;
    let rust_name = match projection {
        TypeProjection::Object(object) => &object.rust_name,
        TypeProjection::Interface(interface) => &interface.client_name,
        TypeProjection::Enum(enumeration) => &enumeration.rust_name,
        TypeProjection::InputObject(input) => &input.rust_name,
        TypeProjection::Scalar(_) | TypeProjection::TargetPrivate(_) => {
            return Err(render_error(
                &SchemaCoordinate::named_type(name),
                "named Rust type is not generated",
            ));
        }
    };
    source_ident(rust_name, &SchemaCoordinate::named_type(name))
}

fn handle_type_ident(
    plan: &ProjectionPlan,
    name: &SchemaName,
    coordinate: &SchemaCoordinate,
) -> Result<Ident, Diagnostic> {
    let projection = plan
        .named_types()
        .get(name)
        .ok_or_else(|| render_error(coordinate, "handle target has no named projection"))?;
    let rust_name = match projection {
        TypeProjection::Object(object) => &object.rust_name,
        TypeProjection::Interface(interface) => &interface.client_name,
        _ => {
            return Err(render_error(
                coordinate,
                "handle target is not an object or interface",
            ));
        }
    };
    source_ident(rust_name, coordinate)
}

fn public_attributes(
    coordinate: &SchemaCoordinate,
    description: Option<&str>,
    fallback: &str,
    deprecation: Option<&str>,
    experimental: Option<&str>,
) -> Result<TokenStream, Diagnostic> {
    let mut documentation = docs::documentation(coordinate, description, fallback)?;
    if let Some(reason) = deprecation {
        let reason = docs::sanitize(coordinate, reason)?;
        documentation.push_str(&format!("\n\n**Deprecated:** {reason}"));
    }
    if let Some(reason) = experimental {
        let reason = docs::sanitize(coordinate, reason)?;
        documentation.push_str(&format!("\n\n**Experimental:** {reason}"));
    }
    let deprecated = deprecation.map(|reason| {
        let note = reason.replace(['\r', '\n'], " ");
        quote! { #[deprecated(note = #note)] }
    });
    Ok(quote! {
        #[doc = #documentation]
        #deprecated
    })
}

fn source_ident(source: &str, coordinate: &SchemaCoordinate) -> Result<Ident, Diagnostic> {
    syn::parse_str::<Ident>(source)
        .map_err(|_| render_error(coordinate, "projected Rust identifier is not valid syntax"))
}

fn finish_file(
    plan: &ProjectionPlan,
    path: &str,
    coordinate: &SchemaCoordinate,
    module_doc: &str,
    tokens: TokenStream,
) -> Result<Vec<u8>, DiagnosticSet> {
    let provenance = serde_json::json!({
        "format": GENERATOR_FORMAT,
        "ownership": "dagger-codegen",
        "schema_digest": plan.target().schema_digest().to_string(),
        "target_revision": plan.target().dagger_revision().as_str(),
    });
    let provenance = serde_json::to_string(&provenance).map_err(|_| {
        DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::GeneratedProvenanceInvalid,
            Some(DiagnosticCoordinate::new(path)),
            "generated provenance could not be encoded",
        ))
    })?;
    let source = format!(
        "//! {module_doc}\n// @generated {provenance}\n{}\n",
        tokens.into_token_stream()
    );
    syn::parse_file(&source).map_err(|_| {
        DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::GeneratedFormatFailed,
            Some(DiagnosticCoordinate::new(path)),
            format!(
                "rendered Rust syntax for `{}` did not parse",
                coordinate.as_str()
            ),
        ))
    })?;
    Ok(source.into_bytes())
}

fn collect_public_symbols(
    plan: &ProjectionPlan,
    artifacts: &BTreeMap<String, Vec<u8>>,
) -> Result<BTreeSet<String>, DiagnosticSet> {
    let mut symbols = BTreeSet::new();
    for (path, bytes) in artifacts {
        if !path.starts_with(GENERATED_ROOT) || path.ends_with("/mod.rs") {
            continue;
        }
        let source = std::str::from_utf8(bytes).map_err(|_| {
            DiagnosticSet::one(Diagnostic::new(
                DiagnosticCode::GeneratedFormatFailed,
                Some(DiagnosticCoordinate::new(path)),
                "rendered Rust source is not UTF-8",
            ))
        })?;
        let file = syn::parse_file(source).map_err(|_| {
            DiagnosticSet::one(Diagnostic::new(
                DiagnosticCode::GeneratedFormatFailed,
                Some(DiagnosticCoordinate::new(path)),
                "rendered Rust source could not be inspected",
            ))
        })?;
        collect_file_symbols(&file, &mut symbols);
    }
    for projection in plan.named_types().values() {
        if let TypeProjection::Scalar(scalar) = projection {
            match scalar.scalar {
                ScalarKind::Id => {
                    symbols.insert("dagger_sdk::Id".to_owned());
                }
                ScalarKind::Json => {
                    symbols.insert("dagger_sdk::Json".to_owned());
                }
                ScalarKind::Platform => {
                    symbols.insert("dagger_sdk::Platform".to_owned());
                }
                ScalarKind::Boolean
                | ScalarKind::Float
                | ScalarKind::Int
                | ScalarKind::String
                | ScalarKind::Void => {}
            }
        }
    }
    Ok(symbols)
}

fn collect_file_symbols(file: &syn::File, symbols: &mut BTreeSet<String>) {
    for item in &file.items {
        match item {
            syn::Item::Struct(item) if is_public(&item.vis) => {
                let owner = format!("dagger_sdk::{}", item.ident);
                symbols.insert(owner.clone());
                if let syn::Fields::Named(fields) = &item.fields {
                    for field in &fields.named {
                        if is_public(&field.vis)
                            && let Some(name) = &field.ident
                        {
                            symbols.insert(format!("{owner}::{name}"));
                        }
                    }
                }
            }
            syn::Item::Enum(item) if is_public(&item.vis) => {
                let owner = format!("dagger_sdk::{}", item.ident);
                symbols.insert(owner.clone());
                for variant in &item.variants {
                    symbols.insert(format!("{owner}::{}", variant.ident));
                }
            }
            syn::Item::Trait(item) if is_public(&item.vis) => {
                let owner = format!("dagger_sdk::{}", item.ident);
                symbols.insert(owner.clone());
                for method in &item.items {
                    if let syn::TraitItem::Fn(method) = method {
                        symbols.insert(format!("{owner}::{}", method.sig.ident));
                    }
                }
            }
            syn::Item::Impl(item) if item.trait_.is_none() => {
                let Some(owner) = impl_self_name(&item.self_ty) else {
                    continue;
                };
                for method in &item.items {
                    if let syn::ImplItem::Fn(method) = method
                        && is_public(&method.vis)
                    {
                        symbols.insert(format!("dagger_sdk::{owner}::{}", method.sig.ident));
                    }
                }
            }
            _ => {}
        }
    }
}

fn impl_self_name(self_type: &syn::Type) -> Option<&Ident> {
    let syn::Type::Path(path) = self_type else {
        return None;
    };
    path.path.segments.last().map(|segment| &segment.ident)
}

fn is_public(visibility: &syn::Visibility) -> bool {
    matches!(visibility, syn::Visibility::Public(_))
}

fn render_reachability(
    plan: &ProjectionPlan,
) -> Result<(TokenStream, BTreeSet<String>), DiagnosticSet> {
    let mut diagnostics = Vec::new();
    let mut helpers = Vec::new();
    let mut assertions = Vec::new();
    let mut symbols = BTreeSet::new();

    for projection in plan.named_types().values() {
        let result = match projection {
            TypeProjection::Scalar(scalar) => {
                let name = match scalar.scalar {
                    ScalarKind::Id => Some("Id"),
                    ScalarKind::Json => Some("Json"),
                    ScalarKind::Platform => Some("Platform"),
                    ScalarKind::Boolean
                    | ScalarKind::Float
                    | ScalarKind::Int
                    | ScalarKind::String
                    | ScalarKind::Void => None,
                };
                if let Some(name) = name {
                    match source_ident(name, &scalar.coordinate) {
                        Ok(name) => {
                            symbols.insert(format!("dagger_sdk::{name}"));
                            assertions.push(quote! { let _ = core::mem::size_of::<#name>(); });
                        }
                        Err(error) => diagnostics.push(error),
                    }
                }
                Ok(())
            }
            TypeProjection::Object(object) => render_handle_reachability(
                plan,
                &object.rust_name,
                &object.fields,
                None,
                &mut helpers,
                &mut assertions,
                &mut symbols,
            ),
            TypeProjection::Interface(interface) => render_handle_reachability(
                plan,
                &interface.client_name,
                &interface.fields,
                Some(&interface.trait_name),
                &mut helpers,
                &mut assertions,
                &mut symbols,
            ),
            TypeProjection::Enum(enumeration) => {
                render_enum_reachability(enumeration, &mut assertions, &mut symbols)
            }
            TypeProjection::InputObject(input) => {
                render_input_reachability(plan, input, &mut helpers, &mut assertions, &mut symbols)
            }
            TypeProjection::TargetPrivate(_) => Ok(()),
        };
        if let Err(error) = result {
            diagnostics.push(error);
        }
    }

    if let Some(errors) = DiagnosticSet::new(diagnostics) {
        return Err(errors);
    }
    Ok((
        quote! {
            use dagger_sdk::*;

            fn generated_value<T>() -> T {
                panic!("compile-only generated value")
            }

            #(#helpers)*

            #[test]
            fn generated_public_reachability() {
                #(#assertions)*
            }
        },
        symbols,
    ))
}

fn render_handle_reachability(
    plan: &ProjectionPlan,
    handle_name: &str,
    coordinates: &BTreeSet<SchemaCoordinate>,
    trait_name: Option<&str>,
    helpers: &mut Vec<TokenStream>,
    assertions: &mut Vec<TokenStream>,
    symbols: &mut BTreeSet<String>,
) -> Result<(), Diagnostic> {
    let handle = source_ident(handle_name, &SchemaCoordinate::semantic(handle_name))?;
    let helper = source_ident(
        &format!(
            "reach_{}",
            handle_name.trim_start_matches("r#").to_ascii_lowercase()
        ),
        &SchemaCoordinate::semantic(handle_name),
    )?;
    let mut calls = Vec::new();
    symbols.insert(format!("dagger_sdk::{handle_name}"));
    assertions.push(quote! {
        let _ = core::mem::size_of::<#handle>();
        let _ = #helper as fn(&#handle);
    });
    for coordinate in coordinates {
        let field = plan
            .fields()
            .get(coordinate)
            .ok_or_else(|| render_error(coordinate, "reachability field has no projection"))?;
        append_method_reachability(
            plan,
            field,
            handle_name,
            quote! { value },
            &mut calls,
            symbols,
        )?;
    }
    helpers.push(quote! {
        #[allow(deprecated)]
        fn #helper(value: &#handle) {
            #(#calls)*
        }
    });

    if let Some(trait_name) = trait_name {
        let trait_ident = source_ident(trait_name, &SchemaCoordinate::semantic(trait_name))?;
        let trait_helper = source_ident(
            &format!(
                "reach_{}_trait",
                trait_name.trim_start_matches("r#").to_ascii_lowercase()
            ),
            &SchemaCoordinate::semantic(trait_name),
        )?;
        let mut trait_calls = Vec::new();
        symbols.insert(format!("dagger_sdk::{trait_name}"));
        for coordinate in coordinates {
            let field = plan.fields().get(coordinate).ok_or_else(|| {
                render_error(coordinate, "trait reachability field has no projection")
            })?;
            append_method_reachability(
                plan,
                field,
                trait_name,
                quote! { value },
                &mut trait_calls,
                symbols,
            )?;
        }
        helpers.push(quote! {
            #[allow(deprecated)]
            fn #trait_helper<T: #trait_ident>(value: &T) {
                #(#trait_calls)*
            }
        });
        assertions.push(quote! {
            let _ = #trait_helper::<#handle> as fn(&#handle);
        });
    }
    Ok(())
}

fn append_method_reachability(
    plan: &ProjectionPlan,
    field: &FieldProjection,
    owner: &str,
    receiver: TokenStream,
    calls: &mut Vec<TokenStream>,
    symbols: &mut BTreeSet<String>,
) -> Result<(), Diagnostic> {
    let method = source_ident(&field.rust_name, &field.coordinate)?;
    let arguments = field
        .arguments
        .iter()
        .filter(|argument| argument.presence == ArgumentPresence::Required)
        .map(|argument| reach_method_value(plan, &argument.rust_type, &argument.coordinate))
        .collect::<Result<Vec<_>, _>>()?;
    calls.push(quote! { let _ = #receiver.#method(#(#arguments),*); });
    symbols.insert(format!("dagger_sdk::{owner}::{}", field.rust_name));

    let Some(options_method) = field.options_method_name.as_deref() else {
        return Ok(());
    };
    let options_method_ident = source_ident(options_method, &field.coordinate)?;
    let options_name = field.options_type_name.as_deref().ok_or_else(|| {
        render_error(&field.coordinate, "reachability options method has no type")
    })?;
    let options_type = source_ident(options_name, &field.coordinate)?;
    calls.push(quote! {
        let opts = #options_type::default();
        let _ = #receiver.#options_method_ident(#(#arguments,)* &opts);
    });
    symbols.insert(format!("dagger_sdk::{owner}::{options_method}"));
    symbols.insert(format!("dagger_sdk::{options_name}"));
    for argument in field
        .arguments
        .iter()
        .filter(|argument| argument.presence.is_omittable())
    {
        let field_name = source_ident(&argument.rust_name, &argument.coordinate)?;
        let setter_name = source_ident(
            &format!("with_{}", argument.rust_name),
            &argument.coordinate,
        )?;
        let value = reach_value(plan, &argument.rust_type, &argument.coordinate)?;
        calls.push(quote! {
            let _ = &opts.#field_name;
            let _ = #options_type::default().#setter_name(#value);
        });
        symbols.insert(format!(
            "dagger_sdk::{options_name}::{}",
            argument.rust_name
        ));
        symbols.insert(format!(
            "dagger_sdk::{options_name}::with_{}",
            argument.rust_name
        ));
    }
    Ok(())
}

fn render_enum_reachability(
    enumeration: &EnumProjection,
    assertions: &mut Vec<TokenStream>,
    symbols: &mut BTreeSet<String>,
) -> Result<(), Diagnostic> {
    let name = source_ident(&enumeration.rust_name, &enumeration.coordinate)?;
    symbols.insert(format!("dagger_sdk::{}", enumeration.rust_name));
    assertions.push(quote! { let _ = core::mem::size_of::<#name>(); });
    for variant in enumeration.variants.values() {
        let variant_name = source_ident(&variant.rust_name, &variant.coordinate)?;
        symbols.insert(format!(
            "dagger_sdk::{}::{}",
            enumeration.rust_name, variant.rust_name
        ));
        assertions.push(quote! {
            #[allow(deprecated)]
            let _ = #name::#variant_name;
        });
    }
    Ok(())
}

fn render_input_reachability(
    plan: &ProjectionPlan,
    input: &InputObjectProjection,
    helpers: &mut Vec<TokenStream>,
    assertions: &mut Vec<TokenStream>,
    symbols: &mut BTreeSet<String>,
) -> Result<(), Diagnostic> {
    let name = source_ident(&input.rust_name, &input.coordinate)?;
    let helper = source_ident(
        &format!("reach_{}_input", input.rust_name.to_ascii_lowercase()),
        &input.coordinate,
    )?;
    let constructor = source_ident(&input.constructor_name, &input.coordinate)?;
    let required = input
        .fields
        .values()
        .filter(|field| field.presence == ArgumentPresence::Required)
        .map(|field| reach_value(plan, &field.rust_type, &field.coordinate))
        .collect::<Result<Vec<_>, _>>()?;
    let mut statements = vec![quote! { let _ = #name::#constructor(#(#required),*); }];
    symbols.insert(format!("dagger_sdk::{}", input.rust_name));
    symbols.insert(format!(
        "dagger_sdk::{}::{}",
        input.rust_name, input.constructor_name
    ));
    for field in input.fields.values() {
        let field_name = source_ident(&field.rust_name, &field.coordinate)?;
        statements.push(quote! { let _ = &value.#field_name; });
        symbols.insert(format!(
            "dagger_sdk::{}::{}",
            input.rust_name, field.rust_name
        ));
        if let Some(setter) = field.setter_name.as_deref() {
            let setter_ident = source_ident(setter, &field.coordinate)?;
            let supplied = reach_value(plan, &field.rust_type, &field.coordinate)?;
            statements.push(quote! { let _ = value.clone().#setter_ident(#supplied); });
            symbols.insert(format!("dagger_sdk::{}::{setter}", input.rust_name));
        }
    }
    helpers.push(quote! {
        #[allow(deprecated)]
        fn #helper(value: &#name) {
            #(#statements)*
        }
    });
    assertions.push(quote! {
        let _ = core::mem::size_of::<#name>();
        let _ = #helper as fn(&#name);
    });
    Ok(())
}

fn reach_value(
    plan: &ProjectionPlan,
    rust_type: &RustType,
    coordinate: &SchemaCoordinate,
) -> Result<TokenStream, Diagnostic> {
    match rust_type {
        RustType::String => Ok(quote! { String::new() }),
        RustType::IdInput(_) => Ok(quote! { Id::from("generated-id").into() }),
        _ => {
            let value_type = rust_type_tokens(plan, rust_type)?;
            let value_type = externalize_type_tokens(value_type, coordinate)?;
            Ok(quote! { generated_value::<#value_type>() })
        }
    }
}

fn reach_method_value(
    plan: &ProjectionPlan,
    rust_type: &RustType,
    coordinate: &SchemaCoordinate,
) -> Result<TokenStream, Diagnostic> {
    if matches!(rust_type, RustType::IdInput(_)) {
        return Ok(quote! { Id::from("generated-id") });
    }
    reach_value(plan, rust_type, coordinate)
}

fn externalize_type_tokens(
    tokens: TokenStream,
    coordinate: &SchemaCoordinate,
) -> Result<TokenStream, Diagnostic> {
    let source = tokens
        .to_string()
        .replace("crate ::", "dagger_sdk ::")
        .replace("super ::", "");
    source
        .parse::<TokenStream>()
        .map_err(|_| render_error(coordinate, "reachability type could not be externalized"))
}

fn render_projection_inventory(plan: &ProjectionPlan) -> TokenStream {
    let fields = plan.fields().values().map(|field| {
        let coordinate = field.coordinate.as_str();
        let owner = field.owner.as_str();
        let wire_name = field.wire_name.as_str();
        let (boundary, concrete_type) = match &field.strategy {
            FieldStrategy::LazyHandle { target } => ("lazy", target.as_str()),
            FieldStrategy::NullableHandle { target, .. } => ("probe", target.as_str()),
            FieldStrategy::ReenterList { target, .. } => ("reenter", target.as_str()),
            FieldStrategy::ExecuteValue { .. } => ("execute", ""),
            FieldStrategy::ExpectedTypeSelf { parent, .. } => ("self-return", parent.as_str()),
            FieldStrategy::TargetPrivate => ("private", ""),
        };
        quote! { (#coordinate, #owner, #wire_name, #boundary, #concrete_type) }
    });
    let arguments = plan
        .fields()
        .values()
        .flat_map(|field| field.arguments.iter())
        .map(|argument| {
            let coordinate = argument.coordinate.as_str();
            let wire_name = argument.wire_name.as_str();
            let required = argument.presence == ArgumentPresence::Required;
            let lazy_id = argument.encoder.contains_lazy_id();
            quote! { (#coordinate, #wire_name, #required, #lazy_id) }
        });
    let field_count = plan.fields().values().count();
    let argument_count = plan
        .fields()
        .values()
        .map(|field| field.arguments.len())
        .sum::<usize>();
    quote! {
        const FIELDS: &[(&str, &str, &str, &str, &str)] = &[#(#fields),*];
        const ARGUMENTS: &[(&str, &str, bool, bool)] = &[#(#arguments),*];

        #[derive(Default)]
        struct RecordingExecutor {
            events: Vec<String>,
        }

        impl RecordingExecutor {
            fn observe(&mut self, field: &(&str, &str, &str, &str, &str)) {
                self.events.push(format!("select:{}", field.2));
                if !matches!(field.3, "lazy" | "private") {
                    self.events.push("request".to_owned());
                }
                if !field.4.is_empty() {
                    self.events.push(format!("fragment:{}", field.4));
                }
            }
        }

        #[test]
        fn generated_query_projection_inventory_is_exhaustive() {
            assert_eq!(FIELDS.len(), #field_count);
            assert_eq!(ARGUMENTS.len(), #argument_count);
            assert!(FIELDS.windows(2).all(|pair| pair[0].0 < pair[1].0));
            assert!(ARGUMENTS.windows(2).all(|pair| pair[0].0 < pair[1].0));

            for field in FIELDS {
                let mut recorder = RecordingExecutor::default();
                recorder.observe(field);
                assert_eq!(
                    recorder.events.iter().filter(|event| *event == "request").count(),
                    usize::from(!matches!(field.3, "lazy" | "private")),
                );
                assert_eq!(
                    recorder.events.iter().filter(|event| event.starts_with("fragment:")).count(),
                    usize::from(!field.4.is_empty()),
                );
            }

            let lazy_coordinates = ARGUMENTS
                .iter()
                .filter(|argument| argument.3)
                .map(|argument| argument.0)
                .collect::<Vec<_>>();
            assert!(lazy_coordinates.windows(2).all(|pair| pair[0] < pair[1]));
        }
    }
}

fn render_error(coordinate: &SchemaCoordinate, message: &str) -> Diagnostic {
    Diagnostic::new(
        DiagnosticCode::GeneratedFormatFailed,
        Some(DiagnosticCoordinate::new(coordinate.as_str())),
        message,
    )
}

impl RenderedCandidate {
    pub(crate) fn new(
        artifacts: BTreeMap<String, Vec<u8>>,
        verification: GeneratedVerification,
    ) -> Self {
        Self {
            artifacts,
            verification,
        }
    }
}
