//! Generated bindings owned by the GraphQL `WorkspaceMigration` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A planned workspace migration."]
#[derive(Clone)]
pub struct WorkspaceMigration {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for WorkspaceMigration {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for WorkspaceMigration {
    fn graphql_type() -> &'static str {
        "WorkspaceMigration"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<WorkspaceMigration> for crate::IdInput<WorkspaceMigration> {
    fn from(value: WorkspaceMigration) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<WorkspaceMigration> for crate::IdInput<super::NodeClient> {
    fn from(value: WorkspaceMigration) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl WorkspaceMigration {
    #[doc = "Filesystem changes for the full migration plan.\n\nSelects GraphQL Wire_Name `changes` on `WorkspaceMigration`."]
    #[must_use]
    pub fn changes(&self) -> super::Changeset {
        let query = self.selection.select("changes");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this WorkspaceMigration.\n\nSelects GraphQL Wire_Name `id` on `WorkspaceMigration`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Logical migration steps, each identified by a stable code.\n\nSelects GraphQL Wire_Name `steps` on `WorkspaceMigration`."]
    pub async fn steps(&self) -> Result<Vec<super::WorkspaceMigrationStep>, crate::QueryError> {
        let query = self.selection.select("steps");
        let query = query.select("id");
        query
            .execute_reentry::<super::WorkspaceMigrationStep, Vec<crate::Id>>(
                &self.session,
                "WorkspaceMigrationStep",
            )
            .await
    }
}
impl super::Node for WorkspaceMigration {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
