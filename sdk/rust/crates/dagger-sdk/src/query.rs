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
use crate::id_input::{GeneratedIdInputShape, ResolveIdInput};
use crate::lifecycle::SessionHandle;
use crate::runtime_errors::ExecError;
use crate::{Id, IdInput, IntoID, loadable};

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
        let encoded = encode_argument(&value);
        self.with_argument(name, LazyResolve::from_result(encoded))
    }

    pub(crate) fn arg_id_input<T>(&self, name: &str, value: T) -> Self
    where
        T: ResolveIdInput,
    {
        let value = Arc::new(value);
        self.with_argument(
            name,
            LazyResolve::new(Box::new(move || {
                let value = Arc::clone(&value);
                Box::pin(async move {
                    let resolved = value.resolve().await?;
                    encode_argument(&resolved)
                })
            })),
        )
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

    pub(crate) async fn execute_reentry<T, I>(
        &self,
        session: &SessionHandle,
        concrete_type: &str,
    ) -> Result<I::Output, QueryError>
    where
        T: loadable::private::Sealed,
        I: DeserializeOwned + ReentryIds<T>,
    {
        // Decode the complete identifier shape before manufacturing any handle. A bad
        // element therefore cannot leak a partial vector of apparently valid values.
        let ids: I = self.execute(session).await?;
        Ok(ids.reenter(session, concrete_type))
    }

    pub(crate) fn decode<D>(&self, response: RawResponse) -> Result<D, QueryError>
    where
        D: DeserializeOwned,
    {
        if !response.errors().is_empty() {
            if let Some(error) = ExecError::from_response(&response) {
                return Err(QueryError::Exec { error, response });
            }
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

    pub(crate) fn unpack_value<D>(&self, data: Value) -> Result<D, serde_json::Error>
    where
        D: DeserializeOwned,
    {
        let projected = project_selection(data, &self.path())?;
        serde_json::from_value(projected)
    }
}

fn encode_argument<T>(value: &T) -> Result<String, QueryBuildError>
where
    T: Serialize,
{
    serde_graphql_input::to_string_pretty(value)
        .map_err(|error| QueryBuildError::with_source(QueryBuildErrorKind::ArgumentEncoding, error))
}

fn project_selection(data: Value, path: &[Selection]) -> Result<Value, serde_json::Error> {
    let Some((selection, remaining)) = path.split_first() else {
        return Ok(data);
    };

    // Inline fragments constrain GraphQL execution but do not add a response key.
    if selection.inline_fragment.is_some() {
        return project_selection(data, remaining);
    }

    match data {
        Value::Array(values) => values
            .into_iter()
            .map(|value| project_selection(value, path))
            .collect::<Result<Vec<_>, _>>()
            .map(Value::Array),
        Value::Object(object) => {
            let key = selection.alias.as_ref().or(selection.name.as_ref());
            let value = key
                .and_then(|key| object.get(key))
                .cloned()
                .unwrap_or(Value::Null);
            project_selection(value, remaining)
        }
        Value::Null => Ok(Value::Null),
        _ => Err(serde::de::Error::custom(
            "a selected GraphQL field was not contained in an object",
        )),
    }
}

pub(crate) fn reenter<T>(session: &SessionHandle, id: Id, concrete_type: &str) -> T
where
    T: loadable::private::Sealed,
{
    let selection = query()
        .select("node")
        .arg("id", id)
        .inline_fragment(concrete_type);
    T::from_query(session.clone(), selection)
}

pub(crate) fn reenter_lazy<T>(session: &SessionHandle, id: IdInput<T>, concrete_type: &str) -> T
where
    T: loadable::private::Sealed + 'static,
{
    let selection = query()
        .select("node")
        .arg_id_input("id", id)
        .inline_fragment(concrete_type);
    T::from_query(session.clone(), selection)
}

pub(crate) trait ReentryIds<T>
where
    T: loadable::private::Sealed,
{
    type Output;

    fn reenter(self, session: &SessionHandle, concrete_type: &str) -> Self::Output;
}

impl<T> ReentryIds<T> for Id
where
    T: loadable::private::Sealed,
{
    type Output = T;

    fn reenter(self, session: &SessionHandle, concrete_type: &str) -> Self::Output {
        reenter(session, self, concrete_type)
    }
}

impl<T, I> ReentryIds<T> for Option<I>
where
    T: loadable::private::Sealed,
    I: ReentryIds<T>,
{
    type Output = Option<I::Output>;

    fn reenter(self, session: &SessionHandle, concrete_type: &str) -> Self::Output {
        self.map(|ids| ids.reenter(session, concrete_type))
    }
}

impl<T, I> ReentryIds<T> for Vec<I>
where
    T: loadable::private::Sealed,
    I: ReentryIds<T>,
{
    type Output = Vec<I::Output>;

    fn reenter(self, session: &SessionHandle, concrete_type: &str) -> Self::Output {
        self.into_iter()
            .map(|ids| ids.reenter(session, concrete_type))
            .collect()
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

    /// Reconstructs the generated root over this builder's existing session lease.
    ///
    /// Module code generation uses this exact-version bridge to expose the checked
    /// typed root without connecting again or storing a process-global client.
    #[cfg(feature = "gen")]
    #[doc(hidden)]
    #[must_use]
    pub fn generated_query_root(&self) -> crate::Query {
        crate::Query {
            session: self.session.clone(),
            selection: self.selection.clone(),
        }
    }

    /// Re-enters a checked generated handle on this builder's existing session.
    ///
    /// `concrete_type` may be more specific than `T` for an interface value. Keeping
    /// that inline-fragment identity prevents interface decoding from manufacturing an
    /// untyped substitute while still returning the checked Rust interface client.
    #[cfg(feature = "gen")]
    #[doc(hidden)]
    #[must_use]
    pub fn reenter_generated_handle<T>(&self, id: Id, concrete_type: &str) -> T
    where
        T: crate::Loadable + 'static,
    {
        reenter(&self.session, id, concrete_type)
    }

    /// Constructs a Core SDK handle from this builder's current selection.
    ///
    /// The sealed [`crate::Loadable`] constructor is invoked inside `dagger-sdk`, so
    /// standalone generated code never receives the session or selection values it
    /// would need to manufacture or splice a Core handle.
    #[doc(hidden)]
    #[must_use]
    pub fn generated_core_handle<T>(&self) -> T
    where
        T: crate::Loadable,
    {
        T::from_query(self.session.clone(), self.selection.clone())
    }

    /// Starts a `node(id:)` selection on this builder's existing session.
    ///
    /// Standalone bindings use the returned public builder to construct their own
    /// private handle types. Resetting the selection while cloning the same session is
    /// what makes identifier re-entry independent of the field which produced it,
    /// without reconnecting or allowing a cross-session splice.
    #[doc(hidden)]
    #[must_use]
    pub fn generated_reentry_builder(&self, id: Id, concrete_type: &'static str) -> Self {
        Self {
            session: self.session.clone(),
            selection: query()
                .select("node")
                .arg("id", id)
                .inline_fragment(concrete_type),
        }
    }

    /// Records one direct target-typed identifier argument for deferred resolution.
    #[doc(hidden)]
    #[must_use]
    pub fn generated_argument_id<H>(&self, name: &'static str, value: H) -> Self
    where
        H: IntoID<Id>,
    {
        Self {
            session: self.session.clone(),
            selection: self
                .selection
                .arg_id_input(name, IdInput::<Id>::generated_lazy(value)),
        }
    }

    /// Records a recursive target-typed identifier shape for deferred resolution.
    ///
    /// Resolution remains internal to request construction. Lists are resolved in
    /// caller order and the containing request is not admitted when any element fails.
    #[doc(hidden)]
    #[must_use]
    pub fn generated_argument_id_shape<S>(&self, name: &'static str, value: S) -> Self
    where
        S: GeneratedIdInputShape,
    {
        Self {
            session: self.session.clone(),
            selection: self.selection.arg_id_input(name, value),
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
    use std::{fmt, pin::Pin};

    use pretty_assertions::assert_eq;
    use serde::Serialize;

    use super::query;
    use crate::errors::{QueryBuildError, QueryBuildErrorKind, QueryError};
    use crate::graphql::{GraphQlError, RawResponse, ResponseData};
    use crate::{Id, IdInput, IntoID};

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
        #[derive(Clone)]
        struct FailingIdentifier;

        impl IntoID<Id> for FailingIdentifier {
            fn into_id(
                self,
            ) -> Pin<Box<dyn core::future::Future<Output = Result<Id, QueryError>> + Send>>
            {
                Box::pin(async {
                    Err(QueryError::Build(QueryBuildError::new(
                        QueryBuildErrorKind::LazyIdentifier,
                    )))
                })
            }
        }

        let selection = query()
            .select("node")
            .arg_id_input("id", IdInput::<Id>::lazy(FailingIdentifier));
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
    fn valid_exec_errors_map_without_losing_the_raw_response() {
        let extensions = serde_json::Map::from_iter([
            ("_type".to_owned(), serde_json::json!("EXEC_ERROR")),
            ("exitCode".to_owned(), serde_json::json!(127)),
            ("cmd".to_owned(), serde_json::json!(["sh", "-c", "false"])),
            ("stdout".to_owned(), serde_json::json!("captured-out")),
            ("stderr".to_owned(), serde_json::json!("captured-err")),
            ("future".to_owned(), serde_json::json!(true)),
        ]);
        let response = RawResponse::new(ResponseData::Value(serde_json::json!({
            "container": null
        })))
        .with_errors(vec![
            GraphQlError::new("process exited with code 127").with_extensions(extensions),
        ]);
        let error = query()
            .select("container")
            .decode::<String>(response.clone())
            .expect_err("execution failure is authoritative");
        let QueryError::Exec {
            error,
            response: actual,
        } = error
        else {
            panic!("expected typed execution error");
        };
        assert_eq!(error.exit_code(), Some(127));
        assert_eq!(
            error.command(),
            Some(&["sh".into(), "-c".into(), "false".into()][..])
        );
        assert_eq!(error.stdout(), Some("captured-out"));
        assert_eq!(error.stderr(), Some("captured-err"));
        assert_eq!(actual, response);
        assert!(!error.to_string().contains("captured-out"));
        assert!(!error.to_string().contains("captured-err"));
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
