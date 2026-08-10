//! Checked renderer for the private module protocol probe.

use crate::diagnostic::DiagnosticSet;

use super::model::{EntrypointInput, ModuleProjectionInput};
use super::render::checked_source;

pub(crate) fn render_source(
    module: &ModuleProjectionInput,
    input: &EntrypointInput,
) -> Result<Vec<u8>, DiagnosticSet> {
    let module_identity = serde_json::to_string(&serde_json::json!({
        "name": module.name,
        "original_name": module.original_name,
        "source_digest": module.source_digest,
        "source_subpath": module.source_subpath.as_str(),
    }))
    .map_err(|_| {
        DiagnosticSet::one(super::model::operation_diagnostic(
            crate::diagnostic::DiagnosticCode::GeneratedProvenanceInvalid,
            "entrypoint.module",
            "module identity could not be encoded into entrypoint provenance",
        ))
    })?;
    let object_name = rust_string(input.object_name())?;
    let function_name = rust_string(input.function_name())?;
    let return_scalar = rust_string(input.return_scalar())?;
    let result_json = rust_string(input.result_json())?;
    let source = format!(
        r#"//! Private fixed protocol probe for Rust SDK engine integration.
// @module {module_identity}

const PROBE_OBJECT: &str = {object_name};
const PROBE_FUNCTION: &str = {function_name};
const PROBE_RETURN_SCALAR: &str = {return_scalar};
const PROBE_RESULT_JSON: &str = {result_json};

fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {{
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    runtime.block_on(run())
}}

async fn run() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {{
    let client = dagger_sdk::connect().await?;
    let operation = handle_call(&client).await;
    let close = client.close().await;
    match operation {{
        Err(primary) => {{
            // The operation failure remains primary; close still runs to completion.
            let _secondary_close_failure = close.err();
            Err(primary)
        }}
        Ok(()) => {{
            close?;
            Ok(())
        }}
    }}
}}

async fn handle_call(
    client: &dagger_sdk::Client,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {{
    let query = client.query();
    let call = query.current_function_call();
    let name = call.name().await?;
    match name.as_str() {{
        "" => {{
            let result = query.type_def().with_scalar(PROBE_RETURN_SCALAR);
            let function = query.function(PROBE_FUNCTION, result);
            let object = query
                .type_def()
                .with_object(PROBE_OBJECT)
                .with_function(function);
            query.module().with_object(object).serve().await?;
            Ok(())
        }}
        PROBE_FUNCTION => {{
            call.return_value(dagger_sdk::Json::new(PROBE_RESULT_JSON))
                .await?;
            Ok(())
        }}
        _ => Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "unsupported private protocol probe function",
        )
        .into()),
    }}
}}
"#
    );
    checked_source("src/bin/dagger-module.rs", source)
}

fn rust_string(value: &str) -> Result<String, DiagnosticSet> {
    serde_json::to_string(value).map_err(|_| {
        DiagnosticSet::one(super::model::operation_diagnostic(
            crate::diagnostic::DiagnosticCode::GeneratedProvenanceInvalid,
            "entrypoint",
            "entrypoint constant could not be encoded",
        ))
    })
}
