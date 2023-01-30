use dagger_core::introspection::FullType;
use genco::{prelude::rust, quote};

use crate::predicates::is_enum_type;

use super::{utility::render_description, Handler};

pub struct Enumeration;

impl Handler for Enumeration {
    fn predicate(&self, t: &FullType) -> bool {
        is_enum_type(t)
    }

    fn render(&self, t: &FullType) -> eyre::Result<rust::Tokens> {
        let name = t
            .name
            .as_ref()
            .ok_or(eyre::anyhow!("could not get name from type"))?;

        let description =
            render_description(t).ok_or(eyre::anyhow!("could not find description"))?;

        let out = quote! {
            $description
            pub enum $name {
                // TODO: Add individual items
            }
        };

        Ok(out)
    }
}
