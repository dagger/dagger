//! Generated bindings owned by the GraphQL `InputTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A graphql input type, which is essentially just a group of named args.\nThis is currently only used to represent pre-existing usage of graphql input types\nin the core API. It is not used by user modules and shouldn't ever be as user\nmodule accept input objects via their id rather than graphql input types."]
#[derive(Clone)]
pub struct InputTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for InputTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for InputTypeDef {
    fn graphql_type() -> &'static str {
        "InputTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<InputTypeDef> for crate::IdInput<InputTypeDef> {
    fn from(value: InputTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<InputTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: InputTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl InputTypeDef {
    #[doc = "Static fields defined on this input object, if any.\n\nSelects GraphQL Wire_Name `fields` on `InputTypeDef`."]
    pub async fn fields(&self) -> Result<Vec<super::FieldTypeDef>, crate::QueryError> {
        let query = self.selection.select("fields");
        let query = query.select("id");
        query
            .execute_reentry::<super::FieldTypeDef, Vec<crate::Id>>(&self.session, "FieldTypeDef")
            .await
    }
    #[doc = "A unique identifier for this InputTypeDef.\n\nSelects GraphQL Wire_Name `id` on `InputTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the input object.\n\nSelects GraphQL Wire_Name `name` on `InputTypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
}
impl super::Node for InputTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
