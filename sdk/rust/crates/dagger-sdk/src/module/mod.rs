//! Rust module-authoring values and the version-locked generated bridge boundary.
//!
//! Public application errors and call context are ordinary owned Rust values. The
//! hidden exact-version bridge carries typed wire values, codecs, static descriptors,
//! and call-scoped dispatch access needed by generated code. It cannot create or close
//! a connection, adopt process-global state, or erase values into a fallback codec.

mod codec;
mod context;
mod dispatch;
mod error;
mod view;
mod wire;

pub use context::{CurrentCall, ModuleCancellation, TelemetryContext};
pub use error::{ModuleError, ModuleErrorBuildError, ModuleErrorDetail};

#[doc(hidden)]
pub mod __private {
    pub use serde_json;

    pub use super::codec::__private::*;
    pub use super::context::ModuleContextBase;
    pub use super::dispatch::{
        CallOutcome, CallReceipt, DispatchRegistry, InvocationError, PreparedCall,
        RegistrationError, RegistrationSink, ResultPublishError, ResultSink,
    };
    pub use super::error::ModuleError;
    pub use super::view::{
        ArgumentView, FunctionView, ModuleDescriptorView, ModuleTypeKindView, ModuleTypeView,
        RegistrationView,
    };
    pub use super::wire::{
        CallEnvelope, CallIdentity, CallSelector, ModuleJson, ModuleWireName, NamedModuleArgument,
    };
}
