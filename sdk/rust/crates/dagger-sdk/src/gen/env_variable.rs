//! Generated bindings owned by the GraphQL `EnvVariable` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An environment variable name and value."]
#[derive(Clone)]
pub struct EnvVariable {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for EnvVariable {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for EnvVariable {
    fn graphql_type() -> &'static str {
        "EnvVariable"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<EnvVariable> for crate::IdInput<EnvVariable> {
    fn from(value: EnvVariable) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<EnvVariable> for crate::IdInput<super::NodeClient> {
    fn from(value: EnvVariable) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl EnvVariable {
    #[doc = "A unique identifier for this EnvVariable.\n\nSelects GraphQL Wire_Name `id` on `EnvVariable`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The environment variable name.\n\nSelects GraphQL Wire_Name `name` on `EnvVariable`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The environment variable value.\n\nSelects GraphQL Wire_Name `value` on `EnvVariable`."]
    pub async fn value(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("value");
        query.execute(&self.session).await
    }
}
impl super::Node for EnvVariable {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
