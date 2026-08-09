//! Generated bindings owned by the GraphQL `Schema` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A GraphQL introspection schema that can be inspected and merged."]
#[derive(Clone)]
pub struct Schema {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Schema {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Schema {
    fn graphql_type() -> &'static str {
        "Schema"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Schema> for crate::IdInput<Schema> {
    fn from(value: Schema) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Schema> for crate::IdInput<super::NodeClient> {
    fn from(value: Schema) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Schema {
    #[doc = "Serialize the schema back to introspection JSON.\n\nSelects GraphQL Wire_Name `contents` on `Schema`."]
    pub async fn contents(&self) -> Result<crate::Json, crate::QueryError> {
        let query = self.selection.select("contents");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Schema.\n\nSelects GraphQL Wire_Name `id` on `Schema`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Merge a module's introspection-shaped type definitions into the schema, returning the combined schema.\n\nSelects GraphQL Wire_Name `merge` on `Schema`."]
    #[must_use]
    pub fn merge(
        &self,
        module_name: impl Into<String>,
        module_types: crate::Json,
    ) -> super::Schema {
        let query = self.selection.select("merge");
        let query = query.arg("moduleName", module_name.into());
        let query = query.arg("moduleTypes", module_types);
        super::Schema {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Schema {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
