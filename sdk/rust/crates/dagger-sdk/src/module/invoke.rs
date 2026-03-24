//! Function invocation — dispatches calls to the appropriate module function.

use std::collections::HashMap;

use super::DaggerModule;
use crate::gen::Query;

/// Invoke a function on the module.
pub async fn invoke(
    client: &Query,
    module: &impl DaggerModule,
    _parent_name: &str,
    name: &str,
    parent_json: serde_json::Value,
    args: HashMap<String, serde_json::Value>,
) -> eyre::Result<serde_json::Value> {
    let functions = module.functions();

    let func = functions
        .into_iter()
        .find(|f| f.name == name)
        .ok_or_else(|| eyre::eyre!("function '{}' not found", name))?;

    (func.handler)(client.clone(), parent_json, args).await
}
