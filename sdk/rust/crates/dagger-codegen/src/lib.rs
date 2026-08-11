#![deny(warnings)]
// Compiler diagnostics intentionally retain owned typed coordinates and safe source
// chains. Boxing each single-phase error would add allocation and API noise without
// reducing the aggregate diagnostic set retained at the public boundary.
#![allow(clippy::result_large_err)]
//! Pure schema-to-Rust code generation for the Dagger SDK.
//!
//! This crate accepts data and returns validated candidate source. It deliberately has
//! no filesystem, process, network, engine-session, or completeness-ledger authority.

pub mod diagnostic;
pub mod directive;
pub mod engine;
pub mod module;
pub mod naming;
pub mod projection;
pub mod render;
pub mod rust;
pub mod schema;
pub mod target;

use std::collections::BTreeMap;

use diagnostic::CodegenError;
use diagnostic::DiagnosticSet;
#[cfg(test)]
use diagnostic::{Diagnostic, DiagnosticCode, DiagnosticCoordinate};
use directive::DirectiveProjection;
use naming::RustNameMap;
use projection::catalog::ProjectionCatalog;
use projection::fields::FieldProjection;
use projection::types::{InterfaceImplementationProjection, TypeProjection};
use render::verification::GeneratedVerification;
use rust::RustGenerator;
use schema::canonical::CanonicalSchema;
use schema::raw::Schema;
use target::CodegenTarget;

/// Borrowed inputs for exact-target core schema projection.
pub struct CoreProjectionRequest<'a> {
    /// Target identity decoded exclusively from the checked target descriptor.
    pub target: &'a CodegenTarget,
    /// Complete checked introspection snapshot bytes.
    pub schema_json: &'a [u8],
}

/// An immutable schema plan that is safe to pass to semantic projection and rendering.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ProjectionPlan {
    target: CodegenTarget,
    schema: CanonicalSchema,
    names: RustNameMap,
    named_types: BTreeMap<schema::canonical::SchemaName, TypeProjection>,
    fields: BTreeMap<schema::canonical::SchemaCoordinate, FieldProjection>,
    directives: DirectiveProjection,
    implementations: Vec<InterfaceImplementationProjection>,
    catalog: ProjectionCatalog,
}

impl ProjectionPlan {
    /// Returns the exact target identity bound to this plan.
    #[must_use]
    pub const fn target(&self) -> &CodegenTarget {
        &self.target
    }

    /// Returns the complete validated canonical schema.
    #[must_use]
    pub const fn schema(&self) -> &CanonicalSchema {
        &self.schema
    }

    /// Returns the complete collision-checked Rust name map.
    #[must_use]
    pub const fn names(&self) -> &RustNameMap {
        &self.names
    }

    /// Returns one total projection for every public named type.
    #[must_use]
    pub const fn named_types(&self) -> &BTreeMap<schema::canonical::SchemaName, TypeProjection> {
        &self.named_types
    }

    /// Returns one total operation projection for every public field.
    #[must_use]
    pub const fn fields(&self) -> &BTreeMap<schema::canonical::SchemaCoordinate, FieldProjection> {
        &self.fields
    }

    /// Returns all registered directive definitions and active applications.
    #[must_use]
    pub const fn directives(&self) -> &DirectiveProjection {
        &self.directives
    }

    /// Returns every declared object/interface implementation edge.
    #[must_use]
    pub fn implementations(&self) -> &[InterfaceImplementationProjection] {
        &self.implementations
    }

    /// Returns the exhaustive semantic binding catalog.
    #[must_use]
    pub const fn catalog(&self) -> &ProjectionCatalog {
        &self.catalog
    }
}

/// Deterministic in-memory artifacts produced from a complete projection plan.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RenderedCandidate {
    artifacts: BTreeMap<String, Vec<u8>>,
    verification: GeneratedVerification,
}

impl RenderedCandidate {
    /// Returns candidate artifacts in stable repository-relative path order.
    #[must_use]
    pub const fn artifacts(&self) -> &BTreeMap<String, Vec<u8>> {
        &self.artifacts
    }

    /// Returns the exhaustive compile and query-projection verification plan.
    #[must_use]
    pub const fn verification(&self) -> &GeneratedVerification {
        &self.verification
    }
}

/// Validates exact target identity and compiles raw introspection into an ordered model.
pub fn project_core(request: CoreProjectionRequest<'_>) -> Result<ProjectionPlan, DiagnosticSet> {
    let schema = schema::decode_and_validate(request.target, request.schema_json)?;
    let projection = projection::project(&schema)?;
    Ok(ProjectionPlan {
        target: request.target.clone(),
        schema,
        names: projection.names,
        named_types: projection.named_types,
        fields: projection.fields,
        directives: projection.directives,
        implementations: projection.implementations,
        catalog: projection.catalog,
    })
}

/// Renders the complete generated client and verification candidate in memory.
///
/// Rendering consumes only the immutable semantic plan. It performs no filesystem,
/// formatter, process, network, or publication operation.
pub fn render_core(plan: &ProjectionPlan) -> Result<RenderedCandidate, DiagnosticSet> {
    render::render_plan(plan)
}

#[cfg(test)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub(crate) struct CanonicalRenderedCandidate {
    artifacts: BTreeMap<String, Vec<u8>>,
}

#[cfg(test)]
pub(crate) fn render_canonical_checkpoint(
    target: &CodegenTarget,
    schema: &CanonicalSchema,
) -> Result<CanonicalRenderedCandidate, DiagnosticSet> {
    let canonical = encode_canonical_checkpoint(target, schema)?;
    Ok(CanonicalRenderedCandidate {
        artifacts: BTreeMap::from([("canonical-schema.json".to_owned(), canonical)]),
    })
}

#[cfg(test)]
fn encode_canonical_checkpoint(
    target: &CodegenTarget,
    schema: &CanonicalSchema,
) -> Result<Vec<u8>, DiagnosticSet> {
    let payload = (
        target,
        schema.query(),
        schema.types(),
        schema.directives(),
        schema.inventory(),
    );
    serde_json::to_vec(&payload).map_err(|_| {
        DiagnosticSet::one(Diagnostic::new(
            DiagnosticCode::GeneratedProvenanceInvalid,
            Some(DiagnosticCoordinate::new("canonical-schema.json")),
            "canonical schema checkpoint artifact could not be encoded",
        ))
    })
}

/// Generates a Rust client candidate from raw introspection schema data.
///
/// Parent links are populated on a private copy because transitional projection uses
/// the owning GraphQL type for option-structure names and typed-ID conversion.
pub fn generate(mut schema: Schema) -> Result<String, CodegenError> {
    for definition in schema.types.as_mut().into_iter().flatten().flatten() {
        let parent = definition.full_type.clone();
        for field in definition.full_type.fields.as_mut().into_iter().flatten() {
            field.parent_type = Some(parent.clone());
        }
    }

    let tokens = RustGenerator.render(&schema)?;
    let file = render::validate_file(tokens)?;
    Ok(rust::candidate_text(&file))
}

#[cfg(test)]
mod tests {
    use super::generate;
    use crate::schema::raw::IntrospectionResponse;

    fn generate_from_json(json: &str) -> String {
        let response = serde_json::from_str::<IntrospectionResponse>(json)
            .expect("test introspection response must decode");
        generate(
            response
                .into_schema()
                .schema
                .expect("test introspection response must contain a schema"),
        )
        .expect("test schema must render")
    }

    fn compact(value: &str) -> String {
        value
            .chars()
            .filter(|character| !character.is_whitespace())
            .collect()
    }

    fn contains_tokens(source: &str, expected: &str) -> bool {
        compact(source).contains(&compact(expected))
    }

    fn interface_schema() -> &'static str {
        include_str!("../tests/fixtures/interface-schema.json")
    }

    fn expected_type_schema() -> &'static str {
        include_str!("../tests/fixtures/expected-type-schema.json")
    }

    fn optional_arg_lifetime_schema() -> &'static str {
        include_str!("../tests/fixtures/optional-arg-lifetime-schema.json")
    }

    #[test]
    fn generated_output_is_deterministic() {
        let first = generate_from_json(interface_schema());
        let second = generate_from_json(interface_schema());
        assert_eq!(first.as_bytes(), second.as_bytes());
    }

    #[test]
    fn interfaces_and_implementors_are_projected() {
        let code = generate_from_json(interface_schema());
        for expected in [
            "pub trait Node",
            "pub struct NodeClient",
            "impl Node for NodeClient",
            "impl Node for Container",
            "impl Node for Directory",
            "impl Sealed for Container",
            "impl Sealed for Directory",
            "impl Sealed for NodeClient",
        ] {
            assert!(
                contains_tokens(&code, expected),
                "missing `{expected}` in {code}"
            );
        }
        assert!(!contains_tokens(&code, "pub struct Node {"));
        assert!(!contains_tokens(&code, "impl Sealed for Query"));
    }

    #[test]
    fn generated_handles_use_the_shared_session() {
        let code = generate_from_json(interface_schema());
        for expected in [
            "pub(crate) session: SessionHandle",
            "pub(crate) selection: Selection",
            "query.execute(&self.session).await",
            "let session = self.session.clone();",
            "query.execute(&session).await",
            "query = query.arg(\"path\", path.into());",
            "-> NodeClient",
        ] {
            assert!(
                contains_tokens(&code, expected),
                "missing `{expected}` in {code}"
            );
        }
        assert!(!code.contains("graphql_client"));
        assert!(!code.contains("DaggerSessionProc"));
    }

    #[test]
    fn expected_type_projection_preserves_typed_ids() {
        let code = generate_from_json(expected_type_schema());
        for expected in [
            "fn sync",
            "-> Result<Container, QueryError>",
            "crate::query::reenter(&self.session, id, \"Container\")",
            "fn id",
            "-> Result<Id, QueryError>",
            "directory: impl IntoID<Id>",
            "arg_id_input(\"directory\", IdInput::<Id>::lazy(directory))",
        ] {
            assert!(
                contains_tokens(&code, expected),
                "missing `{expected}` in {code}"
            );
        }
    }

    #[test]
    fn optional_argument_lifetimes_follow_the_projected_type() {
        let code = generate_from_json(optional_arg_lifetime_schema());
        for expected in [
            "pub enum RegistryProtocol",
            "pub struct QueryEnumOptionOpts {",
            "pub protocol: Option<RegistryProtocol>",
            "pub struct QueryStringOptionOpts<'a>",
            "pub name: Option<&'a str>",
        ] {
            assert!(
                contains_tokens(&code, expected),
                "missing `{expected}` in {code}"
            );
        }
        assert!(!contains_tokens(
            &code,
            "pub struct QueryEnumOptionOpts<'a>"
        ));
    }
}
