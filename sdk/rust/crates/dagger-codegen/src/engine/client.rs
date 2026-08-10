//! Bounded standalone-client renderer for hook-level engine conformance.

use crate::diagnostic::DiagnosticSet;

use super::model::{CandidateArtifactKind, ContentDomain, PostWorkPlan, PublishedSdkDependency};
use super::render::{checked_source, rust_source_paths, visible_binding_artifacts};
use super::renderers::{ClientRenderInput, RendererOutput};

pub(crate) fn render(
    input: ClientRenderInput<'_>,
    metadata: &super::metadata::ClientGenerationMetadata,
) -> Result<RendererOutput, DiagnosticSet> {
    let mut output = RendererOutput::new(ContentDomain::EngineHookBaseline);
    let generated_root = input.output.join("src/dagger_generated")?;
    output.artifacts = visible_binding_artifacts(input.schema, &generated_root)?;
    let manifest = cargo_manifest(&input)?;
    output.insert_artifact(
        input.output.join("Cargo.toml")?,
        CandidateArtifactKind::CargoManifest,
        manifest.into_bytes(),
    )?;
    let library = checked_source(
        "src/lib.rs",
        "//! Engine-hook baseline client over generated visible-schema bindings.\n\npub mod dagger_generated;\npub use dagger_generated::*;\n"
            .to_owned(),
    )?;
    output.insert_artifact(
        input.output.join("src/lib.rs")?,
        CandidateArtifactKind::RustSource,
        library,
    )?;
    let rust_files = rust_source_paths(&output.artifacts);
    output.vcs_generated.extend(rust_files.iter().cloned());
    output
        .post_work
        .push(PostWorkPlan::FormatRust { files: rust_files });
    output.client_generation = Some(metadata.clone());
    Ok(output)
}

fn cargo_manifest(input: &ClientRenderInput<'_>) -> Result<String, DiagnosticSet> {
    let dependency = match input.sdk_dependency {
        PublishedSdkDependency::Registry {
            registry,
            exact_version,
        } if registry == "crates-io" => format!(
            "{{ version = {} }}",
            toml_string(&format!("={exact_version}"))?
        ),
        PublishedSdkDependency::Registry {
            registry,
            exact_version,
        } => format!(
            "{{ version = {}, registry = {} }}",
            toml_string(&format!("={exact_version}"))?,
            toml_string(registry)?
        ),
        PublishedSdkDependency::Git { url, revision } => format!(
            "{{ git = {}, rev = {} }}",
            toml_string(url)?,
            toml_string(revision)?
        ),
    };
    Ok(format!(
        "[package]\nname = \"dagger-rust-client\"\nversion = \"0.0.0\"\npublish = false\nedition = {:?}\nrust-version = {:?}\n\n[package.metadata.dagger]\ncontent-domain = \"engine-hook-baseline\"\nvisible-schema-digest = {:?}\nmodule-source-digest = {:?}\n\n[dependencies]\ndagger-sdk = {dependency}\n",
        input.target.rust_edition().as_str(),
        input.target.rust_version().to_string(),
        input.schema.digest().as_str(),
        input.module.source_digest,
    ))
}

fn toml_string(value: &str) -> Result<String, DiagnosticSet> {
    serde_json::to_string(value).map_err(|_| {
        DiagnosticSet::one(super::model::operation_diagnostic(
            crate::diagnostic::DiagnosticCode::GeneratedProvenanceInvalid,
            "Cargo.toml",
            "Cargo dependency value could not be encoded",
        ))
    })
}
