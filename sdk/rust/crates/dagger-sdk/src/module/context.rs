//! Call-scoped module context shared with generated bindings.
//!
//! The public values in this module describe one active call without owning a client
//! lifecycle. Generated code receives the hidden context base, clones its existing
//! query builder, and therefore cannot reconnect or create process-global state.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use tokio::sync::Notify;

use super::wire::CallSelector;
use crate::QueryBuilder;

/// OpenTelemetry context inherited by one active module call.
pub type TelemetryContext = opentelemetry::Context;

#[derive(Debug, Default)]
struct CancellationState {
    cancelled: AtomicBool,
    notification: Notify,
}

/// Cloneable cancellation signal scoped to one module invocation.
///
/// Cancellation is monotonic. Clones observe the same transition, while signals from
/// another call use a different state allocation and cannot interfere.
#[derive(Clone, Debug, Default)]
pub struct ModuleCancellation(Arc<CancellationState>);

impl ModuleCancellation {
    /// Returns whether cancellation has already been requested.
    #[must_use]
    pub fn is_cancelled(&self) -> bool {
        self.0.cancelled.load(Ordering::Acquire)
    }

    /// Waits until cancellation is requested.
    pub async fn cancelled(&self) {
        loop {
            if self.is_cancelled() {
                return;
            }
            let notified = self.0.notification.notified();
            // Register the waiter before the second observation so a cancellation
            // between the two checks cannot be missed.
            if self.is_cancelled() {
                return;
            }
            notified.await;
        }
    }

    /// Requests cancellation for generated runtime adapters.
    #[doc(hidden)]
    pub fn cancel(&self) {
        if !self.0.cancelled.swap(true, Ordering::AcqRel) {
            self.0.notification.notify_waiters();
        }
    }
}

/// Stable coordinates for the module call currently being executed.
///
/// The call ID is local diagnostic identity rather than an engine wire field. The
/// selector retains registration versus invocation without confusing the engine's
/// empty parent-name registration signal with its empty function-name constructor.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CurrentCall {
    call_id: String,
    selector: CallSelector,
}

impl CurrentCall {
    /// Returns the call-local diagnostic identity.
    #[must_use]
    pub fn id(&self) -> &str {
        &self.call_id
    }

    /// Borrows the exact-version selector for generated adapters.
    #[doc(hidden)]
    #[must_use]
    pub const fn selector(&self) -> &CallSelector {
        &self.selector
    }

    /// Returns whether this call requests module registration.
    #[must_use]
    pub const fn is_registration(&self) -> bool {
        matches!(self.selector, CallSelector::Registration)
    }

    /// Returns the selected parent wire name for an invocation.
    #[must_use]
    pub fn parent_wire_name(&self) -> Option<&str> {
        match &self.selector {
            CallSelector::Registration => None,
            CallSelector::Invocation {
                parent_wire_name, ..
            } => Some(parent_wire_name.as_str()),
        }
    }

    /// Returns the selected function wire name; an empty value identifies a constructor.
    #[must_use]
    pub fn function_wire_name(&self) -> Option<&str> {
        match &self.selector {
            CallSelector::Registration => None,
            CallSelector::Invocation {
                function_wire_name, ..
            } => Some(function_wire_name.as_str()),
        }
    }

    /// Constructs validated current-call coordinates for generated adapters.
    #[doc(hidden)]
    pub fn new(call_id: impl Into<String>, selector: CallSelector) -> Result<Self, &'static str> {
        let call_id = call_id.into();
        if call_id.is_empty() || call_id.len() > 128 || call_id.contains('\0') {
            return Err("module call identity is invalid");
        }
        Ok(Self { call_id, selector })
    }
}

/// Exact-version context bridge consumed by generated module code.
///
/// This type deliberately has no state codec and no connection constructor. The only
/// query builder it can expose is the one already bound to the active shared session.
#[doc(hidden)]
#[derive(Clone)]
pub struct ModuleContextBase {
    query_builder: QueryBuilder,
    cancellation: ModuleCancellation,
    telemetry: TelemetryContext,
    call: CurrentCall,
}

impl ModuleContextBase {
    /// Constructs a context from one already-active shared session.
    #[doc(hidden)]
    #[must_use]
    pub fn new(
        query_builder: QueryBuilder,
        cancellation: ModuleCancellation,
        telemetry: TelemetryContext,
        call: CurrentCall,
    ) -> Self {
        Self {
            query_builder,
            cancellation,
            telemetry,
            call,
        }
    }

    /// Clones the immutable builder while retaining the same session lease.
    #[doc(hidden)]
    #[must_use]
    pub fn query_builder(&self) -> QueryBuilder {
        self.query_builder.clone()
    }

    /// Borrows this call's cancellation signal.
    #[doc(hidden)]
    #[must_use]
    pub const fn cancellation(&self) -> &ModuleCancellation {
        &self.cancellation
    }

    /// Borrows this call's telemetry context.
    #[doc(hidden)]
    #[must_use]
    pub const fn telemetry_context(&self) -> &TelemetryContext {
        &self.telemetry
    }

    /// Borrows this call's stable coordinates.
    #[doc(hidden)]
    #[must_use]
    pub const fn current_call(&self) -> &CurrentCall {
        &self.call
    }
}

#[cfg(test)]
mod tests {
    use async_trait::async_trait;
    use proptest::prelude::*;

    use super::{CurrentCall, ModuleCancellation, ModuleContextBase};
    use crate::connection::{EngineConnection, EngineConnectionError};
    use crate::graphql::{RawRequest, RawResponse, ResponseData};
    use crate::lifecycle::SessionHandle;
    use crate::module::wire::{CallSelector, ModuleFunctionName, ModuleWireName};
    use crate::query::QueryBuilder;
    use crate::test_support::proptest_config;

    struct InertConnection;

    #[async_trait]
    impl EngineConnection for InertConnection {
        async fn execute(
            &self,
            _request: RawRequest,
        ) -> Result<RawResponse, EngineConnectionError> {
            Ok(RawResponse::new(ResponseData::Null))
        }

        async fn close(&self) -> Result<(), EngineConnectionError> {
            Ok(())
        }

        fn abort(&self) {}
    }

    fn query_builder() -> QueryBuilder {
        QueryBuilder::new(SessionHandle::new(Box::new(InertConnection), None, None))
    }

    fn selector() -> CallSelector {
        CallSelector::Invocation {
            parent_wire_name: ModuleWireName::new("Fixture").expect("static name is valid"),
            function_wire_name: ModuleFunctionName::new("run").expect("static name is valid"),
        }
    }

    #[tokio::test]
    async fn cancellation_is_monotonic_and_shared_only_by_clones() {
        let first = ModuleCancellation::default();
        let clone = first.clone();
        let sibling = ModuleCancellation::default();

        clone.cancel();
        first.cancelled().await;

        assert!(first.is_cancelled());
        assert!(!sibling.is_cancelled());
    }

    proptest! {
        #![proptest_config(proptest_config())]

        #[test]
        fn property_19_module_context_scoped_active_call(
            call_id in "[a-zA-Z0-9-]{1,32}",
            cancel in any::<bool>(),
            choose_immediate in any::<bool>(),
        ) {
            let builder = query_builder();
            let session_identity = builder.session_identity();
            let cancellation = ModuleCancellation::default();
            let context = ModuleContextBase::new(
                builder,
                cancellation.clone(),
                opentelemetry::Context::new(),
                CurrentCall::new(call_id.clone(), selector()).expect("generated identity is valid"),
            );
            let sibling = ModuleContextBase::new(
                query_builder(),
                ModuleCancellation::default(),
                opentelemetry::Context::new(),
                CurrentCall::new(format!("sibling-{call_id}"), selector()).expect("generated identity is valid"),
            );

            let root = context.query_builder().generated_query_root();
            let observed_identity = if choose_immediate {
                root.session.identity()
            } else {
                root.container().session.identity()
            };
            prop_assert_eq!(observed_identity, session_identity);
            prop_assert_eq!(context.current_call().id(), call_id);
            prop_assert_ne!(sibling.query_builder().session_identity(), session_identity);
            prop_assert!(!sibling.cancellation().is_cancelled());

            if cancel {
                cancellation.cancel();
                prop_assert!(context.cancellation().is_cancelled());
                prop_assert!(!sibling.cancellation().is_cancelled());
            }
        }
    }
}
