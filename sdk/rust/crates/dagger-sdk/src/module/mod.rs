//! Manual dispatcher for Dagger modules written in Rust.
//!
//! The Rust SDK does not yet ship procedural macros for registering module
//! functions. Until it does, modules wire themselves up by hand: declare each
//! function in a [`FunctionDef`] slice, register the object with
//! [`register_module`] when the engine asks the module to introspect itself
//! (the `parent_name == module && fn_name == ""` case), and dispatch on the
//! function name in the body of the [`serve`] handler.

use std::collections::HashMap;
use std::future::Future;

use serde_json::Value;

use crate::client::{connect, DaggerConn};
use crate::errors::ConnectError;
use crate::gen::{Json, TypeDef, TypeDefKind};

/// A single function exported by a module object.
///
/// Argument and return type names use the same wire-format strings as the
/// engine's `TypeDefKind` enum: `"String"`, `"Integer"`, `"Boolean"`,
/// `"Float"`, `"Void"`. Object kinds are not yet supported by this minimal
/// dispatcher.
#[derive(Clone, Copy, Debug)]
pub struct FunctionDef {
    pub name: &'static str,
    pub return_type_name: &'static str,
    pub args: &'static [(&'static str, &'static str)],
}

/// Context handed to the user-supplied dispatch closure.
pub struct FunctionCallContext {
    pub parent_name: String,
    pub fn_name: String,
    pub parent_json: Value,
    pub args: HashMap<String, Value>,
    pub client: DaggerConn,
}

/// Run the module: connect to the engine, read the current FunctionCall,
/// invoke the user handler, and return the JSON result back to the engine.
pub async fn serve<F, Fut>(handler: F) -> Result<(), ConnectError>
where
    F: FnOnce(FunctionCallContext) -> Fut + 'static,
    Fut: Future<Output = eyre::Result<Value>> + 'static,
{
    connect(|client| async move {
        let fc = client.current_function_call();

        let parent_name = fc.parent_name().await?;
        let fn_name = fc.name().await?;
        let parent_raw = fc.parent().await?;
        let parent_json: Value = if parent_raw.0.is_empty() {
            Value::Null
        } else {
            serde_json::from_str(&parent_raw.0)?
        };

        let mut args = HashMap::new();
        for arg in fc.input_args() {
            let name = arg.name().await?;
            let raw = arg.value().await?;
            let value: Value = if raw.0.is_empty() {
                Value::Null
            } else {
                serde_json::from_str(&raw.0)?
            };
            args.insert(name, value);
        }

        let ctx = FunctionCallContext {
            parent_name,
            fn_name,
            parent_json,
            args,
            client: client.clone(),
        };

        let result = handler(ctx).await?;
        fc.return_value(Json(result.to_string())).await?;
        Ok(())
    })
    .await
}

/// Register a single object (named `module_name`) and its functions with
/// the engine. Use from the `("<module>", "")` arm of your dispatch.
pub async fn register_module(
    client: &DaggerConn,
    module_name: &str,
    fns: &[FunctionDef],
) -> eyre::Result<Value> {
    let mut object = client.type_def().with_object(module_name);

    for f in fns {
        let return_type = type_def_for(client, f.return_type_name)?;
        let mut function = client.function(f.name, return_type);
        for (arg_name, arg_type_name) in f.args {
            let arg_type = type_def_for(client, arg_type_name)?;
            function = function.with_arg(*arg_name, arg_type);
        }
        object = object.with_function(function);
    }

    let module_id = client.module().with_object(object).id().await?;
    Ok(serde_json::json!(module_id.0))
}

fn type_def_for(client: &DaggerConn, name: &str) -> eyre::Result<TypeDef> {
    let kind = match name {
        "String" => TypeDefKind::String,
        "Integer" | "Int" => TypeDefKind::Integer,
        "Float" => TypeDefKind::Float,
        "Boolean" | "Bool" => TypeDefKind::Boolean,
        "Void" => TypeDefKind::Void,
        other => {
            return Err(eyre::eyre!(
                "unsupported type for manual dispatch: {other} (only scalar kinds are supported)"
            ))
        }
    };
    Ok(client.type_def().with_kind(kind))
}
