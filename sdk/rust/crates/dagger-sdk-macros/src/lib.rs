//! Exact-version procedural authoring attributes re-exported by `dagger-sdk`.
//!
//! Expansion is intentionally limited to typed crate-local bridges. Source discovery,
//! Dagger naming, schema projection, JSON codecs, dispatch, and engine work remain in
//! the pure compiler and generated runtime.

use proc_macro::TokenStream;
use syn::parse_macro_input;

mod attribute;
mod bridge;
mod fingerprint;

/// Exports a named struct as a Dagger object.
#[proc_macro_attribute]
pub fn object(args: TokenStream, item: TokenStream) -> TokenStream {
    let item = parse_macro_input!(item as syn::ItemStruct);
    bridge::object(args.into(), item)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Exports a trait as a Dagger interface.
#[proc_macro_attribute]
pub fn interface(args: TokenStream, item: TokenStream) -> TokenStream {
    let item = parse_macro_input!(item as syn::ItemTrait);
    bridge::interface(args.into(), item)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Exports a unit-variant enum as a Dagger enum.
#[proc_macro_attribute]
pub fn enum_type(args: TokenStream, item: TokenStream) -> TokenStream {
    let item = parse_macro_input!(item as syn::ItemEnum);
    bridge::enum_type(args.into(), item)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Exports a transparent one-field tuple struct as a Dagger scalar.
#[proc_macro_attribute]
pub fn scalar(args: TokenStream, item: TokenStream) -> TokenStream {
    let item = parse_macro_input!(item as syn::ItemStruct);
    bridge::scalar(args.into(), item)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}

/// Exports explicitly marked constructors and functions from an inherent impl.
#[proc_macro_attribute]
pub fn methods(args: TokenStream, item: TokenStream) -> TokenStream {
    let item = parse_macro_input!(item as syn::ItemImpl);
    bridge::methods(args.into(), item)
        .unwrap_or_else(syn::Error::into_compile_error)
        .into()
}
