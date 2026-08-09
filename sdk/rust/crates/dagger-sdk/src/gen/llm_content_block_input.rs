//! Generated bindings owned by the GraphQL `LLMContentBlockInput` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A content block within an LLM message."]
#[derive(Clone, Debug, PartialEq, serde :: Deserialize, serde :: Serialize)]
#[non_exhaustive]
pub struct LlmContentBlockInput {
    #[doc = "The arguments to pass to the tool (for TOOL_CALL kind)."]
    #[serde(rename = "arguments", default, skip_serializing_if = "Option::is_none")]
    pub arguments: Option<crate::Json>,
    #[doc = "The unique ID of a tool call (for TOOL_CALL or TOOL_RESULT kinds)."]
    #[serde(rename = "callId", default, skip_serializing_if = "Option::is_none")]
    pub call_id: Option<String>,
    #[doc = "Whether the tool call resulted in an error (for TOOL_RESULT kind)."]
    #[serde(rename = "errored", default, skip_serializing_if = "Option::is_none")]
    pub errored: Option<bool>,
    #[doc = "The kind of content block."]
    #[serde(rename = "kind")]
    pub kind: super::LlmContentBlockKind,
    #[doc = "Provider-specific opaque data (e.g. Anthropic thinking signature)."]
    #[serde(rename = "signature", default, skip_serializing_if = "Option::is_none")]
    pub signature: Option<String>,
    #[doc = "Text content (for TEXT, THINKING, or TOOL_RESULT kinds)."]
    #[serde(rename = "text", default, skip_serializing_if = "Option::is_none")]
    pub text: Option<String>,
    #[doc = "The name of the tool to call (for TOOL_CALL kind)."]
    #[serde(rename = "toolName", default, skip_serializing_if = "Option::is_none")]
    pub tool_name: Option<String>,
}
impl LlmContentBlockInput {
    #[doc = "Creates `LlmContentBlockInput` with every required GraphQL input field."]
    #[must_use]
    pub fn new(kind: super::LlmContentBlockKind) -> Self {
        Self {
            arguments: None,
            call_id: None,
            errored: None,
            kind,
            signature: None,
            text: None,
            tool_name: None,
        }
    }
    #[doc = "Sets GraphQL input field `arguments`; the field is omitted until this method is called."]
    #[must_use]
    pub fn with_arguments(mut self, value: crate::Json) -> Self {
        self.arguments = Some(value);
        self
    }
    #[doc = "Sets GraphQL input field `callId`; the field is omitted until this method is called."]
    #[must_use]
    pub fn with_call_id(mut self, value: String) -> Self {
        self.call_id = Some(value);
        self
    }
    #[doc = "Sets GraphQL input field `errored`; the field is omitted until this method is called."]
    #[must_use]
    pub fn with_errored(mut self, value: bool) -> Self {
        self.errored = Some(value);
        self
    }
    #[doc = "Sets GraphQL input field `signature`; the field is omitted until this method is called."]
    #[must_use]
    pub fn with_signature(mut self, value: String) -> Self {
        self.signature = Some(value);
        self
    }
    #[doc = "Sets GraphQL input field `text`; the field is omitted until this method is called."]
    #[must_use]
    pub fn with_text(mut self, value: String) -> Self {
        self.text = Some(value);
        self
    }
    #[doc = "Sets GraphQL input field `toolName`; the field is omitted until this method is called."]
    #[must_use]
    pub fn with_tool_name(mut self, value: String) -> Self {
        self.tool_name = Some(value);
        self
    }
}
