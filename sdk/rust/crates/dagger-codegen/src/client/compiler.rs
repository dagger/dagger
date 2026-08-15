//! Exact Core-plus-one-module standalone-client compiler.
//!
//! The visible schema has already passed target and Core compatibility checks. This
//! phase proves the stricter client scope, resolves every Core binding to the checked
//! public SDK catalog, and re-keys only the reachable selected-module bindings.

use std::collections::{BTreeMap, BTreeSet, VecDeque};
use std::sync::OnceLock;

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::engine::{ModuleProjectionInput, VisibleSchemaPlan, project_visible_schema};
use crate::projection::catalog::{BindingDescriptor, BindingKey, SemanticDigest};
use crate::projection::types::{InterfaceImplementationProjection, TypeProjection};
use crate::schema::canonical::{
    FieldDefinition, SchemaCoordinate, SchemaName, TypeDefinition, TypeShape, TypeUse,
};
use crate::target::CodegenTarget;

use super::model::{
    ClientBindingCatalog, ClientBindingDescriptor, ClientBindingPlan, ClientBindingSource,
    ClientNameRole, ClientNamespaceRecord, ClientProjectIdentity, ClientSchemaSurface,
    CoreBindingReference, ModuleRoot, ModuleSurfacePlan,
};
use super::naming::plan_client_names;

const CHECKED_CORE_SCHEMA: &[u8] = include_bytes!("../../../../codegen/schema.json");
static CHECKED_CORE_PLAN: OnceLock<Result<VisibleSchemaPlan, DiagnosticSet>> = OnceLock::new();

/// Borrowed inputs to one total standalone-client compilation.
pub struct ClientCompilationInput<'a> {
    /// Exact checked target.
    pub target: &'a CodegenTarget,
    /// Complete Core-compatible schema projected exactly once.
    pub visible_schema: &'a VisibleSchemaPlan,
    /// Exact engine-selected module identity.
    pub module: &'a ModuleProjectionInput,
    /// Deterministic Cargo and crate identity selected before source rendering.
    pub project: &'a ClientProjectIdentity,
}

/// Compiles one exact Core-only or Core-plus-selected-module client plan.
pub fn compile_client(
    input: ClientCompilationInput<'_>,
) -> Result<ClientBindingPlan, DiagnosticSet> {
    if input.visible_schema.projection().target() != input.target {
        return Err(client_error(
            DiagnosticCode::TargetIdentityInvalid,
            "client.target",
            "visible schema plan belongs to a different checked target",
        ));
    }
    let checked = checked_core_plan(input.target)?;
    let core_bindings = resolve_core_bindings(input.visible_schema, &checked)?;
    let compiled_surface = compile_surface(input.visible_schema, input.module)?;
    let (surface, names, module_types, module_fields, module_implementations) =
        match compiled_surface {
            CompiledSurface::CoreOnly => (
                ClientSchemaSurface::CoreOnly,
                None,
                BTreeMap::new(),
                BTreeMap::new(),
                Vec::new(),
            ),
            CompiledSurface::Bound { root, closure } => {
                let module_types = input
                    .visible_schema
                    .projection()
                    .named_types()
                    .iter()
                    .filter(|(name, _)| closure.contains(&SchemaCoordinate::named_type(name)))
                    .map(|(name, projection)| (name.clone(), projection.clone()))
                    .collect::<BTreeMap<_, _>>();
                let module_fields = input
                    .visible_schema
                    .projection()
                    .fields()
                    .iter()
                    .filter(|(coordinate, _)| closure.contains(coordinate))
                    .map(|(coordinate, projection)| (coordinate.clone(), projection.clone()))
                    .collect::<BTreeMap<_, _>>();
                let module_names = module_types.keys().cloned().collect::<BTreeSet<_>>();
                let module_implementations = input
                    .visible_schema
                    .projection()
                    .implementations()
                    .iter()
                    .filter(|edge| {
                        module_names.contains(&edge.implementor)
                            && module_names.contains(&edge.interface)
                    })
                    .cloned()
                    .collect::<Vec<_>>();
                let names = plan_client_names(&root, &module_types, &module_fields)?;
                let namespace = ClientNamespaceRecord {
                    namespace: names.namespace.clone(),
                    extension_trait: names.extension_trait.clone(),
                    root_type: names.root_type.clone(),
                };
                (
                    ClientSchemaSurface::BoundModule(ModuleSurfacePlan {
                        root,
                        closure,
                        namespace,
                    }),
                    Some(names),
                    module_types,
                    module_fields,
                    module_implementations,
                )
            }
        };
    let generated_bindings = resolve_generated_bindings(GeneratedBindingInput {
        visible: input.visible_schema,
        checked: &checked,
        core: &core_bindings,
        names: names.as_ref(),
        surface: &surface,
        types: &module_types,
        fields: &module_fields,
        implementations: &module_implementations,
    })?;
    let catalog = build_catalog(
        input.target,
        input.visible_schema.digest(),
        &core_bindings,
        &generated_bindings,
    )?;
    Ok(ClientBindingPlan {
        target: input.target.clone(),
        visible_schema_digest: input.visible_schema.digest().clone(),
        schema: input.visible_schema.canonical().clone(),
        directives: input.visible_schema.projection().directives().clone(),
        project: input.project.clone(),
        surface,
        core_bindings,
        names,
        module_types,
        module_fields,
        module_implementations,
        generated_bindings,
        catalog,
    })
}

pub(crate) fn validate_core_references(plan: &ClientBindingPlan) -> Result<(), DiagnosticSet> {
    let checked = checked_core_plan(&plan.target)?;
    let expected = checked.projection().catalog().bindings();
    let mut diagnostics = Vec::new();
    for (key, binding) in expected {
        match plan.core_bindings.get(key) {
            None => diagnostics.push(Diagnostic::new(
                DiagnosticCode::CapabilityBindingMissing,
                coordinate(key),
                "standalone client plan omits a checked Core binding",
            )),
            Some(candidate)
                if candidate.implementation_fingerprint != binding.implementation_fingerprint
                    || candidate.public_path != public_core_path(key.rust_symbol.as_deref()) =>
            {
                diagnostics.push(Diagnostic::new(
                    DiagnosticCode::CapabilityFingerprintMismatch,
                    coordinate(key),
                    "standalone client Core reference differs from the checked SDK catalog",
                ));
            }
            Some(_) => {}
        }
    }
    for key in plan.core_bindings.keys() {
        if !expected.contains_key(key) {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::CapabilityBindingDuplicate,
                coordinate(key),
                "standalone client plan contains a non-Core binding in its Core catalog",
            ));
        }
    }
    DiagnosticSet::new(diagnostics).map_or(Ok(()), Err)
}

enum CompiledSurface {
    CoreOnly,
    Bound {
        root: ModuleRoot,
        closure: BTreeSet<SchemaCoordinate>,
    },
}

fn compile_surface(
    plan: &VisibleSchemaPlan,
    module: &ModuleProjectionInput,
) -> Result<CompiledSurface, DiagnosticSet> {
    if plan.extension_coordinates().is_empty() {
        return Ok(CompiledSurface::CoreOnly);
    }
    let module_name = SchemaName::try_from(module.name.as_str()).map_err(|()| {
        client_error(
            DiagnosticCode::ClientModuleRootInvalid,
            "client.module.name",
            "engine-normalized module name is not a GraphQL Wire_Name",
        )
    })?;
    let query_name = plan.canonical().query();
    let query = match plan.canonical().types().get(query_name) {
        Some(TypeDefinition::Object(query)) => query,
        _ => {
            return Err(client_error(
                DiagnosticCode::ClientModuleRootInvalid,
                "schema.query",
                "canonical query root is not an object",
            ));
        }
    };
    let extension_fields = query
        .fields
        .values()
        .filter(|field| plan.extension_coordinates().contains(&field.coordinate))
        .collect::<Vec<_>>();
    if extension_fields.len() != 1 {
        let coordinate = extension_fields
            .first()
            .map_or("schema.query", |field| field.coordinate.as_str());
        return Err(client_error(
            DiagnosticCode::ClientModuleRootInvalid,
            coordinate,
            "client schema must expose exactly one selected-module field on Query",
        ));
    }
    let field = extension_fields[0];
    if field.name != module_name {
        return Err(client_error(
            DiagnosticCode::ClientModuleRootInvalid,
            field.coordinate.as_str(),
            "Query extension does not match the engine-normalized selected module",
        ));
    }
    if !field.arguments.is_empty() || field.type_use.nullable {
        return Err(client_error(
            DiagnosticCode::ClientModuleRootInvalid,
            field.coordinate.as_str(),
            "selected module root must be a non-null argument-free object field",
        ));
    }
    let object_name = match &field.type_use.shape {
        TypeShape::Named(name)
            if matches!(
                plan.canonical().types().get(name),
                Some(TypeDefinition::Object(_))
            ) =>
        {
            name.clone()
        }
        _ => {
            return Err(client_error(
                DiagnosticCode::ClientModuleRootInvalid,
                field.coordinate.as_str(),
                "selected module root must return one object without a list wrapper",
            ));
        }
    };
    let root = ModuleRoot {
        field_coordinate: field.coordinate.clone(),
        field_wire_name: field.name.clone(),
        object_wire_name: object_name.clone(),
        object_coordinate: SchemaCoordinate::named_type(&object_name),
    };
    let closure = reachable_module_closure(plan, &root)?;
    if closure != *plan.extension_coordinates() {
        let coordinate = plan
            .extension_coordinates()
            .difference(&closure)
            .next()
            .or_else(|| closure.difference(plan.extension_coordinates()).next())
            .unwrap_or(&root.field_coordinate);
        return Err(client_error(
            DiagnosticCode::ClientSchemaScopeInvalid,
            coordinate.as_str(),
            "client schema contains an unreachable or unsupported extension coordinate",
        ));
    }
    Ok(CompiledSurface::Bound { root, closure })
}

fn reachable_module_closure(
    plan: &VisibleSchemaPlan,
    root: &ModuleRoot,
) -> Result<BTreeSet<SchemaCoordinate>, DiagnosticSet> {
    let mut closure = BTreeSet::from([root.field_coordinate.clone()]);
    let mut pending = VecDeque::from([root.object_wire_name.clone()]);
    let mut visited = BTreeSet::new();
    while let Some(name) = pending.pop_front() {
        if !visited.insert(name.clone()) {
            continue;
        }
        let coordinate = SchemaCoordinate::named_type(&name);
        if plan.core_coordinates().contains(&coordinate) {
            continue;
        }
        if !owns_module_type(&root.object_wire_name, &name) {
            return Err(client_error(
                DiagnosticCode::ClientSchemaScopeInvalid,
                coordinate.as_str(),
                "reachable non-Core type is outside the selected module namespace",
            ));
        }
        let definition = plan.canonical().types().get(&name).ok_or_else(|| {
            client_error(
                DiagnosticCode::ClientSchemaScopeInvalid,
                coordinate.as_str(),
                "reachable selected-module type has no canonical definition",
            )
        })?;
        closure.insert(coordinate);
        match definition {
            TypeDefinition::Scalar(_) => {}
            TypeDefinition::Object(object) => {
                enqueue_names(&object.interfaces, &mut pending);
                add_fields(plan, &object.fields, &mut closure, &mut pending)?;
            }
            TypeDefinition::Interface(interface) => {
                enqueue_names(&interface.interfaces, &mut pending);
                enqueue_names(&interface.possible_types, &mut pending);
                add_fields(plan, &interface.fields, &mut closure, &mut pending)?;
            }
            TypeDefinition::Enum(enumeration) => {
                closure.extend(
                    enumeration
                        .values
                        .values()
                        .map(|value| value.coordinate.clone()),
                );
            }
            TypeDefinition::InputObject(input) => {
                for field in input.fields.values() {
                    closure.insert(field.coordinate.clone());
                    enqueue_type(plan, &field.type_use, &mut pending)?;
                }
            }
        }
    }
    Ok(closure)
}

fn add_fields(
    plan: &VisibleSchemaPlan,
    fields: &BTreeMap<SchemaName, FieldDefinition>,
    closure: &mut BTreeSet<SchemaCoordinate>,
    pending: &mut VecDeque<SchemaName>,
) -> Result<(), DiagnosticSet> {
    for field in fields.values() {
        closure.insert(field.coordinate.clone());
        enqueue_type(plan, &field.type_use, pending)?;
        for argument in field.arguments.values() {
            closure.insert(argument.coordinate.clone());
            enqueue_type(plan, &argument.type_use, pending)?;
        }
    }
    Ok(())
}

fn enqueue_type(
    plan: &VisibleSchemaPlan,
    type_use: &TypeUse,
    pending: &mut VecDeque<SchemaName>,
) -> Result<(), DiagnosticSet> {
    let name = match &type_use.shape {
        TypeShape::Named(name) => name,
        TypeShape::List(element) => return enqueue_type(plan, element, pending),
    };
    let coordinate = SchemaCoordinate::named_type(name);
    if !plan.core_coordinates().contains(&coordinate) {
        if !plan.canonical().types().contains_key(name) {
            return Err(client_error(
                DiagnosticCode::ClientSchemaScopeInvalid,
                coordinate.as_str(),
                "selected-module coordinate references an unsupported type",
            ));
        }
        pending.push_back(name.clone());
    }
    Ok(())
}

fn enqueue_names(names: &BTreeSet<SchemaName>, pending: &mut VecDeque<SchemaName>) {
    pending.extend(names.iter().cloned());
}

fn owns_module_type(root: &SchemaName, candidate: &SchemaName) -> bool {
    candidate == root
        || candidate
            .as_str()
            .strip_prefix(root.as_str())
            .is_some_and(|suffix| {
                suffix
                    .bytes()
                    .next()
                    .is_some_and(|first| first.is_ascii_uppercase() || first.is_ascii_digit())
            })
}

fn checked_core_plan(target: &CodegenTarget) -> Result<VisibleSchemaPlan, DiagnosticSet> {
    if target.dagger_revision().as_str()
        != CodegenTarget::decode_exact(include_bytes!("../../../../codegen/target.json"))?
            .dagger_revision()
            .as_str()
    {
        return Err(client_error(
            DiagnosticCode::TargetIdentityInvalid,
            "client.target",
            "client compiler target differs from the checked Core catalog target",
        ));
    }
    CHECKED_CORE_PLAN
        .get_or_init(|| project_visible_schema(target, CHECKED_CORE_SCHEMA))
        .clone()
}

fn resolve_core_bindings(
    visible: &VisibleSchemaPlan,
    checked: &VisibleSchemaPlan,
) -> Result<BTreeMap<BindingKey, CoreBindingReference>, DiagnosticSet> {
    let mut resolved = BTreeMap::new();
    let mut diagnostics = Vec::new();
    for (key, expected) in checked.projection().catalog().bindings() {
        match visible.projection().catalog().bindings().get(key) {
            None => diagnostics.push(Diagnostic::new(
                DiagnosticCode::CapabilityBindingMissing,
                coordinate(key),
                "visible schema projection omits a checked Core catalog binding",
            )),
            Some(_) => {
                // Extension fields deliberately change the complete Query projection.
                // Core compatibility has already verified each original coordinate;
                // the standalone catalog must therefore reuse the checked descriptor,
                // never a whole-type fingerprint contaminated by module extensions.
                resolved.insert(
                    key.clone(),
                    CoreBindingReference {
                        key: key.clone(),
                        public_path: public_core_path(key.rust_symbol.as_deref()),
                        implementation_fingerprint: expected.implementation_fingerprint.clone(),
                    },
                );
            }
        }
    }
    DiagnosticSet::new(diagnostics).map_or(Ok(resolved), Err)
}

struct GeneratedBindingInput<'a> {
    visible: &'a VisibleSchemaPlan,
    checked: &'a VisibleSchemaPlan,
    core: &'a BTreeMap<BindingKey, CoreBindingReference>,
    names: Option<&'a super::model::ClientNamePlan>,
    surface: &'a ClientSchemaSurface,
    types: &'a BTreeMap<SchemaName, TypeProjection>,
    fields: &'a BTreeMap<SchemaCoordinate, crate::projection::fields::FieldProjection>,
    implementations: &'a [InterfaceImplementationProjection],
}

fn resolve_generated_bindings(
    input: GeneratedBindingInput<'_>,
) -> Result<BTreeMap<BindingKey, ClientBindingDescriptor>, DiagnosticSet> {
    let GeneratedBindingInput {
        visible,
        checked,
        core,
        names,
        surface,
        types,
        fields,
        implementations,
    } = input;
    let checked_bindings = checked.projection().catalog().bindings();
    let extension_coordinates = match surface {
        ClientSchemaSurface::CoreOnly => BTreeSet::new(),
        ClientSchemaSurface::BoundModule(module) => module.closure.clone(),
    };
    let implementation_coordinates = implementations
        .iter()
        .map(|implementation| implementation.coordinate.clone())
        .collect::<BTreeSet<_>>();
    let mut rewrites = names
        .map(|names| symbol_rewrites(names, types, surface))
        .transpose()?
        .unwrap_or_default();
    rewrites.sort_by(|left, right| right.0.len().cmp(&left.0.len()).then(left.cmp(right)));
    let signature_context = GeneratedSignatureContext {
        schema: visible.canonical(),
        surface,
        core,
        names,
        fields,
        types,
        implementations,
        rewrites: &rewrites,
    };
    let mut generated = BTreeMap::new();
    let mut diagnostics = Vec::new();
    for (key, descriptor) in visible.projection().catalog().bindings() {
        if checked_bindings.contains_key(key) {
            continue;
        }
        let in_scope = key.wire_coordinate.as_ref().is_some_and(|coordinate| {
            extension_coordinates.contains(coordinate)
                || implementation_coordinates.contains(coordinate)
        });
        if !in_scope {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::ClientSchemaScopeInvalid,
                coordinate(key),
                "projected non-Core binding is outside the selected module closure",
            ));
            continue;
        }
        let transformed_symbol = names
            .map(|names| transformed_symbol(key, names, surface, types, fields, implementations))
            .transpose()?
            .flatten();
        if key.rust_symbol.is_some() && transformed_symbol == key.rust_symbol {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::CapabilityBindingMissing,
                coordinate(key),
                "selected-module binding has no generated public Rust path",
            ));
            continue;
        }
        let transformed_key = BindingKey {
            wire_coordinate: key.wire_coordinate.clone(),
            rust_symbol: transformed_symbol,
            binding_kind: key.binding_kind,
        };
        let rust_signature = client_signature(&signature_context, key, descriptor);
        let transformed = BindingDescriptor::new(
            transformed_key.clone(),
            if transformed_key.binding_kind == crate::projection::catalog::BindingKind::Scalar {
                crate::projection::catalog::CatalogDisposition::Emitted
            } else {
                descriptor.disposition
            },
            rust_signature,
            descriptor.semantic_shape.clone(),
            descriptor.required_evidence.clone(),
        )
        .map_err(|_| {
            client_error(
                DiagnosticCode::CapabilityFingerprintMismatch,
                key.wire_coordinate
                    .as_ref()
                    .map_or("client.binding", SchemaCoordinate::as_str),
                "selected-module binding could not be fingerprinted",
            )
        })?;
        if generated
            .insert(
                transformed_key,
                ClientBindingDescriptor {
                    source: ClientBindingSource::SelectedModule,
                    binding: transformed,
                },
            )
            .is_some()
        {
            diagnostics.push(Diagnostic::new(
                DiagnosticCode::CapabilityBindingDuplicate,
                coordinate(key),
                "selected-module bindings collide after public-path projection",
            ));
        }
    }
    if let Some(names) = names {
        for field in fields
            .values()
            .filter(|field| field.options_type_name.is_some())
        {
            let Some(options_name) = names.get(&field.coordinate, ClientNameRole::Options) else {
                diagnostics.push(Diagnostic::new(
                    DiagnosticCode::CapabilityBindingMissing,
                    Some(DiagnosticCoordinate::new(field.coordinate.as_str())),
                    "field options lost their collision-checked public name",
                ));
                continue;
            };
            let key = BindingKey {
                wire_coordinate: Some(field.coordinate.clone()),
                rust_symbol: Some(format!(
                    "crate::dagger_client::{}::{options_name}",
                    names.namespace
                )),
                binding_kind: crate::projection::catalog::BindingKind::FieldOptions,
            };
            let omittable = field
                .arguments
                .iter()
                .filter(|argument| argument.presence.is_omittable())
                .collect::<Vec<_>>();
            let descriptor = BindingDescriptor::new(
                key.clone(),
                crate::projection::catalog::CatalogDisposition::Emitted,
                options_name.to_string(),
                serde_json::json!({
                    "field": field.coordinate,
                    "omittable_arguments": omittable,
                }),
                [
                    crate::projection::catalog::EvidenceScope::EngineSchema,
                    crate::projection::catalog::EvidenceScope::RustPolicy,
                    crate::projection::catalog::EvidenceScope::RustRuntime,
                ]
                .into_iter()
                .collect(),
            )
            .map_err(|_| {
                client_error(
                    DiagnosticCode::CapabilityFingerprintMismatch,
                    field.coordinate.as_str(),
                    "field-options binding could not be fingerprinted",
                )
            })?;
            if generated
                .insert(
                    key,
                    ClientBindingDescriptor {
                        source: ClientBindingSource::SelectedModule,
                        binding: descriptor,
                    },
                )
                .is_some()
            {
                diagnostics.push(Diagnostic::new(
                    DiagnosticCode::CapabilityBindingDuplicate,
                    Some(DiagnosticCoordinate::new(field.coordinate.as_str())),
                    "field-options binding collides with another generated path",
                ));
            }
        }
    }
    DiagnosticSet::new(diagnostics).map_or(Ok(generated), Err)
}

struct GeneratedSignatureContext<'a> {
    schema: &'a crate::schema::canonical::CanonicalSchema,
    surface: &'a ClientSchemaSurface,
    core: &'a BTreeMap<BindingKey, CoreBindingReference>,
    names: Option<&'a super::model::ClientNamePlan>,
    fields: &'a BTreeMap<SchemaCoordinate, crate::projection::fields::FieldProjection>,
    types: &'a BTreeMap<SchemaName, TypeProjection>,
    implementations: &'a [InterfaceImplementationProjection],
    rewrites: &'a [(String, String)],
}

fn client_signature(
    context: &GeneratedSignatureContext<'_>,
    key: &BindingKey,
    descriptor: &BindingDescriptor,
) -> String {
    let Some(coordinate) = key.wire_coordinate.as_ref() else {
        return rewrite_signature(&descriptor.rust_signature, context.rewrites);
    };
    match key.binding_kind {
        crate::projection::catalog::BindingKind::InterfaceImplementation => context
            .implementations
            .iter()
            .find(|implementation| &implementation.coordinate == coordinate)
            .and_then(|implementation| {
                let names = context.names?;
                let implementor = names.get(
                    &SchemaCoordinate::named_type(&implementation.implementor),
                    ClientNameRole::Object,
                )?;
                let interface = names.get(
                    &SchemaCoordinate::named_type(&implementation.interface),
                    ClientNameRole::InterfaceTrait,
                )?;
                Some(format!("{implementor}: {interface}"))
            })
            .unwrap_or_else(|| rewrite_signature(&descriptor.rust_signature, context.rewrites)),
        crate::projection::catalog::BindingKind::FieldOperation => {
            if let (ClientSchemaSurface::BoundModule(module), Some(names)) =
                (context.surface, context.names)
                && coordinate == &module.root.field_coordinate
            {
                let method = crate::naming::rust_name(
                    &module.root.field_wire_name,
                    crate::naming::NameContext::Method,
                )
                .identifier;
                format!(
                    "fn {method}(&self) -> crate::dagger_client::{}::Client",
                    names.namespace
                )
            } else {
                context
                    .fields
                    .get(coordinate)
                    .map(|field| field_signature(context, field))
                    .unwrap_or_else(|| {
                        rewrite_signature(&descriptor.rust_signature, context.rewrites)
                    })
            }
        }
        crate::projection::catalog::BindingKind::Argument => context
            .fields
            .values()
            .flat_map(|field| &field.arguments)
            .find(|argument| &argument.coordinate == coordinate)
            .map(|argument| {
                nullable_input_signature(
                    &argument.rust_type,
                    &argument.presence,
                    canonical_input_type_use(context.schema, coordinate),
                    context.names,
                    context.types,
                    context.core,
                    context.rewrites,
                )
            })
            .unwrap_or_else(|| rewrite_signature(&descriptor.rust_signature, context.rewrites)),
        crate::projection::catalog::BindingKind::InputField => context
            .types
            .values()
            .filter_map(|projection| match projection {
                TypeProjection::InputObject(input) => input
                    .fields
                    .values()
                    .find(|field| &field.coordinate == coordinate),
                _ => None,
            })
            .next()
            .map(|field| {
                let value = nullable_input_signature(
                    &field.rust_type,
                    &field.presence,
                    canonical_input_type_use(context.schema, coordinate),
                    context.names,
                    context.types,
                    context.core,
                    context.rewrites,
                );
                if field.presence.is_omittable() {
                    format!("Option<{value}>")
                } else {
                    value
                }
            })
            .unwrap_or_else(|| rewrite_signature(&descriptor.rust_signature, context.rewrites)),
        _ => rewrite_signature(&descriptor.rust_signature, context.rewrites),
    }
}

fn transformed_symbol(
    key: &BindingKey,
    names: &super::model::ClientNamePlan,
    surface: &ClientSchemaSurface,
    types: &BTreeMap<SchemaName, TypeProjection>,
    fields: &BTreeMap<SchemaCoordinate, crate::projection::fields::FieldProjection>,
    implementations: &[InterfaceImplementationProjection],
) -> Result<Option<String>, DiagnosticSet> {
    use crate::projection::catalog::BindingKind;

    if key.rust_symbol.is_none() {
        return Ok(None);
    }
    let namespace = names.namespace.as_str();
    let coordinate = key.wire_coordinate.as_ref().ok_or_else(|| {
        client_error(
            DiagnosticCode::CapabilityBindingMissing,
            "client.binding",
            "selected-module symbol has no schema coordinate",
        )
    })?;
    let local = |name: &str| format!("crate::dagger_client::{namespace}::{name}");
    let selected =
        |coordinate: &SchemaCoordinate, role| names.get(coordinate, role).map(ToString::to_string);
    let symbol = match key.binding_kind {
        BindingKind::Scalar => {
            selected(coordinate, ClientNameRole::CustomScalar).map(|name| local(&name))
        }
        BindingKind::ObjectHandle => {
            selected(coordinate, ClientNameRole::Object).map(|name| local(&name))
        }
        BindingKind::InterfaceTrait => {
            selected(coordinate, ClientNameRole::InterfaceTrait).map(|name| local(&name))
        }
        BindingKind::InterfaceClient => {
            selected(coordinate, ClientNameRole::InterfaceClient).map(|name| local(&name))
        }
        BindingKind::Enum => selected(coordinate, ClientNameRole::Enum).map(|name| local(&name)),
        BindingKind::InputObject => {
            selected(coordinate, ClientNameRole::InputObject).map(|name| local(&name))
        }
        BindingKind::EnumVariant | BindingKind::EnumAlias => {
            types.values().find_map(|projection| {
                let TypeProjection::Enum(enumeration) = projection else {
                    return None;
                };
                let variant = enumeration
                    .variants
                    .values()
                    .find(|variant| &variant.coordinate == coordinate)
                    .map(|variant| variant.rust_name.as_str())
                    .or_else(|| {
                        enumeration
                            .aliases
                            .values()
                            .find(|alias| &alias.coordinate == coordinate)
                            .map(|alias| alias.rust_name.as_str())
                    })?;
                let owner = selected(&enumeration.coordinate, ClientNameRole::Enum)?;
                Some(format!("{}::{variant}", local(&owner)))
            })
        }
        BindingKind::InputField => types.values().find_map(|projection| {
            let TypeProjection::InputObject(input) = projection else {
                return None;
            };
            let field = input
                .fields
                .values()
                .find(|field| &field.coordinate == coordinate)?;
            let owner = selected(&input.coordinate, ClientNameRole::InputObject)?;
            Some(format!("{}::{}", local(&owner), field.rust_name))
        }),
        BindingKind::FieldOperation => {
            let module = match surface {
                ClientSchemaSurface::BoundModule(module) => module,
                ClientSchemaSurface::CoreOnly => {
                    return Ok(None);
                }
            };
            if coordinate == &module.root.field_coordinate {
                let method = crate::naming::rust_name(
                    &module.root.field_wire_name,
                    crate::naming::NameContext::Method,
                )
                .identifier;
                Some(format!(
                    "crate::dagger_client::{}::{method}",
                    names.extension_trait
                ))
            } else {
                fields.get(coordinate).and_then(|field| {
                    owner_name(names, types, &field.owner)
                        .map(|owner| format!("{}::{}", local(&owner), field.rust_name))
                })
            }
        }
        BindingKind::Argument => fields.values().find_map(|field| {
            let argument = field
                .arguments
                .iter()
                .find(|argument| &argument.coordinate == coordinate)?;
            let owner = owner_name(names, types, &field.owner)?;
            Some(format!(
                "{}::{}::{}",
                local(&owner),
                field.rust_name,
                argument.rust_name
            ))
        }),
        BindingKind::InterfaceImplementation => implementations.iter().find_map(|edge| {
            if &edge.coordinate != coordinate {
                return None;
            }
            let interface = selected(
                &SchemaCoordinate::named_type(&edge.interface),
                ClientNameRole::InterfaceTrait,
            )?;
            let implementor = selected(
                &SchemaCoordinate::named_type(&edge.implementor),
                ClientNameRole::Object,
            )?;
            Some(format!(
                "impl {} for {}",
                local(&interface),
                local(&implementor)
            ))
        }),
        BindingKind::QueryRoot
        | BindingKind::FieldOptions
        | BindingKind::TargetPrivateType
        | BindingKind::TargetPrivateField
        | BindingKind::DirectivePolicy
        | BindingKind::DirectiveArgument => None,
    };
    symbol.map(Some).ok_or_else(|| {
        client_error(
            DiagnosticCode::CapabilityBindingMissing,
            coordinate.as_str(),
            "selected-module binding has no generated public Rust path",
        )
    })
}

fn owner_name(
    names: &super::model::ClientNamePlan,
    types: &BTreeMap<SchemaName, TypeProjection>,
    owner: &SchemaName,
) -> Option<String> {
    let projection = types.get(owner)?;
    let role = match projection {
        TypeProjection::Object(_) => ClientNameRole::Object,
        TypeProjection::Interface(_) => ClientNameRole::InterfaceClient,
        _ => return None,
    };
    names
        .get(projection_coordinate(projection), role)
        .map(ToString::to_string)
}

fn field_signature(
    context: &GeneratedSignatureContext<'_>,
    field: &crate::projection::fields::FieldProjection,
) -> String {
    let required = field
        .arguments
        .iter()
        .filter(|argument| {
            argument.presence == crate::projection::fields::ArgumentPresence::Required
        })
        .map(|argument| {
            let value = nullable_input_signature(
                &argument.rust_type,
                &argument.presence,
                canonical_input_type_use(context.schema, &argument.coordinate),
                context.names,
                context.types,
                context.core,
                context.rewrites,
            );
            if argument.encoder.contains_lazy_id() {
                format!("{}: impl Into<{value}>", argument.rust_name)
            } else {
                format!("{}: {value}", argument.rust_name)
            }
        })
        .collect::<Vec<_>>()
        .join(", ");
    let parameters = if required.is_empty() {
        "&self".to_owned()
    } else {
        format!("&self, {required}")
    };
    let output = client_type_signature(
        &field.return_type,
        context.names,
        context.types,
        context.core,
        context.rewrites,
    );
    let (async_token, result) = if matches!(
        field.strategy,
        crate::projection::fields::FieldStrategy::LazyHandle { .. }
    ) {
        ("", output)
    } else {
        (
            "async ",
            format!("Result<{output}, dagger_sdk::QueryError>"),
        )
    };
    let mut signature = format!(
        "{async_token}fn {}({parameters}) -> {result}",
        field.rust_name
    );
    if field
        .arguments
        .iter()
        .any(|argument| argument.presence.is_omittable())
        && let (Some(names), Some(method)) = (context.names, &field.options_method_name)
        && let Some(options) = names.get(&field.coordinate, ClientNameRole::Options)
    {
        signature.push_str(&format!(
            "; {async_token}fn {method}({parameters}, opts: {options}) -> {result}"
        ));
    }
    signature
}

fn nullable_input_signature(
    rust_type: &crate::projection::types::RustType,
    presence: &crate::projection::fields::ArgumentPresence,
    type_use: Option<&TypeUse>,
    names: Option<&super::model::ClientNamePlan>,
    types: &BTreeMap<SchemaName, TypeProjection>,
    core: &BTreeMap<BindingKey, CoreBindingReference>,
    rewrites: &[(String, String)],
) -> String {
    let signature = client_type_signature(rust_type, names, types, core, rewrites);
    if presence.is_omittable()
        && type_use.is_some_and(|type_use| type_use.nullable)
        && !matches!(rust_type, crate::projection::types::RustType::Unit)
    {
        format!("Option<{signature}>")
    } else {
        signature
    }
}

fn client_type_signature(
    rust_type: &crate::projection::types::RustType,
    names: Option<&super::model::ClientNamePlan>,
    types: &BTreeMap<SchemaName, TypeProjection>,
    core: &BTreeMap<BindingKey, CoreBindingReference>,
    rewrites: &[(String, String)],
) -> String {
    use crate::projection::types::RustType;

    let local_name = |name: &SchemaName, role| {
        names.and_then(|names| {
            types.get(name).and_then(|projection| {
                names
                    .get(projection_coordinate(projection), role)
                    .map(ToString::to_string)
            })
        })
    };
    match rust_type {
        RustType::Id => "dagger_sdk::Id".to_owned(),
        RustType::Json => "dagger_sdk::Json".to_owned(),
        RustType::Platform => "dagger_sdk::Platform".to_owned(),
        RustType::Enum(name) => local_name(name, ClientNameRole::Enum)
            .map(|name| format!("super::{name}"))
            .or_else(|| core_type_path(core, name, crate::projection::catalog::BindingKind::Enum))
            .unwrap_or_else(|| rewrite_signature(name.as_str(), rewrites)),
        RustType::Input(name) => local_name(name, ClientNameRole::InputObject)
            .map(|name| format!("super::{name}"))
            .or_else(|| {
                core_type_path(
                    core,
                    name,
                    crate::projection::catalog::BindingKind::InputObject,
                )
            })
            .unwrap_or_else(|| rewrite_signature(name.as_str(), rewrites)),
        RustType::CustomScalar(name) => local_name(name, ClientNameRole::CustomScalar)
            .map(|name| format!("super::{name}"))
            .unwrap_or_else(|| rewrite_signature(name.as_str(), rewrites)),
        RustType::Handle(name) => local_name(name, ClientNameRole::Object)
            .map(|name| format!("super::{name}"))
            .or_else(|| {
                core_type_path(
                    core,
                    name,
                    crate::projection::catalog::BindingKind::ObjectHandle,
                )
            })
            .unwrap_or_else(|| rewrite_signature(name.as_str(), rewrites)),
        RustType::InterfaceHandle(name) => local_name(name, ClientNameRole::InterfaceClient)
            .map(|name| format!("super::{name}"))
            .or_else(|| {
                core_type_path(
                    core,
                    name,
                    crate::projection::catalog::BindingKind::InterfaceClient,
                )
            })
            .unwrap_or_else(|| rewrite_signature(name.as_str(), rewrites)),
        RustType::IdInput(name) => format!(
            "dagger_sdk::IdInput<{}>",
            local_name(name, ClientNameRole::Object)
                .map(|name| format!("super::{name}"))
                .or_else(|| {
                    local_name(name, ClientNameRole::InterfaceClient)
                        .map(|name| format!("super::{name}"))
                })
                .or_else(|| {
                    core_type_path(
                        core,
                        name,
                        crate::projection::catalog::BindingKind::ObjectHandle,
                    )
                })
                .or_else(|| {
                    core_type_path(
                        core,
                        name,
                        crate::projection::catalog::BindingKind::InterfaceClient,
                    )
                })
                .unwrap_or_else(|| rewrite_signature(name.as_str(), rewrites))
        ),
        RustType::Option(inner) => format!(
            "Option<{}>",
            client_type_signature(inner, names, types, core, rewrites)
        ),
        RustType::Vec(inner) => format!(
            "Vec<{}>",
            client_type_signature(inner, names, types, core, rewrites)
        ),
        _ => rust_type.signature(),
    }
}

fn core_type_path(
    core: &BTreeMap<BindingKey, CoreBindingReference>,
    name: &SchemaName,
    kind: crate::projection::catalog::BindingKind,
) -> Option<String> {
    let coordinate = SchemaCoordinate::named_type(name);
    core.values()
        .find(|binding| {
            binding.key.wire_coordinate.as_ref() == Some(&coordinate)
                && binding.key.binding_kind == kind
        })
        .and_then(|binding| binding.public_path.clone())
}

fn projection_coordinate(projection: &TypeProjection) -> &SchemaCoordinate {
    match projection {
        TypeProjection::Scalar(value) => &value.coordinate,
        TypeProjection::Object(value) => &value.coordinate,
        TypeProjection::Interface(value) => &value.coordinate,
        TypeProjection::Enum(value) => &value.coordinate,
        TypeProjection::InputObject(value) => &value.coordinate,
        TypeProjection::TargetPrivate(value) => &value.coordinate,
    }
}

fn canonical_input_type_use<'a>(
    schema: &'a crate::schema::canonical::CanonicalSchema,
    coordinate: &SchemaCoordinate,
) -> Option<&'a TypeUse> {
    schema
        .types()
        .values()
        .find_map(|definition| match definition {
            TypeDefinition::Object(object) => object
                .fields
                .values()
                .flat_map(|field| field.arguments.values())
                .find(|argument| &argument.coordinate == coordinate)
                .map(|argument| &argument.type_use),
            TypeDefinition::Interface(interface) => interface
                .fields
                .values()
                .flat_map(|field| field.arguments.values())
                .find(|argument| &argument.coordinate == coordinate)
                .map(|argument| &argument.type_use),
            TypeDefinition::InputObject(input) => input
                .fields
                .values()
                .find(|field| &field.coordinate == coordinate)
                .map(|field| &field.type_use),
            TypeDefinition::Scalar(_) | TypeDefinition::Enum(_) => None,
        })
}

fn rewrite_signature(signature: &str, rewrites: &[(String, String)]) -> String {
    rewrites
        .iter()
        .filter_map(|(from, to)| {
            let original = from.strip_prefix("crate::gen::")?;
            (!original.contains("::"))
                .then(|| (original, to.rsplit("::").next().unwrap_or(to.as_str())))
        })
        .fold(signature.to_owned(), |value, (from, to)| {
            value.replace(from, to)
        })
}

fn symbol_rewrites(
    names: &super::model::ClientNamePlan,
    types: &BTreeMap<SchemaName, TypeProjection>,
    surface: &ClientSchemaSurface,
) -> Result<Vec<(String, String)>, DiagnosticSet> {
    let mut rewrites = Vec::new();
    let namespace = names.namespace.as_str();
    let module = match surface {
        ClientSchemaSurface::BoundModule(module) => module,
        ClientSchemaSurface::CoreOnly => return Ok(rewrites),
    };
    let root_method = crate::naming::rust_name(
        &module.root.field_wire_name,
        crate::naming::NameContext::Method,
    )
    .identifier;
    rewrites.push((
        format!("crate::gen::Query::{root_method}"),
        format!(
            "crate::dagger_client::{}::{root_method}",
            names.extension_trait
        ),
    ));
    for projection in types.values() {
        let (coordinate, roles) = match projection {
            TypeProjection::Object(object) => (
                &object.coordinate,
                vec![(ClientNameRole::Object, object.rust_name.as_str())],
            ),
            TypeProjection::Interface(interface) => (
                &interface.coordinate,
                vec![
                    (
                        ClientNameRole::InterfaceTrait,
                        interface.trait_name.as_str(),
                    ),
                    (
                        ClientNameRole::InterfaceClient,
                        interface.client_name.as_str(),
                    ),
                ],
            ),
            TypeProjection::Enum(enumeration) => (
                &enumeration.coordinate,
                vec![(ClientNameRole::Enum, enumeration.rust_name.as_str())],
            ),
            TypeProjection::InputObject(input) => (
                &input.coordinate,
                vec![(ClientNameRole::InputObject, input.rust_name.as_str())],
            ),
            TypeProjection::Scalar(scalar)
                if scalar.scalar == crate::projection::types::ScalarKind::Custom =>
            {
                (
                    &scalar.coordinate,
                    vec![(ClientNameRole::CustomScalar, scalar.wire_name.as_str())],
                )
            }
            TypeProjection::Scalar(_) | TypeProjection::TargetPrivate(_) => continue,
        };
        for (role, original) in roles {
            let selected = names.get(coordinate, role).ok_or_else(|| {
                client_error(
                    DiagnosticCode::RustNameInvalid,
                    coordinate.as_str(),
                    "selected-module binding lost its collision-checked Rust name",
                )
            })?;
            rewrites.push((
                format!("crate::gen::{original}"),
                format!("crate::dagger_client::{namespace}::{selected}"),
            ));
        }
    }
    Ok(rewrites)
}

fn build_catalog(
    target: &CodegenTarget,
    schema_digest: &SemanticDigest,
    core: &BTreeMap<BindingKey, CoreBindingReference>,
    generated: &BTreeMap<BindingKey, ClientBindingDescriptor>,
) -> Result<ClientBindingCatalog, DiagnosticSet> {
    let core = core.values().cloned().collect::<Vec<_>>();
    let generated = generated.values().cloned().collect::<Vec<_>>();
    let digest = SemanticDigest::for_value(&(
        target,
        schema_digest,
        &core,
        &generated,
        "dagger-rust-standalone-client-catalog-v1",
    ))
    .map_err(|_| {
        client_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "binding-catalog.json",
            "standalone-client binding catalog could not be fingerprinted",
        )
    })?;
    Ok(ClientBindingCatalog {
        target: target.clone(),
        visible_schema_digest: schema_digest.clone(),
        core,
        generated,
        digest,
    })
}

fn public_core_path(symbol: Option<&str>) -> Option<String> {
    symbol.map(|symbol| {
        symbol
            .replace("crate::gen::", "dagger_sdk::")
            .replace("crate::", "dagger_sdk::")
    })
}

fn coordinate(key: &BindingKey) -> Option<DiagnosticCoordinate> {
    key.wire_coordinate
        .as_ref()
        .map(|coordinate| DiagnosticCoordinate::new(coordinate.as_str()))
}

fn client_error(code: DiagnosticCode, coordinate: &str, message: &str) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate)),
        message,
    ))
}
