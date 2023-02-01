use dagger_core::introspection::{FullType, FullTypeInputFields, TypeRef, __TypeKind};

use crate::models::Scalars;

pub fn is_scalar_type(t: &FullType) -> bool {
    if let Some(__TypeKind::SCALAR) = t.kind {
        return true;
    }
    false
}

pub fn is_scalar_type_ref(t: &TypeRef) -> bool {
    if let Some(__TypeKind::SCALAR) = t.kind {
        return true;
    }
    false
}

pub fn is_enum_type(t: &FullType) -> bool {
    if let Some(__TypeKind::ENUM) = t.kind {
        return true;
    }
    false
}

pub fn is_input_object_type(t: &FullType) -> bool {
    if let Some(__TypeKind::INPUT_OBJECT) = t.kind {
        return true;
    }
    false
}

pub fn is_required_type(t: &FullTypeInputFields) -> bool {
    match t.input_value.type_.kind {
        Some(__TypeKind::NON_NULL) => return true,
        Some(_) => return false,
        _ => return false,
    }
}

pub fn is_required_type_ref(t: &TypeRef) -> bool {
    match t.kind {
        Some(__TypeKind::NON_NULL) => return true,
        Some(_) => return false,
        _ => return false,
    }
}

pub fn is_list_type(t: &TypeRef) -> bool {
    if let Some(__TypeKind::LIST) = t.kind {
        return true;
    }
    false
}

pub fn is_object_type(t: &FullType) -> bool {
    if let Some(__TypeKind::OBJECT) = t.kind {
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

pub fn is_custom_scalar_type_ref(t: &TypeRef) -> bool {
    if is_scalar_type_ref(t) {
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
