//! Generated bindings owned by the GraphQL `Error` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `Error`."]
#[derive(Clone)]
pub struct Error {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Error {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Error {
    fn graphql_type() -> &'static str {
        "Error"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Error> for crate::IdInput<Error> {
    fn from(value: Error) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Error> for crate::IdInput<super::NodeClient> {
    fn from(value: Error) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Error {
    #[doc = "A unique identifier for this Error.\n\nSelects GraphQL Wire_Name `id` on `Error`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "A description of the error.\n\nSelects GraphQL Wire_Name `message` on `Error`."]
    pub async fn message(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("message");
        query.execute(&self.session).await
    }
    #[doc = "The extensions of the error.\n\nSelects GraphQL Wire_Name `values` on `Error`."]
    pub async fn values(&self) -> Result<Vec<super::ErrorValue>, crate::QueryError> {
        let query = self.selection.select("values");
        let query = query.select("id");
        query
            .execute_reentry::<super::ErrorValue, Vec<crate::Id>>(&self.session, "ErrorValue")
            .await
    }
    #[doc = "Add a value to the error.\n\nSelects GraphQL Wire_Name `withValue` on `Error`."]
    #[must_use]
    pub fn with_value(&self, name: impl Into<String>, value: crate::Json) -> super::Error {
        let query = self.selection.select("withValue");
        let query = query.arg("name", name.into());
        let query = query.arg("value", value);
        super::Error {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Error {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
