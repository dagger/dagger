//! Generated bindings owned by the GraphQL `EnumTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a custom enum defined in a Module."]
#[derive(Clone)]
pub struct EnumTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for EnumTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for EnumTypeDef {
    fn graphql_type() -> &'static str {
        "EnumTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<EnumTypeDef> for crate::IdInput<EnumTypeDef> {
    fn from(value: EnumTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<EnumTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: EnumTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl EnumTypeDef {
    #[doc = "A doc string for the enum, if any.\n\nSelects GraphQL Wire_Name `description` on `EnumTypeDef`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this EnumTypeDef.\n\nSelects GraphQL Wire_Name `id` on `EnumTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The members of the enum.\n\nSelects GraphQL Wire_Name `members` on `EnumTypeDef`."]
    pub async fn members(&self) -> Result<Vec<super::EnumValueTypeDef>, crate::QueryError> {
        let query = self.selection.select("members");
        let query = query.select("id");
        query
            .execute_reentry::<super::EnumValueTypeDef, Vec<crate::Id>>(
                &self.session,
                "EnumValueTypeDef",
            )
            .await
    }
    #[doc = "The name of the enum.\n\nSelects GraphQL Wire_Name `name` on `EnumTypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The location of this enum declaration.\n\nSelects GraphQL Wire_Name `sourceMap` on `EnumTypeDef`."]
    pub async fn source_map(&self) -> Result<Option<super::SourceMap>, crate::QueryError> {
        let query = self.selection.select("sourceMap");
        let query = query.select("id");
        query
            .execute_reentry::<super::SourceMap, Option<crate::Id>>(&self.session, "SourceMap")
            .await
    }
    #[doc = "If this EnumTypeDef is associated with a Module, the name of the module. Unset otherwise.\n\nSelects GraphQL Wire_Name `sourceModuleName` on `EnumTypeDef`."]
    pub async fn source_module_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(&self.session).await
    }
    #[doc = "The members of the enum.\n\nSelects GraphQL Wire_Name `values` on `EnumTypeDef`.\n\n**Deprecated:** use members instead"]
    #[deprecated(note = "use members instead")]
    pub async fn values(&self) -> Result<Vec<super::EnumValueTypeDef>, crate::QueryError> {
        let query = self.selection.select("values");
        let query = query.select("id");
        query
            .execute_reentry::<super::EnumValueTypeDef, Vec<crate::Id>>(
                &self.session,
                "EnumValueTypeDef",
            )
            .await
    }
}
impl super::Node for EnumTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
