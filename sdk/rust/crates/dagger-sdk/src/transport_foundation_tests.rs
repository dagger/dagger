//! Shared strategy, recorder, and dependency-family checks for transport work.

use std::collections::HashMap;
use std::time::Duration;

use opentelemetry::propagation::{TextMapCompositePropagator, TextMapPropagator};
use opentelemetry::trace::TracerProvider as _;
use opentelemetry_sdk::propagation::{BaggagePropagator, TraceContextPropagator};
use opentelemetry_sdk::trace::SdkTracerProvider;
use proptest::prelude::*;
use tracing_opentelemetry::{OpenTelemetrySpanExt, layer};
use tracing_subscriber::prelude::*;

use crate::test_support::{
    IO_PROPTEST_CASES, PROPTEST_CASES, RecordingClock, RecordingHttp, RecordingLauncher,
    RecordingProvisioner, RecordingTransport, TestArchitecture, TestArchiveFormat,
    TestOperatingSystem, TransportEventLog, TransportTestEvent, archive_entries, byte_chunks,
    diagnostic_case, failure_schedule, io_proptest_config, manifest_case, native_path,
    process_snapshot_case, proptest_config, target_descriptor_case, version_identity_case,
};

#[test]
fn property_case_budgets_and_failure_persistence_are_explicit() {
    let pure = proptest_config();
    let io = io_proptest_config();
    assert_eq!(pure.cases, PROPTEST_CASES);
    assert_eq!(io.cases, IO_PROPTEST_CASES);
    assert_eq!(PROPTEST_CASES, 256);
    assert_eq!(IO_PROPTEST_CASES, 128);
    assert!(pure.failure_persistence.is_some());
    assert!(io.failure_persistence.is_some());
}

proptest! {
    #![proptest_config(proptest_config())]

    #[test]
    fn transport_strategies_are_bounded_and_portable(
        path in native_path(),
        snapshot in process_snapshot_case(),
        target in target_descriptor_case(),
        chunks in byte_chunks(),
        manifest in manifest_case(),
        entries in archive_entries(),
        version in version_identity_case(),
        diagnostic in diagnostic_case(),
        schedule in failure_schedule(),
    ) {
        prop_assert!(!path.is_empty());
        prop_assert!(snapshot.path_entries.len() < 6);
        prop_assert!(snapshot.session_port.as_ref().is_none_or(|value| value.len() <= 5));
        prop_assert!(snapshot.session_token.as_ref().is_none_or(|value| value.len() <= 24));
        prop_assert!(snapshot.local_cli.as_ref().is_none_or(|value| !value.is_empty()));
        prop_assert!(snapshot.inherited_traceparent.as_ref().is_none_or(|value| value.starts_with("00-")));
        prop_assert!(matches!(
            target.operating_system,
            TestOperatingSystem::Linux
                | TestOperatingSystem::Macos
                | TestOperatingSystem::Windows
                | TestOperatingSystem::Unsupported
        ));
        prop_assert!(matches!(
            target.architecture,
            TestArchitecture::Amd64 | TestArchitecture::Arm64 | TestArchitecture::Unsupported
        ));
        prop_assert!(matches!(
            target.archive_format,
            TestArchiveFormat::TarGz | TestArchiveFormat::Zip | TestArchiveFormat::Unsupported
        ));
        prop_assert!(target.version.starts_with('v'));
        prop_assert_eq!(target.revision.len(), 40);
        prop_assert!(chunks.len() < 12);
        prop_assert!(chunks.iter().all(|chunk| chunk.len() < 64));
        prop_assert!(!manifest.archive_name.is_empty());
        prop_assert_eq!(manifest.digest.len(), 64);
        prop_assert!(manifest.unrelated_lines.len() < 8);
        prop_assert!(entries.len() < 8);
        let entries_are_bounded = entries.iter().all(|entry| {
            let _ = (&entry.path, entry.regular);
            entry.bytes.len() < 128
        });
        prop_assert!(entries_are_bounded);
        prop_assert!(version.version.starts_with('v'));
        prop_assert!(version.build_metadata.as_ref().is_none_or(|value| !value.is_empty()));
        let _ = version.dirty;
        prop_assert!(diagnostic.chunks.len() < 12);
        prop_assert!(!diagnostic.secret.is_empty());
        prop_assert!(diagnostic.sink_fails_at.is_none_or(|index| index < 12));
        prop_assert!(schedule.len() < 16);
    }
}

#[test]
fn recording_components_share_one_ordered_event_vocabulary() {
    let log = TransportEventLog::default();
    RecordingProvisioner(log.clone()).provision();
    RecordingLauncher(log.clone()).launch();
    RecordingTransport(log.clone()).execute();
    RecordingHttp(log.clone()).send();
    RecordingClock(log.clone()).advance(Duration::from_millis(7));

    assert_eq!(
        log.events(),
        [
            TransportTestEvent::Provision,
            TransportTestEvent::Launch,
            TransportTestEvent::Execute,
            TransportTestEvent::Http,
            TransportTestEvent::Advance(Duration::from_millis(7)),
        ]
    );
}

#[test]
fn tracing_bridge_and_w3c_propagators_share_one_compatible_context_family() {
    let provider = SdkTracerProvider::builder().build();
    let tracer = provider.tracer("dagger-sdk-dependency-check");
    let subscriber = tracing_subscriber::registry().with(layer().with_tracer(tracer));
    let propagator = TextMapCompositePropagator::new(vec![
        Box::new(TraceContextPropagator::new()),
        Box::new(BaggagePropagator::new()),
    ]);
    let mut carrier = HashMap::new();

    tracing::subscriber::with_default(subscriber, || {
        let span = tracing::info_span!("dependency_family_check");
        let _entered = span.enter();
        propagator.inject_context(&span.context(), &mut carrier);
    });

    assert!(carrier.contains_key("traceparent"));
    drop(provider);
}
