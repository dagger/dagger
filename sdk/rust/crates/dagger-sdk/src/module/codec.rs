//! Version-locked typed bridges between authored items and generated module code.
//!
//! Procedural macros implement these traits inside the authored crate. Generated code
//! re-exports them through `crate::dagger_generated::__private`, so expansion never
//! depends on the spelling of the SDK dependency in `Cargo.toml`.

/// Hidden bridge ABI consumed by generated module support.
#[doc(hidden)]
pub mod __private {
    /// Current syntactic contract shared by source analysis and procedural macros.
    pub const AUTHORING_ABI_VERSION: u32 = 1;

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
}
