//! Generated bindings owned by the GraphQL `CheckGroup` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `CheckGroup`."]
#[derive(Clone)]
pub struct CheckGroup {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `CheckGroup.run`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct CheckGroupRunOpts {
    #[doc = "If true, stop running checks as soon as any check fails.\n\n`None` omits GraphQL Wire_Name `failFast`."]
    pub fail_fast: Option<bool>,
}
impl CheckGroupRunOpts {
    #[doc = "Sets GraphQL argument `failFast` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_fail_fast(mut self, value: bool) -> Self {
        self.fail_fast = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for CheckGroup {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for CheckGroup {
    fn graphql_type() -> &'static str {
        "CheckGroup"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<CheckGroup> for crate::IdInput<CheckGroup> {
    fn from(value: CheckGroup) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<CheckGroup> for crate::IdInput<super::NodeClient> {
    fn from(value: CheckGroup) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl CheckGroup {
    #[doc = "A unique identifier for this CheckGroup.\n\nSelects GraphQL Wire_Name `id` on `CheckGroup`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Return a list of individual checks and their details\n\nSelects GraphQL Wire_Name `list` on `CheckGroup`."]
    pub async fn list(&self) -> Result<Vec<super::Check>, crate::QueryError> {
        let query = self.selection.select("list");
        let query = query.select("id");
        query
            .execute_reentry::<super::Check, Vec<crate::Id>>(&self.session, "Check")
            .await
    }
    #[doc = "Generate a markdown report\n\nSelects GraphQL Wire_Name `report` on `CheckGroup`."]
    #[must_use]
    pub fn report(&self) -> super::File {
        let query = self.selection.select("report");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Execute all selected checks\n\nSelects GraphQL Wire_Name `run` on `CheckGroup`."]
    #[must_use]
    pub fn run(&self) -> super::CheckGroup {
        let query = self.selection.select("run");
        super::CheckGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `run` with a borrowed, reusable `CheckGroupRunOpts` value."]
    #[must_use]
    pub fn run_opts(&self, opts: &CheckGroupRunOpts) -> super::CheckGroup {
        let query = self.selection.select("run");
        let query = if let Some(value) = &opts.fail_fast {
            query.arg("failFast", value)
        } else {
            query
        };
        super::CheckGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for CheckGroup {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
