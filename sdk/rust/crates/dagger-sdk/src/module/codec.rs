//! Version-locked typed bridges between authored items and generated module code.
//!
//! Procedural macros implement these traits inside the authored crate. Generated code
//! re-exports them through `crate::dagger_generated::__private`, so expansion never
//! depends on the spelling of the SDK dependency in `Cargo.toml`.

/// Hidden bridge ABI consumed by generated module support.
#[doc(hidden)]
pub mod __private {
    use std::error::Error;
    use std::fmt;
    use std::future::Future;
    use std::pin::Pin;

    use super::super::context::ModuleContextBase;
    use super::super::wire::ModuleJson;

    /// Current syntactic contract shared by source analysis and procedural macros.
    pub const AUTHORING_ABI_VERSION: u32 = 1;

    /// Boxed generated future whose lifetime remains tied to the active call.
    pub type ModuleBoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

    /// Typed failure to decode one module boundary value.
    #[derive(Clone, Copy, Debug, Eq, PartialEq)]
    pub struct DecodeError;

    impl fmt::Display for DecodeError {
        fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
            formatter.write_str("module input decoding failed")
        }
    }

    impl Error for DecodeError {}

    /// Typed failure to encode one module boundary value.
    #[derive(Clone, Copy, Debug, Eq, PartialEq)]
    pub struct EncodeError;

    impl fmt::Display for EncodeError {
        fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
            formatter.write_str("module output encoding failed")
        }
    }

    impl Error for EncodeError {}

    /// Const-generic witness for one normalized authoring declaration.
    #[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
    pub struct AuthoringFingerprint<const VALUE: u128>;

    /// Owned persistent-state access emitted for an authored object.
    pub trait ModuleObjectBridge: Sized {
        /// Tuple of exposed and private-persistent fields in authored order.
        type PersistentState;
        /// Const-generic witness of the normalized object declaration.
        type Fingerprint;

        /// Reconstructs the object without bypassing normal Rust initialization.
        fn from_persistent_state(state: Self::PersistentState) -> Self;

        /// Extracts only fields explicitly declared persistent.
        fn into_persistent_state(self) -> Self::PersistentState;

        /// Returns the declaration fingerprint expected by generated code.
        fn authoring_fingerprint() -> Self::Fingerprint;
    }

    /// Fingerprint witness emitted for an authored interface.
    pub trait ModuleInterfaceBridge {
        /// Const-generic witness of the normalized interface declaration.
        type Fingerprint;

        /// Returns the declaration fingerprint expected by generated code.
        fn authoring_fingerprint() -> Self::Fingerprint;
    }

    /// Fingerprint witness emitted for an authored enum.
    pub trait ModuleEnumBridge {
        /// Const-generic witness of the normalized enum declaration.
        type Fingerprint;

        /// Returns the declaration fingerprint expected by generated code.
        fn authoring_fingerprint() -> Self::Fingerprint;
    }

    /// Lossless owned access to an authored transparent scalar newtype.
    pub trait ModuleScalarBridge: Sized {
        /// Transparent scalar representation checked by generated codecs.
        type Representation;
        /// Const-generic witness of the normalized scalar declaration.
        type Fingerprint;

        /// Wraps one representation without transformation.
        fn from_representation(value: Self::Representation) -> Self;

        /// Unwraps one representation without transformation.
        fn into_representation(self) -> Self::Representation;

        /// Returns the declaration fingerprint expected by generated code.
        fn authoring_fingerprint() -> Self::Fingerprint;
    }

    /// Per-function fingerprint witness emitted by the `methods` attribute.
    pub trait ModuleMethodBridge<const VALUE: u128> {
        /// Returns the function declaration fingerprint expected by generated code.
        fn authoring_fingerprint() -> AuthoringFingerprint<VALUE>;
    }

    /// Generated input conversion for one exact descriptor type.
    pub trait ModuleInputCodec: Sized {
        /// Decodes one canonical input using the active session when handles re-enter.
        fn decode_input(
            value: &ModuleJson,
            context: &ModuleContextBase,
        ) -> Result<Self, DecodeError>;
    }

    /// Generated output conversion for one exact descriptor type.
    pub trait ModuleOutputCodec: Sized {
        /// Encodes one value, resolving generated handle IDs on the active session.
        fn encode_output<'a>(
            self,
            context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a;
    }

    /// Value with both exact input and output conversion.
    pub trait ModuleValueCodec: ModuleInputCodec + ModuleOutputCodec {}

    impl<T> ModuleValueCodec for T where T: ModuleInputCodec + ModuleOutputCodec {}

    impl ModuleInputCodec for String {
        fn decode_input(
            value: &ModuleJson,
            _context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            value
                .as_json()
                .as_str()
                .map(str::to_owned)
                .ok_or(DecodeError)
        }
    }

    impl ModuleOutputCodec for String {
        fn encode_output<'a>(
            self,
            _context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move { Ok(ModuleJson::new(serde_json::Value::String(self))) })
        }
    }

    impl ModuleInputCodec for i64 {
        fn decode_input(
            value: &ModuleJson,
            _context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            value.as_json().as_i64().ok_or(DecodeError)
        }
    }

    impl ModuleOutputCodec for i64 {
        fn encode_output<'a>(
            self,
            _context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move { Ok(ModuleJson::new(serde_json::Value::Number(self.into()))) })
        }
    }

    impl ModuleInputCodec for bool {
        fn decode_input(
            value: &ModuleJson,
            _context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            value.as_json().as_bool().ok_or(DecodeError)
        }
    }

    impl ModuleOutputCodec for bool {
        fn encode_output<'a>(
            self,
            _context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move { Ok(ModuleJson::new(serde_json::Value::Bool(self))) })
        }
    }

    impl ModuleInputCodec for f64 {
        fn decode_input(
            value: &ModuleJson,
            _context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            value.as_json().as_f64().ok_or(DecodeError)
        }
    }

    impl ModuleOutputCodec for f64 {
        fn encode_output<'a>(
            self,
            _context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move {
                serde_json::Number::from_f64(self)
                    .map(serde_json::Value::Number)
                    .map(ModuleJson::new)
                    .ok_or(EncodeError)
            })
        }
    }

    impl ModuleInputCodec for () {
        fn decode_input(
            value: &ModuleJson,
            _context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            value.as_json().is_null().then_some(()).ok_or(DecodeError)
        }
    }

    impl ModuleOutputCodec for () {
        fn encode_output<'a>(
            self,
            _context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move { Ok(ModuleJson::new(serde_json::Value::Null)) })
        }
    }

    impl<T> ModuleInputCodec for Vec<T>
    where
        T: ModuleInputCodec,
    {
        fn decode_input(
            value: &ModuleJson,
            context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            value
                .as_json()
                .as_array()
                .ok_or(DecodeError)?
                .iter()
                .map(|value| T::decode_input(&ModuleJson::new(value.clone()), context))
                .collect()
        }
    }

    impl<T> ModuleOutputCodec for Vec<T>
    where
        T: ModuleOutputCodec + Send,
    {
        fn encode_output<'a>(
            self,
            context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move {
                let mut encoded = Vec::with_capacity(self.len());
                for value in self {
                    encoded.push(value.encode_output(context).await?.into_json());
                }
                Ok(ModuleJson::new(serde_json::Value::Array(encoded)))
            })
        }
    }

    impl<T> ModuleInputCodec for Option<T>
    where
        T: ModuleInputCodec,
    {
        fn decode_input(
            value: &ModuleJson,
            context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            if value.as_json().is_null() {
                Ok(None)
            } else {
                T::decode_input(value, context).map(Some)
            }
        }
    }

    impl<T> ModuleOutputCodec for Option<T>
    where
        T: ModuleOutputCodec + Send,
    {
        fn encode_output<'a>(
            self,
            context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move {
                match self {
                    Some(value) => value.encode_output(context).await,
                    None => Ok(ModuleJson::new(serde_json::Value::Null)),
                }
            })
        }
    }

    #[cfg(feature = "gen")]
    impl<T> ModuleInputCodec for T
    where
        T: crate::Loadable + 'static,
    {
        fn decode_input(
            value: &ModuleJson,
            context: &ModuleContextBase,
        ) -> Result<Self, DecodeError> {
            let (id, concrete_type) = match value.as_json() {
                serde_json::Value::String(id) => (
                    id.as_str(),
                    <T as crate::loadable::private::Sealed>::graphql_type(),
                ),
                serde_json::Value::Object(object)
                    if object.keys().all(|key| key == "id" || key == "__typename") =>
                {
                    let id = object
                        .get("id")
                        .and_then(serde_json::Value::as_str)
                        .ok_or(DecodeError)?;
                    let concrete_type = object
                        .get("__typename")
                        .and_then(serde_json::Value::as_str)
                        .unwrap_or_else(|| <T as crate::loadable::private::Sealed>::graphql_type());
                    (id, concrete_type)
                }
                _ => return Err(DecodeError),
            };
            if id.is_empty()
                || id.len() > 16 * 1024
                || id.contains('\0')
                || concrete_type.is_empty()
                || !concrete_type
                    .chars()
                    .all(|character| character == '_' || character.is_ascii_alphanumeric())
            {
                return Err(DecodeError);
            }
            Ok(context
                .query_builder()
                .reenter_generated_handle(crate::Id::new(id), concrete_type))
        }
    }

    #[cfg(feature = "gen")]
    impl<T> ModuleOutputCodec for T
    where
        T: crate::Loadable + crate::IntoID<crate::Id> + Send + 'static,
    {
        fn encode_output<'a>(
            self,
            _context: &'a ModuleContextBase,
        ) -> ModuleBoxFuture<'a, Result<ModuleJson, EncodeError>>
        where
            Self: 'a,
        {
            Box::pin(async move {
                let id = self.into_id().await.map_err(|_| EncodeError)?;
                if id.as_str().is_empty() || id.as_str().contains('\0') {
                    return Err(EncodeError);
                }
                Ok(ModuleJson::new(serde_json::Value::String(id.into_inner())))
            })
        }
    }
}

#[cfg(all(test, feature = "gen"))]
mod tests {
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use async_trait::async_trait;
    use proptest::prelude::*;
    use serde_json::json;

    use super::__private::{ModuleInputCodec, ModuleOutputCodec};
    use crate::connection::{EngineConnection, EngineConnectionError, EngineConnectionErrorKind};
    use crate::graphql::{RawRequest, RawResponse, ResponseData};
    use crate::lifecycle::SessionHandle;
    use crate::module::context::{CurrentCall, ModuleCancellation, ModuleContextBase};
    use crate::module::wire::{CallSelector, ModuleJson, ModuleWireName};
    use crate::query::QueryBuilder;
    use crate::test_support::proptest_config;

    #[derive(Clone)]
    struct RecordingConnection {
        requests: Arc<AtomicUsize>,
        id: String,
        fail: bool,
    }

    #[async_trait]
    impl EngineConnection for RecordingConnection {
        async fn execute(
            &self,
            _request: RawRequest,
        ) -> Result<RawResponse, EngineConnectionError> {
            self.requests.fetch_add(1, Ordering::SeqCst);
            if self.fail {
                Err(EngineConnectionError::new(
                    EngineConnectionErrorKind::Unavailable,
                ))
            } else {
                Ok(RawResponse::new(ResponseData::Value(json!({
                    "node": {"id": self.id}
                }))))
            }
        }

        async fn close(&self) -> Result<(), EngineConnectionError> {
            Ok(())
        }

        fn abort(&self) {}
    }

    fn context(id: &str, fail: bool) -> (ModuleContextBase, Arc<AtomicUsize>) {
        let requests = Arc::new(AtomicUsize::new(0));
        let connection = RecordingConnection {
            requests: Arc::clone(&requests),
            id: id.to_owned(),
            fail,
        };
        let builder = QueryBuilder::new(SessionHandle::new(Box::new(connection), None, None));
        let selector = CallSelector::Invocation {
            parent_wire_name: ModuleWireName::new("Fixture").expect("static name is valid"),
            function_wire_name: ModuleWireName::new("run").expect("static name is valid"),
        };
        (
            ModuleContextBase::new(
                builder,
                ModuleCancellation::default(),
                opentelemetry::Context::new(),
                CurrentCall::new("call", selector).expect("static identity is valid"),
            ),
            requests,
        )
    }

    proptest! {
        #![proptest_config(proptest_config())]

        #[test]
        fn property_16_handle_reconstruction_retains_identity_session(
            id in "[a-zA-Z0-9_=-]{1,64}",
            interface in any::<bool>(),
            invalid in any::<bool>(),
        ) {
            let (context, requests) = context(&id, false);
            let session_identity = context.query_builder().session_identity();
            let value = if invalid {
                ModuleJson::new(serde_json::Value::String(String::new()))
            } else if interface {
                ModuleJson::new(json!({"id": id, "__typename": "Container"}))
            } else {
                ModuleJson::new(serde_json::Value::String(id.clone()))
            };

            if interface {
                let decoded = <crate::ExportableClient as ModuleInputCodec>::decode_input(&value, &context);
                if invalid {
                    prop_assert!(decoded.is_err());
                } else {
                    let handle = decoded.expect("valid interface identifier re-enters");
                    prop_assert_eq!(handle.session.identity(), session_identity);
                    let document = futures::executor::block_on(handle.selection.build())
                        .expect("re-entry selection is valid");
                    prop_assert!(document.contains("... on Container"));
                }
            } else {
                let decoded = <crate::Container as ModuleInputCodec>::decode_input(&value, &context);
                if invalid {
                    prop_assert!(decoded.is_err());
                } else {
                    let handle = decoded.expect("valid object identifier re-enters");
                    prop_assert_eq!(handle.session.identity(), session_identity);
                    let document = futures::executor::block_on(handle.selection.build())
                        .expect("re-entry selection is valid");
                    prop_assert!(document.contains("... on Container"));
                }
            }
            prop_assert_eq!(requests.load(Ordering::SeqCst), 0);
        }

        #[test]
        fn property_17_successful_values_encode_exactly_once(
            values in proptest::collection::vec(any::<i64>(), 0..12),
            optional in proptest::option::of("[a-zA-Z0-9 ]{0,32}"),
            id in "[a-zA-Z0-9_=-]{1,64}",
            fail_handle in any::<bool>(),
        ) {
            let (primitive_context, _) = context(&id, false);
            let integers = futures::executor::block_on(values.clone().encode_output(&primitive_context))
                .expect("integer lists encode");
            prop_assert_eq!(integers.as_json(), &json!(values));
            let optional_value = futures::executor::block_on(optional.clone().encode_output(&primitive_context))
                .expect("optional strings encode");
            prop_assert_eq!(optional_value.as_json(), &json!(optional));
            let unit = futures::executor::block_on(().encode_output(&primitive_context))
                .expect("unit encodes");
            prop_assert_eq!(unit.as_json(), &serde_json::Value::Null);

            let (handle_context, requests) = context(&id, fail_handle);
            let handle = <crate::Container as ModuleInputCodec>::decode_input(
                &ModuleJson::new(serde_json::Value::String(id.clone())),
                &handle_context,
            ).expect("valid handle input");
            let encoded = futures::executor::block_on(handle.encode_output(&handle_context));
            if fail_handle {
                prop_assert!(encoded.is_err());
            } else {
                let encoded = encoded.expect("handle ID resolves");
                prop_assert_eq!(encoded.as_json(), &json!(id));
            }
            prop_assert_eq!(requests.load(Ordering::SeqCst), 1);
        }
    }
}
