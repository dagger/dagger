//! Generated bindings owned by the GraphQL `LLMTokenUsage` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A count of tokens consumed by LLM API calls."]
#[derive(Clone)]
pub struct LlmTokenUsage {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for LlmTokenUsage {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for LlmTokenUsage {
    fn graphql_type() -> &'static str {
        "LLMTokenUsage"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<LlmTokenUsage> for crate::IdInput<LlmTokenUsage> {
    fn from(value: LlmTokenUsage) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<LlmTokenUsage> for crate::IdInput<super::NodeClient> {
    fn from(value: LlmTokenUsage) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl LlmTokenUsage {
    #[doc = "Input tokens served from the provider's prompt cache.\n\nSelects GraphQL Wire_Name `cachedTokenReads` on `LLMTokenUsage`."]
    pub async fn cached_token_reads(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("cachedTokenReads");
        query.execute(&self.session).await
    }
    #[doc = "Input tokens written to the provider's prompt cache.\n\nSelects GraphQL Wire_Name `cachedTokenWrites` on `LLMTokenUsage`."]
    pub async fn cached_token_writes(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("cachedTokenWrites");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this LLMTokenUsage.\n\nSelects GraphQL Wire_Name `id` on `LLMTokenUsage`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Uncached input tokens sent to the model.\n\nSelects GraphQL Wire_Name `inputTokens` on `LLMTokenUsage`."]
    pub async fn input_tokens(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("inputTokens");
        query.execute(&self.session).await
    }
    #[doc = "Tokens received from the model, including text and tool calls.\n\nSelects GraphQL Wire_Name `outputTokens` on `LLMTokenUsage`."]
    pub async fn output_tokens(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("outputTokens");
        query.execute(&self.session).await
    }
    #[doc = "Total tokens consumed, as reported by the provider.\n\nSelects GraphQL Wire_Name `totalTokens` on `LLMTokenUsage`."]
    pub async fn total_tokens(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("totalTokens");
        query.execute(&self.session).await
    }
}
impl super::Node for LlmTokenUsage {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
