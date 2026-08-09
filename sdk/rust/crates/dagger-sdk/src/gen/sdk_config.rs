//! Generated bindings owned by the GraphQL `SDKConfig` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The SDK config of the module."]
#[derive(Clone)]
pub struct SdkConfig {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for SdkConfig {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for SdkConfig {
    fn graphql_type() -> &'static str {
        "SDKConfig"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<SdkConfig> for crate::IdInput<SdkConfig> {
    fn from(value: SdkConfig) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<SdkConfig> for crate::IdInput<super::NodeClient> {
    fn from(value: SdkConfig) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl SdkConfig {
    #[doc = "Whether to start the SDK runtime in debug mode with an interactive terminal.\n\nSelects GraphQL Wire_Name `debug` on `SDKConfig`."]
    pub async fn debug(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("debug");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this SDKConfig.\n\nSelects GraphQL Wire_Name `id` on `SDKConfig`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.\n\nSelects GraphQL Wire_Name `source` on `SDKConfig`."]
    pub async fn source(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("source");
        query.execute(&self.session).await
    }
}
impl super::Node for SdkConfig {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
