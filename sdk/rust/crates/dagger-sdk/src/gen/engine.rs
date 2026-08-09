//! Generated bindings owned by the GraphQL `Engine` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The Dagger engine configuration and state"]
#[derive(Clone)]
pub struct Engine {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Engine {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Engine {
    fn graphql_type() -> &'static str {
        "Engine"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Engine> for crate::IdInput<Engine> {
    fn from(value: Engine) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Engine> for crate::IdInput<super::NodeClient> {
    fn from(value: Engine) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Engine {
    #[doc = "The list of connected client IDs\n\nSelects GraphQL Wire_Name `clients` on `Engine`."]
    pub async fn clients(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("clients");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Engine.\n\nSelects GraphQL Wire_Name `id` on `Engine`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The local engine cache state tracked by dagql\n\nSelects GraphQL Wire_Name `localCache` on `Engine`."]
    #[must_use]
    pub fn local_cache(&self) -> super::EngineCache {
        let query = self.selection.select("localCache");
        super::EngineCache {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The name of the engine instance.\n\nSelects GraphQL Wire_Name `name` on `Engine`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
}
impl super::Node for Engine {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
