//! Module registration — builds TypeDefs and registers them with the Dagger engine.

use super::{DaggerModule, TypeKind};
use crate::gen::{Query, TypeDef, TypeDefKind};

/// Register the module's types with the Dagger engine.
/// Returns the serialized ModuleID string.
pub async fn register(client: &Query, module: &impl DaggerModule) -> eyre::Result<String> {
    let mut mod_def = client.module().with_description(module.description());

    // Build the main object type with all its functions
    let mut obj_typedef = client.type_def().with_object(module.name());

    for func in module.functions() {
        // Build return type
        let ret_type = build_typedef(client, &func.return_type);

        // Build the function definition
        let mut fn_def = client.function(&func.name, ret_type);

        if !func.description.is_empty() {
            fn_def = fn_def.with_description(&func.description);
        }

        // Add arguments
        for arg in &func.args {
            let arg_type = build_typedef(client, &arg.type_kind);
            let arg_type = if arg.optional {
                arg_type.with_optional(true)
            } else {
                arg_type
            };

            fn_def = fn_def.with_arg(&arg.name, arg_type);
        }

        obj_typedef = obj_typedef.with_function(fn_def);
    }

    mod_def = mod_def.with_object(obj_typedef);

    // Finalize registration by getting the module ID
    let id = mod_def.id().await?;
    Ok(id.0)
}

/// Build a Dagger TypeDef from our TypeKind description.
fn build_typedef(client: &Query, kind: &TypeKind) -> TypeDef {
    match kind {
        TypeKind::String => client.type_def().with_kind(TypeDefKind::StringKind),
        TypeKind::Integer => client.type_def().with_kind(TypeDefKind::IntegerKind),
        TypeKind::Float => client.type_def().with_kind(TypeDefKind::FloatKind),
        TypeKind::Boolean => client.type_def().with_kind(TypeDefKind::BooleanKind),
        TypeKind::Void => client.type_def().with_kind(TypeDefKind::VoidKind),
        TypeKind::Object(name) => client.type_def().with_object(name.as_str()),
        TypeKind::List(inner) => {
            let element_type = build_typedef(client, inner);
            client.type_def().with_list_of(element_type)
        }
        TypeKind::Optional(inner) => {
            let inner_type = build_typedef(client, inner);
            inner_type.with_optional(true)
        }
    }
}
