//! Generated bindings owned by the GraphQL `LLMMessage` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A single message in an LLM conversation."]
#[derive(Clone)]
pub struct LlmMessage {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for LlmMessage {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for LlmMessage {
    fn graphql_type() -> &'static str {
        "LLMMessage"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<LlmMessage> for crate::IdInput<LlmMessage> {
    fn from(value: LlmMessage) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<LlmMessage> for crate::IdInput<super::NodeClient> {
    fn from(value: LlmMessage) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl LlmMessage {
    #[doc = "The message's content blocks, in the order the model produced them.\n\nSelects GraphQL Wire_Name `content` on `LLMMessage`."]
    pub async fn content(&self) -> Result<Vec<super::LlmContentBlock>, crate::QueryError> {
        let query = self.selection.select("content");
        let query = query.select("id");
        query
            .execute_reentry::<super::LlmContentBlock, Vec<crate::Id>>(
                &self.session,
                "LLMContentBlock",
            )
            .await
    }
    #[doc = "A unique identifier for this LLMMessage.\n\nSelects GraphQL Wire_Name `id` on `LLMMessage`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The role that produced this message.\n\nSelects GraphQL Wire_Name `role` on `LLMMessage`."]
    pub async fn role(&self) -> Result<super::LlmMessageRole, crate::QueryError> {
        let query = self.selection.select("role");
        query.execute(&self.session).await
    }
    #[doc = "Token usage reported by the provider for the API call that produced this message; all zeros except on assistant responses.\n\nSelects GraphQL Wire_Name `tokenUsage` on `LLMMessage`."]
    #[must_use]
    pub fn token_usage(&self) -> super::LlmTokenUsage {
        let query = self.selection.select("tokenUsage");
        super::LlmTokenUsage {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for LlmMessage {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
