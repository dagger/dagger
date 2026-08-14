use std::io::{Cursor, Write};

use dagger_sdk_completeness::{
    Digest, PackagedArtifactKind, PackagedArtifactScanBundle, RepositoryRelativePath,
    assemble_packaged_artifact_scan_bundle, scan_packaged_artifact, secret_canary_set_from_entropy,
};
use flate2::{Compression, write::GzEncoder};
use serde_json::json;
use tar::{Builder, Header};

#[allow(dead_code)]
pub fn packaged_artifact_scan_bundle() -> PackagedArtifactScanBundle {
    let canaries =
        secret_canary_set_from_entropy(&std::array::from_fn::<_, 32, _>(|index| index as u8))
            .unwrap();
    let cli = b"fixture executable".to_vec();
    let image = minimal_oci_tar(br#"{}"#, b"safe fixture bytes", None);
    let artifacts = [
        ("build/cli", PackagedArtifactKind::RawExecutable, cli),
        (
            "build/backend-image.tar",
            PackagedArtifactKind::OciImageTar,
            image.clone(),
        ),
        (
            "build/frontend-image.tar",
            PackagedArtifactKind::OciImageTar,
            image,
        ),
    ];
    let reports = artifacts.into_iter().map(|(path, kind, bytes)| {
        let expected = Digest::sha256(&bytes);
        scan_packaged_artifact(
            &mut Cursor::new(bytes),
            RepositoryRelativePath::new(path).unwrap(),
            kind,
            &expected,
            &canaries,
        )
        .unwrap()
    });
    assemble_packaged_artifact_scan_bundle(reports).unwrap()
}

pub fn minimal_oci_tar(config: &[u8], layer_contents: &[u8], extra_blob: Option<&[u8]>) -> Vec<u8> {
    let expanded_layer = tar_with_file("app/fixture.txt", layer_contents);
    let mut compressor = GzEncoder::new(Vec::new(), Compression::default());
    compressor.write_all(&expanded_layer).unwrap();
    let layer = compressor.finish().unwrap();
    let config = config.to_vec();
    let layer_digest = Digest::sha256(&layer);
    let config_digest = Digest::sha256(&config);
    let manifest = serde_json::to_vec(&json!({
        "schemaVersion": 2,
        "config": {
            "mediaType": "application/vnd.oci.image.config.v1+json",
            "digest": config_digest.as_str(),
            "size": config.len()
        },
        "layers": [{
            "mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
            "digest": layer_digest.as_str(),
            "size": layer.len()
        }]
    }))
    .unwrap();
    let manifest_digest = Digest::sha256(&manifest);
    let index = serde_json::to_vec(&json!({
        "schemaVersion": 2,
        "manifests": [{
            "mediaType": "application/vnd.oci.image.manifest.v1+json",
            "digest": manifest_digest.as_str(),
            "size": manifest.len()
        }]
    }))
    .unwrap();

    let mut outer = Builder::new(Vec::new());
    append(
        &mut outer,
        "oci-layout",
        br#"{"imageLayoutVersion":"1.0.0"}"#,
    );
    append(&mut outer, "index.json", &index);
    append_blob(&mut outer, &config_digest, &config);
    append_blob(&mut outer, &manifest_digest, &manifest);
    append_blob(&mut outer, &layer_digest, &layer);
    if let Some(extra_blob) = extra_blob {
        append_blob(&mut outer, &Digest::sha256(extra_blob), extra_blob);
    }
    outer.into_inner().unwrap()
}

fn tar_with_file(path: &str, bytes: &[u8]) -> Vec<u8> {
    let mut archive = Builder::new(Vec::new());
    append(&mut archive, path, bytes);
    archive.into_inner().unwrap()
}

fn append_blob(archive: &mut Builder<Vec<u8>>, digest: &Digest, bytes: &[u8]) {
    append(
        archive,
        &format!(
            "blobs/sha256/{}",
            digest.as_str().trim_start_matches("sha256:")
        ),
        bytes,
    );
}

fn append(archive: &mut Builder<Vec<u8>>, path: &str, bytes: &[u8]) {
    let mut header = Header::new_gnu();
    header.set_size(bytes.len() as u64);
    header.set_mode(0o644);
    header.set_cksum();
    archive.append_data(&mut header, path, bytes).unwrap();
}
