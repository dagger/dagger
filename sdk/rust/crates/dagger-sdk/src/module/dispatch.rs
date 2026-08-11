//! Version-locked registry and sink contracts for generated module dispatch.
//!
//! Generated code implements these traits over static descriptor assets. The generic
//! runtime can therefore select typed adapters and publish outcomes without reflection,
//! downcasting, link-time registries, or user-authored switch statements.

use std::error::Error;
use std::fmt;

use super::codec::__private::ModuleBoxFuture;
use super::context::ModuleContextBase;
use super::error::ModuleError;
use super::view::{ModuleDescriptorView, RegistrationView};
use super::wire::{CallIdentity, ModuleJson};

/// Validated call inputs owned by one generated dispatch arm.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PreparedCall {
    /// Stable call identity and selected coordinate.
    pub identity: CallIdentity,
    /// Canonical parent value retained for receiver decoding.
    pub parent: Option<ModuleJson>,
    /// Arguments ordered by the generated function descriptor.
    pub arguments: Vec<ModuleJson>,
}

/// One terminal outcome selected before publication.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CallOutcome {
    /// Canonically encoded successful value.
    Value(ModuleJson),
    /// Intentional structured application error.
    ApplicationError(ModuleError),
}

macro_rules! adapter_error {
    ($name:ident, $message:literal) => {
        #[doc = $message]
        #[derive(Clone, Copy, Debug, Eq, PartialEq)]
        pub struct $name;

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str($message)
            }
        }

        impl Error for $name {}
    };
}

adapter_error!(RegistrationError, "module registration publication failed");
adapter_error!(ResultPublishError, "module result publication failed");

/// Typed failure from one generated registry arm.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum InvocationError {
    /// Parent, argument, or handle decoding failed.
    Decode,
    /// Authored code returned an intentional application error.
    Application(ModuleError),
    /// Successful value encoding or handle-ID resolution failed.
    Encode,
    /// The parent coordinate is absent from the closed registry.
    UnknownParent,
    /// The function coordinate is absent below a known parent.
    UnknownFunction,
}

impl fmt::Display for InvocationError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Decode => "generated module input decoding failed",
            Self::Application(_) => "module application returned an error",
            Self::Encode => "generated module output encoding failed",
            Self::UnknownParent => "module dispatch parent is unknown",
            Self::UnknownFunction => "module dispatch function is unknown",
        })
    }
}

impl Error for InvocationError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Application(error) => Some(error),
            Self::Decode | Self::Encode | Self::UnknownParent | Self::UnknownFunction => None,
        }
    }
}

/// Closed generated dispatch registry.
#[doc(hidden)]
pub trait DispatchRegistry: Send + Sync {
    /// Returns the descriptor view compiled into this registry.
    fn descriptor(&self) -> &'static ModuleDescriptorView;

    /// Invokes one already-selected and validated typed adapter.
    fn invoke<'a>(
        &'a self,
        call: PreparedCall,
        context: ModuleContextBase,
    ) -> ModuleBoxFuture<'a, Result<ModuleJson, InvocationError>>;
}

/// Sink for one complete descriptor-derived registration projection.
#[doc(hidden)]
pub trait RegistrationSink: Send + Sync {
    /// Serves the complete registration projection exactly once.
    fn serve<'a>(
        &'a self,
        registration: &'a RegistrationView,
    ) -> ModuleBoxFuture<'a, Result<(), RegistrationError>>;
}

/// Sink for one selected terminal invocation outcome.
#[doc(hidden)]
pub trait ResultSink: Send + Sync {
    /// Publishes one terminal outcome without retry or fallback.
    fn publish<'a>(
        &'a self,
        outcome: CallOutcome,
    ) -> ModuleBoxFuture<'a, Result<(), ResultPublishError>>;
}

/// Successful registration or invocation handling receipt.
#[doc(hidden)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CallReceipt {
    /// Stable call identity accepted by the selected sink.
    pub identity: CallIdentity,
}
