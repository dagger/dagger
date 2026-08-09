//! Generated bindings owned by the GraphQL `GeneratorGroup` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `GeneratorGroup`."]
#[derive(Clone)]
pub struct GeneratorGroup {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `GeneratorGroup.changes`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct GeneratorGroupChangesOpts {
    #[doc = "Strategy to apply on conflicts between generators\n\n`None` omits GraphQL Wire_Name `onConflict` and preserves engine default `Enum(SchemaName(\"FAIL_EARLY\"))`."]
    pub on_conflict: Option<super::ChangesetsMergeConflict>,
}
impl GeneratorGroupChangesOpts {
    #[doc = "Sets GraphQL argument `onConflict` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_on_conflict(mut self, value: super::ChangesetsMergeConflict) -> Self {
        self.on_conflict = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for GeneratorGroup {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for GeneratorGroup {
    fn graphql_type() -> &'static str {
        "GeneratorGroup"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<GeneratorGroup> for crate::IdInput<GeneratorGroup> {
    fn from(value: GeneratorGroup) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<GeneratorGroup> for crate::IdInput<super::NodeClient> {
    fn from(value: GeneratorGroup) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl GeneratorGroup {
    #[doc = "The combined changes from the last run of the generators\n\nIf any conflict occurs, for instance if the same file is modified by multiple generators, or if a file is both modified and deleted, an error is raised and the merge of the changesets will failed.\n\nSet 'continueOnConflicts' flag to force to merge the changes in a 'last write wins' strategy.\n\nSelects GraphQL Wire_Name `changes` on `GeneratorGroup`."]
    #[must_use]
    pub fn changes(&self) -> super::Changeset {
        let query = self.selection.select("changes");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `changes` with a borrowed, reusable `GeneratorGroupChangesOpts` value."]
    #[must_use]
    pub fn changes_opts(&self, opts: &GeneratorGroupChangesOpts) -> super::Changeset {
        let query = self.selection.select("changes");
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
    #[doc = "A unique identifier for this GeneratorGroup.\n\nSelects GraphQL Wire_Name `id` on `GeneratorGroup`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Whether the generated changeset from the last run is empty or not\n\nSelects GraphQL Wire_Name `isEmpty` on `GeneratorGroup`."]
    pub async fn is_empty(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("isEmpty");
        query.execute(&self.session).await
    }
    #[doc = "Return a list of individual generators and their details\n\nSelects GraphQL Wire_Name `list` on `GeneratorGroup`."]
    pub async fn list(&self) -> Result<Vec<super::Generator>, crate::QueryError> {
        let query = self.selection.select("list");
        let query = query.select("id");
        query
            .execute_reentry::<super::Generator, Vec<crate::Id>>(&self.session, "Generator")
            .await
    }
    #[doc = "Load failures tolerated while collecting the generators.\n\nEmpty unless a workspace module could not be loaded during an unscoped 'dagger generate' (no selector), where load failures are tolerated so the modules that do load still generate. Each entry is a human-readable error message. An explicit selector keeps failing hard instead.\n\nSelects GraphQL Wire_Name `loadFailures` on `GeneratorGroup`."]
    pub async fn load_failures(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("loadFailures");
        query.execute(&self.session).await
    }
    #[doc = "Execute all selected generators\n\nSelects GraphQL Wire_Name `run` on `GeneratorGroup`."]
    #[must_use]
    pub fn run(&self) -> super::GeneratorGroup {
        let query = self.selection.select("run");
        super::GeneratorGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for GeneratorGroup {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
