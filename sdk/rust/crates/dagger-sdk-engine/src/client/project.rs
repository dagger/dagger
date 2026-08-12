//! Conservative standalone-client project discovery and semantic reconciliation.
//!
//! Discovery reads a fixed file set beneath one explicit operation root. Reconciliation
//! is pure over those bytes: it proposes complete replacement bytes for transaction
//! safety while granting ownership only to named Cargo keys, one Rust item, one marked
//! documentation region, and exact VCS policy lines.

use std::collections::{BTreeMap, BTreeSet};
use std::str::FromStr as _;

use semver::{Version, VersionReq};
use sha2::{Digest as _, Sha256};
use syn::{Item, Visibility};
use toml_edit::{DocumentMut, InlineTable, Item as TomlItem, Table, Value, value};

use crate::project::toolchain::{ToolchainDeclaration, select_toolchain};
use crate::project::toolchain_declarations;
use crate::project::vcs::{append_missing_lines, generated_attributes, ignored_paths};
use crate::{
    AmendmentCoordinate, AmendmentKind, CargoPackageName, ClientProjectIdentity, EngineDiagnostic,
    EngineDiagnosticCode, ExactRustToolchain, OperationRoot, PublishedSdkDependency,
    RelativeOperationPath, RustIdentifier, Sha256Digest, StableCoordinate, ToolchainSelection,
};

const RUST_EDITION: &str = "2024";
const RUST_MSRV: &str = "1.97.1";
const TOKIO_RUNTIME_VERSION: &str = "1.35.1";
const README_REGION: &str = "dagger-client-quickstart-v1";

/// One bounded caller-authored file retained as exact bytes and byte identity.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoredFile {
    /// Operation-relative file path.
    pub path: RelativeOperationPath,
    /// Exact bytes read during discovery.
    pub bytes: Vec<u8>,
    /// Digest used to reject changes between discovery and publication.
    pub digest: Sha256Digest,
}

/// Complete byte-only view of the fixed project file set.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientProjectSnapshot {
    /// Selected project root.
    pub root: RelativeOperationPath,
    /// Selected package manifest, when already present.
    pub manifest: Option<AuthoredFile>,
    /// Existing package name extracted from the selected manifest.
    pub package_name: Option<String>,
    /// Selected custom or default library-root path, whether or not it exists.
    pub library_path: RelativeOperationPath,
    /// Library-adjacent root where Rust module discovery expects generated bindings.
    pub generated_client_root: RelativeOperationPath,
    /// Existing library root.
    pub library_root: Option<AuthoredFile>,
    /// Existing README.
    pub readme: Option<AuthoredFile>,
    /// Existing generated-path attributes.
    pub gitattributes: Option<AuthoredFile>,
    /// Existing ignore policy.
    pub gitignore: Option<AuthoredFile>,
    /// Digest of caller-owned lockfile bytes.
    pub lockfile_digest: Option<Sha256Digest>,
    /// Nearest compatible declaration or target default.
    pub toolchain: ToolchainSelection,
}

/// Documentation state selected by initialization or completed generation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ClientDocumentationState {
    /// Honest scaffold instructions before bindings exist.
    Initialized,
    /// Quickstart instructions after bindings were generated.
    Generated,
}

/// Pure inputs that determine project policy amendments.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientProjectRequest {
    /// Adopted or newly selected Cargo identity.
    pub identity: ClientProjectIdentity,
    /// Exact immutable public SDK dependency.
    pub sdk_dependency: PublishedSdkDependency,
    /// Whether documentation may claim generated bindings.
    pub documentation: ClientDocumentationState,
}

/// One semantic item candidate inside an otherwise caller-authored file.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AmendmentCandidate {
    /// Parser required to authenticate the semantic value.
    pub kind: AmendmentKind,
    /// Semantic identity observed during discovery, absent for a newly introduced item.
    pub prior_semantic_digest: Option<Sha256Digest>,
    /// Digest of the complete authored file observed during discovery.
    ///
    /// Semantic ownership permits unrelated caller edits between generations, while
    /// this snapshot identity prevents an edit racing the already completed plan from
    /// being overwritten by its complete-file transaction candidate.
    pub prior_file_digest: Option<Sha256Digest>,
    /// Semantic identity represented by the complete candidate bytes.
    pub next_semantic_digest: Sha256Digest,
    /// Complete file bytes used by the failure-atomic transaction.
    pub complete_file_bytes: Vec<u8>,
}

/// Complete project candidate before generated artifacts are combined with it.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientProjectPlan {
    /// Cargo and crate identity used by generated source.
    pub identity: ClientProjectIdentity,
    /// Every SDK-owned semantic value in authored files.
    pub amendments: BTreeMap<AmendmentCoordinate, AmendmentCandidate>,
    /// Whole files created outside semantic amendment ownership.
    pub created_files: BTreeMap<RelativeOperationPath, Vec<u8>>,
    /// Exact selected compiler toolchain.
    pub toolchain: ExactRustToolchain,
}

/// Borrowed semantic inputs used to select a standalone client's Cargo identity.
#[derive(Clone, Copy, Debug)]
pub struct ClientProjectIdentityRequest<'a> {
    /// Existing Cargo package name, when project discovery found one.
    pub existing_package_name: Option<&'a str>,
    /// Confined client root whose basename seeds a new package name.
    pub client_root: &'a RelativeOperationPath,
    /// Engine-normalized module name used only when the root has no usable basename.
    pub bound_module_name: &'a StableCoordinate,
}

/// Selects a Cargo package and crate identity without reading a filesystem.
pub fn select_client_project_identity(
    request: ClientProjectIdentityRequest<'_>,
) -> Result<ClientProjectIdentity, EngineDiagnostic> {
    let package_name = match request.existing_package_name {
        Some(existing) => CargoPackageName::new(existing.to_owned()).map_err(|_| {
            project_conflict(
                "package.name",
                "existing Cargo package name is incompatible with Rust client use",
            )
        })?,
        None => {
            let basename = request
                .client_root
                .as_str()
                .rsplit('/')
                .next()
                .map(normalize_package_component)
                .filter(|value| !value.is_empty())
                .unwrap_or_else(|| normalize_package_component(request.bound_module_name.as_str()));
            CargoPackageName::new(format!("{basename}-dagger-client")).map_err(|_| {
                project_conflict(
                    "package.name",
                    "derived Cargo package name is incompatible with Rust client use",
                )
            })?
        }
    };
    let crate_name =
        RustIdentifier::new(package_name.as_str().replace('-', "_")).map_err(|_| {
            project_conflict(
                "package.name",
                "Cargo package name does not normalize to a Rust crate identifier",
            )
        })?;
    Ok(ClientProjectIdentity {
        package_name,
        crate_name,
    })
}

/// Reads the fixed standalone-client project file set without executing Cargo.
pub fn discover_client_project(
    root: &OperationRoot,
    client_root: &RelativeOperationPath,
) -> Result<ClientProjectSnapshot, EngineDiagnostic> {
    root.validate_prospective(client_root)?;
    let manifest_path = child(client_root, "Cargo.toml")?;
    let manifest = read_optional(root, manifest_path.clone())?;
    let (package_name, library_path) = if let Some(file) = &manifest {
        let source = utf8(&file.path, &file.bytes, "Cargo manifest must be UTF-8")?;
        let document = DocumentMut::from_str(source).map_err(|_| {
            project_conflict(file.path.as_str(), "Cargo manifest is not valid TOML")
        })?;
        let package = document
            .get("package")
            .and_then(TomlItem::as_table)
            .ok_or_else(|| {
                project_conflict("Cargo.toml::package", "selected manifest is virtual-only")
            })?;
        let name = package
            .get("name")
            .and_then(TomlItem::as_str)
            .ok_or_else(|| {
                project_conflict(
                    "Cargo.toml::package.name",
                    "package name must be one string",
                )
            })?;
        CargoPackageName::new(name.to_owned()).map_err(|_| {
            project_conflict(
                "Cargo.toml::package.name",
                "package name is not a valid client identity",
            )
        })?;
        let library = match document
            .get("lib")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("path"))
        {
            Some(item) => {
                let relative = item.as_str().ok_or_else(|| {
                    project_conflict("Cargo.toml::lib.path", "library path must be one string")
                })?;
                child(client_root, relative).map_err(|_| {
                    project_conflict(
                        "Cargo.toml::lib.path",
                        "library path escapes the selected client root",
                    )
                })?
            }
            None => child(client_root, "src/lib.rs")?,
        };
        (Some(name.to_owned()), library)
    } else {
        (None, child(client_root, "src/lib.rs")?)
    };
    let library_parent = library_path
        .as_str()
        .rsplit_once('/')
        .map(|(parent, _)| parent)
        .ok_or_else(|| {
            project_conflict(
                library_path.as_str(),
                "selected library root has no confined parent",
            )
        })?;
    let generated_client_root =
        RelativeOperationPath::parse(&format!("{library_parent}/dagger_client")).map_err(|_| {
            project_conflict(
                library_path.as_str(),
                "generated client root is not canonical",
            )
        })?;
    let library_root = read_optional(root, library_path.clone())?;
    if let Some(file) = &library_root {
        let source = utf8(&file.path, &file.bytes, "library root must be UTF-8")?;
        syn::parse_file(source).map_err(|_| {
            project_conflict(file.path.as_str(), "library root is not valid Rust source")
        })?;
    }
    let readme = read_optional(root, child(client_root, "README.md")?)?;
    if let Some(file) = &readme {
        let _ = utf8(&file.path, &file.bytes, "README must be UTF-8")?;
    }
    let gitattributes = read_optional(root, child(client_root, ".gitattributes")?)?;
    let gitignore = read_optional(root, child(client_root, ".gitignore")?)?;
    for file in [gitattributes.as_ref(), gitignore.as_ref()]
        .into_iter()
        .flatten()
    {
        let _ = utf8(&file.path, &file.bytes, "VCS policy must be UTF-8")?;
    }
    let lockfile = read_optional(root, child(client_root, "Cargo.lock")?)?;
    let declarations = toolchain_declarations(root, client_root)?;
    let borrowed = declarations
        .iter()
        .map(|(path, bytes)| ToolchainDeclaration { path, bytes })
        .collect::<Vec<_>>();
    Ok(ClientProjectSnapshot {
        root: client_root.clone(),
        manifest,
        package_name,
        library_path,
        generated_client_root,
        library_root,
        readme,
        gitattributes,
        gitignore,
        lockfile_digest: lockfile.as_ref().map(|file| file.digest.clone()),
        toolchain: select_toolchain(&borrowed)?,
    })
}

/// Produces deterministic project amendments without filesystem or process authority.
pub fn reconcile_client_project(
    snapshot: &ClientProjectSnapshot,
    request: &ClientProjectRequest,
) -> Result<ClientProjectPlan, EngineDiagnostic> {
    if snapshot
        .package_name
        .as_deref()
        .is_some_and(|name| name != request.identity.package_name.as_str())
    {
        return Err(project_conflict(
            "Cargo.toml::package.name",
            "selected project identity differs from its Cargo manifest",
        ));
    }
    let manifest_path = child(&snapshot.root, "Cargo.toml")?;
    let mut amendments = BTreeMap::new();
    let manifest_bytes = reconcile_manifest(snapshot.manifest.as_ref(), request)?;
    for key in [
        "package.name",
        "package.publish",
        "package.edition",
        "package.rust-version",
        "dependencies.dagger-sdk",
        "dependencies.tokio",
    ] {
        insert_amendment(
            &mut amendments,
            manifest_path.clone(),
            key,
            AmendmentKind::CargoKey,
            snapshot.manifest.as_ref().map(|file| file.bytes.as_slice()),
            &manifest_bytes,
        )?;
    }

    let mut library_bytes = snapshot.library_root.as_ref().map_or_else(
        || b"//! Standalone Dagger client library.\n".to_vec(),
        |file| file.bytes.clone(),
    );
    if request.documentation == ClientDocumentationState::Generated {
        library_bytes = reconcile_library(&snapshot.library_path, &library_bytes)?;
        insert_amendment(
            &mut amendments,
            snapshot.library_path.clone(),
            "rust-module.dagger_client",
            AmendmentKind::RustModuleItem,
            snapshot
                .library_root
                .as_ref()
                .map(|file| file.bytes.as_slice()),
            &library_bytes,
        )?;
    }

    let readme_path = child(&snapshot.root, "README.md")?;
    let readme_current = snapshot
        .readme
        .as_ref()
        .map_or(&[][..], |file| file.bytes.as_slice());
    let readme_bytes = reconcile_readme(readme_current, request.documentation)?;
    insert_amendment(
        &mut amendments,
        readme_path,
        "docs.dagger-client-quickstart-v1",
        AmendmentKind::DocumentationRegion,
        snapshot.readme.as_ref().map(|file| file.bytes.as_slice()),
        &readme_bytes,
    )?;

    let generated_relative = snapshot
        .generated_client_root
        .as_str()
        .strip_prefix(&format!("{}/", snapshot.root))
        .ok_or_else(|| {
            project_conflict(
                snapshot.generated_client_root.as_str(),
                "generated client root is outside the selected project",
            )
        })?;
    let generated =
        RelativeOperationPath::parse(&format!("{generated_relative}/**")).map_err(|_| {
            project_conflict(
                snapshot.generated_client_root.as_str(),
                "generated VCS pattern is not canonical",
            )
        })?;
    if request.documentation == ClientDocumentationState::Generated {
        let path = child(&snapshot.root, ".gitattributes")?;
        let current = snapshot
            .gitattributes
            .as_ref()
            .map_or(&[][..], |file| file.bytes.as_slice());
        let bytes =
            append_missing_lines(current, &generated_attributes(&BTreeSet::from([generated])));
        insert_amendment(
            &mut amendments,
            path,
            "gitattributes.dagger-client-generated-root",
            AmendmentKind::VcsPolicyLine,
            snapshot
                .gitattributes
                .as_ref()
                .map(|file| file.bytes.as_slice()),
            &bytes,
        )?;
    }
    let gitignore_path = child(&snapshot.root, ".gitignore")?;
    let target_path =
        RelativeOperationPath::parse("target").expect("reviewed Cargo target path is canonical");
    let ignore_current = snapshot
        .gitignore
        .as_ref()
        .map_or(&[][..], |file| file.bytes.as_slice());
    let ignore_bytes = append_missing_lines(
        ignore_current,
        &ignored_paths(&BTreeSet::from([target_path])),
    );
    insert_amendment(
        &mut amendments,
        gitignore_path,
        "gitignore.cargo-target",
        AmendmentKind::VcsPolicyLine,
        snapshot
            .gitignore
            .as_ref()
            .map(|file| file.bytes.as_slice()),
        &ignore_bytes,
    )?;

    let mut created_files = BTreeMap::new();
    if snapshot.library_root.is_none()
        && request.documentation == ClientDocumentationState::Initialized
    {
        created_files.insert(snapshot.library_path.clone(), library_bytes);
    }
    let toolchain = match &snapshot.toolchain {
        ToolchainSelection::Declared { toolchain, .. } => toolchain.clone(),
        ToolchainSelection::TargetDefault { toolchain } => {
            created_files.insert(
                child(&snapshot.root, "rust-toolchain.toml")?,
                format!("[toolchain]\nchannel = \"{toolchain}\"\n").into_bytes(),
            );
            toolchain.clone()
        }
    };
    Ok(ClientProjectPlan {
        identity: request.identity.clone(),
        amendments,
        created_files,
        toolchain,
    })
}

/// Extracts and hashes one amendment's canonical semantic value from complete bytes.
pub fn semantic_amendment_digest(
    kind: AmendmentKind,
    semantic_key: &StableCoordinate,
    bytes: &[u8],
) -> Result<Sha256Digest, EngineDiagnostic> {
    let value = match kind {
        AmendmentKind::CargoKey => cargo_semantic_value(semantic_key.as_str(), bytes)?,
        AmendmentKind::RustModuleItem => rust_module_semantic_value(bytes)?,
        AmendmentKind::DocumentationRegion => readme_semantic_value(bytes)?,
        AmendmentKind::VcsPolicyLine => vcs_semantic_value(semantic_key.as_str(), bytes)?,
    };
    Ok(semantic_digest(kind, semantic_key.as_str(), &value))
}

fn reconcile_manifest(
    current: Option<&AuthoredFile>,
    request: &ClientProjectRequest,
) -> Result<Vec<u8>, EngineDiagnostic> {
    let mut document = if let Some(file) = current {
        DocumentMut::from_str(utf8(
            &file.path,
            &file.bytes,
            "Cargo manifest must be UTF-8",
        )?)
        .map_err(|_| project_conflict(file.path.as_str(), "Cargo manifest is not valid TOML"))?
    } else {
        let mut document = DocumentMut::new();
        let mut package = Table::new();
        package.insert("name", value(request.identity.package_name.as_str()));
        package.insert("version", value("0.1.0"));
        document.insert("package", TomlItem::Table(package));
        document
    };
    let package = document
        .get_mut("package")
        .and_then(TomlItem::as_table_mut)
        .ok_or_else(|| {
            project_conflict("Cargo.toml::package", "selected manifest is virtual-only")
        })?;
    require_or_insert_string(package, "name", request.identity.package_name.as_str())?;
    if package.get("version").and_then(TomlItem::as_str).is_none() {
        return Err(project_conflict(
            "Cargo.toml::package.version",
            "package version must be one string",
        ));
    }
    require_or_insert_bool(package, "publish", false)?;
    require_or_insert_string(package, "edition", RUST_EDITION)?;
    require_or_insert_string(package, "rust-version", RUST_MSRV)?;

    let dependencies = document["dependencies"].or_insert(TomlItem::Table(Table::new()));
    let dependencies = dependencies.as_table_mut().ok_or_else(|| {
        project_conflict(
            "Cargo.toml::dependencies",
            "dependencies must be a TOML table",
        )
    })?;
    if let Some(existing) = dependencies.get("dagger-sdk") {
        validate_sdk_dependency(existing, &request.sdk_dependency)?;
    } else {
        dependencies.insert("dagger-sdk", sdk_dependency_item(&request.sdk_dependency));
    }
    if let Some(existing) = dependencies.get("tokio") {
        validate_tokio(existing)?;
    } else {
        let mut runtime = InlineTable::new();
        runtime.insert("version", Value::from(TOKIO_RUNTIME_VERSION));
        runtime.insert("default-features", Value::from(false));
        runtime.insert(
            "features",
            Value::Array(
                ["macros", "rt-multi-thread"]
                    .into_iter()
                    .map(Value::from)
                    .collect(),
            ),
        );
        dependencies.insert("tokio", TomlItem::Value(Value::InlineTable(runtime)));
    }
    Ok(document.to_string().into_bytes())
}

fn require_or_insert_string(
    table: &mut Table,
    key: &str,
    expected: &str,
) -> Result<(), EngineDiagnostic> {
    match table.get(key) {
        Some(item) if item.as_str() == Some(expected) => Ok(()),
        Some(_) => Err(project_conflict(
            &format!("Cargo.toml::package.{key}"),
            "existing package policy conflicts with standalone-client adoption",
        )),
        None => {
            table.insert(key, value(expected));
            Ok(())
        }
    }
}

fn require_or_insert_bool(
    table: &mut Table,
    key: &str,
    expected: bool,
) -> Result<(), EngineDiagnostic> {
    match table.get(key) {
        Some(item) if item.as_bool() == Some(expected) => Ok(()),
        Some(_) => Err(project_conflict(
            &format!("Cargo.toml::package.{key}"),
            "client packages must remain non-publishable",
        )),
        None => {
            table.insert(key, value(expected));
            Ok(())
        }
    }
}

fn sdk_dependency_item(dependency: &PublishedSdkDependency) -> TomlItem {
    match dependency {
        PublishedSdkDependency::Registry { exact_version, .. } => {
            value(format!("={exact_version}"))
        }
        PublishedSdkDependency::Git { url, revision, .. } => {
            let mut table = InlineTable::new();
            table.insert("git", Value::from(url.as_str()));
            table.insert("rev", Value::from(revision.as_str()));
            TomlItem::Value(Value::InlineTable(table))
        }
    }
}

fn validate_sdk_dependency(
    item: &TomlItem,
    expected: &PublishedSdkDependency,
) -> Result<(), EngineDiagnostic> {
    if let Some(version) = item.as_str() {
        return match expected {
            PublishedSdkDependency::Registry { exact_version, .. }
                if version == format!("={exact_version}") =>
            {
                Ok(())
            }
            _ => Err(sdk_conflict()),
        };
    }
    let Some(table) = item.as_table_like() else {
        return Err(sdk_mutable());
    };
    if table.get("workspace").and_then(TomlItem::as_bool) == Some(true)
        || ["path", "branch", "tag"]
            .iter()
            .any(|key| table.get(key).is_some())
    {
        return Err(sdk_mutable());
    }
    match expected {
        PublishedSdkDependency::Registry {
            exact_version,
            registry,
            ..
        } => {
            let version = table.get("version").and_then(TomlItem::as_str);
            let actual_registry = table.get("registry").and_then(TomlItem::as_str);
            if version == Some(format!("={exact_version}").as_str())
                && actual_registry.is_none_or(|actual| actual == registry.as_str())
                && table.get("git").is_none()
            {
                Ok(())
            } else {
                Err(sdk_conflict())
            }
        }
        PublishedSdkDependency::Git { url, revision, .. } => {
            if table.get("git").and_then(TomlItem::as_str) == Some(url.as_str())
                && table.get("rev").and_then(TomlItem::as_str) == Some(revision.as_str())
                && table.get("version").is_none()
            {
                Ok(())
            } else if table.get("rev").is_none() {
                Err(sdk_mutable())
            } else {
                Err(sdk_conflict())
            }
        }
    }
}

fn validate_tokio(item: &TomlItem) -> Result<(), EngineDiagnostic> {
    let Some(table) = item.as_table_like() else {
        return Err(project_conflict(
            "Cargo.toml::dependencies.tokio",
            "Tokio must declare its required runtime features",
        ));
    };
    if ["path", "branch", "tag", "workspace"]
        .iter()
        .any(|key| table.get(key).is_some())
        || (table.get("git").is_some() && table.get("rev").is_none())
    {
        return Err(project_conflict(
            "Cargo.toml::dependencies.tokio",
            "Tokio dependency source is mutable or inherited",
        ));
    }
    let compatible_source = table
        .get("version")
        .and_then(TomlItem::as_str)
        .and_then(|requirement| VersionReq::parse(requirement).ok())
        .is_some_and(|requirement| {
            requirement.matches(
                &Version::parse(TOKIO_RUNTIME_VERSION).expect("Tokio version constant must parse"),
            )
        })
        || (table.get("git").is_some() && table.get("rev").is_some());
    let features = table
        .get("features")
        .and_then(TomlItem::as_array)
        .map(|array| {
            array
                .iter()
                .filter_map(Value::as_str)
                .collect::<BTreeSet<_>>()
        })
        .unwrap_or_default();
    if compatible_source
        && (features.contains("full")
            || (features.contains("macros") && features.contains("rt-multi-thread")))
    {
        Ok(())
    } else {
        Err(project_conflict(
            "Cargo.toml::dependencies.tokio",
            "Tokio must provide macros and the multi-thread runtime",
        ))
    }
}

fn reconcile_library(
    path: &RelativeOperationPath,
    current: &[u8],
) -> Result<Vec<u8>, EngineDiagnostic> {
    let source = utf8(path, current, "library root must be UTF-8")?;
    let syntax = syn::parse_file(source)
        .map_err(|_| project_conflict(path.as_str(), "library root is not valid Rust source"))?;
    let mut equivalent = 0_u8;
    for item in syntax.items {
        if let Item::Mod(module) = item
            && module.ident == "dagger_client"
        {
            if matches!(module.vis, Visibility::Public(_))
                && module.content.is_none()
                && module.attrs.is_empty()
            {
                equivalent = equivalent.saturating_add(1);
            } else {
                return Err(project_conflict(
                    path.as_str(),
                    "dagger_client module identifier is already used incompatibly",
                ));
            }
        }
    }
    if equivalent > 1 {
        return Err(project_conflict(
            path.as_str(),
            "dagger_client module is declared more than once",
        ));
    }
    if equivalent == 1 {
        return Ok(current.to_vec());
    }
    let mut rendered = current.to_vec();
    if !rendered.is_empty() && !rendered.ends_with(b"\n") {
        rendered.push(b'\n');
    }
    rendered.extend_from_slice(b"\npub mod dagger_client;\n");
    Ok(rendered)
}

fn reconcile_readme(
    current: &[u8],
    state: ClientDocumentationState,
) -> Result<Vec<u8>, EngineDiagnostic> {
    let source = std::str::from_utf8(current)
        .map_err(|_| project_conflict("README.md", "README must be UTF-8"))?;
    let body = match state {
        ClientDocumentationState::Initialized => {
            "## Dagger client\n\nThis Cargo project is ready for generated bindings. Run `dagger generate`; generation binds Core plus this client's one selected module. Bind each dependency through its own client.\n"
        }
        ClientDocumentationState::Generated => {
            "## Dagger client\n\nBindings under `src/dagger_client` reuse the public `dagger-sdk` lifecycle and bind Core plus this client's one selected module. Run `cargo run --example dagger-client-quickstart`; bind each dependency through its own client. Files outside this generated region remain authored.\n"
        }
    };
    let region = render_readme_region(body);
    let Some((start, end, _)) = parse_readme_region(source)? else {
        let mut rendered = current.to_vec();
        if !rendered.is_empty() && !rendered.ends_with(b"\n") {
            rendered.push(b'\n');
        }
        if !rendered.is_empty() {
            rendered.push(b'\n');
        }
        rendered.extend_from_slice(region.as_bytes());
        return Ok(rendered);
    };
    let mut rendered = String::with_capacity(source.len() + region.len());
    rendered.push_str(&source[..start]);
    rendered.push_str(&region);
    rendered.push_str(&source[end..]);
    Ok(rendered.into_bytes())
}

fn render_readme_region(body: &str) -> String {
    let digest = digest(body.as_bytes());
    format!("<!-- {README_REGION} {digest} -->\n{body}<!-- /{README_REGION} -->\n")
}

fn parse_readme_region(source: &str) -> Result<Option<(usize, usize, String)>, EngineDiagnostic> {
    let opening_prefix = format!("<!-- {README_REGION} ");
    let closing = format!("<!-- /{README_REGION} -->");
    let openings = source.match_indices(&opening_prefix).collect::<Vec<_>>();
    let closings = source.match_indices(&closing).collect::<Vec<_>>();
    if openings.is_empty() && closings.is_empty() {
        return Ok(None);
    }
    if openings.len() != 1 || closings.len() != 1 || closings[0].0 < openings[0].0 {
        return Err(project_conflict(
            "README.md::docs.dagger-client-quickstart-v1",
            "quickstart region is malformed, nested, or duplicated",
        ));
    }
    let start = openings[0].0;
    let marker_end = source[start..]
        .find(" -->\n")
        .map(|offset| start + offset)
        .ok_or_else(|| {
            project_conflict(
                "README.md::docs.dagger-client-quickstart-v1",
                "quickstart opening marker is malformed",
            )
        })?;
    let expected = &source[start + opening_prefix.len()..marker_end];
    let body_start = marker_end + " -->\n".len();
    let body_end = closings[0].0;
    if body_end < body_start
        || expected != digest(&source.as_bytes()[body_start..body_end]).as_str()
    {
        return Err(project_conflict(
            "README.md::docs.dagger-client-quickstart-v1",
            "quickstart body differs from its recorded digest",
        ));
    }
    let mut end = body_end + closing.len();
    if source[end..].starts_with('\n') {
        end += 1;
    }
    Ok(Some((start, end, source[body_start..body_end].to_owned())))
}

fn insert_amendment(
    amendments: &mut BTreeMap<AmendmentCoordinate, AmendmentCandidate>,
    file: RelativeOperationPath,
    key: &str,
    kind: AmendmentKind,
    prior: Option<&[u8]>,
    next: &[u8],
) -> Result<(), EngineDiagnostic> {
    let semantic_key = stable(key)?;
    let prior_semantic_digest = prior
        .map(|bytes| prior_semantic_digest(kind, &semantic_key, bytes))
        .transpose()?
        .flatten();
    let next_semantic_digest = semantic_amendment_digest(kind, &semantic_key, next)?;
    let coordinate = AmendmentCoordinate::new(file, semantic_key);
    if amendments
        .insert(
            coordinate,
            AmendmentCandidate {
                kind,
                prior_semantic_digest,
                prior_file_digest: prior.map(digest),
                next_semantic_digest,
                complete_file_bytes: next.to_vec(),
            },
        )
        .is_some()
    {
        return Err(project_conflict(
            key,
            "semantic amendment coordinate is duplicated",
        ));
    }
    Ok(())
}

fn cargo_semantic_value(key: &str, bytes: &[u8]) -> Result<Vec<u8>, EngineDiagnostic> {
    let source = std::str::from_utf8(bytes)
        .map_err(|_| project_conflict("Cargo.toml", "Cargo manifest must be UTF-8"))?;
    let document = DocumentMut::from_str(source)
        .map_err(|_| project_conflict("Cargo.toml", "Cargo manifest is not valid TOML"))?;
    let item = match key {
        "package.name" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("name")),
        "package.version" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("version")),
        "package.publish" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("publish")),
        "package.edition" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("edition")),
        "package.rust-version" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("rust-version")),
        "dependencies.dagger-sdk" => document
            .get("dependencies")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("dagger-sdk")),
        "dependencies.tokio" => document
            .get("dependencies")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("tokio")),
        _ => None,
    }
    .ok_or_else(|| project_conflict(key, "owned Cargo semantic value is missing"))?;
    canonical_toml_item(item)
}

fn prior_semantic_digest(
    kind: AmendmentKind,
    semantic_key: &StableCoordinate,
    bytes: &[u8],
) -> Result<Option<Sha256Digest>, EngineDiagnostic> {
    let value = match kind {
        AmendmentKind::CargoKey => cargo_semantic_value_optional(semantic_key.as_str(), bytes)?,
        AmendmentKind::RustModuleItem => rust_module_semantic_value_optional(bytes)?,
        AmendmentKind::DocumentationRegion => readme_semantic_value_optional(bytes)?,
        AmendmentKind::VcsPolicyLine => vcs_semantic_value_optional(semantic_key.as_str(), bytes)?,
    };
    Ok(value.map(|value| semantic_digest(kind, semantic_key.as_str(), &value)))
}

fn cargo_semantic_value_optional(
    key: &str,
    bytes: &[u8],
) -> Result<Option<Vec<u8>>, EngineDiagnostic> {
    let source = std::str::from_utf8(bytes)
        .map_err(|_| project_conflict("Cargo.toml", "Cargo manifest must be UTF-8"))?;
    let document = DocumentMut::from_str(source)
        .map_err(|_| project_conflict("Cargo.toml", "Cargo manifest is not valid TOML"))?;
    let item = match key {
        "package.name" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("name")),
        "package.publish" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("publish")),
        "package.edition" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("edition")),
        "package.rust-version" => document
            .get("package")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("rust-version")),
        "dependencies.dagger-sdk" => document
            .get("dependencies")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("dagger-sdk")),
        "dependencies.tokio" => document
            .get("dependencies")
            .and_then(TomlItem::as_table)
            .and_then(|table| table.get("tokio")),
        _ => return Err(project_conflict(key, "unknown Cargo amendment coordinate")),
    };
    item.map(canonical_toml_item).transpose()
}

fn canonical_toml_item(item: &TomlItem) -> Result<Vec<u8>, EngineDiagnostic> {
    fn encode_item(item: &TomlItem, encoded: &mut Vec<u8>) -> Result<(), EngineDiagnostic> {
        if let Some(table) = item.as_table_like() {
            encoded.push(b't');
            let mut entries = table.iter().collect::<Vec<_>>();
            entries.sort_by(|left, right| left.0.cmp(right.0));
            for (key, value) in entries {
                encode_bytes(key.as_bytes(), encoded);
                encode_item(value, encoded)?;
            }
            encoded.push(0xff);
        } else if let Some(value) = item.as_value() {
            encode_value(value, encoded)?;
        } else {
            return Err(project_conflict(
                "Cargo.toml",
                "owned Cargo value has an unsupported semantic shape",
            ));
        }
        Ok(())
    }

    fn encode_value(value: &Value, encoded: &mut Vec<u8>) -> Result<(), EngineDiagnostic> {
        match value {
            Value::String(value) => {
                encoded.push(b's');
                encode_bytes(value.value().as_bytes(), encoded);
            }
            Value::Integer(value) => {
                encoded.push(b'i');
                encode_bytes(value.value().to_string().as_bytes(), encoded);
            }
            Value::Float(value) => {
                encoded.push(b'f');
                encode_bytes(value.value().to_string().as_bytes(), encoded);
            }
            Value::Boolean(value) => {
                encoded.push(b'b');
                encoded.push(u8::from(*value.value()));
            }
            Value::Datetime(value) => {
                encoded.push(b'd');
                encode_bytes(value.value().to_string().as_bytes(), encoded);
            }
            Value::Array(array) => {
                encoded.push(b'a');
                for value in array.iter() {
                    encode_value(value, encoded)?;
                }
                encoded.push(0xff);
            }
            Value::InlineTable(table) => {
                encoded.push(b't');
                let mut entries = table.iter().collect::<Vec<_>>();
                entries.sort_by(|left, right| left.0.cmp(right.0));
                for (key, value) in entries {
                    encode_bytes(key.as_bytes(), encoded);
                    encode_value(value, encoded)?;
                }
                encoded.push(0xff);
            }
        }
        Ok(())
    }

    fn encode_bytes(bytes: &[u8], encoded: &mut Vec<u8>) {
        encoded.extend_from_slice(&(bytes.len() as u64).to_be_bytes());
        encoded.extend_from_slice(bytes);
    }

    let mut encoded = Vec::new();
    encode_item(item, &mut encoded)?;
    Ok(encoded)
}

fn rust_module_semantic_value(bytes: &[u8]) -> Result<Vec<u8>, EngineDiagnostic> {
    rust_module_semantic_value_optional(bytes)?.ok_or_else(|| {
        project_conflict(
            "rust-module.dagger_client",
            "owned dagger_client module item is missing",
        )
    })
}

fn rust_module_semantic_value_optional(bytes: &[u8]) -> Result<Option<Vec<u8>>, EngineDiagnostic> {
    let source = std::str::from_utf8(bytes)
        .map_err(|_| project_conflict("src/lib.rs", "library root must be UTF-8"))?;
    let syntax = syn::parse_file(source)
        .map_err(|_| project_conflict("src/lib.rs", "library root is not valid Rust source"))?;
    let matches = syntax
        .items
        .into_iter()
        .filter_map(|item| match item {
            Item::Mod(module) if module.ident == "dagger_client" => Some(module),
            _ => None,
        })
        .collect::<Vec<_>>();
    if matches.is_empty() {
        return Ok(None);
    }
    let [module] = matches.as_slice() else {
        return Err(project_conflict(
            "rust-module.dagger_client",
            "owned dagger_client module item is missing or duplicated",
        ));
    };
    if !matches!(module.vis, Visibility::Public(_))
        || module.content.is_some()
        || !module.attrs.is_empty()
    {
        return Err(project_conflict(
            "rust-module.dagger_client",
            "owned dagger_client module item is incompatible",
        ));
    }
    Ok(Some(b"pub mod dagger_client;".to_vec()))
}

fn readme_semantic_value(bytes: &[u8]) -> Result<Vec<u8>, EngineDiagnostic> {
    readme_semantic_value_optional(bytes)?.ok_or_else(|| {
        project_conflict(
            "docs.dagger-client-quickstart-v1",
            "owned quickstart region is missing",
        )
    })
}

fn readme_semantic_value_optional(bytes: &[u8]) -> Result<Option<Vec<u8>>, EngineDiagnostic> {
    let source = std::str::from_utf8(bytes)
        .map_err(|_| project_conflict("README.md", "README must be UTF-8"))?;
    Ok(parse_readme_region(source)?.map(|(_, _, body)| body.into_bytes()))
}

fn vcs_semantic_value(key: &str, bytes: &[u8]) -> Result<Vec<u8>, EngineDiagnostic> {
    vcs_semantic_value_optional(key, bytes)?
        .ok_or_else(|| project_conflict(key, "owned VCS policy line is missing"))
}

fn vcs_semantic_value_optional(
    key: &str,
    bytes: &[u8],
) -> Result<Option<Vec<u8>>, EngineDiagnostic> {
    match key {
        "gitattributes.dagger-client-generated-root" | "gitignore.cargo-target" => {}
        _ => return Err(project_conflict(key, "unknown VCS amendment coordinate")),
    }
    let lines = bytes
        .split(|byte| *byte == b'\n')
        .map(|line| line.strip_suffix(b"\r").unwrap_or(line))
        .filter(|line| {
            let line = std::str::from_utf8(line).unwrap_or_default();
            match key {
                "gitattributes.dagger-client-generated-root" => line
                    .strip_suffix(" linguist-generated=true")
                    .is_some_and(|pattern| {
                        pattern == "dagger_client/**" || pattern.ends_with("/dagger_client/**")
                    }),
                "gitignore.cargo-target" => line == "target",
                _ => false,
            }
        })
        .collect::<Vec<_>>();
    if lines.is_empty() {
        return Ok(None);
    }
    let [line] = lines.as_slice() else {
        return Err(project_conflict(
            key,
            "owned VCS policy line is missing or duplicated",
        ));
    };
    Ok(Some(line.to_vec()))
}

fn child(
    root: &RelativeOperationPath,
    relative: &str,
) -> Result<RelativeOperationPath, EngineDiagnostic> {
    RelativeOperationPath::parse(&format!("{}/{relative}", root.as_str()))
        .map_err(|_| project_conflict(relative, "project child path is not confined and canonical"))
}

fn read_optional(
    root: &OperationRoot,
    path: RelativeOperationPath,
) -> Result<Option<AuthoredFile>, EngineDiagnostic> {
    if !root.exists(&path) {
        return Ok(None);
    }
    let bytes = root.read(&path)?;
    Ok(Some(AuthoredFile {
        path,
        digest: digest(&bytes),
        bytes,
    }))
}

fn utf8<'a>(
    path: &RelativeOperationPath,
    bytes: &'a [u8],
    message: &str,
) -> Result<&'a str, EngineDiagnostic> {
    std::str::from_utf8(bytes).map_err(|_| project_conflict(path.as_str(), message))
}

fn semantic_digest(kind: AmendmentKind, key: &str, value: &[u8]) -> Sha256Digest {
    let mut hasher = Sha256::new();
    hasher.update(b"dagger-rust-client-amendment-v1\0");
    hasher.update(format!("{kind:?}").as_bytes());
    hasher.update(b"\0");
    hasher.update(key.as_bytes());
    hasher.update(b"\0");
    hasher.update(value);
    format!("sha256:{:x}", hasher.finalize())
        .parse()
        .expect("SHA-256 formatting must satisfy the digest scalar")
}

fn digest(bytes: &[u8]) -> Sha256Digest {
    format!("sha256:{:x}", Sha256::digest(bytes))
        .parse()
        .expect("SHA-256 formatting must satisfy the digest scalar")
}

fn stable(value: &str) -> Result<StableCoordinate, EngineDiagnostic> {
    value
        .parse()
        .map_err(|_| project_conflict(value, "semantic amendment coordinate is invalid"))
}

fn normalize_package_component(value: &str) -> String {
    let mut normalized = String::new();
    let mut separated = false;
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() {
            normalized.push(char::from(byte.to_ascii_lowercase()));
            separated = false;
        } else if !normalized.is_empty() && !separated {
            normalized.push('-');
            separated = true;
        }
    }
    while normalized.ends_with('-') {
        normalized.pop();
    }
    normalized
}

fn sdk_conflict() -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::SdkDependencyConflict,
        Some("Cargo.toml::dependencies.dagger-sdk"),
        "existing dagger-sdk dependency differs from the immutable engine descriptor",
    )
}

fn sdk_mutable() -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::SdkDependencyMutable,
        Some("Cargo.toml::dependencies.dagger-sdk"),
        "dagger-sdk dependency must use an exact registry version or full Git revision",
    )
}

fn project_conflict(coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::ClientProjectConflict,
        Some(coordinate),
        message,
    )
}
