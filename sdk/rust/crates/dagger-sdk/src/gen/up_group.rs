//! Generated bindings owned by the GraphQL `UpGroup` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `UpGroup`."]
#[derive(Clone)]
pub struct UpGroup {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for UpGroup {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for UpGroup {
    fn graphql_type() -> &'static str {
        "UpGroup"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<UpGroup> for crate::IdInput<UpGroup> {
    fn from(value: UpGroup) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<UpGroup> for crate::IdInput<super::NodeClient> {
    fn from(value: UpGroup) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl UpGroup {
    #[doc = "A unique identifier for this UpGroup.\n\nSelects GraphQL Wire_Name `id` on `UpGroup`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Return a list of individual services and their details\n\nSelects GraphQL Wire_Name `list` on `UpGroup`."]
    pub async fn list(&self) -> Result<Vec<super::Up>, crate::QueryError> {
        let query = self.selection.select("list");
        let query = query.select("id");
        query
            .execute_reentry::<super::Up, Vec<crate::Id>>(&self.session, "Up")
            .await
    }
    #[doc = "Execute all selected service functions\n\nSelects GraphQL Wire_Name `run` on `UpGroup`."]
    #[must_use]
    pub fn run(&self) -> super::UpGroup {
        let query = self.selection.select("run");
        super::UpGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for UpGroup {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
