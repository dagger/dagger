//! Generated bindings owned by the GraphQL `Check` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `Check`."]
#[derive(Clone)]
pub struct Check {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Check {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Check {
    fn graphql_type() -> &'static str {
        "Check"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Check> for crate::IdInput<Check> {
    fn from(value: Check) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Check> for crate::IdInput<super::NodeClient> {
    fn from(value: Check) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Check {
    #[doc = "The type of check: 'check' for annotated checks, 'generate' for generate-as-checks\n\nSelects GraphQL Wire_Name `checkType` on `Check`."]
    pub async fn check_type(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("checkType");
        query.execute(&self.session).await
    }
    #[doc = "Whether the check completed\n\nSelects GraphQL Wire_Name `completed` on `Check`."]
    pub async fn completed(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("completed");
        query.execute(&self.session).await
    }
    #[doc = "The description of the check\n\nSelects GraphQL Wire_Name `description` on `Check`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "If the check failed, this is the error\n\nSelects GraphQL Wire_Name `error` on `Check`."]
    pub async fn error(&self) -> Result<Option<super::Error>, crate::QueryError> {
        let query = self.selection.select("error");
        let query = query.select("id");
        query
            .execute_reentry::<super::Error, Option<crate::Id>>(&self.session, "Error")
            .await
    }
    #[doc = "A unique identifier for this Check.\n\nSelects GraphQL Wire_Name `id` on `Check`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Return the fully qualified name of the check\n\nSelects GraphQL Wire_Name `name` on `Check`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The original module in which the check has been defined\n\nSelects GraphQL Wire_Name `originalModule` on `Check`."]
    #[must_use]
    pub fn original_module(&self) -> super::Module {
        let query = self.selection.select("originalModule");
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Whether the check passed\n\nSelects GraphQL Wire_Name `passed` on `Check`."]
    pub async fn passed(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("passed");
        query.execute(&self.session).await
    }
    #[doc = "The path of the check within its module\n\nSelects GraphQL Wire_Name `path` on `Check`."]
    pub async fn path(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("path");
        query.execute(&self.session).await
    }
    #[doc = "An emoji representing the result of the check\n\nSelects GraphQL Wire_Name `resultEmoji` on `Check`."]
    pub async fn result_emoji(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("resultEmoji");
        query.execute(&self.session).await
    }
    #[doc = "Execute the check\n\nSelects GraphQL Wire_Name `run` on `Check`."]
    #[must_use]
    pub fn run(&self) -> super::Check {
        let query = self.selection.select("run");
        super::Check {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Check {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
