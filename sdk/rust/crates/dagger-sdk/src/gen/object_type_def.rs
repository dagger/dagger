//! Generated bindings owned by the GraphQL `ObjectTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a custom object defined in a Module."]
#[derive(Clone)]
pub struct ObjectTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for ObjectTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for ObjectTypeDef {
    fn graphql_type() -> &'static str {
        "ObjectTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<ObjectTypeDef> for crate::IdInput<ObjectTypeDef> {
    fn from(value: ObjectTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<ObjectTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: ObjectTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl ObjectTypeDef {
    #[doc = "The function used to construct new instances of this object, if any.\n\nSelects GraphQL Wire_Name `constructor` on `ObjectTypeDef`."]
    pub async fn constructor(&self) -> Result<Option<super::Function>, crate::QueryError> {
        let query = self.selection.select("constructor");
        let query = query.select("id");
        query
            .execute_reentry::<super::Function, Option<crate::Id>>(&self.session, "Function")
            .await
    }
    #[doc = "The reason this enum member is deprecated, if any.\n\nSelects GraphQL Wire_Name `deprecated` on `ObjectTypeDef`."]
    pub async fn deprecated(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("deprecated");
        query.execute(&self.session).await
    }
    #[doc = "The doc string for the object, if any.\n\nSelects GraphQL Wire_Name `description` on `ObjectTypeDef`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "Static fields defined on this object, if any.\n\nSelects GraphQL Wire_Name `fields` on `ObjectTypeDef`."]
    pub async fn fields(&self) -> Result<Vec<super::FieldTypeDef>, crate::QueryError> {
        let query = self.selection.select("fields");
        let query = query.select("id");
        query
            .execute_reentry::<super::FieldTypeDef, Vec<crate::Id>>(&self.session, "FieldTypeDef")
            .await
    }
    #[doc = "Functions defined on this object, if any.\n\nSelects GraphQL Wire_Name `functions` on `ObjectTypeDef`."]
    pub async fn functions(&self) -> Result<Vec<super::Function>, crate::QueryError> {
        let query = self.selection.select("functions");
        let query = query.select("id");
        query
            .execute_reentry::<super::Function, Vec<crate::Id>>(&self.session, "Function")
            .await
    }
    #[doc = "A unique identifier for this ObjectTypeDef.\n\nSelects GraphQL Wire_Name `id` on `ObjectTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the object.\n\nSelects GraphQL Wire_Name `name` on `ObjectTypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The location of this object declaration.\n\nSelects GraphQL Wire_Name `sourceMap` on `ObjectTypeDef`."]
    pub async fn source_map(&self) -> Result<Option<super::SourceMap>, crate::QueryError> {
        let query = self.selection.select("sourceMap");
        let query = query.select("id");
        query
            .execute_reentry::<super::SourceMap, Option<crate::Id>>(&self.session, "SourceMap")
            .await
    }
    #[doc = "If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise.\n\nSelects GraphQL Wire_Name `sourceModuleName` on `ObjectTypeDef`."]
    pub async fn source_module_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(&self.session).await
    }
}
impl super::Node for ObjectTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
