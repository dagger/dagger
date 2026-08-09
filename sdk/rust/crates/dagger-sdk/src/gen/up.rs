//! Generated bindings owned by the GraphQL `Up` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `Up`."]
#[derive(Clone)]
pub struct Up {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Up {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Up {
    fn graphql_type() -> &'static str {
        "Up"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Up> for crate::IdInput<Up> {
    fn from(value: Up) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Up> for crate::IdInput<super::NodeClient> {
    fn from(value: Up) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Up {
    #[doc = "The description of the service\n\nSelects GraphQL Wire_Name `description` on `Up`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Up.\n\nSelects GraphQL Wire_Name `id` on `Up`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Return the fully qualified name of the service\n\nSelects GraphQL Wire_Name `name` on `Up`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The original module in which the service has been defined\n\nSelects GraphQL Wire_Name `originalModule` on `Up`."]
    #[must_use]
    pub fn original_module(&self) -> super::Module {
        let query = self.selection.select("originalModule");
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The path of the service within its module\n\nSelects GraphQL Wire_Name `path` on `Up`."]
    pub async fn path(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("path");
        query.execute(&self.session).await
    }
    #[doc = "Execute the service function\n\nSelects GraphQL Wire_Name `run` on `Up`."]
    #[must_use]
    pub fn run(&self) -> super::Up {
        let query = self.selection.select("run");
        super::Up {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Up {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
