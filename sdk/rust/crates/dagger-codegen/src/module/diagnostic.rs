//! Stable, source-located, credential-safe diagnostics for module compilation.

use std::error::Error;
use std::fmt;

use serde::{Deserialize, Deserializer, Serialize};

use super::model::{GeneratedCoordinate, RustSymbol, SourceCoordinate, WireName};

const MAX_SOURCE_CHAIN_DEPTH: usize = 8;

/// Closed module compiler/runtime/evidence diagnostic taxonomy.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ModuleDiagnosticCode {
    /// Capability scope or mapping is incomplete or inconsistent.
    ModuleScopeInvalid,
    /// Evidence is stale, failed, target-incompatible, or outside its admitted domain.
    ModuleEvidenceRejected,
    /// No valid root object was discovered.
    RootMissing,
    /// More than one root object was discovered.
    RootAmbiguous,
    /// Source module traversal is invalid.
    SourceModuleInvalid,
    /// A Rust import, alias, or module path is invalid.
    RustPathInvalid,
    /// An exported declaration depends on unresolved configuration.
    CfgUnresolved,
    /// A foreign Rust type has no supported target mapping.
    ForeignTypeUnsupported,
    /// A checked generated type has stale or incompatible provenance.
    GeneratedTypeStale,
    /// A referenced local contract lacks an explicit export marker.
    ExplicitExportRequired,
    /// An exported type is inaccessible to generated sibling code.
    ExportVisibilityInvalid,
    /// Authoring metadata is syntactically malformed.
    MetadataMalformed,
    /// Authoring metadata is unknown to the selected ABI or target.
    MetadataUnknown,
    /// Authoring metadata is duplicated, conflicting, or target-invalid.
    MetadataConflict,
    /// Source and procedural interpretations have different fingerprints.
    AuthoringFingerprintMismatch,
    /// Two authored coordinates normalize to the same wire name.
    WireNameCollision,
    /// A wire or Rust name is invalid.
    NameInvalid,
    /// Root construction is missing, duplicated, or unsafe.
    ConstructorInvalid,
    /// Object state cannot be reconstructed losslessly.
    StateShapeInvalid,
    /// Parent JSON is malformed.
    ParentJsonInvalid,
    /// Parent JSON has an incompatible state shape.
    ParentStateInvalid,
    /// An interface declaration or implementation is invalid.
    InterfaceInvalid,
    /// An enum contract or member value is invalid.
    EnumInvalid,
    /// A scalar is not a transparent supported newtype.
    ScalarInvalid,
    /// A Rust type or wrapper has no lossless target mapping.
    TypeUnsupported,
    /// A numeric value is outside the target range.
    NumericOutOfRange,
    /// A JSON value has the wrong kind or structure.
    ValueDecodeFailed,
    /// A default expression is unsupported or type-incompatible.
    DefaultInvalid,
    /// A function receiver, generic, argument, or return shape is unsupported.
    FunctionSignatureInvalid,
    /// Function or argument metadata is target-incompatible.
    FunctionMetadataInvalid,
    /// A descriptor invariant or strict decode failed.
    DescriptorInvalid,
    /// Registration and introspection projections disagree.
    ProjectionMismatch,
    /// A local wire coordinate conflicts with checked visible schema.
    VisibleSchemaCollision,
    /// A call names an unknown parent.
    UnknownParent,
    /// A call names an unknown function for a valid parent.
    UnknownFunction,
    /// A generated dispatch coordinate occurs more than once.
    DispatchDuplicate,
    /// A required argument is absent.
    ArgumentMissing,
    /// An argument occurs more than once.
    ArgumentDuplicate,
    /// A supplied argument name is unknown.
    ArgumentUnknown,
    /// An argument cannot be decoded to its exact Rust type.
    ArgumentDecodeFailed,
    /// A handle or interface identity cannot re-enter the active session.
    HandleReentryFailed,
    /// Authored code returned an intentional application error.
    ModuleApplication,
    /// A user panic was contained at the invocation boundary.
    PanicContained,
    /// A successful result cannot be encoded or resolved.
    ResultEncodeFailed,
    /// A second terminal result was attempted.
    ResultAlreadySet,
    /// A selected terminal result could not be published.
    ResultPublishFailed,
    /// A call was cancelled before publication.
    DispatchCancelled,
    /// Call-owned work could not terminate after cancellation.
    CancellationCleanupFailed,
    /// Active module context construction failed.
    ModuleContextFailed,
    /// An operation and its subsequent close both failed.
    OperationAndCloseFailed,
    /// Session close failed after otherwise successful work.
    SessionCloseFailed,
    /// Asset rendering or formatting failed.
    ModuleGenerationFailed,
    /// Generated ownership is unknown or collides with user content.
    GeneratedOwnershipConflict,
    /// Checked generated assets are stale or missing.
    GeneratedAssetsStale,
    /// Atomic publication or restoration failed.
    ModulePublicationFailed,
    /// A checkpoint entered an engine, network, or unrelated SDK boundary.
    CheckpointScopeInvalid,
    /// The public Cargo package graph is cyclic or version-incoherent.
    PackageGraphInvalid,
    /// An unsafe diagnostic value reached the renderer boundary.
    DiagnosticRedactionFailed,
}

impl ModuleDiagnosticCode {
    /// Complete diagnostic taxonomy in a stable exhaustive order.
    pub const ALL: &'static [Self] = &[
        Self::ModuleScopeInvalid,
        Self::ModuleEvidenceRejected,
        Self::RootMissing,
        Self::RootAmbiguous,
        Self::SourceModuleInvalid,
        Self::RustPathInvalid,
        Self::CfgUnresolved,
        Self::ForeignTypeUnsupported,
        Self::GeneratedTypeStale,
        Self::ExplicitExportRequired,
        Self::ExportVisibilityInvalid,
        Self::MetadataMalformed,
        Self::MetadataUnknown,
        Self::MetadataConflict,
        Self::AuthoringFingerprintMismatch,
        Self::WireNameCollision,
        Self::NameInvalid,
        Self::ConstructorInvalid,
        Self::StateShapeInvalid,
        Self::ParentJsonInvalid,
        Self::ParentStateInvalid,
        Self::InterfaceInvalid,
        Self::EnumInvalid,
        Self::ScalarInvalid,
        Self::TypeUnsupported,
        Self::NumericOutOfRange,
        Self::ValueDecodeFailed,
        Self::DefaultInvalid,
        Self::FunctionSignatureInvalid,
        Self::FunctionMetadataInvalid,
        Self::DescriptorInvalid,
        Self::ProjectionMismatch,
        Self::VisibleSchemaCollision,
        Self::UnknownParent,
        Self::UnknownFunction,
        Self::DispatchDuplicate,
        Self::ArgumentMissing,
        Self::ArgumentDuplicate,
        Self::ArgumentUnknown,
        Self::ArgumentDecodeFailed,
        Self::HandleReentryFailed,
        Self::ModuleApplication,
        Self::PanicContained,
        Self::ResultEncodeFailed,
        Self::ResultAlreadySet,
        Self::ResultPublishFailed,
        Self::DispatchCancelled,
        Self::CancellationCleanupFailed,
        Self::ModuleContextFailed,
        Self::OperationAndCloseFailed,
        Self::SessionCloseFailed,
        Self::ModuleGenerationFailed,
        Self::GeneratedOwnershipConflict,
        Self::GeneratedAssetsStale,
        Self::ModulePublicationFailed,
        Self::CheckpointScopeInvalid,
        Self::PackageGraphInvalid,
        Self::DiagnosticRedactionFailed,
    ];

    /// Returns the stable external code carried across compiler and adapter boundaries.
    #[must_use]
    pub const fn external(self) -> &'static str {
        match self {
            Self::ModuleScopeInvalid => "module.scope-invalid",
            Self::ModuleEvidenceRejected => "module.evidence-rejected",
            Self::RootMissing => "module.root-missing",
            Self::RootAmbiguous => "module.root-ambiguous",
            Self::SourceModuleInvalid => "module.source-invalid",
            Self::RustPathInvalid => "module.rust-path",
            Self::CfgUnresolved => "module.cfg-unresolved",
            Self::ForeignTypeUnsupported => "module.foreign-type",
            Self::GeneratedTypeStale => "module.generated-type-stale",
            Self::ExplicitExportRequired => "module.export-required",
            Self::ExportVisibilityInvalid => "module.export-visibility",
            Self::MetadataMalformed => "module.metadata-malformed",
            Self::MetadataUnknown => "module.metadata-unknown",
            Self::MetadataConflict => "module.metadata-conflict",
            Self::AuthoringFingerprintMismatch => "module.authoring-drift",
            Self::WireNameCollision => "module.name-collision",
            Self::NameInvalid => "module.name-invalid",
            Self::ConstructorInvalid => "module.constructor-invalid",
            Self::StateShapeInvalid => "module.state-invalid",
            Self::ParentJsonInvalid => "module.parent-json-invalid",
            Self::ParentStateInvalid => "module.parent-state-invalid",
            Self::InterfaceInvalid => "module.interface-invalid",
            Self::EnumInvalid => "module.enum-invalid",
            Self::ScalarInvalid => "module.scalar-invalid",
            Self::TypeUnsupported => "module.type-unsupported",
            Self::NumericOutOfRange => "module.numeric-range",
            Self::ValueDecodeFailed => "module.value-decode",
            Self::DefaultInvalid => "module.default-invalid",
            Self::FunctionSignatureInvalid => "module.function-signature",
            Self::FunctionMetadataInvalid => "module.function-metadata",
            Self::DescriptorInvalid => "module.descriptor-invalid",
            Self::ProjectionMismatch => "module.projection-mismatch",
            Self::VisibleSchemaCollision => "module.schema-collision",
            Self::UnknownParent => "module.unknown-parent",
            Self::UnknownFunction => "module.unknown-function",
            Self::DispatchDuplicate => "module.dispatch-duplicate",
            Self::ArgumentMissing => "module.argument-missing",
            Self::ArgumentDuplicate => "module.argument-duplicate",
            Self::ArgumentUnknown => "module.argument-unknown",
            Self::ArgumentDecodeFailed => "module.argument-decode",
            Self::HandleReentryFailed => "module.handle-reentry",
            Self::ModuleApplication => "module.application",
            Self::PanicContained => "module.panic",
            Self::ResultEncodeFailed => "module.result-encode",
            Self::ResultAlreadySet => "module.result-already-set",
            Self::ResultPublishFailed => "module.result-publish",
            Self::DispatchCancelled => "module.cancelled",
            Self::CancellationCleanupFailed => "module.cancel-cleanup",
            Self::ModuleContextFailed => "module.context",
            Self::OperationAndCloseFailed => "module.operation-and-close",
            Self::SessionCloseFailed => "module.close",
            Self::ModuleGenerationFailed => "module.generation",
            Self::GeneratedOwnershipConflict => "module.ownership",
            Self::GeneratedAssetsStale => "module.generated-stale",
            Self::ModulePublicationFailed => "module.publication",
            Self::CheckpointScopeInvalid => "module.checkpoint-scope",
            Self::PackageGraphInvalid => "module.package-graph",
            Self::DiagnosticRedactionFailed => "module.redaction",
        }
    }
}

/// Closed class for a safe underlying compiler/runtime source.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum DiagnosticSourceKind {
    /// Cargo metadata or package resolution.
    Cargo,
    /// Rust compiler or formatter.
    Rustc,
    /// Confined filesystem access.
    Filesystem,
    /// Typed value codec.
    Codec,
    /// GraphQL query execution.
    Query,
    /// Session transport.
    Transport,
    /// Generated-asset publication.
    Publication,
}

/// Bounded, redacted error source safe for diagnostic rendering.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct SafeDiagnosticSource {
    /// Typed source class.
    kind: DiagnosticSourceKind,
    /// Safe bounded fact rather than arbitrary external error prose.
    message: String,
    /// Optional earlier source in the typed chain.
    source: Option<Box<SafeDiagnosticSource>>,
}

impl SafeDiagnosticSource {
    /// Constructs a safe source after rejecting unbounded or credential-shaped text.
    pub fn new(kind: DiagnosticSourceKind, message: impl Into<String>) -> Result<Self, String> {
        let message = message.into();
        if message.is_empty() || message.len() > 512 || looks_sensitive(&message) {
            return Err("diagnostic source is not safe to render".to_owned());
        }
        Ok(Self {
            kind,
            message,
            source: None,
        })
    }

    /// Returns the closed class of the underlying operation.
    #[must_use]
    pub const fn kind(&self) -> DiagnosticSourceKind {
        self.kind
    }

    /// Borrows the bounded, credential-safe source fact.
    #[must_use]
    pub fn message(&self) -> &str {
        &self.message
    }

    /// Returns the next safe source in the bounded chain, when one exists.
    #[must_use]
    pub fn source_fact(&self) -> Option<&Self> {
        self.source.as_deref()
    }

    /// Attaches one already-safe underlying source within the chain-depth bound.
    pub fn with_source(mut self, source: SafeDiagnosticSource) -> Result<Self, String> {
        if source.depth() >= MAX_SOURCE_CHAIN_DEPTH {
            return Err("diagnostic source chain is too deep".to_owned());
        }
        self.source = Some(Box::new(source));
        Ok(self)
    }

    fn depth(&self) -> usize {
        1 + self.source.as_deref().map_or(0, Self::depth)
    }
}

impl<'de> Deserialize<'de> for SafeDiagnosticSource {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Raw {
            kind: DiagnosticSourceKind,
            message: String,
            source: Option<SafeDiagnosticSource>,
        }

        let raw = Raw::deserialize(deserializer)?;
        let source = Self::new(raw.kind, raw.message).map_err(serde::de::Error::custom)?;
        match raw.source {
            Some(inner) => source.with_source(inner).map_err(serde::de::Error::custom),
            None => Ok(source),
        }
    }
}

impl fmt::Display for SafeDiagnosticSource {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl Error for SafeDiagnosticSource {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.source
            .as_deref()
            .map(|source| source as &(dyn Error + 'static))
    }
}

/// One deterministic module diagnostic with safe semantic coordinates.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct ModuleDiagnostic {
    /// Stable typed code.
    code: ModuleDiagnosticCode,
    /// Primary authored repair coordinate, when one exists.
    source_coordinate: Option<SourceCoordinate>,
    /// Optional generated location and its durable authored mapping.
    generated_coordinate: Option<GeneratedCoordinate>,
    /// Owning Rust symbol, when resolution reached one.
    rust_symbol: Option<RustSymbol>,
    /// Parent/type/member wire coordinate, when one exists.
    wire_name: Option<WireName>,
    /// Static or otherwise reviewed credential-safe explanation.
    message: String,
    /// Stable repair guidance.
    remediation: String,
    /// Optional typed safe source chain.
    cause: Option<SafeDiagnosticSource>,
}

impl ModuleDiagnostic {
    /// Constructs a diagnostic after enforcing the renderer's safe-text boundary.
    pub fn new(
        code: ModuleDiagnosticCode,
        source_coordinate: Option<SourceCoordinate>,
        message: impl Into<String>,
        remediation: impl Into<String>,
    ) -> Result<Self, String> {
        let message = message.into();
        let remediation = remediation.into();
        if message.is_empty()
            || remediation.is_empty()
            || message.len() > 512
            || remediation.len() > 512
            || looks_sensitive(&message)
            || looks_sensitive(&remediation)
        {
            return Err("module diagnostic contains unsafe text".to_owned());
        }
        Ok(Self {
            code,
            source_coordinate,
            generated_coordinate: None,
            rust_symbol: None,
            wire_name: None,
            message,
            remediation,
            cause: None,
        })
    }

    /// Adds a safe Rust symbol coordinate.
    #[must_use]
    pub fn with_rust_symbol(mut self, rust_symbol: RustSymbol) -> Self {
        self.rust_symbol = Some(rust_symbol);
        self
    }

    /// Adds a safe wire coordinate.
    #[must_use]
    pub fn with_wire_name(mut self, wire_name: WireName) -> Self {
        self.wire_name = Some(wire_name);
        self
    }

    /// Retains a generated location while making its authored mapping primary.
    #[must_use]
    pub fn with_generated_coordinate(mut self, generated: GeneratedCoordinate) -> Self {
        self.source_coordinate = Some(generated.authored.clone());
        self.generated_coordinate = Some(generated);
        self
    }

    /// Returns the stable diagnostic code.
    #[must_use]
    pub const fn code(&self) -> ModuleDiagnosticCode {
        self.code
    }

    /// Returns the primary authored repair coordinate, when one exists.
    #[must_use]
    pub const fn source_coordinate(&self) -> Option<&SourceCoordinate> {
        self.source_coordinate.as_ref()
    }

    /// Returns the retained generated-to-authored mapping, when one exists.
    #[must_use]
    pub const fn generated_coordinate(&self) -> Option<&GeneratedCoordinate> {
        self.generated_coordinate.as_ref()
    }

    /// Returns the owning Rust symbol, when resolution reached one.
    #[must_use]
    pub const fn rust_symbol(&self) -> Option<&RustSymbol> {
        self.rust_symbol.as_ref()
    }

    /// Returns the wire coordinate, when one exists.
    #[must_use]
    pub const fn wire_name(&self) -> Option<&WireName> {
        self.wire_name.as_ref()
    }

    /// Borrows the bounded, credential-safe explanation.
    #[must_use]
    pub fn message(&self) -> &str {
        &self.message
    }

    /// Borrows the stable remediation text.
    #[must_use]
    pub fn remediation(&self) -> &str {
        &self.remediation
    }

    /// Returns the typed safe source chain, when one exists.
    #[must_use]
    pub const fn cause(&self) -> Option<&SafeDiagnosticSource> {
        self.cause.as_ref()
    }

    /// Adds an already-redacted source chain.
    #[must_use]
    pub fn with_cause(mut self, cause: SafeDiagnosticSource) -> Self {
        self.cause = Some(cause);
        self
    }
}

impl<'de> Deserialize<'de> for ModuleDiagnostic {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Raw {
            code: ModuleDiagnosticCode,
            source_coordinate: Option<SourceCoordinate>,
            generated_coordinate: Option<GeneratedCoordinate>,
            rust_symbol: Option<RustSymbol>,
            wire_name: Option<WireName>,
            message: String,
            remediation: String,
            cause: Option<SafeDiagnosticSource>,
        }

        let raw = Raw::deserialize(deserializer)?;
        if let Some(generated) = &raw.generated_coordinate
            && raw.source_coordinate.as_ref() != Some(&generated.authored)
        {
            return Err(serde::de::Error::custom(
                "generated diagnostic must retain its authored coordinate as primary",
            ));
        }
        let mut diagnostic = Self::new(
            raw.code,
            raw.source_coordinate,
            raw.message,
            raw.remediation,
        )
        .map_err(serde::de::Error::custom)?;
        diagnostic.generated_coordinate = raw.generated_coordinate;
        diagnostic.rust_symbol = raw.rust_symbol;
        diagnostic.wire_name = raw.wire_name;
        diagnostic.cause = raw.cause;
        Ok(diagnostic)
    }
}

impl fmt::Display for ModuleDiagnostic {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{}: {}", self.code.external(), self.message)
    }
}

impl Error for ModuleDiagnostic {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.cause
            .as_ref()
            .map(|source| source as &(dyn Error + 'static))
    }
}

/// Non-empty, deterministically ordered compiler diagnostics.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ModuleDiagnosticSet(Vec<ModuleDiagnostic>);

impl ModuleDiagnosticSet {
    /// Sorts and retains a non-empty diagnostic collection.
    pub fn new(diagnostics: impl IntoIterator<Item = ModuleDiagnostic>) -> Option<Self> {
        let mut diagnostics = diagnostics.into_iter().collect::<Vec<_>>();
        diagnostics.sort_by(|left, right| {
            left.code
                .cmp(&right.code)
                .then_with(|| left.source_coordinate.cmp(&right.source_coordinate))
                .then_with(|| left.wire_name.cmp(&right.wire_name))
                .then_with(|| left.rust_symbol.cmp(&right.rust_symbol))
        });
        (!diagnostics.is_empty()).then_some(Self(diagnostics))
    }

    /// Borrows diagnostics in canonical order.
    #[must_use]
    pub fn diagnostics(&self) -> &[ModuleDiagnostic] {
        &self.0
    }
}

impl fmt::Display for ModuleDiagnosticSet {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        for (index, diagnostic) in self.0.iter().enumerate() {
            if index != 0 {
                formatter.write_str("\n")?;
            }
            write!(formatter, "{diagnostic}")?;
        }
        Ok(())
    }
}

impl Error for ModuleDiagnosticSet {}

fn looks_sensitive(value: &str) -> bool {
    let lowercase = value.to_ascii_lowercase();
    lowercase.contains("authorization:")
        || lowercase.contains("bearer ")
        || lowercase.contains("dagger_session_token")
        || lowercase.contains("token=")
        || lowercase.contains("password=")
        || lowercase.contains("://") && lowercase.contains('@')
}

#[cfg(test)]
mod tests {
    use std::num::NonZeroU32;

    use super::{
        DiagnosticSourceKind, ModuleDiagnostic, ModuleDiagnosticCode, SafeDiagnosticSource,
    };
    use crate::module::model::{
        GeneratedAssetPath, GeneratedCoordinate, ModuleSourcePath, SourceCoordinate,
    };

    #[test]
    fn unsafe_sources_and_messages_are_rejected() {
        assert!(
            SafeDiagnosticSource::new(DiagnosticSourceKind::Transport, "token=secret").is_err()
        );
        assert!(
            ModuleDiagnostic::new(
                ModuleDiagnosticCode::DiagnosticRedactionFailed,
                None,
                "Authorization: Basic secret",
                "remove unsafe text",
            )
            .is_err()
        );

        let safe = ModuleDiagnostic::new(
            ModuleDiagnosticCode::DiagnosticRedactionFailed,
            None,
            "diagnostic text was rejected",
            "use a typed safe source fact",
        )
        .expect("safe static diagnostic");
        let mut encoded = serde_json::to_value(safe).expect("diagnostic serializes");
        encoded["message"] = serde_json::json!("token=secret");
        assert!(serde_json::from_value::<ModuleDiagnostic>(encoded).is_err());

        let mut source = SafeDiagnosticSource::new(DiagnosticSourceKind::Codec, "source 0")
            .expect("safe source");
        for index in 1..8 {
            source =
                SafeDiagnosticSource::new(DiagnosticSourceKind::Codec, format!("source {index}"))
                    .expect("safe source")
                    .with_source(source)
                    .expect("chain remains within the bound");
        }
        assert!(
            SafeDiagnosticSource::new(DiagnosticSourceKind::Codec, "source 8")
                .expect("safe source")
                .with_source(source)
                .is_err()
        );
    }

    #[test]
    fn generated_locations_keep_the_authored_coordinate_primary() {
        let authored = SourceCoordinate {
            path: ModuleSourcePath::new("src/lib.rs").expect("valid authored path"),
            line: NonZeroU32::new(7).expect("non-zero line"),
            column: NonZeroU32::new(11).expect("non-zero column"),
        };
        let generated = GeneratedCoordinate {
            path: GeneratedAssetPath::new("src/dagger_generated.rs").expect("valid generated path"),
            line: NonZeroU32::new(31).expect("non-zero line"),
            column: NonZeroU32::new(5).expect("non-zero column"),
            authored: authored.clone(),
        };

        let diagnostic = ModuleDiagnostic::new(
            ModuleDiagnosticCode::FunctionSignatureInvalid,
            None,
            "generated bridge does not match the authored function",
            "repair the authored function signature",
        )
        .expect("safe static diagnostic")
        .with_generated_coordinate(generated.clone());

        assert_eq!(diagnostic.source_coordinate(), Some(&authored));
        assert_eq!(diagnostic.generated_coordinate(), Some(&generated));
        let decoded: ModuleDiagnostic = serde_json::from_value(
            serde_json::to_value(&diagnostic).expect("diagnostic serializes"),
        )
        .expect("valid generated mapping deserializes");
        assert_eq!(decoded, diagnostic);
    }
}
