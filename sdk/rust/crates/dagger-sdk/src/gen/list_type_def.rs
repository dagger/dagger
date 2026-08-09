//! Generated bindings owned by the GraphQL `ListTypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a list type in a Module."]
#[derive(Clone)]
pub struct ListTypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for ListTypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for ListTypeDef {
    fn graphql_type() -> &'static str {
        "ListTypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<ListTypeDef> for crate::IdInput<ListTypeDef> {
    fn from(value: ListTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<ListTypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: ListTypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl ListTypeDef {
    #[doc = "The type of the elements in the list.\n\nSelects GraphQL Wire_Name `elementTypeDef` on `ListTypeDef`."]
    #[must_use]
    pub fn element_type_def(&self) -> super::TypeDef {
        let query = self.selection.select("elementTypeDef");
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this ListTypeDef.\n\nSelects GraphQL Wire_Name `id` on `ListTypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
}
impl super::Node for ListTypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
