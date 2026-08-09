//! Generated bindings owned by the GraphQL `GeneratedCode` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The result of running an SDK's codegen."]
#[derive(Clone)]
pub struct GeneratedCode {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for GeneratedCode {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for GeneratedCode {
    fn graphql_type() -> &'static str {
        "GeneratedCode"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<GeneratedCode> for crate::IdInput<GeneratedCode> {
    fn from(value: GeneratedCode) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<GeneratedCode> for crate::IdInput<super::NodeClient> {
    fn from(value: GeneratedCode) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl GeneratedCode {
    #[doc = "The directory containing the generated code.\n\nSelects GraphQL Wire_Name `code` on `GeneratedCode`."]
    #[must_use]
    pub fn code(&self) -> super::Directory {
        let query = self.selection.select("code");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this GeneratedCode.\n\nSelects GraphQL Wire_Name `id` on `GeneratedCode`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "List of paths to mark generated in version control (i.e. .gitattributes).\n\nSelects GraphQL Wire_Name `vcsGeneratedPaths` on `GeneratedCode`."]
    pub async fn vcs_generated_paths(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("vcsGeneratedPaths");
        query.execute(&self.session).await
    }
    #[doc = "List of paths to ignore in version control (i.e. .gitignore).\n\nSelects GraphQL Wire_Name `vcsIgnoredPaths` on `GeneratedCode`."]
    pub async fn vcs_ignored_paths(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("vcsIgnoredPaths");
        query.execute(&self.session).await
    }
    #[doc = "Set the list of paths to mark generated in version control.\n\nSelects GraphQL Wire_Name `withVCSGeneratedPaths` on `GeneratedCode`."]
    #[must_use]
    pub fn with_vcs_generated_paths(&self, paths: Vec<impl Into<String>>) -> super::GeneratedCode {
        let query = self.selection.select("withVCSGeneratedPaths");
        let paths = paths.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("paths", paths);
        super::GeneratedCode {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Set the list of paths to ignore in version control.\n\nSelects GraphQL Wire_Name `withVCSIgnoredPaths` on `GeneratedCode`."]
    #[must_use]
    pub fn with_vcs_ignored_paths(&self, paths: Vec<impl Into<String>>) -> super::GeneratedCode {
        let query = self.selection.select("withVCSIgnoredPaths");
        let paths = paths.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("paths", paths);
        super::GeneratedCode {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for GeneratedCode {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
