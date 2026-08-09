//! Generated bindings owned by the GraphQL `DiffStat` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `DiffStat`."]
#[derive(Clone)]
pub struct DiffStat {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for DiffStat {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for DiffStat {
    fn graphql_type() -> &'static str {
        "DiffStat"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<DiffStat> for crate::IdInput<DiffStat> {
    fn from(value: DiffStat) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<DiffStat> for crate::IdInput<super::NodeClient> {
    fn from(value: DiffStat) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl DiffStat {
    #[doc = "Number of added lines for this path.\n\nSelects GraphQL Wire_Name `addedLines` on `DiffStat`."]
    pub async fn added_lines(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("addedLines");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this DiffStat.\n\nSelects GraphQL Wire_Name `id` on `DiffStat`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Type of change.\n\nSelects GraphQL Wire_Name `kind` on `DiffStat`."]
    pub async fn kind(&self) -> Result<super::DiffStatKind, crate::QueryError> {
        let query = self.selection.select("kind");
        query.execute(&self.session).await
    }
    #[doc = "Previous path of the file, set only for renames.\n\nSelects GraphQL Wire_Name `oldPath` on `DiffStat`."]
    pub async fn old_path(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("oldPath");
        query.execute(&self.session).await
    }
    #[doc = "Path of the changed file or directory.\n\nSelects GraphQL Wire_Name `path` on `DiffStat`."]
    pub async fn path(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("path");
        query.execute(&self.session).await
    }
    #[doc = "Number of removed lines for this path.\n\nSelects GraphQL Wire_Name `removedLines` on `DiffStat`."]
    pub async fn removed_lines(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("removedLines");
        query.execute(&self.session).await
    }
}
impl super::Node for DiffStat {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
