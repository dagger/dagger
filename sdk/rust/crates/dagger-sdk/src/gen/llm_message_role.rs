//! Generated bindings owned by the GraphQL `LLMMessageRole` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The role that generated a message."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum LlmMessageRole {
    #[doc = "A reply from the model."]
    #[serde(rename = "ASSISTANT")]
    Assistant,
    #[doc = "A system prompt."]
    #[serde(rename = "SYSTEM")]
    System,
    #[doc = "A user prompt or tool response."]
    #[serde(rename = "USER")]
    User,
}
