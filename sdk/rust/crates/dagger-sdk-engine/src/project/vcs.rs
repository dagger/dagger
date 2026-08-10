//! Narrow line-preserving VCS policy amendments.
//!
//! Generated and ignored entries are appended only when absent. Existing bytes,
//! comments, line endings, and unrelated rules remain byte-for-byte unchanged.

use std::collections::BTreeSet;

use crate::RelativeOperationPath;

/// Adds missing literal entries while preserving every existing byte.
#[must_use]
pub fn append_missing_lines(current: &[u8], entries: &BTreeSet<String>) -> Vec<u8> {
    let existing = current
        .split(|byte| *byte == b'\n')
        .map(|line| line.strip_suffix(b"\r").unwrap_or(line))
        .collect::<BTreeSet<_>>();
    let mut result = current.to_vec();
    for entry in entries {
        if existing.contains(entry.as_bytes()) {
            continue;
        }
        if !result.is_empty() && !result.ends_with(b"\n") {
            result.push(b'\n');
        }
        result.extend_from_slice(entry.as_bytes());
        result.push(b'\n');
    }
    result
}

/// Produces narrow `.gitattributes` rules for generator-owned paths.
#[must_use]
pub fn generated_attributes(paths: &BTreeSet<RelativeOperationPath>) -> BTreeSet<String> {
    paths
        .iter()
        .map(|path| format!("{} linguist-generated=true", path.as_str()))
        .collect()
}

/// Produces literal `.gitignore` rules for operation-owned cache paths.
#[must_use]
pub fn ignored_paths(paths: &BTreeSet<RelativeOperationPath>) -> BTreeSet<String> {
    paths.iter().map(ToString::to_string).collect()
}

/// Documents one fixed regeneration command without embedding an executable override.
#[must_use]
pub fn regeneration_command() -> &'static str {
    "dagger generate"
}
