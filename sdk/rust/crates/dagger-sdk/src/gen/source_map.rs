//! Generated bindings owned by the GraphQL `SourceMap` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Source location information."]
#[derive(Clone)]
pub struct SourceMap {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for SourceMap {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for SourceMap {
    fn graphql_type() -> &'static str {
        "SourceMap"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<SourceMap> for crate::IdInput<SourceMap> {
    fn from(value: SourceMap) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<SourceMap> for crate::IdInput<super::NodeClient> {
    fn from(value: SourceMap) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl SourceMap {
    #[doc = "The column number within the line.\n\nSelects GraphQL Wire_Name `column` on `SourceMap`."]
    pub async fn column(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("column");
        query.execute(&self.session).await
    }
    #[doc = "The filename from the module source.\n\nSelects GraphQL Wire_Name `filename` on `SourceMap`."]
    pub async fn filename(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("filename");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this SourceMap.\n\nSelects GraphQL Wire_Name `id` on `SourceMap`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The line number within the filename.\n\nSelects GraphQL Wire_Name `line` on `SourceMap`."]
    pub async fn line(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("line");
        query.execute(&self.session).await
    }
    #[doc = "The module dependency this was declared in.\n\nSelects GraphQL Wire_Name `module` on `SourceMap`."]
    pub async fn module(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("module");
        query.execute(&self.session).await
    }
    #[doc = "The URL to the file, if any. This can be used to link to the source map in the browser.\n\nSelects GraphQL Wire_Name `url` on `SourceMap`."]
    pub async fn url(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("url");
        query.execute(&self.session).await
    }
}
impl super::Node for SourceMap {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
