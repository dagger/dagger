//! Generated bindings owned by the GraphQL `WorkspaceMigrationStep` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A single logical part of a workspace migration."]
#[derive(Clone)]
pub struct WorkspaceMigrationStep {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for WorkspaceMigrationStep {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for WorkspaceMigrationStep {
    fn graphql_type() -> &'static str {
        "WorkspaceMigrationStep"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<WorkspaceMigrationStep> for crate::IdInput<WorkspaceMigrationStep> {
    fn from(value: WorkspaceMigrationStep) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<WorkspaceMigrationStep> for crate::IdInput<super::NodeClient> {
    fn from(value: WorkspaceMigrationStep) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl WorkspaceMigrationStep {
    #[doc = "Filesystem changes for this step.\n\nSelects GraphQL Wire_Name `changes` on `WorkspaceMigrationStep`."]
    #[must_use]
    pub fn changes(&self) -> super::Changeset {
        let query = self.selection.select("changes");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Stable code identifying this logical migration step.\n\nSelects GraphQL Wire_Name `code` on `WorkspaceMigrationStep`."]
    pub async fn code(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("code");
        query.execute(&self.session).await
    }
    #[doc = "Generic summary of this step's purpose and impact.\n\nSelects GraphQL Wire_Name `description` on `WorkspaceMigrationStep`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this WorkspaceMigrationStep.\n\nSelects GraphQL Wire_Name `id` on `WorkspaceMigrationStep`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Non-fatal warnings raised while planning this step.\n\nSelects GraphQL Wire_Name `warnings` on `WorkspaceMigrationStep`."]
    pub async fn warnings(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("warnings");
        query.execute(&self.session).await
    }
}
impl super::Node for WorkspaceMigrationStep {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
