//! Pure Rust module-authoring models and source interpretation.
//!
//! This boundary accepts immutable typed values and returns typed values. It owns no
//! filesystem, process, network, Cargo, engine, publication, or user-code execution.

pub mod authoring;
pub mod canonical;
pub mod compiler;
pub mod descriptor;
pub mod diagnostic;
pub mod metadata;
pub mod model;
pub mod projection;
pub mod regeneration;
pub mod render;
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
pub use compiler::{ModuleCompilation, ModuleCompilationRequest, ModuleCompiler};
pub use descriptor::{DescriptorBuilder, DescriptorInput, descriptor_digest};
pub use diagnostic::{
    ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet, SafeDiagnosticSource,
};
pub use metadata::{
    ArgumentMetadata, CachePolicy, CompiledArgument, CompiledFunction, ExecutionKind,
    FunctionCompiler, FunctionKind, FunctionMetadata, FunctionReturn, FunctionRole, ReceiverKind,
};
pub use model::*;
pub use projection::{ModuleProjections, ProjectionCompiler};
pub use regeneration::{RegenerationPlan, RegenerationPlanner};
pub use render::{
    ModuleRenderRequest, ModuleRenderer, RenderedModuleAssets, manifest_digest, validate_manifest,
};
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
