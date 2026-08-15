//! Exact checked-target schema compiler regressions.

use dagger_codegen::diagnostic::DiagnosticCode;
use dagger_codegen::target::CodegenTarget;
use dagger_codegen::{CoreProjectionRequest, project_core, render_core};

const TARGET: &[u8] = include_bytes!("../../../codegen/target.json");
const SCHEMA: &[u8] = include_bytes!("../../../codegen/schema.json");

#[test]
fn checked_target_compiles_complete_coordinate_inventory() {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must be valid");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked schema must compile");
    let inventory = plan.schema().inventory();

    assert_eq!(inventory.query_roots, 1);
    assert_eq!(inventory.named_types, 111);
    assert_eq!(inventory.scalars, 8);
    assert_eq!(inventory.objects, 78);
    assert_eq!(inventory.interfaces, 3);
    assert_eq!(inventory.enums, 18);
    assert_eq!(inventory.input_objects, 4);
    assert_eq!(inventory.fields, 720);
    assert_eq!(inventory.arguments, 611);
    assert_eq!(inventory.input_fields, 14);
    assert_eq!(inventory.enum_values, 84);
    assert_eq!(inventory.interface_edges, 91);
    assert_eq!(inventory.directives, 12);
    assert_eq!(inventory.directive_arguments, 14);
}

#[test]
fn checked_target_rendering_is_byte_stable() {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must be valid");
    let plan = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: SCHEMA,
    })
    .expect("checked schema must compile");

    assert_eq!(
        render_core(&plan).expect("first render must succeed"),
        render_core(&plan).expect("second render must succeed")
    );
}

#[test]
fn schema_byte_drift_is_rejected_before_projection() {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must be valid");
    let mut changed = SCHEMA.to_vec();
    changed[0] ^= 1;
    let diagnostics = project_core(CoreProjectionRequest {
        target: &target,
        schema_json: &changed,
    })
    .expect_err("changed schema bytes must be rejected");

    assert!(diagnostics.contains(DiagnosticCode::SchemaDigestMismatch));
}

#[test]
fn target_serialization_preserves_exact_identity() {
    let target = CodegenTarget::decode_exact(TARGET).expect("checked target must be valid");
    let encoded = serde_json::to_vec(&target).expect("validated target must serialize");

    assert_eq!(
        CodegenTarget::decode_exact(&encoded).expect("serialized target must decode"),
        target
    );
}
