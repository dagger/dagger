#![deny(warnings)]
#![deny(unsafe_code)]
//! Private, engine-packaged orchestration for the Dagger Rust SDK.
//!
//! This crate owns the typed boundary between the engine adapter and Rust code
//! generation. Canonical models reject ambiguous paths, mutable dependency sources,
//! malformed identities, and unknown wire fields before later slices add filesystem,
//! process, or Dagger I/O.

pub mod canonical;
pub mod checkpoint;
pub mod cli;
pub mod client;
pub mod descriptor;
pub mod diagnostic;
pub mod initialization;
pub mod model;
pub mod packaging;
pub mod post_work;
pub mod project;
pub mod protocol;
pub mod publication;
pub mod root;
pub mod runner;
pub mod runtime;
pub mod scalar;
pub mod surface;

pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use checkpoint::{
    CheckpointAction, CheckpointActionObservation, CheckpointActionOutcome,
    CheckpointGenerationDecision, CheckpointObservation, CheckpointPackage, CheckpointPlan,
    CheckpointProposal, CheckpointRecord, CheckpointRequest, CheckpointTestTarget,
    ClientAssetDisposition, ClientCargoExpectation, ClientCheckedAssetState,
    ClientCheckpointActionObservation, ClientCheckpointObservation, ClientCheckpointPlan,
    ClientCheckpointRecord, ClientCheckpointRequest, DeferredSignoffException,
    ForbiddenCheckpointBoundary, ModuleProperty, PublicCheckpointPackage, RustGoAbiPackage,
    client_feature_end_checkpoint_actions, plan_checkpoint, plan_client_checkpoint,
    record_checkpoint, record_client_checkpoint,
};
pub use client::initialization::{execute_client_initialization, plan_client_initialization};
pub use client::project::{
    AmendmentCandidate, AuthoredFile, ClientDocumentationState, ClientProjectIdentityRequest,
    ClientProjectPlan, ClientProjectRequest, ClientProjectSnapshot, discover_client_project,
    reconcile_client_project, select_client_project_identity, semantic_amendment_digest,
};
pub use client::security::{ClientBoundaryArtifactKind, validate_client_boundary};
pub use client::workspace::{
    ClientOperationOutcome, ClientSetOutcome, admit_client_set, bind_client_module, plan_client_set,
};
pub use dagger_codegen::client::{CargoPackageName, RustIdentifier};
pub use diagnostic::{EngineDiagnostic, EngineDiagnosticCode};
pub use model::*;
pub use packaging::{
    DESCRIPTOR_PATH, PACKAGED_ASSET_MANIFEST_PATH, PackageIdentity, SecurityAuditGraph,
    SecuritySubject, SecuritySubjectKind, build_packaged_content, derive_shipped_audit_graph,
    validate_packaged_distribution, validate_packaged_source,
};
pub use project::source_snapshot::{
    MAX_SOURCE_FILE_BYTES, MAX_SOURCE_FILES, MAX_SOURCE_TOTAL_BYTES, SourceSnapshotBuilder,
    SourceSnapshotLimits, SourceSnapshotRequest,
};
pub use root::OperationRoot;
pub use runner::{OperationPostWork, PackagedPostWork, execute_operation_with_post_work};
pub use scalar::*;
