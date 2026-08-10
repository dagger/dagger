//! Shared operation-selector boundary between the wire model and pure compiler.

use dagger_sdk_engine::OperationKind;

#[test]
fn wire_model_and_pure_compiler_share_one_closed_selector_type() {
    for operation in [
        OperationKind::GenerateLibrary,
        OperationKind::GenerateModule,
        OperationKind::GenerateClient,
        OperationKind::GenerateEntrypoint,
    ] {
        let compiler_operation: dagger_codegen::engine::OperationKind = operation;
        let encoded = serde_json::to_string(&compiler_operation)
            .expect("closed operation selector must encode");
        let decoded: OperationKind =
            serde_json::from_str(&encoded).expect("closed operation selector must decode");
        assert_eq!(decoded, operation);
    }
}
