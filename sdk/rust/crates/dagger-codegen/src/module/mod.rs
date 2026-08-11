//! Pure Rust module-authoring models and source interpretation.
//!
//! This boundary accepts immutable typed values and returns typed values. It owns no
//! filesystem, process, network, Cargo, engine, publication, or user-code execution.

pub mod authoring;
pub mod canonical;
pub mod diagnostic;
pub mod metadata;
pub mod model;
pub mod source;
pub mod types;

pub use authoring::{
    AuthoringDeclaration, AuthoringDeclarationKind, AuthoringField, AuthoringFieldPolicy,
    AuthoringFunction, AuthoringInterfaceMethod, AuthoringParameter, AuthoringParser,
    AuthoringVariant, AuthoringVisibility,
};
pub use canonical::{
    CanonicalError, DigestDomain, canonical_bytes, canonical_digest, decode_canonical,
};
pub use diagnostic::{
    ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet, SafeDiagnosticSource,
};
pub use metadata::{
    ArgumentMetadata, CachePolicy, CompiledArgument, CompiledFunction, ExecutionKind,
    FunctionCompiler, FunctionKind, FunctionMetadata, FunctionReturn, FunctionRole, ReceiverKind,
};
pub use model::*;
pub use source::{
    GeneratedTypeBinding, GeneratedTypeKind, GeneratedTypeRegistry, ModuleDiscovery,
    ResolvedGeneratedType, ResolvedTypeAlias, SourceDiscovery, source_snapshot_digest,
};
pub use types::{
    ConstructionPolicy, EnumContract, EnumVariantContract, InterfaceContract, InterfaceMethod,
    ModuleValue, ModuleValueCodec, ObjectContract, ObjectFieldContract, ObjectFieldMode,
    ProjectedType, RustModuleType, ScalarContract, TypeCatalog, TypePolicyDisposition,
    TypePolicyRow, TypePosition, TypeResolver, rust_type_policy_manifest,
};
