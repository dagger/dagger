//! Generated bindings owned by the GraphQL `Changeset` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A comparison between two directories representing changes that can be applied."]
#[derive(Clone)]
pub struct Changeset {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Changeset.withChangeset`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ChangesetWithChangesetOpts {
    #[doc = "What to do on a merge conflict\n\n`None` omits GraphQL Wire_Name `onConflict` and preserves engine default `Enum(SchemaName(\"FAIL\"))`."]
    pub on_conflict: Option<super::ChangesetMergeConflict>,
}
impl ChangesetWithChangesetOpts {
    #[doc = "Sets GraphQL argument `onConflict` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_on_conflict(mut self, value: super::ChangesetMergeConflict) -> Self {
        self.on_conflict = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Changeset.withChangesets`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ChangesetWithChangesetsOpts {
    #[doc = "What to do on a merge conflict\n\n`None` omits GraphQL Wire_Name `onConflict` and preserves engine default `Enum(SchemaName(\"FAIL\"))`."]
    pub on_conflict: Option<super::ChangesetsMergeConflict>,
}
impl ChangesetWithChangesetsOpts {
    #[doc = "Sets GraphQL argument `onConflict` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_on_conflict(mut self, value: super::ChangesetsMergeConflict) -> Self {
        self.on_conflict = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for Changeset {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Changeset {
    fn graphql_type() -> &'static str {
        "Changeset"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Changeset> for crate::IdInput<Changeset> {
    fn from(value: Changeset) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Changeset> for crate::IdInput<super::ExportableClient> {
    fn from(value: Changeset) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Changeset> for crate::IdInput<super::NodeClient> {
    fn from(value: Changeset) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Changeset> for crate::IdInput<super::SyncerClient> {
    fn from(value: Changeset) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Changeset {
    #[doc = "Files and directories that were added in the newer directory.\n\nSelects GraphQL Wire_Name `addedPaths` on `Changeset`."]
    pub async fn added_paths(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("addedPaths");
        query.execute(&self.session).await
    }
    #[doc = "The newer/upper snapshot.\n\nSelects GraphQL Wire_Name `after` on `Changeset`."]
    #[must_use]
    pub fn after(&self) -> super::Directory {
        let query = self.selection.select("after");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a Git-compatible patch of the changes\n\nSelects GraphQL Wire_Name `asPatch` on `Changeset`."]
    #[must_use]
    pub fn as_patch(&self) -> super::File {
        let query = self.selection.select("asPatch");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The older/lower snapshot to compare against.\n\nSelects GraphQL Wire_Name `before` on `Changeset`."]
    #[must_use]
    pub fn before(&self) -> super::Directory {
        let query = self.selection.select("before");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Structured per-path diff statistics (kind and line counts) for this changeset.\n\nSelects GraphQL Wire_Name `diffStats` on `Changeset`."]
    pub async fn diff_stats(&self) -> Result<Vec<super::DiffStat>, crate::QueryError> {
        let query = self.selection.select("diffStats");
        let query = query.select("id");
        query
            .execute_reentry::<super::DiffStat, Vec<crate::Id>>(&self.session, "DiffStat")
            .await
    }
    #[doc = "Applies the diff represented by this changeset to a path on the host.\n\nSelects GraphQL Wire_Name `export` on `Changeset`."]
    pub async fn export(&self, path: impl Into<String>) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Changeset.\n\nSelects GraphQL Wire_Name `id` on `Changeset`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Returns true if the changeset is empty (i.e. there are no changes).\n\nSelects GraphQL Wire_Name `isEmpty` on `Changeset`."]
    pub async fn is_empty(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("isEmpty");
        query.execute(&self.session).await
    }
    #[doc = "Return a snapshot containing only the created and modified files\n\nSelects GraphQL Wire_Name `layer` on `Changeset`."]
    #[must_use]
    pub fn layer(&self) -> super::Directory {
        let query = self.selection.select("layer");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Files and directories that existed before and were updated in the newer directory.\n\nSelects GraphQL Wire_Name `modifiedPaths` on `Changeset`."]
    pub async fn modified_paths(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("modifiedPaths");
        query.execute(&self.session).await
    }
    #[doc = "Files and directories that were removed. Directories are indicated by a trailing slash, and their child paths are not included.\n\nSelects GraphQL Wire_Name `removedPaths` on `Changeset`."]
    pub async fn removed_paths(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("removedPaths");
        query.execute(&self.session).await
    }
    #[doc = "Force evaluation in the engine.\n\nSelects GraphQL Wire_Name `sync` on `Changeset`."]
    pub async fn sync(&self) -> Result<super::Changeset, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Changeset>(
            &self.session,
            id,
            "Changeset",
        ))
    }
    #[doc = "Add changes to an existing changeset\n\nBy default the operation will fail in case of conflicts, for instance a file modified in both changesets. The behavior can be adjusted using onConflict argument\n\nSelects GraphQL Wire_Name `withChangeset` on `Changeset`."]
    #[must_use]
    pub fn with_changeset(
        &self,
        changes: impl Into<crate::IdInput<super::Changeset>>,
    ) -> super::Changeset {
        let query = self.selection.select("withChangeset");
        let query = query.arg_id_input("changes", changes.into());
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withChangeset` with a borrowed, reusable `ChangesetWithChangesetOpts` value."]
    #[must_use]
    pub fn with_changeset_opts(
        &self,
        changes: impl Into<crate::IdInput<super::Changeset>>,
        opts: &ChangesetWithChangesetOpts,
    ) -> super::Changeset {
        let query = self.selection.select("withChangeset");
        let query = query.arg_id_input("changes", changes.into());
        let query = if let Some(value) = &opts.on_conflict {
            query.arg("onConflict", value)
        } else {
            query
        };
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Add changes from multiple changesets using git octopus merge strategy\n\nThis is more efficient than chaining multiple withChangeset calls when merging many changesets.\n\nOnly FAIL and FAIL_EARLY conflict strategies are supported (octopus merge cannot use -X ours/theirs).\n\nSelects GraphQL Wire_Name `withChangesets` on `Changeset`."]
    #[must_use]
    pub fn with_changesets(
        &self,
        changes: Vec<crate::IdInput<super::Changeset>>,
    ) -> super::Changeset {
        let query = self.selection.select("withChangesets");
        let query = query.arg_id_input("changes", changes);
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withChangesets` with a borrowed, reusable `ChangesetWithChangesetsOpts` value."]
    #[must_use]
    pub fn with_changesets_opts(
        &self,
        changes: Vec<crate::IdInput<super::Changeset>>,
        opts: &ChangesetWithChangesetsOpts,
    ) -> super::Changeset {
        let query = self.selection.select("withChangesets");
        let query = query.arg_id_input("changes", changes);
        let query = if let Some(value) = &opts.on_conflict {
            query.arg("onConflict", value)
        } else {
            query
        };
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Exportable for Changeset {
    fn export(
        &self,
        path: impl Into<String> + Send,
    ) -> impl core::future::Future<Output = Result<String, crate::QueryError>> + Send {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Node for Changeset {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for Changeset {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
    fn sync(
        &self,
    ) -> impl core::future::Future<Output = Result<super::SyncerClient, crate::QueryError>> + Send
    {
        let query = self.selection.select("sync");
        let session = self.session.clone();
        async move {
            let id: crate::Id = query.execute(&session).await?;
            Ok(crate::query::reenter::<super::SyncerClient>(
                &session, id, "Syncer",
            ))
        }
    }
}
