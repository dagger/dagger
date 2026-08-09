//! Generated bindings owned by the GraphQL `Generator` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `Generator`."]
#[derive(Clone)]
pub struct Generator {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Generator {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Generator {
    fn graphql_type() -> &'static str {
        "Generator"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Generator> for crate::IdInput<Generator> {
    fn from(value: Generator) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Generator> for crate::IdInput<super::NodeClient> {
    fn from(value: Generator) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Generator {
    #[doc = "The generated changeset from the last run\n\nSelects GraphQL Wire_Name `changes` on `Generator`."]
    #[must_use]
    pub fn changes(&self) -> super::Changeset {
        let query = self.selection.select("changes");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Whether the generator complete\n\nSelects GraphQL Wire_Name `completed` on `Generator`."]
    pub async fn completed(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("completed");
        query.execute(&self.session).await
    }
    #[doc = "Return the description of the generator\n\nSelects GraphQL Wire_Name `description` on `Generator`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Generator.\n\nSelects GraphQL Wire_Name `id` on `Generator`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Whether changeset from the last generator run is empty or not\n\nSelects GraphQL Wire_Name `isEmpty` on `Generator`."]
    pub async fn is_empty(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("isEmpty");
        query.execute(&self.session).await
    }
    #[doc = "Return the fully qualified name of the generator\n\nSelects GraphQL Wire_Name `name` on `Generator`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The original module in which the generator has been defined\n\nSelects GraphQL Wire_Name `originalModule` on `Generator`."]
    #[must_use]
    pub fn original_module(&self) -> super::Module {
        let query = self.selection.select("originalModule");
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The path of the generator within its module\n\nSelects GraphQL Wire_Name `path` on `Generator`."]
    pub async fn path(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("path");
        query.execute(&self.session).await
    }
    #[doc = "Execute the generator\n\nSelects GraphQL Wire_Name `run` on `Generator`."]
    #[must_use]
    pub fn run(&self) -> super::Generator {
        let query = self.selection.select("run");
        super::Generator {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Generator {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
