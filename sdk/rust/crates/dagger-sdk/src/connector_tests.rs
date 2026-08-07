//! Connection-resource tests live here so private guard state never becomes public API.

use std::future::pending;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use proptest::prelude::*;

use crate::client::connect_with;
use crate::config::ClientConfig;
use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
use crate::connector::{CliSessionConnection, PendingConnection};
use crate::graphql::{RawRequest, RawResponse, ResponseData};
use crate::test_support::proptest_config;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ResourceEvent {
    TransportReleased,
    StdinClosed,
    ChildReaped,
    StdoutJoined,
    StderrJoined,
    Abort,
}

#[derive(Clone)]
struct ResourceProbe {
    cli_owned: bool,
    close_fails: bool,
    events: Arc<Mutex<Vec<ResourceEvent>>>,
}

impl ResourceProbe {
    fn new(cli_owned: bool, close_fails: bool) -> Self {
        Self {
            cli_owned,
            close_fails,
            events: Arc::new(Mutex::new(Vec::new())),
        }
    }

    fn record(&self, event: ResourceEvent) {
        self.events.lock().expect("resource event log").push(event);
    }
}

#[async_trait]
impl EngineConnection for ResourceProbe {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        Ok(RawResponse::new(ResponseData::Absent))
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        self.record(ResourceEvent::TransportReleased);
        if self.cli_owned {
            self.record(ResourceEvent::StdinClosed);
            self.record(ResourceEvent::ChildReaped);
            self.record(ResourceEvent::StdoutJoined);
            self.record(ResourceEvent::StderrJoined);
        }
        if self.close_fails {
            Err(EngineConnectionError::new(
                EngineConnectionErrorKind::Transport,
            ))
        } else {
            Ok(())
        }
    }

    fn abort(&self) {
        self.record(ResourceEvent::Abort);
    }
}

struct TaskEnd(Arc<AtomicUsize>);

impl Drop for TaskEnd {
    fn drop(&mut self) {
        self.0.fetch_add(1, Ordering::SeqCst);
    }
}

async fn guarded_task(ended: Arc<AtomicUsize>) {
    let _end = TaskEnd(ended);
    pending::<()>().await;
}

proptest! {
    #![proptest_config(proptest_config())]

    // Feature: rust-sdk-client-lifecycle, Property 6: close respects resource ownership
    #[test]
    fn close_respects_resource_ownership(cli_owned in any::<bool>(), close_fails in any::<bool>()) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let probe = ResourceProbe::new(cli_owned, close_fails);
            let config = ClientConfig::builder()
                .connection(Box::new(probe.clone()))
                .build()
                .expect("valid injected config");
            let client = connect_with(config).await.expect("injected client");
            let result = client.close().await;
            prop_assert_eq!(result.is_err(), close_fails);

            let events = probe.events.lock().expect("resource event log").clone();
            let expected = if cli_owned {
                vec![
                    ResourceEvent::TransportReleased,
                    ResourceEvent::StdinClosed,
                    ResourceEvent::ChildReaped,
                    ResourceEvent::StdoutJoined,
                    ResourceEvent::StderrJoined,
                ]
            } else {
                vec![ResourceEvent::TransportReleased]
            };
            prop_assert_eq!(&events[..expected.len()], expected.as_slice());
            prop_assert_eq!(
                events.iter().filter(|event| **event == ResourceEvent::Abort).count(),
                usize::from(close_fails),
            );
            if !cli_owned {
                prop_assert!(!events.iter().any(|event| matches!(
                    event,
                    ResourceEvent::StdinClosed
                        | ResourceEvent::ChildReaped
                        | ResourceEvent::StdoutJoined
                        | ResourceEvent::StderrJoined
                )));
            }
            Ok(())
        })?;
    }

    // Feature: rust-sdk-client-lifecycle, Property 9: pending connection resources cannot escape cancellation
    #[test]
    fn pending_resources_end_at_every_stage(
        io_task_count in 0_usize..8,
        cleanup_mode in 0_u8..3,
    ) {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("test runtime");
        runtime.block_on(async move {
            let ended = Arc::new(AtomicUsize::new(0));
            let mut pending = PendingConnection::new();
            for _ in 0..io_task_count {
                pending.push_io_task(tokio::spawn(guarded_task(Arc::clone(&ended))));
            }
            tokio::task::yield_now().await;

            match cleanup_mode {
                0 => pending.cleanup().await,
                1 => drop(pending),
                _ => drop(pending.disarm()),
            }

            tokio::time::timeout(Duration::from_secs(1), async {
                while ended.load(Ordering::SeqCst) != io_task_count {
                    tokio::task::yield_now().await;
                }
            })
            .await
            .expect("guard cleanup finishes every started I/O task");
            prop_assert_eq!(ended.load(Ordering::SeqCst), io_task_count);
            Ok(())
        })?;
    }
}

#[cfg(unix)]
#[tokio::test]
async fn pending_child_is_terminated_and_reaped_on_failure() {
    let mut pending = PendingConnection::new();
    let mut command = tokio::process::Command::new("sh");
    command.args(["-c", "sleep 30"]);
    pending
        .spawn_child(&mut command)
        .expect("portable test child starts");

    tokio::time::timeout(Duration::from_secs(3), pending.cleanup())
        .await
        .expect("child termination and reap are bounded");
}

#[cfg(unix)]
#[tokio::test]
async fn every_staged_child_and_io_acquisition_is_cleanup_safe() {
    for acquired_io_tasks in 0..=3 {
        let ended = Arc::new(AtomicUsize::new(0));
        let mut pending = PendingConnection::new();
        let mut command = tokio::process::Command::new("sh");
        command.args(["-c", "sleep 30"]);
        pending
            .spawn_child(&mut command)
            .expect("portable test child starts");

        for _ in 0..acquired_io_tasks {
            pending.push_io_task(tokio::spawn(guarded_task(Arc::clone(&ended))));
            tokio::task::yield_now().await;
        }

        tokio::time::timeout(Duration::from_secs(3), pending.cleanup())
            .await
            .expect("each partially acquired resource set cleans up");
        assert_eq!(ended.load(Ordering::SeqCst), acquired_io_tasks);
    }
}

#[tokio::test]
async fn cli_resource_close_waits_for_owned_io_tasks() {
    let ended = Arc::new(AtomicUsize::new(0));
    let mut pending = PendingConnection::new();
    pending.push_io_task(tokio::spawn({
        let ended = Arc::clone(&ended);
        async move {
            let _end = TaskEnd(ended);
            tokio::task::yield_now().await;
        }
    }));

    let transport = ResourceProbe::new(false, false);
    let connection = CliSessionConnection::new(Box::new(transport), pending.disarm());
    connection.close().await.expect("owned CLI resource closes");

    assert_eq!(ended.load(Ordering::SeqCst), 1);
}
