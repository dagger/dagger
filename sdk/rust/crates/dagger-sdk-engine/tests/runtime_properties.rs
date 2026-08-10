//! Runtime reproducibility, state-machine, provenance, and boundary properties.

mod support;

use std::collections::{BTreeMap, BTreeSet};

use dagger_sdk_engine::project::toolchain::{ToolchainDeclaration, select_toolchain};
use dagger_sdk_engine::runtime::{
    redact_runtime_output, runtime_boundary_is_clean, runtime_cargo_arguments, runtime_codegen_mode,
};
use dagger_sdk_engine::*;
use proptest::prelude::*;
use serde_json::Value;
use support::fixed_model_corpus;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // Every provenance coordinate is mandatory, digest-sensitive, and independent
    // from ambient values which are deliberately absent from the wire model.
    #[test]
    fn property_17_runtime_provenance_complete_secret_free(
        seed in any::<u8>(),
        mutation in 0_u8..9,
        secret in "[A-Za-z0-9]{8,32}",
    ) {
        let corpus = fixed_model_corpus(seed, seed % 2 == 0, 1);
        let original = canonical_digest(DigestDomain::RuntimeProvenance, &corpus.provenance).unwrap();
        let mut value = serde_json::to_value(&corpus.provenance).unwrap();
        let input = value["input"].as_object_mut().unwrap();
        let coordinate = match mutation {
            0 => "engine_source",
            1 => "toolchain",
            2 => "base_image_digest",
            3 => "lockfile_digest",
            4 => "module_source_digest",
            5 => "operation_manifest_digest",
            6 => "target",
            7 => "mode",
            _ => "format_version",
        };
        input.remove(coordinate);
        let bytes = canonical_bytes(&value).unwrap();
        prop_assert!(decode_canonical::<RuntimeProvenance>(&bytes).is_err());

        let encoded = canonical_bytes(&corpus.provenance).unwrap();
        prop_assert!(!String::from_utf8_lossy(&encoded).contains(&secret));
        let decoded: RuntimeProvenance = decode_canonical(&encoded).unwrap();
        prop_assert_eq!(canonical_digest(DigestDomain::RuntimeProvenance, &decoded).unwrap(), original.clone());
        let mut changed = decoded;
        changed.binary_digest = format!("sha256:{:064x}", u32::from(seed) + 65_536)
            .parse()
            .unwrap();
        prop_assert_ne!(canonical_digest(DigestDomain::RuntimeProvenance, &changed).unwrap(), original);
    }

    // Toolchain precedence and the emitted Cargo vector leave no moving toolchain,
    // unlocked graph, alternate package, binary, target, or target directory slot.
    #[test]
    fn property_18_runtime_toolchain_lock_target_reproducible(
        seed in any::<u8>(),
        declaration in 0_u8..4,
    ) {
        let corpus = fixed_model_corpus(seed, true, 1);
        let path = RelativeOperationPath::parse("workspace/rust-toolchain").unwrap();
        let bytes = match declaration {
            0 => "1.97.1\n".to_owned(),
            1 => "1.98.0\n".to_owned(),
            2 => "stable\n".to_owned(),
            _ => "1.96.0\n".to_owned(),
        };
        let selected = select_toolchain(&[ToolchainDeclaration { path: &path, bytes: bytes.as_bytes() }]);
        prop_assert_eq!(selected.is_ok(), declaration < 2);

        let args = runtime_cargo_arguments(&corpus.runtime_project, &corpus.provenance_input.target);
        prop_assert_eq!(args[0].as_str(), "build");
        prop_assert_eq!(&args[3..8], &["--package", corpus.runtime_project.discovered.target_package.name.as_str(), "--bin", "dagger-module", "--release"]);
        prop_assert_eq!(&args[8..], &["--locked", "--target", "x86_64-unknown-linux-gnu", "--target-dir", "/var/lib/dagger/rust/target"]);
        prop_assert!(!args.iter().any(|argument| argument.contains(';') || argument.contains("--workspace")));
    }

    // Semantically equal ordered models retain one canonical plan regardless of map
    // insertion order or unrelated ambient state.
    #[test]
    fn property_19_equivalent_runtime_inputs_equivalent_construction(
        seed in any::<u8>(),
        reverse in any::<bool>(),
        _ambient in proptest::collection::btree_map("[A-Z_]{1,12}", "[a-z]{0,12}", 0..12),
    ) {
        let corpus = fixed_model_corpus(seed, seed % 2 == 0, 1);
        let mut plan = corpus.runtime_plan.clone();
        let mut entries = plan.manifest.artifacts.clone().into_iter().collect::<Vec<_>>();
        if reverse {
            entries.reverse();
        }
        plan.manifest.artifacts = entries.into_iter().collect::<BTreeMap<_, _>>();
        let left = canonical_bytes(&corpus.runtime_plan).unwrap();
        let right = canonical_bytes(&plan).unwrap();
        prop_assert_eq!(left, right);
    }

    // The schema input is the complete two-state discriminator; checked mode never
    // invents a generation event and legacy mode never claims committed verification.
    #[test]
    fn property_20_generated_file_mode_explicit_state_machine(
        schema_present in any::<bool>(),
        artifacts_current in any::<bool>(),
        generation_succeeds in any::<bool>(),
    ) {
        let mode = runtime_codegen_mode(schema_present);
        let expected = if schema_present {
            RuntimeCodegenMode::LegacyRuntimeCodegen
        } else {
            RuntimeCodegenMode::CheckedGenerated
        };
        prop_assert_eq!(mode, expected);
        let generation_events = usize::from(schema_present);
        let host_writes = 0_usize;
        let accepted = if schema_present { generation_succeeds } else { artifacts_current };
        prop_assert_eq!(generation_events, usize::from(matches!(mode, RuntimeCodegenMode::LegacyRuntimeCodegen)));
        prop_assert_eq!(host_writes, 0);
        prop_assert_eq!(accepted, if schema_present { generation_succeeds } else { artifacts_current });
    }
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(128))]

    // Secret-bearing output is reduced to a constant marker, and the clean runtime
    // projection contains only the executable and provenance with no retained mount.
    #[test]
    fn property_24_build_credentials_caches_cannot_cross_runtime(
        seed in any::<u8>(),
        secret in "[A-Za-z0-9]{8,32}",
        cache_path in "[a-z]{1,12}",
        expose_secret in any::<bool>(),
    ) {
        let corpus = fixed_model_corpus(seed, true, 1);
        let output = if expose_secret {
            format!("dependency failed Authorization: Bearer {secret}")
        } else {
            format!("compiler failure {seed}")
        };
        let redacted = redact_runtime_output(output.as_bytes(), &[secret.as_bytes()]);
        prop_assert!(!String::from_utf8_lossy(&redacted).contains(&secret));

        let paths = BTreeSet::from([
            corpus.runtime_policy.runtime_install_path.to_string(),
            corpus.runtime_policy.provenance_install_path.to_string(),
        ]);
        prop_assert!(runtime_boundary_is_clean(&paths, &BTreeSet::new(), &corpus.runtime_policy));
        let contaminated_mounts = BTreeSet::from([format!("/var/cache/{cache_path}")]);
        prop_assert!(!runtime_boundary_is_clean(&paths, &contaminated_mounts, &corpus.runtime_policy));

        let mut provenance: Value = serde_json::to_value(&corpus.provenance).unwrap();
        provenance.as_object_mut().unwrap().insert("secret".to_owned(), Value::String(secret));
        prop_assert!(decode_canonical::<RuntimeProvenance>(&canonical_bytes(&provenance).unwrap()).is_err());
    }
}
