use dagger_core::introspection::TypeRef;
use genco::prelude::rust;
use genco::prelude::*;

use crate::predicates::{
    is_custom_scalar_type_ref, is_list_type, is_required_type_ref, is_scalar_type_ref,
};

//fn optional(t: rust::Tokens) -> impl FormatInto<Rust> {
//    quote_fn! {"Option<$[const](t)>"}
//}
//
//fn required(t: rust::Tokens) -> impl FormatInto<Rust> {
//    quote_fn! {"$[const](t)"}
//}

pub fn render_type_ref(inner: &TypeRef) -> eyre::Result<rust::Tokens> {
    let extract_of_type = |t: &TypeRef| -> Option<TypeRef> {
        return t.clone().of_type.map(|t| *t);
    };

    let (optional, inner) = if !is_required_type_ref(inner) {
        (true, inner.clone())
    } else {
        (false, extract_of_type(inner).unwrap())
    };

    if is_list_type(&inner) {
        if let Some(inner_of_type) = extract_of_type(&inner) {
            let inner_field = render_type_ref(&inner_of_type)?;
            if optional {
                return Ok(quote! {
                    Option<Vec<$inner_field>>
                });
            }
            return Ok(quote! {
                Vec<$inner_field>
            });
        }
    }

    if is_custom_scalar_type_ref(&inner) {
        if let Some(inner_of_type) = extract_of_type(&inner) {
            let inner_field = render_type_ref(&inner_of_type)?;
            if optional {
                return Ok(quote! {
                    Option<$inner_field>
                });
            }
            return Ok(quote! {
                $inner_field
            });
        }
    }

    if is_scalar_type_ref(&inner) {
        let name = match inner.name.as_ref().map(|s| s.as_str()) {
            Some("ID") => "ID",
            Some("Int") => "Int",
            Some("String") => "String",
            Some("Float") => "Float",
            Some("Boolean") => "Boolean",
            Some("Date") => "Date",
            Some("DateTime") => "DateTime",
            Some("Time") => "Time",
            Some("Decimal") => "Decimal",
            Some(n) => n,
            _ => eyre::bail!("missing type in the end of chain"),
        };

        if optional {
            return Ok(quote! {
                Option<$name>
            });
        }

        return Ok(quote! {
            $name
        });
    }

    if let Some(inner_type) = inner.of_type.as_ref() {
        let inner_field = render_type_ref(&inner_type)?;
        if optional {
            return Ok(quote! {
                Option<$inner_field>
            });
        }

        return Ok(inner_field);
    }

    if let Some(name) = inner.name.as_ref() {
        if optional {
            return Ok(quote! {
                Option<$name>
            });
        }
        return Ok(quote! {
            $name
        });
    }

    eyre::bail!("could not determine type")
}
