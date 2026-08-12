//! Canonical standalone-client model and project-identity foundations.

mod support;

use dagger_sdk_engine::*;
use proptest::prelude::*;
use support::{fixed_model_corpus, model_corpus};

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn client_wire_models_are_canonical_and_credential_free(corpus in model_corpus()) {
        let request = canonical_bytes(&corpus.client_execution_request).unwrap();
        let manifest = canonical_bytes(&corpus.client_operation_manifest).unwrap();
        prop_assert_eq!(
            decode_canonical::<EngineExecutionRequest>(&request).unwrap(),
            corpus.client_execution_request
        );
        prop_assert_eq!(
            decode_canonical::<OperationManifest>(&manifest).unwrap(),
            corpus.client_operation_manifest
        );
        for forbidden in [
            "module_reference",
            "session_token",
            "authorization",
            "ambient_path",
            "filesystem_handle",
        ] {
            prop_assert!(!String::from_utf8_lossy(&request).contains(forbidden));
            prop_assert!(!String::from_utf8_lossy(&manifest).contains(forbidden));
        }
    }

    #[test]
    fn client_project_identity_is_deterministic(
        seed in any::<u16>(),
        separator in prop_oneof![Just("-"), Just("_"), Just(" "), Just(".")],
    ) {
        let root = RelativeOperationPath::parse(&format!("workspace/Acme{separator}{seed}"))
            .unwrap();
        let module = StableCoordinate::new(format!("Fallback{seed}")).unwrap();
        let request = ClientProjectIdentityRequest {
            existing_package_name: None,
            client_root: &root,
            bound_module_name: &module,
        };
        let first = select_client_project_identity(request).unwrap();
        let second = select_client_project_identity(request).unwrap();
        prop_assert_eq!(&first, &second);
        prop_assert_eq!(
            first.crate_name.as_str(),
            first.package_name.as_str().replace('-', "_")
        );
        prop_assert!(first.package_name.as_str().ends_with("-dagger-client"));
    }
}

#[test]
fn legacy_non_client_wire_bytes_do_not_gain_empty_client_fields() {
    let corpus = fixed_model_corpus(7, true, 0);
    let request = canonical_bytes(&corpus.request).unwrap();
    let manifest = canonical_bytes(&corpus.manifest).unwrap();
    let request_text = String::from_utf8(request.clone()).unwrap();
    let manifest_text = String::from_utf8(manifest.clone()).unwrap();
    assert!(!request_text.contains("resolved_pin"));
    assert!(!manifest_text.contains("amendments"));
    assert!(!manifest_text.contains("\"client\""));
    assert_eq!(
        canonical_bytes(&decode_canonical::<OperationRequest>(&request).unwrap()).unwrap(),
        request
    );
    assert_eq!(
        canonical_bytes(&decode_canonical::<OperationManifest>(&manifest).unwrap()).unwrap(),
        manifest
    );
}

#[test]
fn existing_package_identity_is_preserved_but_must_normalize_to_rust() {
    let root = RelativeOperationPath::parse("clients/minimal").unwrap();
    let module = StableCoordinate::new("minimal").unwrap();
    let identity = select_client_project_identity(ClientProjectIdentityRequest {
        existing_package_name: Some("my-existing-client"),
        client_root: &root,
        bound_module_name: &module,
    })
    .unwrap();
    assert_eq!(identity.package_name.as_str(), "my-existing-client");
    assert_eq!(identity.crate_name.as_str(), "my_existing_client");

    for invalid in ["async", "9client", "client!", ""] {
        let diagnostic = select_client_project_identity(ClientProjectIdentityRequest {
            existing_package_name: Some(invalid),
            client_root: &root,
            bound_module_name: &module,
        })
        .unwrap_err();
        assert_eq!(diagnostic.code, EngineDiagnosticCode::ClientProjectConflict);
        assert_eq!(diagnostic.coordinate.as_deref(), Some("package.name"));
    }
}
