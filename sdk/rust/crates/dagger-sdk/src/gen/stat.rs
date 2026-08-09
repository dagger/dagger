//! Generated bindings owned by the GraphQL `Stat` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A file or directory status object."]
#[derive(Clone)]
pub struct Stat {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Stat {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Stat {
    fn graphql_type() -> &'static str {
        "Stat"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Stat> for crate::IdInput<Stat> {
    fn from(value: Stat) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Stat> for crate::IdInput<super::NodeClient> {
    fn from(value: Stat) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Stat {
    #[doc = "file type\n\nSelects GraphQL Wire_Name `fileType` on `Stat`."]
    pub async fn file_type(&self) -> Result<Option<super::FileType>, crate::QueryError> {
        let query = self.selection.select("fileType");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Stat.\n\nSelects GraphQL Wire_Name `id` on `Stat`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "file name\n\nSelects GraphQL Wire_Name `name` on `Stat`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "permission bits\n\nSelects GraphQL Wire_Name `permissions` on `Stat`."]
    pub async fn permissions(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("permissions");
        query.execute(&self.session).await
    }
    #[doc = "file size\n\nSelects GraphQL Wire_Name `size` on `Stat`."]
    pub async fn size(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("size");
        query.execute(&self.session).await
    }
}
impl super::Node for Stat {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
