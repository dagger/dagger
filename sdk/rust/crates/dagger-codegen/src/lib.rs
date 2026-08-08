#![deny(warnings)]
//! Pure schema-to-Rust code generation for the Dagger SDK.
//!
//! This crate accepts data and returns validated candidate source. It deliberately has
//! no filesystem, process, network, engine-session, or completeness-ledger authority.

pub mod diagnostic;
pub mod projection;
pub mod render;
pub mod rust;
pub mod schema;
pub mod target;

use diagnostic::CodegenError;
use rust::RustGenerator;
use schema::raw::Schema;

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
            "select(\"node\")",
            "inline_fragment(\"Container\")",
            "fn id",
            "-> Result<Id, QueryError>",
            "directory: impl IntoID<Id>",
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
