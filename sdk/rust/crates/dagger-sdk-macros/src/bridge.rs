//! Typed bridge emission for authored objects, values, and functions.

use proc_macro2::TokenStream;
use quote::{format_ident, quote};
use syn::{
    Error, Fields, FnArg, GenericArgument, ImplItem, ImplItemFn, ItemEnum, ItemImpl, ItemStruct,
    ItemTrait, Pat, PathArguments, ReturnType, Type, parse_quote,
};

use crate::attribute::{Metadata, canonical_tokens, require_export_visibility};
use crate::fingerprint::fingerprint;

pub(crate) fn object(args: TokenStream, mut item: ItemStruct) -> syn::Result<TokenStream> {
    require_export_visibility(&item.vis)?;
    reject_generics(&item.generics)?;
    let outer = Metadata::parse_args(args)?;
    let Fields::Named(fields) = &mut item.fields else {
        return Err(Error::new_spanned(
            &item.fields,
            "a Dagger object must use named fields",
        ));
    };

    let mut fingerprint_parts = vec![
        "object".to_owned(),
        item.ident.to_string(),
        outer.canonical(),
    ];
    let mut persistent = Vec::new();
    let mut transient = Vec::new();
    for field in &mut fields.named {
        let metadata = Metadata::take_from(&mut field.attrs)?;
        let Some(ident) = field.ident.clone() else {
            return Err(Error::new_spanned(&*field, "object fields must be named"));
        };
        let policy = if metadata.has("field") {
            "field"
        } else if metadata.has("state") {
            "state"
        } else {
            "transient"
        };
        fingerprint_parts.extend([
            policy.to_owned(),
            ident.to_string(),
            canonical_tokens(&field.ty),
            metadata.canonical(),
        ]);
        if policy == "transient" {
            transient.push(ident);
        } else {
            persistent.push((ident, field.ty.clone()));
        }
    }

    let ident = &item.ident;
    let value = fingerprint(fingerprint_parts);
    let state_type = tuple_type(&persistent);
    let state_pattern = tuple_pattern(&persistent);
    let state_value = tuple_value(&persistent);
    let persistent_initializers = persistent.iter().map(|(field, _)| quote!(#field));
    let transient_initializers = transient
        .iter()
        .map(|field| quote!(#field: ::core::default::Default::default()));

    Ok(quote! {
        #item

        impl crate::dagger_generated::__private::ModuleObjectBridge for #ident {
            type PersistentState = #state_type;
            type Fingerprint = crate::dagger_generated::__private::AuthoringFingerprint<#value>;

            fn from_persistent_state(state: Self::PersistentState) -> Self {
                let #state_pattern = state;
                Self {
                    #(#persistent_initializers,)*
                    #(#transient_initializers,)*
                }
            }

            fn into_persistent_state(self) -> Self::PersistentState {
                #state_value
            }

            fn authoring_fingerprint() -> Self::Fingerprint {
                crate::dagger_generated::__private::AuthoringFingerprint
            }
        }
    })
}

pub(crate) fn interface(args: TokenStream, mut item: ItemTrait) -> syn::Result<TokenStream> {
    require_export_visibility(&item.vis)?;
    reject_generics(&item.generics)?;
    let outer = Metadata::parse_args(args)?;
    let mut parts = vec![
        "interface".to_owned(),
        item.ident.to_string(),
        outer.canonical(),
    ];
    for trait_item in &mut item.items {
        if let syn::TraitItem::Fn(function) = trait_item {
            let metadata = Metadata::take_from(&mut function.attrs)?;
            strip_inputs(&mut function.sig.inputs)?;
            parts.extend([
                function.sig.ident.to_string(),
                canonical_tokens(&function.sig),
                metadata.canonical(),
            ]);
        }
    }
    let ident = &item.ident;
    let value = fingerprint(parts);
    Ok(quote! {
        #item

        impl crate::dagger_generated::__private::ModuleInterfaceBridge for dyn #ident {
            type Fingerprint = crate::dagger_generated::__private::AuthoringFingerprint<#value>;

            fn authoring_fingerprint() -> Self::Fingerprint {
                crate::dagger_generated::__private::AuthoringFingerprint
            }
        }
    })
}

pub(crate) fn enum_type(args: TokenStream, mut item: ItemEnum) -> syn::Result<TokenStream> {
    require_export_visibility(&item.vis)?;
    reject_generics(&item.generics)?;
    let outer = Metadata::parse_args(args)?;
    let mut parts = vec!["enum".to_owned(), item.ident.to_string(), outer.canonical()];
    for variant in &mut item.variants {
        if !matches!(variant.fields, Fields::Unit) {
            return Err(Error::new_spanned(
                &variant.fields,
                "a Dagger enum supports only unit variants",
            ));
        }
        let metadata = Metadata::take_from(&mut variant.attrs)?;
        parts.extend([variant.ident.to_string(), metadata.canonical()]);
    }
    let ident = &item.ident;
    let value = fingerprint(parts);
    Ok(quote! {
        #item

        impl crate::dagger_generated::__private::ModuleEnumBridge for #ident {
            type Fingerprint = crate::dagger_generated::__private::AuthoringFingerprint<#value>;

            fn authoring_fingerprint() -> Self::Fingerprint {
                crate::dagger_generated::__private::AuthoringFingerprint
            }
        }
    })
}

pub(crate) fn scalar(args: TokenStream, mut item: ItemStruct) -> syn::Result<TokenStream> {
    require_export_visibility(&item.vis)?;
    reject_generics(&item.generics)?;
    let outer = Metadata::parse_args(args)?;
    let Fields::Unnamed(fields) = &mut item.fields else {
        return Err(Error::new_spanned(
            &item.fields,
            "a Dagger scalar must be a transparent one-field tuple struct",
        ));
    };
    if fields.unnamed.len() != 1 {
        return Err(Error::new_spanned(
            &fields.unnamed,
            "a Dagger scalar must contain exactly one field",
        ));
    }
    let field = &mut fields.unnamed[0];
    let metadata = Metadata::take_from(&mut field.attrs)?;
    let representation = field.ty.clone();
    let ident = item.ident.clone();
    let value = fingerprint([
        "scalar".to_owned(),
        ident.to_string(),
        outer.canonical(),
        canonical_tokens(&representation),
        metadata.canonical(),
    ]);
    Ok(quote! {
        #item

        impl crate::dagger_generated::__private::ModuleScalarBridge for #ident {
            type Representation = #representation;
            type Fingerprint = crate::dagger_generated::__private::AuthoringFingerprint<#value>;

            fn from_representation(value: Self::Representation) -> Self {
                Self(value)
            }

            fn into_representation(self) -> Self::Representation {
                self.0
            }

            fn authoring_fingerprint() -> Self::Fingerprint {
                crate::dagger_generated::__private::AuthoringFingerprint
            }
        }
    })
}

pub(crate) fn methods(args: TokenStream, mut item: ItemImpl) -> syn::Result<TokenStream> {
    if !args.is_empty() {
        Metadata::parse_args(args)?;
    }
    if item.trait_.is_some() {
        return Err(Error::new_spanned(
            &item,
            "the Dagger methods attribute requires an inherent impl",
        ));
    }
    if !item.generics.params.is_empty() {
        return Err(Error::new_spanned(
            &item.generics,
            "generic Dagger impl blocks are not supported",
        ));
    }

    let self_type = (*item.self_ty).clone();
    let mut bridges = Vec::new();
    let mut witnesses = Vec::new();
    for impl_item in &mut item.items {
        let ImplItem::Fn(function) = impl_item else {
            continue;
        };
        let metadata = Metadata::take_from(&mut function.attrs)?;
        let parameter_metadata = strip_inputs(&mut function.sig.inputs)?;
        let exported = metadata.has("constructor") || metadata.has("function");
        if !exported {
            if parameter_metadata
                .iter()
                .any(|metadata| !metadata.canonical().is_empty())
            {
                return Err(Error::new_spanned(
                    &function.sig,
                    "Dagger parameter metadata requires an exported function",
                ));
            }
            continue;
        }

        let value = method_fingerprint(&self_type, function, &metadata, &parameter_metadata);
        bridges.push(method_bridge(function, value)?);
        witnesses.push(quote! {
            impl crate::dagger_generated::__private::ModuleMethodBridge<#value> for #self_type {
                fn authoring_fingerprint(
                ) -> crate::dagger_generated::__private::AuthoringFingerprint<#value> {
                    crate::dagger_generated::__private::AuthoringFingerprint
                }
            }
        });
    }
    item.items.extend(bridges.into_iter().map(ImplItem::Fn));

    Ok(quote! {
        #item
        #(#witnesses)*
    })
}

fn method_fingerprint(
    self_type: &Type,
    function: &ImplItemFn,
    metadata: &Metadata,
    parameter_metadata: &[Metadata],
) -> u128 {
    let mut parts = vec![
        "method".to_owned(),
        canonical_tokens(self_type),
        function.sig.ident.to_string(),
        canonical_tokens(&function.sig),
        metadata.canonical(),
    ];
    parts.extend(parameter_metadata.iter().map(Metadata::canonical));
    fingerprint(parts)
}

fn method_bridge(function: &ImplItemFn, value: u128) -> syn::Result<ImplItemFn> {
    let original_name = &function.sig.ident;
    let bridge_name = format_ident!("__dagger_bridge_{}_{}", original_name, value);
    let mut bridge = function.clone();
    bridge.attrs.clear();
    bridge.vis = parse_quote!(pub(crate));
    bridge.sig.ident = bridge_name;
    let arguments = bridge
        .sig
        .inputs
        .iter()
        .filter_map(|argument| match argument {
            FnArg::Receiver(_) => None,
            FnArg::Typed(argument) => Some(argument),
        })
        .map(|argument| match argument.pat.as_ref() {
            Pat::Ident(ident) => Ok(ident.ident.clone()),
            pattern => Err(Error::new_spanned(
                pattern,
                "an exported Dagger parameter must use an identifier pattern",
            )),
        })
        .collect::<syn::Result<Vec<_>>>()?;
    let call = if function.sig.receiver().is_some() {
        quote!(self.#original_name(#(#arguments),*))
    } else {
        quote!(Self::#original_name(#(#arguments),*))
    };
    let call = if function.sig.asyncness.is_some() {
        quote!(#call.await)
    } else {
        call
    };

    if let Some(success) = result_success_type(&function.sig.output) {
        bridge.sig.output = parse_quote!(
            -> ::core::result::Result<
                #success,
                crate::dagger_generated::__private::ModuleError
            >
        );
        bridge.block = parse_quote!({ #call.map_err(::core::convert::Into::into) });
    } else {
        bridge.block = parse_quote!({ #call });
    }
    Ok(bridge)
}

fn result_success_type(output: &ReturnType) -> Option<Type> {
    let ReturnType::Type(_, ty) = output else {
        return None;
    };
    let Type::Path(path) = ty.as_ref() else {
        return None;
    };
    let segment = path.path.segments.last()?;
    if segment.ident != "Result" {
        return None;
    }
    let PathArguments::AngleBracketed(arguments) = &segment.arguments else {
        return None;
    };
    arguments.args.iter().find_map(|argument| match argument {
        GenericArgument::Type(success) => Some(success.clone()),
        _ => None,
    })
}

fn strip_inputs(
    inputs: &mut syn::punctuated::Punctuated<FnArg, syn::Token![,]>,
) -> syn::Result<Vec<Metadata>> {
    inputs
        .iter_mut()
        .map(|input| match input {
            FnArg::Receiver(receiver) => Metadata::take_from(&mut receiver.attrs),
            FnArg::Typed(argument) => Metadata::take_from(&mut argument.attrs),
        })
        .collect()
}

fn reject_generics(generics: &syn::Generics) -> syn::Result<()> {
    if generics.params.is_empty() && generics.where_clause.is_none() {
        Ok(())
    } else {
        Err(Error::new_spanned(
            generics,
            "generic Dagger exports are not supported",
        ))
    }
}

fn tuple_type(fields: &[(syn::Ident, Type)]) -> TokenStream {
    let types = fields.iter().map(|(_, ty)| ty);
    quote!((#(#types,)*))
}

fn tuple_pattern(fields: &[(syn::Ident, Type)]) -> TokenStream {
    let names = fields.iter().map(|(name, _)| name);
    quote!((#(#names,)*))
}

fn tuple_value(fields: &[(syn::Ident, Type)]) -> TokenStream {
    let names = fields.iter().map(|(name, _)| name);
    quote!((#(self.#names,)*))
}

#[cfg(test)]
mod tests {
    use proc_macro2::TokenStream;
    use proptest::prelude::*;
    use quote::{format_ident, quote};

    use super::object;

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(256))]

        // The source grammar and expansion agree on normalized metadata while the
        // emitted bridge remains independent of the SDK dependency's local name.
        #[test]
        fn source_metadata_has_one_crate_local_expansion(
            root_first in any::<bool>(),
            semantic_mutation in any::<bool>(),
            seed in any::<u16>(),
        ) {
            let ident = format_ident!("Fixture{seed}");
            let rename = if semantic_mutation {
                format!("fixture_{seed}_changed")
            } else {
                format!("fixture_{seed}")
            };
            let args: TokenStream = if root_first {
                quote!(root, rename = #rename)
            } else {
                quote!(rename = #rename, root)
            };
            let item = syn::parse2(quote! {
                pub(crate) struct #ident {
                    #[dagger(field)]
                    value: String,
                }
            }).expect("generated struct is valid Rust");
            let expansion = object(args, item).expect("generated metadata is valid").to_string();
            let expected_fingerprint = reference_object_fingerprint(&ident.to_string(), &rename);
            let expected_witness = format!("< {expected_fingerprint}u128 >");

            let reference_item = syn::parse2(quote! {
                pub(crate) struct #ident {
                    #[dagger(field)]
                    value: String,
                }
            }).expect("reference struct is valid Rust");
            let reference = object(
                quote!(rename = #rename, root),
                reference_item,
            ).expect("reference metadata is valid").to_string();
            prop_assert_eq!(&expansion, &reference);
            prop_assert!(expansion.contains("crate :: dagger_generated :: __private"));
            prop_assert!(!expansion.contains("dagger_sdk"));
            prop_assert!(expansion.contains(&expected_witness));

            let baseline_item = syn::parse2(quote! {
                pub(crate) struct #ident {
                    #[dagger(field)]
                    value: String,
                }
            }).expect("baseline struct is valid Rust");
            let baseline = object(
                quote!(root, rename = #rename),
                baseline_item,
            ).expect("baseline metadata is valid").to_string();
            if semantic_mutation {
                let unchanged_item = syn::parse2(quote! {
                    pub(crate) struct #ident {
                        #[dagger(field)]
                        value: String,
                    }
                }).expect("unchanged struct is valid Rust");
                let unchanged = object(
                    quote!(root, rename = "fixture"),
                    unchanged_item,
                ).expect("unchanged metadata is valid").to_string();
                prop_assert_ne!(baseline, unchanged);
            }

            let malformed_item = syn::parse2(quote! {
                pub struct #ident { value: String }
            }).expect("malformed-metadata struct remains valid Rust");
            prop_assert!(object(quote!(root, root), malformed_item).is_err());
        }
    }

    fn reference_object_fingerprint(type_name: &str, rename: &str) -> u128 {
        const OFFSET: u128 = 0x6c62_272e_07bb_0142_62b8_2175_6295_c58d;
        const PRIME: u128 = 0x0000_0000_0100_0000_0000_0000_0000_013b;

        let metadata = format!("6:rename=\"{rename}\"|4:root=true");
        let parts = [
            "object".to_owned(),
            type_name.to_owned(),
            metadata,
            "field".to_owned(),
            "value".to_owned(),
            "String".to_owned(),
            "5:field=true".to_owned(),
        ];
        let mut value = OFFSET;
        for part in parts {
            let length = u64::try_from(part.len()).unwrap_or(u64::MAX);
            for byte in length.to_le_bytes().into_iter().chain(part.bytes()) {
                value ^= u128::from(byte);
                value = value.wrapping_mul(PRIME);
            }
        }
        value
    }
}
