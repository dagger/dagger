//! Generated bindings owned by the GraphQL `WorkspaceSDK` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An installed SDK: a module marked for scaffolding other modules and clients."]
#[derive(Clone)]
pub struct WorkspaceSdk {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for WorkspaceSdk {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for WorkspaceSdk {
    fn graphql_type() -> &'static str {
        "WorkspaceSDK"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<WorkspaceSdk> for crate::IdInput<WorkspaceSdk> {
    fn from(value: WorkspaceSdk) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<WorkspaceSdk> for crate::IdInput<super::NodeClient> {
    fn from(value: WorkspaceSdk) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl WorkspaceSdk {
    #[doc = "Clients generated with this SDK.\n\nSelects GraphQL Wire_Name `clients` on `WorkspaceSDK`."]
    pub async fn clients(&self) -> Result<Vec<super::WorkspaceModule>, crate::QueryError> {
        let query = self.selection.select("clients");
        let query = query.select("id");
        query
            .execute_reentry::<super::WorkspaceModule, Vec<crate::Id>>(
                &self.session,
                "WorkspaceModule",
            )
            .await
    }
    #[doc = "A unique identifier for this WorkspaceSDK.\n\nSelects GraphQL Wire_Name `id` on `WorkspaceSDK`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Modules authored with this SDK.\n\nSelects GraphQL Wire_Name `modules` on `WorkspaceSDK`."]
    pub async fn modules(&self) -> Result<Vec<super::WorkspaceModule>, crate::QueryError> {
        let query = self.selection.select("modules");
        let query = query.select("id");
        query
            .execute_reentry::<super::WorkspaceModule, Vec<crate::Id>>(
                &self.session,
                "WorkspaceModule",
            )
            .await
    }
    #[doc = "The user-facing SDK name.\n\nSelects GraphQL Wire_Name `name` on `WorkspaceSDK`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The module reference this SDK was installed from.\n\nSelects GraphQL Wire_Name `ref` on `WorkspaceSDK`."]
    pub async fn r#ref(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("ref");
        query.execute(&self.session).await
    }
}
impl super::Node for WorkspaceSdk {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
