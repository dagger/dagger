//! Exact-target artifact planning, byte assembly, and admission.
//!
//! The portable artifact is a canonical tar containing sidecars and the real OCI payload. A
//! digest identifies those bytes but never substitutes for them: the only constructible verified
//! bundle has been assembled from, or decoded back into, the complete payload.

#![warn(missing_docs)]

use std::collections::{BTreeMap, BTreeSet};
use std::io::{Cursor, Read};

use serde::{Deserialize, Serialize};
use tar::{Archive, Builder, EntryType, Header};

use crate::canonical::{DigestDomain, canonical_bytes, canonical_digest, decode_canonical};
use crate::model::{CanonicalSet, CommitSha, Digest, TargetDigest};

use super::{
    ConformanceDiagnostic, ConformanceDiagnosticCode, ConformanceDiagnosticSet,
    ConformanceFormatVersion, DiagnosticCoordinate, DiagnosticPhase, PlatformDescriptor,
    ProvenanceId, ToolchainRole,
};

const MANIFEST_NAME: &str = "manifest.json";
const PROVENANCE_NAME: &str = "provenance.json";
const PAYLOAD_NAME: &str = "engine.oci.tar.zst";
const CHECKSUMS_NAME: &str = "checksums.sha256";
const BUNDLE_MEMBERS: [&str; 4] = [MANIFEST_NAME, PROVENANCE_NAME, PAYLOAD_NAME, CHECKSUMS_NAME];
const MAX_BUNDLE_BYTES: u64 = 64 * 1024 * 1024 * 1024;

/// Components whose immutable content must be accounted for by an exact-target artifact.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ArtifactComponent {
    /// Exact target engine binary and runtime content.
    Engine,
    /// Exact target command-line client.
    Cli,
    /// Mandatory Go runtime content embedded in the target image.
    GoRuntime,
    /// Packaged Rust SDK content installed into the target image.
    RustSdk,
}

/// Durable artifact format. Unknown versions fail during canonical decoding.
pub type ArtifactFormatVersion = ConformanceFormatVersion;

/// One component's independently checkable inputs, bytes, and provenance.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ArtifactComponentRecord {
    /// Component named by this record; it must equal the containing map key.
    pub component: ArtifactComponent,
    /// Identity of every semantic input used to produce the component.
    pub input_digest: Digest,
    /// Direct identity of the component bytes included in the payload.
    pub content_digest: Digest,
    /// Reviewed immutable inputs contributing to the component.
    pub provenance: CanonicalSet<ProvenanceId>,
}

/// Checked fork revision whose focused source is being evaluated.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SubjectRevisionObservation {
    /// Full immutable Git commit.
    pub revision: CommitSha,
    /// Focused source identity computed from the committed revision.
    pub focused_source_digest: Digest,
    /// Focused source identity computed from the workspace supplied to the builder.
    pub workspace_focused_source_digest: Digest,
    /// Whether the revision is reachable from the supplied repository object graph.
    pub reachable: bool,
    /// Whether the focused workspace contains no uncommitted mutation.
    pub clean: bool,
    /// Whether resolution used the full commit rather than a mutable ref.
    pub immutable: bool,
}

/// Complete byte-independent identity expected before construction or import begins.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ArtifactPlan {
    /// Durable plan format.
    pub format_version: ArtifactFormatVersion,
    /// Exact target descriptor selected for sign-off.
    pub target_descriptor_digest: TargetDigest,
    /// Engine/CLI target revision.
    pub target_revision: CommitSha,
    /// Exact fork revision and focused workspace proof.
    pub subject: SubjectRevisionObservation,
    /// Target platform; host platform is admitted separately.
    pub platform: PlatformDescriptor,
    /// Exact engine construction input identity.
    pub engine_input_digest: Digest,
    /// Exact CLI construction input identity.
    pub cli_input_digest: Digest,
    /// Mandatory Go runtime input identity.
    pub go_runtime_digest: Digest,
    /// Rust workspace manifest identity.
    pub rust_manifest_digest: Digest,
    /// Rust target descriptor identity.
    pub rust_descriptor_digest: Digest,
    /// Closed toolchain/base/scanner identities.
    pub toolchain_digests: BTreeMap<ToolchainRole, Digest>,
    /// Exact component records expected in the payload.
    pub components: BTreeMap<ArtifactComponent, ArtifactComponentRecord>,
    /// Canonical component provenance sidecar identity.
    pub provenance_digest: Digest,
    /// Exclusive materialization strategy.
    pub materialization: ArtifactMaterialization,
}

/// Exclusive artifact branch selected before graph construction.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ArtifactMaterialization {
    /// Construct and export the exact target once.
    Build,
    /// Verify and import one already materialized bundle.
    Import {
        /// Exact canonical manifest expected from the supplied bundle.
        manifest_digest: Digest,
        /// Exact OCI payload expected from the supplied bundle.
        payload_digest: Digest,
    },
}

/// Canonical manifest bound to every immutable artifact input and actual payload byte identity.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExactTargetArtifactManifest {
    /// Durable manifest format.
    pub format_version: ArtifactFormatVersion,
    /// Exact target descriptor selected for sign-off.
    pub target_descriptor_digest: TargetDigest,
    /// Engine/CLI target revision.
    pub target_revision: CommitSha,
    /// Fork revision whose Rust source is contained in the payload.
    pub subject_revision: CommitSha,
    /// Focused committed source identity proved by the subject observation.
    pub subject_source_digest: Digest,
    /// Exact target platform.
    pub platform: PlatformDescriptor,
    /// Exact engine construction input identity.
    pub engine_input_digest: Digest,
    /// Exact CLI construction input identity.
    pub cli_input_digest: Digest,
    /// Mandatory Go runtime input identity.
    pub go_runtime_digest: Digest,
    /// Rust workspace manifest identity.
    pub rust_manifest_digest: Digest,
    /// Rust target descriptor identity.
    pub rust_descriptor_digest: Digest,
    /// Closed toolchain/base/scanner identities.
    pub toolchain_digests: BTreeMap<ToolchainRole, Digest>,
    /// Exact byte-accounted component records.
    pub components: BTreeMap<ArtifactComponent, ArtifactComponentRecord>,
    /// Direct identity of `engine.oci.tar.zst`.
    pub payload_digest: Digest,
    /// Exact non-zero payload byte count.
    pub payload_size_bytes: u64,
    /// Canonical provenance sidecar identity.
    pub provenance_digest: Digest,
}

/// Canonical provenance sidecar kept separate from the target payload manifest.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ArtifactProvenanceDocument {
    /// Durable sidecar format.
    pub format_version: ArtifactFormatVersion,
    /// Provenance retained independently for every required component.
    pub components: BTreeMap<ArtifactComponent, CanonicalSet<ProvenanceId>>,
    /// Toolchain identities whose bytes are not inferred from ambient host state.
    pub toolchain_digests: BTreeMap<ToolchainRole, Digest>,
}

/// Events accepted by the two exclusive materialization automata.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ArtifactEvent {
    /// Build graph construction began.
    ConstructionStarted,
    /// One component build node was evaluated.
    ComponentBuilt {
        /// Exact component whose focused build node was evaluated.
        component: ArtifactComponent,
    },
    /// The constructed container was exported to OCI bytes.
    PayloadExported,
    /// A host-supplied bundle entered the import branch.
    BundleSupplied,
    /// Canonical manifest and plan identity were verified.
    ManifestVerified,
    /// Actual payload bytes and checksum were verified.
    PayloadVerified,
    /// Every component record was verified against the payload.
    ComponentsVerified,
    /// The verified payload was passed to the single container import site.
    ContainerImported,
    /// No further artifact work may occur after this terminal event.
    ArtifactReady,
}

/// Work outside the exact Rust sign-off artifact graph.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ForbiddenArtifactWork {
    /// A language SDK other than the mandatory Go runtime or Rust SDK was built.
    UnrelatedSdkBuild,
    /// Any foreign SDK test suite ran.
    UnrelatedSdkTest,
    /// The complete Go SDK suite ran instead of the mandatory focused build input.
    CompleteGoTestSuite,
    /// Repository-wide or otherwise unscoped generation ran.
    UnscopedGeneration,
    /// A distribution graph was evaluated.
    DistributionBuild,
    /// An error path changed from Build to Import or vice versa.
    StrategyFallback,
}

/// Counted work used to reject duplication even when the resulting bytes happen to match.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ArtifactCounters {
    /// Focused construction count.
    pub construction: u32,
    /// Verified container import count.
    pub imports: u32,
    /// Per-component build counts; all required keys remain visible even when zero.
    pub component_builds: BTreeMap<ArtifactComponent, u32>,
    /// Forbidden graph observations, retained as a set so any occurrence rejects admission.
    pub forbidden_work: CanonicalSet<ForbiddenArtifactWork>,
}

/// Complete typed observation supplied to artifact admission.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ArtifactObservation {
    /// Strategy selected by the evaluated graph.
    pub strategy: ArtifactMaterialization,
    /// Manifest reported by the graph; it must equal the decoded bundle manifest.
    pub manifest: ExactTargetArtifactManifest,
    /// Verified bundle retaining the actual payload bytes.
    pub bundle: VerifiedArtifactBundle,
    /// Ordered materialization history.
    pub events: Vec<ArtifactEvent>,
    /// Exact work counters.
    pub counters: ArtifactCounters,
    /// Component byte identities independently observed after build or import.
    pub verified_component_digests: BTreeMap<ArtifactComponent, Digest>,
    /// Positive bounded adapter duration.
    pub elapsed_millis: u64,
}

/// Canonical bundle that retains the complete portable archive and verified inner payload.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct VerifiedArtifactBundle {
    bytes: Vec<u8>,
    manifest: ExactTargetArtifactManifest,
    provenance: ArtifactProvenanceDocument,
    payload: Vec<u8>,
    bundle_digest: Digest,
    manifest_digest: Digest,
}

impl VerifiedArtifactBundle {
    /// Borrows the canonical exportable tar bytes.
    pub fn bytes(&self) -> &[u8] {
        &self.bytes
    }

    /// Borrows the actual verified OCI payload bytes.
    pub fn payload(&self) -> &[u8] {
        &self.payload
    }

    /// Borrows the canonical decoded manifest.
    pub fn manifest(&self) -> &ExactTargetArtifactManifest {
        &self.manifest
    }

    /// Borrows the canonical decoded provenance document.
    pub fn provenance(&self) -> &ArtifactProvenanceDocument {
        &self.provenance
    }

    /// Borrows the direct identity of every portable bundle byte.
    pub fn bundle_digest(&self) -> &Digest {
        &self.bundle_digest
    }

    /// Borrows the domain-separated canonical manifest identity.
    pub fn manifest_digest(&self) -> &Digest {
        &self.manifest_digest
    }
}

/// Artifact admitted for later engine startup without discarding its actual bytes.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AdmittedArtifact {
    /// Exact canonical manifest identity.
    manifest_digest: Digest,
    /// Exact OCI payload identity.
    payload_digest: Digest,
    /// Exact OCI payload size.
    payload_size_bytes: u64,
    /// Complete canonical bundle retained for export, import, or scanning.
    bundle: VerifiedArtifactBundle,
}

impl AdmittedArtifact {
    /// Borrows the exact canonical manifest identity.
    pub fn manifest_digest(&self) -> &Digest {
        &self.manifest_digest
    }

    /// Borrows the exact existing OCI payload identity.
    pub fn payload_digest(&self) -> &Digest {
        &self.payload_digest
    }

    /// Returns the exact existing OCI payload size.
    pub const fn payload_size_bytes(&self) -> u64 {
        self.payload_size_bytes
    }

    /// Borrows the complete canonical bundle and its actual payload bytes.
    pub fn bundle(&self) -> &VerifiedArtifactBundle {
        &self.bundle
    }
}

/// Assembles the sole canonical outer-tar representation from real sidecar and payload bytes.
pub fn assemble_artifact_bundle(
    manifest: ExactTargetArtifactManifest,
    provenance: ArtifactProvenanceDocument,
    payload: Vec<u8>,
) -> Result<VerifiedArtifactBundle, ConformanceDiagnosticSet> {
    validate_bundle_inputs(&manifest, &provenance, &payload)?;
    let manifest_bytes = canonical_bytes(&manifest).map_err(|_| bundle_error())?;
    let provenance_bytes = canonical_bytes(&provenance).map_err(|_| bundle_error())?;
    let checksums = checksum_document(&manifest_bytes, &provenance_bytes, &payload);
    let bytes = write_tar([
        (MANIFEST_NAME, manifest_bytes.as_slice()),
        (PROVENANCE_NAME, provenance_bytes.as_slice()),
        (PAYLOAD_NAME, payload.as_slice()),
        (CHECKSUMS_NAME, checksums.as_slice()),
    ])?;
    if bytes.len() as u64 > MAX_BUNDLE_BYTES {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactPayloadInvalid,
            "artifact bundle exceeds the portable size bound",
        ));
    }
    Ok(VerifiedArtifactBundle {
        bundle_digest: Digest::sha256(&bytes),
        manifest_digest: canonical_digest(DigestDomain::Artifact, &manifest)
            .map_err(|_| bundle_error())?,
        bytes,
        manifest,
        provenance,
        payload,
    })
}

/// Decodes only the canonical tar representation and re-verifies every actual member byte.
pub fn decode_artifact_bundle(
    bytes: &[u8],
) -> Result<VerifiedArtifactBundle, ConformanceDiagnosticSet> {
    if bytes.is_empty() || bytes.len() as u64 > MAX_BUNDLE_BYTES {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactPayloadInvalid,
            "artifact bundle is empty or exceeds the portable size bound",
        ));
    }
    let mut members = BTreeMap::<String, Vec<u8>>::new();
    let mut order = Vec::new();
    let mut archive = Archive::new(Cursor::new(bytes));
    let entries = archive.entries().map_err(|_| bundle_error())?;
    for entry in entries {
        let mut entry = entry.map_err(|_| bundle_error())?;
        let path = entry
            .path()
            .map_err(|_| bundle_error())?
            .to_str()
            .ok_or_else(bundle_error)?
            .to_owned();
        let header = entry.header();
        if header.entry_type() != EntryType::Regular
            || header.mode().ok() != Some(0o644)
            || header.uid().ok() != Some(0)
            || header.gid().ok() != Some(0)
            || header.mtime().ok() != Some(0)
        {
            return Err(bundle_error());
        }
        let mut content = Vec::new();
        entry
            .read_to_end(&mut content)
            .map_err(|_| bundle_error())?;
        order.push(path.clone());
        if members.insert(path, content).is_some() {
            return Err(bundle_error());
        }
    }
    if order != BUNDLE_MEMBERS || members.len() != BUNDLE_MEMBERS.len() {
        return Err(bundle_error());
    }
    let manifest_bytes = members.get(MANIFEST_NAME).ok_or_else(bundle_error)?;
    let provenance_bytes = members.get(PROVENANCE_NAME).ok_or_else(bundle_error)?;
    let payload = members.get(PAYLOAD_NAME).ok_or_else(bundle_error)?.clone();
    let manifest = decode_canonical(manifest_bytes).map_err(|_| bundle_error())?;
    let provenance = decode_canonical(provenance_bytes).map_err(|_| bundle_error())?;
    if members.get(CHECKSUMS_NAME)
        != Some(&checksum_document(
            manifest_bytes,
            provenance_bytes,
            &payload,
        ))
    {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactPayloadInvalid,
            "artifact member checksum does not match the retained bytes",
        ));
    }
    let canonical = assemble_artifact_bundle(manifest, provenance, payload)?;
    if canonical.bytes() != bytes {
        return Err(bundle_error());
    }
    Ok(canonical)
}

/// Admits a complete artifact only when plan, bytes, history, and counters agree exactly.
pub fn admit_artifact(
    plan: &ArtifactPlan,
    observation: ArtifactObservation,
) -> Result<AdmittedArtifact, ConformanceDiagnosticSet> {
    validate_plan(plan)?;
    if observation.elapsed_millis == 0
        || observation.manifest != *observation.bundle.manifest()
        || !manifest_matches_plan(&observation.manifest, plan)
    {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactManifestInvalid,
            "artifact observation does not match its admitted plan and bundle",
        ));
    }
    if observation.strategy != plan.materialization {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactStateInvalid,
            "artifact strategy differs from the branch selected by the plan",
        ));
    }
    validate_strategy(plan, &observation)?;
    let manifest_digest = observation.bundle.manifest_digest().clone();
    if let ArtifactMaterialization::Import {
        manifest_digest: expected_manifest,
        payload_digest: expected_payload,
    } = &plan.materialization
        && (expected_manifest != &manifest_digest
            || expected_payload != &observation.manifest.payload_digest)
    {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactImportFailed,
            "imported artifact identity differs from the planned immutable bytes",
        ));
    }
    Ok(AdmittedArtifact {
        manifest_digest,
        payload_digest: observation.manifest.payload_digest.clone(),
        payload_size_bytes: observation.manifest.payload_size_bytes,
        bundle: observation.bundle,
    })
}

/// Returns the exact component set required by artifact policy.
pub fn required_artifact_components() -> BTreeSet<ArtifactComponent> {
    BTreeSet::from([
        ArtifactComponent::Engine,
        ArtifactComponent::Cli,
        ArtifactComponent::GoRuntime,
        ArtifactComponent::RustSdk,
    ])
}

/// Returns the closed toolchain/base/scanner role set required by artifact policy.
pub fn required_artifact_toolchains() -> BTreeSet<ToolchainRole> {
    BTreeSet::from([
        ToolchainRole::ArtifactBuilder,
        ToolchainRole::EngineBase,
        ToolchainRole::RustToolchain,
        ToolchainRole::GoToolchain,
        ToolchainRole::ArtifactScanner,
    ])
}

/// Derives the canonical provenance sidecar from the already closed artifact plan.
pub fn artifact_provenance_document(
    plan: &ArtifactPlan,
) -> Result<ArtifactProvenanceDocument, ConformanceDiagnosticSet> {
    validate_plan(plan)?;
    Ok(provenance_from_plan(plan))
}

/// Binds an admitted plan to the actual payload bytes it produced or supplied.
pub fn artifact_manifest_for_payload(
    plan: &ArtifactPlan,
    payload: &[u8],
) -> Result<ExactTargetArtifactManifest, ConformanceDiagnosticSet> {
    validate_plan(plan)?;
    if payload.is_empty() {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactPayloadInvalid,
            "artifact payload bytes are required to construct the manifest",
        ));
    }
    Ok(ExactTargetArtifactManifest {
        format_version: plan.format_version,
        target_descriptor_digest: plan.target_descriptor_digest.clone(),
        target_revision: plan.target_revision.clone(),
        subject_revision: plan.subject.revision.clone(),
        subject_source_digest: plan.subject.focused_source_digest.clone(),
        platform: plan.platform.clone(),
        engine_input_digest: plan.engine_input_digest.clone(),
        cli_input_digest: plan.cli_input_digest.clone(),
        go_runtime_digest: plan.go_runtime_digest.clone(),
        rust_manifest_digest: plan.rust_manifest_digest.clone(),
        rust_descriptor_digest: plan.rust_descriptor_digest.clone(),
        toolchain_digests: plan.toolchain_digests.clone(),
        components: plan.components.clone(),
        payload_digest: Digest::sha256(payload),
        payload_size_bytes: payload.len() as u64,
        provenance_digest: plan.provenance_digest.clone(),
    })
}

fn validate_plan(plan: &ArtifactPlan) -> Result<(), ConformanceDiagnosticSet> {
    let subject_valid = plan.subject.reachable
        && plan.subject.clean
        && plan.subject.immutable
        && plan.subject.focused_source_digest == plan.subject.workspace_focused_source_digest;
    let components_valid = plan.components.keys().copied().collect::<BTreeSet<_>>()
        == required_artifact_components()
        && plan.components.iter().all(|(component, record)| {
            component == &record.component && !record.provenance.is_empty()
        });
    let tools_valid = plan
        .toolchain_digests
        .keys()
        .copied()
        .collect::<BTreeSet<_>>()
        == required_artifact_toolchains();
    let provenance = provenance_from_plan(plan);
    let provenance_valid = canonical_digest(DigestDomain::ConformanceSecurity, &provenance)
        .is_ok_and(|digest| digest == plan.provenance_digest);
    if subject_valid && components_valid && tools_valid && provenance_valid {
        Ok(())
    } else {
        Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactProvenanceInvalid,
            "artifact plan has incomplete mutable unreachable or mismatched provenance",
        ))
    }
}

fn manifest_matches_plan(manifest: &ExactTargetArtifactManifest, plan: &ArtifactPlan) -> bool {
    manifest.format_version == plan.format_version
        && manifest.target_descriptor_digest == plan.target_descriptor_digest
        && manifest.target_revision == plan.target_revision
        && manifest.subject_revision == plan.subject.revision
        && manifest.subject_source_digest == plan.subject.focused_source_digest
        && manifest.platform == plan.platform
        && manifest.engine_input_digest == plan.engine_input_digest
        && manifest.cli_input_digest == plan.cli_input_digest
        && manifest.go_runtime_digest == plan.go_runtime_digest
        && manifest.rust_manifest_digest == plan.rust_manifest_digest
        && manifest.rust_descriptor_digest == plan.rust_descriptor_digest
        && manifest.toolchain_digests == plan.toolchain_digests
        && manifest.components == plan.components
        && manifest.provenance_digest == plan.provenance_digest
}

fn validate_bundle_inputs(
    manifest: &ExactTargetArtifactManifest,
    provenance: &ArtifactProvenanceDocument,
    payload: &[u8],
) -> Result<(), ConformanceDiagnosticSet> {
    if payload.is_empty()
        || manifest.payload_size_bytes != payload.len() as u64
        || manifest.payload_digest != Digest::sha256(payload)
    {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactPayloadInvalid,
            "artifact payload bytes do not match the manifest identity and size",
        ));
    }
    let expected_components = manifest
        .components
        .iter()
        .map(|(component, record)| (*component, record.provenance.clone()))
        .collect::<BTreeMap<_, _>>();
    let provenance_digest = canonical_digest(DigestDomain::ConformanceSecurity, provenance)
        .map_err(|_| bundle_error())?;
    if provenance.format_version != manifest.format_version
        || provenance.components != expected_components
        || provenance.toolchain_digests != manifest.toolchain_digests
        || provenance_digest != manifest.provenance_digest
    {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffArtifactProvenanceInvalid,
            "artifact provenance sidecar does not match the manifest",
        ));
    }
    Ok(())
}

fn validate_strategy(
    plan: &ArtifactPlan,
    observation: &ArtifactObservation,
) -> Result<(), ConformanceDiagnosticSet> {
    let exact_component_keys = observation
        .counters
        .component_builds
        .keys()
        .copied()
        .collect::<BTreeSet<_>>()
        == required_artifact_components();
    let component_bytes_verified = observation.verified_component_digests
        == observation
            .manifest
            .components
            .iter()
            .map(|(component, record)| (*component, record.content_digest.clone()))
            .collect();
    let counts_valid = match plan.materialization {
        ArtifactMaterialization::Build => {
            observation.counters.construction == 1
                && observation.counters.imports == 0
                && observation
                    .counters
                    .component_builds
                    .values()
                    .all(|count| *count <= 1)
        }
        ArtifactMaterialization::Import { .. } => {
            observation.counters.construction == 0
                && observation.counters.imports == 1
                && observation
                    .counters
                    .component_builds
                    .values()
                    .all(|count| *count == 0)
        }
    };
    if !exact_component_keys
        || !component_bytes_verified
        || !counts_valid
        || !observation.counters.forbidden_work.is_empty()
        || !events_are_valid(
            &plan.materialization,
            &observation.events,
            &observation.counters,
        )
    {
        return Err(artifact_error(
            ConformanceDiagnosticCode::SignoffDuplicateWork,
            "artifact history contains duplicated mixed forbidden or out of order work",
        ));
    }
    Ok(())
}

fn events_are_valid(
    strategy: &ArtifactMaterialization,
    events: &[ArtifactEvent],
    counters: &ArtifactCounters,
) -> bool {
    let mut expected_components = BTreeMap::<ArtifactComponent, u32>::new();
    let fixed_tail: &[ArtifactEvent];
    let prefix_length;
    match strategy {
        ArtifactMaterialization::Build => {
            if events.first() != Some(&ArtifactEvent::ConstructionStarted) {
                return false;
            }
            let mut index = 1;
            while let Some(ArtifactEvent::ComponentBuilt { component }) = events.get(index) {
                *expected_components.entry(*component).or_default() += 1;
                index += 1;
            }
            prefix_length = index;
            fixed_tail = &[
                ArtifactEvent::PayloadExported,
                ArtifactEvent::ManifestVerified,
                ArtifactEvent::PayloadVerified,
                ArtifactEvent::ComponentsVerified,
                ArtifactEvent::ArtifactReady,
            ];
        }
        ArtifactMaterialization::Import { .. } => {
            prefix_length = 0;
            fixed_tail = &[
                ArtifactEvent::BundleSupplied,
                ArtifactEvent::ManifestVerified,
                ArtifactEvent::PayloadVerified,
                ArtifactEvent::ComponentsVerified,
                ArtifactEvent::ContainerImported,
                ArtifactEvent::ArtifactReady,
            ];
        }
    }
    let component_events_match = counters.component_builds.iter().all(|(component, count)| {
        expected_components.get(component).copied().unwrap_or(0) == *count
    });
    component_events_match && events.get(prefix_length..) == Some(fixed_tail)
}

fn provenance_from_plan(plan: &ArtifactPlan) -> ArtifactProvenanceDocument {
    ArtifactProvenanceDocument {
        format_version: plan.format_version,
        components: plan
            .components
            .iter()
            .map(|(component, record)| (*component, record.provenance.clone()))
            .collect(),
        toolchain_digests: plan.toolchain_digests.clone(),
    }
}

fn checksum_document(manifest: &[u8], provenance: &[u8], payload: &[u8]) -> Vec<u8> {
    let mut output = String::new();
    for (name, bytes) in [
        (MANIFEST_NAME, manifest),
        (PROVENANCE_NAME, provenance),
        (PAYLOAD_NAME, payload),
    ] {
        let digest = Digest::sha256(bytes);
        output.push_str(digest.as_str().trim_start_matches("sha256:"));
        output.push_str("  ");
        output.push_str(name);
        output.push('\n');
    }
    output.into_bytes()
}

fn write_tar<'a>(
    entries: impl IntoIterator<Item = (&'a str, &'a [u8])>,
) -> Result<Vec<u8>, ConformanceDiagnosticSet> {
    let mut builder = Builder::new(Vec::new());
    for (name, content) in entries {
        let mut header = Header::new_ustar();
        header.set_path(name).map_err(|_| bundle_error())?;
        header.set_entry_type(EntryType::Regular);
        header.set_size(content.len() as u64);
        header.set_mode(0o644);
        header.set_uid(0);
        header.set_gid(0);
        header.set_mtime(0);
        header.set_cksum();
        builder
            .append(&header, content)
            .map_err(|_| bundle_error())?;
    }
    builder.into_inner().map_err(|_| bundle_error())
}

fn artifact_error(
    code: ConformanceDiagnosticCode,
    detail: &'static str,
) -> ConformanceDiagnosticSet {
    ConformanceDiagnosticSet::new([ConformanceDiagnostic::new(
        code,
        DiagnosticCoordinate {
            phase: Some(DiagnosticPhase::Artifact),
            ..DiagnosticCoordinate::default()
        },
        detail,
    )])
    .expect("one artifact diagnostic is non-empty")
}

fn bundle_error() -> ConformanceDiagnosticSet {
    artifact_error(
        ConformanceDiagnosticCode::SignoffArtifactManifestInvalid,
        "artifact bundle is malformed or not in canonical byte form",
    )
}
