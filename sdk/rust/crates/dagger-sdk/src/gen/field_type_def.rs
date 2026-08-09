//! Generated bindings owned by the GraphQL `FieldTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a field on a custom object defined in a Module.\n\nA field on an object has a static value, as opposed to a function on an object whose value is computed by invoking code (and can accept arguments)."]
#[derive(Clone)]
pub struct FieldTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for FieldTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for FieldTypeDef {
    fn graphql_type() -> &'static str {
        "FieldTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<FieldTypeDef> for crate::IdInput<FieldTypeDef> {
    fn from(value: FieldTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<FieldTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: FieldTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl FieldTypeDef {
    #[doc = "The reason this enum member is deprecated, if any.\n\nSelects GraphQL Wire_Name `deprecated` on `FieldTypeDef`."]
    pub async fn deprecated(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("deprecated");
        query.execute(&self.session).await
    }
    #[doc = "A doc string for the field, if any.\n\nSelects GraphQL Wire_Name `description` on `FieldTypeDef`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this FieldTypeDef.\n\nSelects GraphQL Wire_Name `id` on `FieldTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the field in lowerCamelCase format.\n\nSelects GraphQL Wire_Name `name` on `FieldTypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The location of this field declaration.\n\nSelects GraphQL Wire_Name `sourceMap` on `FieldTypeDef`."]
    pub async fn source_map(&self) -> Result<Option<super::SourceMap>, crate::QueryError> {
        let query = self.selection.select("sourceMap");
        let query = query.select("id");
        query
            .execute_reentry::<super::SourceMap, Option<crate::Id>>(&self.session, "SourceMap")
            .await
    }
    #[doc = "The type of the field.\n\nSelects GraphQL Wire_Name `typeDef` on `FieldTypeDef`."]
    #[must_use]
    pub fn type_def(&self) -> super::TypeDef {
        let query = self.selection.select("typeDef");
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for FieldTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
