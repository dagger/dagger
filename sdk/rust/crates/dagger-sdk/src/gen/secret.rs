//! Generated bindings owned by the GraphQL `Secret` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A reference to a secret value, which can be handled more safely than the value itself."]
#[derive(Clone)]
pub struct Secret {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Secret {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Secret {
    fn graphql_type() -> &'static str {
        "Secret"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Secret> for crate::IdInput<Secret> {
    fn from(value: Secret) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Secret> for crate::IdInput<super::NodeClient> {
    fn from(value: Secret) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Secret {
    #[doc = "A unique identifier for this Secret.\n\nSelects GraphQL Wire_Name `id` on `Secret`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of this secret.\n\nSelects GraphQL Wire_Name `name` on `Secret`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The value of this secret.\n\nSelects GraphQL Wire_Name `plaintext` on `Secret`."]
    pub async fn plaintext(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("plaintext");
        query.execute(&self.session).await
    }
    #[doc = "The URI of this secret.\n\nSelects GraphQL Wire_Name `uri` on `Secret`."]
    pub async fn uri(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("uri");
        query.execute(&self.session).await
    }
}
impl super::Node for Secret {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
