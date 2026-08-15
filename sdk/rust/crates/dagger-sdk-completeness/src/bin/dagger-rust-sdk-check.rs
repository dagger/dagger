//! Ordinary build checks for the two public Rust packages and packaged engine content.

use std::collections::{BTreeMap, BTreeSet};
use std::ffi::OsStr;
use std::fs;
use std::io::Read;
use std::path::{Component, Path, PathBuf};
use std::process::{Command as ProcessCommand, ExitCode};

use clap::{Arg, Command, value_parser};
use flate2::read::GzDecoder;
use serde::Deserialize;
use thiserror::Error;
use toml_edit::{DocumentMut, Item, Value};

const SDK_PACKAGE: &str = "dagger-sdk";
const MACRO_PACKAGE: &str = "dagger-sdk-macros";
const MAX_ARCHIVE_BYTES: u64 = 128 * 1024 * 1024;
const MAX_MANIFEST_BYTES: u64 = 1024 * 1024;

#[derive(Debug)]
enum CheckCommand {
    /// Validate Cargo metadata and exactly two public package archives.
    Packages {
        workspace: PathBuf,
        packages: PathBuf,
    },
    /// Validate the Rust manifest selected by a completed engine.
    Engine {
        content: PathBuf,
        expected_rust_manifest: String,
        rust_manifest: String,
    },
    /// Safely unpack the two validated packages for an external consumer.
    Unpack {
        packages: PathBuf,
        destination: PathBuf,
    },
}

#[derive(Debug, Error)]
pub(crate) enum CheckError {
    #[error("Cargo metadata check failed: {0}")]
    Metadata(String),
    #[error("public package contract failed: {0}")]
    PackageContract(String),
    #[error("unsafe package archive: {0}")]
    UnsafeArchive(String),
    #[error("Rust engine content check failed: {0}")]
    EngineContract(String),
    #[error("ordinary build I/O failed: {0}")]
    Io(String),
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
struct CargoMetadata {
    packages: Vec<CargoPackage>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
struct CargoPackage {
    name: String,
    version: String,
    publish: Option<Vec<String>>,
    #[serde(default)]
    features: BTreeMap<String, Vec<String>>,
    #[serde(default)]
    dependencies: Vec<CargoDependency>,
    edition: String,
    rust_version: Option<String>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
struct CargoDependency {
    name: String,
    req: String,
    kind: Option<String>,
    source: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct MetadataView {
    pub(crate) packages: Vec<PackageView>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct PackageView {
    pub(crate) name: String,
    pub(crate) version: String,
    pub(crate) publishable: bool,
    pub(crate) features: BTreeMap<String, Vec<String>>,
    pub(crate) dependencies: Vec<DependencyView>,
    pub(crate) edition: String,
    pub(crate) rust_version: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct DependencyView {
    pub(crate) name: String,
    pub(crate) requirement: String,
    pub(crate) kind: Option<String>,
    pub(crate) source: Option<String>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct PackageManifestView {
    pub(crate) name: String,
    pub(crate) version: String,
    pub(crate) features: BTreeSet<String>,
    pub(crate) macro_dependency: Option<String>,
    pub(crate) sdk_dependency_present: bool,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct PackageArchive {
    pub(crate) file_name: String,
    pub(crate) root: String,
    pub(crate) files: BTreeSet<String>,
    pub(crate) manifest: PackageManifestView,
    pub(crate) safe: bool,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct PackageSet {
    pub(crate) version: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct EngineContractInput {
    pub(crate) expected_rust_manifest: String,
    pub(crate) rust_manifest: String,
    pub(crate) blobs: BTreeSet<String>,
}

fn main() -> ExitCode {
    match run(parse_cli()) {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{error}");
            ExitCode::FAILURE
        }
    }
}

fn parse_cli() -> CheckCommand {
    let path = || value_parser!(PathBuf);
    let matches = Command::new("dagger-rust-sdk-check")
        .about("Validate ordinary Dagger Rust SDK build outputs")
        .subcommand_required(true)
        .subcommand(
            Command::new("packages")
                .about("Validate Cargo metadata and exactly two public package archives")
                .arg(
                    Arg::new("workspace")
                        .long("workspace")
                        .required(true)
                        .value_parser(path()),
                )
                .arg(
                    Arg::new("packages")
                        .long("packages")
                        .required(true)
                        .value_parser(path()),
                ),
        )
        .subcommand(
            Command::new("engine")
                .about("Validate the Rust manifest selected by a completed engine")
                .arg(
                    Arg::new("content")
                        .long("content")
                        .required(true)
                        .value_parser(path()),
                )
                .arg(
                    Arg::new("expected-rust-manifest")
                        .long("expected-rust-manifest")
                        .required(true),
                )
                .arg(
                    Arg::new("rust-manifest")
                        .long("rust-manifest")
                        .required(true),
                ),
        )
        .subcommand(
            Command::new("unpack")
                .about("Safely unpack the two validated packages for an external consumer")
                .arg(
                    Arg::new("packages")
                        .long("packages")
                        .required(true)
                        .value_parser(path()),
                )
                .arg(
                    Arg::new("destination")
                        .long("destination")
                        .required(true)
                        .value_parser(path()),
                ),
        )
        .get_matches();

    match matches.subcommand() {
        Some(("packages", arguments)) => CheckCommand::Packages {
            workspace: arguments
                .get_one::<PathBuf>("workspace")
                .expect("required by clap")
                .clone(),
            packages: arguments
                .get_one::<PathBuf>("packages")
                .expect("required by clap")
                .clone(),
        },
        Some(("engine", arguments)) => CheckCommand::Engine {
            content: arguments
                .get_one::<PathBuf>("content")
                .expect("required by clap")
                .clone(),
            expected_rust_manifest: arguments
                .get_one::<String>("expected-rust-manifest")
                .expect("required by clap")
                .clone(),
            rust_manifest: arguments
                .get_one::<String>("rust-manifest")
                .expect("required by clap")
                .clone(),
        },
        Some(("unpack", arguments)) => CheckCommand::Unpack {
            packages: arguments
                .get_one::<PathBuf>("packages")
                .expect("required by clap")
                .clone(),
            destination: arguments
                .get_one::<PathBuf>("destination")
                .expect("required by clap")
                .clone(),
        },
        _ => unreachable!("clap requires a known subcommand"),
    }
}

fn run(command: CheckCommand) -> Result<(), CheckError> {
    match command {
        CheckCommand::Packages {
            workspace,
            packages,
        } => {
            let metadata = load_metadata(&workspace)?;
            let archives = load_archives(&packages)?;
            let package_set = validate_package_contract(&metadata, &archives)?;
            let _validated_version = package_set.version;
            Ok(())
        }
        CheckCommand::Engine {
            content,
            expected_rust_manifest,
            rust_manifest,
        } => {
            let blobs = blob_names(&content)?;
            validate_engine_contract(&EngineContractInput {
                expected_rust_manifest,
                rust_manifest,
                blobs,
            })
        }
        CheckCommand::Unpack {
            packages,
            destination,
        } => {
            let archives = load_archives(&packages)?;
            validate_archive_set(&archives)?;
            unpack_archives(&packages, &archives, &destination)
        }
    }
}

fn load_metadata(workspace: &Path) -> Result<MetadataView, CheckError> {
    let manifest = workspace.join("Cargo.toml");
    let output = ProcessCommand::new("cargo")
        .args([
            "metadata",
            "--no-deps",
            "--format-version",
            "1",
            "--locked",
            "--manifest-path",
        ])
        .arg(&manifest)
        .output()
        .map_err(|error| CheckError::Metadata(format!("cannot execute Cargo: {error}")))?;
    if !output.status.success() {
        return Err(CheckError::Metadata(format!(
            "Cargo exited with status {}",
            output.status
        )));
    }
    let metadata: CargoMetadata = serde_json::from_slice(&output.stdout)
        .map_err(|error| CheckError::Metadata(format!("invalid Cargo JSON: {error}")))?;
    Ok(MetadataView {
        packages: metadata
            .packages
            .into_iter()
            .map(|package| PackageView {
                name: package.name,
                version: package.version,
                publishable: package
                    .publish
                    .as_ref()
                    .is_none_or(|registries| !registries.is_empty()),
                features: package.features,
                dependencies: package
                    .dependencies
                    .into_iter()
                    .map(|dependency| DependencyView {
                        name: dependency.name,
                        requirement: dependency.req,
                        kind: dependency.kind,
                        source: dependency.source,
                    })
                    .collect(),
                edition: package.edition,
                rust_version: package.rust_version,
            })
            .collect(),
    })
}

pub(crate) fn validate_package_contract(
    metadata: &MetadataView,
    archives: &[PackageArchive],
) -> Result<PackageSet, CheckError> {
    let public = metadata
        .packages
        .iter()
        .filter(|package| package.publishable)
        .collect::<Vec<_>>();
    let public_names = public
        .iter()
        .map(|package| package.name.as_str())
        .collect::<BTreeSet<_>>();
    if public.len() != 2 || public_names != BTreeSet::from([MACRO_PACKAGE, SDK_PACKAGE]) {
        return Err(CheckError::PackageContract(
            "workspace must contain exactly the two public Rust packages".to_owned(),
        ));
    }

    let sdk = public
        .iter()
        .find(|package| package.name == SDK_PACKAGE)
        .expect("checked public package set contains SDK");
    let macros = public
        .iter()
        .find(|package| package.name == MACRO_PACKAGE)
        .expect("checked public package set contains macros");
    if sdk.version != macros.version {
        return Err(CheckError::PackageContract(
            "public package versions differ".to_owned(),
        ));
    }
    let expected_features = BTreeMap::from([
        ("default".to_owned(), vec!["gen".to_owned()]),
        ("gen".to_owned(), Vec::new()),
    ]);
    if sdk.features != expected_features {
        return Err(CheckError::PackageContract(
            "dagger-sdk features differ from the ordinary public surface".to_owned(),
        ));
    }
    let expected_requirement = format!("={}", sdk.version);
    let macro_edges = sdk
        .dependencies
        .iter()
        .filter(|dependency| {
            dependency.name == MACRO_PACKAGE
                && dependency.kind.is_none()
                && dependency.requirement == expected_requirement
        })
        .count();
    if macro_edges != 1 {
        return Err(CheckError::PackageContract(
            "dagger-sdk must have one exact normal macro dependency".to_owned(),
        ));
    }
    if sdk.dependencies.iter().any(|dependency| {
        dependency.requirement == "*"
            || dependency.source.as_ref().is_some_and(|source| {
                !source.starts_with("registry+https://github.com/rust-lang/crates.io-index")
            })
    }) {
        return Err(CheckError::PackageContract(
            "dagger-sdk contains a wildcard or unsupported registry dependency".to_owned(),
        ));
    }
    if macros.edition != "2024"
        || macros.rust_version.as_deref() != Some("1.97.1")
        || macros
            .dependencies
            .iter()
            .any(|dependency| dependency.name == SDK_PACKAGE)
    {
        return Err(CheckError::PackageContract(
            "macro package metadata differs from the public contract".to_owned(),
        ));
    }

    let package_set = validate_archive_set(archives)?;
    if package_set.version != sdk.version {
        return Err(CheckError::PackageContract(
            "package archives differ from workspace version".to_owned(),
        ));
    }
    Ok(package_set)
}

fn validate_archive_set(archives: &[PackageArchive]) -> Result<PackageSet, CheckError> {
    if archives.len() != 2 {
        return Err(CheckError::PackageContract(
            "package directory must contain exactly two archives".to_owned(),
        ));
    }
    let by_name = archives
        .iter()
        .map(|archive| (archive.manifest.name.as_str(), archive))
        .collect::<BTreeMap<_, _>>();
    if by_name.len() != 2
        || !by_name.contains_key(SDK_PACKAGE)
        || !by_name.contains_key(MACRO_PACKAGE)
    {
        return Err(CheckError::PackageContract(
            "package archives must be dagger-sdk and dagger-sdk-macros".to_owned(),
        ));
    }
    let sdk = by_name[SDK_PACKAGE];
    let macros = by_name[MACRO_PACKAGE];
    if !sdk.safe || !macros.safe {
        return Err(CheckError::UnsafeArchive(
            "archive entry escaped its package root".to_owned(),
        ));
    }
    if sdk.manifest.version != macros.manifest.version {
        return Err(CheckError::PackageContract(
            "archive versions differ".to_owned(),
        ));
    }
    let version = sdk.manifest.version.clone();
    for archive in [sdk, macros] {
        let expected_root = format!("{}-{version}", archive.manifest.name);
        let expected_file = format!("{expected_root}.crate");
        if archive.root != expected_root || archive.file_name != expected_file {
            return Err(CheckError::PackageContract(
                "archive filename or top-level root is not canonical".to_owned(),
            ));
        }
    }
    let sdk_required = [
        "Cargo.toml",
        "LICENSE",
        "README.md",
        "examples/first-pipeline/main.rs",
        "src/gen/mod.rs",
        "src/lib.rs",
    ];
    let macros_required = ["Cargo.toml", "README.md", "src/lib.rs"];
    if sdk_required
        .iter()
        .any(|required| !sdk.files.contains(*required))
        || macros_required
            .iter()
            .any(|required| !macros.files.contains(*required))
    {
        return Err(CheckError::PackageContract(
            "a public package omits required content".to_owned(),
        ));
    }
    if sdk.manifest.features != BTreeSet::from(["default".to_owned(), "gen".to_owned()]) {
        return Err(CheckError::PackageContract(
            "dagger-sdk archive features differ from the ordinary public surface".to_owned(),
        ));
    }
    let expected_requirement = format!("={version}");
    if sdk.manifest.macro_dependency.as_deref() != Some(expected_requirement.as_str())
        || macros.manifest.sdk_dependency_present
    {
        return Err(CheckError::PackageContract(
            "packaged public dependency edge is not exact and acyclic".to_owned(),
        ));
    }
    Ok(PackageSet { version })
}

fn load_archives(packages: &Path) -> Result<Vec<PackageArchive>, CheckError> {
    let mut paths = fs::read_dir(packages)
        .map_err(|error| CheckError::Io(format!("read package directory: {error}")))?
        .map(|entry| {
            entry
                .map(|entry| entry.path())
                .map_err(|error| CheckError::Io(format!("read package entry: {error}")))
        })
        .collect::<Result<Vec<_>, _>>()?;
    paths.retain(|path| path.extension() == Some(OsStr::new("crate")));
    paths.sort();
    paths
        .iter()
        .map(|path| read_archive(path))
        .collect::<Result<Vec<_>, _>>()
}

fn read_archive(path: &Path) -> Result<PackageArchive, CheckError> {
    let file_name = path
        .file_name()
        .and_then(OsStr::to_str)
        .ok_or_else(|| CheckError::UnsafeArchive("package filename is not UTF-8".to_owned()))?
        .to_owned();
    let file = fs::File::open(path)
        .map_err(|error| CheckError::Io(format!("open package {file_name}: {error}")))?;
    let mut archive = tar::Archive::new(GzDecoder::new(file));
    let mut roots = BTreeSet::new();
    let mut files = BTreeSet::new();
    let mut manifest = None;
    let mut total_size = 0_u64;
    let entries = archive
        .entries()
        .map_err(|error| CheckError::UnsafeArchive(format!("read {file_name}: {error}")))?;
    for entry in entries {
        let mut entry = entry
            .map_err(|error| CheckError::UnsafeArchive(format!("read {file_name}: {error}")))?;
        let entry_type = entry.header().entry_type();
        if !entry_type.is_file() && !entry_type.is_dir() {
            return Err(CheckError::UnsafeArchive(format!(
                "{file_name} contains a non-file entry"
            )));
        }
        let parts = confined_parts(&entry.path().map_err(|error| {
            CheckError::UnsafeArchive(format!("read path in {file_name}: {error}"))
        })?)?;
        if parts.len() < 2 {
            return Err(CheckError::UnsafeArchive(format!(
                "{file_name} contains an entry outside its package root"
            )));
        }
        roots.insert(parts[0].clone());
        let relative = parts[1..].join("/");
        if entry_type.is_dir() {
            continue;
        }
        total_size = total_size.saturating_add(entry.header().size().unwrap_or(u64::MAX));
        if total_size > MAX_ARCHIVE_BYTES || !files.insert(relative.clone()) {
            return Err(CheckError::UnsafeArchive(format!(
                "{file_name} is oversized or contains duplicate files"
            )));
        }
        if relative == "Cargo.toml" {
            let mut bytes = Vec::new();
            entry
                .by_ref()
                .take(MAX_MANIFEST_BYTES + 1)
                .read_to_end(&mut bytes)
                .map_err(|error| {
                    CheckError::UnsafeArchive(format!("read manifest in {file_name}: {error}"))
                })?;
            if bytes.len() as u64 > MAX_MANIFEST_BYTES {
                return Err(CheckError::UnsafeArchive(format!(
                    "manifest in {file_name} is oversized"
                )));
            }
            manifest = Some(parse_package_manifest(&bytes)?);
        }
    }
    if roots.len() != 1 {
        return Err(CheckError::UnsafeArchive(format!(
            "{file_name} must contain one top-level root"
        )));
    }
    Ok(PackageArchive {
        file_name,
        root: roots.into_iter().next().expect("checked one root"),
        files,
        manifest: manifest.ok_or_else(|| {
            CheckError::PackageContract("package archive omits Cargo.toml".to_owned())
        })?,
        safe: true,
    })
}

fn confined_parts(path: &Path) -> Result<Vec<String>, CheckError> {
    path.components()
        .map(|component| match component {
            Component::Normal(part) => part
                .to_str()
                .map(str::to_owned)
                .ok_or_else(|| CheckError::UnsafeArchive("archive path is not UTF-8".to_owned())),
            _ => Err(CheckError::UnsafeArchive(
                "archive path is absolute or contains traversal".to_owned(),
            )),
        })
        .collect()
}

fn parse_package_manifest(bytes: &[u8]) -> Result<PackageManifestView, CheckError> {
    let text = std::str::from_utf8(bytes)
        .map_err(|error| CheckError::PackageContract(format!("manifest is not UTF-8: {error}")))?;
    let document = text.parse::<DocumentMut>().map_err(|error| {
        CheckError::PackageContract(format!("packaged manifest is invalid TOML: {error}"))
    })?;
    let package = document
        .get("package")
        .and_then(Item::as_table)
        .ok_or_else(|| CheckError::PackageContract("manifest omits [package]".to_owned()))?;
    let name = package
        .get("name")
        .and_then(Item::as_str)
        .ok_or_else(|| CheckError::PackageContract("manifest omits package name".to_owned()))?
        .to_owned();
    let version = package
        .get("version")
        .and_then(Item::as_str)
        .ok_or_else(|| CheckError::PackageContract("manifest omits package version".to_owned()))?
        .to_owned();
    let features = document
        .get("features")
        .and_then(Item::as_table)
        .map(|table| table.iter().map(|(name, _)| name.to_owned()).collect())
        .unwrap_or_default();
    Ok(PackageManifestView {
        name,
        version,
        features,
        macro_dependency: dependency_requirement(&document, MACRO_PACKAGE),
        sdk_dependency_present: has_dependency(&document, SDK_PACKAGE),
    })
}

fn dependencies(document: &DocumentMut) -> Option<&toml_edit::Table> {
    document.get("dependencies").and_then(Item::as_table)
}

fn has_dependency(document: &DocumentMut, name: &str) -> bool {
    dependencies(document).is_some_and(|table| table.contains_key(name))
}

fn dependency_requirement(document: &DocumentMut, name: &str) -> Option<String> {
    let item = dependencies(document)?.get(name)?;
    if let Some(requirement) = item.as_str() {
        return Some(requirement.to_owned());
    }
    match item {
        Item::Table(table) => table
            .get("version")
            .and_then(Item::as_str)
            .map(str::to_owned),
        Item::Value(Value::InlineTable(table)) => table
            .get("version")
            .and_then(Value::as_str)
            .map(str::to_owned),
        _ => None,
    }
}

pub(crate) fn validate_engine_contract(input: &EngineContractInput) -> Result<(), CheckError> {
    if !canonical_sha256(&input.expected_rust_manifest)
        || !canonical_sha256(&input.rust_manifest)
        || input.expected_rust_manifest != input.rust_manifest
    {
        return Err(CheckError::EngineContract(
            "selected Rust manifest differs from EngineContent".to_owned(),
        ));
    }
    let encoded = input
        .rust_manifest
        .strip_prefix("sha256:")
        .expect("checked canonical digest");
    if !input.blobs.contains(encoded) {
        return Err(CheckError::EngineContract(
            "selected Rust manifest blob is absent".to_owned(),
        ));
    }
    Ok(())
}

fn canonical_sha256(value: &str) -> bool {
    value.strip_prefix("sha256:").is_some_and(|encoded| {
        encoded.len() == 64
            && encoded
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    })
}

fn blob_names(content: &Path) -> Result<BTreeSet<String>, CheckError> {
    let directory = content.join("blobs/sha256");
    fs::read_dir(&directory)
        .map_err(|error| CheckError::EngineContract(format!("read content blobs: {error}")))?
        .map(|entry| {
            let entry = entry.map_err(|error| {
                CheckError::EngineContract(format!("read content blob: {error}"))
            })?;
            entry
                .file_name()
                .into_string()
                .map_err(|_| CheckError::EngineContract("blob name is not UTF-8".to_owned()))
        })
        .collect()
}

fn unpack_archives(
    packages: &Path,
    archives: &[PackageArchive],
    destination: &Path,
) -> Result<(), CheckError> {
    if destination.exists()
        && fs::read_dir(destination)
            .map_err(|error| CheckError::Io(format!("read unpack destination: {error}")))?
            .next()
            .is_some()
    {
        return Err(CheckError::Io(
            "unpack destination must be empty".to_owned(),
        ));
    }
    fs::create_dir_all(destination)
        .map_err(|error| CheckError::Io(format!("create unpack destination: {error}")))?;
    for package in archives {
        let path = packages.join(&package.file_name);
        let file = fs::File::open(&path).map_err(|error| {
            CheckError::Io(format!("open package {}: {error}", package.file_name))
        })?;
        let mut archive = tar::Archive::new(GzDecoder::new(file));
        let target_root = destination.join(&package.manifest.name);
        fs::create_dir_all(&target_root)
            .map_err(|error| CheckError::Io(format!("create package root: {error}")))?;
        for entry in archive.entries().map_err(|error| {
            CheckError::UnsafeArchive(format!("read {}: {error}", package.file_name))
        })? {
            let mut entry = entry.map_err(|error| {
                CheckError::UnsafeArchive(format!("read {}: {error}", package.file_name))
            })?;
            let entry_type = entry.header().entry_type();
            if !entry_type.is_file() && !entry_type.is_dir() {
                return Err(CheckError::UnsafeArchive(format!(
                    "{} contains a non-file entry",
                    package.file_name
                )));
            }
            let parts = confined_parts(&entry.path().map_err(|error| {
                CheckError::UnsafeArchive(format!("read {} path: {error}", package.file_name))
            })?)?;
            if parts.len() < 2 || parts[0] != package.root {
                return Err(CheckError::UnsafeArchive(format!(
                    "{} changed root while unpacking",
                    package.file_name
                )));
            }
            let output = parts[1..]
                .iter()
                .fold(target_root.clone(), |path, part| path.join(part));
            if entry_type.is_dir() {
                fs::create_dir_all(&output).map_err(|error| {
                    CheckError::Io(format!("create package directory: {error}"))
                })?;
                continue;
            }
            if let Some(parent) = output.parent() {
                fs::create_dir_all(parent).map_err(|error| {
                    CheckError::Io(format!("create package parent directory: {error}"))
                })?;
            }
            let mut file = fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .open(&output)
                .map_err(|error| CheckError::Io(format!("create package file: {error}")))?;
            std::io::copy(&mut entry, &mut file)
                .map_err(|error| CheckError::Io(format!("write package file: {error}")))?;
        }
    }
    Ok(())
}
