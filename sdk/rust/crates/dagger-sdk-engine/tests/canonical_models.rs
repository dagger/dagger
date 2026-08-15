//! Canonical engine wire-model round-trip and rejection properties.

mod support;

use std::fmt::Debug;

use dagger_sdk_engine::*;
use proptest::prelude::*;
use serde::Serialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use support::{ModelCorpus, model_corpus};

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // A valid semantic value has one byte spelling and one domain-separated identity.
    #[test]
    fn property_30_canonical_models_round_trip_without_semantic_loss(
        corpus in model_corpus(),
        invalid_kind in 0_u8..15,
    ) {
        round_trip_corpus(&corpus);
        reject_invalid_boundary(&corpus, invalid_kind);
    }
}

fn round_trip_corpus(corpus: &ModelCorpus) {
    round_trip(&corpus.target);
    round_trip(&corpus.schema);
    round_trip(&corpus.module);
    round_trip(&corpus.client_module);
    round_trip(&corpus.client_project);
    round_trip(&corpus.client_initialization);
    round_trip(&corpus.client_execution_request);
    round_trip(&corpus.dependency);
    round_trip(&corpus.request);
    round_trip(&corpus.candidate);
    round_trip(&corpus.post_work);
    round_trip(&corpus.plan);
    round_trip(&corpus.post_work_record);
    round_trip(&corpus.generator);
    round_trip(&corpus.artifact_record);
    round_trip(&corpus.manifest);
    round_trip(&corpus.amendment_coordinate);
    round_trip(&corpus.amendment_record);
    round_trip(&corpus.client_manifest);
    round_trip(&corpus.client_operation_manifest);
    round_trip(&corpus.engine_source);
    round_trip(&corpus.cargo_package);
    round_trip(&corpus.toolchain);
    round_trip(&corpus.discovered);
    round_trip(&corpus.cargo_target);
    round_trip(&corpus.runtime_project);
    round_trip(&corpus.provenance_input);
    round_trip(&corpus.provenance);
    round_trip(&corpus.runtime_policy);
    round_trip(&corpus.runtime_request);
    round_trip(&corpus.runtime_plan);
    round_trip(&corpus.asset);
    round_trip(&corpus.asset_manifest);

    digest_round_trip(DigestDomain::OperationRequest, &corpus.request);
    digest_round_trip(DigestDomain::OperationManifest, &corpus.manifest);
    digest_round_trip(
        DigestDomain::OperationManifest,
        &corpus.client_operation_manifest,
    );
    digest_round_trip(DigestDomain::EngineSource, &corpus.engine_source);
    digest_round_trip(DigestDomain::RuntimeProvenance, &corpus.provenance);
    digest_round_trip(DigestDomain::PackagedAssets, &corpus.asset_manifest);
}

fn round_trip<T>(value: &T)
where
    T: Debug + Eq + Serialize + DeserializeOwned,
{
    let bytes = canonical_bytes(value).unwrap();
    let decoded: T = decode_canonical(&bytes).unwrap();
    assert_eq!(&decoded, value);
    assert_eq!(canonical_bytes(&decoded).unwrap(), bytes);
}

fn digest_round_trip<T>(domain: DigestDomain, value: &T)
where
    T: Debug + Eq + Serialize + DeserializeOwned,
{
    let bytes = canonical_bytes(value).unwrap();
    let decoded: T = decode_canonical(&bytes).unwrap();
    assert_eq!(
        canonical_digest(domain, value).unwrap(),
        canonical_digest(domain, &decoded).unwrap()
    );
}

fn reject_invalid_boundary(corpus: &ModelCorpus, invalid_kind: u8) {
    match invalid_kind {
        0 => reject_request(corpus, |value| {
            value
                .as_object_mut()
                .unwrap()
                .insert("ambient_path".into(), json!("/tmp"));
        }),
        1 => reject_request(corpus, |value| {
            value["operation"] = json!("unknown-operation")
        }),
        2 => reject_request(corpus, |value| {
            value["sdk_dependency"] = json!({
                "package": "dagger-sdk",
                "revision": "main",
                "source": "git",
                "url": "https://github.com/dagger/dagger"
            });
        }),
        3 => reject_request(corpus, |value| {
            value["target"]["core_schema_digest"] = json!("sha256:00");
        }),
        4 => reject_request(corpus, |value| value["output_root"] = json!("../escape")),
        5 => reject_request(corpus, |value| value["format_version"] = json!(2)),
        6 => reject::<EngineSourceDescriptor>(&corpus.engine_source, |value| {
            value["dagger_revision"] = json!("stable");
        }),
        7 => reject::<OperationManifest>(&corpus.manifest, |value| {
            value["output_root"] = json!("/absolute");
        }),
        8 => reject::<RuntimeProvenanceInput>(&corpus.provenance_input, |value| {
            value["base_image_digest"] = json!("SHA256:BAD");
        }),
        9 => reject::<PackagedAssetManifest>(&corpus.asset_manifest, |value| {
            let assets = value["assets"].as_object_mut().unwrap();
            let (_, asset) = assets.iter().next().unwrap();
            let mut asset = asset.clone();
            asset["path"] = json!("../private/tool");
            assets.clear();
            assets.insert("../private/tool".into(), asset);
        }),
        10 => {
            let non_canonical = serde_json::to_vec(&corpus.request).unwrap();
            assert!(decode_canonical::<OperationRequest>(&non_canonical).is_err());
        }
        11 => reject::<ClientInitializationRequest>(&corpus.client_initialization, |value| {
            value["client_root"] = json!("../escape");
        }),
        12 => reject::<ClientProjectIdentity>(&corpus.client_project, |value| {
            value["package_name"] = json!("async");
        }),
        13 => reject::<OperationManifest>(&corpus.client_operation_manifest, |value| {
            value["client"]["module"]["resolved_pin"] = json!("main");
        }),
        14 => reject::<EngineExecutionRequest>(&corpus.client_execution_request, |value| {
            value["request_kind"] = json!("initialize-everything");
        }),
        _ => unreachable!(),
    }
}

fn reject_request(corpus: &ModelCorpus, mutate: impl FnOnce(&mut Value)) {
    reject::<OperationRequest>(&corpus.request, mutate);
}

fn reject<T>(value: &T, mutate: impl FnOnce(&mut Value))
where
    T: Serialize + DeserializeOwned,
{
    let mut invalid = serde_json::to_value(value).unwrap();
    mutate(&mut invalid);
    let bytes = canonical_bytes(&invalid).unwrap();
    assert!(decode_canonical::<T>(&bytes).is_err());
}
