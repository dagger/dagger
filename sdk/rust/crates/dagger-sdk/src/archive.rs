//! Bounded manifest parsing and path-confined Dagger CLI archive extraction.
//!
//! Archive member names are inspected only as data: they are never joined to a cache
//! path. Exactly one regular member with the descriptor-selected basename may write to
//! the already-open private output file, and each write is checked before it can cross
//! the output bound.

use std::io::{Read, Seek, Write};

use flate2::read::GzDecoder;
use tar::Archive;
use zip::ZipArchive;

use crate::provisioning_control::{
    ProvisionCheckpoint, ProvisioningCancellation, ProvisioningObserver, checkpoint,
};
use crate::provisioning_error::{ProvisionError, ProvisionErrorKind};
use crate::target::ArchiveFormat;

pub(crate) const MANIFEST_LIMIT: u64 = 8 * 1024 * 1024;
pub(crate) const ARCHIVE_LIMIT: u64 = 1024 * 1024 * 1024;
pub(crate) const EXECUTABLE_LIMIT: u64 = 1024 * 1024 * 1024;

/// The one validated digest selected from a release manifest.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct ExpectedArchive {
    sha256: [u8; 32],
}

impl ExpectedArchive {
    pub(crate) const fn new(sha256: [u8; 32]) -> Self {
        Self { sha256 }
    }

    pub(crate) const fn sha256(&self) -> &[u8; 32] {
        &self.sha256
    }
}

/// Parses one bounded release manifest without allocating per-line state.
pub(crate) fn parse_manifest(
    bytes: &[u8],
    expected_archive_name: &str,
) -> Result<ExpectedArchive, ProvisionError> {
    if bytes.len() as u64 > MANIFEST_LIMIT {
        return Err(ProvisionError::new(ProvisionErrorKind::ManifestTooLarge));
    }
    let text = std::str::from_utf8(bytes)
        .map_err(|_| ProvisionError::new(ProvisionErrorKind::ManifestEncoding))?;
    let mut selected = None;

    for line in text.lines() {
        let mut fields = line.split_whitespace();
        let digest = fields
            .next()
            .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::ManifestSyntax))?;
        let archive_name = fields
            .next()
            .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::ManifestSyntax))?;
        if fields.next().is_some() {
            return Err(ProvisionError::new(ProvisionErrorKind::ManifestSyntax));
        }
        if archive_name != expected_archive_name {
            continue;
        }
        let digest = decode_sha256(digest)?;
        if selected.replace(digest).is_some() {
            return Err(ProvisionError::new(ProvisionErrorKind::AmbiguousChecksum));
        }
    }

    selected
        .map(ExpectedArchive::new)
        .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::MissingChecksum))
}

fn decode_sha256(value: &str) -> Result<[u8; 32], ProvisionError> {
    if value.len() != 64 {
        return Err(ProvisionError::new(ProvisionErrorKind::InvalidChecksum));
    }
    let mut digest = [0_u8; 32];
    for (index, pair) in value.as_bytes().chunks_exact(2).enumerate() {
        let high = decode_hex(pair[0])
            .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::InvalidChecksum))?;
        let low = decode_hex(pair[1])
            .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::InvalidChecksum))?;
        digest[index] = (high << 4) | low;
    }
    Ok(digest)
}

fn decode_hex(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

/// Extracts the one expected executable into an already-confined private writer.
pub(crate) fn extract_expected<R, W, O>(
    archive: R,
    output: &mut W,
    format: ArchiveFormat,
    expected_member: &str,
    output_limit: u64,
    cancellation: &ProvisioningCancellation,
    observer: &O,
) -> Result<u64, ProvisionError>
where
    R: Read + Seek,
    W: Write,
    O: ProvisioningObserver,
{
    match format {
        ArchiveFormat::TarGz => extract_tar_gz(
            archive,
            output,
            expected_member,
            output_limit,
            cancellation,
            observer,
        ),
        ArchiveFormat::Zip => extract_zip(
            archive,
            output,
            expected_member,
            output_limit,
            cancellation,
            observer,
        ),
    }
}

fn extract_tar_gz<R, W, O>(
    archive: R,
    output: &mut W,
    expected_member: &str,
    output_limit: u64,
    cancellation: &ProvisioningCancellation,
    observer: &O,
) -> Result<u64, ProvisionError>
where
    R: Read,
    W: Write,
    O: ProvisioningObserver,
{
    let decoder = GzDecoder::new(archive);
    let mut archive = Archive::new(decoder);
    let entries = archive
        .entries()
        .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::ArchiveFormat, error))?;
    let mut found = false;
    let mut written = 0;

    for entry in entries {
        cancellation.check()?;
        let mut entry = entry.map_err(|error| {
            ProvisionError::with_source(ProvisionErrorKind::ArchiveFormat, error)
        })?;
        let path = entry.path_bytes();
        if !member_path_is_safe(&path) {
            return Err(ProvisionError::new(ProvisionErrorKind::UnsafeMember));
        }
        let entry_type = entry.header().entry_type();
        if !(entry_type.is_file() || entry_type.is_dir()) {
            return Err(ProvisionError::new(ProvisionErrorKind::UnsafeMember));
        }
        if member_basename(&path) != expected_member.as_bytes() {
            continue;
        }
        if !entry_type.is_file() {
            return Err(ProvisionError::new(ProvisionErrorKind::UnsafeMember));
        }
        if found {
            return Err(ProvisionError::new(ProvisionErrorKind::AmbiguousMember));
        }
        written = copy_bounded(&mut entry, output, output_limit, cancellation, observer)?;
        found = true;
    }

    if found {
        Ok(written)
    } else {
        Err(ProvisionError::new(ProvisionErrorKind::MissingMember))
    }
}

fn extract_zip<R, W, O>(
    archive: R,
    output: &mut W,
    expected_member: &str,
    output_limit: u64,
    cancellation: &ProvisioningCancellation,
    observer: &O,
) -> Result<u64, ProvisionError>
where
    R: Read + Seek,
    W: Write,
    O: ProvisioningObserver,
{
    let mut archive = ZipArchive::new(archive)
        .map_err(|error| ProvisionError::with_source(ProvisionErrorKind::ArchiveFormat, error))?;
    let mut found = false;
    let mut written = 0;

    for index in 0..archive.len() {
        cancellation.check()?;
        let mut entry = archive.by_index(index).map_err(|error| {
            ProvisionError::with_source(ProvisionErrorKind::ArchiveFormat, error)
        })?;
        let path = entry.name_raw();
        if !member_path_is_safe(path) {
            return Err(ProvisionError::new(ProvisionErrorKind::UnsafeMember));
        }
        if entry.is_symlink() || !(entry.is_file() || entry.is_dir()) {
            return Err(ProvisionError::new(ProvisionErrorKind::UnsafeMember));
        }
        if member_basename(path) != expected_member.as_bytes() {
            continue;
        }
        if !entry.is_file() {
            return Err(ProvisionError::new(ProvisionErrorKind::UnsafeMember));
        }
        if found {
            return Err(ProvisionError::new(ProvisionErrorKind::AmbiguousMember));
        }
        written = copy_bounded(&mut entry, output, output_limit, cancellation, observer)?;
        found = true;
    }

    if found {
        Ok(written)
    } else {
        Err(ProvisionError::new(ProvisionErrorKind::MissingMember))
    }
}

fn copy_bounded<R, W, O>(
    reader: &mut R,
    writer: &mut W,
    limit: u64,
    cancellation: &ProvisioningCancellation,
    observer: &O,
) -> Result<u64, ProvisionError>
where
    R: Read,
    W: Write,
    O: ProvisioningObserver,
{
    let mut observed = 0_u64;
    let mut buffer = [0_u8; 64 * 1024];
    loop {
        checkpoint(observer, cancellation, ProvisionCheckpoint::ExtractRead)?;
        let count = reader.read(&mut buffer).map_err(|error| {
            ProvisionError::with_source(ProvisionErrorKind::ArchiveFormat, error)
        })?;
        if count == 0 {
            return Ok(observed);
        }
        let next = observed
            .checked_add(count as u64)
            .ok_or_else(|| ProvisionError::new(ProvisionErrorKind::ExecutableTooLarge))?;
        if next > limit {
            return Err(ProvisionError::new(ProvisionErrorKind::ExecutableTooLarge));
        }
        writer.write_all(&buffer[..count]).map_err(|error| {
            ProvisionError::with_source(ProvisionErrorKind::CachePublication, error)
        })?;
        observed = next;
    }
}

fn member_path_is_safe(path: &[u8]) -> bool {
    if path.is_empty() || path.contains(&0) || matches!(path.first(), Some(b'/') | Some(b'\\')) {
        return false;
    }
    if path.len() >= 3
        && path[0].is_ascii_alphabetic()
        && path[1] == b':'
        && matches!(path[2], b'/' | b'\\')
    {
        return false;
    }
    !path
        .split(|byte| matches!(byte, b'/' | b'\\'))
        .any(|component| component == b"..")
}

fn member_basename(path: &[u8]) -> &[u8] {
    path.rsplit(|byte| matches!(byte, b'/' | b'\\'))
        .next()
        .unwrap_or(path)
}
