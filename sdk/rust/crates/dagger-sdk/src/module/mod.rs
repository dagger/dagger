//! Dagger module support.
//!
//! This module provides the runtime infrastructure for building Dagger modules in Rust.
//! It handles module registration (exposing type definitions to the engine)
//! and function invocation (executing module functions when called).
//!
//! # Usage
//!
//! ```ignore
//! use dagger_sdk::*;
//!
//! #[derive(Default)]
//! pub struct MyModule;
//!
//! #[dagger_module]
//! impl MyModule {
//!     #[dagger_function]
//!     fn hello(&self) -> String {
//!         "Hello!".to_string()
//!     }
//! }
//!
//! #[tokio::main]
//! async fn main() -> eyre::Result<()> {
//!     dagger_sdk::run(MyModule).await
//! }
//! ```

pub mod invoke;
pub mod register;

use std::collections::HashMap;
use std::sync::OnceLock;

use crate::gen::{Json, Query};

pub use dagger_sdk_derive::{dagger_function, dagger_module};

static DAG_CLIENT: OnceLock<Query> = OnceLock::new();

/// Returns a reference to the global Dagger client.
/// Available inside `#[dagger_function]` methods.
pub fn dag() -> &'static Query {
    DAG_CLIENT.get().expect(
        "dagger client not initialized — this should only be called inside a dagger function",
    )
}

/// Store the client for user code to access via `dag()`.
fn set_dag(client: Query) {
    let _ = DAG_CLIENT.set(client);
}

/// Re-exports of internal types needed by generated code (dagger_gen.rs).
pub mod gen_deps {
    pub use crate::core::cli_session::DaggerSessionProc;
    pub use crate::core::graphql_client::DynGraphQLClient;
    pub use crate::errors::DaggerError;
    pub use crate::id::IntoID;
    pub use crate::querybuilder::Selection;
}

/// Convenient re-exports for module authors.
pub mod prelude {
    pub use super::{
        dag, dagger_function, dagger_module, DaggerModule, FunctionArg, ModuleFunction, TypeKind,
    };
    pub use crate::gen::*;
    pub use serde_json;
}

/// The type kind for function arguments and return types.
#[derive(Debug, Clone)]
pub enum TypeKind {
    String,
    Integer,
    Float,
    Boolean,
    /// A Dagger API object type (Container, Directory, File, etc.)
    Object(std::string::String),
    /// A list of the given type
    List(Box<TypeKind>),
    /// An optional value
    Optional(Box<TypeKind>),
    /// Void (no return)
    Void,
}

/// Describes a function argument.
#[derive(Debug, Clone)]
pub struct FunctionArg {
    pub name: std::string::String,
    pub description: std::string::String,
    pub type_kind: TypeKind,
    pub optional: bool,
    pub default_value: Option<serde_json::Value>,
}

/// Describes a function exposed by a module.
pub struct ModuleFunction {
    pub name: std::string::String,
    pub description: std::string::String,
    pub args: Vec<FunctionArg>,
    pub return_type: TypeKind,
    /// The actual function to call. Takes (client, parent_json, args_map) and returns JSON result.
    pub handler: Box<
        dyn Fn(
                Query,
                serde_json::Value,
                HashMap<std::string::String, serde_json::Value>,
            ) -> std::pin::Pin<
                Box<dyn std::future::Future<Output = eyre::Result<serde_json::Value>> + Send>,
            > + Send
            + Sync,
    >,
}

/// Trait that user modules implement to describe their types and functions.
pub trait DaggerModule: Send + Sync + 'static {
    /// The name of the main object type.
    fn name(&self) -> &str;

    /// Description of the module.
    fn description(&self) -> &str {
        ""
    }

    /// Returns the functions exposed by this module.
    fn functions(&self) -> Vec<ModuleFunction>;
}

/// Represents a single function call argument from the engine.
#[derive(serde::Deserialize, Debug)]
struct InputArg {
    name: std::string::String,
    value: std::string::String,
}

/// Main entry point for a Dagger module.
pub async fn run(module: impl DaggerModule) -> eyre::Result<()> {
    crate::connect(move |client| async move {
        set_dag(client.clone());
        let fn_call = client.current_function_call();

        // Try to get parent_name. It may be null/missing for registration.
        let parent_name = match fn_call.parent_name().await {
            Ok(name) => name,
            Err(_) => std::string::String::new(),
        };

        if parent_name.is_empty() {
            // Registration mode: tell the engine about our types
            let mod_id = register::register(&client, &module).await?;

            // Return the module ID as a JSON string.
            // Ignore the Void deserialization error.
            let mod_id_json = serde_json::to_string(&mod_id)?;
            let _ = fn_call.return_value(Json(mod_id_json)).await;
        } else {
            // Invocation mode: execute the requested function
            let name = fn_call.name().await?;
            let parent_json_str = fn_call.parent().await.unwrap_or(Json("{}".into()));

            // Fetch all input args in one GraphQL query
            let query_str =
                "query { currentFunctionCall { inputArgs { name value } } }".to_string();
            let resp: Option<serde_json::Value> = client
                .graphql_client
                .query(&query_str)
                .await
                .map_err(|e| eyre::eyre!("failed to query input args: {}", e))?;

            let args_map = if let Some(resp) = resp {
                let input_args: Vec<InputArg> = serde_json::from_value(
                    resp.pointer("/currentFunctionCall/inputArgs")
                        .cloned()
                        .unwrap_or(serde_json::Value::Array(vec![])),
                )?;

                let mut map = HashMap::new();
                for arg in input_args {
                    let value: serde_json::Value =
                        serde_json::from_str(&arg.value).unwrap_or(serde_json::Value::Null);
                    map.insert(arg.name, value);
                }
                map
            } else {
                HashMap::new()
            };

            let parent_value: serde_json::Value = serde_json::from_str(&parent_json_str.0)
                .unwrap_or(serde_json::Value::Object(serde_json::Map::new()));

            let result = invoke::invoke(
                &client,
                &module,
                &parent_name,
                &name,
                parent_value,
                args_map,
            )
            .await?;

            let result_json = serde_json::to_string(&result)?;
            let _ = fn_call.return_value(Json(result_json)).await;
        }

        Ok(())
    })
    .await?;

    Ok(())
}
