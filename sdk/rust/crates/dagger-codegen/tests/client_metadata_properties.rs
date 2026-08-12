//! Engine-free properties for the finite standalone-client host projection.

use dagger_codegen::diagnostic::DiagnosticCode;
use dagger_codegen::engine::{
    ClientGenerationMetadata, REQUIRED_CLIENT_HOST_FILES, RelativeOperationPath,
};
use proptest::prelude::*;

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // Host discovery stays a closed canonical allow-list under arbitrary hostile mutation.
    #[test]
    fn property_22_required_host_file_metadata_finite_canonical(
        seed in any::<u8>(),
        mutation in 0_u8..16,
    ) {
        let mut candidate = REQUIRED_CLIENT_HOST_FILES
            .into_iter()
            .map(str::to_owned)
            .collect::<Vec<_>>();
        match mutation {
            0 => {}
            1 => candidate.reverse(),
            2 => { candidate.remove(usize::from(seed) % candidate.len()); }
            3 => candidate.push(candidate[usize::from(seed) % candidate.len()].clone()),
            4 => candidate[1] = "**/Cargo.lock".to_owned(),
            5 => candidate[5] = "**/target/debug/client".to_owned(),
            6 => candidate[0] = "**/.git/config".to_owned(),
            7 => candidate[2] = "**/.env".to_owned(),
            8 => candidate[5] = "**/*".to_owned(),
            9 => candidate[1] = "/Cargo.toml".to_owned(),
            10 => candidate[1] = "../Cargo.toml".to_owned(),
            11 => candidate[5] = "**\\src\\lib.rs".to_owned(),
            12 => candidate[2] = "**/README.md\nAuthorization: Bearer secret".to_owned(),
            13 => candidate[1] = "**/./Cargo.toml".to_owned(),
            14 => candidate[1] = "**/cargo.toml".to_owned(),
            15 => {}
            _ => unreachable!(),
        }

        if mutation == 15 {
            let bytes = serde_json::to_vec(&serde_json::json!({
                "format_version": 2,
                "required_host_files": candidate,
            })).unwrap();
            prop_assert!(serde_json::from_slice::<ClientGenerationMetadata>(&bytes).is_err());
            return Ok(());
        }

        let decoded = ClientGenerationMetadata::try_new(candidate.iter().map(String::as_str));
        if mutation == 0 {
            let metadata = decoded.unwrap();
            let bytes = metadata.encode().unwrap();
            let round_trip = serde_json::from_slice::<ClientGenerationMetadata>(&bytes).unwrap();
            prop_assert_eq!(&round_trip, &metadata);
            prop_assert_eq!(round_trip.encode().unwrap(), bytes);
            prop_assert_eq!(
                metadata
                    .required_host_files
                    .iter()
                    .map(RelativeOperationPath::as_str)
                    .collect::<Vec<_>>(),
                REQUIRED_CLIENT_HOST_FILES
            );
        } else {
            let diagnostics = decoded.unwrap_err();
            prop_assert!(diagnostics.contains(DiagnosticCode::RequiredHostFileInvalid));
            let rendered = diagnostics.to_string();
            for forbidden in ["Authorization", "Bearer", "secret", "Cargo.lock", "target/debug"] {
                prop_assert!(!rendered.contains(forbidden));
            }
        }
    }
}

#[test]
fn checked_client_generation_asset_is_the_canonical_baseline() {
    let checked = dagger_codegen::engine::BASELINE_CLIENT_GENERATION_JSON;
    assert_eq!(
        checked.strip_suffix(b"\n").unwrap_or(checked),
        ClientGenerationMetadata::baseline().encode().unwrap()
    );
}
