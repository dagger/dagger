//! Compatibility and order invariants for complete engine-visible schemas.

mod support;

use dagger_codegen::diagnostic::DiagnosticCode;
use dagger_codegen::engine::{
    OperationKind, OperationProjectionRequest, PublishedSdkDependency, RelativeOperationPath,
    project_operation,
};
use dagger_codegen::target::CodegenTarget;
use proptest::prelude::*;

use support::{TARGET_BYTES, VisibleSchemaCase, pure_config, visible_schema};

fn case(discriminant: u8) -> VisibleSchemaCase {
    match discriminant % 7 {
        0 => VisibleSchemaCase::ExactCore,
        1 => VisibleSchemaCase::CompatibleExtension,
        2 => VisibleSchemaCase::EngineModuleExtension,
        3 => VisibleSchemaCase::CoreMutation,
        4 => VisibleSchemaCase::CoreOmission,
        5 => VisibleSchemaCase::UnresolvedReference,
        _ => VisibleSchemaCase::RustNameCollision,
    }
}

fn project(
    bytes: &[u8],
) -> Result<dagger_codegen::engine::OperationPlan, dagger_codegen::diagnostic::DiagnosticSet> {
    let target = CodegenTarget::decode_exact(TARGET_BYTES).expect("checked target must decode");
    let output = RelativeOperationPath::parse("generated").expect("fixture path must parse");
    let dependency = PublishedSdkDependency::Registry {
        registry: "crates-io".to_owned(),
        exact_version: "1.0.0-beta.10".to_owned(),
    };
    project_operation(OperationProjectionRequest {
        target: &target,
        operation: OperationKind::GenerateLibrary,
        visible_schema_json: bytes,
        module: None,
        output: &output,
        sdk_dependency: &dependency,
        entrypoint: None,
    })
}

proptest! {
    #![proptest_config(pure_config())]

    // Compatible extension semantics and their diagnostics cannot depend on source-array order.
    #[test]
    fn property_10_visible_schema_compatible_order_invariant(
        discriminant in any::<u8>(),
        permutation in any::<u16>(),
    ) {
        let case = case(discriminant);
        let baseline = project(&visible_schema(case, 0));
        let permuted = project(&visible_schema(case, permutation));
        match case {
            VisibleSchemaCase::ExactCore
            | VisibleSchemaCase::CompatibleExtension
            | VisibleSchemaCase::EngineModuleExtension => {
                prop_assert_eq!(
                    permuted.expect("compatible schema permutation must project"),
                    baseline.expect("compatible schema baseline must project"),
                );
            }
            VisibleSchemaCase::CoreMutation => {
                let baseline = baseline.expect_err("core mutation must fail");
                let permuted = permuted.expect_err("permuted core mutation must fail");
                prop_assert!(baseline.contains(DiagnosticCode::SchemaCoreCoordinateIncompatible));
                prop_assert_eq!(permuted, baseline);
            }
            VisibleSchemaCase::CoreOmission => {
                let baseline = baseline.expect_err("core omission must fail");
                let permuted = permuted.expect_err("permuted core omission must fail");
                prop_assert!(baseline.contains(DiagnosticCode::SchemaCoreCoordinateMissing));
                prop_assert_eq!(permuted, baseline);
            }
            VisibleSchemaCase::UnresolvedReference => {
                let baseline = baseline.expect_err("unresolved reference must fail");
                let permuted = permuted.expect_err("permuted unresolved reference must fail");
                prop_assert!(baseline.contains(DiagnosticCode::SchemaReferenceInvalid));
                prop_assert_eq!(permuted, baseline);
            }
            VisibleSchemaCase::RustNameCollision => {
                let baseline = baseline.expect_err("Rust naming collision must fail");
                let permuted = permuted.expect_err("permuted Rust naming collision must fail");
                prop_assert!(baseline.contains(DiagnosticCode::RustNameCollision));
                prop_assert_eq!(permuted, baseline);
            }
        }
    }
}
