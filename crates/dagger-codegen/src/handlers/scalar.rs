use genco::Tokens;
use graphql_introspection_query::introspection_response::FullType;

use crate::predicates::is_custom_scalar_type;

use super::Handler;

pub struct Scalar;

impl Handler for Scalar {
    fn predicate(&self, t: &FullType) -> bool {
        is_custom_scalar_type(t)
    }
}
