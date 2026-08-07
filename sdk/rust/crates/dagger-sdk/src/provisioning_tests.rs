//! Deterministic properties for verified acquisition and cache publication.

use std::collections::VecDeque;
use std::io::Read;
use std::io::{Cursor, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::atomic::AtomicBool;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use flate2::Compression;
use flate2::write::GzEncoder;
use futures::stream;
use proptest::prelude::*;
use semver::Version;
use sha2::{Digest, Sha256};
use tar::{Builder as TarBuilder, EntryType, Header};
use zip::ZipWriter;
use zip::write::SimpleFileOptions;

use crate::archive::{MANIFEST_LIMIT, extract_expected, parse_manifest};
use crate::provision::{
    DefaultCliProvisioner, DownloadResponse, ProvisioningHttp, ProvisioningRequestKind,
    ReqwestProvisioningHttp, RetentionRemover, cache_for_test,
};
use crate::provisioning_control::{
    NoopProvisioningObserver, ProvisionCheckpoint, ProvisioningCancellation, ProvisioningObserver,
};
use crate::provisioning_error::{ProvisionError, ProvisionErrorKind};
use crate::target::{
    Architecture, ArchiveDescriptor, ArchiveFormat, OperatingSystem, exact_target,
};
use crate::test_support::{io_proptest_config, proptest_config};

#[derive(Clone)]
struct ResponseFixture {
    status: u16,
    content_length: Option<u64>,
    chunks: Vec<Result<Vec<u8>, ProvisionError>>,
    yields: usize,
}

impl ResponseFixture {
    fn bytes(status: u16, bytes: Vec<u8>, chunk_size: usize) -> Self {
        let chunk_size = chunk_size.max(1);
        Self {
            status,
            content_length: Some(bytes.len() as u64),
            chunks: bytes
                .chunks(chunk_size)
                .map(|chunk| Ok(chunk.to_vec()))
                .collect(),
            yields: 0,
        }
    }
}

#[derive(Clone)]
struct FixtureHttp {
    manifest: ResponseFixture,
    archive: ResponseFixture,
    events: Arc<Mutex<Vec<ProvisioningRequestKind>>>,
}

impl FixtureHttp {
    fn new(manifest: ResponseFixture, archive: ResponseFixture) -> Self {
        Self {
            manifest,
            archive,
            events: Arc::new(Mutex::new(Vec::new())),
        }
    }

    fn events(&self) -> Vec<ProvisioningRequestKind> {
        self.events
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .clone()
    }
}

impl ProvisioningHttp for FixtureHttp {
    async fn get(
        &self,
        _url: &url::Url,
        kind: ProvisioningRequestKind,
        cancellation: &ProvisioningCancellation,
    ) -> Result<DownloadResponse, ProvisionError> {
        cancellation.check()?;
        self.events
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .push(kind);
        let fixture = match kind {
            ProvisioningRequestKind::Manifest => self.manifest.clone(),
            ProvisioningRequestKind::Archive => self.archive.clone(),
        };
        let stream = stream::unfold(
            (VecDeque::from(fixture.chunks), fixture.yields),
            |(mut chunks, yields)| async move {
                let item = chunks.pop_front()?;
                for _ in 0..yields {
                    tokio::task::yield_now().await;
                }
                Some((item, (chunks, yields)))
            },
        );
        Ok(DownloadResponse::new(
            fixture.status,
            fixture.content_length,
            Box::pin(stream),
        ))
    }
}

#[derive(Clone)]
struct CancelAt {
    target: ProvisionCheckpoint,
    cancellation: ProvisioningCancellation,
}

impl ProvisioningObserver for CancelAt {
    fn checkpoint(&self, checkpoint: ProvisionCheckpoint) {
        if checkpoint == self.target {
            self.cancellation.cancel();
        }
    }
}

#[derive(Clone)]
struct FailingRemover {
    calls: Arc<AtomicUsize>,
}

impl RetentionRemover for FailingRemover {
    fn remove(&self, _path: &Path) -> std::io::Result<()> {
        self.calls.fetch_add(1, Ordering::Relaxed);
        Err(std::io::Error::other("injected retention failure"))
    }
}

fn descriptor(format: ArchiveFormat) -> ArchiveDescriptor {
    let target = exact_target().expect("the checked exact target is valid");
    let operating_system = match format {
        ArchiveFormat::TarGz => OperatingSystem::Linux,
        ArchiveFormat::Zip => OperatingSystem::Windows,
    };
    ArchiveDescriptor::for_target(target, operating_system, Architecture::Amd64)
        .expect("the fixture target is supported")
}

fn descriptor_version(descriptor: &ArchiveDescriptor) -> Version {
    descriptor
        .cli_version()
        .expect("the checked descriptor version is valid")
}

fn digest(bytes: &[u8]) -> [u8; 32] {
    Sha256::digest(bytes).into()
}

fn manifest_for(descriptor: &ArchiveDescriptor, archive: &[u8]) -> Vec<u8> {
    format!(
        "{}  {}\n",
        hex::encode(digest(archive)),
        descriptor.archive_name()
    )
    .into_bytes()
}

#[derive(Clone, Copy)]
enum FixtureEntryKind {
    File,
    Directory,
    Symlink,
}

fn tar_archive(entries: &[(String, Vec<u8>, FixtureEntryKind)]) -> Vec<u8> {
    let encoder = GzEncoder::new(Vec::new(), Compression::default());
    let mut archive = TarBuilder::new(encoder);
    for (path, bytes, kind) in entries {
        let mut header = Header::new_gnu();
        header.set_mode(0o700);
        header.set_mtime(0);
        header.set_uid(0);
        header.set_gid(0);
        match kind {
            FixtureEntryKind::File => {
                header.set_entry_type(EntryType::Regular);
                header.set_size(bytes.len() as u64);
            }
            FixtureEntryKind::Directory => {
                header.set_entry_type(EntryType::Directory);
                header.set_size(0);
            }
            FixtureEntryKind::Symlink => {
                header.set_entry_type(EntryType::Symlink);
                header.set_size(0);
                header
                    .set_link_name("target")
                    .expect("fixture link target is valid");
            }
        }
        header.set_cksum();
        archive
            .append_data(&mut header, path, bytes.as_slice())
            .expect("fixture tar entry is valid");
    }
    let encoder = archive.into_inner().expect("fixture tar finishes");
    encoder.finish().expect("fixture gzip finishes")
}

fn zip_archive(entries: &[(String, Vec<u8>, FixtureEntryKind)]) -> Vec<u8> {
    let mut archive = ZipWriter::new(Cursor::new(Vec::new()));
    for (path, bytes, kind) in entries {
        match kind {
            FixtureEntryKind::File => {
                archive
                    .start_file(
                        path,
                        SimpleFileOptions::default()
                            .compression_method(zip::CompressionMethod::Deflated)
                            .unix_permissions(0o700),
                    )
                    .expect("fixture ZIP file starts");
                archive
                    .write_all(bytes)
                    .expect("fixture ZIP payload writes");
            }
            FixtureEntryKind::Directory => {
                archive
                    .add_directory(path, SimpleFileOptions::default().unix_permissions(0o700))
                    .expect("fixture ZIP directory writes");
            }
            FixtureEntryKind::Symlink => {
                archive
                    .add_symlink(
                        path,
                        "target",
                        SimpleFileOptions::default().unix_permissions(0o777),
                    )
                    .expect("fixture ZIP symlink writes");
            }
        }
    }
    archive.finish().expect("fixture ZIP finishes").into_inner()
}

fn archive_for(format: ArchiveFormat, entries: &[(String, Vec<u8>, FixtureEntryKind)]) -> Vec<u8> {
    match format {
        ArchiveFormat::TarGz => tar_archive(entries),
        ArchiveFormat::Zip => zip_archive(entries),
    }
}

fn fixture_http(
    descriptor: &ArchiveDescriptor,
    archive: Vec<u8>,
    chunk_size: usize,
) -> FixtureHttp {
    FixtureHttp::new(
        ResponseFixture::bytes(200, manifest_for(descriptor, &archive), chunk_size),
        ResponseFixture::bytes(200, archive, chunk_size),
    )
}

fn runtime() -> tokio::runtime::Runtime {
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("the property runtime is available")
}

fn checkpoint_case(value: u8) -> ProvisionCheckpoint {
    match value % 10 {
        0 => ProvisionCheckpoint::ManifestRequest,
        1 => ProvisionCheckpoint::ManifestRead,
        2 => ProvisionCheckpoint::ArchiveRequest,
        3 => ProvisionCheckpoint::ArchiveRead,
        4 => ProvisionCheckpoint::ChecksumAccepted,
        5 => ProvisionCheckpoint::ExtractRead,
        6 => ProvisionCheckpoint::Extracted,
        7 => ProvisionCheckpoint::CacheLock,
        8 => ProvisionCheckpoint::Flush,
        _ => ProvisionCheckpoint::Publication,
    }
}

proptest! {
    #![proptest_config(proptest_config())]

    // Invariant: a manifest selects one exact, syntactically valid SHA-256 entry or a typed error.
    // Feature: rust-sdk-transport-observability, Property 7: manifest parsing is bounded and total
    #[test]
    fn property_07_manifest_parsing_bounded_total(
        archive_name in "dagger_v[0-9]{1,3}_[a-z]{3,8}_[a-z0-9]{3,8}\\.(tar\\.gz|zip)",
        digest_bytes in any::<[u8; 32]>(),
        unrelated in proptest::collection::vec(("[a-f0-9]{64}", "other-[a-z0-9.]{1,24}"), 0..6),
        mutation in 0_u8..7,
        crlf in any::<bool>(),
    ) {
        let newline = if crlf { "\r\n" } else { "\n" };
        let digest_text = hex::encode(digest_bytes);
        let mut lines = unrelated
            .iter()
            .map(|(digest, name)| format!("{digest}  {name}"))
            .collect::<Vec<_>>();
        let expected = match mutation {
            0 => {
                lines.push(format!("{digest_text}  {archive_name}"));
                Ok(digest_bytes)
            }
            1 => Err(ProvisionErrorKind::MissingChecksum),
            2 => {
                lines.push(format!("{digest_text} {archive_name} extra"));
                Err(ProvisionErrorKind::ManifestSyntax)
            }
            3 => {
                lines.push(format!("not-a-digest  {archive_name}"));
                Err(ProvisionErrorKind::InvalidChecksum)
            }
            4 => {
                lines.push(format!("{digest_text}  {archive_name}"));
                lines.push(format!("{digest_text}  {archive_name}"));
                Err(ProvisionErrorKind::AmbiguousChecksum)
            }
            5 => {
                lines.push(format!("{digest_text}  {archive_name}"));
                lines.push(format!("{}  {archive_name}", "f".repeat(64)));
                Err(ProvisionErrorKind::AmbiguousChecksum)
            }
            _ => {
                lines.insert(0, String::new());
                lines.push(format!("{digest_text}  {archive_name}"));
                Err(ProvisionErrorKind::ManifestSyntax)
            }
        };
        let bytes = lines.join(newline).into_bytes();
        let observed = std::panic::catch_unwind(|| parse_manifest(&bytes, &archive_name));
        prop_assert!(observed.is_ok());
        let observed = observed.expect("the parser did not unwind");
        match expected {
            Ok(expected) => prop_assert_eq!(observed.expect("valid manifest"), crate::archive::ExpectedArchive::new(expected)),
            Err(kind) => prop_assert_eq!(observed.expect_err("invalid manifest").kind(), kind),
        }
    }
}

proptest! {
    #![proptest_config(io_proptest_config())]

    // Invariant: archive bytes reach output only through one verified, regular, bounded member.
    // Feature: rust-sdk-transport-observability, Property 8: archive acceptance is integrity-gated, bounded, and confined
    #[test]
    fn property_08_archive_integrity_bounded_confined(
        zip in any::<bool>(),
        payload in proptest::collection::vec(any::<u8>(), 0..96),
        case in 0_u8..7,
        output_limit in 0_u64..96,
    ) {
        let format = if zip { ArchiveFormat::Zip } else { ArchiveFormat::TarGz };
        let member = if zip { "dagger.exe" } else { "dagger" };
        let entries = match case {
            0 => vec![(member.to_owned(), payload.clone(), FixtureEntryKind::File)],
            1 => vec![("README".to_owned(), payload.clone(), FixtureEntryKind::File)],
            2 => vec![
                (member.to_owned(), payload.clone(), FixtureEntryKind::File),
                (format!("nested/{member}"), payload.clone(), FixtureEntryKind::File),
            ],
            3 => vec![(member.to_owned(), Vec::new(), FixtureEntryKind::Symlink)],
            4 => vec![(format!("nested/{member}"), payload.clone(), FixtureEntryKind::File)],
            5 if zip => vec![(format!("../{member}"), payload.clone(), FixtureEntryKind::File)],
            5 => vec![(format!("folder/{member}"), payload.clone(), FixtureEntryKind::Directory)],
            _ => vec![(member.to_owned(), payload.clone(), FixtureEntryKind::File)],
        };
        let archive = if case == 6 {
            vec![0x1f, 0x8b, 0x08, 0xff, 0x00]
        } else {
            archive_for(format, &entries)
        };
        let mut output = Vec::new();
        let observed = extract_expected(
            Cursor::new(archive),
            &mut output,
            format,
            member,
            output_limit,
            &ProvisioningCancellation::new(),
            &NoopProvisioningObserver,
        );
        let expected_kind = match case {
            0 | 4 if payload.len() as u64 <= output_limit => None,
            0 | 4 => Some(ProvisionErrorKind::ExecutableTooLarge),
            1 => Some(ProvisionErrorKind::MissingMember),
            2 if payload.len() as u64 > output_limit => {
                Some(ProvisionErrorKind::ExecutableTooLarge)
            }
            2 => Some(ProvisionErrorKind::AmbiguousMember),
            3 | 5 => Some(ProvisionErrorKind::UnsafeMember),
            6 => Some(ProvisionErrorKind::ArchiveFormat),
            _ => unreachable!(),
        };
        match expected_kind {
            None => {
                prop_assert_eq!(observed.expect("accepted archive"), payload.len() as u64);
                prop_assert_eq!(output, payload);
            }
            Some(kind) => {
                let error = observed.expect_err("rejected archive");
                prop_assert_eq!(error.kind(), kind);
                let rendered = format!("{} {:?}", error, error);
                let payload_text = String::from_utf8_lossy(&payload);
                // Single-byte values are ordinary language fragments, not meaningful
                // leak canaries. Requiring a nontrivial sequence avoids classifying the
                // `c` in "archive" as disclosure while preserving the redaction check.
                let secret_safe = payload.len() < 8 || !rendered.contains(payload_text.as_ref());
                prop_assert!(secret_safe);
            }
        }
    }

    // Invariant: cancellation at every acquisition boundary leaves only durable cache metadata.
    // Feature: rust-sdk-transport-observability, Property 9: provisioning cancellation removes private state
    #[test]
    fn property_09_provisioning_cancellation_removes_private_state(
        checkpoint_index in 0_u8..10,
        zip in any::<bool>(),
        chunk_size in 1_usize..24,
        payload in proptest::collection::vec(any::<u8>(), 1..96),
    ) {
        let format = if zip { ArchiveFormat::Zip } else { ArchiveFormat::TarGz };
        let descriptor = descriptor(format);
        let archive = archive_for(
            format,
            &[(descriptor.member_name().to_owned(), payload, FixtureEntryKind::File)],
        );
        let http = fixture_http(&descriptor, archive, chunk_size);
        let cancellation = ProvisioningCancellation::new();
        let observer = CancelAt {
            target: checkpoint_case(checkpoint_index),
            cancellation: cancellation.clone(),
        };
        let fixture = tempfile::tempdir().expect("cache fixture");
        let provisioner = DefaultCliProvisioner::with_cache_root(
            http,
            fixture.path().to_path_buf(),
            observer,
        );
        let error = runtime()
            .block_on(provisioner.acquire(&descriptor, &cancellation))
            .expect_err("the selected checkpoint cancels");
        prop_assert_eq!(error.kind(), ProvisionErrorKind::Cancelled);
        let cache = cache_for_test(
            fixture.path().to_path_buf(),
            descriptor_version(&descriptor),
            format,
        );
        prop_assert!(!cache.selected_path().exists());
        let surviving = std::fs::read_dir(fixture.path())
            .expect("cache remains readable")
            .filter_map(Result::ok)
            .map(|entry| entry.file_name())
            .collect::<Vec<_>>();
        prop_assert!(surviving.iter().all(|name| name == ".rust-sdk-cli-cache.lock"));
    }
}

proptest! {
    #![proptest_config(io_proptest_config())]

    // Invariant: no-follow cache validation accepts only private regular files and cache hits perform no HTTP.
    // Feature: rust-sdk-transport-observability, Property 10: cache validation is no-follow and network-free on hits
    #[test]
    fn property_10_cache_validation_no_follow_network_free(
        shape in 0_u8..4,
        zip in any::<bool>(),
        payload in proptest::collection::vec(any::<u8>(), 0..64),
    ) {
        let format = if zip { ArchiveFormat::Zip } else { ArchiveFormat::TarGz };
        let descriptor = descriptor(format);
        let fixture = tempfile::tempdir().expect("cache fixture");
        let cache = cache_for_test(
            fixture.path().to_path_buf(),
            descriptor_version(&descriptor),
            format,
        );
        cache.prepare().expect("cache prepares");
        let selected = cache.selected_path();
        match shape {
            0 => {}
            1 | 2 => {
                std::fs::write(&selected, &payload).expect("cache file writes");
                #[cfg(unix)]
                {
                    use std::os::unix::fs::PermissionsExt;
                    let mode = if shape == 1 { 0o700 } else { 0o600 };
                    std::fs::set_permissions(&selected, std::fs::Permissions::from_mode(mode))
                        .expect("cache permissions set");
                }
            }
            _ => std::fs::create_dir(&selected).expect("cache directory shape writes"),
        }
        let validation = cache.validate();
        let accepted = shape == 1 || (shape == 2 && !cfg!(unix));
        prop_assert_eq!(validation.is_ok_and(|value| value), accepted);

        if accepted {
            let empty = ResponseFixture::bytes(500, Vec::new(), 1);
            let http = FixtureHttp::new(empty.clone(), empty);
            let probe = http.clone();
            let provisioner = DefaultCliProvisioner::with_cache_root(
                http,
                fixture.path().to_path_buf(),
                NoopProvisioningObserver,
            );
            let executable = runtime()
                .block_on(provisioner.acquire(&descriptor, &ProvisioningCancellation::new()))
                .expect("valid cache hit");
            prop_assert_eq!(executable.path(), selected.as_path());
            prop_assert!(probe.events().is_empty());
        }
    }

    // Invariant: independent publishers converge on one complete executable and expose no partial final file.
    // Feature: rust-sdk-transport-observability, Property 11: concurrent publication has one atomic result
    #[test]
    fn property_11_concurrent_publication_one_atomic_result(
        payload in proptest::collection::vec(any::<u8>(), 1..96),
        chunk_a in 1_usize..24,
        chunk_b in 1_usize..24,
        yields_a in 0_usize..3,
        yields_b in 0_usize..3,
        cancel_second in any::<bool>(),
    ) {
        let descriptor = descriptor(ArchiveFormat::TarGz);
        let archive = archive_for(
            ArchiveFormat::TarGz,
            &[(descriptor.member_name().to_owned(), payload.clone(), FixtureEntryKind::File)],
        );
        let mut http_a = fixture_http(&descriptor, archive.clone(), chunk_a);
        let mut http_b = fixture_http(&descriptor, archive, chunk_b);
        http_a.archive.yields = yields_a;
        http_b.archive.yields = yields_b;
        let fixture = tempfile::tempdir().expect("cache fixture");
        let root = fixture.path().to_path_buf();
        let cancellation_a = ProvisioningCancellation::new();
        let cancellation_b = ProvisioningCancellation::new();
        if cancel_second {
            cancellation_b.cancel();
        }

        let observed = runtime().block_on(async {
            let first = DefaultCliProvisioner::with_cache_root(
                http_a,
                root.clone(),
                NoopProvisioningObserver,
            );
            let second = DefaultCliProvisioner::with_cache_root(
                http_b,
                root.clone(),
                NoopProvisioningObserver,
            );
            let descriptor_a = descriptor.clone();
            let descriptor_b = descriptor.clone();
            let task_a = tokio::spawn(async move {
                let executable = first.acquire(&descriptor_a, &cancellation_a).await?;
                let bytes = std::fs::read(executable.path()).map_err(|error| {
                    ProvisionError::with_source(ProvisionErrorKind::CachePublication, error)
                })?;
                drop(executable);
                Ok::<_, ProvisionError>(bytes)
            });
            let task_b = tokio::spawn(async move {
                let result = second.acquire(&descriptor_b, &cancellation_b).await;
                match result {
                    Ok(executable) => {
                        let bytes = std::fs::read(executable.path()).map_err(|error| {
                            ProvisionError::with_source(ProvisionErrorKind::CachePublication, error)
                        })?;
                        drop(executable);
                        Ok(Some(bytes))
                    }
                    Err(error) if error.kind() == ProvisionErrorKind::Cancelled => Ok(None),
                    Err(error) => Err(error),
                }
            });
            let first = task_a.await.expect("first publisher joins")?;
            let second = task_b.await.expect("second publisher joins")?;
            Ok::<_, ProvisionError>((first, second))
        }).expect("at least one publisher succeeds");
        prop_assert_eq!(observed.0.as_slice(), payload.as_slice());
        if let Some(second) = observed.1 {
            prop_assert_eq!(second.as_slice(), payload.as_slice());
        }
        let cache = cache_for_test(
            root,
            descriptor_version(&descriptor),
            ArchiveFormat::TarGz,
        );
        let published = std::fs::read(cache.selected_path()).expect("published bytes");
        prop_assert_eq!(published.as_slice(), payload.as_slice());
        let entries = std::fs::read_dir(fixture.path())
            .expect("cache reads")
            .filter_map(Result::ok)
            .map(|entry| entry.file_name())
            .collect::<Vec<_>>();
        let entries_are_owned = entries.iter().all(|name| {
            name == ".rust-sdk-cli-cache.lock" || name == cache.selected_path().file_name().unwrap_or_default()
        });
        prop_assert!(entries_are_owned);
    }

    // Invariant: retention removes only obsolete managed regular files and failure is non-fatal.
    // Feature: rust-sdk-transport-observability, Property 12: retention is locked, confined, and non-destructive
    #[test]
    fn property_12_retention_locked_confined_non_destructive(
        unrelated_name in "unrelated-[a-z0-9]{1,12}",
        old_minor in 0_u64..100,
        fail_removal in any::<bool>(),
    ) {
        let descriptor = descriptor(ArchiveFormat::TarGz);
        let fixture = tempfile::tempdir().expect("cache fixture");
        let cache = cache_for_test(
            fixture.path().to_path_buf(),
            descriptor_version(&descriptor),
            ArchiveFormat::TarGz,
        );
        cache.prepare().expect("cache prepares");
        let selected = cache.selected_path();
        std::fs::write(&selected, b"selected").expect("selected writes");
        let old = fixture.path().join(format!("dagger-0.{old_minor}.0"));
        std::fs::write(&old, b"old").expect("old writes");
        let unrelated = fixture.path().join(unrelated_name);
        std::fs::write(&unrelated, b"unrelated").expect("unrelated writes");
        let managed_directory = fixture.path().join("dagger-9.9.9");
        std::fs::create_dir(&managed_directory).expect("managed directory writes");
        #[cfg(unix)]
        let managed_symlink = {
            use std::os::unix::fs::symlink;
            let path = fixture.path().join("dagger-8.8.8");
            symlink(&unrelated, &path).expect("managed symlink writes");
            path
        };

        let failures = cache.prune_with(&selected, |path| {
            if fail_removal {
                Err(std::io::Error::other("injected"))
            } else {
                std::fs::remove_file(path)
            }
        });
        prop_assert!(selected.exists());
        prop_assert!(unrelated.exists());
        prop_assert!(managed_directory.exists());
        #[cfg(unix)]
        prop_assert!(std::fs::symlink_metadata(&managed_symlink)
            .expect("managed symlink remains")
            .file_type()
            .is_symlink());
        prop_assert_eq!(old.exists(), fail_removal);
        prop_assert_eq!(failures, usize::from(fail_removal));
    }
}

#[test]
fn manifest_boundaries_and_http_statuses_are_typed_before_archive_access() {
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let oversized = vec![b'x'; MANIFEST_LIMIT as usize + 1];
    assert_eq!(
        parse_manifest(&oversized, descriptor.archive_name())
            .expect_err("oversized manifest")
            .kind(),
        ProvisionErrorKind::ManifestTooLarge
    );

    for status in [403, 404, 500] {
        let manifest = ResponseFixture::bytes(status, Vec::new(), 1);
        let archive = ResponseFixture::bytes(200, Vec::new(), 1);
        let http = FixtureHttp::new(manifest, archive);
        let probe = http.clone();
        let fixture = tempfile::tempdir().expect("cache fixture");
        let error = runtime()
            .block_on(
                DefaultCliProvisioner::with_cache_root(
                    http,
                    fixture.path().to_path_buf(),
                    NoopProvisioningObserver,
                )
                .acquire(&descriptor, &ProvisioningCancellation::new()),
            )
            .expect_err("manifest status rejects");
        let expected = if matches!(status, 403 | 404) {
            ProvisionErrorKind::ReleaseUnavailable
        } else {
            ProvisionErrorKind::ManifestStatus
        };
        assert_eq!(error.kind(), expected);
        assert_eq!(error.status(), Some(status));
        assert_eq!(probe.events(), vec![ProvisioningRequestKind::Manifest]);
    }
}

#[test]
fn production_http_rejects_non_release_authority_before_network_access() {
    let adapter = ReqwestProvisioningHttp::new().expect("production HTTP client builds");
    for url in [
        "http://dl.dagger.io/dagger/releases/1.0.0/checksums.txt",
        "https://example.com/dagger/releases/1.0.0/checksums.txt",
        "https://user@dl.dagger.io/dagger/releases/1.0.0/checksums.txt",
        "https://dl.dagger.io/other/checksums.txt",
    ] {
        let url = url::Url::parse(url).expect("fixture URL parses");
        let error = match runtime().block_on(adapter.get(
            &url,
            ProvisioningRequestKind::Manifest,
            &ProvisioningCancellation::new(),
        )) {
            Ok(_) => panic!("non-release authority was accepted"),
            Err(error) => error,
        };
        assert_eq!(error.kind(), ProvisionErrorKind::InvalidReleaseUrl);
    }
}

#[test]
fn declared_archive_size_is_rejected_before_body_polling() {
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let manifest = ResponseFixture::bytes(
        200,
        format!("{}  {}\n", "0".repeat(64), descriptor.archive_name()).into_bytes(),
        8,
    );
    let archive = ResponseFixture {
        status: 200,
        content_length: Some(crate::archive::ARCHIVE_LIMIT + 1),
        chunks: vec![Err(ProvisionError::new(ProvisionErrorKind::ArchiveRead))],
        yields: 0,
    };
    let http = FixtureHttp::new(manifest, archive);
    let fixture = tempfile::tempdir().expect("cache fixture");
    let error = runtime()
        .block_on(
            DefaultCliProvisioner::with_cache_root(
                http,
                fixture.path().to_path_buf(),
                NoopProvisioningObserver,
            )
            .acquire(&descriptor, &ProvisioningCancellation::new()),
        )
        .expect_err("declared oversized archive rejects");
    assert_eq!(error.kind(), ProvisionErrorKind::ArchiveTooLarge);
}

#[test]
fn checksum_mismatch_never_extracts_or_publishes() {
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let archive = archive_for(
        ArchiveFormat::TarGz,
        &[(
            descriptor.member_name().to_owned(),
            b"verified executable".to_vec(),
            FixtureEntryKind::File,
        )],
    );
    let manifest = format!("{}  {}\n", "0".repeat(64), descriptor.archive_name()).into_bytes();
    let http = FixtureHttp::new(
        ResponseFixture::bytes(200, manifest, 7),
        ResponseFixture::bytes(200, archive, 11),
    );
    let fixture = tempfile::tempdir().expect("cache fixture");
    let error = runtime()
        .block_on(
            DefaultCliProvisioner::with_cache_root(
                http,
                fixture.path().to_path_buf(),
                NoopProvisioningObserver,
            )
            .acquire(&descriptor, &ProvisioningCancellation::new()),
        )
        .expect_err("checksum mismatch rejects");
    assert_eq!(error.kind(), ProvisionErrorKind::ChecksumMismatch);
    assert!(error.expected_digest().is_some());
    assert!(error.actual_digest().is_some());
    let cache = cache_for_test(
        fixture.path().to_path_buf(),
        descriptor_version(&descriptor),
        ArchiveFormat::TarGz,
    );
    assert!(!cache.selected_path().exists());
}

#[cfg(unix)]
#[test]
fn symlink_swapped_after_lock_is_rejected_without_http() {
    use std::os::unix::fs::{PermissionsExt, symlink};

    #[derive(Clone)]
    struct SwapToSymlink {
        selected: PathBuf,
        target: PathBuf,
    }

    impl ProvisioningObserver for SwapToSymlink {
        fn checkpoint(&self, checkpoint: ProvisionCheckpoint) {
            if checkpoint == ProvisionCheckpoint::CacheLock {
                let _ = std::fs::remove_file(&self.selected);
                symlink(&self.target, &self.selected).expect("controlled symlink swap");
            }
        }
    }

    let descriptor = descriptor(ArchiveFormat::TarGz);
    let fixture = tempfile::tempdir().expect("cache fixture");
    let cache = cache_for_test(
        fixture.path().to_path_buf(),
        descriptor_version(&descriptor),
        ArchiveFormat::TarGz,
    );
    cache.prepare().expect("cache prepares");
    let selected = cache.selected_path();
    std::fs::write(&selected, b"initial").expect("initial cache file");
    std::fs::set_permissions(&selected, std::fs::Permissions::from_mode(0o700))
        .expect("initial permissions");
    let target = fixture.path().join("outside");
    std::fs::write(&target, b"outside").expect("symlink target");
    let empty = ResponseFixture::bytes(500, Vec::new(), 1);
    let http = FixtureHttp::new(empty.clone(), empty);
    let probe = http.clone();
    let provisioner = DefaultCliProvisioner::with_cache_root(
        http,
        fixture.path().to_path_buf(),
        SwapToSymlink { selected, target },
    );
    let error = runtime()
        .block_on(provisioner.acquire(&descriptor, &ProvisioningCancellation::new()))
        .expect_err("swapped symlink rejects");
    assert_eq!(error.kind(), ProvisionErrorKind::CacheEntrySymlink);
    assert!(probe.events().is_empty());
}

#[test]
fn retention_failure_keeps_the_newly_published_executable_usable() {
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let payload = b"published despite retention failure".to_vec();
    let archive = archive_for(
        ArchiveFormat::TarGz,
        &[(
            descriptor.member_name().to_owned(),
            payload.clone(),
            FixtureEntryKind::File,
        )],
    );
    let http = fixture_http(&descriptor, archive, 9);
    let fixture = tempfile::tempdir().expect("cache fixture");
    std::fs::write(fixture.path().join("dagger-0.0.1"), b"obsolete")
        .expect("obsolete entry writes");
    let calls = Arc::new(AtomicUsize::new(0));
    let remover = FailingRemover {
        calls: calls.clone(),
    };
    let executable = runtime()
        .block_on(
            DefaultCliProvisioner::with_cache_root_and_remover(
                http,
                fixture.path().to_path_buf(),
                NoopProvisioningObserver,
                remover,
            )
            .acquire(&descriptor, &ProvisioningCancellation::new()),
        )
        .expect("retention failure is non-fatal");
    assert_eq!(
        std::fs::read(executable.path()).expect("published CLI reads"),
        payload
    );
    assert_eq!(calls.load(Ordering::Relaxed), 1);
}

#[test]
fn extracted_output_limit_is_checked_before_crossing_the_boundary() {
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let payload = vec![0x5a; 65];
    let archive = archive_for(
        ArchiveFormat::TarGz,
        &[(
            descriptor.member_name().to_owned(),
            payload,
            FixtureEntryKind::File,
        )],
    );
    let mut output = Vec::new();
    let error = extract_expected(
        Cursor::new(archive),
        &mut output,
        ArchiveFormat::TarGz,
        descriptor.member_name(),
        64,
        &ProvisioningCancellation::new(),
        &NoopProvisioningObserver,
    )
    .expect_err("oversized executable rejects");
    assert_eq!(error.kind(), ProvisionErrorKind::ExecutableTooLarge);
    assert!(output.is_empty());
}

#[test]
fn absolute_tar_member_is_rejected_without_resolving_a_destination() {
    let encoder = GzEncoder::new(Vec::new(), Compression::default());
    let mut archive = TarBuilder::new(encoder);
    let payload = b"absolute member";
    let mut header = Header::new_gnu();
    header.set_mode(0o700);
    header.set_size(payload.len() as u64);
    header.set_entry_type(EntryType::Regular);
    header
        .set_path_absolute("/dagger")
        .expect("absolute fixture path is represented intentionally");
    header.set_cksum();
    archive
        .append(&header, payload.as_slice())
        .expect("absolute fixture entry writes");
    let encoder = archive.into_inner().expect("fixture tar finishes");
    let bytes = encoder.finish().expect("fixture gzip finishes");
    let mut output = Vec::new();
    let error = extract_expected(
        Cursor::new(bytes),
        &mut output,
        ArchiveFormat::TarGz,
        "dagger",
        1024,
        &ProvisioningCancellation::new(),
        &NoopProvisioningObserver,
    )
    .expect_err("absolute member rejects");
    assert_eq!(error.kind(), ProvisionErrorKind::UnsafeMember);
    assert!(output.is_empty());
}

#[test]
fn cache_execution_lease_holds_the_cross_process_lock() {
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let fixture = tempfile::tempdir().expect("cache fixture");
    let cache = cache_for_test(
        fixture.path().to_path_buf(),
        descriptor_version(&descriptor),
        ArchiveFormat::TarGz,
    );
    cache.prepare().expect("cache prepares");
    let selected = cache.selected_path();
    std::fs::write(&selected, b"leased").expect("cache entry writes");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&selected, std::fs::Permissions::from_mode(0o700))
            .expect("cache entry permissions");
    }
    let empty = ResponseFixture::bytes(500, Vec::new(), 1);
    let executable = runtime()
        .block_on(
            DefaultCliProvisioner::with_cache_root(
                FixtureHttp::new(empty.clone(), empty),
                fixture.path().to_path_buf(),
                NoopProvisioningObserver,
            )
            .acquire(&descriptor, &ProvisioningCancellation::new()),
        )
        .expect("cache hit leases executable");
    let contender = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(fixture.path().join(".rust-sdk-cli-cache.lock"))
        .expect("contender opens lock file");
    assert!(matches!(
        fs4::FileExt::try_lock(&contender),
        Err(fs4::TryLockError::WouldBlock)
    ));
    drop(executable);
    fs4::FileExt::try_lock(&contender).expect("dropping the lease releases the lock");
}

#[test]
fn managed_name_parser_rejects_unowned_lookalikes() {
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let fixture = tempfile::tempdir().expect("cache fixture");
    let cache = cache_for_test(
        fixture.path().to_path_buf(),
        Version::new(1, 2, 3),
        ArchiveFormat::TarGz,
    );
    cache.prepare().expect("cache prepares");
    let selected = cache.selected_path();
    std::fs::write(&selected, b"selected").expect("selected writes");
    for name in [
        "dagger-not-semver",
        "dagger-1.2.3.backup",
        "dagger_1.2.3",
        "unrelated",
    ] {
        std::fs::write(fixture.path().join(name), name).expect("lookalike writes");
    }
    assert_eq!(
        cache.prune_with(&selected, |path| std::fs::remove_file(path)),
        0
    );
    for name in [
        "dagger-not-semver",
        "dagger-1.2.3.backup",
        "dagger_1.2.3",
        "unrelated",
    ] {
        assert!(fixture.path().join(name).exists());
    }
    assert_eq!(descriptor.format(), ArchiveFormat::TarGz);
}

#[derive(Clone)]
struct ProcessPublicationBarrier {
    address: Arc<str>,
    entered: Arc<AtomicBool>,
}

impl ProvisioningObserver for ProcessPublicationBarrier {
    fn checkpoint(&self, checkpoint: ProvisionCheckpoint) {
        if checkpoint != ProvisionCheckpoint::ManifestRequest
            || self.entered.swap(true, Ordering::AcqRel)
        {
            return;
        }
        let mut stream = TcpStream::connect(self.address.as_ref())
            .expect("the parent publication barrier accepts the fixture");
        stream
            .write_all(&[1])
            .expect("the fixture announces its empty-cache observation");
        let mut release = [0_u8; 1];
        stream
            .read_exact(&mut release)
            .expect("the parent releases both first publishers");
    }
}

#[test]
#[ignore = "invoked by cross_process_first_publication_is_atomic"]
fn cache_publication_process_fixture() {
    let Some(cache_root) = std::env::var_os("DAGGER_RUST_CACHE_FIXTURE") else {
        return;
    };
    let barrier = std::env::var("DAGGER_RUST_CACHE_BARRIER")
        .expect("the helper receives a publication barrier");
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let payload = b"cross-process verified CLI".to_vec();
    let archive = archive_for(
        ArchiveFormat::TarGz,
        &[(
            descriptor.member_name().to_owned(),
            payload.clone(),
            FixtureEntryKind::File,
        )],
    );
    let http = fixture_http(&descriptor, archive, 3);
    let observer = ProcessPublicationBarrier {
        address: Arc::from(barrier),
        entered: Arc::new(AtomicBool::new(false)),
    };
    let executable = runtime()
        .block_on(
            DefaultCliProvisioner::with_cache_root(http, PathBuf::from(cache_root), observer)
                .acquire(&descriptor, &ProvisioningCancellation::new()),
        )
        .expect("the process publisher converges");
    assert_eq!(
        std::fs::read(executable.path()).expect("the process reads its leased executable"),
        payload
    );
}

#[test]
fn cross_process_first_publication_is_atomic() {
    let fixture = tempfile::tempdir().expect("cache fixture");
    let listener = TcpListener::bind("127.0.0.1:0").expect("publication barrier binds");
    let address = listener
        .local_addr()
        .expect("publication barrier address")
        .to_string();
    let executable = std::env::current_exe().expect("current test executable");
    let spawn = || {
        Command::new(&executable)
            .args([
                "--ignored",
                "--exact",
                "provisioning_tests::cache_publication_process_fixture",
                "--nocapture",
            ])
            .env("DAGGER_RUST_CACHE_FIXTURE", fixture.path())
            .env("DAGGER_RUST_CACHE_BARRIER", &address)
            .spawn()
            .expect("publisher fixture starts")
    };
    let first = spawn();
    let second = spawn();

    let mut waiting = Vec::new();
    for _ in 0..2 {
        let (mut stream, _) = listener.accept().expect("publisher reaches barrier");
        let mut ready = [0_u8; 1];
        stream
            .read_exact(&mut ready)
            .expect("publisher reports empty cache");
        waiting.push(stream);
    }
    for stream in &mut waiting {
        stream.write_all(&[1]).expect("publisher leaves barrier");
    }

    for mut child in [first, second] {
        assert!(child.wait().expect("publisher fixture exits").success());
    }
    let descriptor = descriptor(ArchiveFormat::TarGz);
    let cache = cache_for_test(
        fixture.path().to_path_buf(),
        descriptor_version(&descriptor),
        ArchiveFormat::TarGz,
    );
    assert_eq!(
        std::fs::read(cache.selected_path()).expect("one complete cache result"),
        b"cross-process verified CLI"
    );
}
