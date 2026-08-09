//! Generated bindings owned by the GraphQL `InterfaceTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a custom interface defined in a Module."]
#[derive(Clone)]
pub struct InterfaceTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for InterfaceTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for InterfaceTypeDef {
    fn graphql_type() -> &'static str {
        "InterfaceTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<InterfaceTypeDef> for crate::IdInput<InterfaceTypeDef> {
    fn from(value: InterfaceTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<InterfaceTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: InterfaceTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl InterfaceTypeDef {
    #[doc = "The doc string for the interface, if any.\n\nSelects GraphQL Wire_Name `description` on `InterfaceTypeDef`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "Functions defined on this interface, if any.\n\nSelects GraphQL Wire_Name `functions` on `InterfaceTypeDef`."]
    pub async fn functions(&self) -> Result<Vec<super::Function>, crate::QueryError> {
        let query = self.selection.select("functions");
        let query = query.select("id");
        query
            .execute_reentry::<super::Function, Vec<crate::Id>>(&self.session, "Function")
            .await
    }
    #[doc = "A unique identifier for this InterfaceTypeDef.\n\nSelects GraphQL Wire_Name `id` on `InterfaceTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the interface.\n\nSelects GraphQL Wire_Name `name` on `InterfaceTypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The location of this interface declaration.\n\nSelects GraphQL Wire_Name `sourceMap` on `InterfaceTypeDef`."]
    pub async fn source_map(&self) -> Result<Option<super::SourceMap>, crate::QueryError> {
        let query = self.selection.select("sourceMap");
        let query = query.select("id");
        query
            .execute_reentry::<super::SourceMap, Option<crate::Id>>(&self.session, "SourceMap")
            .await
    }
    #[doc = "If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise.\n\nSelects GraphQL Wire_Name `sourceModuleName` on `InterfaceTypeDef`."]
    pub async fn source_module_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(&self.session).await
    }
}
impl super::Node for InterfaceTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
