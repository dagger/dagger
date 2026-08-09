//! Generated bindings owned by the GraphQL `WorkspaceModule` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A module entry in the workspace configuration."]
#[derive(Clone)]
pub struct WorkspaceModule {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for WorkspaceModule {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for WorkspaceModule {
    fn graphql_type() -> &'static str {
        "WorkspaceModule"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<WorkspaceModule> for crate::IdInput<WorkspaceModule> {
    fn from(value: WorkspaceModule) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<WorkspaceModule> for crate::IdInput<super::NodeClient> {
    fn from(value: WorkspaceModule) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl WorkspaceModule {
    #[doc = "Whether the module is the workspace entrypoint (functions aliased to Query root).\n\nSelects GraphQL Wire_Name `entrypoint` on `WorkspaceModule`."]
    pub async fn entrypoint(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("entrypoint");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this WorkspaceModule.\n\nSelects GraphQL Wire_Name `id` on `WorkspaceModule`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The module name.\n\nSelects GraphQL Wire_Name `name` on `WorkspaceModule`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "List constructor-backed settings for this module.\n\nSelects GraphQL Wire_Name `settings` on `WorkspaceModule`."]
    pub async fn settings(&self) -> Result<Vec<super::WorkspaceModuleSetting>, crate::QueryError> {
        let query = self.selection.select("settings");
        let query = query.select("id");
        query
            .execute_reentry::<super::WorkspaceModuleSetting, Vec<crate::Id>>(
                &self.session,
                "WorkspaceModuleSetting",
            )
            .await
    }
    #[doc = "The module source path.\n\nSelects GraphQL Wire_Name `source` on `WorkspaceModule`."]
    pub async fn source(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("source");
        query.execute(&self.session).await
    }
}
impl super::Node for WorkspaceModule {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
