//! Failure-gated planning for new and existing Rust module projects.
//!
//! Initialization composes Cargo, exact-toolchain, starter-source, and VCS amendments
//! without invoking a renderer. It returns no publishable plan until the caller has
//! proved dependency resolution, keeping a failed lock operation changeset-free.

use std::collections::{BTreeMap, BTreeSet};

use crate::PostWorkPlan;
use crate::diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
use crate::project::manifest::{CargoManifestPlan, plan_manifest};
use crate::project::vcs::{append_missing_lines, generated_attributes, ignored_paths};
use crate::{PublishedSdkDependency, RelativeOperationPath, ToolchainSelection};

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

    let generated = BTreeSet::from([
        join(inputs.module_root, "src/dagger_generated")?,
        join(inputs.module_root, "src/bin/dagger-module.rs")?,
    ]);
    let ignored = BTreeSet::from([join(inputs.module_root, "target")?]);
    let attributes_path = join(inputs.module_root, ".gitattributes")?;
    let attributes = append_missing_lines(
        inputs.gitattributes.unwrap_or_default(),
        &generated_attributes(&generated),
    );
    if inputs.gitattributes != Some(attributes.as_slice()) {
        files.insert(attributes_path, attributes);
    }
    let ignore_path = join(inputs.module_root, ".gitignore")?;
    let ignore = append_missing_lines(
        inputs.gitignore.unwrap_or_default(),
        &ignored_paths(&ignored),
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
    let regeneration = generated
        .into_iter()
        .map(|path| (path, "dagger generate"))
        .collect();
    let post_work = vec![if inputs.lockfile_present && !manifest.dependency_changed {
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
