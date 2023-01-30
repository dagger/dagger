pub mod enumeration;
mod fields;
pub mod input;
mod input_field;
pub mod object;
pub mod scalar;
mod type_ref;
mod utility;

use std::sync::Arc;

use dagger_core::introspection::FullType;
use genco::prelude::rust::Tokens;
use genco::prelude::*;

pub trait Handler {
    fn predicate(&self, _t: &FullType) -> bool {
        false
    }

    fn render(&self, t: &FullType) -> eyre::Result<rust::Tokens> {
        let tstruct = self.render_struct(t)?;
        let timpl = self.render_impl(t)?;
        let mut out = rust::Tokens::new();
        out.append(tstruct);
        out.push();
        out.append(timpl);
        out.push();
        Ok(out)
    }

    fn render_struct(&self, t: &FullType) -> eyre::Result<Tokens> {
        let name = t.name.as_ref().ok_or(eyre::anyhow!("name not found"))?;

        Ok(quote! {
            pub struct $name {} {
                // TODO: Add fields
            }
        })
    }

    fn render_impl(&self, t: &FullType) -> eyre::Result<Tokens> {
        let name = t.name.as_ref().ok_or(eyre::anyhow!("name not found"))?;

        Ok(quote! {
            impl $name {} {
                // TODO: Add fields
            }
        })
    }
}

pub type DynHandler = Arc<dyn Handler + Send + Sync>;
pub type Handlers = Vec<DynHandler>;

#[cfg(test)]
mod tests {
    use dagger_core::introspection::FullType;
    use pretty_assertions::assert_eq;

    use super::Handler;

    struct DefaultHandler;
    impl Handler for DefaultHandler {}

    #[test]
    fn render_returns_expected() {
        let handler = DefaultHandler {};
        let t = FullType {
            kind: None,
            name: Some("SomeName".into()),
            description: None,
            fields: None,
            input_fields: None,
            interfaces: None,
            enum_values: None,
            possible_types: None,
        };

        let res = handler.render(&t).unwrap();

        assert_eq!(
            res.to_string().unwrap(),
            "pub struct SomeName {} {

}
impl SomeName {} {

}"
            .to_string()
        );
    }
}
