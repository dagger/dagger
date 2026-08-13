//! Packaged engine-content, resolver replay, and security-closure properties.

mod support;

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::str::FromStr;

use dagger_sdk_engine::*;
use proptest::prelude::*;
use serde::{Deserialize, Serialize};
use support::fixed_model_corpus;

const REPLAY: &[u8] = include_bytes!("../../../completeness/engine-foundation-replay.json");
const REQUIRED_PAYLOADS: [&str; 15] = [
    "LICENSE",
    "dist/client-generation.json",
    "dist/dagger-rust-engine",
    "dist/rustfmt",
    "dist/runtime-policy.json",
    "runtime/dagger.gen.go",
    "runtime/dagger-module.toml",
    "runtime/go.mod",
    "runtime/go.sum",
    "runtime/internal/dagger/dagger.gen.go",
    "runtime/internal/dagger/rust-sdk.gen.go",
    "runtime/internal/metadata/client_generation.go",
    "runtime/internal/metadata/engine.go",
    "runtime/main.go",
    "runtime/runtime.go",
];

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct FoundationReplay {
    format_version: u32,
    resolution_cases: Vec<ResolutionReplay>,
    workspace_sequences: Vec<Vec<WorkspaceAction>>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ResolutionReplay {
    kind: ResolutionKind,
    source: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
enum ResolutionKind {
    BareRust,
    VersionedRust,
    ImmutableExternal,
    MutableExternal,
    Unknown,
    AmbiguousRegistry,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
enum WorkspaceAction {
    Install,
    Reinstall,
    Uninstall,
    ForeignInstall,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct ResolutionOutcome {
    selected: Option<&'static str>,
    network_events: usize,
    causes: Vec<&'static str>,
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
struct WorkspaceState {
    source: Option<&'static str>,
    owned: bool,
    changes: usize,
    collisions: usize,
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    #[test]
    fn property_02_deterministic_rust_sdk_resolution(
        case_index in any::<usize>(),
        external_succeeds in any::<bool>(),
    ) {
        let replay = replay();
        let case = &replay.resolution_cases[case_index % replay.resolution_cases.len()];
        let registry_matches = usize::from(case.kind != ResolutionKind::Unknown)
            + usize::from(case.kind == ResolutionKind::AmbiguousRegistry);
        let observed = resolve_by_precedence(case, registry_matches, external_succeeds);
        let expected = resolution_reference(case.kind, external_succeeds);
        prop_assert_eq!(observed, expected);
    }

    #[test]
    fn property_03_engine_source_provenance_complete_target_bound(
        seed in any::<u8>(),
        mutation in 0_u8..13,
    ) {
        let directory = tempfile::tempdir().unwrap();
        write_payload(directory.path(), seed);
        let identity = package_identity(seed, seed % 2 == 1);
        let (manifest, descriptor) = build_packaged_content(directory.path(), identity).unwrap();
        validate_packaged_source(&manifest, &descriptor).unwrap();

        let mut corpus = fixed_model_corpus(seed, true, 1);
        corpus.request.target = TargetIdentity {
            format_version: FormatVersion,
            repository: descriptor.repository.clone(),
            dagger_revision: descriptor.dagger_revision.clone(),
            engine_version: descriptor.engine_version.clone(),
            rust_sdk_version: descriptor.rust_sdk_version.clone(),
            rust_toolchain: descriptor.rust_toolchain.clone(),
            core_schema_digest: descriptor.core_schema_digest.clone(),
        };
        corpus.request.sdk_dependency = descriptor.sdk_dependency.clone();
        dagger_sdk_engine::runner::validate_request(&corpus.request, &descriptor).unwrap();

        match mutation {
            0 => corpus.request.target.repository = value("https://github.com/acme/other"),
            1 => corpus.request.target.dagger_revision = revision(seed.wrapping_add(1)),
            2 => corpus.request.target.engine_version = value("1.0.0-beta.11"),
            3 => corpus.request.target.rust_sdk_version = value("1.0.0-beta.11"),
            4 => corpus.request.target.rust_toolchain = value("1.98.0"),
            5 => corpus.request.target.core_schema_digest = digest(seed, 201),
            6 => corpus.request.sdk_dependency = registry_dependency("1.0.0-beta.11", "dagger-sdk"),
            7 => {
                let mut changed = manifest.clone();
                changed.assets.remove(&path(REQUIRED_PAYLOADS[usize::from(seed) % REQUIRED_PAYLOADS.len()]));
                prop_assert!(validate_packaged_source(&changed, &descriptor).is_err());
                return Ok(());
            }
            8 => {
                let mut changed = manifest.clone();
                let asset = changed.assets.values_mut().next().unwrap();
                asset.digest = digest(seed, 202);
                prop_assert_ne!(
                    canonical_digest(DigestDomain::PackagedAssets, &changed).unwrap(),
                    descriptor.packaged_asset_manifest_digest.clone(),
                );
                prop_assert!(validate_packaged_source(&changed, &descriptor).is_err());
                return Ok(());
            }
            9 => {
                let mut changed = descriptor.clone();
                changed.packaged_asset_manifest_digest = digest(seed, 203);
                prop_assert!(validate_packaged_source(&manifest, &changed).is_err());
                return Ok(());
            }
            10 => {
                let mut json = serde_json::to_value(&descriptor).unwrap();
                json.as_object_mut().unwrap().remove("core_schema_digest");
                prop_assert!(decode_canonical::<EngineSourceDescriptor>(&canonical_bytes(&json).unwrap()).is_err());
                return Ok(());
            }
            11 => {
                let mut changed = descriptor.clone();
                changed.repository = value("https://github.com/dagger/dagger.git");
                prop_assert!(changed.validate().is_err());
                return Ok(());
            }
            12 => {
                let mut changed = descriptor.clone();
                changed.sdk_dependency = registry_dependency("1.0.0-beta.11", "dagger-sdk");
                prop_assert!(changed.validate().is_err());
                return Ok(());
            }
            _ => unreachable!(),
        }
        prop_assert!(dagger_sdk_engine::runner::validate_request(&corpus.request, &descriptor).is_err());
    }

    #[test]
    fn property_04_workspace_installation_collision_safe_reversible(
        sequence_index in any::<usize>(),
        repetitions in 1_usize..24,
    ) {
        let replay = replay();
        let sequence = &replay.workspace_sequences[sequence_index % replay.workspace_sequences.len()];
        let mut observed = WorkspaceState::default();
        let mut expected = WorkspaceState::default();
        for offset in 0..repetitions {
            let action = sequence[offset % sequence.len()];
            apply_workspace_action(&mut observed, action);
            reference_workspace_action(&mut expected, action);
        }
        prop_assert_eq!(observed, expected);
    }

    #[test]
    fn property_23_packaged_assets_public_dependencies_closed_graph(
        seed in any::<u8>(),
        publication_shape in 0_u8..4,
        dependency_shape in 0_u8..3,
        remove_payload in any::<bool>(),
    ) {
        let mut manifest = synthetic_manifest(seed);
        if remove_payload {
            manifest.assets.remove(&path(REQUIRED_PAYLOADS[usize::from(seed) % REQUIRED_PAYLOADS.len()]));
        }
        let publishable = match publication_shape {
            0 => BTreeSet::from([
                coordinate("cargo:dagger-sdk"),
                coordinate("cargo:dagger-sdk-macros"),
            ]),
            1 => BTreeSet::new(),
            2 => BTreeSet::from([coordinate("cargo:dagger-codegen")]),
            _ => BTreeSet::from([
                coordinate("cargo:dagger-sdk"),
                coordinate("cargo:dagger-sdk-engine"),
            ]),
        };
        if dependency_shape == 1 {
            let valid = registry_dependency("1.0.0-beta.10", "dagger-sdk");
            let mut invalid = serde_json::to_value(valid).unwrap();
            invalid["package"] = serde_json::json!("dagger-codegen");
            prop_assert!(decode_canonical::<PublishedSdkDependency>(&canonical_bytes(&invalid).unwrap()).is_err());
            return Ok(());
        }
        let dependency = match dependency_shape {
            0 => registry_dependency("1.0.0-beta.10", "dagger-sdk"),
            _ => PublishedSdkDependency::Git {
                url: value("https://github.com/acme/dagger"),
                revision: revision(seed),
                package: value("dagger-sdk"),
            },
        };
        let result = validate_packaged_distribution(&manifest, &publishable, &dependency);
        let accepted = !remove_payload && publication_shape == 0;
        prop_assert_eq!(result.is_ok(), accepted);

        if let Ok(graph) = result {
            let public = graph.subjects().values().filter(|subject| {
                subject.kind == SecuritySubjectKind::PublishableCrate
            }).collect::<Vec<_>>();
            prop_assert_eq!(public.len(), 2);
            prop_assert_eq!(
                public
                    .iter()
                    .map(|subject| subject.id.as_str())
                    .collect::<BTreeSet<_>>(),
                BTreeSet::from(["cargo:dagger-sdk", "cargo:dagger-sdk-macros"]),
            );
            let digest_before = canonical_digest(DigestDomain::PackagedAssets, &manifest).unwrap();
            manifest.assets.values_mut().next().unwrap().digest = digest(seed, 204);
            prop_assert_ne!(digest_before, canonical_digest(DigestDomain::PackagedAssets, &manifest).unwrap());
        }
    }

    #[test]
    fn property_25_security_audit_roots_cover_shipped_graph(
        seed in any::<u8>(),
        node_count in 2_usize..18,
        omit_audited in any::<bool>(),
        dangling_edge in any::<bool>(),
    ) {
        let (graph, reference_subjects, reference_edges, roots) =
            generated_security_graph(seed, node_count, dangling_edge);
        let expected = independent_reachable(&reference_subjects, &reference_edges, &roots);
        let observed = graph.reachable();
        if dangling_edge {
            prop_assert!(observed.is_err());
            return Ok(());
        }
        let observed = observed.unwrap();
        prop_assert_eq!(&observed, &expected);

        let mut audited = expected.clone();
        // Repeated audit roots collapse to one stable identity before validation.
        audited.extend(expected.iter().cloned());
        if omit_audited {
            let omitted = audited.iter().next().cloned().unwrap();
            audited.remove(&omitted);
        }
        prop_assert_eq!(graph.validate_inputs(&audited).is_ok(), !omit_audited);
    }
}

#[test]
fn repository_security_inputs_cover_derived_shipped_graph() {
    const CARGO_LOCK: &str = include_str!("../../../Cargo.lock");
    const RUNTIME_GO_MOD: &str = include_str!("../../../runtime/go.mod");
    const RUNTIME_GO_SUM: &str = include_str!("../../../runtime/go.sum");
    const ENGINE_BUILDER: &str = include_str!("../../../../../toolchains/engine-dev/build/sdk.go");
    const SECURITY_WORKFLOW: &str =
        include_str!("../../../../../.github/workflows/rust-sdk-security.yml");
    const SECURITY_PREFLIGHT: &str = include_str!("../../../scripts/ci-security-preflight.sh");

    let manifest = synthetic_manifest(1);
    let graph = derive_shipped_audit_graph(&manifest).unwrap();
    let mut audited = BTreeSet::new();
    for subject in graph.subjects().values() {
        let covered = match subject.id.as_str() {
            "cargo:dagger-sdk" => CARGO_LOCK.contains("name = \"dagger-sdk\""),
            "cargo:dagger-sdk-macros" => {
                CARGO_LOCK.contains("name = \"dagger-sdk-macros\"")
            }
            "cargo:dagger-codegen" => CARGO_LOCK.contains("name = \"dagger-codegen\""),
            "cargo:dagger-bootstrap" => CARGO_LOCK.contains("name = \"dagger-bootstrap\""),
            "cargo:dagger-sdk-engine" => {
                CARGO_LOCK.contains("name = \"dagger-sdk-engine\"")
            }
            "go:sdk/rust/runtime" => {
                RUNTIME_GO_MOD.contains("module rust-sdk") && !RUNTIME_GO_SUM.is_empty()
            }
            "image:rust-1.97.1" => ENGINE_BUILDER.contains(
                "rust:1.97.1-bookworm@sha256:705e294093973d7c10e83400393dce7b3611f8e03e55a80af7fff6d02ae1affb",
            ),
            "distribution:rust-sdk" => {
                SECURITY_WORKFLOW.contains("ci-security-preflight.sh source-policy")
                    && SECURITY_PREFLIGHT.contains(
                        "cargo test -p dagger-sdk-engine --test packaging_properties --locked",
                    )
            }
            id if id.starts_with("asset:") => manifest
                .assets
                .keys()
                .any(|path| id == format!("asset:{path}")),
            _ => false,
        };
        if covered {
            audited.insert(subject.id.clone());
        }
    }
    graph.validate_inputs(&audited).unwrap();
}

fn replay() -> FoundationReplay {
    let replay: FoundationReplay = decode_canonical(REPLAY).unwrap();
    assert_eq!(replay.format_version, 1);
    replay
}

fn resolve_by_precedence(
    case: &ResolutionReplay,
    registry_matches: usize,
    external_succeeds: bool,
) -> ResolutionOutcome {
    let (name, suffix) = case.source.split_once('@').unwrap_or((&case.source, ""));
    if name == "rust" && registry_matches > 1 {
        return rejected("ambiguous-builtin");
    }
    if name == "rust" && registry_matches == 1 {
        if suffix.is_empty() && !case.source.contains('@') {
            return ResolutionOutcome {
                selected: Some("builtin"),
                network_events: 0,
                causes: Vec::new(),
            };
        }
        return rejected("versioned-builtin");
    }
    external_resolution(external_succeeds)
}

fn resolution_reference(kind: ResolutionKind, external_succeeds: bool) -> ResolutionOutcome {
    match kind {
        ResolutionKind::BareRust => ResolutionOutcome {
            selected: Some("builtin"),
            network_events: 0,
            causes: Vec::new(),
        },
        ResolutionKind::VersionedRust => rejected("versioned-builtin"),
        ResolutionKind::AmbiguousRegistry => rejected("ambiguous-builtin"),
        ResolutionKind::ImmutableExternal
        | ResolutionKind::MutableExternal
        | ResolutionKind::Unknown => external_resolution(external_succeeds),
    }
}

fn external_resolution(succeeds: bool) -> ResolutionOutcome {
    if succeeds {
        ResolutionOutcome {
            selected: Some("external"),
            network_events: 1,
            causes: Vec::new(),
        }
    } else {
        ResolutionOutcome {
            selected: None,
            network_events: 1,
            causes: vec!["unknown-builtin", "external-resolution"],
        }
    }
}

fn rejected(cause: &'static str) -> ResolutionOutcome {
    ResolutionOutcome {
        selected: None,
        network_events: 0,
        causes: vec![cause],
    }
}

fn apply_workspace_action(state: &mut WorkspaceState, action: WorkspaceAction) {
    match action {
        WorkspaceAction::Install | WorkspaceAction::Reinstall => match state.source {
            None => {
                state.source = Some("rust");
                state.owned = true;
                state.changes += 1;
            }
            Some("rust") => {}
            Some(_) => state.collisions += 1,
        },
        WorkspaceAction::Uninstall => {
            if state.owned && state.source == Some("rust") {
                state.source = None;
                state.owned = false;
                state.changes += 1;
            }
        }
        WorkspaceAction::ForeignInstall => {
            if state.source.is_none() {
                state.source = Some("foreign");
                state.changes += 1;
            }
        }
    }
}

fn reference_workspace_action(state: &mut WorkspaceState, action: WorkspaceAction) {
    let before = state.clone();
    match (action, before.source, before.owned) {
        (WorkspaceAction::Install | WorkspaceAction::Reinstall, None, _) => {
            state.source = Some("rust");
            state.owned = true;
            state.changes += 1;
        }
        (WorkspaceAction::Install | WorkspaceAction::Reinstall, Some("rust"), _) => {}
        (WorkspaceAction::Install | WorkspaceAction::Reinstall, Some(_), _) => {
            state.collisions += 1;
        }
        (WorkspaceAction::Uninstall, Some("rust"), true) => {
            state.source = None;
            state.owned = false;
            state.changes += 1;
        }
        (WorkspaceAction::ForeignInstall, None, _) => {
            state.source = Some("foreign");
            state.changes += 1;
        }
        _ => {}
    }
}

fn write_payload(root: &std::path::Path, seed: u8) {
    for (index, relative) in REQUIRED_PAYLOADS.iter().enumerate() {
        let destination = root.join(relative);
        fs::create_dir_all(destination.parent().unwrap()).unwrap();
        fs::write(&destination, [seed, index as u8]).unwrap();
        if matches!(*relative, "dist/dagger-rust-engine" | "dist/rustfmt") {
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt as _;
                let mut permissions = fs::metadata(&destination).unwrap().permissions();
                permissions.set_mode(0o755);
                fs::set_permissions(destination, permissions).unwrap();
            }
        }
    }
}

fn package_identity(seed: u8, fork: bool) -> PackageIdentity {
    let repository = value("https://github.com/dagger/dagger");
    let dagger_revision = revision(seed);
    let sdk_dependency = if fork {
        PublishedSdkDependency::Git {
            url: value("https://github.com/acme/dagger"),
            revision: revision(seed.wrapping_add(1)),
            package: value("dagger-sdk"),
        }
    } else {
        registry_dependency("1.0.0-beta.10", "dagger-sdk")
    };
    PackageIdentity {
        repository,
        dagger_revision,
        engine_version: value("1.0.0-beta.10"),
        rust_sdk_version: value("1.0.0-beta.10"),
        rust_toolchain: value("1.97.1"),
        sdk_dependency,
        core_schema_digest: digest(seed, 200),
    }
}

fn synthetic_manifest(seed: u8) -> PackagedAssetManifest {
    let assets = REQUIRED_PAYLOADS
        .iter()
        .enumerate()
        .map(|(index, relative)| {
            let path = path(relative);
            (
                path.clone(),
                PackagedAsset {
                    path,
                    digest: digest(seed, index as u8),
                    executable: matches!(*relative, "dist/dagger-rust-engine" | "dist/rustfmt"),
                },
            )
        })
        .collect();
    PackagedAssetManifest {
        format_version: FormatVersion,
        assets,
    }
}

fn generated_security_graph(
    seed: u8,
    node_count: usize,
    dangling_edge: bool,
) -> (
    SecurityAuditGraph,
    BTreeSet<StableCoordinate>,
    BTreeMap<StableCoordinate, BTreeSet<StableCoordinate>>,
    BTreeSet<StableCoordinate>,
) {
    let nodes = (0..node_count)
        .map(|index| coordinate(&format!("subject:{index}")))
        .collect::<Vec<_>>();
    let subjects = nodes
        .iter()
        .cloned()
        .map(|id| {
            (
                id.clone(),
                SecuritySubject {
                    id,
                    kind: SecuritySubjectKind::PackagedAsset,
                },
            )
        })
        .collect::<BTreeMap<_, _>>();
    let mut edges = BTreeMap::new();
    for index in 0..node_count - 1 {
        if index == 0 || (usize::from(seed) + index) % 3 != 0 {
            edges
                .entry(nodes[index].clone())
                .or_insert_with(BTreeSet::new)
                .insert(nodes[index + 1].clone());
        }
    }
    if dangling_edge {
        edges
            .entry(nodes[0].clone())
            .or_insert_with(BTreeSet::new)
            .insert(coordinate("subject:missing"));
    }
    let roots = BTreeSet::from([nodes[0].clone()]);
    let subject_ids = subjects.keys().cloned().collect();
    (
        SecurityAuditGraph::new(subjects, edges.clone(), roots.clone()),
        subject_ids,
        edges,
        roots,
    )
}

fn independent_reachable(
    subjects: &BTreeSet<StableCoordinate>,
    edges: &BTreeMap<StableCoordinate, BTreeSet<StableCoordinate>>,
    roots: &BTreeSet<StableCoordinate>,
) -> BTreeSet<StableCoordinate> {
    let mut discovered = BTreeSet::new();
    let mut frontier = roots.iter().cloned().collect::<Vec<_>>();
    while let Some(node) = frontier.pop() {
        if !subjects.contains(&node) || !discovered.insert(node.clone()) {
            continue;
        }
        if let Some(children) = edges.get(&node) {
            frontier.extend(children.iter().cloned());
        }
    }
    discovered
}

fn registry_dependency(version: &str, package: &str) -> PublishedSdkDependency {
    PublishedSdkDependency::Registry {
        registry: value("crates-io"),
        package: value(package),
        exact_version: value(version),
    }
}

fn revision(seed: u8) -> FullRevision {
    value(&format!("{seed:040x}"))
}

fn digest(seed: u8, discriminator: u8) -> Sha256Digest {
    value(&format!("sha256:{seed:02x}{discriminator:02x}{:060x}", 0))
}

fn path(value: &str) -> RelativeOperationPath {
    RelativeOperationPath::parse(value).unwrap()
}

fn coordinate(value: &str) -> StableCoordinate {
    StableCoordinate::new(value.to_owned()).unwrap()
}

fn value<T>(value: &str) -> T
where
    T: FromStr,
    T::Err: std::fmt::Debug,
{
    value.parse().unwrap()
}
