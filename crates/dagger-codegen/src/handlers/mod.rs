use std::sync::Arc;

use genco::prelude::rust::Tokens;
use genco::prelude::*;
use graphql_introspection_query::introspection_response::FullType;

pub trait Handler {
    fn predicate(&self, t: &FullType) -> bool {
        false
    }

    fn render(&self, t: &FullType) -> eyre::Result<String> {
        let mut s = String::new();

        s.push_str("\n");
        s.push_str(self.render_struct(t)?.to_string()?.as_str());
        s.push_str("\n");
        s.push_str(self.render_impl(t)?.to_string()?.as_str());
        s.push_str("\n");

        Ok(s)
    }

    fn render_struct(&self, t: &FullType) -> eyre::Result<Tokens> {
        let name = t.name.as_ref().ok_or(eyre::anyhow!("name not found"))?;

        Ok(quote! {
            pub $name {} {
                // TODO: Add fields
            }
        })
    }

    fn render_impl(&self, t: &FullType) -> eyre::Result<Tokens> {
        let name = t.name.as_ref().ok_or(eyre::anyhow!("name not found"))?;

        Ok(quote! {
            pub $name {} {
                // TODO: Add fields
            }
        })
    }
}

pub type DynHandler = Arc<dyn Handler + Send + Sync>;
pub type Handlers = Vec<DynHandler>;

#[cfg(test)]
mod tests {
    use graphql_introspection_query::introspection_response::FullType;

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
            res,
            "
pub SomeName {} { }
pub SomeName {} { }
"
            .to_string()
        );
    }
}
