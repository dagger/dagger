//! Generated bindings owned by the GraphQL `LLMContentBlock` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A single piece of content within an LLM message."]
#[derive(Clone)]
pub struct LlmContentBlock {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for LlmContentBlock {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for LlmContentBlock {
    fn graphql_type() -> &'static str {
        "LLMContentBlock"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<LlmContentBlock> for crate::IdInput<LlmContentBlock> {
    fn from(value: LlmContentBlock) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<LlmContentBlock> for crate::IdInput<super::NodeClient> {
    fn from(value: LlmContentBlock) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl LlmContentBlock {
    #[doc = "The arguments passed to the tool, JSON-encoded (for TOOL_CALL kind).\n\nSelects GraphQL Wire_Name `arguments` on `LLMContentBlock`."]
    pub async fn arguments(&self) -> Result<crate::Json, crate::QueryError> {
        let query = self.selection.select("arguments");
        query.execute(&self.session).await
    }
    #[doc = "The unique ID of a tool call (for TOOL_CALL or TOOL_RESULT kinds).\n\nSelects GraphQL Wire_Name `callId` on `LLMContentBlock`."]
    pub async fn call_id(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("callId");
        query.execute(&self.session).await
    }
    #[doc = "Whether the tool call resulted in an error (for TOOL_RESULT kind).\n\nSelects GraphQL Wire_Name `errored` on `LLMContentBlock`."]
    pub async fn errored(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("errored");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this LLMContentBlock.\n\nSelects GraphQL Wire_Name `id` on `LLMContentBlock`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The kind of content block, which determines the other populated fields.\n\nSelects GraphQL Wire_Name `kind` on `LLMContentBlock`."]
    pub async fn kind(&self) -> Result<super::LlmContentBlockKind, crate::QueryError> {
        let query = self.selection.select("kind");
        query.execute(&self.session).await
    }
    #[doc = "Provider-specific opaque data (e.g. Anthropic thinking signature). Preserve it when reconstructing a conversation.\n\nSelects GraphQL Wire_Name `signature` on `LLMContentBlock`."]
    pub async fn signature(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("signature");
        query.execute(&self.session).await
    }
    #[doc = "Text content (for TEXT, THINKING, or TOOL_RESULT kinds).\n\nSelects GraphQL Wire_Name `text` on `LLMContentBlock`."]
    pub async fn text(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("text");
        query.execute(&self.session).await
    }
    #[doc = "The name of the tool called (for TOOL_CALL kind).\n\nSelects GraphQL Wire_Name `toolName` on `LLMContentBlock`."]
    pub async fn tool_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("toolName");
        query.execute(&self.session).await
    }
}
impl super::Node for LlmContentBlock {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
