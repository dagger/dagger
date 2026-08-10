//! Manifest-backed compatibility for engine-visible schemas.
//!
//! The checked core schema is reduced to coordinate-level semantic fingerprints.
//! Visible schemas may add operation-scoped coordinates, but every reviewed core
//! coordinate must remain present and byte-independent semantic equality must hold.
//! No name prefix is trusted as an ownership boundary.

use std::collections::{BTreeMap, BTreeSet};

use serde::Serialize;

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::projection::catalog::SemanticDigest;

use super::canonical::{
    CanonicalSchema, DirectiveDefinition, EnumDefinition, InputObjectDefinition,
    InterfaceDefinition, ObjectDefinition, SchemaCoordinate, TypeDefinition,
};

/// Validation policy selected before canonical schema projection.
#[derive(Clone, Copy, Debug)]
pub enum SchemaCompatibilityMode<'a> {
    /// Require the checked snapshot's exact bytes and coordinate inventory.
    ExactTarget,
    /// Require exact checked core semantics while admitting additional coordinates.
    ExactCoreWithExtensions(&'a CoreCoordinateManifest),
}

/// Semantic fingerprints for every coordinate in the reviewed core schema.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CoreCoordinateManifest {
    coordinates: BTreeMap<SchemaCoordinate, SemanticDigest>,
}

impl CoreCoordinateManifest {
    /// Derives the manifest from a schema that already passed exact-target validation.
    pub fn from_checked_schema(schema: &CanonicalSchema) -> Result<Self, DiagnosticSet> {
        let coordinates = coordinate_fingerprints(schema)?;
        Ok(Self { coordinates })
    }

    /// Returns the complete reviewed coordinate set.
    #[must_use]
    pub fn coordinates(&self) -> &BTreeMap<SchemaCoordinate, SemanticDigest> {
        &self.coordinates
    }

    /// Returns whether a coordinate belongs to the checked core schema.
    #[must_use]
    pub fn contains(&self, coordinate: &SchemaCoordinate) -> bool {
        self.coordinates.contains_key(coordinate)
    }

    pub(super) fn verify(&self, schema: &CanonicalSchema) -> Result<(), DiagnosticSet> {
        let actual = coordinate_fingerprints(schema)?;
        let mut diagnostics = Vec::new();
        for (coordinate, expected) in &self.coordinates {
            match actual.get(coordinate) {
                None => diagnostics.push(Diagnostic::new(
                    DiagnosticCode::SchemaCoreCoordinateMissing,
                    Some(DiagnosticCoordinate::new(coordinate.as_str())),
                    "visible schema omits a reviewed core coordinate",
                )),
                Some(candidate) if candidate != expected => diagnostics.push(Diagnostic::new(
                    DiagnosticCode::SchemaCoreCoordinateIncompatible,
                    Some(DiagnosticCoordinate::new(coordinate.as_str())),
                    "visible schema redefines a reviewed core coordinate",
                )),
                Some(_) => {}
            }
        }
        match DiagnosticSet::new(diagnostics) {
            Some(diagnostics) => Err(diagnostics),
            None => Ok(()),
        }
    }
}

#[derive(Serialize)]
#[serde(tag = "kind", rename_all = "kebab-case")]
enum TypeSemantics<'a> {
    Scalar(&'a super::canonical::ScalarDefinition),
    Object {
        name: &'a super::canonical::SchemaName,
        description: &'a Option<String>,
        interfaces: &'a BTreeSet<super::canonical::SchemaName>,
        directives: &'a [super::canonical::DirectiveApplication],
    },
    Interface {
        name: &'a super::canonical::SchemaName,
        description: &'a Option<String>,
        interfaces: &'a BTreeSet<super::canonical::SchemaName>,
        possible_types: &'a BTreeSet<super::canonical::SchemaName>,
        directives: &'a [super::canonical::DirectiveApplication],
    },
    Enum(&'a EnumDefinition),
    InputObject(&'a InputObjectDefinition),
}

fn coordinate_fingerprints(
    schema: &CanonicalSchema,
) -> Result<BTreeMap<SchemaCoordinate, SemanticDigest>, DiagnosticSet> {
    let mut coordinates = BTreeMap::new();
    insert_fingerprint(
        &mut coordinates,
        SchemaCoordinate::query_root(),
        schema.query(),
    )?;
    for definition in schema.types().values() {
        match definition {
            TypeDefinition::Scalar(definition) => {
                insert_type(&mut coordinates, TypeSemantics::Scalar(definition))?;
            }
            TypeDefinition::Object(definition) => {
                insert_object(&mut coordinates, definition)?;
            }
            TypeDefinition::Interface(definition) => {
                insert_interface(&mut coordinates, definition)?;
            }
            TypeDefinition::Enum(definition) => {
                insert_type(&mut coordinates, TypeSemantics::Enum(definition))?;
                for value in definition.values.values() {
                    insert_fingerprint(&mut coordinates, value.coordinate.clone(), value)?;
                }
            }
            TypeDefinition::InputObject(definition) => {
                insert_type(&mut coordinates, TypeSemantics::InputObject(definition))?;
                for field in definition.fields.values() {
                    insert_fingerprint(&mut coordinates, field.coordinate.clone(), field)?;
                }
            }
        }
    }
    for directive in schema.directives().values() {
        insert_directive(&mut coordinates, directive)?;
    }
    Ok(coordinates)
}

fn insert_object(
    coordinates: &mut BTreeMap<SchemaCoordinate, SemanticDigest>,
    definition: &ObjectDefinition,
) -> Result<(), DiagnosticSet> {
    insert_type(
        coordinates,
        TypeSemantics::Object {
            name: &definition.name,
            description: &definition.description,
            interfaces: &definition.interfaces,
            directives: &definition.directives,
        },
    )?;
    insert_fields(coordinates, &definition.fields)
}

fn insert_interface(
    coordinates: &mut BTreeMap<SchemaCoordinate, SemanticDigest>,
    definition: &InterfaceDefinition,
) -> Result<(), DiagnosticSet> {
    insert_type(
        coordinates,
        TypeSemantics::Interface {
            name: &definition.name,
            description: &definition.description,
            interfaces: &definition.interfaces,
            possible_types: &definition.possible_types,
            directives: &definition.directives,
        },
    )?;
    insert_fields(coordinates, &definition.fields)
}

fn insert_type(
    coordinates: &mut BTreeMap<SchemaCoordinate, SemanticDigest>,
    semantics: TypeSemantics<'_>,
) -> Result<(), DiagnosticSet> {
    let name = match &semantics {
        TypeSemantics::Scalar(definition) => &definition.name,
        TypeSemantics::Object { name, .. } | TypeSemantics::Interface { name, .. } => name,
        TypeSemantics::Enum(definition) => &definition.name,
        TypeSemantics::InputObject(definition) => &definition.name,
    };
    insert_fingerprint(coordinates, SchemaCoordinate::named_type(name), &semantics)
}

fn insert_fields(
    coordinates: &mut BTreeMap<SchemaCoordinate, SemanticDigest>,
    fields: &BTreeMap<super::canonical::SchemaName, super::canonical::FieldDefinition>,
) -> Result<(), DiagnosticSet> {
    for field in fields.values() {
        insert_fingerprint(coordinates, field.coordinate.clone(), field)?;
        for argument in field.arguments.values() {
            insert_fingerprint(coordinates, argument.coordinate.clone(), argument)?;
        }
    }
    Ok(())
}

fn insert_directive(
    coordinates: &mut BTreeMap<SchemaCoordinate, SemanticDigest>,
    definition: &DirectiveDefinition,
) -> Result<(), DiagnosticSet> {
    insert_fingerprint(coordinates, definition.coordinate.clone(), definition)?;
    for argument in definition.arguments.values() {
        insert_fingerprint(coordinates, argument.coordinate.clone(), argument)?;
    }
    Ok(())
}

fn insert_fingerprint<T: Serialize>(
    coordinates: &mut BTreeMap<SchemaCoordinate, SemanticDigest>,
    coordinate: SchemaCoordinate,
    semantics: &T,
) -> Result<(), DiagnosticSet> {
    let fingerprint = SemanticDigest::for_value(semantics).map_err(|_| {
        DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::GeneratedProvenanceInvalid,
            Some(DiagnosticCoordinate::new(coordinate.as_str())),
            "schema coordinate could not be fingerprinted",
        ))
    })?;
    // Canonical validation already makes coordinates unique. Treat a duplicate here
    // as provenance failure so a future coordinate family cannot silently alias one.
    if coordinates
        .insert(coordinate.clone(), fingerprint)
        .is_some()
    {
        return Err(DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::GeneratedProvenanceInvalid,
            Some(DiagnosticCoordinate::new(coordinate.as_str())),
            "schema coordinate fingerprint aliases an existing coordinate",
        )));
    }
    Ok(())
}
