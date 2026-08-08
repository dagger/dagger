//! Pinned formatting and final artifact construction.
//!
//! Generated source is formatted only in a process-private directory. The adapter
//! verifies the pinned compiler channel, invokes that channel's `rustfmt`, reparses
//! every result, and proves formatting did not change the semantic token fingerprint.

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;
use std::process::Command;

use dagger_codegen::RenderedCandidate;
use dagger_codegen::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use dagger_codegen::target::CodegenTarget;
use proc_macro2::{Delimiter, Spacing, TokenStream, TokenTree};
use quote::ToTokens as _;
use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};

use super::ArtifactPath;

const PROVENANCE_PREFIX: &str = "// @generated ";

/// Stable generated-artifact category.
#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ArtifactKind {
    /// Generated Rust module source.
    RustModule,
    /// Generated Rust integration-test source.
    RustTest,
}

/// Machine-readable ownership and exact-target provenance.
#[derive(Clone, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
pub struct Provenance {
    /// Generator source format.
    pub format: String,
    /// Component owning the output.
    pub ownership: String,
    /// Exact canonical schema digest.
    pub schema_digest: String,
    /// Exact Dagger source revision.
    pub target_revision: String,
}

impl Provenance {
    pub(super) fn validate(
        &self,
        target: &CodegenTarget,
        path: &ArtifactPath,
    ) -> Result<(), DiagnosticSet> {
        if self.format != "dagger-rust-client-v1"
            || self.ownership != "dagger-codegen"
            || self.schema_digest != target.schema_digest().to_string()
            || self.target_revision != target.dagger_revision().as_str()
        {
            return Err(format_error(
                DiagnosticCode::GeneratedProvenanceInvalid,
                path,
                "generated artifact provenance differs from the exact target",
            ));
        }
        Ok(())
    }
}

/// Final formatted artifact and its byte/semantic identities.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FormattedArtifact {
    kind: ArtifactKind,
    bytes: Vec<u8>,
    sha256: String,
    semantic_sha256: String,
    provenance: Provenance,
}

impl FormattedArtifact {
    /// Constructs and validates one already-formatted source artifact.
    pub fn from_bytes(
        path: &ArtifactPath,
        bytes: Vec<u8>,
        target: &CodegenTarget,
    ) -> Result<Self, DiagnosticSet> {
        let source = std::str::from_utf8(&bytes).map_err(|_| {
            format_error(
                DiagnosticCode::GeneratedFormatFailed,
                path,
                "formatted generated source is not UTF-8",
            )
        })?;
        let file = syn::parse_file(source).map_err(|_| {
            format_error(
                DiagnosticCode::GeneratedFormatFailed,
                path,
                "formatted generated source did not parse",
            )
        })?;
        let provenance = source
            .lines()
            .find_map(|line| line.strip_prefix(PROVENANCE_PREFIX))
            .ok_or_else(|| {
                format_error(
                    DiagnosticCode::GeneratedProvenanceInvalid,
                    path,
                    "generated artifact has no provenance header",
                )
            })
            .and_then(|json| {
                serde_json::from_str::<Provenance>(json).map_err(|_| {
                    format_error(
                        DiagnosticCode::GeneratedProvenanceInvalid,
                        path,
                        "generated artifact provenance is invalid JSON",
                    )
                })
            })?;
        provenance.validate(target, path)?;
        let kind = if path.as_str().starts_with("crates/dagger-sdk/src/gen/") {
            ArtifactKind::RustModule
        } else if matches!(
            path.as_str(),
            "crates/dagger-sdk/tests/core_reachability.rs"
                | "crates/dagger-sdk/tests/core_projection.rs"
        ) {
            ArtifactKind::RustTest
        } else {
            return Err(format_error(
                DiagnosticCode::GeneratedProvenanceInvalid,
                path,
                "generated artifact is outside the declared source roots",
            ));
        };
        let semantic = semantic_source(&file);
        Ok(Self {
            kind,
            sha256: digest(&bytes),
            semantic_sha256: digest(semantic.as_bytes()),
            bytes,
            provenance,
        })
    }

    /// Returns the generated-artifact category.
    #[must_use]
    pub const fn kind(&self) -> ArtifactKind {
        self.kind
    }

    /// Borrows final formatted bytes.
    #[must_use]
    pub fn bytes(&self) -> &[u8] {
        &self.bytes
    }

    /// Borrows the final byte digest.
    #[must_use]
    pub fn sha256(&self) -> &str {
        &self.sha256
    }

    /// Borrows the syntax-token digest, which is independent of formatting.
    #[must_use]
    pub fn semantic_sha256(&self) -> &str {
        &self.semantic_sha256
    }

    /// Borrows exact-target provenance.
    #[must_use]
    pub const fn provenance(&self) -> &Provenance {
        &self.provenance
    }
}

/// Complete formatted source/test artifact set.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FormattedArtifactSet {
    files: BTreeMap<ArtifactPath, FormattedArtifact>,
    formatter: String,
}

impl FormattedArtifactSet {
    /// Validates an already-formatted artifact map.
    pub fn from_bytes(
        target: &CodegenTarget,
        files: BTreeMap<ArtifactPath, Vec<u8>>,
        formatter: impl Into<String>,
    ) -> Result<Self, DiagnosticSet> {
        let mut finalized = BTreeMap::new();
        let mut diagnostics = Vec::new();
        for (path, bytes) in files {
            match FormattedArtifact::from_bytes(&path, bytes, target) {
                Ok(artifact) => {
                    finalized.insert(path, artifact);
                }
                Err(errors) => diagnostics.extend(errors.diagnostics().iter().cloned()),
            }
        }
        if let Some(errors) = DiagnosticSet::new(diagnostics) {
            return Err(errors);
        }
        Ok(Self {
            files: finalized,
            formatter: formatter.into(),
        })
    }

    /// Borrows artifacts in stable path order.
    #[must_use]
    pub const fn files(&self) -> &BTreeMap<ArtifactPath, FormattedArtifact> {
        &self.files
    }

    /// Identifies the only formatter used for final bytes.
    #[must_use]
    pub fn formatter(&self) -> &str {
        &self.formatter
    }
}

/// Private-state candidate formatting boundary.
pub trait CandidateFormatter: Send + Sync {
    /// Formats and validates every rendered artifact without touching its destination.
    fn finalize(
        &self,
        workspace: &Path,
        target: &CodegenTarget,
        candidate: &RenderedCandidate,
    ) -> Result<FormattedArtifactSet, DiagnosticSet>;
}

/// Formatter selected through the workspace's pinned rustup toolchain.
pub struct PinnedRustfmt;

impl PinnedRustfmt {
    /// Formats an explicit in-memory source set through the pinned toolchain.
    pub fn finalize_files(
        &self,
        workspace: &Path,
        target: &CodegenTarget,
        files: BTreeMap<ArtifactPath, Vec<u8>>,
    ) -> Result<FormattedArtifactSet, DiagnosticSet> {
        let channel = pinned_channel(workspace)?;
        validate_toolchain(&channel, target)?;
        let private = tempfile::Builder::new()
            .prefix(".dagger-rust-format-")
            .tempdir_in(workspace)
            .map_err(|_| generic_format_error("private formatter state could not be created"))?;
        let mut paths = Vec::new();
        let mut semantic_before = BTreeMap::new();
        for (path, bytes) in files {
            let source = std::str::from_utf8(&bytes).map_err(|_| {
                format_error(
                    DiagnosticCode::GeneratedFormatFailed,
                    &path,
                    "rendered generated source is not UTF-8",
                )
            })?;
            let parsed = syn::parse_file(source).map_err(|_| {
                format_error(
                    DiagnosticCode::GeneratedFormatFailed,
                    &path,
                    "rendered generated source did not parse before formatting",
                )
            })?;
            semantic_before.insert(path.clone(), semantic_source(&parsed));
            let physical = path.resolve(private.path());
            if let Some(parent) = physical.parent() {
                fs::create_dir_all(parent).map_err(|_| {
                    format_error(
                        DiagnosticCode::GeneratedFormatFailed,
                        &path,
                        "private formatter directory could not be created",
                    )
                })?;
            }
            fs::write(&physical, &bytes).map_err(|_| {
                format_error(
                    DiagnosticCode::GeneratedFormatFailed,
                    &path,
                    "private formatter input could not be written",
                )
            })?;
            paths.push((path, physical));
        }

        let mut command = Command::new("rustup");
        command
            .arg("run")
            .arg(&channel)
            .arg("rustfmt")
            .arg("--edition")
            .arg(target.rust_edition().as_str())
            .arg("--emit")
            .arg("files");
        command.args(paths.iter().map(|(_, path)| path));
        let status = command
            .output()
            .map_err(|_| generic_format_error("pinned rustfmt could not be started"))?;
        if !status.status.success() {
            return Err(generic_format_error(
                "pinned rustfmt rejected generated source",
            ));
        }

        let mut formatted = BTreeMap::new();
        for (path, physical) in paths {
            let bytes = fs::read(physical).map_err(|_| {
                format_error(
                    DiagnosticCode::GeneratedFormatFailed,
                    &path,
                    "formatted private source could not be read",
                )
            })?;
            let artifact = FormattedArtifact::from_bytes(&path, bytes, target)?;
            let after = std::str::from_utf8(&artifact.bytes)
                .ok()
                .and_then(|source| syn::parse_file(source).ok())
                .map(|file| semantic_source(&file));
            if semantic_before.get(&path) != after.as_ref() {
                return Err(format_error(
                    DiagnosticCode::GeneratedFormatFailed,
                    &path,
                    "formatter changed generated syntax semantics",
                ));
            }
            formatted.insert(path, artifact.bytes);
        }
        FormattedArtifactSet::from_bytes(target, formatted, format!("rustfmt:{channel}"))
    }
}

impl CandidateFormatter for PinnedRustfmt {
    fn finalize(
        &self,
        workspace: &Path,
        target: &CodegenTarget,
        candidate: &RenderedCandidate,
    ) -> Result<FormattedArtifactSet, DiagnosticSet> {
        let files = candidate
            .artifacts()
            .iter()
            .map(|(path, bytes)| ArtifactPath::new(path).map(|path| (path, bytes.clone())))
            .collect::<Result<BTreeMap<_, _>, _>>()?;
        self.finalize_files(workspace, target, files)
    }
}

fn pinned_channel(workspace: &Path) -> Result<String, DiagnosticSet> {
    let path = workspace.join("rust-toolchain.toml");
    let metadata = fs::symlink_metadata(&path)
        .map_err(|_| generic_format_error("pinned toolchain descriptor is unavailable"))?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(generic_format_error(
            "pinned toolchain descriptor must be a regular non-symlink file",
        ));
    }
    let source = fs::read_to_string(path)
        .map_err(|_| generic_format_error("pinned toolchain descriptor is not UTF-8"))?;
    source
        .lines()
        .find_map(|line| {
            let line = line.trim();
            let value = line.strip_prefix("channel")?.trim_start();
            let value = value.strip_prefix('=')?.trim();
            value
                .strip_prefix('"')
                .and_then(|value| value.strip_suffix('"'))
                .map(str::to_owned)
        })
        .ok_or_else(|| generic_format_error("pinned toolchain channel is missing"))
}

fn validate_toolchain(channel: &str, target: &CodegenTarget) -> Result<(), DiagnosticSet> {
    let rustc = Command::new("rustup")
        .args(["run", channel, "rustc", "--version"])
        .output()
        .map_err(|_| generic_format_error("pinned Rust compiler could not be started"))?;
    if !rustc.status.success() {
        return Err(generic_format_error("pinned Rust compiler is unavailable"));
    }
    let version = std::str::from_utf8(&rustc.stdout)
        .ok()
        .and_then(|output| output.split_whitespace().nth(1));
    if version != Some(target.rust_version().to_string().as_str()) {
        return Err(generic_format_error(
            "pinned Rust compiler differs from the exact target",
        ));
    }
    let rustfmt = Command::new("rustup")
        .args(["run", channel, "rustfmt", "--version"])
        .output()
        .map_err(|_| generic_format_error("pinned rustfmt could not be started"))?;
    if !rustfmt.status.success() {
        return Err(generic_format_error("pinned rustfmt is unavailable"));
    }
    Ok(())
}

fn digest(bytes: &[u8]) -> String {
    format!("sha256:{:x}", Sha256::digest(bytes))
}

fn semantic_source(file: &syn::File) -> String {
    let attributes = file
        .attrs
        .iter()
        .map(|attribute| attribute.to_token_stream())
        .map(|tokens| tokens.to_string())
        .collect::<Vec<_>>()
        .join("\n");
    let mut imports = Vec::new();
    let mut modules = Vec::new();
    let mut items = Vec::new();
    for item in &file.items {
        if let syn::Item::Use(item_use) = item {
            let prefix = if item_use.leading_colon.is_some() {
                "::"
            } else {
                ""
            };
            let mut item_imports = Vec::new();
            flatten_use_tree(prefix, &item_use.tree, &mut item_imports);
            let qualifiers = normalized_tokens(
                item_use
                    .attrs
                    .iter()
                    .map(|attribute| attribute.to_token_stream())
                    .chain(std::iter::once(item_use.vis.to_token_stream()))
                    .collect(),
            );
            imports.extend(
                item_imports
                    .into_iter()
                    .map(|import| format!("{qualifiers}|{import}")),
            );
        } else if matches!(item, syn::Item::Mod(item_mod) if item_mod.content.is_none()) {
            modules.push(normalized_tokens(item.to_token_stream()));
        } else {
            items.push(normalized_tokens(item.to_token_stream()));
        }
    }
    imports.sort();
    modules.sort();
    format!(
        "{attributes}\n{}\n{}\n{}",
        imports.join("\n"),
        modules.join("\n"),
        items.join("\n")
    )
}

fn normalized_tokens(tokens: TokenStream) -> String {
    let mut normalized = String::new();
    for token in tokens {
        match token {
            TokenTree::Group(group) => {
                normalized.push('G');
                normalized.push(match group.delimiter() {
                    Delimiter::Parenthesis => '(',
                    Delimiter::Brace => '{',
                    Delimiter::Bracket => '[',
                    Delimiter::None => '_',
                });
                normalized.push_str(&normalized_tokens(group.stream()));
                normalized.push('g');
            }
            TokenTree::Ident(ident) => {
                push_length_prefixed(&mut normalized, 'I', &ident.to_string());
            }
            TokenTree::Literal(literal) => {
                push_length_prefixed(&mut normalized, 'L', &literal.to_string());
            }
            TokenTree::Punct(punct) if punct.as_char() == ',' => {
                // Generator templates do not emit tuple expressions or tuple patterns.
                // Normalizing separator commas prevents rustfmt's optional trailing-comma
                // policy from masquerading as a semantic change.
            }
            TokenTree::Punct(punct) => {
                normalized.push('P');
                normalized.push(punct.as_char());
                normalized.push(match punct.spacing() {
                    Spacing::Alone => 'A',
                    Spacing::Joint => 'J',
                });
            }
        }
    }
    normalized
}

fn push_length_prefixed(output: &mut String, kind: char, value: &str) {
    use std::fmt::Write as _;

    output.push(kind);
    // Length framing keeps adjacent identifiers and literals unambiguous after
    // separator normalization.
    let _ = write!(output, "{}:{value}", value.len());
}

fn flatten_use_tree(prefix: &str, tree: &syn::UseTree, imports: &mut Vec<String>) {
    match tree {
        syn::UseTree::Path(path) => {
            let prefix = format!("{prefix}{}::", path.ident);
            flatten_use_tree(&prefix, &path.tree, imports);
        }
        syn::UseTree::Name(name) => imports.push(format!("{prefix}{}", name.ident)),
        syn::UseTree::Rename(rename) => {
            imports.push(format!("{prefix}{} as {}", rename.ident, rename.rename))
        }
        syn::UseTree::Glob(_) => imports.push(format!("{prefix}*")),
        syn::UseTree::Group(group) => {
            for item in &group.items {
                flatten_use_tree(prefix, item, imports);
            }
        }
    }
}

fn format_error(code: DiagnosticCode, path: &ArtifactPath, message: &'static str) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(path.as_str())),
        message,
    ))
}

fn generic_format_error(message: &'static str) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        DiagnosticCode::GeneratedFormatFailed,
        Some(DiagnosticCoordinate::new("rustfmt")),
        message,
    ))
}
