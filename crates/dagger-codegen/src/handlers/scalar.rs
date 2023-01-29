use genco::{prelude::rust, quote};
use graphql_introspection_query::introspection_response::FullType;

use crate::predicates::is_custom_scalar_type;

use super::{utility::render_description, Handler};

pub struct Scalar;

impl Handler for Scalar {
    fn predicate(&self, t: &FullType) -> bool {
        is_custom_scalar_type(t)
    }

    fn render(&self, t: &FullType) -> eyre::Result<rust::Tokens> {
        let mut out = rust::Tokens::new();

        let description =
            render_description(t).ok_or(eyre::anyhow!("could not find description"))?;
        let tstruct = self.render_struct(t)?;

        out.append(description);
        out.push();
        out.append(tstruct);

        Ok(out)
    }

    fn render_struct(&self, t: &FullType) -> eyre::Result<genco::prelude::rust::Tokens> {
        let name = t.name.as_ref().ok_or(eyre::anyhow!("name not found"))?;

        Ok(quote! {
            pub struct $name (Scalar);
        })
    }

    fn render_impl(&self, t: &FullType) -> eyre::Result<genco::prelude::rust::Tokens> {
        todo!()
    }
}
