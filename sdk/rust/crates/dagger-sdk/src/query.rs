//! Immutable GraphQL selection construction over one shared client session.
//!
//! [`QueryBuilder`] is the public compositional surface. Generated bindings use the
//! same private [`Selection`] representation, so raw, generated, and compositional
//! requests all pass through the lifecycle and timeout fence owned by `SharedSession`.

use std::{collections::BTreeMap, future::Future, pin::Pin, sync::Arc};

use futures::future;
use serde::{Serialize, de::DeserializeOwned};
use serde_json::Value;

use crate::errors::{
    QueryBuildError, QueryBuildErrorKind, QueryError, ResponseDecodingError,
    ResponseDecodingErrorKind,
};
use crate::graphql::{RawRequest, RawResponse, ResponseData};
use crate::lifecycle::SessionHandle;

type LazyFuture = Pin<Box<dyn Future<Output = Result<String, QueryBuildError>> + Send>>;
type LazyFunction = dyn Fn() -> LazyFuture + Send + Sync;

pub(crate) fn query() -> Selection {
    Selection::default()
}

#[derive(Clone)]
struct LazyResolve(Arc<LazyFunction>);

impl LazyResolve {
    #[cfg_attr(not(feature = "gen"), allow(dead_code))]
    fn new(func: Box<LazyFunction>) -> Self {
        Self(Arc::from(func))
    }

    fn from_result(value: Result<String, QueryBuildError>) -> Self {
        Self(Arc::new(move || Box::pin(future::ready(value.clone()))))
    }

    async fn resolve(&self) -> Result<String, QueryBuildError> {
        (self.0)().await
    }
}

/// One immutable path through a generated GraphQL operation.
///
/// The type stays crate-private so callers cannot splice a generated handle onto an
/// unrelated session or mutate its response traversal rules.
#[derive(Clone, Default)]
pub(crate) struct Selection {
    name: Option<String>,
    alias: Option<String>,
    // BTreeMap makes authored documents reproducible regardless of hash seeding.
    args: BTreeMap<String, LazyResolve>,
    inline_fragment: Option<String>,
    prev: Option<Arc<Selection>>,
}

impl Selection {
    #[cfg_attr(not(feature = "gen"), allow(dead_code))]
    pub(crate) fn root(&self) -> Self {
        Self::default()
    }

    pub(crate) fn select_with_alias(&self, alias: &str, name: &str) -> Self {
        Self {
            name: Some(name.to_owned()),
            alias: Some(alias.to_owned()),
            prev: Some(Arc::new(self.clone())),
            ..Self::default()
        }
    }

    pub(crate) fn select(&self, name: &str) -> Self {
        Self {
            name: Some(name.to_owned()),
            prev: Some(Arc::new(self.clone())),
            ..Self::default()
        }
    }

    #[cfg_attr(not(feature = "gen"), allow(dead_code))]
    pub(crate) fn inline_fragment(&self, type_name: &str) -> Self {
        Self {
            inline_fragment: Some(type_name.to_owned()),
            prev: Some(Arc::new(self.clone())),
            ..Self::default()
        }
    }

    pub(crate) fn arg<S>(&self, name: &str, value: S) -> Self
    where
        S: Serialize,
    {
        let encoded = serde_graphql_input::to_string_pretty(&value).map_err(|error| {
            QueryBuildError::with_source(QueryBuildErrorKind::ArgumentEncoding, error)
        });
        self.with_argument(name, LazyResolve::from_result(encoded))
    }

    #[cfg_attr(not(feature = "gen"), allow(dead_code))]
    pub(crate) fn arg_lazy(&self, name: &str, value: Box<LazyFunction>) -> Self {
        self.with_argument(name, LazyResolve::new(value))
    }

    fn with_argument(&self, name: &str, value: LazyResolve) -> Self {
        let mut next = self.clone();
        next.args.insert(name.to_owned(), value);
        next
    }

    pub(crate) async fn build(&self) -> Result<String, QueryBuildError> {
        let mut fields = vec!["query".to_owned()];

        for selection in self.path() {
            if let Some(type_name) = selection.inline_fragment {
                fields.push(format!("... on {type_name}"));
                continue;
            }

            let Some(mut field) = selection.name else {
                return Err(QueryBuildError::new(QueryBuildErrorKind::InvalidSelection));
            };
            if !selection.args.is_empty() {
                let mut arguments = Vec::with_capacity(selection.args.len());
                for (name, value) in selection.args {
                    arguments.push(format!("{name}:{}", value.resolve().await?));
                }
                field.push('(');
                field.push_str(&arguments.join(", "));
                field.push(')');
            }
            if let Some(alias) = selection.alias {
                field = format!("{alias}:{field}");
            }
            fields.push(field);
        }

        Ok(fields.join("{") + &"}".repeat(fields.len().saturating_sub(1)))
    }

    pub(crate) async fn execute<D>(&self, session: &SessionHandle) -> Result<D, QueryError>
    where
        D: DeserializeOwned,
    {
        let document = self.build().await.map_err(QueryError::Build)?;
        tracing::trace!(query = document.as_str(), "dagger-query");
        let response = session
            .execute(RawRequest::new(document))
            .await
            .map_err(QueryError::Request)?;
        self.decode(response)
    }

    fn decode<D>(&self, response: RawResponse) -> Result<D, QueryError>
    where
        D: DeserializeOwned,
    {
        if !response.errors().is_empty() {
            return Err(QueryError::GraphQl { response });
        }

        let data = match response.data() {
            ResponseData::Value(value) => value.clone(),
            ResponseData::Absent | ResponseData::Null => Value::Null,
        };
        self.unpack_value(data).map_err(|error| {
            QueryError::Decode(ResponseDecodingError::with_source(
                ResponseDecodingErrorKind::InvalidShape,
                error,
            ))
        })
    }

    fn path(&self) -> Vec<Self> {
        let mut selections = Vec::new();
        let mut current = self;
        while let Some(previous) = current.prev.as_ref() {
            selections.push(current.clone());
            current = previous;
        }
        selections.reverse();
        selections
    }

    fn unpack_value<D>(&self, mut data: Value) -> Result<D, serde_json::Error>
    where
        D: DeserializeOwned,
    {
        for selection in self.path() {
            // Inline fragments affect the document type condition, not the JSON path.
            if selection.inline_fragment.is_some() {
                continue;
            }
            if let Some(object) = data.as_object() {
                let key = selection.alias.as_ref().or(selection.name.as_ref());
                data = key
                    .and_then(|key| object.get(key))
                    .cloned()
                    .unwrap_or(Value::Null);
            }
        }

        if let Value::Array(values) = data {
            let values = values
                .into_iter()
                .map(|value| match value {
                    Value::Object(object) if object.len() == 1 => object
                        .into_iter()
                        .next()
                        .map_or(Value::Null, |(_, value)| value),
                    other => other,
                })
                .collect();
            return serde_json::from_value(Value::Array(values));
        }
        serde_json::from_value(data)
    }
}

/// An immutable compositional GraphQL query bound to one client session.
///
/// Building selections performs no I/O and takes no session lock. Execution observes
/// the same close fence and timeout policy as raw and generated requests.
#[derive(Clone)]
pub struct QueryBuilder {
    session: SessionHandle,
    selection: Selection,
}

impl QueryBuilder {
    pub(crate) fn new(session: SessionHandle) -> Self {
        Self {
            session,
            selection: query(),
        }
    }

    /// Returns a new builder selecting `field` below the current path.
    #[must_use]
    pub fn select(&self, field: impl Into<String>) -> Self {
        let field = field.into();
        Self {
            session: self.session.clone(),
            selection: self.selection.select(&field),
        }
    }

    /// Returns a new builder selecting `field` under `alias`.
    #[must_use]
    pub fn select_with_alias(&self, alias: impl Into<String>, field: impl Into<String>) -> Self {
        let alias = alias.into();
        let field = field.into();
        Self {
            session: self.session.clone(),
            selection: self.selection.select_with_alias(&alias, &field),
        }
    }

    /// Returns a new builder with one deterministically ordered GraphQL argument.
    ///
    /// Serialization is recorded rather than panicking; [`Self::document`] or
    /// [`Self::execute`] returns the typed build failure.
    #[must_use]
    pub fn argument<T>(&self, name: impl Into<String>, value: T) -> Self
    where
        T: Serialize,
    {
        let name = name.into();
        Self {
            session: self.session.clone(),
            selection: self.selection.arg(&name, value),
        }
    }

    /// Builds the complete GraphQL document without executing it.
    pub async fn document(&self) -> Result<String, QueryBuildError> {
        self.selection.build().await
    }

    /// Executes and decodes the value at the selected response path.
    ///
    /// A non-empty GraphQL error list returns [`QueryError::GraphQl`] with the full
    /// response, preserving partial data and extensions for inspection.
    pub async fn execute<T>(&self) -> Result<T, QueryError>
    where
        T: DeserializeOwned,
    {
        self.selection.execute(&self.session).await
    }

    #[cfg(test)]
    pub(crate) fn session_identity(&self) -> usize {
        self.session.identity()
    }
}

#[cfg(test)]
mod tests {
    use std::fmt;

    use futures::future;
    use pretty_assertions::assert_eq;
    use serde::Serialize;

    use super::query;
    use crate::errors::{QueryBuildError, QueryBuildErrorKind, QueryError};
    use crate::graphql::{GraphQlError, RawResponse, ResponseData};

    #[tokio::test]
    async fn documents_are_immutable_aliased_and_deterministic() {
        let base = query().select("image").arg("z", 2).arg("a", 1);
        let first = base.select_with_alias("out", "file").build().await;
        let second = base.select("directory").build().await;

        assert_eq!(
            first.expect("valid selection"),
            "query{image(a:1, z:2){out:file}}"
        );
        assert_eq!(
            second.expect("valid selection"),
            "query{image(a:1, z:2){directory}}"
        );
    }

    #[tokio::test]
    async fn inline_fragments_and_arrays_decode_without_synthetic_path_levels() {
        let selection = query()
            .select("node")
            .inline_fragment("Container")
            .select("items")
            .select("id");
        let value = serde_json::json!({"node": {"items": [{"id": "a"}, {"id": "b"}]}});

        let decoded: Vec<String> = selection.unpack_value(value).expect("matching data");
        assert_eq!(decoded, vec!["a", "b"]);
    }

    #[test]
    fn absent_selected_fields_decode_as_graphql_null() {
        let selection = query().select("missing");
        let decoded: Option<String> = selection
            .unpack_value(serde_json::json!({}))
            .expect("Option accepts GraphQL null");
        assert_eq!(decoded, None);
    }

    struct SerializationFailure;

    impl Serialize for SerializationFailure {
        fn serialize<S>(&self, _serializer: S) -> Result<S::Ok, S::Error>
        where
            S: serde::Serializer,
        {
            Err(serde::ser::Error::custom("deliberate failure"))
        }
    }

    impl fmt::Debug for SerializationFailure {
        fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
            formatter.write_str("SerializationFailure")
        }
    }

    #[tokio::test]
    async fn failed_argument_serialization_is_stored_without_panicking() {
        let error = query()
            .select("field")
            .arg("input", SerializationFailure)
            .build()
            .await
            .expect_err("serialization must fail");
        assert_eq!(error.kind(), QueryBuildErrorKind::ArgumentEncoding);
    }

    #[tokio::test]
    async fn failed_lazy_resolution_is_typed() {
        let selection = query().select("node").arg_lazy(
            "id",
            Box::new(|| {
                Box::pin(future::ready(Err(QueryBuildError::new(
                    QueryBuildErrorKind::LazyIdentifier,
                ))))
            }),
        );
        let error = selection.build().await.expect_err("resolution must fail");
        assert_eq!(error.kind(), QueryBuildErrorKind::LazyIdentifier);
    }

    #[test]
    fn selected_data_shape_failures_are_returned() {
        let selection = query().select("count");
        let result = selection.unpack_value::<u64>(serde_json::json!({"count": "many"}));
        assert!(result.is_err());
    }

    #[test]
    fn graphql_errors_preserve_the_complete_partial_response() {
        let response = RawResponse::new(ResponseData::Value(serde_json::json!({
            "version": "partial"
        })))
        .with_errors(vec![GraphQlError::new("failed")])
        .with_extensions(serde_json::Map::from_iter([(
            "requestId".to_owned(),
            serde_json::json!("one"),
        )]));
        let error = query()
            .select("version")
            .decode::<String>(response.clone())
            .expect_err("GraphQL errors are authoritative for typed execution");
        let QueryError::GraphQl { response: actual } = error else {
            panic!("expected complete GraphQL response");
        };
        assert_eq!(actual, response);
    }

    #[test]
    fn selected_data_decode_failures_use_the_typed_family() {
        let response = RawResponse::new(ResponseData::Value(serde_json::json!({
            "count": "many"
        })));
        let error = query()
            .select("count")
            .decode::<u64>(response)
            .expect_err("selected type is incompatible");
        assert!(matches!(error, QueryError::Decode(_)));
    }
}
