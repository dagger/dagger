//! Engine-free standalone-client initialization over one confined project snapshot.
//!
//! Initialization deliberately has no schema, renderer, Cargo process, lockfile, or
//! module-runtime path. It publishes only a validated no-bindings scaffold and returns
//! a result after the complete authored-file transaction succeeds.

use std::collections::{BTreeMap, BTreeSet};

use crate::client::project::{
    ClientDocumentationState, ClientProjectPlan, ClientProjectRequest, discover_client_project,
    reconcile_client_project,
};
use crate::publication::{AuthoredPublicationCandidate, publish, verify_authored_publication};
use crate::{
    ClientInitializationRequest, ClientProjectIdentity, EngineDiagnostic, EngineDiagnosticCode,
    EngineSourceDescriptor, ExecutionResult, ExecutionResultKind, FormatVersion, OperationRoot,
    RustIdentifier,
};

/// Plans one valid Cargo scaffold without generated bindings or external post-work.
pub fn plan_client_initialization(
    root: &OperationRoot,
    request: &ClientInitializationRequest,
    descriptor: &EngineSourceDescriptor,
) -> Result<ClientProjectPlan, EngineDiagnostic> {
    validate_request(request, descriptor)?;
    let snapshot = discover_client_project(root, &request.client_root)?;
    let package_name = snapshot.package_name.as_deref().map_or_else(
        || request.package_name.clone(),
        |name| {
            crate::CargoPackageName::new(name.to_owned())
                .expect("project discovery already validated the Cargo package name")
        },
    );
    let crate_name =
        RustIdentifier::new(package_name.as_str().replace('-', "_")).map_err(|_| {
            diagnostic(
                "client.package-name",
                "Cargo package name does not normalize to one Rust crate identifier",
            )
        })?;
    reconcile_client_project(
        &snapshot,
        &ClientProjectRequest {
            identity: ClientProjectIdentity {
                package_name,
                crate_name,
            },
            sdk_dependency: request.sdk_dependency.clone(),
            documentation: ClientDocumentationState::Initialized,
        },
    )
}

/// Executes client initialization as one failure-atomic authored-file transaction.
pub fn execute_client_initialization(
    root: &OperationRoot,
    request: &ClientInitializationRequest,
    descriptor: &EngineSourceDescriptor,
) -> Result<ExecutionResult, EngineDiagnostic> {
    let plan = plan_client_initialization(root, request, descriptor)?;
    let mut files = BTreeMap::new();
    for (coordinate, amendment) in &plan.amendments {
        let candidate = (
            amendment.prior_file_digest.clone(),
            amendment.complete_file_bytes.clone(),
        );
        match files.get(coordinate.file()) {
            Some(existing) if existing != &candidate => {
                return Err(diagnostic(
                    coordinate.file().as_str(),
                    "semantic amendments disagree on complete scaffold bytes",
                ));
            }
            Some(_) => {}
            None => {
                files.insert(coordinate.file().clone(), candidate);
            }
        }
    }
    for (path, content) in &plan.created_files {
        if files
            .insert(path.clone(), (None, content.clone()))
            .is_some()
        {
            return Err(diagnostic(
                path.as_str(),
                "created scaffold file overlaps a semantic amendment",
            ));
        }
    }
    let publication = verify_authored_publication(root, &AuthoredPublicationCandidate { files })?;
    let touched_paths = match publication {
        Some(publication) => publish(root, publication)?
            .changes
            .into_iter()
            .map(|change| change.path)
            .collect(),
        None => BTreeSet::new(),
    };
    Ok(ExecutionResult {
        format_version: FormatVersion,
        kind: ExecutionResultKind::ClientInitialization,
        output_root: request.client_root.clone(),
        touched_paths,
        operation_manifest: None,
        vcs_generated: BTreeSet::new(),
        vcs_ignored: BTreeSet::new(),
        client_plan: None,
    })
}

fn validate_request(
    request: &ClientInitializationRequest,
    descriptor: &EngineSourceDescriptor,
) -> Result<(), EngineDiagnostic> {
    descriptor.validate()?;
    if request.format_version != FormatVersion
        || request.target.repository != descriptor.repository
        || request.target.dagger_revision != descriptor.dagger_revision
        || request.target.engine_version != descriptor.engine_version
        || request.target.rust_sdk_version != descriptor.rust_sdk_version
        || request.target.rust_toolchain != descriptor.rust_toolchain
        || request.target.core_schema_digest != descriptor.core_schema_digest
        || request.sdk_dependency != descriptor.sdk_dependency
    {
        return Err(diagnostic(
            "client.initialization.target",
            "client initialization differs from the immutable engine descriptor",
        ));
    }
    Ok(())
}

fn diagnostic(coordinate: &str, message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::ClientInitializationInvalid,
        Some(coordinate),
        message,
    )
}
