//! Failure-gated planning for new and existing Rust module projects.
//!
//! Initialization composes Cargo, exact-toolchain, starter-source, and VCS amendments
//! without invoking a renderer. It returns no publishable plan until the caller has
//! proved dependency resolution, keeping a failed lock operation changeset-free.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;

use crate::PostWorkPlan;
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::post_work::{Cancellation, current_allowlisted_environment, execute};
use crate::project::manifest::{CargoManifestPlan, plan_manifest};
use crate::project::toolchain::{ToolchainDeclaration, select_toolchain};
use crate::project::toolchain_declarations;
use crate::project::vcs::{append_missing_lines, generated_attributes, ignored_paths};
use crate::{
    EngineSourceDescriptor, ExecutionResult, ExecutionResultKind, FormatVersion,
    InitializationRequest, OperationRoot, PublishedSdkDependency, RelativeOperationPath,
    ToolchainSelection,
};

/// Authored project bytes observed before initialization planning.
pub struct InitializationInputs<'a> {
    /// Engine-selected module root.
    pub module_root: &'a RelativeOperationPath,
    /// Package name used only when creating a new manifest.
    pub package_name: &'a str,
    /// Existing selected package manifest, when present.
    pub manifest: Option<&'a [u8]>,
    /// Existing authored Rust target, when present.
    pub starter_source: Option<&'a [u8]>,
    /// Existing module-local ignore policy.
    pub gitignore: Option<&'a [u8]>,
    /// Existing module-local generated-file policy.
    pub gitattributes: Option<&'a [u8]>,
    /// Exact immutable public SDK source.
    pub dependency: &'a PublishedSdkDependency,
    /// Selected exact caller or target-default toolchain.
    pub toolchain: &'a ToolchainSelection,
    /// Dependency resolution gate completed by the confined Cargo runner.
    pub dependency_resolved: bool,
    /// Whether the selected workspace already had a committed lockfile.
    pub lockfile_present: bool,
}

/// One SDK-owned initialization candidate containing no generated renderer output.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct InitializationPlan {
    /// Complete changed files in normalized path order.
    pub files: BTreeMap<RelativeOperationPath, Vec<u8>>,
    /// Manifest planner result retained for Changeset projection.
    pub manifest: CargoManifestPlan,
    /// Regeneration command documented for each future generated artifact.
    pub regeneration: BTreeMap<RelativeOperationPath, &'static str>,
    /// Whether a minimal starter source is part of the candidate.
    pub starter_created: bool,
    /// Closed dependency-resolution action represented by this initialized project.
    pub post_work: Vec<PostWorkPlan>,
}

/// Composes a confined initialization candidate after dependency resolution succeeds.
pub fn plan_initialization(
    inputs: InitializationInputs<'_>,
) -> Result<InitializationPlan, EngineDiagnostic> {
    if !inputs.dependency_resolved {
        return Err(EngineDiagnostic::new(
            EngineDiagnosticCode::DependencyResolutionFailed,
            Some("initialization"),
            "dependency resolution must succeed before an initialization result exists",
        ));
    }
    let binary_relative = RelativeOperationPath::parse("src/bin/dagger-module.rs")
        .expect("fixed binary path must be canonical");
    let manifest = plan_manifest(
        inputs.manifest,
        inputs.package_name,
        inputs.dependency,
        &binary_relative,
    )?;
    let mut files = BTreeMap::new();
    let manifest_path = join(inputs.module_root, "Cargo.toml")?;
    if inputs.manifest != Some(manifest.rendered.as_slice()) {
        files.insert(manifest_path.clone(), manifest.rendered.clone());
    }

    let starter_path = join(inputs.module_root, "src/lib.rs")?;
    let starter_created = inputs.starter_source.is_none();
    if starter_created {
        files.insert(
            starter_path,
            b"//! Rust module entrypoint owned by the application.\n".to_vec(),
        );
    }

    if matches!(inputs.toolchain, ToolchainSelection::TargetDefault { .. }) {
        let toolchain = match inputs.toolchain {
            ToolchainSelection::TargetDefault { toolchain } => toolchain,
            ToolchainSelection::Declared { .. } => unreachable!("matched target default"),
        };
        files.insert(
            join(inputs.module_root, "rust-toolchain.toml")?,
            format!("[toolchain]\nchannel = \"{toolchain}\"\n").into_bytes(),
        );
    }

    let generated_local = BTreeSet::from([
        RelativeOperationPath::parse("src/dagger_generated")
            .expect("fixed generated path is canonical"),
        RelativeOperationPath::parse("src/bin/dagger-module.rs")
            .expect("fixed generated path is canonical"),
    ]);
    let ignored_local = BTreeSet::from([
        RelativeOperationPath::parse("target").expect("fixed ignored path is canonical")
    ]);
    let attributes_path = join(inputs.module_root, ".gitattributes")?;
    let attributes = append_missing_lines(
        inputs.gitattributes.unwrap_or_default(),
        &generated_attributes(&generated_local),
    );
    if inputs.gitattributes != Some(attributes.as_slice()) {
        files.insert(attributes_path, attributes);
    }
    let ignore_path = join(inputs.module_root, ".gitignore")?;
    let ignore = append_missing_lines(
        inputs.gitignore.unwrap_or_default(),
        &ignored_paths(&ignored_local),
    );
    if inputs.gitignore != Some(ignore.as_slice()) {
        files.insert(ignore_path, ignore);
    }
    if files.keys().any(|path| {
        matches!(
            path.as_str().rsplit('/').next(),
            Some("dagger.toml" | "dagger-module.toml")
        )
    }) {
        return Err(EngineDiagnostic::new(
            EngineDiagnosticCode::OperationInputInvalid,
            Some("initialization"),
            "engine-owned module configuration cannot be part of a Rust initialization plan",
        ));
    }
    let regeneration = generated_local
        .into_iter()
        .map(|path| join(inputs.module_root, path.as_str()).map(|path| (path, "dagger generate")))
        .collect::<Result<BTreeMap<_, _>, _>>()?;
    let post_work = vec![if inputs.lockfile_present
        && !manifest.dependency_changed
        && !manifest.runtime_dependency_changed
    {
        PostWorkPlan::VerifyLockedMetadata {
            manifest_path: manifest_path.clone(),
        }
    } else {
        PostWorkPlan::GenerateLockfile {
            manifest_path: manifest_path.clone(),
        }
    }];
    Ok(InitializationPlan {
        files,
        manifest,
        regeneration,
        starter_created,
        post_work,
    })
}

/// Executes one SDK-owned initialization in the caller's private Dagger snapshot.
///
/// The operation root is already an immutable-derived directory. The runner returns a
/// result only after Cargo lock generation or locked verification succeeds, so a failed
/// process cannot yield a mutation-capable Changeset to the engine.
pub async fn execute_initialization(
    root: &OperationRoot,
    request: &InitializationRequest,
    descriptor: &EngineSourceDescriptor,
    cancel: &Cancellation,
) -> Result<ExecutionResult, EngineDiagnostic> {
    validate_initialization_target(request, descriptor)?;
    let module_root = &request.module.source_subpath;
    ensure_directory(root, module_root)?;
    let manifest_path = join(module_root, "Cargo.toml")?;
    let manifest = root
        .exists(&manifest_path)
        .then(|| root.read(&manifest_path))
        .transpose()?;
    let starter_present = contains_rust_source(root, &join(module_root, "src")?)?;
    let gitignore_path = join(module_root, ".gitignore")?;
    let gitattributes_path = join(module_root, ".gitattributes")?;
    let gitignore = root
        .exists(&gitignore_path)
        .then(|| root.read(&gitignore_path))
        .transpose()?;
    let gitattributes = root
        .exists(&gitattributes_path)
        .then(|| root.read(&gitattributes_path))
        .transpose()?;
    let declarations = toolchain_declarations(root, module_root)?;
    let borrowed = declarations
        .iter()
        .map(|(path, bytes)| ToolchainDeclaration {
            path,
            bytes: bytes.as_slice(),
        })
        .collect::<Vec<_>>();
    let toolchain = select_toolchain(&borrowed)?;
    let lockfile_present = nearest_lockfile(root, module_root)?.is_some();
    let starter_marker = starter_present.then_some(&b"present"[..]);
    let plan = plan_initialization(InitializationInputs {
        module_root,
        package_name: request.package_name.as_str(),
        manifest: manifest.as_deref(),
        starter_source: starter_marker,
        gitignore: gitignore.as_deref(),
        gitattributes: gitattributes.as_deref(),
        dependency: &request.sdk_dependency,
        toolchain: &toolchain,
        dependency_resolved: true,
        lockfile_present,
    })?;
    for (path, bytes) in &plan.files {
        let parent = root.ensure_parent(path)?;
        let destination = parent.join(
            path.as_str()
                .rsplit('/')
                .next()
                .expect("validated relative path has a file name"),
        );
        fs::write(destination, bytes).map_err(|_| {
            EngineDiagnostic::new(
                EngineDiagnosticCode::PublicationFailed,
                Some(path.as_str()),
                "initialization candidate could not be staged",
            )
        })?;
    }
    for work in &plan.post_work {
        let outcome = execute(root, work, &current_allowlisted_environment(), cancel).await?;
        if !outcome.success {
            return Err(EngineDiagnostic::new(
                EngineDiagnosticCode::DependencyResolutionFailed,
                Some("initialization.cargo"),
                "Cargo dependency resolution failed; no initialization Changeset is available",
            ));
        }
    }
    Ok(ExecutionResult {
        format_version: FormatVersion,
        kind: ExecutionResultKind::Initialization,
        output_root: module_root.clone(),
        touched_paths: BTreeSet::new(),
        operation_manifest: None,
        vcs_generated: BTreeSet::new(),
        vcs_ignored: BTreeSet::new(),
        client_plan: None,
    })
}

fn validate_initialization_target(
    request: &InitializationRequest,
    descriptor: &EngineSourceDescriptor,
) -> Result<(), EngineDiagnostic> {
    descriptor.validate()?;
    let target = &request.target;
    if target.repository != descriptor.repository
        || target.dagger_revision != descriptor.dagger_revision
        || target.engine_version != descriptor.engine_version
        || target.rust_sdk_version != descriptor.rust_sdk_version
        || target.rust_toolchain != descriptor.rust_toolchain
        || target.core_schema_digest != descriptor.core_schema_digest
        || request.sdk_dependency != descriptor.sdk_dependency
    {
        return Err(EngineDiagnostic::new(
            EngineDiagnosticCode::OperationInputInvalid,
            Some("initialization.target"),
            "initialization request differs from the packaged immutable descriptor",
        ));
    }
    Ok(())
}

fn ensure_directory(
    root: &OperationRoot,
    path: &RelativeOperationPath,
) -> Result<(), EngineDiagnostic> {
    let marker = join(path, ".dagger-rust-initialization-parent")?;
    let parent = root.ensure_parent(&marker)?;
    if parent != path.join_lexically(root.absolute()) {
        return Err(EngineDiagnostic::new(
            EngineDiagnosticCode::OutputPathEscape,
            Some(path.as_str()),
            "initialization module root differs from its confined parent",
        ));
    }
    Ok(())
}

fn contains_rust_source(
    root: &OperationRoot,
    source_root: &RelativeOperationPath,
) -> Result<bool, EngineDiagnostic> {
    if !root.exists(source_root) {
        return Ok(false);
    }
    let start = root.resolve_existing(source_root)?;
    let mut pending = vec![start];
    while let Some(directory) = pending.pop() {
        for entry in fs::read_dir(&directory).map_err(|_| {
            EngineDiagnostic::new(
                EngineDiagnosticCode::OutputPathEscape,
                Some(source_root.as_str()),
                "authored source directory could not be enumerated",
            )
        })? {
            let entry = entry.map_err(|_| {
                EngineDiagnostic::new(
                    EngineDiagnosticCode::OutputPathEscape,
                    Some(source_root.as_str()),
                    "authored source entry could not be enumerated",
                )
            })?;
            let file_type = entry.file_type().map_err(|_| {
                EngineDiagnostic::new(
                    EngineDiagnosticCode::OutputPathEscape,
                    Some(source_root.as_str()),
                    "authored source metadata could not be read",
                )
            })?;
            if file_type.is_symlink() {
                return Err(EngineDiagnostic::new(
                    EngineDiagnosticCode::OutputSymlinkEscape,
                    Some(source_root.as_str()),
                    "authored source tree contains a symlink",
                ));
            }
            if file_type.is_dir() {
                pending.push(entry.path());
            } else if file_type.is_file()
                && entry
                    .path()
                    .extension()
                    .is_some_and(|extension| extension == "rs")
            {
                return Ok(true);
            }
        }
    }
    Ok(false)
}

fn nearest_lockfile(
    root: &OperationRoot,
    module_root: &RelativeOperationPath,
) -> Result<Option<RelativeOperationPath>, EngineDiagnostic> {
    let mut current = Some(module_root.as_str().to_owned());
    while let Some(directory) = current {
        let spelling = if directory.is_empty() {
            "Cargo.lock".to_owned()
        } else {
            format!("{directory}/Cargo.lock")
        };
        let path = RelativeOperationPath::parse(&spelling).map_err(|_| {
            EngineDiagnostic::new(
                EngineDiagnosticCode::OutputPathEscape,
                Some(module_root.as_str()),
                "lockfile search path is not canonical",
            )
        })?;
        if root.exists(&path) {
            return Ok(Some(path));
        }
        current = directory
            .rsplit_once('/')
            .map(|(parent, _)| parent.to_owned())
            .or_else(|| (!directory.is_empty()).then(String::new));
    }
    Ok(None)
}

fn join(
    root: &RelativeOperationPath,
    suffix: &str,
) -> Result<RelativeOperationPath, EngineDiagnostic> {
    RelativeOperationPath::parse(&format!("{}/{suffix}", root.as_str())).map_err(|_| {
        EngineDiagnostic::new(
            EngineDiagnosticCode::OutputPathEscape,
            Some(root.as_str()),
            "initialization path is not canonical",
        )
    })
}
