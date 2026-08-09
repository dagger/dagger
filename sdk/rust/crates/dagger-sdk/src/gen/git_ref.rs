//! Generated bindings owned by the GraphQL `GitRef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A git ref (tag, branch, or commit)."]
#[derive(Clone)]
pub struct GitRef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `GitRef.asWorkspace`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct GitRefAsWorkspaceOpts {
    #[doc = "Current working directory inside the workspace root. Defaults to the workspace root.\n\n`None` omits GraphQL Wire_Name `cwd` and preserves engine default `String(\"/\")`."]
    pub cwd: Option<String>,
}
impl GitRefAsWorkspaceOpts {
    #[doc = "Sets GraphQL argument `cwd` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cwd(mut self, value: impl Into<String>) -> Self {
        self.cwd = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `GitRef.tree`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct GitRefTreeOpts {
    #[doc = "The depth of the tree to fetch.\n\n`None` omits GraphQL Wire_Name `depth` and preserves engine default `Int(1)`."]
    pub depth: Option<i64>,
    #[doc = "Set to true to discard .git directory.\n\n`None` omits GraphQL Wire_Name `discardGitDir` and preserves engine default `Boolean(false)`."]
    pub discard_git_dir: Option<bool>,
    #[doc = "Set to true to populate tag refs in the local checkout .git.\n\n`None` omits GraphQL Wire_Name `includeTags` and preserves engine default `Boolean(false)`."]
    pub include_tags: Option<bool>,
}
impl GitRefTreeOpts {
    #[doc = "Sets GraphQL argument `depth` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_depth(mut self, value: i64) -> Self {
        self.depth = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `discardGitDir` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_discard_git_dir(mut self, value: bool) -> Self {
        self.discard_git_dir = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `includeTags` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include_tags(mut self, value: bool) -> Self {
        self.include_tags = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for GitRef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for GitRef {
    fn graphql_type() -> &'static str {
        "GitRef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<GitRef> for crate::IdInput<GitRef> {
    fn from(value: GitRef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<GitRef> for crate::IdInput<super::NodeClient> {
    fn from(value: GitRef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl GitRef {
    #[doc = "Creates a synthetic workspace from this git ref.\n\nSelects GraphQL Wire_Name `asWorkspace` on `GitRef`."]
    #[must_use]
    pub fn as_workspace(&self) -> super::Workspace {
        let query = self.selection.select("asWorkspace");
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asWorkspace` with a borrowed, reusable `GitRefAsWorkspaceOpts` value."]
    #[must_use]
    pub fn as_workspace_opts(&self, opts: &GitRefAsWorkspaceOpts) -> super::Workspace {
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
    #[doc = "The resolved commit id at this ref.\n\nSelects GraphQL Wire_Name `commit` on `GitRef`."]
    pub async fn commit(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("commit");
        query.execute(&self.session).await
    }
    #[doc = "Find the best common ancestor between this ref and another ref.\n\nSelects GraphQL Wire_Name `commonAncestor` on `GitRef`."]
    #[must_use]
    pub fn common_ancestor(
        &self,
        other: impl Into<crate::IdInput<super::GitRef>>,
    ) -> super::GitRef {
        let query = self.selection.select("commonAncestor");
        let query = query.arg_id_input("other", other.into());
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this GitRef.\n\nSelects GraphQL Wire_Name `id` on `GitRef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The resolved ref name at this ref.\n\nSelects GraphQL Wire_Name `ref` on `GitRef`."]
    pub async fn r#ref(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("ref");
        query.execute(&self.session).await
    }
    #[doc = "The filesystem tree at this ref.\n\nSelects GraphQL Wire_Name `tree` on `GitRef`."]
    #[must_use]
    pub fn tree(&self) -> super::Directory {
        let query = self.selection.select("tree");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `tree` with a borrowed, reusable `GitRefTreeOpts` value."]
    #[must_use]
    pub fn tree_opts(&self, opts: &GitRefTreeOpts) -> super::Directory {
        let query = self.selection.select("tree");
        let query = if let Some(value) = &opts.depth {
            query.arg("depth", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.discard_git_dir {
            query.arg("discardGitDir", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.include_tags {
            query.arg("includeTags", value)
        } else {
            query
        };
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for GitRef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
