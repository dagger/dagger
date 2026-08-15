//! One checked visible-schema plan shared by every operation renderer.

use std::collections::BTreeSet;
use std::sync::OnceLock;

use crate::ProjectionPlan;
use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::projection::catalog::SemanticDigest;
use crate::schema::canonical::{CanonicalSchema, SchemaCoordinate};
use crate::schema::{
    CoreCoordinateManifest, SchemaCompatibilityMode, decode_and_validate,
    decode_and_validate_with_mode,
};
use crate::target::CodegenTarget;

use super::OperationKind;

const CHECKED_CORE_SCHEMA: &[u8] = include_bytes!("../../../../codegen/schema.json");

static CHECKED_CORE_MANIFEST: OnceLock<Result<CoreCoordinateManifest, DiagnosticSet>> =
    OnceLock::new();
static CHECKED_MODULE_MANIFEST: OnceLock<Result<CoreCoordinateManifest, DiagnosticSet>> =
    OnceLock::new();

// These are the exact module-introspection exclusions selected by core/moddeps.go and
// core/env.go at the checked target. ID types are scrubbed beside each raw type by the
// engine's schema builder.
const MODULE_HIDDEN_TYPES: &[&str] = &[
    "Host",
    "HostID",
    "Engine",
    "EngineID",
    "EngineCache",
    "EngineCacheID",
    "EngineCacheEntry",
    "EngineCacheEntryID",
    "EngineCacheEntrySet",
    "EngineCacheEntrySetID",
];
const MODULE_HIDDEN_FIELDS: &[&str] = &[
    "Query.currentWorkspace",
    "Query.engineVolume",
    "Query.sshfsVolume",
    "Address.volume",
];

/// Complete compatible visible schema and its single semantic projection.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct VisibleSchemaPlan {
    canonical: CanonicalSchema,
    core_coordinates: BTreeSet<SchemaCoordinate>,
    extension_coordinates: BTreeSet<SchemaCoordinate>,
    digest: SemanticDigest,
    projection: ProjectionPlan,
}

impl VisibleSchemaPlan {
    /// Returns the complete canonical visible schema.
    #[must_use]
    pub const fn canonical(&self) -> &CanonicalSchema {
        &self.canonical
    }

    /// Returns every coordinate owned by the checked core manifest.
    #[must_use]
    pub const fn core_coordinates(&self) -> &BTreeSet<SchemaCoordinate> {
        &self.core_coordinates
    }

    /// Returns every operation-scoped coordinate added around the core manifest.
    #[must_use]
    pub const fn extension_coordinates(&self) -> &BTreeSet<SchemaCoordinate> {
        &self.extension_coordinates
    }

    /// Returns the order-independent semantic identity of the complete visible schema.
    #[must_use]
    pub const fn digest(&self) -> &SemanticDigest {
        &self.digest
    }

    /// Returns the single semantic projection consumed by all operation renderers.
    #[must_use]
    pub const fn projection(&self) -> &ProjectionPlan {
        &self.projection
    }

    /// Returns whether a named type is operation-scoped rather than checked core.
    #[must_use]
    pub fn is_extension_type(&self, name: &crate::schema::canonical::SchemaName) -> bool {
        self.extension_coordinates
            .contains(&SchemaCoordinate::named_type(name))
    }
}

/// Validates the complete visible schema and projects it exactly once.
pub fn project_visible_schema(
    target: &CodegenTarget,
    input: &[u8],
) -> Result<VisibleSchemaPlan, DiagnosticSet> {
    project_visible_schema_with_manifest(target, input, checked_core_manifest(target)?)
}

pub(super) fn project_operation_visible_schema(
    target: &CodegenTarget,
    operation: OperationKind,
    input: &[u8],
) -> Result<VisibleSchemaPlan, DiagnosticSet> {
    let manifest = if matches!(
        operation,
        OperationKind::GenerateModule | OperationKind::GenerateEntrypoint
    ) {
        checked_module_manifest(target)?
    } else {
        checked_core_manifest(target)?
    };
    project_visible_schema_with_manifest(target, input, manifest)
}

fn project_visible_schema_with_manifest(
    target: &CodegenTarget,
    input: &[u8],
    manifest: CoreCoordinateManifest,
) -> Result<VisibleSchemaPlan, DiagnosticSet> {
    let canonical = decode_and_validate_with_mode(
        target,
        input,
        SchemaCompatibilityMode::ExactCoreWithExtensions(&manifest),
    )?;
    let all_coordinates = CoreCoordinateManifest::from_checked_schema(&canonical)?;
    let core_coordinates = manifest.coordinates().keys().cloned().collect();
    let extension_coordinates = all_coordinates
        .coordinates()
        .keys()
        .filter(|coordinate| !manifest.contains(coordinate))
        .cloned()
        .collect();
    let digest = SemanticDigest::for_value(&(
        canonical.query(),
        canonical.types(),
        canonical.directives(),
        canonical.inventory(),
    ))
    .map_err(|_| {
        DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::GeneratedProvenanceInvalid,
            Some(DiagnosticCoordinate::new("visible-schema")),
            "visible schema could not be fingerprinted",
        ))
    })?;
    let projected = crate::projection::project(&canonical)?;
    let projection = ProjectionPlan {
        target: target.clone(),
        schema: canonical.clone(),
        names: projected.names,
        named_types: projected.named_types,
        fields: projected.fields,
        directives: projected.directives,
        implementations: projected.implementations,
        catalog: projected.catalog,
    };
    Ok(VisibleSchemaPlan {
        canonical,
        core_coordinates,
        extension_coordinates,
        digest,
        projection,
    })
}

fn checked_core_manifest(target: &CodegenTarget) -> Result<CoreCoordinateManifest, DiagnosticSet> {
    CHECKED_CORE_MANIFEST
        .get_or_init(|| {
            let schema = decode_and_validate(target, CHECKED_CORE_SCHEMA)?;
            CoreCoordinateManifest::from_checked_schema(&schema)
        })
        .clone()
}

fn checked_module_manifest(
    target: &CodegenTarget,
) -> Result<CoreCoordinateManifest, DiagnosticSet> {
    CHECKED_MODULE_MANIFEST
        .get_or_init(|| {
            let schema = decode_and_validate(target, CHECKED_CORE_SCHEMA)?;
            CoreCoordinateManifest::from_checked_schema_with_scrub(
                &schema,
                MODULE_HIDDEN_TYPES,
                MODULE_HIDDEN_FIELDS,
            )
        })
        .clone()
}
