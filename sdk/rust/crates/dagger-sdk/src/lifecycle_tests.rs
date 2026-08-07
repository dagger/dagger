use std::error::Error as _;
use std::fmt;
use std::future::pending;
use std::sync::atomic::{AtomicBool, AtomicU8, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use proptest::prelude::*;
use tokio::sync::Notify;

use crate::client::{connect_with, connect_with_connector};
use crate::config::ClientConfig;
use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
use crate::connector::{ConnectionRequest, Connector};
use crate::errors::{CloseError, RequestError};
use crate::graphql::{RawRequest, RawResponse, ResponseData};
use crate::test_support::proptest_config;

const CLOSE_OK: u8 = 0;
const CLOSE_ERROR: u8 = 1;
const CLOSE_PANIC: u8 = 2;

type RecordedRequest = (String, Option<serde_json::Value>, Option<String>);

#[derive(Debug)]
struct InjectedMarker;

impl fmt::Display for InjectedMarker {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("injected marker")
    }
}

impl std::error::Error for InjectedMarker {}

#[derive(Clone)]
struct LifecycleProbe {
    close_mode: u8,
    execute_mode: Arc<AtomicU8>,
    execute_calls: Arc<AtomicUsize>,
    close_calls: Arc<AtomicUsize>,
    abort_calls: Arc<AtomicUsize>,
    abort_panics: Arc<AtomicBool>,
    requests: Arc<Mutex<Vec<RecordedRequest>>>,
    close_started: Arc<Notify>,
    close_release: Arc<Notify>,
    execute_started: Arc<Notify>,
    execute_release: Arc<Notify>,
}

impl LifecycleProbe {
    fn new(close_mode: u8) -> Self {
        Self {
            close_mode,
            execute_mode: Arc::new(AtomicU8::new(0)),
            execute_calls: Arc::new(AtomicUsize::new(0)),
            close_calls: Arc::new(AtomicUsize::new(0)),
            abort_calls: Arc::new(AtomicUsize::new(0)),
            abort_panics: Arc::new(AtomicBool::new(false)),
            requests: Arc::new(Mutex::new(Vec::new())),
            close_started: Arc::new(Notify::new()),
            close_release: Arc::new(Notify::new()),
            execute_started: Arc::new(Notify::new()),
            execute_release: Arc::new(Notify::new()),
        }
    }

    fn error() -> EngineConnectionError {
        EngineConnectionError::new(EngineConnectionErrorKind::Transport)
    }
}

#[async_trait]
impl EngineConnection for LifecycleProbe {
    async fn execute(&self, request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        self.execute_calls.fetch_add(1, Ordering::SeqCst);
        self.requests.lock().expect("request log").push((
            request.query().to_owned(),
            request.variables().cloned(),
            request.operation_name().map(str::to_owned),
        ));
        match self.execute_mode.load(Ordering::SeqCst) {
            1 => {
                // Register before publishing "started" so close cannot broadcast the
                // release between those operations and strand this fake request.
                let released = self.execute_release.notified();
                self.execute_started.notify_one();
                released.await;
                Err(Self::error())
            }
            2 => {
                self.execute_started.notify_one();
                pending().await
            }
            3 => {
                self.execute_mode.store(0, Ordering::SeqCst);
                pending().await
            }
            4 => {
                self.execute_mode.store(0, Ordering::SeqCst);
                Err(EngineConnectionError::new(
                    EngineConnectionErrorKind::ConnectTimeout,
                ))
            }
            5 => Err(EngineConnectionError::with_source(
                EngineConnectionErrorKind::Transport,
                InjectedMarker,
            )),
            _ => Ok(RawResponse::new(ResponseData::Value(
                serde_json::json!({"ok": true}),
            ))),
        }
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        self.close_calls.fetch_add(1, Ordering::SeqCst);
        // Keep the test handshake correct even when a release follows the started
        // notification immediately under an optimized scheduler.
        let released = self.close_release.notified();
        self.close_started.notify_one();
        self.execute_release.notify_waiters();
        released.await;
        match self.close_mode {
            CLOSE_OK => Ok(()),
            CLOSE_ERROR => Err(Self::error()),
            CLOSE_PANIC => panic!("injected close panic"),
            _ => unreachable!("test generator constrains close modes"),
        }
    }

    fn abort(&self) {
        self.abort_calls.fetch_add(1, Ordering::SeqCst);
        if self.abort_panics.load(Ordering::SeqCst) {
            panic!("injected abort panic");
        }
    }
}

struct NeverConnector;

#[async_trait]
impl Connector for NeverConnector {
    async fn connect(
        &self,
        _request: ConnectionRequest,
    ) -> Result<Box<dyn EngineConnection>, crate::ConnectError> {
        pending().await
    }
}

struct ProbeConnector(LifecycleProbe);

#[async_trait]
impl Connector for ProbeConnector {
    async fn connect(
        &self,
        _request: ConnectionRequest,
    ) -> Result<Box<dyn EngineConnection>, crate::ConnectError> {
        Ok(Box::new(self.0.clone()))
    }
}

async fn injected_client(probe: LifecycleProbe, timeout: Option<Duration>) -> crate::Client {
    let mut builder = ClientConfig::builder().connection(Box::new(probe));
    if let Some(timeout) = timeout {
        builder = builder.graphql_execution_timeout(timeout);
    }
    connect_with(builder.build().expect("valid injected config"))
        .await
        .expect("injected connection bypasses the connector")
}

async fn close_after_start(
    client: &crate::Client,
    probe: &LifecycleProbe,
) -> Result<(), CloseError> {
    let close_task = tokio::spawn({
        let client = client.clone();
        async move { client.close().await }
    });
    probe.close_started.notified().await;
    probe.close_release.notify_one();
    close_task.await.expect("close waiter task")
}

fn close_class(result: &Result<(), CloseError>) -> &'static str {
    match result {
        Ok(()) => "ok",
        Err(CloseError::Connection(_)) => "connection",
        Err(CloseError::Interrupted) => "interrupted",
        Err(CloseError::Panicked) => "panicked",
    }
}

proptest! {
    #![proptest_config(proptest_config())]

    // Feature: rust-sdk-client-lifecycle, Property 5: close linearizes once
    #[test]
    fn close_linearizes_once(
        callers in 1_usize..12,
        close_mode in 0_u8..3,
        cancelled in proptest::collection::vec(any::<bool>(), 1..12),
    ) {
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let probe = LifecycleProbe::new(close_mode);
            let client = injected_client(probe.clone(), None).await;
            let mut waiters = Vec::new();
            for _ in 0..callers {
                let lease = client.clone();
                waiters.push(tokio::spawn(async move { lease.close().await }));
            }

            probe.close_started.notified().await;
            for (index, waiter) in waiters.iter().enumerate() {
                if cancelled[index % cancelled.len()] {
                    waiter.abort();
                }
            }
            probe.close_release.notify_one();

            let expected = match close_mode {
                CLOSE_OK => "ok",
                CLOSE_ERROR => "connection",
                _ => "panicked",
            };
            for waiter in waiters {
                if let Ok(result) = waiter.await {
                    prop_assert_eq!(close_class(&result), expected);
                }
            }
            let repeated = client.close().await;
            prop_assert_eq!(close_class(&repeated), expected);
            prop_assert_eq!(probe.close_calls.load(Ordering::SeqCst), 1);
            prop_assert_eq!(
                probe.abort_calls.load(Ordering::SeqCst),
                usize::from(close_mode != CLOSE_OK),
            );
            Ok(())
        })?;
    }

    // Feature: rust-sdk-client-lifecycle, Property 7: the close fence prevents new transport work
    #[test]
    fn close_fence_prevents_new_transport_work(request_before_close in any::<bool>()) {
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let probe = LifecycleProbe::new(CLOSE_OK);
            probe.execute_mode.store(1, Ordering::SeqCst);
            let client = injected_client(probe.clone(), None).await;

            if request_before_close {
                let request_client = client.clone();
                let request = tokio::spawn(async move {
                    request_client.execute(RawRequest::new("query { version }")).await
                });
                probe.execute_started.notified().await;
                prop_assert!(close_after_start(&client, &probe).await.is_ok());
                prop_assert!(matches!(request.await.expect("request task"), Err(RequestError::InterruptedByClose)));
                prop_assert_eq!(probe.execute_calls.load(Ordering::SeqCst), 1);
            } else {
                prop_assert!(close_after_start(&client, &probe).await.is_ok());
                let result = client.execute(RawRequest::new("query { version }")).await;
                prop_assert!(matches!(result, Err(RequestError::ClientClosed)));
                prop_assert_eq!(probe.execute_calls.load(Ordering::SeqCst), 0);
            }
            Ok(())
        })?;
    }

    // Feature: rust-sdk-client-lifecycle, Property 8: only the final lease initiates implicit cleanup
    #[test]
    fn only_final_lease_initiates_cleanup(
        leases in 1_usize..32,
        runtime_available in any::<bool>(),
        abort_panics in any::<bool>(),
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        let probe = LifecycleProbe::new(CLOSE_OK);
        probe.abort_panics.store(abort_panics, Ordering::SeqCst);
        let client = runtime.block_on(injected_client(probe.clone(), None));
        let mut handles = vec![client];
        while handles.len() < leases {
            handles.push(handles[0].clone());
        }
        while handles.len() > 1 {
            drop(handles.pop());
            prop_assert_eq!(handles[0].lifecycle_state(), "open");
            prop_assert_eq!(probe.close_calls.load(Ordering::SeqCst), 0);
            prop_assert_eq!(probe.abort_calls.load(Ordering::SeqCst), 0);
        }

        let final_handle = handles.pop().expect("one final handle");
        if runtime_available {
            runtime.block_on(async move {
                drop(final_handle);
                probe.close_started.notified().await;
                probe.close_release.notify_one();
                tokio::task::yield_now().await;
            });
            prop_assert_eq!(probe.close_calls.load(Ordering::SeqCst), 1);
            prop_assert_eq!(probe.abort_calls.load(Ordering::SeqCst), 0);
        } else {
            drop(runtime);
            drop(final_handle);
            prop_assert_eq!(probe.close_calls.load(Ordering::SeqCst), 0);
            prop_assert_eq!(probe.abort_calls.load(Ordering::SeqCst), 1);
        }
    }

    // Feature: rust-sdk-client-lifecycle, Property 18: injected execution preserves its abstraction
    #[test]
    fn injected_execution_preserves_requests(
        requests in proptest::collection::vec(("[a-zA-Z0-9_ {}]{1,64}", prop::option::of("[a-zA-Z_]{1,12}")), 0..12),
        timeout_enabled in any::<bool>(),
        inject_request_failure in any::<bool>(),
        close_mode in 0_u8..3,
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let probe = LifecycleProbe::new(close_mode);
            let timeout = timeout_enabled.then_some(Duration::from_secs(1));
            let client = injected_client(probe.clone(), timeout).await;
            for (index, (query, operation)) in requests.iter().enumerate() {
                let variables = serde_json::json!({"index": index});
                let mut request = RawRequest::new(query.clone()).with_variables(variables);
                if let Some(operation) = operation {
                    request = request.with_operation_name(operation.clone());
                }
                prop_assert!(client.execute(request).await.is_ok());
            }
            let observed = probe.requests.lock().expect("request log").clone();
            prop_assert_eq!(observed.len(), requests.len());
            for (index, ((query, operation), actual)) in requests.iter().zip(observed).enumerate() {
                prop_assert_eq!(actual.0.as_str(), query.as_str());
                prop_assert_eq!(actual.1, Some(serde_json::json!({"index": index})));
                prop_assert_eq!(actual.2.as_deref(), operation.as_deref());
            }

            if inject_request_failure {
                probe.execute_mode.store(5, Ordering::SeqCst);
                let failure = client.execute(RawRequest::new("query { failure }")).await;
                let retained_marker = match failure {
                    Err(RequestError::Connection(error)) => error
                        .source()
                        .is_some_and(|source| source.downcast_ref::<InjectedMarker>().is_some()),
                    _ => false,
                };
                prop_assert!(retained_marker);
            }

            let close_result = close_after_start(&client, &probe).await;
            let expected_close = match close_mode {
                CLOSE_OK => "ok",
                CLOSE_ERROR => "connection",
                _ => "panicked",
            };
            prop_assert_eq!(close_class(&close_result), expected_close);
            prop_assert_eq!(probe.close_calls.load(Ordering::SeqCst), 1);
            prop_assert_eq!(
                probe.abort_calls.load(Ordering::SeqCst),
                usize::from(close_mode != CLOSE_OK),
            );
            Ok(())
        })?;
    }

    // Feature: rust-sdk-client-lifecycle, Property 21: timeout phases are independent and non-poisoning
    #[test]
    fn timeout_phases_are_independent_and_non_poisoning(
        startup_ms in 1_u64..4,
        http_ms in 1_u64..4,
        execution_ms in 1_u64..4,
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let startup_timeout = Duration::from_millis(startup_ms);
            let startup_config = ClientConfig::builder()
                .session_startup_timeout(startup_timeout)
                .build()
                .expect("positive startup timeout");
            let startup = connect_with_connector(startup_config, &NeverConnector).await;
            let startup_timed_out = match startup {
                Err(crate::ConnectError::StartupTimeout { duration }) => duration == startup_timeout,
                _ => false,
            };
            prop_assert!(startup_timed_out);

            let http_probe = LifecycleProbe::new(CLOSE_OK);
            http_probe.execute_mode.store(4, Ordering::SeqCst);
            let http_timeout = Duration::from_millis(http_ms);
            let http_config = ClientConfig::builder()
                .http_connect_timeout(http_timeout)
                .build()
                .expect("positive HTTP timeout");
            let http_client = connect_with_connector(http_config, &ProbeConnector(http_probe.clone()))
                .await
                .expect("recording connector");
            let http_first = http_client.execute(RawRequest::new("query { connect }")).await;
            let http_timed_out = match http_first {
                Err(RequestError::TransportConnectTimeout { duration }) => duration == http_timeout,
                _ => false,
            };
            prop_assert!(http_timed_out);
            let http_reused = http_client
                .execute(RawRequest::new("query { ready }"))
                .await
                .is_ok();
            prop_assert!(http_reused);
            prop_assert!(close_after_start(&http_client, &http_probe).await.is_ok());

            let execution_probe = LifecycleProbe::new(CLOSE_OK);
            execution_probe.execute_mode.store(3, Ordering::SeqCst);
            let execution_timeout = Duration::from_millis(execution_ms);
            let client = injected_client(execution_probe.clone(), Some(execution_timeout)).await;
            let first = client.execute(RawRequest::new("query { slow }")).await;
            let timed_out_as_configured = match first {
                Err(RequestError::ExecutionTimeout { duration }) => duration == execution_timeout,
                _ => false,
            };
            prop_assert!(timed_out_as_configured);
            let second = client.execute(RawRequest::new("query { ready }")).await;
            prop_assert!(second.is_ok());
            execution_probe.execute_mode.store(2, Ordering::SeqCst);
            let cancelled = tokio::spawn({
                let client = client.clone();
                async move { client.execute(RawRequest::new("query { cancelled }")).await }
            });
            execution_probe.execute_started.notified().await;
            cancelled.abort();
            let _ = cancelled.await;
            execution_probe.execute_mode.store(0, Ordering::SeqCst);
            let usable_after_cancellation = client
                .execute(RawRequest::new("query { stillReady }"))
                .await
                .is_ok();
            prop_assert!(usable_after_cancellation);
            prop_assert_eq!(execution_probe.execute_calls.load(Ordering::SeqCst), 4);
            prop_assert!(close_after_start(&client, &execution_probe).await.is_ok());
            Ok(())
        })?;
    }
}

#[test]
fn lifecycle_atomic_protocol_is_exhaustive_under_loom() {
    loom::model(|| {
        use loom::sync::Arc as LoomArc;
        use loom::sync::atomic::{AtomicBool as LoomBool, AtomicUsize as LoomUsize};
        use loom::thread;

        let state = LoomArc::new(LoomUsize::new(0));
        let terminal = LoomArc::new(LoomBool::new(false));
        let close_calls = LoomArc::new(LoomUsize::new(0));
        let leases = LoomArc::new(LoomUsize::new(2));
        let final_drops = LoomArc::new(LoomUsize::new(0));

        let mut threads = Vec::new();
        for _ in 0..2 {
            let state = LoomArc::clone(&state);
            let terminal = LoomArc::clone(&terminal);
            let close_calls = LoomArc::clone(&close_calls);
            let leases = LoomArc::clone(&leases);
            let final_drops = LoomArc::clone(&final_drops);
            threads.push(thread::spawn(move || {
                if state
                    .compare_exchange(0, 1, Ordering::AcqRel, Ordering::Acquire)
                    .is_ok()
                {
                    close_calls.fetch_add(1, Ordering::SeqCst);
                    terminal.store(true, Ordering::Release);
                    state.store(2, Ordering::Release);
                }
                if leases.fetch_sub(1, Ordering::AcqRel) == 1 {
                    final_drops.fetch_add(1, Ordering::SeqCst);
                }
                if state.load(Ordering::Acquire) == 2 {
                    assert!(terminal.load(Ordering::Acquire));
                }
            }));
        }
        for thread in threads {
            thread.join().expect("loom thread");
        }
        assert_eq!(close_calls.load(Ordering::SeqCst), 1);
        assert_eq!(final_drops.load(Ordering::SeqCst), 1);
        assert!(terminal.load(Ordering::Acquire));
    });
}

#[test]
fn abandoned_close_task_records_terminal_result() {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("test runtime");
    let probe = LifecycleProbe::new(CLOSE_OK);
    let client = runtime.block_on(injected_client(probe.clone(), None));
    runtime.block_on(async {
        let waiter = tokio::spawn({
            let client = client.clone();
            async move { client.close().await }
        });
        probe.close_started.notified().await;
        waiter.abort();
    });
    drop(runtime);

    let retry_runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("retry runtime");
    let result = retry_runtime.block_on(client.close());
    assert!(matches!(result, Err(CloseError::Interrupted)));
    assert_eq!(probe.close_calls.load(Ordering::SeqCst), 1);
    assert_eq!(probe.abort_calls.load(Ordering::SeqCst), 1);
}
