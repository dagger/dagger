use graphql_introspection_query::introspection_response::{self, FullType};

use crate::models::Scalars;

pub fn is_scalar_type(t: &FullType) -> bool {
    if let Some(introspection_response::__TypeKind::SCALAR) = t.kind {
        return true;
    }
    false
}

pub fn is_enum_type(t: &FullType) -> bool {
    if let Some(introspection_response::__TypeKind::ENUM) = t.kind {
        return true;
    }
    false
}

pub fn is_custom_scalar_type(t: &FullType) -> bool {
    if is_scalar_type(t) {
        // TODO: Insert scalar
        let _ = match t.name.as_ref().map(|s| s.as_str()) {
            Some("ID") => Scalars::ID("ID".into()),
            Some("Int") => Scalars::Int(0),
            Some("String") => Scalars::String("ID".into()),
            Some("Float") => Scalars::Float(0.0),
            Some("Boolean") => Scalars::Boolean(false),
            Some("Date") => Scalars::Date("ID".into()),
            Some("DateTime") => Scalars::DateTime("ID".into()),
            Some("Time") => Scalars::Time("ID".into()),
            Some("Decimal") => Scalars::Decimal(0.0),
            Some(_) => return true,
            None => return false,
        };
    }
    false
}
