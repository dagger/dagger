//! Generated bindings owned by the GraphQL `GitRepository` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A git repository."]
#[derive(Clone)]
pub struct GitRepository {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `GitRepository.asWorkspace`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct GitRepositoryAsWorkspaceOpts {
    #[doc = "Current working directory inside the workspace root. Defaults to the workspace root.\n\n`None` omits GraphQL Wire_Name `cwd` and preserves engine default `String(\"/\")`."]
    pub cwd: Option<String>,
}
impl GitRepositoryAsWorkspaceOpts {
    #[doc = "Sets GraphQL argument `cwd` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cwd(mut self, value: impl Into<String>) -> Self {
        self.cwd = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `GitRepository.branches`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct GitRepositoryBranchesOpts {
    #[doc = "Glob patterns (e.g., \"refs/tags/v*\").\n\n`None` omits GraphQL Wire_Name `patterns`."]
    pub patterns: Option<Vec<String>>,
}
impl GitRepositoryBranchesOpts {
    #[doc = "Sets GraphQL argument `patterns` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_patterns(mut self, value: Vec<impl Into<String>>) -> Self {
        self.patterns = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `GitRepository.tags`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct GitRepositoryTagsOpts {
    #[doc = "Glob patterns (e.g., \"refs/tags/v*\").\n\n`None` omits GraphQL Wire_Name `patterns`."]
    pub patterns: Option<Vec<String>>,
}
impl GitRepositoryTagsOpts {
    #[doc = "Sets GraphQL argument `patterns` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_patterns(mut self, value: Vec<impl Into<String>>) -> Self {
        self.patterns = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
impl crate::IntoID<crate::Id> for GitRepository {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for GitRepository {
    fn graphql_type() -> &'static str {
        "GitRepository"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<GitRepository> for crate::IdInput<GitRepository> {
    fn from(value: GitRepository) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<GitRepository> for crate::IdInput<super::NodeClient> {
    fn from(value: GitRepository) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl GitRepository {
    #[doc = "Creates a synthetic workspace from this git repository.\n\nSelects GraphQL Wire_Name `asWorkspace` on `GitRepository`."]
    #[must_use]
    pub fn as_workspace(&self) -> super::Workspace {
        let query = self.selection.select("asWorkspace");
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asWorkspace` with a borrowed, reusable `GitRepositoryAsWorkspaceOpts` value."]
    #[must_use]
    pub fn as_workspace_opts(&self, opts: &GitRepositoryAsWorkspaceOpts) -> super::Workspace {
        let query = self.selection.select("asWorkspace");
        let query = if let Some(value) = &opts.cwd {
            query.arg("cwd", value)
        } else {
            query
        };
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns details of a branch.\n\nSelects GraphQL Wire_Name `branch` on `GitRepository`."]
    #[must_use]
    pub fn branch(&self, name: impl Into<String>) -> super::GitRef {
        let query = self.selection.select("branch");
        let query = query.arg("name", name.into());
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "branches that match any of the given glob patterns.\n\nSelects GraphQL Wire_Name `branches` on `GitRepository`."]
    pub async fn branches(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("branches");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `branches` with a borrowed, reusable `GitRepositoryBranchesOpts` value."]
    pub async fn branches_opts(
        &self,
        opts: &GitRepositoryBranchesOpts,
    ) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("branches");
        let query = if let Some(value) = &opts.patterns {
            query.arg("patterns", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Returns details of a commit.\n\nSelects GraphQL Wire_Name `commit` on `GitRepository`."]
    #[must_use]
    pub fn commit(&self, id: impl Into<String>) -> super::GitRef {
        let query = self.selection.select("commit");
        let query = query.arg("id", id.into());
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns details for HEAD.\n\nSelects GraphQL Wire_Name `head` on `GitRepository`."]
    #[must_use]
    pub fn head(&self) -> super::GitRef {
        let query = self.selection.select("head");
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this GitRepository.\n\nSelects GraphQL Wire_Name `id` on `GitRepository`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Returns details for the latest semver tag.\n\nSelects GraphQL Wire_Name `latestVersion` on `GitRepository`."]
    #[must_use]
    pub fn latest_version(&self) -> super::GitRef {
        let query = self.selection.select("latestVersion");
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns details of a ref.\n\nSelects GraphQL Wire_Name `ref` on `GitRepository`."]
    #[must_use]
    pub fn r#ref(&self, name: impl Into<String>) -> super::GitRef {
        let query = self.selection.select("ref");
        let query = query.arg("name", name.into());
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns details of a tag.\n\nSelects GraphQL Wire_Name `tag` on `GitRepository`."]
    #[must_use]
    pub fn tag(&self, name: impl Into<String>) -> super::GitRef {
        let query = self.selection.select("tag");
        let query = query.arg("name", name.into());
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "tags that match any of the given glob patterns.\n\nSelects GraphQL Wire_Name `tags` on `GitRepository`."]
    pub async fn tags(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("tags");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `tags` with a borrowed, reusable `GitRepositoryTagsOpts` value."]
    pub async fn tags_opts(
        &self,
        opts: &GitRepositoryTagsOpts,
    ) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("tags");
        let query = if let Some(value) = &opts.patterns {
            query.arg("patterns", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Returns the changeset of uncommitted changes in the git repository.\n\nSelects GraphQL Wire_Name `uncommitted` on `GitRepository`."]
    #[must_use]
    pub fn uncommitted(&self) -> super::Changeset {
        let query = self.selection.select("uncommitted");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The URL of the git repository.\n\nSelects GraphQL Wire_Name `url` on `GitRepository`."]
    pub async fn url(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("url");
        query.execute(&self.session).await
    }
}
impl super::Node for GitRepository {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
