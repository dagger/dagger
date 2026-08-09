//! Generated bindings owned by the GraphQL `HealthcheckConfig` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Image healthcheck configuration."]
#[derive(Clone)]
pub struct HealthcheckConfig {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for HealthcheckConfig {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for HealthcheckConfig {
    fn graphql_type() -> &'static str {
        "HealthcheckConfig"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<HealthcheckConfig> for crate::IdInput<HealthcheckConfig> {
    fn from(value: HealthcheckConfig) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<HealthcheckConfig> for crate::IdInput<super::NodeClient> {
    fn from(value: HealthcheckConfig) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl HealthcheckConfig {
    #[doc = "Healthcheck command arguments.\n\nSelects GraphQL Wire_Name `args` on `HealthcheckConfig`."]
    pub async fn args(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("args");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this HealthcheckConfig.\n\nSelects GraphQL Wire_Name `id` on `HealthcheckConfig`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Interval between running healthcheck. Example:30s\n\nSelects GraphQL Wire_Name `interval` on `HealthcheckConfig`."]
    pub async fn interval(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("interval");
        query.execute(&self.session).await
    }
    #[doc = "The maximum number of consecutive failures before the container is marked as unhealthy. Example:3\n\nSelects GraphQL Wire_Name `retries` on `HealthcheckConfig`."]
    pub async fn retries(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("retries");
        query.execute(&self.session).await
    }
    #[doc = "Healthcheck command is a shell command.\n\nSelects GraphQL Wire_Name `shell` on `HealthcheckConfig`."]
    pub async fn shell(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("shell");
        query.execute(&self.session).await
    }
    #[doc = "StartInterval configures the duration between checks during the startup phase. Example:5s\n\nSelects GraphQL Wire_Name `startInterval` on `HealthcheckConfig`."]
    pub async fn start_interval(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("startInterval");
        query.execute(&self.session).await
    }
    #[doc = "StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example:0s\n\nSelects GraphQL Wire_Name `startPeriod` on `HealthcheckConfig`."]
    pub async fn start_period(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("startPeriod");
        query.execute(&self.session).await
    }
    #[doc = "Healthcheck timeout. Example:3s\n\nSelects GraphQL Wire_Name `timeout` on `HealthcheckConfig`."]
    pub async fn timeout(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("timeout");
        query.execute(&self.session).await
    }
}
impl super::Node for HealthcheckConfig {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
