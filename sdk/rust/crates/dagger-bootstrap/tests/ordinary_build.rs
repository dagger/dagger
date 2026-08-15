//! Pure checks for the ordinary package and complete-engine build contracts.

use std::collections::{BTreeMap, BTreeSet};

use proptest::prelude::*;

#[allow(dead_code)]
#[path = "../src/bin/dagger-rust-sdk-check.rs"]
mod check;

use check::{
    DependencyView, EngineContractInput, MetadataView, PackageArchive, PackageManifestView,
    PackageView, validate_engine_contract, validate_package_contract,
};

fn package(name: &str, version: &str, publishable: bool) -> PackageView {
    PackageView {
        name: name.to_owned(),
        version: version.to_owned(),
        publishable,
        features: BTreeMap::new(),
        dependencies: Vec::new(),
        edition: "2024".to_owned(),
        rust_version: Some("1.97.1".to_owned()),
    }
}

fn valid_package_contract(seed: u16) -> (MetadataView, Vec<PackageArchive>) {
    let version = format!("1.0.0-beta.{}", seed % 32 + 1);
    let mut sdk = package("dagger-sdk", &version, true);
    sdk.features = BTreeMap::from([
        ("default".to_owned(), vec!["gen".to_owned()]),
        ("gen".to_owned(), Vec::new()),
    ]);
    sdk.dependencies.push(DependencyView {
        name: "dagger-sdk-macros".to_owned(),
        requirement: format!("={version}"),
        kind: None,
        source: None,
    });
    let macros = package("dagger-sdk-macros", &version, true);
    let internal = package("dagger-bootstrap", &version, false);

    let sdk_archive = PackageArchive {
        file_name: format!("dagger-sdk-{version}.crate"),
        root: format!("dagger-sdk-{version}"),
        files: BTreeSet::from([
            "Cargo.toml".to_owned(),
            "LICENSE".to_owned(),
            "README.md".to_owned(),
            "examples/first-pipeline/main.rs".to_owned(),
            "src/gen/mod.rs".to_owned(),
            "src/lib.rs".to_owned(),
        ]),
        manifest: PackageManifestView {
            name: "dagger-sdk".to_owned(),
            version: version.clone(),
            features: BTreeSet::from(["default".to_owned(), "gen".to_owned()]),
            macro_dependency: Some(format!("={version}")),
            sdk_dependency_present: false,
        },
        safe: true,
    };
    let macro_archive = PackageArchive {
        file_name: format!("dagger-sdk-macros-{version}.crate"),
        root: format!("dagger-sdk-macros-{version}"),
        files: BTreeSet::from([
            "Cargo.toml".to_owned(),
            "README.md".to_owned(),
            "src/lib.rs".to_owned(),
        ]),
        manifest: PackageManifestView {
            name: "dagger-sdk-macros".to_owned(),
            version,
            features: BTreeSet::new(),
            macro_dependency: None,
            sdk_dependency_present: false,
        },
        safe: true,
    };

    (
        MetadataView {
            packages: vec![internal, sdk, macros],
        },
        vec![sdk_archive, macro_archive],
    )
}

fn mutate_package_contract(
    metadata: &mut MetadataView,
    archives: &mut Vec<PackageArchive>,
    mutation: u8,
) {
    match mutation {
        0 => {}
        1 => metadata
            .packages
            .push(package("unexpected-public-crate", "1.0.0", true)),
        2 => sdk(metadata).version.push_str("-different"),
        3 => {
            sdk(metadata)
                .features
                .insert("unexpected-build-feature".to_owned(), Vec::new());
        }
        4 => sdk(metadata).dependencies[0].requirement = "*".to_owned(),
        5 => sdk_archive(archives).safe = false,
        6 => {
            sdk_archive(archives).files.remove("src/lib.rs");
        }
        7 => {
            sdk_archive(archives)
                .manifest
                .features
                .insert("unexpected-build-feature".to_owned());
        }
        8 => sdk_archive(archives).manifest.macro_dependency = Some("*".to_owned()),
        9 => macro_archive(archives).manifest.sdk_dependency_present = true,
        10 => sdk_archive(archives).file_name.push_str(".unexpected"),
        11 => macro_archive(archives).root.push_str("-unexpected"),
        12 => macro_archive(archives)
            .manifest
            .version
            .push_str("-different"),
        13 => archives.push(archives[0].clone()),
        14 => {
            metadata
                .packages
                .iter_mut()
                .find(|package| package.name == "dagger-sdk-macros")
                .expect("fixture contains macros")
                .rust_version = Some("1.96.0".to_owned());
        }
        _ => unreachable!("strategy bounds mutation"),
    }
}

fn sdk(metadata: &mut MetadataView) -> &mut PackageView {
    metadata
        .packages
        .iter_mut()
        .find(|package| package.name == "dagger-sdk")
        .expect("fixture contains SDK")
}

fn sdk_archive(archives: &mut [PackageArchive]) -> &mut PackageArchive {
    archive(archives, "dagger-sdk")
}

fn macro_archive(archives: &mut [PackageArchive]) -> &mut PackageArchive {
    archive(archives, "dagger-sdk-macros")
}

fn archive<'a>(archives: &'a mut [PackageArchive], name: &str) -> &'a mut PackageArchive {
    archives
        .iter_mut()
        .find(|archive| archive.manifest.name == name)
        .expect("fixture contains requested archive")
}

fn digest(bytes: &[u8; 32]) -> String {
    let encoded = bytes
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    format!("sha256:{encoded}")
}

fn other_digest(value: &str) -> String {
    let mut other = value.to_owned();
    let replacement = if other.ends_with('0') { '1' } else { '0' };
    other.pop();
    other.push(replacement);
    other
}

// The ordinary package result is valid only for the complete two-crate public closure.
proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn property_1_public_package_closure(
        seed in any::<u16>(),
        mutation in 0_u8..15,
        reverse_archives in any::<bool>(),
    ) {
        let (mut metadata, mut archives) = valid_package_contract(seed);
        if reverse_archives {
            archives.reverse();
        }
        mutate_package_contract(&mut metadata, &mut archives, mutation);

        prop_assert_eq!(
            validate_package_contract(&metadata, &archives).is_ok(),
            mutation == 0,
        );
    }
}

// Complete-engine validation selects the exact Rust manifest while tolerating unrelated blobs.
proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn property_2_engine_manifest_selection(
        bytes in any::<[u8; 32]>(),
        unrelated in any::<[u8; 32]>(),
        mutation in 0_u8..5,
    ) {
        let selected = digest(&bytes);
        let selected_blob = selected.trim_start_matches("sha256:").to_owned();
        let mut input = EngineContractInput {
            expected_rust_manifest: selected.clone(),
            rust_manifest: selected,
            blobs: BTreeSet::from([
                selected_blob.clone(),
                digest(&unrelated).trim_start_matches("sha256:").to_owned(),
            ]),
        };
        match mutation {
            0 => {}
            1 => input.expected_rust_manifest = other_digest(&input.expected_rust_manifest),
            2 => input.rust_manifest = input.rust_manifest.to_uppercase(),
            3 => {
                input.blobs.remove(&selected_blob);
            }
            4 => input.rust_manifest = input.rust_manifest.replacen("sha256:", "sha512:", 1),
            _ => unreachable!("strategy bounds mutation"),
        }

        prop_assert_eq!(validate_engine_contract(&input).is_ok(), mutation == 0);
    }
}
