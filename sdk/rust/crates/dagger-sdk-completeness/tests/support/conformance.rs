//! Valid-first generators and deliberately small reference models for sign-off properties.

use std::collections::{BTreeMap, BTreeSet};

use dagger_sdk_completeness::{Architecture, OperatingSystem};
use proptest::prelude::*;

#[derive(Clone, Debug)]
pub enum ReferenceDisposition {
    Same,
    Idiomatic,
    EngineOnly,
    ForeignOnly,
}

#[derive(Clone, Debug)]
pub struct ReferenceScope {
    pub active: BTreeSet<u16>,
    pub decisions: BTreeMap<u16, ReferenceDisposition>,
}

#[derive(Clone, Debug)]
pub struct ReferenceGraph {
    pub assertion_edges: BTreeSet<(u8, u16)>,
    pub case_edges: BTreeSet<(u8, u8)>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ArtifactEvent {
    Build,
    Import,
    Reuse,
}

#[derive(Clone, Copy, Debug)]
pub enum AttemptOutcome {
    Passed,
    InfrastructureFailed,
    AssertionFailed,
}

#[derive(Clone, Debug)]
pub struct ReferenceSecurityGraph {
    pub cargo_edges: BTreeSet<(u8, u8)>,
    pub findings: BTreeSet<u8>,
    pub exceptions: BTreeSet<u8>,
}

#[derive(Clone, Debug)]
pub struct ReferenceVerdict {
    pub closure: bool,
    pub artifact: bool,
    pub cases: bool,
    pub platforms: bool,
    pub security: bool,
    pub cleanup: bool,
}

pub fn scope_strategy() -> BoxedStrategy<ReferenceScope> {
    prop::collection::btree_set(0_u16..1_200, 1..32)
        .prop_flat_map(|active| {
            let keys = active.iter().copied().collect::<Vec<_>>();
            prop::collection::vec(0_u8..4, keys.len()).prop_map(move |choices| {
                let decisions = keys
                    .iter()
                    .copied()
                    .zip(choices)
                    .map(|(id, choice)| {
                        let disposition = match choice {
                            0 => ReferenceDisposition::Same,
                            1 => ReferenceDisposition::Idiomatic,
                            2 => ReferenceDisposition::EngineOnly,
                            _ => ReferenceDisposition::ForeignOnly,
                        };
                        (id, disposition)
                    })
                    .collect();
                ReferenceScope {
                    active: active.clone(),
                    decisions,
                }
            })
        })
        .boxed()
}

pub fn graph_strategy() -> BoxedStrategy<ReferenceGraph> {
    (
        prop::collection::btree_set((0_u8..24, 0_u16..64), 1..48),
        prop::collection::btree_set((0_u8..24, 0_u8..24), 1..48),
    )
        .prop_map(|(assertion_edges, case_edges)| ReferenceGraph {
            assertion_edges,
            case_edges,
        })
        .boxed()
}

pub fn host_schedule_strategy() -> BoxedStrategy<Vec<u32>> {
    prop::collection::vec(1_u32..180_000, 9..=9).boxed()
}

pub fn child_closure_strategy() -> BoxedStrategy<BTreeMap<u8, bool>> {
    prop::collection::btree_map(2_u8..8, any::<bool>(), 6..=6).boxed()
}

pub fn artifact_event_strategy() -> BoxedStrategy<Vec<ArtifactEvent>> {
    prop::collection::vec(
        prop_oneof![
            Just(ArtifactEvent::Build),
            Just(ArtifactEvent::Import),
            Just(ArtifactEvent::Reuse),
        ],
        1..8,
    )
    .boxed()
}

pub fn platform_matrix_strategy() -> BoxedStrategy<BTreeSet<(OperatingSystem, Architecture, bool)>>
{
    prop::collection::btree_set(
        (
            prop_oneof![
                Just(OperatingSystem::Linux),
                Just(OperatingSystem::Macos),
                Just(OperatingSystem::Windows),
            ],
            prop_oneof![Just(Architecture::Amd64), Just(Architecture::Arm64)],
            any::<bool>(),
        ),
        1..7,
    )
    .boxed()
}

pub fn security_graph_strategy() -> BoxedStrategy<ReferenceSecurityGraph> {
    (
        prop::collection::btree_set((0_u8..32, 0_u8..32), 0..64),
        prop::collection::btree_set(0_u8..64, 0..16),
        prop::collection::btree_set(0_u8..64, 0..16),
    )
        .prop_map(
            |(cargo_edges, findings, exceptions)| ReferenceSecurityGraph {
                cargo_edges,
                findings,
                exceptions,
            },
        )
        .boxed()
}

pub fn canary_chunks_strategy() -> BoxedStrategy<Vec<Vec<u8>>> {
    prop::collection::vec(prop::collection::vec(any::<u8>(), 0..64), 1..16).boxed()
}

pub fn attempts_strategy() -> BoxedStrategy<Vec<AttemptOutcome>> {
    prop::collection::vec(
        prop_oneof![
            Just(AttemptOutcome::Passed),
            Just(AttemptOutcome::InfrastructureFailed),
            Just(AttemptOutcome::AssertionFailed),
        ],
        1..6,
    )
    .boxed()
}

pub fn counters_and_timings_strategy() -> BoxedStrategy<(BTreeMap<u8, u8>, BTreeMap<u8, u32>)> {
    (
        prop::collection::btree_map(0_u8..24, 0_u8..4, 1..24),
        prop::collection::btree_map(0_u8..24, 1_u32..600_000, 1..24),
    )
        .boxed()
}

pub fn verdict_strategy() -> BoxedStrategy<ReferenceVerdict> {
    (
        any::<bool>(),
        any::<bool>(),
        any::<bool>(),
        any::<bool>(),
        any::<bool>(),
        any::<bool>(),
    )
        .prop_map(
            |(closure, artifact, cases, platforms, security, cleanup)| ReferenceVerdict {
                closure,
                artifact,
                cases,
                platforms,
                security,
                cleanup,
            },
        )
        .boxed()
}

pub fn scope_is_total(scope: &ReferenceScope) -> bool {
    scope.active.len() == scope.decisions.len()
        && scope
            .active
            .iter()
            .all(|id| scope.decisions.contains_key(id))
}

pub fn artifact_events_are_exclusive(events: &[ArtifactEvent]) -> bool {
    let builds = events
        .iter()
        .filter(|event| **event == ArtifactEvent::Build)
        .count();
    let imports = events
        .iter()
        .filter(|event| **event == ArtifactEvent::Import)
        .count();
    (builds == 1 && imports == 0) || (builds == 0 && imports == 1)
}

pub fn verdict_passes(verdict: &ReferenceVerdict) -> bool {
    verdict.closure
        && verdict.artifact
        && verdict.cases
        && verdict.platforms
        && verdict.security
        && verdict.cleanup
}
