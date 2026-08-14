//! Reviewed feature contracts consumed by completeness scope validation.
//!
//! Scope parsing is deliberately parameterized by immutable reviewed data. Adding a feature must
//! not change parser semantics or weaken an earlier declaration; it contributes only another exact
//! heading, capability set, digest, policy inventory, and prior-owner map.

use std::collections::BTreeMap;

use crate::model::{CanonicalSet, CapabilityId, Digest, FeatureId, RepositoryId};

/// One exact Rust policy statement and the durable guidance clause that constrains its shape.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ReviewedPolicyClause {
    /// Stable suffix used by the Rust-policy source and capability identities.
    pub clause_id: &'static str,
    /// Exact authoritative statement selected from the approved requirements.
    pub exact_text: &'static str,
    /// Rust guidance source combined with the requirement in the capability fingerprint.
    pub guidance_id: &'static str,
}

/// Exact declaration grammar and ownership expectations for one approved delivery scope.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FeatureScopePolicy {
    /// Feature whose specification owns this declaration.
    pub feature: FeatureId,
    /// Unique Markdown heading containing the existing capability fence.
    pub existing_scope_heading: &'static str,
    /// Unique Markdown heading containing the new policy capability fence.
    pub policy_scope_heading: &'static str,
    /// Exact existing status rows admitted by this feature.
    pub existing_capability_ids: CanonicalSet<CapabilityId>,
    /// Reviewed digest of the compact ordered existing-ID array.
    pub existing_scope_digest: Digest,
    /// Exact policy rows introduced by this feature.
    pub policy_capability_ids: CanonicalSet<CapabilityId>,
    /// Blocking owner required before this feature may transition each scoped row.
    pub expected_prior_blocking_owners: BTreeMap<CapabilityId, FeatureId>,
    /// Repository from which implementation and verification evidence may be admitted.
    pub evidence_repository: RepositoryId,
}

impl FeatureScopePolicy {
    /// Returns every status and policy capability in canonical order.
    pub fn capability_ids(&self) -> CanonicalSet<CapabilityId> {
        CanonicalSet::new(
            self.existing_capability_ids
                .iter()
                .chain(self.policy_capability_ids.iter())
                .cloned(),
        )
    }
}

/// Complete reviewed inputs needed to extract and route one feature contract.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FeatureContractPolicy {
    /// Repository-relative approved requirements source.
    pub requirements_path: &'static str,
    /// Scope declaration and transition ownership policy.
    pub scope: FeatureScopePolicy,
    /// Exact policy statements extracted from the requirements source.
    pub policy_clauses: &'static [ReviewedPolicyClause],
}

/// Returns the already-delivered client-lifecycle contract without interpreting its Markdown.
pub fn client_lifecycle_contract() -> FeatureContractPolicy {
    contract(
        FeatureId::Feature2,
        ".kiro/specs/rust-sdk-client-lifecycle/requirements.md",
        "### Existing Capability_IDs Whose Status Feature 2 Intends to Change",
        FEATURE2_EXISTING_IDS,
        "sha256:81ad1a4f2efe1604b9091468bd6a6006d598a2a8ae54a94a974acf08d74b8b40",
        FEATURE2_POLICY_IDS,
        FEATURE2_POLICIES,
        &[],
    )
}

/// Returns the approved transport-and-observability contract.
pub fn transport_contract() -> FeatureContractPolicy {
    contract(
        FeatureId::Feature3,
        ".kiro/specs/rust-sdk-transport-observability/requirements.md",
        "### Existing Capability_IDs Whose Status Feature 3 Intends to Change",
        FEATURE3_EXISTING_IDS,
        "sha256:0b4246157f75b8ce179d8fec3476256fa939ccdf69d29d1fcafaf93f160013b3",
        FEATURE3_POLICY_IDS,
        FEATURE3_POLICIES,
        FEATURE3_PRIOR_FEATURE2_IDS,
    )
}

/// Returns the approved built-in engine-integration contract.
pub fn engine_integration_contract() -> FeatureContractPolicy {
    let existing_capability_ids = reviewed_ids(FEATURE5_EXISTING_IDS);
    let policy_capability_ids = reviewed_ids(FEATURE5_POLICY_IDS);
    let expected_prior_blocking_owners = existing_capability_ids
        .iter()
        .chain(policy_capability_ids.iter())
        .cloned()
        .map(|capability_id| (capability_id, FeatureId::Feature5))
        .collect();

    FeatureContractPolicy {
        requirements_path: ".kiro/specs/rust-sdk-engine-integration/requirements.md",
        scope: FeatureScopePolicy {
            feature: FeatureId::Feature5,
            // Store the exhaustive existing set in one machine-readable policy. The
            // prose heading remains an audit anchor rather than a second list that could
            // drift independently.
            existing_scope_heading: "### Existing Feature 5 Scope",
            policy_scope_heading: "### Rust Policy Capabilities Added by Feature 5",
            existing_capability_ids,
            existing_scope_digest: Digest::new(
                "sha256:1f502e06f809fcfd90a8b9a3912eece3384585ad5c88963fac7681acb79c8cb3",
            )
            .expect("reviewed engine-integration scope digest must be valid"),
            policy_capability_ids,
            expected_prior_blocking_owners,
            evidence_repository: RepositoryId::new("github.com/dagger/dagger")
                .expect("reviewed evidence repository must be valid"),
        },
        policy_clauses: FEATURE5_POLICIES,
    }
}

/// Returns the approved umbrella conformance and security contract.
pub fn conformance_security_contract() -> FeatureContractPolicy {
    let reviewed: crate::conformance::ReviewedConformanceScope =
        serde_json::from_str(include_str!("../../../completeness/conformance-scope.json"))
            .expect("checked conformance scope artifact must decode");
    let existing_capability_ids = reviewed.existing_capability_ids;
    let policy_capability_ids = reviewed.policy_capability_ids;
    let expected_prior_blocking_owners = existing_capability_ids
        .iter()
        .chain(policy_capability_ids.iter())
        .cloned()
        .map(|capability_id| (capability_id, FeatureId::Feature8))
        .collect();

    FeatureContractPolicy {
        requirements_path: ".kiro/specs/rust-sdk-conformance-security/requirements.md",
        scope: FeatureScopePolicy {
            feature: FeatureId::Feature8,
            existing_scope_heading: "### Existing Feature 8 Scope",
            policy_scope_heading: "### New Rust Policy Capabilities",
            existing_capability_ids,
            existing_scope_digest: reviewed.existing_scope_digest,
            policy_capability_ids,
            expected_prior_blocking_owners,
            evidence_repository: RepositoryId::new("github.com/dagger/dagger")
                .expect("reviewed evidence repository must be valid"),
        },
        policy_clauses: FEATURE8_POLICIES,
    }
}

/// Returns every reviewed feature contract in delivery order.
pub fn reviewed_feature_contracts() -> [FeatureContractPolicy; 4] {
    [
        client_lifecycle_contract(),
        transport_contract(),
        engine_integration_contract(),
        conformance_security_contract(),
    ]
}

#[allow(clippy::too_many_arguments)]
fn contract(
    feature: FeatureId,
    requirements_path: &'static str,
    existing_scope_heading: &'static str,
    existing_ids: &[&str],
    existing_digest: &str,
    policy_ids: &[&str],
    policy_clauses: &'static [ReviewedPolicyClause],
    prior_feature2_ids: &[&str],
) -> FeatureContractPolicy {
    let existing_capability_ids = reviewed_ids(existing_ids);
    let policy_capability_ids = reviewed_ids(policy_ids);
    let mut expected_prior_blocking_owners = existing_capability_ids
        .iter()
        .chain(policy_capability_ids.iter())
        .cloned()
        .map(|capability_id| (capability_id, feature.clone()))
        .collect::<BTreeMap<_, _>>();
    for capability_id in reviewed_ids(prior_feature2_ids).iter() {
        expected_prior_blocking_owners.insert(capability_id.clone(), FeatureId::Feature2);
    }
    FeatureContractPolicy {
        requirements_path,
        scope: FeatureScopePolicy {
            feature,
            existing_scope_heading,
            policy_scope_heading: "### Omitted Policy_Capabilities to Add and Complete",
            existing_capability_ids,
            existing_scope_digest: Digest::new(existing_digest)
                .expect("reviewed feature scope digest must be valid"),
            policy_capability_ids,
            expected_prior_blocking_owners,
            evidence_repository: RepositoryId::new("github.com/dagger/dagger")
                .expect("reviewed evidence repository must be valid"),
        },
        policy_clauses,
    }
}

fn reviewed_ids(ids: &[&str]) -> CanonicalSet<CapabilityId> {
    CanonicalSet::new(ids.iter().map(|id| {
        CapabilityId::new(*id).expect("reviewed feature capability identity must be valid")
    }))
}

const FEATURE2_EXISTING_IDS: &[&str] = &[
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2543onnect",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43oad%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43og%254%46utput",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2543onn",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2545nvironment%2556ariable",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2552unner%2548ost",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2553kip%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556erbosity",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556ersion%254%46verride",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkdir",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkspace",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2543lose",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2544o",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2547raph%2551%254%43%2543lient",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2551uery%2542uilder",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%254%43oad%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%2557orkspace",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2543lient",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2543lient%254%46pt",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2552equest",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2552esponse",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2543onfig",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2545ngine%2543onn",
];

const FEATURE2_POLICY_IDS: &[&str] = &[
    "policy/rust-policy/client-beta-config-migration",
    "policy/rust-policy/client-cancelled-connect-cleanup",
    "policy/rust-policy/client-close-idempotency",
    "policy/rust-policy/client-closed-operation-rejection",
    "policy/rust-policy/client-drop-cleanup",
    "policy/rust-policy/client-http-connect-timeout",
    "policy/rust-policy/client-owned-lifecycle",
    "policy/rust-policy/client-preflight-validation",
    "policy/rust-policy/client-public-surface-encapsulation",
    "policy/rust-policy/client-query-execution-timeout",
    "policy/rust-policy/client-reserved-environment",
    "policy/rust-policy/client-secret-redaction",
    "policy/rust-policy/client-session-startup-timeout",
    "policy/rust-policy/client-shared-handle-safety",
];

const FEATURE2_POLICIES: &[ReviewedPolicyClause] = &[
    clause(
        "client-beta-config-migration",
        "The stable Client_Config removes beta unit-encoded timeout and project-path fields.",
        "idiomatic-rust",
    ),
    clause(
        "client-cancelled-connect-cleanup",
        "A cancelled or failed connection attempt cannot leak an owned child process or I/O task.",
        "panic-free-library",
    ),
    clause(
        "client-close-idempotency",
        "Every Client_Handle observes one single-flight close attempt and one terminal result.",
        "explicit-ownership",
    ),
    clause(
        "client-closed-operation-rejection",
        "Operations admitted after close begins fail without reaching the transport.",
        "typed-public-errors",
    ),
    clause(
        "client-drop-cleanup",
        "Dropping the final Client_Handle initiates non-blocking best-effort cleanup with an abort backstop.",
        "panic-free-library",
    ),
    clause(
        "client-http-connect-timeout",
        "HTTP connection establishment has its own positive Duration timeout.",
        "typed-public-errors",
    ),
    clause(
        "client-owned-lifecycle",
        "Successful connection establishment returns an owned Client which is not callback-scoped.",
        "explicit-ownership",
    ),
    clause(
        "client-preflight-validation",
        "Configuration conflicts and local validation failures precede external work.",
        "panic-free-library",
    ),
    clause(
        "client-public-surface-encapsulation",
        "Public client and generated handles hide transports, processes, credentials, and synchronization state.",
        "secret-safe-output",
    ),
    clause(
        "client-query-execution-timeout",
        "One optional positive Duration bounds a complete GraphQL request without closing the Client.",
        "typed-public-errors",
    ),
    clause(
        "client-reserved-environment",
        "Additional environment rejects reserved keys using ASCII case-insensitive comparison.",
        "secret-safe-output",
    ),
    clause(
        "client-secret-redaction",
        "Ordinary errors, diagnostics, tracing, and Debug output never disclose session tokens or environment values.",
        "secret-safe-output",
    ),
    clause(
        "client-session-startup-timeout",
        "Newly selected connection establishment has its own positive Duration timeout.",
        "typed-public-errors",
    ),
    clause(
        "client-shared-handle-safety",
        "Client clones and generated handles share one lifecycle and remain Send plus Sync without unsafe code.",
        "unsafe-denied",
    ),
];

const FEATURE3_EXISTING_IDS: &[&str] = &[
    "behavior/go-client/source%2Fgo-client%2Fgo-const%2Fengineconn%2F%2543%254%43%2549%2556ersion",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2543onnect",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43oad%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43og%254%46utput",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2545nvironment%2556ariable",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2552unner%2548ost",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2553kip%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556erbosity",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556ersion%254%46verride",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkdir",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkspace",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2546rom%254%43ocal%2543%254%43%2549",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2546rom%2544ownloaded%2543%254%43%2549",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2546rom%2553ession%2545nv",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2547et",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2543lose",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fengineconn%2F%2543%254%43%2549%2544ownloader%2F%2544ownload",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fengineconn%2F%2552ound%2554ripper%2546unc%2F%2552ound%2554rip",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%254%44issing%2541rchive%2544oes%254%45ot%2546allback",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%254%45o%2546allback%2554o%254%43ocal%2543%254%43%2549%2546or%254%46ther%2545rrors",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2543%254%43%2549%2553ession%2541rgs%2549nclude%254%43oad%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2543%254%43%2549%2553ession%2541rgs%2549nclude%2557orkspace",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2543hecksum%254%44ap%254%44arks%2555navailable",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2546allback%2554o%254%43ocal%2543%254%43%2549",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2547et%2552ejects%2557orkspace%254%44odule%254%43oading%2546or%2545xisting%2553ession",
    "behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2547et%2552ejects%2557orkspace%2546or%2545xisting%2553ession",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2543%254%43%2549%2544ownloader",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2543onnect%2550arams",
    "behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2552ound%2554ripper%2546unc",
    "behavior/go-client/source%2Fgo-client%2Fgo-var%2Fengineconn%2F%254%46verride%2543%254%43%2549%2541rchive%2555%2552%254%43",
    "behavior/go-client/source%2Fgo-client%2Fgo-var%2Fengineconn%2F%254%46verride%2543hecksums%2555%2552%254%43",
    "behavior/go-engine-sdk/typed-outside-target-response",
];

const FEATURE3_POLICY_IDS: &[&str] = &[
    "policy/rust-policy/transport-background-failure-observation",
    "policy/rust-policy/transport-cache-atomic-publication",
    "policy/rust-policy/transport-cache-permission-safety",
    "policy/rust-policy/transport-cache-retention",
    "policy/rust-policy/transport-cli-archive-bounds",
    "policy/rust-policy/transport-cli-trace-propagation",
    "policy/rust-policy/transport-cli-version-selection",
    "policy/rust-policy/transport-control-line-isolation",
    "policy/rust-policy/transport-diagnostic-bounds",
    "policy/rust-policy/transport-diagnostic-failure-containment",
    "policy/rust-policy/transport-download-fallback-boundary",
    "policy/rust-policy/transport-engine-error-extensions",
    "policy/rust-policy/transport-error-taxonomy",
    "policy/rust-policy/transport-existing-session-validation",
    "policy/rust-policy/transport-http-trace-propagation",
    "policy/rust-policy/transport-local-cli-no-fallback",
    "policy/rust-policy/transport-loopback-authentication",
    "policy/rust-policy/transport-no-query-retry",
    "policy/rust-policy/transport-platform-archive-selection",
    "policy/rust-policy/transport-session-labels",
    "policy/rust-policy/transport-session-protocol",
    "policy/rust-policy/transport-shutdown-bound",
    "policy/rust-policy/transport-source-precedence",
    "policy/rust-policy/transport-startup-retry-boundary",
    "policy/rust-policy/transport-unsupported-target-response",
    "policy/rust-policy/transport-verified-cli-download",
];

const FEATURE3_POLICIES: &[ReviewedPolicyClause] = &[
    clause(
        "transport-background-failure-observation",
        "Every owned process and stream task failure is retained for typed startup or shutdown inspection.",
        "explicit-ownership",
    ),
    clause(
        "transport-cache-atomic-publication",
        "Concurrent provisioners expose either no cache entry or one complete verified executable, never partial bytes.",
        "explicit-ownership",
    ),
    clause(
        "transport-cache-permission-safety",
        "The provisioner rejects symlink or non-regular cache entries and applies private platform-appropriate cache permissions.",
        "unsafe-denied",
    ),
    clause(
        "transport-cache-retention",
        "Managed retention runs under the Cache_Lock, preserves the selected executable, and treats cleanup failure as a redacted non-fatal diagnostic.",
        "explicit-ownership",
    ),
    clause(
        "transport-cli-archive-bounds",
        "Checksum manifests, archive input, extracted executable output, and session control input have fixed documented size bounds.",
        "panic-free-library",
    ),
    clause(
        "transport-cli-trace-propagation",
        "A new CLI receives W3C trace context and baggage derived from the active context or environment fallback.",
        "secret-safe-output",
    ),
    clause(
        "transport-cli-version-selection",
        "The stable connector selects the CLI version declared by the Exact_Target rather than a stale beta constant.",
        "typed-public-errors",
    ),
    clause(
        "transport-control-line-isolation",
        "Session control bytes are parsed once and can never enter diagnostics, traces, Debug output, or rendered errors.",
        "secret-safe-output",
    ),
    clause(
        "transport-diagnostic-bounds",
        "Startup and shutdown diagnostics retain only a fixed redacted tail while live sink delivery remains streaming.",
        "secret-safe-output",
    ),
    clause(
        "transport-diagnostic-failure-containment",
        "A Diagnostic_Sink error or panic disables that sink without failing or panicking the transport operation.",
        "panic-free-library",
    ),
    clause(
        "transport-download-fallback-boundary",
        "PATH fallback is permitted only for a typed Release_Unavailable checksum-manifest response.",
        "typed-public-errors",
    ),
    clause(
        "transport-engine-error-extensions",
        "Known engine error extensions gain typed access without discarding the complete Raw_Response or unknown extension members.",
        "typed-public-errors",
    ),
    clause(
        "transport-error-taxonomy",
        "Configuration, discovery, provisioning, process, protocol, HTTP, GraphQL, engine-domain, compatibility, timeout, background, and shutdown failures remain distinguishable.",
        "typed-public-errors",
    ),
    clause(
        "transport-existing-session-validation",
        "A present session port selects Existing_Session and malformed port or token input fails without considering any CLI source.",
        "typed-public-errors",
    ),
    clause(
        "transport-http-trace-propagation",
        "Every implicit GraphQL HTTP request injects W3C trace context and baggage with active context precedence.",
        "secret-safe-output",
    ),
    clause(
        "transport-local-cli-no-fallback",
        "A present explicit local CLI input is authoritative and any resolution or startup failure is terminal for source selection.",
        "typed-public-errors",
    ),
    clause(
        "transport-loopback-authentication",
        "Implicit GraphQL HTTP dials loopback and authenticates with the session token as Basic username and an empty password.",
        "secret-safe-output",
    ),
    clause(
        "transport-no-query-retry",
        "The transport never automatically repeats a GraphQL operation after request transmission may have begun.",
        "explicit-ownership",
    ),
    clause(
        "transport-platform-archive-selection",
        "Linux and macOS select tar.gz dagger members while Windows selects ZIP dagger.exe members for amd64 and arm64.",
        "idiomatic-rust",
    ),
    clause(
        "transport-session-labels",
        "Every new CLI session receives stable Rust SDK name and package-version labels.",
        "idiomatic-rust",
    ),
    clause(
        "transport-session-protocol",
        "One bounded first stdout line must contain a valid port and non-empty token before resources transfer to the Client.",
        "typed-public-errors",
    ),
    clause(
        "transport-shutdown-bound",
        "Graceful CLI shutdown has a fixed bound after which the SDK kills and reaps the owned child.",
        "explicit-ownership",
    ),
    clause(
        "transport-source-precedence",
        "End-to-end source order is Explicit_Connection, Existing_Session, Explicit_Local_CLI, then Verified_Download.",
        "explicit-ownership",
    ),
    clause(
        "transport-startup-retry-boundary",
        "Process startup retries only a recognized executable-busy condition for at most ten attempts with bounded backoff.",
        "typed-public-errors",
    ),
    clause(
        "transport-unsupported-target-response",
        "An implicit engine outside or unprovable against the Exact_Target fails with a typed compatibility response.",
        "typed-public-errors",
    ),
    clause(
        "transport-verified-cli-download",
        "A downloaded executable is streamed, SHA-256 verified, cancellation-safe, and atomically published before execution.",
        "unsafe-denied",
    ),
];

const FEATURE3_PRIOR_FEATURE2_IDS: &[&str] = &[
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2543onnect",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43oad%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43og%254%46utput",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2545nvironment%2556ariable",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2552unner%2548ost",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2553kip%2557orkspace%254%44odules",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556erbosity",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556ersion%254%46verride",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkdir",
    "behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkspace",
    "behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2543lose",
];

const FEATURE5_EXISTING_IDS: &[&str] = &[
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-const%2Fgenerator%2F%2553%2544%254%42%254%43ang%2547o",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-const%2Fgenerator%2F%2553%2544%254%42%254%43ang%2554ype%2553cript",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-const%2Fgogenerator%2F%2543lient%2547en%2546ile",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-const%2Fgogenerator%2F%2553tarter%2554emplate%2546ile",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-function%2Ftemplates%2F%2544ep%2554emplate",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-function%2Ftemplates%2F%2554emplates",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-method%2Fgogenerator%2F%254%44ounted%2546%2553%2F%254%46pen",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-method%2Fgogenerator%2F%2547o%2547enerator%2F%2547enerate%254%43ibrary",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-method%2Fgogenerator%2F%2547o%2547enerator%2F%2547enerate%254%44odule",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-method%2Fgogenerator%2F%2547o%2547enerator%2F%2547enerate%2543lient",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-method%2Fgogenerator%2F%2547o%2547enerator%2F%2547enerate%2545ntrypoint",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-test%2Fgogenerator%2F%2554est%2553ync%254%44od%2552eplace%2541nd%2554idy%2550ins%2544agger%2557ithout%2555pdating%2554ransitive%2544eps",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Fgenerator%2F%2547enerated%2553tate",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Fgenerator%2F%2547enerator",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Fgenerator%2F%2553%2544%254%42%254%43ang",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Fgogenerator%2F%254%44ounted%2546%2553",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Fgogenerator%2F%2547o%2547enerator",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-type%2Fgogenerator%2F%2550ackage%2549nfo",
    "behavior/go-codegen/source%2Fgo-codegen%2Fgo-var%2Fgenerator%2F%2545rr%2555nknown%2553%2544%254%42%254%43ang",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-method%2Fcore%2F%2543ontainer%2552untime%2F%2541s%2543ontainer",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-method%2Fcore%2F%2543ontainer%2552untime%2F%2543all",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%254%44odule%2549nitializer",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%254%44odule%2552untime",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%254%44odule%2554ypes",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2543lient%2547enerator",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2543lient%2549nitializer",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2543ode%2547enerator",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2543ontainer%2552untime",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2552untime",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2552untime%2554arget",
    "behavior/go-engine-sdk/source%2Fgo-engine-sdk%2Fgo-type%2Fcore%2F%2553%2544%254%42",
];

const FEATURE5_POLICY_IDS: &[&str] = &[
    "policy/rust-policy/engine-bare-sdk-resolution",
    "policy/rust-policy/engine-build-provenance-selection",
    "policy/rust-policy/engine-version-shorthand-rejection",
    "policy/rust-policy/engine-workspace-sdk-installation",
    "policy/rust-policy/engine-init-changeset-confinement",
    "policy/rust-policy/engine-existing-project-preservation",
    "policy/rust-policy/engine-user-generated-file-ownership",
    "policy/rust-policy/engine-visible-schema-core-compatibility",
    "policy/rust-policy/engine-operation-input-completeness",
    "policy/rust-policy/engine-operation-output-confinement",
    "policy/rust-policy/engine-operation-determinism",
    "policy/rust-policy/engine-runtime-toolchain-selection",
    "policy/rust-policy/engine-locked-dependency-closure",
    "policy/rust-policy/engine-immutable-sdk-dependency-source",
    "policy/rust-policy/engine-committed-generated-runtime",
    "policy/rust-policy/engine-legacy-runtime-codegen-isolation",
    "policy/rust-policy/engine-runtime-protocol-boundary",
    "policy/rust-policy/engine-runtime-cache-isolation",
    "policy/rust-policy/engine-packaged-asset-provenance",
    "policy/rust-policy/engine-credential-safe-diagnostics",
    "policy/rust-policy/engine-exact-target-integration-evidence",
    "policy/rust-policy/engine-scope-drift-closure",
];

const FEATURE5_POLICIES: &[ReviewedPolicyClause] = &[
    clause(
        "engine-bare-sdk-resolution",
        "WHEN a module selects Bare_Rust_Reference, THE engine SDK loader SHALL resolve the\n   Builtin_Rust_SDK before attempting external module resolution.",
        "typed-public-errors",
    ),
    clause(
        "engine-build-provenance-selection",
        "WHEN the Builtin_Rust_SDK loads, THE engine SHALL bind it to an\n   Engine_Source_Descriptor embedded by the engine build.",
        "dependency-policy",
    ),
    clause(
        "engine-version-shorthand-rejection",
        "IF Versioned_Rust_Shorthand is supplied, THEN THE engine SHALL return\n   `the rust sdk does not currently support selecting a specific version`.",
        "typed-public-errors",
    ),
    clause(
        "engine-workspace-sdk-installation",
        "WHEN `dagger sdk install rust` succeeds, THE workspace SHALL contain one\n   Workspace_SDK_Installation named `dagger-rust-sdk`.",
        "explicit-ownership",
    ),
    clause(
        "engine-init-changeset-confinement",
        "THE Rust_Initialization Changeset SHALL exclude paths outside the initialized\n   module root.",
        "explicit-ownership",
    ),
    clause(
        "engine-existing-project-preservation",
        "WHEN initialization finds one compatible package manifest, THE Rust initializer\n   SHALL preserve every unrelated semantic setting.",
        "explicit-ownership",
    ),
    clause(
        "engine-user-generated-file-ownership",
        "WHEN authored Rust source exists, THE Rust initializer SHALL preserve every authored\n    source file byte-for-byte.",
        "explicit-ownership",
    ),
    clause(
        "engine-visible-schema-core-compatibility",
        "THE Rust backend SHALL validate every Core_Schema coordinate required by the\n   operation's Target_Revision visibility policy against the reviewed semantic shape.",
        "idiomatic-rust",
    ),
    clause(
        "engine-operation-input-completeness",
        "THE Operation_Input SHALL include the exact engine target identity.",
        "typed-public-errors",
    ),
    clause(
        "engine-operation-output-confinement",
        "IF an output root escapes the engine-selected operation root, THEN THE Rust backend\n    SHALL return a path-confinement diagnostic.",
        "panic-free-library",
    ),
    clause(
        "engine-operation-determinism",
        "WHEN identical Operation_Input is processed twice, THE Rust backend SHALL produce\n    byte-identical artifacts and Operation_Manifest bytes.",
        "explicit-ownership",
    ),
    clause(
        "engine-runtime-toolchain-selection",
        "WHEN a Cargo_Project declares one compatible exact toolchain, THE runtime builder\n    SHALL use that toolchain.",
        "locked-resolution",
    ),
    clause(
        "engine-locked-dependency-closure",
        "WHEN a compatible Cargo.lock is present, THE runtime builder SHALL invoke Cargo\n    with `--locked`.",
        "locked-resolution",
    ),
    clause(
        "engine-immutable-sdk-dependency-source",
        "THE generated Cargo_Project SHALL depend on an exact registry version or immutable\n   Git revision of `dagger-sdk`.",
        "dependency-policy",
    ),
    clause(
        "engine-committed-generated-runtime",
        "WHILE Checked_Generated_Mode is active, THE runtime builder SHALL consume committed\n   generated artifacts.",
        "explicit-ownership",
    ),
    clause(
        "engine-legacy-runtime-codegen-isolation",
        "WHILE Legacy_Runtime_Codegen_Mode is active, THE runtime builder SHALL generate only\n   in private ephemeral container state.",
        "explicit-ownership",
    ),
    clause(
        "engine-runtime-protocol-boundary",
        "WHEN the Runtime_Entrypoint starts under ModuleRuntime.Call, THE Rust process SHALL\n   connect through the supplied nested engine session.",
        "typed-public-errors",
    ),
    clause(
        "engine-runtime-cache-isolation",
        "THE runtime builder SHALL key caches without secret values.",
        "secret-safe-output",
    ),
    clause(
        "engine-packaged-asset-provenance",
        "THE engine build SHALL bind the content digest to the produced engine image.",
        "dependency-policy",
    ),
    clause(
        "engine-credential-safe-diagnostics",
        "WHEN a process exits unsuccessfully, THE Rust integration SHALL capture bounded\n    credential-safe diagnostics.",
        "secret-safe-output",
    ),
    clause(
        "engine-exact-target-integration-evidence",
        "WHEN exact-target observations pass, THE evidence producer SHALL bind their result\n    to the exact engine revision, engine version, schema digest, Rust SDK source digest,\n    toolchain, and packaged-asset digest.",
        "locked-resolution",
    ),
    clause(
        "engine-scope-drift-closure",
        "IF a current or newly extracted engine SDK capability is absent from the scope\n   manifest, THEN THE completeness contract SHALL fail before status rendering.",
        "typed-public-errors",
    ),
];

const FEATURE8_POLICIES: &[ReviewedPolicyClause] = &[
    clause(
        "conformance-capability-scope",
        "policy/rust-policy/conformance-capability-scope",
        "explicit-ownership",
    ),
    clause(
        "conformance-applicability-accounting",
        "policy/rust-policy/conformance-applicability-accounting",
        "typed-public-errors",
    ),
    clause(
        "conformance-case-catalog",
        "policy/rust-policy/conformance-case-catalog",
        "explicit-ownership",
    ),
    clause(
        "conformance-engine-free-checkpoint",
        "policy/rust-policy/conformance-engine-free-checkpoint",
        "explicit-ownership",
    ),
    clause(
        "signoff-host-preflight",
        "policy/rust-policy/signoff-host-preflight",
        "typed-public-errors",
    ),
    clause(
        "signoff-exact-target-artifact",
        "policy/rust-policy/signoff-exact-target-artifact",
        "dependency-policy",
    ),
    clause(
        "signoff-artifact-import-reuse",
        "policy/rust-policy/signoff-artifact-import-reuse",
        "explicit-ownership",
    ),
    clause(
        "signoff-closure-evidence",
        "policy/rust-policy/signoff-closure-evidence",
        "locked-resolution",
    ),
    clause(
        "signoff-single-engine",
        "policy/rust-policy/signoff-single-engine",
        "explicit-ownership",
    ),
    clause(
        "signoff-single-rust-baseline",
        "policy/rust-policy/signoff-single-rust-baseline",
        "explicit-ownership",
    ),
    clause(
        "signoff-isolated-case-fanout",
        "policy/rust-policy/signoff-isolated-case-fanout",
        "explicit-ownership",
    ),
    clause(
        "signoff-case-retry-honesty",
        "policy/rust-policy/signoff-case-retry-honesty",
        "typed-public-errors",
    ),
    clause(
        "signoff-atomic-verdict",
        "policy/rust-policy/signoff-atomic-verdict",
        "typed-public-errors",
    ),
    clause(
        "signoff-release-handoff",
        "policy/rust-policy/signoff-release-handoff",
        "explicit-ownership",
    ),
    clause(
        "signoff-duplicate-work-rejection",
        "policy/rust-policy/signoff-duplicate-work-rejection",
        "explicit-ownership",
    ),
    clause(
        "signoff-phase-budget",
        "policy/rust-policy/signoff-phase-budget",
        "typed-public-errors",
    ),
    clause(
        "platform-native-matrix",
        "policy/rust-policy/platform-native-matrix",
        "idiomatic-rust",
    ),
    clause(
        "security-locked-supply-chain",
        "policy/rust-policy/security-locked-supply-chain",
        "locked-resolution",
    ),
    clause(
        "security-artifact-provenance",
        "policy/rust-policy/security-artifact-provenance",
        "dependency-policy",
    ),
    clause(
        "security-artifact-vulnerability-scan",
        "policy/rust-policy/security-artifact-vulnerability-scan",
        "cargo-deny",
    ),
    clause(
        "security-secret-canary",
        "policy/rust-policy/security-secret-canary",
        "secret-safe-output",
    ),
    clause(
        "security-expiring-exception",
        "policy/rust-policy/security-expiring-exception",
        "dependency-policy",
    ),
];

const fn clause(
    clause_id: &'static str,
    exact_text: &'static str,
    guidance_id: &'static str,
) -> ReviewedPolicyClause {
    ReviewedPolicyClause {
        clause_id,
        exact_text,
        guidance_id,
    }
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use crate::authority::{SourceBundle, recompute_source_digest};
    use crate::model::{AuthorityRegistry, SourceSelector};

    #[test]
    fn rust_policy_authority_digest_covers_every_reviewed_feature_source() {
        let repository_root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..");
        let registry: AuthorityRegistry = serde_json::from_slice(
            &std::fs::read(repository_root.join("sdk/rust/completeness/authorities.json")).unwrap(),
        )
        .unwrap();
        let authority = registry
            .authorities
            .get(&crate::model::AuthorityId::new("rust-policy").unwrap())
            .unwrap();
        let files = authority
            .include
            .iter()
            .map(|selector| match selector {
                SourceSelector::Path(path) => path.path.clone(),
                SourceSelector::Symbol(symbol) => symbol.path.clone(),
            })
            .map(|path| {
                let bytes = std::fs::read(repository_root.join(path.as_str())).unwrap();
                (path, bytes)
            });
        let digest = recompute_source_digest(authority, &SourceBundle::new(files)).unwrap();
        assert_eq!(digest, authority.source_digest);
    }
}
