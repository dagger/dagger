//! Generated bindings owned by the GraphQL `EngineCache` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A cache storage for the Dagger engine"]
#[derive(Clone)]
pub struct EngineCache {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `EngineCache.entrySet`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct EngineCacheEntrySetOpts {
    #[doc = "`None` omits GraphQL Wire_Name `key` and preserves engine default `String(\"\")`."]
    pub key: Option<String>,
}
impl EngineCacheEntrySetOpts {
    #[doc = "Sets GraphQL argument `key` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_key(mut self, value: impl Into<String>) -> Self {
        self.key = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `EngineCache.prune`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct EngineCachePruneOpts {
    #[doc = "Override the maximum disk space to keep before pruning (e.g. \"200GB\" or \"80%\").\n\n`None` omits GraphQL Wire_Name `maxUsedSpace` and preserves engine default `String(\"\")`."]
    pub max_used_space: Option<String>,
    #[doc = "Override the minimum free disk space target during pruning (e.g. \"20GB\" or \"20%\").\n\n`None` omits GraphQL Wire_Name `minFreeSpace` and preserves engine default `String(\"\")`."]
    pub min_free_space: Option<String>,
    #[doc = "Override the minimum disk space to retain during pruning (e.g. \"500GB\" or \"10%\").\n\n`None` omits GraphQL Wire_Name `reservedSpace` and preserves engine default `String(\"\")`."]
    pub reserved_space: Option<String>,
    #[doc = "Override the target disk space to keep after pruning (e.g. \"200GB\" or \"50%\").\n\n`None` omits GraphQL Wire_Name `targetSpace` and preserves engine default `String(\"\")`."]
    pub target_space: Option<String>,
    #[doc = "Use the engine-wide default pruning policy if true, otherwise prune the whole cache of any releasable entries.\n\n`None` omits GraphQL Wire_Name `useDefaultPolicy` and preserves engine default `Boolean(false)`."]
    pub use_default_policy: Option<bool>,
}
impl EngineCachePruneOpts {
    #[doc = "Sets GraphQL argument `maxUsedSpace` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_max_used_space(mut self, value: impl Into<String>) -> Self {
        self.max_used_space = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `minFreeSpace` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_min_free_space(mut self, value: impl Into<String>) -> Self {
        self.min_free_space = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `reservedSpace` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_reserved_space(mut self, value: impl Into<String>) -> Self {
        self.reserved_space = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `targetSpace` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_target_space(mut self, value: impl Into<String>) -> Self {
        self.target_space = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `useDefaultPolicy` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_use_default_policy(mut self, value: bool) -> Self {
        self.use_default_policy = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for EngineCache {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for EngineCache {
    fn graphql_type() -> &'static str {
        "EngineCache"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<EngineCache> for crate::IdInput<EngineCache> {
    fn from(value: EngineCache) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<EngineCache> for crate::IdInput<super::NodeClient> {
    fn from(value: EngineCache) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl EngineCache {
    #[doc = "The current set of entries in the cache\n\nSelects GraphQL Wire_Name `entrySet` on `EngineCache`."]
    #[must_use]
    pub fn entry_set(&self) -> super::EngineCacheEntrySet {
        let query = self.selection.select("entrySet");
        super::EngineCacheEntrySet {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `entrySet` with a borrowed, reusable `EngineCacheEntrySetOpts` value."]
    #[must_use]
    pub fn entry_set_opts(&self, opts: &EngineCacheEntrySetOpts) -> super::EngineCacheEntrySet {
        let query = self.selection.select("entrySet");
        let query = if let Some(value) = &opts.key {
            query.arg("key", value)
        } else {
            query
        };
        super::EngineCacheEntrySet {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this EngineCache.\n\nSelects GraphQL Wire_Name `id` on `EngineCache`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The maximum bytes to keep in the cache without pruning.\n\nSelects GraphQL Wire_Name `maxUsedSpace` on `EngineCache`."]
    pub async fn max_used_space(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("maxUsedSpace");
        query.execute(&self.session).await
    }
    #[doc = "The target amount of free disk space the garbage collector will attempt to leave.\n\nSelects GraphQL Wire_Name `minFreeSpace` on `EngineCache`."]
    pub async fn min_free_space(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("minFreeSpace");
        query.execute(&self.session).await
    }
    #[doc = "Prune the cache of releaseable entries\n\nSelects GraphQL Wire_Name `prune` on `EngineCache`."]
    pub async fn prune(&self) -> Result<(), crate::QueryError> {
        let query = self.selection.select("prune");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `prune` with a borrowed, reusable `EngineCachePruneOpts` value."]
    pub async fn prune_opts(&self, opts: &EngineCachePruneOpts) -> Result<(), crate::QueryError> {
        let query = self.selection.select("prune");
        let query = if let Some(value) = &opts.max_used_space {
            query.arg("maxUsedSpace", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.min_free_space {
            query.arg("minFreeSpace", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.reserved_space {
            query.arg("reservedSpace", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.target_space {
            query.arg("targetSpace", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.use_default_policy {
            query.arg("useDefaultPolicy", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "The minimum amount of disk space this policy is guaranteed to retain.\n\nSelects GraphQL Wire_Name `reservedSpace` on `EngineCache`."]
    pub async fn reserved_space(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("reservedSpace");
        query.execute(&self.session).await
    }
    #[doc = "The target number of bytes to keep when pruning.\n\nSelects GraphQL Wire_Name `targetSpace` on `EngineCache`."]
    pub async fn target_space(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("targetSpace");
        query.execute(&self.session).await
    }
}
impl super::Node for EngineCache {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
