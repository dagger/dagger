//! Generated bindings owned by the GraphQL `WorkspaceGit` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Local git state for a workspace."]
#[derive(Clone)]
pub struct WorkspaceGit {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for WorkspaceGit {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for WorkspaceGit {
    fn graphql_type() -> &'static str {
        "WorkspaceGit"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<WorkspaceGit> for crate::IdInput<WorkspaceGit> {
    fn from(value: WorkspaceGit) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<WorkspaceGit> for crate::IdInput<super::NodeClient> {
    fn from(value: WorkspaceGit) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl WorkspaceGit {
    #[doc = "The checked-out HEAD of this workspace.\n\nSelects GraphQL Wire_Name `head` on `WorkspaceGit`."]
    #[must_use]
    pub fn head(&self) -> super::GitRef {
        let query = self.selection.select("head");
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this WorkspaceGit.\n\nSelects GraphQL Wire_Name `id` on `WorkspaceGit`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Uncommitted changes in this workspace, using the same rules as GitRepository.uncommitted.\n\nSelects GraphQL Wire_Name `uncommitted` on `WorkspaceGit`."]
    #[must_use]
    pub fn uncommitted(&self) -> super::Changeset {
        let query = self.selection.select("uncommitted");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for WorkspaceGit {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
