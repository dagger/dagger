// Auto-generated entrypoint for the {{.ModuleName}} Dagger module.
//
// This Rust SDK currently uses *manual* dispatch: each function the module
// exposes is registered in `register_module` below and matched on by name in
// the `serve` handler. Add new functions by:
//   1. Appending a `FunctionDef` to the slice in the `("{{.ModuleName}}", "")` arm.
//   2. Adding a new match arm `("{{.ModuleName}}", "your_fn")` returning a JSON value.

use dagger_sdk::module::{register_module, serve, FunctionDef};
use serde_json::json;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    serve(|ctx| async move {
        match (ctx.parent_name.as_str(), ctx.fn_name.as_str()) {
            ("{{.ModuleName}}", "") => {
                register_module(
                    &ctx.client,
                    "{{.ModuleName}}",
                    &[FunctionDef {
                        name: "hello",
                        return_type_name: "String",
                        args: &[],
                    }],
                )
                .await
            }
            ("{{.ModuleName}}", "hello") => Ok(json!("Hello from Rust!")),
            (parent, fn_name) => Err(eyre::eyre!(
                "unknown function: {parent}.{fn_name}"
            )),
        }
    })
    .await
}
