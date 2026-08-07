//! Verified Dagger CLI acquisition and native cache publication.
//!
//! A valid cache hit is returned under an execution lease without network access.
//! Misses download outside the cross-process lock, hash every compressed byte, extract
//! one bounded member into private state, then revalidate and atomically publish while
//! locked. The lock remains leased through process spawn so retention cannot reopen the
//! validation-to-execution race.

use std::fs::{self, File, OpenOptions};
use std::future::Future;
use std::io::{Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};
use std::pin::Pin;

use fs4::FileExt;
use futures::{Stream, StreamExt};
use semver::Version;
use sha2::{Digest, Sha256};
use tempfile::NamedTempFile;
use url::Url;

use crate::archive::{
    ARCHIVE_LIMIT, EXECUTABLE_LIMIT, ExpectedArchive, MANIFEST_LIMIT, extract_expected,
    parse_manifest,
};
use crate::discovery::LaunchExecutable;
use crate::provisioning_control::{
    NoopProvisioningObserver, ProvisionCheckpoint, ProvisioningCancellation, ProvisioningObserver,
    checkpoint,
};
use crate::provisioning_error::{ProvisionError, ProvisionErrorKind};
use crate::target::{ArchiveDescriptor, ArchiveFormat};

const RELEASE_HOST: &str = "dl.dagger.io";
const CACHE_DIRECTORY: &str = "dagger";
const CACHE_LOCK_NAME: &str = ".rust-sdk-cli-cache.lock";
const MANAGED_PREFIX: &str = "dagger-";

pub(crate) type DownloadChunks =
    Pin<Box<dyn Stream<Item = Result<Vec<u8>, ProvisionError>> + Send>>;

/// One bounded-response source returned by a private provisioning adapter.
pub(crate) struct DownloadResponse {
    status: u16,
    content_length: Option<u64>,
    body: DownloadChunks,
}

impl DownloadResponse {
    pub(crate) fn new(status: u16, content_length: Option<u64>, body: DownloadChunks) -> Self {
        Self {
            status,
            content_length,
            body,
        }
    }
}

/// Selects the safe error family for a fixed provisioning request.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ProvisioningRequestKind {
    Manifest,
    Archive,
}

/// Instance-local HTTP boundary used by the compiled-release provisioner.
pub(crate) trait ProvisioningHttp: Send + Sync {
    fn get(
        &self,
        url: &Url,
        kind: ProvisioningRequestKind,
        cancellation: &ProvisioningCancellation,
    ) -> impl Future<Output = Result<DownloadResponse, ProvisionError>> + Send;
}

/// Production HTTPS adapter with redirects disabled at client construction.
pub(crate) struct ReqwestProvisioningHttp {
    client: reqwest::Client,
}

impl ReqwestProvisioningHttp {
    pub(crate) fn new() -> Result<Self, ProvisionError> {
        let client = reqwest::Client::builder()
            .https_only(true)
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|error| {
                ProvisionError::with_source(ProvisionErrorKind::ManifestTransport, error)
            })?;
        Ok(Self { client })
    }
}

impl ProvisioningHttp for ReqwestProvisioningHttp {
    async fn get(
        &self,
        url: &Url,
        kind: ProvisioningRequestKind,
        cancellation: &ProvisioningCancellation,
    ) -> Result<DownloadResponse, ProvisionError> {
        validate_release_url(url)?;
        let send = self.client.get(url.clone()).send();
        tokio::pin!(send);
        let response = tokio::select! {
            result = &mut send => result.map_err(|error| {
                ProvisionError::with_source(transport_kind(kind), error)
            })?,
            () = cancellation.cancelled() => {
                return Err(ProvisionError::new(ProvisionErrorKind::Cancelled));
            }
        };
        let status = response.status().as_u16();
        let content_length = response.content_length();
        let body = response.bytes_stream().map(move |chunk| {
            chunk
                .map(|bytes| bytes.to_vec())
                .map_err(|error| ProvisionError::with_source(read_kind(kind), error))
        });
        Ok(DownloadResponse::new(
            status,
            content_length,
            Box::pin(body),
        ))
    }
}

fn validate_release_url(url: &Url) -> Result<(), ProvisionError> {
    if url.scheme() == "https"
        && url.host_str() == Some(RELEASE_HOST)
        && url.port().is_none()
        && url.path().starts_with("/dagger/releases/")
        && url.username().is_empty()
        && url.password().is_none()
    {
        Ok(())
    } else {
        Err(ProvisionError::new(ProvisionErrorKind::InvalidReleaseUrl))
    }
}

const fn transport_kind(kind: ProvisioningRequestKind) -> ProvisionErrorKind {
    match kind {
        ProvisioningRequestKind::Manifest => ProvisionErrorKind::ManifestTransport,
        ProvisioningRequestKind::Archive => ProvisionErrorKind::ArchiveTransport,
    }
}

const fn read_kind(kind: ProvisioningRequestKind) -> ProvisionErrorKind {
    match kind {
        ProvisioningRequestKind::Manifest => ProvisionErrorKind::ManifestRead,
        ProvisioningRequestKind::Archive => ProvisionErrorKind::ArchiveRead,
    }
}

/// A statically dispatched provisioner with one cache root and observation policy.
pub(crate) struct DefaultCliProvisioner<H, O = NoopProvisioningObserver, R = SystemRetentionRemover>
{
    http: H,
    cache_root: PathBuf,
    observer: O,
    retention_remover: R,
}

impl<H> DefaultCliProvisioner<H, NoopProvisioningObserver> {
    pub(crate) fn native(http: H) -> Result<Self, ProvisionError> {
        let cache_root = dirs::cache_dir()
            .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::CacheDirectory))?
            .join(CACHE_DIRECTORY);
        Ok(Self {
            http,
            cache_root,
            observer: NoopProvisioningObserver,
            retention_remover: SystemRetentionRemover,
        })
    }
}

impl<H, O> DefaultCliProvisioner<H, O, SystemRetentionRemover> {
    pub(crate) fn with_cache_root(http: H, cache_root: PathBuf, observer: O) -> Self {
        Self {
            http,
            cache_root,
            observer,
            retention_remover: SystemRetentionRemover,
        }
    }
}

impl<H, O, R> DefaultCliProvisioner<H, O, R> {
    #[cfg(test)]
    pub(crate) fn with_cache_root_and_remover(
        http: H,
        cache_root: PathBuf,
        observer: O,
        retention_remover: R,
    ) -> Self {
        Self {
            http,
            cache_root,
            observer,
            retention_remover,
        }
    }
}

impl<H, O, R> DefaultCliProvisioner<H, O, R>
where
    H: ProvisioningHttp,
    O: ProvisioningObserver,
    R: RetentionRemover,
{
    pub(crate) async fn acquire(
        &self,
        descriptor: &ArchiveDescriptor,
        cancellation: &ProvisioningCancellation,
    ) -> Result<LaunchExecutable, ProvisionError> {
        cancellation.check()?;
        let cli_version = descriptor
            .cli_version()
            .map_err(|_| ProvisionError::new(ProvisionErrorKind::InvalidReleaseUrl))?;
        let cache = CliCache::new(self.cache_root.clone(), cli_version, descriptor.format());
        cache.prepare()?;

        if let Some(executable) = cache.lease_existing(cancellation, &self.observer).await? {
            return Ok(executable);
        }

        let expected = self.acquire_manifest(descriptor, cancellation).await?;
        let mut archive = NamedTempFile::new_in(&self.cache_root)
            .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::ArchiveRead, error))?;
        let actual = self
            .acquire_archive(descriptor, &mut archive, cancellation)
            .await?;
        if actual != *expected.sha256() {
            return Err(ProvisionError::checksum_mismatch(
                *expected.sha256(),
                actual,
            ));
        }
        checkpoint(
            &self.observer,
            cancellation,
            ProvisionCheckpoint::ChecksumAccepted,
        )?;

        archive
            .as_file_mut()
            .seek(SeekFrom::Start(0))
            .map_err(|error| {
                ProvisionError::with_source(ProvisionErrorKind::ArchiveFormat, error)
            })?;
        let mut executable = NamedTempFile::new_in(&self.cache_root).map_err(|error| {
            ProvisionError::with_source(ProvisionErrorKind::CachePublication, error)
        })?;
        extract_expected(
            archive.as_file_mut(),
            executable.as_file_mut(),
            descriptor.format(),
            descriptor.member_name(),
            EXECUTABLE_LIMIT,
            cancellation,
            &self.observer,
        )?;
        checkpoint(&self.observer, cancellation, ProvisionCheckpoint::Extracted)?;

        cache
            .publish(
                executable,
                cancellation,
                &self.observer,
                &self.retention_remover,
            )
            .await
    }

    async fn acquire_manifest(
        &self,
        descriptor: &ArchiveDescriptor,
        cancellation: &ProvisioningCancellation,
    ) -> Result<ExpectedArchive, ProvisionError> {
        checkpoint(
            &self.observer,
            cancellation,
            ProvisionCheckpoint::ManifestRequest,
        )?;
        let response = self
            .http
            .get(
                descriptor.manifest_url(),
                ProvisioningRequestKind::Manifest,
                cancellation,
            )
            .await?;
        match response.status {
            200 => {}
            403 | 404 => {
                return Err(ProvisionError::with_status(
                    ProvisionErrorKind::ReleaseUnavailable,
                    response.status,
                ));
            }
            status => {
                return Err(ProvisionError::with_status(
                    ProvisionErrorKind::ManifestStatus,
                    status,
                ));
            }
        }
        let bytes = read_response(
            response,
            MANIFEST_LIMIT,
            ProvisionErrorKind::ManifestTooLarge,
            ProvisionCheckpoint::ManifestRead,
            cancellation,
            &self.observer,
        )
        .await?;
        parse_manifest(&bytes, descriptor.archive_name())
    }

    async fn acquire_archive(
        &self,
        descriptor: &ArchiveDescriptor,
        destination: &mut NamedTempFile,
        cancellation: &ProvisioningCancellation,
    ) -> Result<[u8; 32], ProvisionError> {
        checkpoint(
            &self.observer,
            cancellation,
            ProvisionCheckpoint::ArchiveRequest,
        )?;
        let response = self
            .http
            .get(
                descriptor.archive_url(),
                ProvisioningRequestKind::Archive,
                cancellation,
            )
            .await?;
        if response.status != 200 {
            return Err(ProvisionError::with_status(
                ProvisionErrorKind::ArchiveStatus,
                response.status,
            ));
        }
        if response
            .content_length
            .is_some_and(|size| size > ARCHIVE_LIMIT)
        {
            return Err(ProvisionError::new(ProvisionErrorKind::ArchiveTooLarge));
        }

        let mut body = response.body;
        let mut observed = 0_u64;
        let mut hasher = Sha256::new();
        loop {
            checkpoint(
                &self.observer,
                cancellation,
                ProvisionCheckpoint::ArchiveRead,
            )?;
            let chunk = tokio::select! {
                chunk = body.next() => chunk,
                () = cancellation.cancelled() => {
                    return Err(ProvisionError::new(ProvisionErrorKind::Cancelled));
                }
            };
            let Some(chunk) = chunk else {
                break;
            };
            let chunk = chunk?;
            let next = observed
                .checked_add(chunk.len() as u64)
                .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::ArchiveTooLarge))?;
            if next > ARCHIVE_LIMIT {
                return Err(ProvisionError::new(ProvisionErrorKind::ArchiveTooLarge));
            }
            hasher.update(&chunk);
            destination
                .as_file_mut()
                .write_all(&chunk)
                .map_err(|error| {
                    ProvisionError::with_source(ProvisionErrorKind::ArchiveRead, error)
                })?;
            observed = next;
        }
        destination
            .as_file_mut()
            .flush()
            .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::ArchiveRead, error))?;
        Ok(hasher.finalize().into())
    }
}

async fn read_response<O: ProvisioningObserver>(
    response: DownloadResponse,
    limit: u64,
    too_large: ProvisionErrorKind,
    read_checkpoint: ProvisionCheckpoint,
    cancellation: &ProvisioningCancellation,
    observer: &O,
) -> Result<Vec<u8>, ProvisionError> {
    if response.content_length.is_some_and(|size| size > limit) {
        return Err(ProvisionError::new(too_large));
    }
    let capacity = response
        .content_length
        .unwrap_or(0)
        .min(limit)
        .try_into()
        .unwrap_or(0);
    let mut bytes = Vec::with_capacity(capacity);
    let mut body = response.body;
    loop {
        checkpoint(observer, cancellation, read_checkpoint)?;
        let chunk = tokio::select! {
            chunk = body.next() => chunk,
            () = cancellation.cancelled() => {
                return Err(ProvisionError::new(ProvisionErrorKind::Cancelled));
            }
        };
        let Some(chunk) = chunk else {
            return Ok(bytes);
        };
        let chunk = chunk?;
        let next = bytes
            .len()
            .checked_add(chunk.len())
            .ok_or_else(|| ProvisionError::new(too_large))?;
        if next as u64 > limit {
            return Err(ProvisionError::new(too_large));
        }
        bytes.extend_from_slice(&chunk);
    }
}

struct CliCache {
    root: PathBuf,
    selected_name: String,
}

impl CliCache {
    fn new(root: PathBuf, version: Version, format: ArchiveFormat) -> Self {
        let suffix = if format == ArchiveFormat::Zip {
            ".exe"
        } else {
            ""
        };
        Self {
            root,
            selected_name: format!("{MANAGED_PREFIX}{version}{suffix}"),
        }
    }

    fn prepare(&self) -> Result<(), ProvisionError> {
        fs::create_dir_all(&self.root).map_err(|error| {
            ProvisionError::with_source(ProvisionErrorKind::CacheDirectory, error)
        })?;
        set_directory_permissions(&self.root)?;
        Ok(())
    }

    fn selected_path(&self) -> PathBuf {
        self.root.join(&self.selected_name)
    }

    async fn lease_existing<O: ProvisioningObserver>(
        &self,
        cancellation: &ProvisioningCancellation,
        observer: &O,
    ) -> Result<Option<LaunchExecutable>, ProvisionError> {
        let lock = self.acquire_lock(cancellation, observer).await?;
        match validate_cache_entry(&self.selected_path())? {
            CacheEntryState::Absent => Ok(None),
            CacheEntryState::Accepted => {
                Ok(Some(LaunchExecutable::cached(self.selected_path(), lock)))
            }
        }
    }

    async fn publish<O, R>(
        &self,
        mut temporary: NamedTempFile,
        cancellation: &ProvisioningCancellation,
        observer: &O,
        retention_remover: &R,
    ) -> Result<LaunchExecutable, ProvisionError>
    where
        O: ProvisioningObserver,
        R: RetentionRemover,
    {
        let lock = self.acquire_lock(cancellation, observer).await?;
        if validate_cache_entry(&self.selected_path())? == CacheEntryState::Accepted {
            return Ok(LaunchExecutable::cached(self.selected_path(), lock));
        }

        checkpoint(observer, cancellation, ProvisionCheckpoint::Flush)?;
        set_executable_permissions(temporary.as_file())?;
        temporary
            .as_file_mut()
            .flush()
            .map_err(|_| ProvisionError::new(ProvisionErrorKind::CachePublication))?;
        temporary
            .as_file()
            .sync_all()
            .map_err(|_| ProvisionError::new(ProvisionErrorKind::CachePublication))?;
        checkpoint(observer, cancellation, ProvisionCheckpoint::Publication)?;

        let final_path = self.selected_path();
        // Close the writable handle before rename so no successful publisher can
        // mutate bytes after the final path becomes observable.
        temporary
            .into_temp_path()
            .persist(&final_path)
            .map_err(|_| ProvisionError::new(ProvisionErrorKind::CachePublication))?;
        if validate_cache_entry(&final_path)? != CacheEntryState::Accepted {
            return Err(ProvisionError::new(ProvisionErrorKind::CachePublication));
        }
        self.prune_managed(&final_path, retention_remover);
        Ok(LaunchExecutable::cached(final_path, lock))
    }

    async fn acquire_lock<O: ProvisioningObserver>(
        &self,
        cancellation: &ProvisioningCancellation,
        observer: &O,
    ) -> Result<File, ProvisionError> {
        cancellation.check()?;
        let path = self.root.join(CACHE_LOCK_NAME);
        if fs::symlink_metadata(&path).is_ok_and(|metadata| metadata.file_type().is_symlink()) {
            return Err(ProvisionError::new(ProvisionErrorKind::CacheLock));
        }
        let file = OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .open(path)
            .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::CacheLock, error))?;
        set_lock_permissions(&file)?;

        let mut task = tokio::task::spawn_blocking(move || {
            FileExt::lock(&file)
                .map(|()| file)
                .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::CacheLock, error))
        });
        let lock = tokio::select! {
            result = &mut task => result
                .map_err(|_| ProvisionError::new(ProvisionErrorKind::CacheLock))??,
            () = cancellation.cancelled() => {
                drop(task);
                return Err(ProvisionError::new(ProvisionErrorKind::Cancelled));
            }
        };
        checkpoint(observer, cancellation, ProvisionCheckpoint::CacheLock)?;
        Ok(lock)
    }

    fn prune_managed<R: RetentionRemover>(&self, selected: &Path, remover: &R) {
        let result = self.prune_managed_with(selected, |path| remover.remove(path));
        if result != 0 {
            tracing::warn!(
                failure_count = result,
                "Dagger CLI cache retention could not remove every obsolete managed entry"
            );
        }
    }

    fn prune_managed_with<F>(&self, selected: &Path, mut remove: F) -> usize
    where
        F: FnMut(&Path) -> std::io::Result<()>,
    {
        let entries = match fs::read_dir(&self.root) {
            Ok(entries) => entries,
            Err(_) => return 1,
        };
        let mut failures = 0;
        for entry in entries {
            let entry = match entry {
                Ok(entry) => entry,
                Err(_) => {
                    failures += 1;
                    continue;
                }
            };
            let path = entry.path();
            if path == selected || !is_managed_name(&entry.file_name()) {
                continue;
            }
            let Ok(metadata) = fs::symlink_metadata(&path) else {
                failures += 1;
                continue;
            };
            // Retention deliberately ignores links and non-files; deleting either could
            // cross an ownership boundary that a name prefix alone cannot establish.
            if metadata.file_type().is_symlink() || !metadata.is_file() {
                continue;
            }
            if remove(&path).is_err() {
                failures += 1;
            }
        }
        failures
    }
}

/// Instance-local removal seam used only for best-effort managed retention.
pub(crate) trait RetentionRemover: Send + Sync {
    fn remove(&self, path: &Path) -> std::io::Result<()>;
}

/// Production remover for recognized obsolete regular cache entries.
#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct SystemRetentionRemover;

impl RetentionRemover for SystemRetentionRemover {
    fn remove(&self, path: &Path) -> std::io::Result<()> {
        fs::remove_file(path)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum CacheEntryState {
    Absent,
    Accepted,
}

fn validate_cache_entry(path: &Path) -> Result<CacheEntryState, ProvisionError> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(CacheEntryState::Absent);
        }
        Err(error) => {
            return Err(ProvisionError::with_source(
                ProvisionErrorKind::CacheDirectory,
                error,
            ));
        }
    };
    if metadata.file_type().is_symlink() {
        return Err(ProvisionError::new(ProvisionErrorKind::CacheEntrySymlink));
    }
    if !metadata.is_file() {
        return Err(ProvisionError::new(
            ProvisionErrorKind::CacheEntryNotRegular,
        ));
    }
    if !permissions_are_private_executable(&metadata) {
        return Err(ProvisionError::new(
            ProvisionErrorKind::CacheEntryPermissions,
        ));
    }
    Ok(CacheEntryState::Accepted)
}

fn is_managed_name(name: &std::ffi::OsStr) -> bool {
    let Some(name) = name.to_str() else {
        return false;
    };
    let Some(version) = name.strip_prefix(MANAGED_PREFIX) else {
        return false;
    };
    let version = version.strip_suffix(".exe").unwrap_or(version);
    Version::parse(version).is_ok()
}

#[cfg(unix)]
fn set_directory_permissions(path: &Path) -> Result<(), ProvisionError> {
    use std::os::unix::fs::PermissionsExt;

    fs::set_permissions(path, fs::Permissions::from_mode(0o700))
        .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::CacheDirectory, error))
}

#[cfg(not(unix))]
fn set_directory_permissions(_path: &Path) -> Result<(), ProvisionError> {
    Ok(())
}

#[cfg(unix)]
fn set_lock_permissions(file: &File) -> Result<(), ProvisionError> {
    use std::os::unix::fs::PermissionsExt;

    file.set_permissions(fs::Permissions::from_mode(0o600))
        .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::CacheLock, error))
}

#[cfg(not(unix))]
fn set_lock_permissions(_file: &File) -> Result<(), ProvisionError> {
    Ok(())
}

#[cfg(unix)]
fn set_executable_permissions(file: &File) -> Result<(), ProvisionError> {
    use std::os::unix::fs::PermissionsExt;

    file.set_permissions(fs::Permissions::from_mode(0o700))
        .map_err(|_| ProvisionError::new(ProvisionErrorKind::CachePublication))
}

#[cfg(not(unix))]
fn set_executable_permissions(_file: &File) -> Result<(), ProvisionError> {
    Ok(())
}

#[cfg(unix)]
fn permissions_are_private_executable(metadata: &fs::Metadata) -> bool {
    use std::os::unix::fs::PermissionsExt;

    metadata.permissions().mode() & 0o777 == 0o700
}

#[cfg(not(unix))]
fn permissions_are_private_executable(_metadata: &fs::Metadata) -> bool {
    true
}

#[cfg(test)]
pub(crate) fn cache_for_test(
    root: PathBuf,
    version: Version,
    format: ArchiveFormat,
) -> TestCliCache {
    TestCliCache(CliCache::new(root, version, format))
}

#[cfg(test)]
pub(crate) struct TestCliCache(CliCache);

#[cfg(test)]
impl TestCliCache {
    pub(crate) fn prepare(&self) -> Result<(), ProvisionError> {
        self.0.prepare()
    }

    pub(crate) fn selected_path(&self) -> PathBuf {
        self.0.selected_path()
    }

    pub(crate) fn validate(&self) -> Result<bool, ProvisionError> {
        validate_cache_entry(&self.0.selected_path())
            .map(|state| state == CacheEntryState::Accepted)
    }

    pub(crate) fn prune_with<F>(&self, selected: &Path, remove: F) -> usize
    where
        F: FnMut(&Path) -> std::io::Result<()>,
    {
        self.0.prune_managed_with(selected, remove)
    }
}
