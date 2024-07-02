use genco::{lang::rust, quote};

pub fn render_heading() -> rust::Tokens {
    quote! {
       #![allow(clippy::needless_lifetimes)]
    }
}
