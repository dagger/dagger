//! Collision-free public naming for one generated module namespace.
//!
//! Every type-level name is planned as one set. Prefix removal is accepted only when
//! its result is legal and globally unique, so schema declaration order can never
//! decide which public spelling wins.

use std::collections::{BTreeMap, BTreeSet};

use crate::diagnostic::{
    Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet, RelatedCoordinate,
};
use crate::naming::{NameContext, rust_name};
use crate::projection::fields::FieldProjection;
use crate::projection::types::TypeProjection;
use crate::schema::canonical::{SchemaCoordinate, SchemaName};

use super::model::{ClientNameKey, ClientNamePlan, ClientNameRole, ModuleRoot, RustIdentifier};

const RESERVED_MODULE_NAMES: &[&str] =
    &["core", "dagger_client", "generated", "prelude", "support"];
const RESERVED_TYPE_NAMES: &[&str] = &["Client", "Core", "Generated", "Prelude", "Support"];

#[derive(Clone)]
struct Candidate {
    key: ClientNameKey,
    original: String,
    shortened: Option<String>,
}

pub(crate) fn plan_client_names(
    root: &ModuleRoot,
    types: &BTreeMap<SchemaName, TypeProjection>,
    fields: &BTreeMap<SchemaCoordinate, FieldProjection>,
) -> Result<ClientNamePlan, DiagnosticSet> {
    let namespace_spelling = rust_name(&root.field_wire_name, NameContext::Module).identifier;
    let namespace = RustIdentifier::new(namespace_spelling).map_err(|_| {
        naming_error(
            DiagnosticCode::RustNameInvalid,
            &root.field_coordinate,
            "selected module Wire_Name cannot form a Rust module namespace",
        )
    })?;
    if RESERVED_MODULE_NAMES.contains(&namespace.as_str().trim_start_matches("r#")) {
        return Err(naming_error(
            DiagnosticCode::RustNameCollision,
            &root.field_coordinate,
            "selected module namespace collides with a generated client role",
        ));
    }

    let extension_prefix = rust_name(&root.field_wire_name, NameContext::Type).identifier;
    let extension_trait = RustIdentifier::new(format!("{extension_prefix}Ext")).map_err(|_| {
        naming_error(
            DiagnosticCode::RustNameInvalid,
            &root.field_coordinate,
            "selected module Wire_Name cannot form an extension trait",
        )
    })?;
    let root_type = RustIdentifier::new("Client").map_err(|_| {
        naming_error(
            DiagnosticCode::RustNameInvalid,
            &root.object_coordinate,
            "generated root role is not a valid Rust identifier",
        )
    })?;

    let module_prefix = types
        .get(&root.object_wire_name)
        .and_then(|projection| match projection {
            TypeProjection::Object(object) => Some(object.rust_name.as_str()),
            _ => None,
        })
        .ok_or_else(|| {
            naming_error(
                DiagnosticCode::ClientModuleRootInvalid,
                &root.object_coordinate,
                "selected module root lost its object projection",
            )
        })?;

    let mut candidates = Vec::new();
    for projection in types.values() {
        match projection {
            TypeProjection::Object(object) => {
                let original = if object.coordinate == root.object_coordinate {
                    root_type.as_str().to_owned()
                } else {
                    object.rust_name.clone()
                };
                candidates.push(candidate(
                    object.coordinate.clone(),
                    ClientNameRole::Object,
                    original,
                    module_prefix,
                ));
            }
            TypeProjection::Interface(interface) => {
                candidates.push(candidate(
                    interface.coordinate.clone(),
                    ClientNameRole::InterfaceTrait,
                    interface.trait_name.clone(),
                    module_prefix,
                ));
                candidates.push(candidate(
                    interface.coordinate.clone(),
                    ClientNameRole::InterfaceClient,
                    interface.client_name.clone(),
                    module_prefix,
                ));
            }
            TypeProjection::Enum(enumeration) => candidates.push(candidate(
                enumeration.coordinate.clone(),
                ClientNameRole::Enum,
                enumeration.rust_name.clone(),
                module_prefix,
            )),
            TypeProjection::InputObject(input) => candidates.push(candidate(
                input.coordinate.clone(),
                ClientNameRole::InputObject,
                input.rust_name.clone(),
                module_prefix,
            )),
            TypeProjection::Scalar(scalar)
                if scalar.scalar == crate::projection::types::ScalarKind::Custom =>
            {
                candidates.push(candidate(
                    scalar.coordinate.clone(),
                    ClientNameRole::CustomScalar,
                    rust_name(&scalar.wire_name, NameContext::Type).identifier,
                    module_prefix,
                ));
            }
            TypeProjection::Scalar(_) => {}
            TypeProjection::TargetPrivate(private) => {
                return Err(naming_error(
                    DiagnosticCode::ClientSchemaScopeInvalid,
                    &private.coordinate,
                    "selected module closure contains a target-private type",
                ));
            }
        }
    }
    for field in fields.values() {
        if let Some(options) = &field.options_type_name {
            candidates.push(candidate(
                field.coordinate.clone(),
                ClientNameRole::Options,
                options.clone(),
                module_prefix,
            ));
        }
    }

    let shortened_counts = candidates
        .iter()
        .filter_map(|candidate| candidate.shortened.as_deref())
        .fold(BTreeMap::<String, usize>::new(), |mut counts, name| {
            *counts.entry(name.to_owned()).or_default() += 1;
            counts
        });
    let mut selected = BTreeMap::new();
    for candidate in candidates {
        let shortened = candidate.shortened.as_deref().filter(|name| {
            shortened_counts.get(*name) == Some(&1)
                && !RESERVED_TYPE_NAMES.contains(name)
                && RustIdentifier::new((*name).to_owned()).is_ok()
        });
        let spelling = shortened.unwrap_or(&candidate.original);
        let identifier = RustIdentifier::new(spelling.to_owned()).map_err(|_| {
            naming_error(
                DiagnosticCode::RustNameInvalid,
                &candidate.key.coordinate,
                "projected module symbol is not a valid Rust identifier",
            )
        })?;
        selected.insert(candidate.key, identifier);
    }

    validate_unique(&selected, &root.object_coordinate)?;
    Ok(ClientNamePlan {
        module_wire_name: root.field_wire_name.clone(),
        namespace,
        extension_trait,
        root_type,
        bindings: selected,
    })
}

fn candidate(
    coordinate: SchemaCoordinate,
    role: ClientNameRole,
    original: String,
    module_prefix: &str,
) -> Candidate {
    let shortened = original
        .strip_prefix(module_prefix)
        .filter(|suffix| !suffix.is_empty())
        .map(str::to_owned);
    Candidate {
        key: ClientNameKey { coordinate, role },
        original,
        shortened,
    }
}

fn validate_unique(
    names: &BTreeMap<ClientNameKey, RustIdentifier>,
    root: &SchemaCoordinate,
) -> Result<(), DiagnosticSet> {
    let mut owners = BTreeMap::<&str, &ClientNameKey>::new();
    let mut diagnostics = Vec::new();
    let mut reserved = RESERVED_TYPE_NAMES.iter().copied().collect::<BTreeSet<_>>();
    reserved.remove("Client");
    for (key, name) in names {
        let spelling = name.as_str();
        if reserved.contains(spelling) {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::RustNameCollision,
                Some(DiagnosticCoordinate::new(key.coordinate.as_str())),
                "projected module symbol collides with a generated client role",
            ));
            continue;
        }
        if spelling == "Client" && &key.coordinate != root {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::RustNameCollision,
                Some(DiagnosticCoordinate::new(key.coordinate.as_str())),
                "projected module symbol collides with the generated root Client",
            ));
            continue;
        }
        if let Some(previous) = owners.insert(spelling, key) {
            diagnostics.push(
                Diagnostic::new(
                    DiagnosticCode::RustNameCollision,
                    Some(DiagnosticCoordinate::new(key.coordinate.as_str())),
                    "multiple module coordinates project to one public Rust identifier",
                )
                .with_related(RelatedCoordinate {
                    coordinate: DiagnosticCoordinate::new(previous.coordinate.as_str()),
                    relationship: "first coordinate projecting to this identifier".to_owned(),
                }),
            );
        }
    }
    DiagnosticSet::new(diagnostics).map_or(Ok(()), Err)
}

fn naming_error(
    code: DiagnosticCode,
    coordinate: &SchemaCoordinate,
    message: &str,
) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate.as_str())),
        message,
    ))
}
