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
}
