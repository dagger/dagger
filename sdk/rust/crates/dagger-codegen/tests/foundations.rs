//! Shared generator strategy and recording-double regressions.

mod support;

use proptest::prelude::*;
use tempfile::tempdir;

use support::*;

proptest! {
    #![proptest_config(pure_config())]

    #[test]
    fn valid_first_foundations_are_deterministic(
        target in target_strategy(),
        raw in raw_schema_fragment_strategy(),
        canonical in canonical_schema_fragment_strategy(),
        wrapper in wrapper_strategy(),
        default_literal in default_literal_strategy(),
        directive in directive_strategy(),
        catalog in catalog_strategy(),
        artifacts in artifact_set_strategy(),
        mapping in capability_mapping_strategy(),
        evidence in evidence_record_strategy(),
    ) {
        let left = serde_json::to_vec(&(
            &target,
            &raw,
            &canonical,
            &wrapper,
            &default_literal,
            &directive,
            &catalog,
        )).expect("valid-first values must serialize");
        let right = serde_json::to_vec(&(
            &target,
            &raw,
            &canonical,
            &wrapper,
            &default_literal,
            &directive,
            &catalog,
        )).expect("valid-first values must serialize repeatedly");
        prop_assert_eq!(left, right);
        prop_assert_eq!(artifacts.0.len(), artifacts.0.keys().collect::<std::collections::BTreeSet<_>>().len());
        prop_assert!(!mapping.capability_id.is_empty());
        prop_assert!(!evidence.subject.is_empty());
    }
}

proptest! {
    #![proptest_config(filesystem_config())]

    #[test]
    fn recording_components_preserve_order_and_private_state(
        fields in prop::collection::vec(name_strategy(), 0..16),
        candidate in prop::collection::vec(any::<u8>(), 0..128),
    ) {
        let private = tempdir().expect("private test directory must be available");
        let path = private.path().join("candidate.rs");
        let mut selection = RecordingSelection::default();
        for field in &fields {
            selection.select(field.clone());
        }
        let mut session = RecordingSession::default();
        session.execute(&selection);
        let mut formatter = RecordingFormatter::default();
        let formatted = formatter.format(&candidate);
        let mut filesystem = RecordingFilesystem::default();
        filesystem.write(path.clone(), formatted.clone());
        let publication_lock = RecordingPublicationLock::default();
        let first_guard = publication_lock.try_acquire();
        let contender = publication_lock.clone();
        let rejected_while_held = std::thread::spawn(move || contender.try_acquire().is_none())
            .join()
            .expect("recording contender must finish");

        prop_assert_eq!(session.executions, vec![fields]);
        prop_assert_eq!(formatter.candidates, vec![candidate]);
        prop_assert_eq!(filesystem.read(&path), Some(formatted));
        prop_assert_eq!(filesystem.events.len(), 2);
        prop_assert!(first_guard.is_some());
        prop_assert!(rejected_while_held);
        drop(first_guard);
        prop_assert!(publication_lock.try_acquire().is_some());
        prop_assert_eq!(publication_lock.acquisitions(), 2);
    }
}
