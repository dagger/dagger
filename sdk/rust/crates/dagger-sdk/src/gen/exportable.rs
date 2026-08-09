//! Generated bindings owned by the GraphQL `Exportable` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An object that can be exported to the host.\n\nCalling export writes the object to a path on the host filesystem and returns the path that was written."]
pub trait Exportable: Clone + Send + Sync {
    #[doc = "Selects GraphQL Wire_Name `export` on `Exportable`."]
    fn export(
        &self,
        path: impl Into<String> + Send,
    ) -> impl core::future::Future<Output = Result<String, crate::QueryError>> + Send;
    #[doc = "Selects GraphQL Wire_Name `id` on `Exportable`."]
    fn id(&self)
    -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send;
}
#[doc = "Lazy client handle for GraphQL interface `Exportable`."]
#[derive(Clone)]
pub struct ExportableClient {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for ExportableClient {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for ExportableClient {
    fn graphql_type() -> &'static str {
        "Exportable"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<ExportableClient> for crate::IdInput<ExportableClient> {
    fn from(value: ExportableClient) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<ExportableClient> for crate::IdInput<super::NodeClient> {
    fn from(value: ExportableClient) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl ExportableClient {
    #[doc = "Selects GraphQL Wire_Name `export` on `Exportable`."]
    pub async fn export(&self, path: impl Into<String>) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "Selects GraphQL Wire_Name `id` on `Exportable`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
}
impl super::Exportable for ExportableClient {
    fn export(
        &self,
        path: impl Into<String> + Send,
    ) -> impl core::future::Future<Output = Result<String, crate::QueryError>> + Send {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Node for ExportableClient {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
