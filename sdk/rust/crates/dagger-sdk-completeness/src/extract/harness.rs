//! String/comment-aware extraction of pinned Dang conformance checks.
//!
//! Dang has no Rust parser dependency in this crate. The deliberately small scanner recognizes
//! the reviewed public check shape and fails closed when delimiters or annotations are ambiguous.

use std::collections::{BTreeMap, BTreeSet};

use serde_json::json;

use crate::diagnostic::{ContractDiagnostic, DiagnosticCode, DiagnosticCollector, Validation};
use crate::inventory::semantic_fingerprint;
use crate::model::{
    AuthorityId, SourceItem, SourceItemId, SourceItemInventory, SourceItemKind, SourceItemState,
    SourceLocator,
};

#[derive(Clone, Debug, Eq, PartialEq)]
/// Public refresh observations captured from the pinned harness.
pub struct HarnessRefresh {
    /// Exact kebab-case identities reported by the pinned public check listing.
    pub check_ids: BTreeSet<String>,
    /// Require the public SdkTarget construction boundary to remain present.
    pub require_sdk_target: bool,
    /// Require the mod-test integration boundary to remain present.
    pub require_mod_test: bool,
}

/// The eighteen public checks at sdk-sdk revision 8c164424b7a8a37b33a77367ef7547490d5b87b5.
pub fn pinned_check_ids() -> BTreeSet<String> {
    [
        "install-registers-sdk",
        "install-marks-as-sdk",
        "init-scaffolds-module",
        "init-writes-module-config",
        "init-registers-module",
        "init-records-authoring-sdk",
        "generate-succeeds",
        "scaffolded-module-loads",
        "sdk-reports-module-options",
        "engine-required-reports-version",
        "deps-list-succeeds",
        "generate-respects-cwd",
        "init-module-seeds-files",
        "init-module-does-not-write-config",
        "init-module-does-not-remove-existing-files",
        "init-module-honors-custom-path",
        "generate-exposes-generator",
        "init-module-renders-root-type",
    ]
    .into_iter()
    .map(str::to_owned)
    .collect()
}

/// Extracts balanced public check declarations and cross-checks the refreshed public inventory.
pub fn extract_harness(
    authority_id: &AuthorityId,
    source: &str,
    refresh: &HarnessRefresh,
) -> Validation<SourceItemInventory> {
    let mut diagnostics = DiagnosticCollector::default();
    let mask = match code_mask(source) {
        Ok(mask) => mask,
        Err(detail) => {
            diagnostics.push(harness_error("sdk-sdk.dang", detail));
            return diagnostics.finish(SourceItemInventory::default());
        }
    };
    let mut items = BTreeMap::new();
    let mut observed = BTreeSet::new();
    let mut cursor = 0;
    while let Some(start) = find_word(&mask, b"pub", cursor) {
        let mut index = skip_space(&mask, start + 3);
        let name_start = index;
        while index < mask.len() && is_identifier_byte(mask[index]) {
            index += 1;
        }
        if index == name_start {
            diagnostics.push(harness_error(
                format!("byte:{start}"),
                "public declaration has no function identity",
            ));
            cursor = start + 3;
            continue;
        }
        let name = &source[name_start..index];
        let Some(body_start) = mask[index..]
            .iter()
            .position(|byte| *byte == b'{')
            .map(|offset| index + offset)
        else {
            diagnostics.push(harness_error(
                name,
                "public declaration has no balanced body",
            ));
            break;
        };
        let annotation = &mask[index..body_start];
        if !contains_check_annotation(annotation) {
            cursor = body_start + 1;
            continue;
        }
        let Some(body_end) = matching_brace(&mask, body_start) else {
            diagnostics.push(harness_error(name, "check body has unbalanced delimiters"));
            break;
        };
        let check_id = camel_to_kebab(name);
        observed.insert(check_id.clone());
        let declaration = normalize_dang(&source[start..=body_end]);
        let signature = json!({
            "check_id": check_id,
            "function": name,
            "declaration": declaration,
        });
        let fingerprint = match semantic_fingerprint(&signature) {
            Ok(fingerprint) => fingerprint,
            Err(errors) => {
                diagnostics.extend(errors.into_inner());
                cursor = body_end + 1;
                continue;
            }
        };
        let (Ok(source_item_id), Ok(item_kind), Ok(locator)) = (
            SourceItemId::new(format!("source/{authority_id}/harness-check/{check_id}")),
            SourceItemKind::new("harness-check"),
            SourceLocator::new(format!(
                "sdk-sdk.dang:{}:{}",
                line_number(source, start),
                name
            )),
        ) else {
            diagnostics.push(harness_error(name, "check identity or locator is invalid"));
            cursor = body_end + 1;
            continue;
        };
        let item = SourceItem {
            source_item_id: source_item_id.clone(),
            authority_id: authority_id.clone(),
            item_kind,
            locator,
            semantic_signature: signature,
            fingerprint,
            state: if check_id == "init-module-renders-root-type" {
                SourceItemState::HarnessSelf
            } else {
                SourceItemState::Active
            },
        };
        if items.insert(source_item_id.clone(), item).is_some() {
            diagnostics.push(ContractDiagnostic::new(
                DiagnosticCode::HarnessCheckDuplicate,
                source_item_id.to_string(),
                None,
                "pinned source declares a duplicate public check identity",
            ));
        }
        cursor = body_end + 1;
    }

    for missing in refresh.check_ids.difference(&observed) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessCheckMissing,
            missing,
            None,
            "refreshed public check is absent from pinned source",
        ));
    }
    for extra in observed.difference(&refresh.check_ids) {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessCheckMissing,
            extra,
            None,
            "pinned source check is absent from refreshed public listing",
        ));
    }
    if refresh.require_sdk_target
        && !(contains_words(&mask, &[b"type", b"SdkTarget"])
            && mask
                .windows(b"SdkTarget".len())
                .any(|window| window == b"SdkTarget"))
    {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessScopeInvalid,
            "SdkTarget",
            None,
            "pinned public target construction boundary is absent",
        ));
    }
    if refresh.require_mod_test && !source.contains("modTest") && !source.contains("mod-test") {
        diagnostics.push(ContractDiagnostic::new(
            DiagnosticCode::HarnessScopeInvalid,
            "mod-test",
            None,
            "pinned mod-test integration boundary is absent",
        ));
    }
    diagnostics.finish(SourceItemInventory { items })
}

fn code_mask(source: &str) -> Result<Vec<u8>, &'static str> {
    let bytes = source.as_bytes();
    let mut mask = bytes.to_vec();
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'#' {
            while index < bytes.len() && bytes[index] != b'\n' {
                mask[index] = b' ';
                index += 1;
            }
            continue;
        }
        if bytes[index..].starts_with(b"\"\"\"") {
            let start = index;
            index += 3;
            while index + 2 < bytes.len() && !bytes[index..].starts_with(b"\"\"\"") {
                index += 1;
            }
            if index + 2 >= bytes.len() {
                return Err("unterminated triple-quoted string");
            }
            index += 3;
            mask[start..index].fill(b' ');
            continue;
        }
        if bytes[index] == b'"' {
            let start = index;
            index += 1;
            let mut escaped = false;
            while index < bytes.len() {
                let byte = bytes[index];
                index += 1;
                if escaped {
                    escaped = false;
                } else if byte == b'\\' {
                    escaped = true;
                } else if byte == b'"' {
                    break;
                }
            }
            if bytes.get(index.saturating_sub(1)) != Some(&b'"') {
                return Err("unterminated string literal");
            }
            mask[start..index].fill(b' ');
            continue;
        }
        index += 1;
    }
    Ok(mask)
}

fn matching_brace(mask: &[u8], start: usize) -> Option<usize> {
    let mut depth = 0_usize;
    for (offset, byte) in mask[start..].iter().enumerate() {
        match byte {
            b'{' => depth += 1,
            b'}' => {
                depth = depth.checked_sub(1)?;
                if depth == 0 {
                    return Some(start + offset);
                }
            }
            _ => {}
        }
    }
    None
}

fn normalize_dang(source: &str) -> String {
    let bytes = source.as_bytes();
    let mut normalized = String::with_capacity(source.len());
    let mut index = 0;
    let mut pending_space = false;
    while index < bytes.len() {
        if bytes[index] == b'#' {
            while index < bytes.len() && bytes[index] != b'\n' {
                index += 1;
            }
            pending_space = true;
            continue;
        }
        if bytes[index].is_ascii_whitespace() {
            pending_space = true;
            index += 1;
            continue;
        }
        if pending_space && !normalized.is_empty() {
            normalized.push(' ');
        }
        pending_space = false;
        if bytes[index..].starts_with(b"\"\"\"") {
            let end = source[index + 3..]
                .find("\"\"\"")
                .map(|offset| index + 3 + offset + 3)
                .unwrap_or(bytes.len());
            normalized.push_str(&source[index..end]);
            index = end;
        } else if bytes[index] == b'"' {
            let start = index;
            index += 1;
            let mut escaped = false;
            while index < bytes.len() {
                let byte = bytes[index];
                index += 1;
                if escaped {
                    escaped = false;
                } else if byte == b'\\' {
                    escaped = true;
                } else if byte == b'"' {
                    break;
                }
            }
            normalized.push_str(&source[start..index]);
        } else {
            normalized.push(char::from(bytes[index]));
            index += 1;
        }
    }
    normalized
}

fn contains_check_annotation(mask: &[u8]) -> bool {
    mask.windows(6).any(|window| window == b"@check")
        || mask
            .windows(7)
            .any(|window| window[0] == b'@' && &window[1..].trim_ascii_start() == b"check")
}

fn find_word(haystack: &[u8], needle: &[u8], from: usize) -> Option<usize> {
    let mut cursor = from;
    while cursor + needle.len() <= haystack.len() {
        let offset = haystack[cursor..]
            .windows(needle.len())
            .position(|window| window == needle)?;
        let position = cursor + offset;
        let bounded = position
            .checked_sub(1)
            .is_none_or(|before| !is_identifier_byte(haystack[before]))
            && haystack
                .get(position + needle.len())
                .is_none_or(|after| !is_identifier_byte(*after));
        if bounded {
            return Some(position);
        }
        cursor = position + needle.len();
    }
    None
}

fn contains_words(mask: &[u8], words: &[&[u8]]) -> bool {
    let mut cursor = 0;
    for word in words {
        let Some(found) = find_word(mask, word, cursor) else {
            return false;
        };
        cursor = found + word.len();
    }
    true
}

fn skip_space(mask: &[u8], mut index: usize) -> usize {
    while index < mask.len() && mask[index].is_ascii_whitespace() {
        index += 1;
    }
    index
}

fn is_identifier_byte(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || byte == b'_'
}

fn camel_to_kebab(name: &str) -> String {
    let mut output = String::with_capacity(name.len());
    for character in name.chars() {
        if character.is_uppercase() {
            if !output.is_empty() {
                output.push('-');
            }
            output.extend(character.to_lowercase());
        } else {
            output.push(character);
        }
    }
    output
}

fn line_number(source: &str, byte: usize) -> usize {
    source[..byte]
        .bytes()
        .filter(|value| *value == b'\n')
        .count()
        + 1
}

fn harness_error(subject: impl Into<String>, detail: impl Into<String>) -> ContractDiagnostic {
    ContractDiagnostic::new(
        DiagnosticCode::HarnessCheckKindInvalid,
        subject,
        None,
        detail,
    )
}
