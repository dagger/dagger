//! Projection from canonical schema values into a complete Rust-facing semantic plan.
//!
//! This boundary is intentionally source-free: naming, wrappers, omission, directives,
//! and execution are settled here so renderers cannot reinterpret or skip coordinates.

use std::collections::{BTreeMap, BTreeSet};

use serde::Serialize;

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::directive::{DirectiveProjection, project_directives};
use crate::naming::{NameContext, NameRegistry, RustNameMap};
use crate::schema::canonical::{CanonicalSchema, SchemaCoordinate, SchemaName, TypeDefinition};

pub mod catalog;
pub mod fields;
pub mod types;

use catalog::{
    BindingDescriptor, BindingKey, BindingKind, CatalogDisposition, EvidenceScope,
    ProjectionCatalog,
};
use fields::{ArgumentPresence, FieldProjection, project_fields};
use types::{InterfaceImplementationProjection, ScalarKind, TypeProjection, project_named_types};

/// The semantic values added to a canonical schema by Rust projection.
pub struct SemanticProjection {
    /// Complete generated Rust name map.
    pub names: RustNameMap,
    /// One total projection for every public named type.
    pub named_types: BTreeMap<SchemaName, TypeProjection>,
    /// One total operation projection for every public object/interface field.
    pub fields: BTreeMap<SchemaCoordinate, FieldProjection>,
    /// One typed policy record for every target directive definition.
    pub directives: DirectiveProjection,
    /// Every object/interface implementation edge.
    pub implementations: Vec<InterfaceImplementationProjection>,
    /// Exhaustive semantic binding descriptors and fingerprints.
    pub catalog: ProjectionCatalog,
}

pub(crate) fn project(schema: &CanonicalSchema) -> Result<SemanticProjection, DiagnosticSet> {
    let directives = project_directives(schema)?;
    let names = build_name_map(schema, &directives)?;
    let (named_types, implementations) = project_named_types(schema, &names, &directives)?;
    let fields = project_fields(schema, &names, &directives)?;
    let catalog = build_catalog(schema, &named_types, &fields, &directives, &implementations)?;
    Ok(SemanticProjection {
        names,
        named_types,
        fields,
        directives,
        implementations,
        catalog,
    })
}

fn build_name_map(
    schema: &CanonicalSchema,
    directives: &DirectiveProjection,
) -> Result<RustNameMap, DiagnosticSet> {
    let mut registry = NameRegistry::default();
    for handwritten in [
        "Client",
        "ClientConfig",
        "ClientConfigBuilder",
        "EngineConnection",
        "Diagnostic",
        "QueryBuilder",
        "QueryError",
        "RawRequest",
        "RawResponse",
        "IntoID",
        "Loadable",
        "Id",
        "Json",
        "Platform",
        "IdInput",
    ] {
        registry.reserve_fixed("crate::types", handwritten, "handwritten crate-root export");
    }

    for definition in schema.types().values() {
        match definition {
            TypeDefinition::Scalar(scalar) => {
                if ScalarKind::from_name(&scalar.name).is_none() {
                    registry.reserve(
                        &scalar.coordinate,
                        &scalar.name,
                        scalar.name.as_str(),
                        NameContext::Type,
                        "crate::types",
                    );
                    reserve_common_type_names(&mut registry, &scalar.coordinate, &scalar.name);
                }
            }
            TypeDefinition::Object(object) => {
                if object.name.as_str().starts_with('_') {
                    continue;
                }
                registry.reserve(
                    &object.coordinate,
                    &object.name,
                    object.name.as_str(),
                    NameContext::Handle,
                    "crate::types",
                );
                reserve_common_type_names(&mut registry, &object.coordinate, &object.name);
                reserve_fields(&mut registry, &object.name, object.fields.values());
            }
            TypeDefinition::Interface(interface) => {
                if interface.name.as_str().starts_with('_') {
                    continue;
                }
                registry.reserve(
                    &interface.coordinate,
                    &interface.name,
                    interface.name.as_str(),
                    NameContext::Trait,
                    "crate::types",
                );
                registry.reserve(
                    &interface.coordinate,
                    &interface.name,
                    &format!("{}Client", interface.name.as_str()),
                    NameContext::Handle,
                    "crate::types",
                );
                reserve_common_type_names(&mut registry, &interface.coordinate, &interface.name);
                reserve_fields(&mut registry, &interface.name, interface.fields.values());
            }
            TypeDefinition::Enum(enumeration) => {
                registry.reserve(
                    &enumeration.coordinate,
                    &enumeration.name,
                    enumeration.name.as_str(),
                    NameContext::Type,
                    "crate::types",
                );
                reserve_common_type_names(
                    &mut registry,
                    &enumeration.coordinate,
                    &enumeration.name,
                );
                for value in enumeration.values.values() {
                    if let Some(canonical) = directives.enum_alias(&value.coordinate) {
                        registry.record_alias(
                            &value.coordinate,
                            &value.name,
                            canonical.as_str(),
                            NameContext::Variant,
                        );
                    } else {
                        registry.reserve(
                            &value.coordinate,
                            &value.name,
                            value.name.as_str(),
                            NameContext::Variant,
                            format!("enum::{}", enumeration.name.as_str()),
                        );
                    }
                }
            }
            TypeDefinition::InputObject(input) => {
                registry.reserve(
                    &input.coordinate,
                    &input.name,
                    input.name.as_str(),
                    NameContext::Type,
                    "crate::types",
                );
                reserve_common_type_names(&mut registry, &input.coordinate, &input.name);
                registry.reserve(
                    &input.coordinate,
                    &input.name,
                    "new",
                    NameContext::Constructor,
                    format!("impl::{}", input.name.as_str()),
                );
                for field in input.fields.values() {
                    registry.reserve(
                        &field.coordinate,
                        &field.name,
                        field.name.as_str(),
                        NameContext::Field,
                        format!("input::{}", input.name.as_str()),
                    );
                    if ArgumentPresence::for_input(&field.type_use, field.default.as_ref())
                        .is_omittable()
                    {
                        registry.reserve(
                            &field.coordinate,
                            &field.name,
                            &format!("with_{}", field.name.as_str()),
                            NameContext::Setter,
                            format!("impl::{}", input.name.as_str()),
                        );
                    }
                }
            }
        }
    }
    registry.finish()
}

fn reserve_common_type_names(
    registry: &mut NameRegistry,
    coordinate: &SchemaCoordinate,
    name: &SchemaName,
) {
    registry.reserve(
        coordinate,
        name,
        name.as_str(),
        NameContext::Module,
        "crate::modules",
    );
    registry.reserve(
        coordinate,
        name,
        &format!("{}_projection_test", name.as_str()),
        NameContext::TestHelper,
        "generated::tests",
    );
}

fn reserve_fields<'a>(
    registry: &mut NameRegistry,
    owner: &SchemaName,
    fields: impl Iterator<Item = &'a crate::schema::canonical::FieldDefinition>,
) {
    for field in fields {
        registry.reserve(
            &field.coordinate,
            &field.name,
            field.name.as_str(),
            NameContext::Method,
            format!("impl::{}", owner.as_str()),
        );
        let has_options = field.arguments.values().any(|argument| {
            ArgumentPresence::for_input(&argument.type_use, argument.default.as_ref())
                .is_omittable()
        });
        if has_options {
            registry.reserve(
                &field.coordinate,
                &field.name,
                &format!("{}_opts", field.name.as_str()),
                NameContext::OptionsMethod,
                format!("impl::{}", owner.as_str()),
            );
            registry.reserve(
                &field.coordinate,
                &field.name,
                &format!("{}_{}_opts", owner.as_str(), field.name.as_str()),
                NameContext::Options,
                "crate::types",
            );
        }
        for argument in field.arguments.values() {
            registry.reserve(
                &argument.coordinate,
                &argument.name,
                argument.name.as_str(),
                NameContext::Argument,
                format!("fn::{}", field.coordinate.as_str()),
            );
            if ArgumentPresence::for_input(&argument.type_use, argument.default.as_ref())
                .is_omittable()
            {
                registry.reserve(
                    &argument.coordinate,
                    &argument.name,
                    argument.name.as_str(),
                    NameContext::Field,
                    format!("opts::{}", field.coordinate.as_str()),
                );
            }
        }
    }
}

fn build_catalog(
    schema: &CanonicalSchema,
    named_types: &BTreeMap<SchemaName, TypeProjection>,
    fields: &BTreeMap<SchemaCoordinate, FieldProjection>,
    directives: &DirectiveProjection,
    implementations: &[InterfaceImplementationProjection],
) -> Result<ProjectionCatalog, DiagnosticSet> {
    let mut bindings = Vec::new();
    let mut diagnostics = Vec::new();
    push_descriptor(
        &mut bindings,
        &mut diagnostics,
        BindingKey {
            wire_coordinate: Some(SchemaCoordinate::query_root()),
            rust_symbol: Some(format!("crate::gen::{}", schema.query().as_str())),
            binding_kind: BindingKind::QueryRoot,
        },
        CatalogDisposition::Emitted,
        schema.query().to_string(),
        schema.query(),
        evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
    );

    for projection in named_types.values() {
        match projection {
            TypeProjection::Scalar(scalar) => push_descriptor(
                &mut bindings,
                &mut diagnostics,
                key(
                    &scalar.coordinate,
                    scalar_symbol(scalar),
                    BindingKind::Scalar,
                ),
                CatalogDisposition::RuntimeProvided,
                scalar_signature(scalar),
                scalar,
                evidence(&[
                    EvidenceScope::EngineSchema,
                    EvidenceScope::RustPolicy,
                    EvidenceScope::RustRuntime,
                ]),
            ),
            TypeProjection::Object(object) => push_descriptor(
                &mut bindings,
                &mut diagnostics,
                key(
                    &object.coordinate,
                    Some(format!("crate::gen::{}", object.rust_name)),
                    BindingKind::ObjectHandle,
                ),
                CatalogDisposition::Emitted,
                object.rust_name.clone(),
                object,
                evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
            ),
            TypeProjection::Interface(interface) => {
                push_descriptor(
                    &mut bindings,
                    &mut diagnostics,
                    key(
                        &interface.coordinate,
                        Some(format!("crate::gen::{}", interface.trait_name)),
                        BindingKind::InterfaceTrait,
                    ),
                    CatalogDisposition::Emitted,
                    interface.trait_name.clone(),
                    interface,
                    evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
                );
                push_descriptor(
                    &mut bindings,
                    &mut diagnostics,
                    key(
                        &interface.coordinate,
                        Some(format!("crate::gen::{}", interface.client_name)),
                        BindingKind::InterfaceClient,
                    ),
                    CatalogDisposition::Emitted,
                    interface.client_name.clone(),
                    interface,
                    evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
                );
            }
            TypeProjection::Enum(enumeration) => {
                push_descriptor(
                    &mut bindings,
                    &mut diagnostics,
                    key(
                        &enumeration.coordinate,
                        Some(format!("crate::gen::{}", enumeration.rust_name)),
                        BindingKind::Enum,
                    ),
                    CatalogDisposition::Emitted,
                    enumeration.rust_name.clone(),
                    enumeration,
                    evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
                );
                for variant in enumeration.variants.values() {
                    push_descriptor(
                        &mut bindings,
                        &mut diagnostics,
                        key(
                            &variant.coordinate,
                            Some(format!(
                                "crate::gen::{}::{}",
                                enumeration.rust_name, variant.rust_name
                            )),
                            BindingKind::EnumVariant,
                        ),
                        CatalogDisposition::Emitted,
                        variant.rust_name.clone(),
                        variant,
                        evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
                    );
                }
                for alias in enumeration.aliases.values() {
                    push_descriptor(
                        &mut bindings,
                        &mut diagnostics,
                        key(
                            &alias.coordinate,
                            Some(format!(
                                "crate::gen::{}::{}",
                                enumeration.rust_name, alias.rust_name
                            )),
                            BindingKind::EnumAlias,
                        ),
                        CatalogDisposition::PolicyRecorded,
                        format!("alias {}", alias.canonical_wire_name.as_str()),
                        alias,
                        evidence(&[
                            EvidenceScope::EngineSchema,
                            EvidenceScope::GoSdk,
                            EvidenceScope::RustPolicy,
                        ]),
                    );
                }
            }
            TypeProjection::InputObject(input) => {
                push_descriptor(
                    &mut bindings,
                    &mut diagnostics,
                    key(
                        &input.coordinate,
                        Some(format!("crate::gen::{}", input.rust_name)),
                        BindingKind::InputObject,
                    ),
                    CatalogDisposition::Emitted,
                    input.rust_name.clone(),
                    input,
                    evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
                );
                for field in input.fields.values() {
                    push_descriptor(
                        &mut bindings,
                        &mut diagnostics,
                        key(
                            &field.coordinate,
                            Some(format!(
                                "crate::gen::{}::{}",
                                input.rust_name, field.rust_name
                            )),
                            BindingKind::InputField,
                        ),
                        CatalogDisposition::Emitted,
                        field.rust_type.signature(),
                        field,
                        evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
                    );
                }
            }
            TypeProjection::TargetPrivate(private) => push_descriptor(
                &mut bindings,
                &mut diagnostics,
                key(&private.coordinate, None, BindingKind::TargetPrivateType),
                CatalogDisposition::PolicyRecorded,
                "target-private no-symbol policy".to_owned(),
                private,
                evidence(&[
                    EvidenceScope::EngineSchema,
                    EvidenceScope::GoSdk,
                    EvidenceScope::RustPolicy,
                ]),
            ),
        }
    }

    for field in fields.values() {
        let target_private = matches!(field.strategy, fields::FieldStrategy::TargetPrivate);
        push_descriptor(
            &mut bindings,
            &mut diagnostics,
            key(
                &field.coordinate,
                (!target_private)
                    .then(|| format!("crate::gen::{}::{}", field.owner.as_str(), field.rust_name)),
                if target_private {
                    BindingKind::TargetPrivateField
                } else {
                    BindingKind::FieldOperation
                },
            ),
            if target_private {
                CatalogDisposition::PolicyRecorded
            } else {
                CatalogDisposition::Emitted
            },
            field_signature(field),
            field,
            evidence(&[
                EvidenceScope::EngineSchema,
                EvidenceScope::GoSdk,
                EvidenceScope::RustPolicy,
                EvidenceScope::RustRuntime,
            ]),
        );
        for argument in &field.arguments {
            push_descriptor(
                &mut bindings,
                &mut diagnostics,
                key(
                    &argument.coordinate,
                    Some(format!(
                        "crate::gen::{}::{}::{}",
                        field.owner.as_str(),
                        field.rust_name,
                        argument.rust_name
                    )),
                    BindingKind::Argument,
                ),
                CatalogDisposition::Emitted,
                argument.rust_type.signature(),
                argument,
                evidence(&[
                    EvidenceScope::EngineSchema,
                    EvidenceScope::RustPolicy,
                    EvidenceScope::RustRuntime,
                ]),
            );
        }
    }

    for implementation in implementations {
        push_descriptor(
            &mut bindings,
            &mut diagnostics,
            key(
                &implementation.coordinate,
                Some(format!(
                    "impl crate::gen::{} for crate::gen::{}",
                    implementation.interface.as_str(),
                    implementation.implementor.as_str()
                )),
                BindingKind::InterfaceImplementation,
            ),
            CatalogDisposition::Emitted,
            format!(
                "{}: {}",
                implementation.implementor.as_str(),
                implementation.interface.as_str()
            ),
            implementation,
            evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
        );
    }

    for record in directives.records().values() {
        push_descriptor(
            &mut bindings,
            &mut diagnostics,
            key(&record.coordinate, None, BindingKind::DirectivePolicy),
            CatalogDisposition::PolicyRecorded,
            format!("{:?}", record.policy),
            record,
            evidence(&[
                EvidenceScope::EngineSchema,
                EvidenceScope::RustPolicy,
                EvidenceScope::GoSdk,
            ]),
        );
        let Some(definition) = schema.directives().get(&record.name) else {
            diagnostics.push(projection_diagnostic(
                DiagnosticCode::SchemaDirectiveUnmapped,
                &record.coordinate,
                "directive record has no canonical definition",
            ));
            continue;
        };
        for argument in definition.arguments.values() {
            push_descriptor(
                &mut bindings,
                &mut diagnostics,
                key(&argument.coordinate, None, BindingKind::DirectiveArgument),
                CatalogDisposition::PolicyRecorded,
                format!("{:?}", record.policy),
                argument,
                evidence(&[EvidenceScope::EngineSchema, EvidenceScope::RustPolicy]),
            );
        }
    }

    if let Some(diagnostics) = DiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    ProjectionCatalog::from_bindings(schema.target().clone(), bindings).map_err(|duplicate| {
        DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::CapabilityBindingDuplicate,
            duplicate
                .wire_coordinate
                .as_ref()
                .map(|coordinate| DiagnosticCoordinate::new(coordinate.as_str())),
            "semantic projection produced a duplicate binding key",
        ))
    })
}

fn push_descriptor<T: Serialize>(
    bindings: &mut Vec<BindingDescriptor>,
    diagnostics: &mut Vec<Diagnostic>,
    key: BindingKey,
    disposition: CatalogDisposition,
    signature: String,
    shape: &T,
    required_evidence: BTreeSet<EvidenceScope>,
) {
    let coordinate = key.wire_coordinate.clone();
    let descriptor = serde_json::to_value(shape).and_then(|shape| {
        BindingDescriptor::new(key, disposition, signature, shape, required_evidence)
    });
    match descriptor {
        Ok(descriptor) => bindings.push(descriptor),
        Err(_) => diagnostics.push(Diagnostic::new(
            DiagnosticCode::CapabilityFingerprintMismatch,
            coordinate
                .as_ref()
                .map(|coordinate| DiagnosticCoordinate::new(coordinate.as_str())),
            "binding semantic shape could not be fingerprinted",
        )),
    }
}

fn key(
    coordinate: &SchemaCoordinate,
    rust_symbol: Option<String>,
    binding_kind: BindingKind,
) -> BindingKey {
    BindingKey {
        wire_coordinate: Some(coordinate.clone()),
        rust_symbol,
        binding_kind,
    }
}

fn evidence(scopes: &[EvidenceScope]) -> BTreeSet<EvidenceScope> {
    scopes.iter().copied().collect()
}

fn scalar_symbol(scalar: &types::ScalarProjection) -> Option<String> {
    match scalar.scalar {
        ScalarKind::Boolean | ScalarKind::Float | ScalarKind::Int | ScalarKind::String => None,
        ScalarKind::Id => Some("crate::Id".to_owned()),
        ScalarKind::Json => Some("crate::Json".to_owned()),
        ScalarKind::Platform => Some("crate::Platform".to_owned()),
        ScalarKind::Void => Some("()".to_owned()),
        ScalarKind::Custom => Some(format!("crate::gen::{}", scalar.wire_name)),
    }
}

fn scalar_signature(scalar: &types::ScalarProjection) -> String {
    match scalar.scalar {
        ScalarKind::Boolean => "bool",
        ScalarKind::Float => "f64",
        ScalarKind::Int => "i64",
        ScalarKind::String => "String",
        ScalarKind::Id => "Id",
        ScalarKind::Json => "Json",
        ScalarKind::Platform => "Platform",
        ScalarKind::Void => "()",
        ScalarKind::Custom => scalar.wire_name.as_str(),
    }
    .to_owned()
}

fn field_signature(field: &FieldProjection) -> String {
    let arguments = field
        .arguments
        .iter()
        .map(|argument| {
            let carrier = if argument.presence.is_omittable() {
                "Option<"
            } else {
                ""
            };
            let suffix = if argument.presence.is_omittable() {
                ">"
            } else {
                ""
            };
            format!(
                "{}: {carrier}{}{suffix}",
                argument.rust_name,
                argument.rust_type.signature()
            )
        })
        .collect::<Vec<_>>()
        .join(", ");
    format!(
        "fn {}({arguments}) -> {}",
        field.rust_name,
        field.return_type.signature()
    )
}

fn projection_diagnostic(
    code: DiagnosticCode,
    coordinate: &SchemaCoordinate,
    message: &str,
) -> Diagnostic {
    Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate.as_str())),
        message,
    )
}
