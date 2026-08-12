//! Smoke coverage for shared valid-first conformance strategies and independent folds.

#[path = "support/conformance.rs"]
mod conformance;

use conformance::*;
use proptest::prelude::*;
use proptest::test_runner::Config;

proptest! {
    #![proptest_config(Config::with_cases(256))]

    #[test]
    fn generated_scope_graph_and_host_models_preserve_construction_invariants(
        scope in scope_strategy(),
        graph in graph_strategy(),
        host_schedule in host_schedule_strategy(),
        child_closure in child_closure_strategy(),
        platforms in platform_matrix_strategy(),
    ) {
        prop_assert!(scope_is_total(&scope));
        prop_assert!(!graph.assertion_edges.is_empty());
        prop_assert!(!graph.case_edges.is_empty());
        prop_assert_eq!(host_schedule.len(), 9);
        prop_assert_eq!(child_closure.len(), 6);
        prop_assert!(!platforms.is_empty());
    }

    #[test]
    fn generated_artifact_security_attempt_and_verdict_models_are_bounded(
        artifact_events in artifact_event_strategy(),
        security in security_graph_strategy(),
        chunks in canary_chunks_strategy(),
        attempts in attempts_strategy(),
        counters_and_timings in counters_and_timings_strategy(),
        verdict in verdict_strategy(),
    ) {
        let exclusive = artifact_events_are_exclusive(&artifact_events);
        let builds = artifact_events.iter().filter(|event| matches!(event, ArtifactEvent::Build)).count();
        let imports = artifact_events.iter().filter(|event| matches!(event, ArtifactEvent::Import)).count();
        prop_assert_eq!(exclusive, builds + imports == 1);
        prop_assert!(security.cargo_edges.len() <= 64);
        prop_assert!(security.findings.len() <= 16);
        prop_assert!(security.exceptions.len() <= 16);
        prop_assert!(!chunks.is_empty());
        prop_assert!(!attempts.is_empty());
        prop_assert!(!counters_and_timings.0.is_empty());
        prop_assert!(!counters_and_timings.1.is_empty());
        prop_assert_eq!(
            verdict_passes(&verdict),
            [
                verdict.closure,
                verdict.artifact,
                verdict.cases,
                verdict.platforms,
                verdict.security,
                verdict.cleanup,
            ]
            .into_iter()
            .all(|gate| gate)
        );
    }
}
