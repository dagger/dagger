//! Generated bindings owned by the GraphQL `EngineCacheEntry` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An individual cache entry in a cache entry set"]
#[derive(Clone)]
pub struct EngineCacheEntry {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for EngineCacheEntry {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for EngineCacheEntry {
    fn graphql_type() -> &'static str {
        "EngineCacheEntry"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<EngineCacheEntry> for crate::IdInput<EngineCacheEntry> {
    fn from(value: EngineCacheEntry) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<EngineCacheEntry> for crate::IdInput<super::NodeClient> {
    fn from(value: EngineCacheEntry) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl EngineCacheEntry {
    #[doc = "Whether the cache entry is actively being used.\n\nSelects GraphQL Wire_Name `activelyUsed` on `EngineCacheEntry`."]
    pub async fn actively_used(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("activelyUsed");
        query.execute(&self.session).await
    }
    #[doc = "The time the cache entry was created, in Unix nanoseconds.\n\nSelects GraphQL Wire_Name `createdTimeUnixNano` on `EngineCacheEntry`."]
    pub async fn created_time_unix_nano(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("createdTimeUnixNano");
        query.execute(&self.session).await
    }
    #[doc = "The DagQL call that produced this cache entry.\n\nSelects GraphQL Wire_Name `dagqlCall` on `EngineCacheEntry`."]
    pub async fn dagql_call(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("dagqlCall");
        query.execute(&self.session).await
    }
    #[doc = "The description of the cache entry.\n\nSelects GraphQL Wire_Name `description` on `EngineCacheEntry`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "The disk space used by the cache entry.\n\nSelects GraphQL Wire_Name `diskSpaceBytes` on `EngineCacheEntry`."]
    pub async fn disk_space_bytes(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("diskSpaceBytes");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this EngineCacheEntry.\n\nSelects GraphQL Wire_Name `id` on `EngineCacheEntry`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The most recent time the cache entry was used, in Unix nanoseconds.\n\nSelects GraphQL Wire_Name `mostRecentUseTimeUnixNano` on `EngineCacheEntry`."]
    pub async fn most_recent_use_time_unix_nano(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("mostRecentUseTimeUnixNano");
        query.execute(&self.session).await
    }
    #[doc = "The type of the cache record (e.g. regular, internal, frontend, source.local, source.git.checkout, exec.cachemount).\n\nSelects GraphQL Wire_Name `recordType` on `EngineCacheEntry`."]
    pub async fn record_type(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("recordType");
        query.execute(&self.session).await
    }
    #[doc = "The storage record types represented by this cache entry.\n\nSelects GraphQL Wire_Name `recordTypes` on `EngineCacheEntry`."]
    pub async fn record_types(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("recordTypes");
        query.execute(&self.session).await
    }
}
impl super::Node for EngineCacheEntry {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
