//! Generated bindings owned by the GraphQL `ScalarTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a custom scalar defined in a Module."]
#[derive(Clone)]
pub struct ScalarTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for ScalarTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for ScalarTypeDef {
    fn graphql_type() -> &'static str {
        "ScalarTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<ScalarTypeDef> for crate::IdInput<ScalarTypeDef> {
    fn from(value: ScalarTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<ScalarTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: ScalarTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl ScalarTypeDef {
    #[doc = "A doc string for the scalar, if any.\n\nSelects GraphQL Wire_Name `description` on `ScalarTypeDef`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this ScalarTypeDef.\n\nSelects GraphQL Wire_Name `id` on `ScalarTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the scalar.\n\nSelects GraphQL Wire_Name `name` on `ScalarTypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "If this ScalarTypeDef is associated with a Module, the name of the module. Unset otherwise.\n\nSelects GraphQL Wire_Name `sourceModuleName` on `ScalarTypeDef`."]
    pub async fn source_module_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(&self.session).await
    }
}
impl super::Node for ScalarTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
