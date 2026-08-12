//! Runtime isolation, taxonomy, and rejection properties.

use std::collections::BTreeSet;

use dagger_sdk_engine::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use dagger_sdk_engine::protocol::{RuntimeCallInput, isolate_runtime_calls};
use dagger_sdk_engine::surface::{AdapterSurface, detect_adapter_surfaces};
use proptest::prelude::*;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // Placeholders and similarly named helpers cannot advertise an engine hook.
    #[test]
    fn property_15_engine_surfaces_report_only_implemented_hooks(
        present in prop::collection::vec(any::<bool>(), 4),
        placeholders in prop::collection::vec("[a-zA-Z][a-zA-Z0-9]{0,24}", 0..12),
    ) {
        let hooks = ["initModule", "codegen", "generateClient", "moduleRuntime"];
        let expected_surfaces = [
            AdapterSurface::ModuleInitializer,
            AdapterSurface::CodeGenerator,
            AdapterSurface::ClientGenerator,
            AdapterSurface::Runtime,
        ];
        let mut names = placeholders;
        let mut expected = BTreeSet::from([AdapterSurface::AsModule]);
        for (index, enabled) in present.into_iter().enumerate() {
            if enabled {
                names.push(hooks[index].to_owned());
                expected.insert(expected_surfaces[index]);
            }
        }
        let observed = detect_adapter_surfaces(names.iter().map(String::as_str));
        prop_assert_eq!(observed, expected);
    }

    // Every call observes one cloned filesystem and publishes only its own terminal result.
    #[test]
    fn property_22_concurrent_runtime_calls_remain_isolated(
        raw in prop::collection::vec((any::<u16>(), any::<bool>(), any::<bool>()), 1..16),
        rotation in any::<usize>(),
    ) {
        let calls = raw
            .into_iter()
            .enumerate()
            .map(|(index, (seed, cancelled, failed))| RuntimeCallInput {
                call_id: format!("call-{index}"),
                execution_metadata: format!("metadata-{seed}-{index}"),
                filesystem_writes: BTreeSet::from([format!("private/{index}/{seed}.txt")]),
                result_json: format!("\"result-{seed}-{index}\""),
                cancelled,
                failed,
            })
            .collect::<Vec<_>>();
        let mut scheduled = calls.clone();
        let len = scheduled.len();
        scheduled.rotate_left(rotation % len);

        let observed = isolate_runtime_calls(scheduled).expect("unique calls must isolate");
        prop_assert_eq!(observed.len(), calls.len());
        for call in calls {
            let result = observed.get(&call.call_id).expect("call observation must exist");
            prop_assert_eq!(&result.execution_metadata, &call.execution_metadata);
            prop_assert_eq!(&result.filesystem_writes, &call.filesystem_writes);
            prop_assert!(result.process_reaped);
            if call.cancelled || call.failed {
                prop_assert!(result.result_json.is_none());
            } else {
                prop_assert_eq!(result.result_json.as_deref(), Some(call.result_json.as_str()));
            }
        }
    }

    // Every private failure class has one stable code and credential-safe rendering.
    #[test]
    fn property_26_diagnostics_stable_typed_taxonomy(
        index in any::<usize>(),
        secret_bearing in any::<bool>(),
        coordinate in "[a-z0-9_/.-]{1,96}",
    ) {
        let codes = diagnostic_codes();
        let code = codes[index % codes.len()];
        let message = if secret_bearing {
            "dependency failed at https://user:password@example.invalid token=secret"
        } else {
            "operation failed at its validated boundary"
        };
        let diagnostic = EngineDiagnostic::new(code, Some(&coordinate), message);
        let encoded = serde_json::to_string(&code).expect("diagnostic code must serialize");
        prop_assert!(encoded.starts_with('"') && encoded.ends_with('"'));
        prop_assert!(encoded[1..encoded.len() - 1]
            .chars()
            .all(|character| character.is_ascii_uppercase() || character == '_' || character.is_ascii_digit()));
        let rendered = diagnostic.render();
        prop_assert!(!rendered.contains("password"));
        prop_assert!(!rendered.contains("token=secret"));
        prop_assert!(rendered.contains(&coordinate));
    }
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(128))]

    // Rejected or cancelled work cannot publish a result and cannot retain a child process.
    #[test]
    fn property_27_rejection_cancellation_no_partial_result(
        boundary in 0_u8..9,
        cancelled in any::<bool>(),
    ) {
        let call = RuntimeCallInput {
            call_id: "rejected-call".to_owned(),
            execution_metadata: format!("failure-boundary-{boundary}"),
            filesystem_writes: BTreeSet::from(["private/staged-output".to_owned()]),
            result_json: "\"must-not-publish\"".to_owned(),
            cancelled,
            failed: !cancelled,
        };
        let observed = isolate_runtime_calls([call])
            .expect("one rejected call must still have a terminal observation");
        let rejected = observed.get("rejected-call").expect("call must be observed");
        prop_assert!(rejected.result_json.is_none());
        prop_assert!(rejected.process_reaped);
    }
}

fn diagnostic_codes() -> &'static [EngineDiagnosticCode] {
    &[
        EngineDiagnosticCode::SdkManifestInvalid,
        EngineDiagnosticCode::PackagedAssetInvalid,
        EngineDiagnosticCode::SecurityAuditIncomplete,
        EngineDiagnosticCode::CargoManifestMissing,
        EngineDiagnosticCode::CargoManifestInvalid,
        EngineDiagnosticCode::CargoPackageMissing,
        EngineDiagnosticCode::CargoPackageAmbiguous,
        EngineDiagnosticCode::SdkDependencyConflict,
        EngineDiagnosticCode::SdkDependencyMutable,
        EngineDiagnosticCode::DependencyResolutionFailed,
        EngineDiagnosticCode::ToolchainUnsupported,
        EngineDiagnosticCode::ToolchainNonReproducible,
        EngineDiagnosticCode::OutputPathEscape,
        EngineDiagnosticCode::OutputSymlinkEscape,
        EngineDiagnosticCode::OwnershipConflict,
        EngineDiagnosticCode::OperationManifestStale,
        EngineDiagnosticCode::PostWorkRejected,
        EngineDiagnosticCode::GenerationNonConvergent,
        EngineDiagnosticCode::GenerationFailed,
        EngineDiagnosticCode::FormatFailed,
        EngineDiagnosticCode::PublicationFailed,
        EngineDiagnosticCode::RollbackFailed,
        EngineDiagnosticCode::OperationInputInvalid,
        EngineDiagnosticCode::OperationCancelled,
        EngineDiagnosticCode::GeneratedMissing,
        EngineDiagnosticCode::GeneratedStale,
        EngineDiagnosticCode::LockfileMissing,
        EngineDiagnosticCode::LockfileStale,
        EngineDiagnosticCode::RuntimeTargetInvalid,
        EngineDiagnosticCode::RuntimeBuildFailed,
        EngineDiagnosticCode::RuntimeSessionInvalid,
        EngineDiagnosticCode::RuntimeProtocolInvalid,
        EngineDiagnosticCode::RuntimeProtocolFailed,
        EngineDiagnosticCode::ResultReportFailed,
        EngineDiagnosticCode::DiagnosticRedactionFailed,
    ]
}
