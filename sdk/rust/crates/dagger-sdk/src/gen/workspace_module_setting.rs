//! Generated bindings owned by the GraphQL `WorkspaceModuleSetting` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A constructor-backed module setting."]
#[derive(Clone)]
pub struct WorkspaceModuleSetting {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for WorkspaceModuleSetting {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for WorkspaceModuleSetting {
    fn graphql_type() -> &'static str {
        "WorkspaceModuleSetting"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<WorkspaceModuleSetting> for crate::IdInput<WorkspaceModuleSetting> {
    fn from(value: WorkspaceModuleSetting) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<WorkspaceModuleSetting> for crate::IdInput<super::NodeClient> {
    fn from(value: WorkspaceModuleSetting) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl WorkspaceModuleSetting {
    #[doc = "The constructor argument description.\n\nSelects GraphQL Wire_Name `description` on `WorkspaceModuleSetting`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this WorkspaceModuleSetting.\n\nSelects GraphQL Wire_Name `id` on `WorkspaceModuleSetting`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Whether the setting accepts a list of values.\n\nSelects GraphQL Wire_Name `isList` on `WorkspaceModuleSetting`."]
    pub async fn is_list(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("isList");
        query.execute(&self.session).await
    }
    #[doc = "The setting key.\n\nSelects GraphQL Wire_Name `key` on `WorkspaceModuleSetting`."]
    pub async fn key(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("key");
        query.execute(&self.session).await
    }
    #[doc = "The configured value after applying the selected workspace environment, or empty when unset.\n\nSelects GraphQL Wire_Name `value` on `WorkspaceModuleSetting`."]
    pub async fn value(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("value");
        query.execute(&self.session).await
    }
}
impl super::Node for WorkspaceModuleSetting {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
