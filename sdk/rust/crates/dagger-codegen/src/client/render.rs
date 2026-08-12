//! Deterministic standalone-client subtree rendering.
//!
//! Rendering consumes only a completed client binding plan. It emits generated Rust
//! and catalog bytes without consulting a filesystem or interpreting schema policy.

use std::collections::{BTreeMap, BTreeSet};

use convert_case::{Case, Casing};

use crate::diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate, DiagnosticSet};
use crate::engine::{CandidateArtifact, CandidateArtifactKind, RelativeOperationPath};
use crate::projection::catalog::{BindingKind, SemanticDigest};
use crate::projection::fields::{ArgumentPresence, FieldProjection, FieldStrategy};
use crate::projection::types::{
    EnumProjection, InputObjectProjection, InterfaceProjection, ObjectProjection, RustType,
    TypeProjection,
};
use crate::schema::canonical::{SchemaCoordinate, SchemaName, TypeDefinition};

use super::compiler::validate_core_references;
use super::model::{
    ClientBindingCatalog, ClientBindingPlan, ClientNamePlan, ClientNameRole, ClientSchemaSurface,
};

/// Complete generated subtree and semantic catalog before publication.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RenderedClient {
    /// Candidate artifacts in normalized project-relative order.
    pub artifacts: BTreeMap<RelativeOperationPath, CandidateArtifact>,
    /// Exhaustive catalog encoded into the generated subtree.
    pub catalog: ClientBindingCatalog,
    /// Exact generated Rust set requiring pinned formatting.
    pub rust_sources: BTreeSet<RelativeOperationPath>,
}

/// Renders one standalone client beneath `project_root`.
pub fn render_client(
    plan: &ClientBindingPlan,
    project_root: &RelativeOperationPath,
) -> Result<RenderedClient, DiagnosticSet> {
    validate_plan(plan)?;
    let client_root = project_root.join("src/dagger_client")?;
    let generated_root = client_root.join("generated")?;
    let mut artifacts = BTreeMap::new();
    let top = render_top_module(plan)?;
    insert_source(&mut artifacts, client_root.join("mod.rs")?, top)?;
    insert_source(
        &mut artifacts,
        generated_root.join("mod.rs")?,
        format!(
            "{}\n",
            header(
                plan,
                "Private generated index for one standalone Dagger client."
            )?
        ),
    )?;

    if let (ClientSchemaSurface::BoundModule(surface), Some(names)) = (&plan.surface, &plan.names) {
        let namespace_root = generated_root.join(&file_identifier(names.namespace.as_str()))?;
        let mut modules = Vec::new();
        for projection in plan.module_types.values() {
            let (file, source) = render_type(plan, names, projection)?;
            insert_source(&mut artifacts, namespace_root.join(&file)?, source)?;
            modules.push((module_identifier(&file), file));
        }
        modules.sort();
        let mut index = header(
            plan,
            &format!(
                "Typed bindings for GraphQL module root `{}`.",
                surface.root.field_wire_name
            ),
        )?;
        for (module, file) in modules {
            index.push_str(&format!(
                "#[path = {file:?}]\nmod {module};\npub use {module}::*;\n"
            ));
        }
        insert_source(&mut artifacts, namespace_root.join("mod.rs")?, index)?;
    }

    let catalog = serde_json::to_vec(&plan.catalog).map_err(|_| {
        render_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "binding-catalog.json",
            "standalone-client binding catalog could not be encoded",
        )
    })?;
    insert(
        &mut artifacts,
        generated_root.join("binding-catalog.json")?,
        CandidateArtifactKind::ControlManifest,
        catalog,
    )?;
    insert_source(
        &mut artifacts,
        project_root.join("examples/dagger-client-quickstart.rs")?,
        render_quickstart(plan)?,
    )?;
    let rust_sources = artifacts
        .iter()
        .filter(|(_, artifact)| artifact.kind == CandidateArtifactKind::RustSource)
        .map(|(path, _)| path.clone())
        .collect();
    Ok(RenderedClient {
        artifacts,
        catalog: plan.catalog.clone(),
        rust_sources,
    })
}

fn validate_plan(plan: &ClientBindingPlan) -> Result<(), DiagnosticSet> {
    validate_core_references(plan)?;
    let expected_core = plan.core_bindings.values().cloned().collect::<Vec<_>>();
    let expected_generated = plan
        .generated_bindings
        .values()
        .cloned()
        .collect::<Vec<_>>();
    if plan.catalog.core != expected_core || plan.catalog.generated != expected_generated {
        return Err(render_error(
            DiagnosticCode::CapabilityBindingMissing,
            "binding-catalog.json",
            "standalone-client catalog is not exhaustive for its binding plan",
        ));
    }
    let expected_digest = SemanticDigest::for_value(&(
        &plan.catalog.target,
        &plan.catalog.visible_schema_digest,
        &plan.catalog.core,
        &plan.catalog.generated,
        "dagger-rust-standalone-client-catalog-v1",
    ))
    .map_err(|_| {
        render_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "binding-catalog.json",
            "standalone-client catalog could not be fingerprinted",
        )
    })?;
    if expected_digest != plan.catalog.digest {
        return Err(render_error(
            DiagnosticCode::CapabilityFingerprintMismatch,
            "binding-catalog.json",
            "standalone-client catalog digest does not match its semantic bindings",
        ));
    }
    match (&plan.surface, &plan.names) {
        (ClientSchemaSurface::CoreOnly, None) => {}
        (ClientSchemaSurface::BoundModule(_), Some(_)) => {}
        _ => {
            return Err(render_error(
                DiagnosticCode::CapabilityBindingMissing,
                "client.names",
                "client surface and collision-checked name plan disagree",
            ));
        }
    }
    Ok(())
}

fn render_top_module(plan: &ClientBindingPlan) -> Result<String, DiagnosticSet> {
    let mut source = header(
        plan,
        "Standalone Dagger client composed over the shared public Rust SDK runtime.",
    )?;
    source.push_str(
        "pub use dagger_sdk::{Client, ClientConfig, connect, connect_with};\n\
         pub use dagger_sdk as core;\n\
         mod generated;\n",
    );
    match (&plan.surface, &plan.names) {
        (ClientSchemaSurface::CoreOnly, None) => {
            source.push_str(
                "/// Imports the standalone client's extension traits.\n\
                 pub mod prelude {}\n",
            );
        }
        (ClientSchemaSurface::BoundModule(surface), Some(names)) => {
            let namespace = names.namespace.as_str();
            let trait_name = names.extension_trait.as_str();
            let method = crate::naming::rust_name(
                &surface.root.field_wire_name,
                crate::naming::NameContext::Method,
            )
            .identifier;
            let wire = rust_literal(surface.root.field_wire_name.as_str())?;
            source.push_str(&format!(
                "/// Typed bindings for selected GraphQL module `{}`.\n\
                 #[path = \"generated/{}/mod.rs\"]\n\
                 pub mod {namespace};\n\
                 /// Adds the selected GraphQL module root to an existing shared client.\n\
                 pub trait {trait_name} {{\n\
                 /// Selects GraphQL root field `{}` without opening another session.\n\
                 fn {method}(&self) -> {namespace}::Client;\n\
                 }}\n\
                 impl {trait_name} for dagger_sdk::Client {{\n\
                 fn {method}(&self) -> {namespace}::Client {{\n\
                 {namespace}::Client::from_query(self.query_builder().select({wire}))\n\
                 }}\n\
                 }}\n\
                 impl {trait_name} for dagger_sdk::QueryBuilder {{\n\
                 fn {method}(&self) -> {namespace}::Client {{\n\
                 {namespace}::Client::from_query(self.select({wire}))\n\
                 }}\n\
                 }}\n\
                 /// Imports the selected module extension trait for method resolution.\n\
                 pub mod prelude {{ pub use super::{trait_name} as _; }}\n",
                surface.root.field_wire_name,
                file_identifier(namespace),
                surface.root.field_wire_name,
            ));
        }
        _ => {
            return Err(render_error(
                DiagnosticCode::CapabilityBindingMissing,
                "client.names",
                "client renderer received an inconsistent surface/name plan",
            ));
        }
    }
    checked_source("src/dagger_client/mod.rs", source)
}

fn render_type(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    projection: &TypeProjection,
) -> Result<(String, String), DiagnosticSet> {
    let (coordinate, primary, body) = match projection {
        TypeProjection::Object(object) => (
            &object.coordinate,
            required_name(names, &object.coordinate, ClientNameRole::Object)?,
            render_object(plan, names, object)?,
        ),
        TypeProjection::Interface(interface) => (
            &interface.coordinate,
            required_name(names, &interface.coordinate, ClientNameRole::InterfaceTrait)?,
            render_interface(plan, names, interface)?,
        ),
        TypeProjection::Enum(enumeration) => (
            &enumeration.coordinate,
            required_name(names, &enumeration.coordinate, ClientNameRole::Enum)?,
            render_enum(plan, names, enumeration)?,
        ),
        TypeProjection::InputObject(input) => (
            &input.coordinate,
            required_name(names, &input.coordinate, ClientNameRole::InputObject)?,
            render_input(plan, names, input)?,
        ),
        TypeProjection::Scalar(scalar)
            if scalar.scalar == crate::projection::types::ScalarKind::Custom =>
        {
            (
                &scalar.coordinate,
                required_name(names, &scalar.coordinate, ClientNameRole::CustomScalar)?,
                render_custom_scalar(plan, names, scalar)?,
            )
        }
        TypeProjection::Scalar(scalar) => {
            return Err(render_error(
                DiagnosticCode::ClientSchemaScopeInvalid,
                scalar.coordinate.as_str(),
                "selected module attempts to regenerate a runtime-provided scalar",
            ));
        }
        TypeProjection::TargetPrivate(private) => {
            return Err(render_error(
                DiagnosticCode::ClientSchemaScopeInvalid,
                private.coordinate.as_str(),
                "selected module contains a target-private type",
            ));
        }
    };
    let mut source = header(
        plan,
        &format!("Generated bindings owned by GraphQL coordinate `{coordinate}`."),
    )?;
    source.push_str(&body);
    let file = format!("{}.rs", file_identifier(primary));
    Ok((file.clone(), checked_source(&file, source)?))
}

fn render_object(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    object: &ObjectProjection,
) -> Result<String, DiagnosticSet> {
    let name = required_name(names, &object.coordinate, ClientNameRole::Object)?;
    let definition = match definition(plan, &object.wire_name)? {
        TypeDefinition::Object(definition) => definition,
        _ => return Err(lost_definition(&object.coordinate, "object")),
    };
    let attributes = public_attributes(
        &object.coordinate,
        definition.description.as_deref(),
        &format!("Lazy handle for GraphQL object `{}`.", object.wire_name),
        None,
        None,
    )?;
    let (support, methods) = render_fields(plan, names, &object.wire_name)?;
    let mut source = format!(
        "{attributes}#[derive(Clone)]\npub struct {name} {{ query: dagger_sdk::QueryBuilder }}\n{support}\
         impl {name} {{\n\
         #[doc(hidden)]\n#[must_use]\npub fn from_query(query: dagger_sdk::QueryBuilder) -> Self {{ Self {{ query }} }}\n\
         /// Borrows the immutable query represented by this handle.\n#[must_use]\npub fn selection(&self) -> &dagger_sdk::QueryBuilder {{ &self.query }}\n\
         {methods}}}\n"
    );
    if object.has_id {
        source.push_str(&render_into_id(name));
    }
    for implementation in plan
        .module_implementations
        .iter()
        .filter(|implementation| implementation.implementor == object.wire_name)
    {
        let interface_coordinate = SchemaCoordinate::named_type(&implementation.interface);
        let interface =
            required_name(names, &interface_coordinate, ClientNameRole::InterfaceTrait)?;
        source.push_str(&format!(
            "impl super::{interface} for {name} {{\n\
             fn selection(&self) -> &dagger_sdk::QueryBuilder {{ &self.query }}\n\
             }}\n"
        ));
    }
    Ok(source)
}

fn render_custom_scalar(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    scalar: &crate::projection::types::ScalarProjection,
) -> Result<String, DiagnosticSet> {
    let name = required_name(names, &scalar.coordinate, ClientNameRole::CustomScalar)?;
    let definition = match definition(plan, &scalar.wire_name)? {
        TypeDefinition::Scalar(definition) => definition,
        _ => return Err(lost_definition(&scalar.coordinate, "custom scalar")),
    };
    let attributes = public_attributes(
        &scalar.coordinate,
        definition.description.as_deref(),
        &format!(
            "Exact string wire value for custom GraphQL scalar `{}`.",
            scalar.wire_name
        ),
        None,
        None,
    )?;
    Ok(format!(
        "{attributes}#[derive(Clone, Debug, Eq, Hash, PartialEq, dagger_sdk::__private::serde::Deserialize, dagger_sdk::__private::serde::Serialize)]\n\
         #[serde(crate = \"dagger_sdk::__private::serde\", transparent)]\npub struct {name}(\n\
         /// Exact string retained for GraphQL request and response encoding.\n\
         pub String,\n\
         );\n\
         impl From<String> for {name} {{ fn from(value: String) -> Self {{ Self(value) }} }}\n\
         impl From<&str> for {name} {{ fn from(value: &str) -> Self {{ Self(value.to_owned()) }} }}\n"
    ))
}

fn render_interface(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    interface: &InterfaceProjection,
) -> Result<String, DiagnosticSet> {
    let trait_name = required_name(names, &interface.coordinate, ClientNameRole::InterfaceTrait)?;
    let client_name = required_name(
        names,
        &interface.coordinate,
        ClientNameRole::InterfaceClient,
    )?;
    let definition = match definition(plan, &interface.wire_name)? {
        TypeDefinition::Interface(definition) => definition,
        _ => return Err(lost_definition(&interface.coordinate, "interface")),
    };
    let trait_attributes = public_attributes(
        &interface.coordinate,
        definition.description.as_deref(),
        &format!(
            "Generated trait for GraphQL interface `{}`.",
            interface.wire_name
        ),
        None,
        None,
    )?;
    let client_attributes = public_attributes(
        &interface.coordinate,
        None,
        &format!(
            "Lazy handle for GraphQL interface `{}`.",
            interface.wire_name
        ),
        None,
        None,
    )?;
    let (support, methods) = render_fields(plan, names, &interface.wire_name)?;
    let mut source = format!(
        "{trait_attributes}pub trait {trait_name} {{\n\
         /// Borrows the immutable query represented by this interface handle.\nfn selection(&self) -> &dagger_sdk::QueryBuilder;\n\
         }}\n\
         {client_attributes}#[derive(Clone)]\npub struct {client_name} {{ query: dagger_sdk::QueryBuilder }}\n{support}\
         impl {client_name} {{\n\
         #[doc(hidden)]\n#[must_use]\npub fn from_query(query: dagger_sdk::QueryBuilder) -> Self {{ Self {{ query }} }}\n\
         {methods}}}\n\
         impl {trait_name} for {client_name} {{ fn selection(&self) -> &dagger_sdk::QueryBuilder {{ &self.query }} }}\n"
    );
    if interface.has_id {
        source.push_str(&render_into_id(client_name));
    }
    Ok(source)
}

fn render_enum(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    enumeration: &EnumProjection,
) -> Result<String, DiagnosticSet> {
    let name = required_name(names, &enumeration.coordinate, ClientNameRole::Enum)?;
    let definition = match definition(plan, &enumeration.wire_name)? {
        TypeDefinition::Enum(definition) => definition,
        _ => return Err(lost_definition(&enumeration.coordinate, "enum")),
    };
    let attributes = public_attributes(
        &enumeration.coordinate,
        definition.description.as_deref(),
        &format!("Closed GraphQL enum `{}`.", enumeration.wire_name),
        None,
        None,
    )?;
    let mut source = format!(
        "{attributes}#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, dagger_sdk::__private::serde::Deserialize, dagger_sdk::__private::serde::Serialize)]\n\
         #[serde(crate = \"dagger_sdk::__private::serde\")]\npub enum {name} {{\n"
    );
    for variant in enumeration.variants.values() {
        let attributes = public_attributes(
            &variant.coordinate,
            variant.description.as_deref(),
            &format!("GraphQL enum value `{}`.", variant.wire_name),
            variant.deprecation.as_deref(),
            variant.experimental.as_deref(),
        )?;
        source.push_str(&format!(
            "{attributes}#[serde(rename = {})]\n{},\n",
            rust_literal(variant.wire_name.as_str())?,
            variant.rust_name
        ));
    }
    source.push_str("}\n");
    Ok(source)
}

fn render_input(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    input: &InputObjectProjection,
) -> Result<String, DiagnosticSet> {
    let name = required_name(names, &input.coordinate, ClientNameRole::InputObject)?;
    let definition = match definition(plan, &input.wire_name)? {
        TypeDefinition::InputObject(definition) => definition,
        _ => return Err(lost_definition(&input.coordinate, "input object")),
    };
    let attributes = public_attributes(
        &input.coordinate,
        definition.description.as_deref(),
        &format!("Owned GraphQL input object `{}`.", input.wire_name),
        None,
        None,
    )?;
    let mut source = format!(
        "{attributes}#[derive(Clone, Debug, dagger_sdk::__private::serde::Serialize)]\n\
         #[serde(crate = \"dagger_sdk::__private::serde\")]\n#[non_exhaustive]\npub struct {name} {{\n"
    );
    for field in input.fields.values() {
        let canonical = definition
            .fields
            .get(&field.wire_name)
            .ok_or_else(|| lost_definition(&field.coordinate, "input-object field"))?;
        let field_attributes = public_attributes(
            &field.coordinate,
            canonical.description.as_deref(),
            &format!(
                "GraphQL input field `{}.{}`.",
                input.wire_name, field.wire_name
            ),
            plan.directives.deprecation_reason(&field.coordinate),
            plan.directives.experimental_reason(&field.coordinate),
        )?;
        let type_name = rust_type(plan, names, &field.rust_type)?;
        let explicitly_nullable = field.presence.is_omittable()
            && canonical.type_use.nullable
            && !matches!(field.rust_type, RustType::Unit);
        let value_type = if explicitly_nullable {
            format!("Option<{type_name}>")
        } else {
            type_name.clone()
        };
        let carrier = if field.presence.is_omittable() {
            format!("Option<{value_type}>")
        } else {
            value_type
        };
        let skip = if field.presence.is_omittable() {
            ", skip_serializing_if = \"Option::is_none\""
        } else {
            ""
        };
        source.push_str(&format!(
            "{field_attributes}#[serde(rename = {}{skip})]\npub {}: {carrier},\n",
            rust_literal(field.wire_name.as_str())?,
            field.rust_name
        ));
    }
    source.push_str("}\n");
    let required = input
        .fields
        .values()
        .filter(|field| field.presence == ArgumentPresence::Required)
        .collect::<Vec<_>>();
    let parameters = required
        .iter()
        .map(|field| {
            rust_type(plan, names, &field.rust_type)
                .map(|value| format!("{}: {value}", field.rust_name))
        })
        .collect::<Result<Vec<_>, _>>()?
        .join(", ");
    source.push_str(&format!(
        "impl {name} {{\n/// Creates `{name}` with every required GraphQL input.\n#[must_use]\npub fn new({parameters}) -> Self {{ Self {{\n"
    ));
    for field in input.fields.values() {
        if field.presence == ArgumentPresence::Required {
            source.push_str(&format!("{},\n", field.rust_name));
        } else {
            source.push_str(&format!("{}: None,\n", field.rust_name));
        }
    }
    source.push_str("} }\n");
    for field in input
        .fields
        .values()
        .filter(|field| field.presence.is_omittable())
    {
        let type_name = rust_type(plan, names, &field.rust_type)?;
        let setter = field
            .setter_name
            .as_deref()
            .ok_or_else(|| lost_definition(&field.coordinate, "omittable input setter"))?;
        source.push_str(&format!(
            "/// Supplies GraphQL input `{}`; calling this method preserves explicit null and zero values.\n\
             #[must_use]\npub fn {setter}(mut self, value: {type_name}) -> Self {{ self.{} = Some({}); self }}\n",
            field.wire_name,
            field.rust_name,
            if definition
                .fields
                .get(&field.wire_name)
                .is_some_and(|canonical| canonical.type_use.nullable)
                && !matches!(field.rust_type, RustType::Unit)
            {
                "Some(value)"
            } else {
                "value"
            }
        ));
        if definition
            .fields
            .get(&field.wire_name)
            .is_some_and(|canonical| canonical.type_use.nullable)
            && !matches!(field.rust_type, RustType::Unit)
        {
            // The extra method makes explicit GraphQL null available without making
            // ordinary concrete values pay an `Option` ergonomics tax.
            let null_setter = format!("{setter}_null");
            source.push_str(&format!(
                "/// Supplies an explicit GraphQL null for input `{}` rather than omitting it.\n\
                 #[must_use]\npub fn {null_setter}(mut self) -> Self {{ self.{} = Some(None); self }}\n",
                field.wire_name, field.rust_name
            ));
        }
    }
    source.push_str("}\n");
    Ok(source)
}

fn render_fields(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    owner: &SchemaName,
) -> Result<(String, String), DiagnosticSet> {
    let mut support = String::new();
    let mut methods = String::new();
    for field in plan
        .module_fields
        .values()
        .filter(|field| &field.owner == owner)
    {
        render_field(plan, names, field, &mut support, &mut methods)?;
    }
    Ok((support, methods))
}

fn render_field(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    field: &FieldProjection,
    support: &mut String,
    methods: &mut String,
) -> Result<(), DiagnosticSet> {
    if matches!(field.strategy, FieldStrategy::TargetPrivate) {
        return Ok(());
    }
    let definition = field_definition(plan, field)?;
    let attributes = public_attributes(
        &field.coordinate,
        definition.description.as_deref(),
        &format!(
            "Selects GraphQL field `{}.{}`.",
            field.owner, field.wire_name
        ),
        field.deprecation.as_deref(),
        field.experimental.as_deref(),
    )?;
    let required = field
        .arguments
        .iter()
        .filter(|argument| argument.presence == ArgumentPresence::Required)
        .collect::<Vec<_>>();
    let parameters = required
        .iter()
        .map(|argument| parameter(plan, names, argument))
        .collect::<Result<Vec<_>, _>>()?;
    let parameters = if parameters.is_empty() {
        String::new()
    } else {
        format!(", {}", parameters.join(", "))
    };
    let query = render_query(field, &required, None)?;
    let body = render_execution(plan, names, field, &query)?;
    let (async_token, result) = method_result(plan, names, field)?;
    methods.push_str(&format!(
        "{attributes}#[must_use]\npub {async_token}fn {}(&self{parameters}) -> {result} {{\n{body}}}\n",
        field.rust_name
    ));

    let omittable = field
        .arguments
        .iter()
        .filter(|argument| argument.presence.is_omittable())
        .collect::<Vec<_>>();
    if omittable.is_empty() {
        return Ok(());
    }
    let options_name = required_name(names, &field.coordinate, ClientNameRole::Options)?;
    let options_method = field
        .options_method_name
        .as_deref()
        .ok_or_else(|| lost_definition(&field.coordinate, "field options method"))?;
    let options_attributes = public_attributes(
        &field.coordinate,
        None,
        &format!(
            "Owned optional arguments for GraphQL operation `{}.{}`.",
            field.owner, field.wire_name
        ),
        None,
        None,
    )?;
    support.push_str(&format!(
        "{options_attributes}#[derive(Clone, Debug, Default)]\n#[non_exhaustive]\npub struct {options_name} {{\n"
    ));
    for argument in &omittable {
        let type_name = rust_type(plan, names, &argument.rust_type)?;
        let canonical = argument_definition(plan, field, &argument.wire_name)?;
        let explicitly_nullable =
            canonical.type_use.nullable && !matches!(argument.rust_type, RustType::Unit);
        let carrier = if explicitly_nullable {
            format!("Option<Option<{type_name}>>")
        } else {
            format!("Option<{type_name}>")
        };
        support.push_str(&format!("{}: {carrier},\n", argument.rust_name));
    }
    support.push_str(&format!("}}\nimpl {options_name} {{\n"));
    for argument in &omittable {
        let type_name = rust_type(plan, names, &argument.rust_type)?;
        let canonical = argument_definition(plan, field, &argument.wire_name)?;
        let explicitly_nullable =
            canonical.type_use.nullable && !matches!(argument.rust_type, RustType::Unit);
        support.push_str(&format!(
            "/// Supplies GraphQL argument `{}` while retaining explicit null and zero values.\n\
             #[must_use]\npub fn with_{}(mut self, value: {type_name}) -> Self {{ self.{} = Some({}); self }}\n",
            argument.wire_name,
            argument.rust_name,
            argument.rust_name,
            if explicitly_nullable {
                "Some(value)"
            } else {
                "value"
            }
        ));
        if explicitly_nullable {
            support.push_str(&format!(
                "/// Supplies an explicit GraphQL null for argument `{}` rather than omitting it.\n\
                 #[must_use]\npub fn with_{}_null(mut self) -> Self {{ self.{} = Some(None); self }}\n",
                argument.wire_name, argument.rust_name, argument.rust_name
            ));
        }
    }
    support.push_str("}\n");
    let query = render_query(field, &required, Some((&omittable, options_name)))?;
    let body = render_execution(plan, names, field, &query)?;
    methods.push_str(&format!(
        "{attributes}#[must_use]\npub {async_token}fn {options_method}(&self{parameters}, opts: {options_name}) -> {result} {{\n{body}}}\n"
    ));
    Ok(())
}

fn parameter(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    argument: &crate::projection::fields::ArgumentProjection,
) -> Result<String, DiagnosticSet> {
    let value = rust_type(plan, names, &argument.rust_type)?;
    if argument.encoder.contains_lazy_id() {
        Ok(format!("{}: impl Into<{value}>", argument.rust_name))
    } else {
        Ok(format!("{}: {value}", argument.rust_name))
    }
}

fn render_query(
    field: &FieldProjection,
    required: &[&crate::projection::fields::ArgumentProjection],
    options: Option<(&[&crate::projection::fields::ArgumentProjection], &str)>,
) -> Result<String, DiagnosticSet> {
    let mut source = format!(
        "let query = self.query.select({});\n",
        rust_literal(field.wire_name.as_str())?
    );
    for argument in required {
        let value = if argument.encoder.contains_lazy_id() {
            format!("{}.into()", argument.rust_name)
        } else {
            argument.rust_name.clone()
        };
        let operation = if argument.encoder.contains_lazy_id() {
            "generated_argument_id"
        } else {
            "argument"
        };
        source.push_str(&format!(
            "let query = query.{operation}({}, {value});\n",
            rust_literal(argument.wire_name.as_str())?
        ));
    }
    if let Some((arguments, options_name)) = options {
        let _ = options_name;
        for argument in arguments {
            let operation = if argument.encoder.contains_lazy_id() {
                "generated_argument_id"
            } else {
                "argument"
            };
            source.push_str(&format!(
                "let query = if let Some(value) = opts.{} {{ query.{operation}({}, value) }} else {{ query }};\n",
                argument.rust_name,
                rust_literal(argument.wire_name.as_str())?
            ));
        }
    }
    Ok(source)
}

fn method_result(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    field: &FieldProjection,
) -> Result<(&'static str, String), DiagnosticSet> {
    let output = rust_type(plan, names, &field.return_type)?;
    if matches!(field.strategy, FieldStrategy::LazyHandle { .. }) {
        Ok(("", output))
    } else {
        Ok((
            "async ",
            format!("Result<{output}, dagger_sdk::QueryError>"),
        ))
    }
}

fn render_execution(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    field: &FieldProjection,
    query: &str,
) -> Result<String, DiagnosticSet> {
    let mut body = query.to_owned();
    match &field.strategy {
        FieldStrategy::LazyHandle { target } => {
            let target_type = named_handle(plan, names, target)?;
            if plan.module_types.contains_key(target) {
                body.push_str(&format!("{target_type}::from_query(query)\n"));
            } else {
                body.push_str(&format!("query.generated_core_handle::<{target_type}>()\n"));
            }
        }
        FieldStrategy::ExecuteValue { .. } => body.push_str("query.execute().await\n"),
        FieldStrategy::NullableHandle { target, .. }
        | FieldStrategy::ReenterList { target, .. } => {
            let output = rust_type(plan, names, &field.return_type)?;
            body.push_str(&format!(
                "query.generated_reenter_shape::<{output}>({}).await\n",
                rust_literal(target.as_str())?
            ));
        }
        FieldStrategy::ExpectedTypeSelf { parent, .. } => {
            let output = rust_type(plan, names, &field.return_type)?;
            body.push_str(&format!(
                "query.generated_reenter_shape::<{output}>({}).await\n",
                rust_literal(parent.as_str())?
            ));
        }
        FieldStrategy::TargetPrivate => {
            return Err(render_error(
                DiagnosticCode::SchemaFieldUnmapped,
                field.coordinate.as_str(),
                "target-private field reached the standalone-client renderer",
            ));
        }
    }
    Ok(body)
}

fn rust_type(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    ty: &RustType,
) -> Result<String, DiagnosticSet> {
    Ok(match ty {
        RustType::Bool => "bool".to_owned(),
        RustType::F64 => "f64".to_owned(),
        RustType::I64 => "i64".to_owned(),
        RustType::String => "String".to_owned(),
        RustType::Id => "dagger_sdk::Id".to_owned(),
        RustType::Json => "dagger_sdk::Json".to_owned(),
        RustType::Platform => "dagger_sdk::Platform".to_owned(),
        RustType::Unit => "()".to_owned(),
        RustType::Enum(name) => {
            named_type(plan, names, name, ClientNameRole::Enum, BindingKind::Enum)?
        }
        RustType::Input(name) => named_type(
            plan,
            names,
            name,
            ClientNameRole::InputObject,
            BindingKind::InputObject,
        )?,
        RustType::CustomScalar(name) => named_type(
            plan,
            names,
            name,
            ClientNameRole::CustomScalar,
            BindingKind::Scalar,
        )?,
        RustType::Handle(name) => named_type(
            plan,
            names,
            name,
            ClientNameRole::Object,
            BindingKind::ObjectHandle,
        )?,
        RustType::InterfaceHandle(name) => named_type(
            plan,
            names,
            name,
            ClientNameRole::InterfaceClient,
            BindingKind::InterfaceClient,
        )?,
        RustType::IdInput(name) => {
            format!("dagger_sdk::IdInput<{}>", named_handle(plan, names, name)?)
        }
        RustType::Option(inner) => format!("Option<{}>", rust_type(plan, names, inner)?),
        RustType::Vec(inner) => format!("Vec<{}>", rust_type(plan, names, inner)?),
    })
}

fn named_handle(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    name: &SchemaName,
) -> Result<String, DiagnosticSet> {
    match plan.module_types.get(name) {
        Some(TypeProjection::Object(_)) => named_type(
            plan,
            names,
            name,
            ClientNameRole::Object,
            BindingKind::ObjectHandle,
        ),
        Some(TypeProjection::Interface(_)) => named_type(
            plan,
            names,
            name,
            ClientNameRole::InterfaceClient,
            BindingKind::InterfaceClient,
        ),
        _ => core_path(plan, name, BindingKind::ObjectHandle)
            .or_else(|_| core_path(plan, name, BindingKind::InterfaceClient)),
    }
}

fn named_type(
    plan: &ClientBindingPlan,
    names: &ClientNamePlan,
    name: &SchemaName,
    role: ClientNameRole,
    core_kind: BindingKind,
) -> Result<String, DiagnosticSet> {
    if let Some(projection) = plan.module_types.get(name) {
        let coordinate = match projection {
            TypeProjection::Scalar(scalar) => &scalar.coordinate,
            TypeProjection::Object(object) => &object.coordinate,
            TypeProjection::Interface(interface) => &interface.coordinate,
            TypeProjection::Enum(enumeration) => &enumeration.coordinate,
            TypeProjection::InputObject(input) => &input.coordinate,
            TypeProjection::TargetPrivate(private) => &private.coordinate,
        };
        return required_name(names, coordinate, role).map(|name| format!("super::{name}"));
    }
    core_path(plan, name, core_kind)
}

fn core_path(
    plan: &ClientBindingPlan,
    name: &SchemaName,
    kind: BindingKind,
) -> Result<String, DiagnosticSet> {
    let coordinate = SchemaCoordinate::named_type(name);
    plan.core_bindings
        .values()
        .find(|reference| {
            reference.key.wire_coordinate.as_ref() == Some(&coordinate)
                && reference.key.binding_kind == kind
        })
        .and_then(|reference| reference.public_path.clone())
        .ok_or_else(|| {
            render_error(
                DiagnosticCode::CapabilityBindingMissing,
                coordinate.as_str(),
                "typed client projection has no matching public Core SDK path",
            )
        })
}

fn render_into_id(name: &str) -> String {
    format!(
        "impl dagger_sdk::IntoID<dagger_sdk::Id> for {name} {{\n\
         fn into_id(self) -> core::pin::Pin<Box<dyn core::future::Future<Output = Result<dagger_sdk::Id, dagger_sdk::QueryError>> + Send>> {{\n\
         Box::pin(async move {{ self.id().await }})\n\
         }}\n\
         }}\n\
         impl From<{name}> for dagger_sdk::IdInput<{name}> {{\n\
         fn from(value: {name}) -> Self {{ dagger_sdk::IdInput::generated_lazy(value) }}\n\
         }}\n"
    )
}

fn render_quickstart(plan: &ClientBindingPlan) -> Result<String, DiagnosticSet> {
    let crate_name = plan.project.crate_name.as_str();
    let mut source = format!(
        "//! Minimal lifecycle-safe use of the generated standalone client.\n\
         use {crate_name}::dagger_client;\n"
    );
    if let (ClientSchemaSurface::BoundModule(surface), Some(_)) = (&plan.surface, &plan.names) {
        let method = crate::naming::rust_name(
            &surface.root.field_wire_name,
            crate::naming::NameContext::Method,
        )
        .identifier;
        source.push_str(&format!(
            "use {crate_name}::dagger_client::prelude::*;\n\
             #[tokio::main]\nasync fn main() -> Result<(), Box<dyn std::error::Error>> {{\n\
             let client = dagger_client::connect().await?;\n\
             let module = client.{method}();\n\
             let _selection = module.selection();\n\
             client.close().await?;\n\
             Ok(())\n\
             }}\n"
        ));
    } else {
        source.push_str(
            "#[tokio::main]\nasync fn main() -> Result<(), Box<dyn std::error::Error>> {\n\
             let client = dagger_client::connect().await?;\n\
             let _query = client.query_builder();\n\
             client.close().await?;\n\
             Ok(())\n\
             }\n",
        );
    }
    checked_source("examples/dagger-client-quickstart.rs", source)
}

fn header(plan: &ClientBindingPlan, module_doc: &str) -> Result<String, DiagnosticSet> {
    let provenance = serde_json::to_string(&serde_json::json!({
        "format": "dagger-rust-standalone-client-v1",
        "ownership": "dagger-codegen",
        "schema_digest": plan.visible_schema_digest.as_str(),
        "target_revision": plan.target.dagger_revision().as_str(),
    }))
    .map_err(|_| {
        render_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "generated-source",
            "standalone-client provenance could not be encoded",
        )
    })?;
    Ok(format!("//! {module_doc}\n// @generated {provenance}\n"))
}

fn definition<'a>(
    plan: &'a ClientBindingPlan,
    name: &SchemaName,
) -> Result<&'a TypeDefinition, DiagnosticSet> {
    plan.schema.types().get(name).ok_or_else(|| {
        render_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            name.as_str(),
            "module type is absent from the canonical client schema",
        )
    })
}

fn field_definition<'a>(
    plan: &'a ClientBindingPlan,
    field: &FieldProjection,
) -> Result<&'a crate::schema::canonical::FieldDefinition, DiagnosticSet> {
    plan.schema
        .types()
        .get(&field.owner)
        .and_then(|definition| match definition {
            TypeDefinition::Object(definition) => definition.fields.get(&field.wire_name),
            TypeDefinition::Interface(definition) => definition.fields.get(&field.wire_name),
            _ => None,
        })
        .ok_or_else(|| lost_definition(&field.coordinate, "field"))
}

fn argument_definition<'a>(
    plan: &'a ClientBindingPlan,
    field: &FieldProjection,
    argument: &SchemaName,
) -> Result<&'a crate::schema::canonical::ArgumentDefinition, DiagnosticSet> {
    field_definition(plan, field)?
        .arguments
        .get(argument)
        .ok_or_else(|| lost_definition(&field.coordinate, "field argument"))
}

fn public_attributes(
    coordinate: &SchemaCoordinate,
    description: Option<&str>,
    fallback: &str,
    deprecation: Option<&str>,
    experimental: Option<&str>,
) -> Result<String, DiagnosticSet> {
    let mut documentation = crate::render::docs::documentation(coordinate, description, fallback)
        .map_err(DiagnosticSet::one)?;
    if let Some(reason) = deprecation {
        let reason =
            crate::render::docs::sanitize(coordinate, reason).map_err(DiagnosticSet::one)?;
        documentation.push_str(&format!("\n\n**Deprecated:** {reason}"));
    }
    if let Some(reason) = experimental {
        let reason =
            crate::render::docs::sanitize(coordinate, reason).map_err(DiagnosticSet::one)?;
        documentation.push_str(&format!("\n\n**Experimental:** {reason}"));
    }
    let documentation = rust_literal(&documentation)?;
    let deprecated = deprecation
        .map(|reason| rust_literal(&reason.replace(['\r', '\n'], " ")))
        .transpose()?
        .map(|reason| format!("#[deprecated(note = {reason})]\n"))
        .unwrap_or_default();
    Ok(format!("#[doc = {documentation}]\n{deprecated}"))
}

fn required_name<'a>(
    names: &'a ClientNamePlan,
    coordinate: &SchemaCoordinate,
    role: ClientNameRole,
) -> Result<&'a str, DiagnosticSet> {
    names
        .get(coordinate, role)
        .map(|name| name.as_str())
        .ok_or_else(|| {
            render_error(
                DiagnosticCode::RustNameInvalid,
                coordinate.as_str(),
                "client name plan is missing a required public identifier",
            )
        })
}

fn file_identifier(identifier: &str) -> String {
    identifier.trim_start_matches("r#").to_case(Case::Snake)
}

fn module_identifier(file: &str) -> String {
    file.strip_suffix(".rs").unwrap_or(file).to_owned()
}

fn rust_literal(value: &str) -> Result<String, DiagnosticSet> {
    serde_json::to_string(value).map_err(|_| {
        render_error(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "generated-source",
            "generated Rust literal could not be encoded",
        )
    })
}

fn checked_source(coordinate: &str, source: String) -> Result<String, DiagnosticSet> {
    syn::parse_file(&source).map_err(|_| {
        render_error(
            DiagnosticCode::GeneratedFormatFailed,
            coordinate,
            "rendered standalone-client source is not valid Rust syntax",
        )
    })?;
    Ok(source)
}

fn insert_source(
    artifacts: &mut BTreeMap<RelativeOperationPath, CandidateArtifact>,
    path: RelativeOperationPath,
    source: String,
) -> Result<(), DiagnosticSet> {
    let source = checked_source(path.as_str(), source)?;
    insert(
        artifacts,
        path,
        CandidateArtifactKind::RustSource,
        source.into_bytes(),
    )
}

fn insert(
    artifacts: &mut BTreeMap<RelativeOperationPath, CandidateArtifact>,
    path: RelativeOperationPath,
    kind: CandidateArtifactKind,
    content: Vec<u8>,
) -> Result<(), DiagnosticSet> {
    if artifacts
        .insert(path.clone(), CandidateArtifact { kind, content })
        .is_some()
    {
        return Err(render_error(
            DiagnosticCode::OperationArtifactCollision,
            path.as_str(),
            "standalone-client renderer emitted the same artifact path more than once",
        ));
    }
    Ok(())
}

fn lost_definition(coordinate: &SchemaCoordinate, kind: &str) -> DiagnosticSet {
    render_error(
        DiagnosticCode::GeneratedProvenanceInvalid,
        coordinate.as_str(),
        &format!("{kind} projection lost its canonical semantic record"),
    )
}

fn render_error(code: DiagnosticCode, coordinate: &str, message: &str) -> DiagnosticSet {
    DiagnosticSet::one(Diagnostic::new(
        code,
        Some(DiagnosticCoordinate::new(coordinate)),
        message,
    ))
}
