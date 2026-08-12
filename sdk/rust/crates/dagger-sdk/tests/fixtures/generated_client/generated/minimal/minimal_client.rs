//! Generated bindings owned by GraphQL coordinate `MinimalClient`.
// @generated {"format":"dagger-rust-standalone-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:9ee1f1eeccf3db6eacb6690f6c097fe6ff27d366c4598025d7df1ddc99134787","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Client fixture object MinimalClient."]
#[derive(Clone)]
pub struct MinimalClient {
    query: dagger_sdk::QueryBuilder,
}
impl MinimalClient {
    #[must_use]
    pub(in crate::dagger_client) fn from_query(query: dagger_sdk::QueryBuilder) -> Self {
        Self { query }
    }
    /// Borrows the immutable query represented by this handle.
    #[must_use]
    pub fn selection(&self) -> &dagger_sdk::QueryBuilder {
        &self.query
    }
    #[doc = "Client fixture field id."]
    pub async fn id(&self) -> Result<dagger_sdk::Id, dagger_sdk::QueryError> {
        let query = self.query.select("id");
        query.execute().await
    }
}
impl dagger_sdk::IntoID<dagger_sdk::Id> for MinimalClient {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<
            dyn core::future::Future<Output = Result<dagger_sdk::Id, dagger_sdk::QueryError>>
                + Send,
        >,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl From<MinimalClient> for dagger_sdk::IdInput<MinimalClient> {
    fn from(value: MinimalClient) -> Self {
        dagger_sdk::IdInput::generated_lazy(value)
    }
}
