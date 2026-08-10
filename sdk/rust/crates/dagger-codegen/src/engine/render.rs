//! Shared deterministic source rendering for operation-scoped extension bindings.

use std::collections::{BTreeMap, BTreeSet};

use crate::diagnostic::{DiagnosticCode, DiagnosticSet};
use crate::projection::fields::{ArgumentPresence, FieldProjection};
use crate::projection::types::{
    EnumProjection, InputObjectProjection, InterfaceProjection, ObjectProjection, TypeProjection,
};
use crate::schema::canonical::{SchemaCoordinate, SchemaName, TypeDefinition};

use super::model::{
    CandidateArtifact, CandidateArtifactKind, RelativeOperationPath, operation_diagnostic,
};
use super::visible::VisibleSchemaPlan;

pub(crate) fn visible_binding_artifacts(
    plan: &VisibleSchemaPlan,
    root: &RelativeOperationPath,
) -> Result<BTreeMap<RelativeOperationPath, CandidateArtifact>, DiagnosticSet> {
    let mut artifacts = BTreeMap::new();
    let mut modules = Vec::new();
    for projection in plan.projection().named_types().values() {
        let Some((wire_name, module_name, source)) = render_extension_type(plan, projection)?
        else {
            continue;
        };
        let file_stem = module_name.strip_prefix("r#").unwrap_or(&module_name);
        let file_name = format!("{file_stem}.rs");
        let path = root.join(&file_name)?;
        insert(
            &mut artifacts,
            path,
            CandidateArtifactKind::RustSource,
            source,
        )?;
        modules.push((module_name, file_name, wire_name));
    }
    modules.sort();
    let mut index = generated_header(
        plan,
        "Operation-scoped visible-schema bindings over the public Dagger SDK.",
    )?;
    index.push_str("pub use dagger_sdk::*;\n");
    for (module, file, _) in &modules {
        index.push_str(&format!(
            "#[path = {file:?}]\nmod {module};\npub use {module}::*;\n"
        ));
    }
    let index = checked_source(root.as_str(), index)?;
    insert(
        &mut artifacts,
        root.join("mod.rs")?,
        CandidateArtifactKind::RustSource,
        index,
    )?;
    // JSON object keys cannot represent the catalog's structured BindingKey. Encoding
    // the already ordered values retains every key inside its descriptor without a
    // lossy string-key projection.
    let catalog = serde_json::to_vec(
        &plan
            .projection()
            .catalog()
            .bindings()
            .values()
            .collect::<Vec<_>>(),
    )
    .map_err(|_| {
        DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "binding-catalog.json",
            "visible-schema semantic catalog could not be encoded",
        ))
    })?;
    insert(
        &mut artifacts,
        root.join("binding-catalog.json")?,
        CandidateArtifactKind::ControlManifest,
        catalog,
    )?;
    Ok(artifacts)
}

pub(crate) fn rust_source_paths(
    artifacts: &BTreeMap<RelativeOperationPath, CandidateArtifact>,
) -> BTreeSet<RelativeOperationPath> {
    artifacts
        .iter()
        .filter(|(_, artifact)| artifact.kind == CandidateArtifactKind::RustSource)
        .map(|(path, _)| path.clone())
        .collect()
}

pub(crate) fn checked_source(coordinate: &str, source: String) -> Result<Vec<u8>, DiagnosticSet> {
    syn::parse_file(&source).map_err(|_| {
        DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::GeneratedFormatFailed,
            coordinate,
            "rendered operation source is not valid Rust syntax",
        ))
    })?;
    Ok(source.into_bytes())
}

pub(crate) fn generated_header(
    plan: &VisibleSchemaPlan,
    module_doc: &str,
) -> Result<String, DiagnosticSet> {
    let provenance = serde_json::to_string(&serde_json::json!({
        "format": "dagger-rust-visible-v1",
        "ownership": "dagger-codegen",
        "schema_digest": plan.digest().as_str(),
        "target_revision": plan.projection().target().dagger_revision().as_str(),
    }))
    .map_err(|_| {
        DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::GeneratedProvenanceInvalid,
            "generated-source",
            "visible-schema provenance could not be encoded",
        ))
    })?;
    Ok(format!("//! {module_doc}\n// @generated {provenance}\n"))
}

fn render_extension_type(
    plan: &VisibleSchemaPlan,
    projection: &TypeProjection,
) -> Result<Option<(String, String, Vec<u8>)>, DiagnosticSet> {
    let (wire_name, module_name, body) = match projection {
        TypeProjection::Object(object) if plan.is_extension_type(&object.wire_name) => (
            object.wire_name.to_string(),
            object.module_name.clone(),
            render_object(plan, object)?,
        ),
        TypeProjection::Interface(interface) if plan.is_extension_type(&interface.wire_name) => (
            interface.wire_name.to_string(),
            interface.module_name.clone(),
            render_interface(plan, interface)?,
        ),
        TypeProjection::Enum(enumeration) if plan.is_extension_type(&enumeration.wire_name) => (
            enumeration.wire_name.to_string(),
            plan.projection()
                .names()
                .get(&enumeration.coordinate, crate::naming::NameContext::Module)
                .map(|name| name.identifier.clone())
                .ok_or_else(|| {
                    DiagnosticSet::one(operation_diagnostic(
                        DiagnosticCode::RustNameInvalid,
                        enumeration.coordinate.as_str(),
                        "extension enum has no projected module name",
                    ))
                })?,
            render_enum(plan, enumeration)?,
        ),
        TypeProjection::InputObject(input) if plan.is_extension_type(&input.wire_name) => (
            input.wire_name.to_string(),
            input.module_name.clone(),
            render_input(plan, input)?,
        ),
        _ => return Ok(None),
    };
    let mut source = generated_header(
        plan,
        &format!("Bindings owned by visible GraphQL type `{wire_name}`."),
    )?;
    source.push_str(&body);
    let source = checked_source(&wire_name, source)?;
    Ok(Some((wire_name, module_name, source)))
}

fn render_object(
    plan: &VisibleSchemaPlan,
    object: &ObjectProjection,
) -> Result<String, DiagnosticSet> {
    let definition = match plan.canonical().types().get(&object.wire_name) {
        Some(TypeDefinition::Object(definition)) => definition,
        _ => return Err(lost_definition(&object.coordinate, "object")),
    };
    let attributes = public_attributes(
        &object.coordinate,
        definition.description.as_deref(),
        &format!(
            "Lazy visible-schema handle for GraphQL object `{}`.",
            object.wire_name
        ),
        None,
        None,
    )?;
    let (support, methods) = render_fields(plan, &object.wire_name)?;
    let mut source = format!(
        "{attributes}#[derive(Clone)]\npub struct {} {{ query: dagger_sdk::QueryBuilder }}\n{support}impl {} {{\n#[doc(hidden)]\npub fn from_query(query: dagger_sdk::QueryBuilder) -> Self {{ Self {{ query }} }}\n/// Borrows the immutable query represented by this visible-schema handle.\n#[must_use]\npub fn selection(&self) -> &dagger_sdk::QueryBuilder {{ &self.query }}\n{methods}",
        object.rust_name, object.rust_name,
    );
    source.push_str("}\n");
    Ok(source)
}

fn render_interface(
    plan: &VisibleSchemaPlan,
    interface: &InterfaceProjection,
) -> Result<String, DiagnosticSet> {
    let definition = match plan.canonical().types().get(&interface.wire_name) {
        Some(TypeDefinition::Interface(definition)) => definition,
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
            "Lazy visible-schema client for GraphQL interface `{}`.",
            interface.wire_name
        ),
        None,
        None,
    )?;
    let (support, methods) = render_fields(plan, &interface.wire_name)?;
    let mut source = format!(
        "{trait_attributes}pub trait {} {{\n/// Borrows the immutable query represented by this interface handle.\nfn selection(&self) -> &dagger_sdk::QueryBuilder;\n}}\n{client_attributes}#[derive(Clone)]\npub struct {} {{ query: dagger_sdk::QueryBuilder }}\n{support}impl {} {{\n#[doc(hidden)]\npub fn from_query(query: dagger_sdk::QueryBuilder) -> Self {{ Self {{ query }} }}\n{methods}",
        interface.trait_name, interface.client_name, interface.client_name,
    );
    source.push_str("}\n");
    source.push_str(&format!(
        "impl {} for {} {{ fn selection(&self) -> &dagger_sdk::QueryBuilder {{ &self.query }} }}\n",
        interface.trait_name, interface.client_name
    ));
    Ok(source)
}

fn render_fields(
    plan: &VisibleSchemaPlan,
    owner: &SchemaName,
) -> Result<(String, String), DiagnosticSet> {
    let mut support = String::new();
    let mut methods = String::new();
    for field in plan
        .projection()
        .fields()
        .values()
        .filter(|field| &field.owner == owner)
    {
        if matches!(
            field.strategy,
            crate::projection::fields::FieldStrategy::TargetPrivate
        ) {
            continue;
        }
        render_field(plan, field, &mut support, &mut methods)?;
    }
    Ok((support, methods))
}

fn render_field(
    plan: &VisibleSchemaPlan,
    field: &FieldProjection,
    support: &mut String,
    methods: &mut String,
) -> Result<(), DiagnosticSet> {
    let canonical = plan
        .canonical()
        .types()
        .get(&field.owner)
        .and_then(|definition| match definition {
            TypeDefinition::Object(definition) => definition.fields.get(&field.wire_name),
            TypeDefinition::Interface(definition) => definition.fields.get(&field.wire_name),
            _ => None,
        })
        .ok_or_else(|| lost_definition(&field.coordinate, "field"))?;
    let attributes = public_attributes(
        &field.coordinate,
        canonical.description.as_deref(),
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
        .map(|argument| format!(", {}: dagger_sdk::Json", argument.rust_name))
        .collect::<String>();
    methods.push_str(&format!(
        "{attributes}#[must_use]\npub fn {}(&self{}) -> dagger_sdk::QueryBuilder {{\nlet query = self.query.select({:?});\n",
        field.rust_name,
        parameters,
        field.wire_name.as_str()
    ));
    for argument in &required {
        methods.push_str(&format!(
            "let query = query.argument({:?}, {});\n",
            argument.wire_name.as_str(),
            argument.rust_name
        ));
    }
    methods.push_str("query\n}\n");
    let omittable = field
        .arguments
        .iter()
        .filter(|argument| argument.presence.is_omittable())
        .collect::<Vec<_>>();
    let (Some(options_name), Some(options_method)) = (
        field.options_type_name.as_deref(),
        field.options_method_name.as_deref(),
    ) else {
        return Ok(());
    };
    let options_attributes = public_attributes(
        &field.coordinate,
        None,
        &format!(
            "Owned optional arguments for GraphQL operation `{}.{}`; `None` preserves omission.",
            field.owner, field.wire_name
        ),
        None,
        None,
    )?;
    support.push_str(&format!(
        "{options_attributes}#[derive(Clone, Debug, Default)]\n#[non_exhaustive]\npub struct {options_name} {{\n"
    ));
    for argument in &omittable {
        let definition = canonical
            .arguments
            .get(&argument.wire_name)
            .ok_or_else(|| lost_definition(&argument.coordinate, "argument"))?;
        let omission = match &argument.presence {
            ArgumentPresence::Required => "This argument is required.".to_owned(),
            ArgumentPresence::Omittable { engine_default } => match engine_default {
                Some(default) => format!(
                    "`None` omits GraphQL Wire_Name `{}` and preserves engine default `{default:?}`.",
                    argument.wire_name
                ),
                None => format!("`None` omits GraphQL Wire_Name `{}`.", argument.wire_name),
            },
        };
        let argument_attributes = public_attributes(
            &argument.coordinate,
            definition.description.as_deref(),
            &omission,
            argument.deprecation.as_deref(),
            argument.experimental.as_deref(),
        )?;
        support.push_str(&format!(
            "{argument_attributes}pub {}: Option<dagger_sdk::Json>,\n",
            argument.rust_name
        ));
    }
    support.push_str("}\n");
    methods.push_str(&format!(
        "{attributes}#[must_use]\npub fn {options_method}(&self{parameters}, opts: &{options_name}) -> dagger_sdk::QueryBuilder {{\nlet query = self.query.select({:?});\n",
        field.wire_name.as_str()
    ));
    for argument in &required {
        methods.push_str(&format!(
            "let query = query.argument({:?}, {});\n",
            argument.wire_name.as_str(),
            argument.rust_name
        ));
    }
    for argument in omittable {
        methods.push_str(&format!(
            "let query = if let Some(value) = &opts.{} {{ query.argument({:?}, value) }} else {{ query }};\n",
            argument.rust_name,
            argument.wire_name.as_str()
        ));
    }
    methods.push_str("query\n}\n");
    Ok(())
}

fn render_enum(
    plan: &VisibleSchemaPlan,
    enumeration: &EnumProjection,
) -> Result<String, DiagnosticSet> {
    let definition = match plan.canonical().types().get(&enumeration.wire_name) {
        Some(TypeDefinition::Enum(definition)) => definition,
        _ => return Err(lost_definition(&enumeration.coordinate, "enum")),
    };
    let attributes = public_attributes(
        &enumeration.coordinate,
        definition.description.as_deref(),
        &format!("Generated enum for GraphQL `{}`.", enumeration.wire_name),
        None,
        None,
    )?;
    let mut source = format!(
        "{attributes}#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]\npub enum {} {{\n",
        enumeration.rust_name
    );
    for variant in enumeration.variants.values() {
        let attributes = public_attributes(
            &variant.coordinate,
            variant.description.as_deref(),
            &format!("GraphQL enum value `{}`.", variant.wire_name),
            variant.deprecation.as_deref(),
            variant.experimental.as_deref(),
        )?;
        source.push_str(&format!("{attributes}{},\n", variant.rust_name));
    }
    source.push_str("}\n");
    Ok(source)
}

fn render_input(
    plan: &VisibleSchemaPlan,
    input: &InputObjectProjection,
) -> Result<String, DiagnosticSet> {
    let definition = match plan.canonical().types().get(&input.wire_name) {
        Some(TypeDefinition::InputObject(definition)) => definition,
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
        "{attributes}#[derive(Clone, Debug)]\n#[non_exhaustive]\npub struct {} {{\n",
        input.rust_name
    );
    for field in input.fields.values() {
        let canonical = definition
            .fields
            .get(&field.wire_name)
            .ok_or_else(|| lost_definition(&field.coordinate, "input field"))?;
        let carrier = if field.presence.is_omittable() {
            "Option<dagger_sdk::Json>"
        } else {
            "dagger_sdk::Json"
        };
        let fallback = match &field.presence {
            ArgumentPresence::Required => {
                format!("Required GraphQL input field `{}`.", field.wire_name)
            }
            ArgumentPresence::Omittable { engine_default } => match engine_default {
                Some(default) => format!(
                    "Optional GraphQL input field `{}`; omission preserves engine default `{default:?}`.",
                    field.wire_name
                ),
                None => format!(
                    "Optional GraphQL input field `{}`; `None` omits its Wire_Name.",
                    field.wire_name
                ),
            },
        };
        let attributes = public_attributes(
            &field.coordinate,
            canonical.description.as_deref(),
            &fallback,
            canonical
                .deprecation
                .as_ref()
                .and_then(|deprecation| deprecation.reason.as_deref()),
            plan.projection()
                .directives()
                .experimental_reason(&field.coordinate),
        )?;
        source.push_str(&format!(
            "{attributes}pub {}: {carrier},\n",
            field.rust_name
        ));
    }
    source.push_str("}\n");
    Ok(source)
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
    let documentation = serde_json::to_string(&documentation).map_err(|_| {
        DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::GeneratedDocumentationInvalid,
            coordinate.as_str(),
            "generated documentation could not be encoded as a Rust literal",
        ))
    })?;
    let deprecated = deprecation
        .map(|reason| {
            serde_json::to_string(&reason.replace(['\r', '\n'], " "))
                .map(|note| format!("#[deprecated(note = {note})]\n"))
        })
        .transpose()
        .map_err(|_| {
            DiagnosticSet::one(operation_diagnostic(
                DiagnosticCode::GeneratedDocumentationInvalid,
                coordinate.as_str(),
                "generated deprecation note could not be encoded as a Rust literal",
            ))
        })?
        .unwrap_or_default();
    Ok(format!("#[doc = {documentation}]\n{deprecated}"))
}

fn lost_definition(coordinate: &SchemaCoordinate, kind: &str) -> DiagnosticSet {
    DiagnosticSet::one(operation_diagnostic(
        DiagnosticCode::GeneratedProvenanceInvalid,
        coordinate.as_str(),
        &format!("{kind} projection lost its canonical definition"),
    ))
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
        return Err(DiagnosticSet::one(operation_diagnostic(
            DiagnosticCode::OperationArtifactCollision,
            path.as_str(),
            "visible binding renderer emitted a duplicate path",
        )));
    }
    Ok(())
}
