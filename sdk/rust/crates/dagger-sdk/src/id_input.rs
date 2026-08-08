//! Target-typed identifier inputs resolved before a GraphQL request is admitted.
//!
//! The public marker prevents generated methods from accepting an unrelated handle,
//! while the private representation lets a raw identifier remain immediately ready
//! and a generated handle defer its identifier query until document construction.

use std::{error::Error, fmt, future::Future, marker::PhantomData, pin::Pin, sync::Arc};

use serde::Serialize;

use crate::errors::{QueryBuildError, QueryBuildErrorKind, QueryError};
use crate::{Id, IntoID};

type IdentifierFuture = Pin<Box<dyn Future<Output = Result<Id, QueryError>> + Send>>;
type ResolutionFuture<'a, T> =
    Pin<Box<dyn Future<Output = Result<T, QueryBuildError>> + Send + 'a>>;

trait ErasedIdResolver: Send + Sync {
    fn resolve(&self) -> IdentifierFuture;
}

struct IntoIdResolver<T>(T);

impl<T> ErasedIdResolver for IntoIdResolver<T>
where
    T: IntoID<Id>,
{
    fn resolve(&self) -> IdentifierFuture {
        self.0.clone().into_id()
    }
}

#[derive(Clone)]
enum IdInputValue {
    Ready(Id),
    Lazy(Arc<dyn ErasedIdResolver>),
}

/// An identifier accepted for the generated target type `T`.
///
/// Raw [`Id`] values convert to any target without an engine lookup. Generated code
/// supplies conversions from compatible handles; no blanket handle conversion exists,
/// so an identifier for an unrelated schema type is rejected at compile time.
pub struct IdInput<T> {
    value: IdInputValue,
    target: PhantomData<fn() -> T>,
}

impl<T> Clone for IdInput<T> {
    fn clone(&self) -> Self {
        Self {
            value: self.value.clone(),
            target: PhantomData,
        }
    }
}

impl<T> IdInput<T> {
    /// Creates a ready target-typed input from an opaque engine identifier.
    #[must_use]
    pub fn new(id: Id) -> Self {
        Self {
            value: IdInputValue::Ready(id),
            target: PhantomData,
        }
    }

    pub(crate) fn lazy<H>(handle: H) -> Self
    where
        H: IntoID<Id>,
    {
        Self {
            value: IdInputValue::Lazy(Arc::new(IntoIdResolver(handle))),
            target: PhantomData,
        }
    }
}

impl<T> From<Id> for IdInput<T> {
    fn from(id: Id) -> Self {
        Self::new(id)
    }
}

impl<T> fmt::Debug for IdInput<T> {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        // A handle's selection may contain secrets, so Debug exposes only whether the
        // identifier is already available and never its value or deferred query.
        let state = match self.value {
            IdInputValue::Ready(_) => "ready",
            IdInputValue::Lazy(_) => "lazy",
        };
        formatter
            .debug_struct("IdInput")
            .field("state", &state)
            .finish_non_exhaustive()
    }
}

pub(crate) trait ResolveIdInput: Send + Sync + 'static {
    type Resolved: Serialize + Send + 'static;

    fn resolve(&self) -> ResolutionFuture<'_, Self::Resolved>;
}

impl<T: 'static> ResolveIdInput for IdInput<T> {
    type Resolved = Id;

    fn resolve(&self) -> ResolutionFuture<'_, Self::Resolved> {
        let value = self.value.clone();
        Box::pin(async move {
            match value {
                IdInputValue::Ready(id) => Ok(id),
                IdInputValue::Lazy(resolver) => resolver.resolve().await.map_err(|error| {
                    QueryBuildError::with_source(QueryBuildErrorKind::LazyIdentifier, error)
                }),
            }
        })
    }
}

impl<T> ResolveIdInput for Option<T>
where
    T: ResolveIdInput,
{
    type Resolved = Option<T::Resolved>;

    fn resolve(&self) -> ResolutionFuture<'_, Self::Resolved> {
        Box::pin(async move {
            match self {
                Some(value) => value.resolve().await.map(Some),
                None => Ok(None),
            }
        })
    }
}

impl<T> ResolveIdInput for Vec<T>
where
    T: ResolveIdInput,
{
    type Resolved = Vec<T::Resolved>;

    fn resolve(&self) -> ResolutionFuture<'_, Self::Resolved> {
        Box::pin(async move {
            // Sequential resolution makes observable resolver order match caller order
            // and stops before the containing request if any element fails.
            let mut resolved = Vec::with_capacity(self.len());
            for (index, value) in self.iter().enumerate() {
                let value = value.resolve().await.map_err(|source| {
                    QueryBuildError::with_source(
                        QueryBuildErrorKind::LazyIdentifier,
                        IndexedIdentifierError { index, source },
                    )
                })?;
                resolved.push(value);
            }
            Ok(resolved)
        })
    }
}

#[derive(Clone)]
struct IndexedIdentifierError {
    index: usize,
    source: QueryBuildError,
}

impl fmt::Debug for IndexedIdentifierError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("IndexedIdentifierError")
            .field("index", &self.index)
            .finish_non_exhaustive()
    }
}

impl fmt::Display for IndexedIdentifierError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "identifier element {} failed", self.index)
    }
}

impl Error for IndexedIdentifierError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(&self.source)
    }
}
