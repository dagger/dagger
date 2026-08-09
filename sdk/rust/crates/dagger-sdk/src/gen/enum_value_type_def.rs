//! Generated bindings owned by the GraphQL `EnumValueTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a value in a custom enum defined in a Module."]
#[derive(Clone)]
pub struct EnumValueTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for EnumValueTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for EnumValueTypeDef {
    fn graphql_type() -> &'static str {
        "EnumValueTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<EnumValueTypeDef> for crate::IdInput<EnumValueTypeDef> {
    fn from(value: EnumValueTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<EnumValueTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: EnumValueTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl EnumValueTypeDef {
    #[doc = "The reason this enum member is deprecated, if any.\n\nSelects GraphQL Wire_Name `deprecated` on `EnumValueTypeDef`."]
    pub async fn deprecated(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("deprecated");
        query.execute(&self.session).await
    }
    #[doc = "A doc string for the enum member, if any.\n\nSelects GraphQL Wire_Name `description` on `EnumValueTypeDef`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this EnumValueTypeDef.\n\nSelects GraphQL Wire_Name `id` on `EnumValueTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the enum member.\n\nSelects GraphQL Wire_Name `name` on `EnumValueTypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The location of this enum member declaration.\n\nSelects GraphQL Wire_Name `sourceMap` on `EnumValueTypeDef`."]
    pub async fn source_map(&self) -> Result<Option<super::SourceMap>, crate::QueryError> {
        let query = self.selection.select("sourceMap");
        let query = query.select("id");
        query
            .execute_reentry::<super::SourceMap, Option<crate::Id>>(&self.session, "SourceMap")
            .await
    }
    #[doc = "The value of the enum member\n\nSelects GraphQL Wire_Name `value` on `EnumValueTypeDef`."]
    pub async fn value(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("value");
        query.execute(&self.session).await
    }
}
impl super::Node for EnumValueTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
