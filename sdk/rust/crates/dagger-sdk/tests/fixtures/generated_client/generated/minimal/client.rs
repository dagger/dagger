//! Generated bindings owned by GraphQL coordinate `Minimal`.
// @generated {"format":"dagger-rust-standalone-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:9ee1f1eeccf3db6eacb6690f6c097fe6ff27d366c4598025d7df1ddc99134787","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Client fixture object Minimal."]
#[derive(Clone)]
pub struct Client {
    query: dagger_sdk::QueryBuilder,
}
#[doc = "Owned optional arguments for GraphQL operation `Minimal.item`."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ItemOpts {
    config: Option<Option<super::Config>>,
}
impl ItemOpts {
    /// Supplies GraphQL argument `config` while retaining explicit null and zero values.
    #[must_use]
    pub fn with_config(mut self, value: super::Config) -> Self {
        self.config = Some(Some(value));
        self
    }
    /// Supplies an explicit GraphQL null for argument `config` rather than omitting it.
    #[must_use]
    pub fn with_config_null(mut self) -> Self {
        self.config = Some(None);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Minimal.search`."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct SearchOpts {
    count: Option<Option<i64>>,
    enabled: Option<Option<bool>>,
    item: Option<Option<dagger_sdk::IdInput<super::Item>>>,
    label: Option<Option<String>>,
}
impl SearchOpts {
    /// Supplies GraphQL argument `count` while retaining explicit null and zero values.
    #[must_use]
    pub fn with_count(mut self, value: i64) -> Self {
        self.count = Some(Some(value));
        self
    }
    /// Supplies an explicit GraphQL null for argument `count` rather than omitting it.
    #[must_use]
    pub fn with_count_null(mut self) -> Self {
        self.count = Some(None);
        self
    }
    /// Supplies GraphQL argument `enabled` while retaining explicit null and zero values.
    #[must_use]
    pub fn with_enabled(mut self, value: bool) -> Self {
        self.enabled = Some(Some(value));
        self
    }
    /// Supplies an explicit GraphQL null for argument `enabled` rather than omitting it.
    #[must_use]
    pub fn with_enabled_null(mut self) -> Self {
        self.enabled = Some(None);
        self
    }
    /// Supplies GraphQL argument `item` while retaining explicit null and zero values.
    #[must_use]
    pub fn with_item(mut self, value: dagger_sdk::IdInput<super::Item>) -> Self {
        self.item = Some(Some(value));
        self
    }
    /// Supplies an explicit GraphQL null for argument `item` rather than omitting it.
    #[must_use]
    pub fn with_item_null(mut self) -> Self {
        self.item = Some(None);
        self
    }
    /// Supplies GraphQL argument `label` while retaining explicit null and zero values.
    #[must_use]
    pub fn with_label(mut self, value: String) -> Self {
        self.label = Some(Some(value));
        self
    }
    /// Supplies an explicit GraphQL null for argument `label` rather than omitting it.
    #[must_use]
    pub fn with_label_null(mut self) -> Self {
        self.label = Some(None);
        self
    }
}
impl Client {
    #[must_use]
    pub(in crate::dagger_client) fn from_query(query: dagger_sdk::QueryBuilder) -> Self {
        Self { query }
    }
    /// Borrows the immutable query represented by this handle.
    #[must_use]
    pub fn selection(&self) -> &dagger_sdk::QueryBuilder {
        &self.query
    }
    #[doc = "Client fixture field container."]
    #[must_use]
    pub fn container(&self) -> dagger_sdk::Container {
        let query = self.query.select("container");
        query.generated_core_handle::<dagger_sdk::Container>()
    }
    #[doc = "Client fixture field helper."]
    #[must_use]
    pub fn helper(&self) -> super::MinimalClient {
        let query = self.query.select("helper");
        super::MinimalClient::from_query(query)
    }
    #[doc = "Client fixture field id."]
    pub async fn id(&self) -> Result<dagger_sdk::Id, dagger_sdk::QueryError> {
        let query = self.query.select("id");
        query.execute().await
    }
    #[doc = "Client fixture field item."]
    pub async fn item(&self) -> Result<Option<super::Item>, dagger_sdk::QueryError> {
        let query = self.query.select("item");
        let ids: Option<dagger_sdk::Id> = query.select("id").execute().await?;
        Ok(ids.map(|value_0| {
            super::Item::from_query(query.generated_reentry_builder(value_0, "MinimalItem"))
        }))
    }
    #[doc = "Client fixture field item."]
    pub async fn item_opts(
        &self,
        opts: ItemOpts,
    ) -> Result<Option<super::Item>, dagger_sdk::QueryError> {
        let query = self.query.select("item");
        let query = if let Some(value) = opts.config {
            query.argument("config", value)
        } else {
            query
        };
        let ids: Option<dagger_sdk::Id> = query.select("id").execute().await?;
        Ok(ids.map(|value_0| {
            super::Item::from_query(query.generated_reentry_builder(value_0, "MinimalItem"))
        }))
    }
    #[doc = "Client fixture field items."]
    pub async fn items(&self) -> Result<Vec<Option<super::Item>>, dagger_sdk::QueryError> {
        let query = self.query.select("items");
        let ids: Vec<Option<dagger_sdk::Id>> = query.select("id").execute().await?;
        Ok(ids
            .into_iter()
            .map(|value_0| {
                value_0.map(|value_1| {
                    super::Item::from_query(query.generated_reentry_builder(value_1, "MinimalItem"))
                })
            })
            .collect())
    }
    #[doc = "Client fixture field maybeContainer."]
    pub async fn maybe_container(
        &self,
    ) -> Result<Option<dagger_sdk::Container>, dagger_sdk::QueryError> {
        let query = self.query.select("maybeContainer");
        let ids: Option<dagger_sdk::Id> = query.select("id").execute().await?;
        Ok(ids.map(|value_0| {
            query
                .generated_reentry_builder(value_0, "Container")
                .generated_core_handle::<dagger_sdk::Container>()
        }))
    }
    #[doc = "Client fixture field message."]
    pub async fn message(&self) -> Result<String, dagger_sdk::QueryError> {
        let query = self.query.select("message");
        query.execute().await
    }
    #[doc = "Client fixture field node."]
    #[must_use]
    pub fn node(&self) -> super::NodeClient {
        let query = self.query.select("node");
        super::NodeClient::from_query(query)
    }
    #[doc = "Client fixture field search."]
    pub async fn search(&self) -> Result<String, dagger_sdk::QueryError> {
        let query = self.query.select("search");
        query.execute().await
    }
    #[doc = "Client fixture field search."]
    pub async fn search_opts(&self, opts: SearchOpts) -> Result<String, dagger_sdk::QueryError> {
        let query = self.query.select("search");
        let query = if let Some(value) = opts.count {
            query.argument("count", value)
        } else {
            query
        };
        let query = if let Some(value) = opts.enabled {
            query.argument("enabled", value)
        } else {
            query
        };
        let query = if let Some(value) = opts.item {
            query.generated_argument_id_shape("item", value)
        } else {
            query
        };
        let query = if let Some(value) = opts.label {
            query.argument("label", value)
        } else {
            query
        };
        query.execute().await
    }
    #[doc = "Client fixture field sync."]
    pub async fn sync(&self) -> Result<super::Client, dagger_sdk::QueryError> {
        let query = self.query.select("sync");
        let id: dagger_sdk::Id = query.execute().await?;
        Ok(super::Client::from_query(
            query.generated_reentry_builder(id, "Minimal"),
        ))
    }
    #[doc = "Client fixture field token."]
    pub async fn token(&self) -> Result<super::Token, dagger_sdk::QueryError> {
        let query = self.query.select("token");
        query.execute().await
    }
    #[doc = "Client fixture field type."]
    pub async fn r#type(&self) -> Result<String, dagger_sdk::QueryError> {
        let query = self.query.select("type");
        query.execute().await
    }
    #[doc = "Client fixture field useItem."]
    pub async fn use_item(
        &self,
        item: impl Into<dagger_sdk::IdInput<super::Item>>,
    ) -> Result<String, dagger_sdk::QueryError> {
        let query = self.query.select("useItem");
        let query = query.generated_argument_id_shape("item", item.into());
        query.execute().await
    }
    #[doc = "Client fixture field useItems."]
    pub async fn use_items(
        &self,
        items: impl Into<Vec<Option<dagger_sdk::IdInput<super::Item>>>>,
    ) -> Result<String, dagger_sdk::QueryError> {
        let query = self.query.select("useItems");
        let query = query.generated_argument_id_shape("items", items.into());
        query.execute().await
    }
}
impl dagger_sdk::IntoID<dagger_sdk::Id> for Client {
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
impl From<Client> for dagger_sdk::IdInput<Client> {
    fn from(value: Client) -> Self {
        dagger_sdk::IdInput::generated_lazy(value)
    }
}
