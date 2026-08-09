//! Generated bindings owned by the GraphQL `EngineCacheEntrySet` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A set of cache entries returned by a query to a cache"]
#[derive(Clone)]
pub struct EngineCacheEntrySet {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for EngineCacheEntrySet {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for EngineCacheEntrySet {
    fn graphql_type() -> &'static str {
        "EngineCacheEntrySet"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<EngineCacheEntrySet> for crate::IdInput<EngineCacheEntrySet> {
    fn from(value: EngineCacheEntrySet) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<EngineCacheEntrySet> for crate::IdInput<super::NodeClient> {
    fn from(value: EngineCacheEntrySet) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl EngineCacheEntrySet {
    #[doc = "The total disk space used by the cache entries in this set.\n\nSelects GraphQL Wire_Name `diskSpaceBytes` on `EngineCacheEntrySet`."]
    pub async fn disk_space_bytes(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("diskSpaceBytes");
        query.execute(&self.session).await
    }
    #[doc = "The list of individual cache entries in the set\n\nSelects GraphQL Wire_Name `entries` on `EngineCacheEntrySet`."]
    pub async fn entries(&self) -> Result<Vec<super::EngineCacheEntry>, crate::QueryError> {
        let query = self.selection.select("entries");
        let query = query.select("id");
        query
            .execute_reentry::<super::EngineCacheEntry, Vec<crate::Id>>(
                &self.session,
                "EngineCacheEntry",
            )
            .await
    }
    #[doc = "The number of cache entries in this set.\n\nSelects GraphQL Wire_Name `entryCount` on `EngineCacheEntrySet`."]
    pub async fn entry_count(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("entryCount");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this EngineCacheEntrySet.\n\nSelects GraphQL Wire_Name `id` on `EngineCacheEntrySet`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
}
impl super::Node for EngineCacheEntrySet {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
