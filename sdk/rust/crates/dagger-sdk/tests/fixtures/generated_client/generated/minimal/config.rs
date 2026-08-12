//! Generated bindings owned by GraphQL coordinate `MinimalConfig`.
// @generated {"format":"dagger-rust-standalone-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:9ee1f1eeccf3db6eacb6690f6c097fe6ff27d366c4598025d7df1ddc99134787","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Client fixture input MinimalConfig."]
#[derive(Clone, Debug, dagger_sdk::__private::serde::Serialize)]
#[serde(crate = "dagger_sdk::__private::serde")]
#[non_exhaustive]
pub struct Config {
    #[doc = "Client fixture argument enabled."]
    #[serde(rename = "enabled", skip_serializing_if = "Option::is_none")]
    pub enabled: Option<Option<bool>>,
}
impl Config {
    /// Creates `Config` with every required GraphQL input.
    #[must_use]
    pub fn new() -> Self {
        Self { enabled: None }
    }
    /// Supplies GraphQL input `enabled`; calling this method preserves explicit null and zero values.
    #[must_use]
    pub fn with_enabled(mut self, value: bool) -> Self {
        self.enabled = Some(Some(value));
        self
    }
    /// Supplies an explicit GraphQL null for input `enabled` rather than omitting it.
    #[must_use]
    pub fn with_enabled_null(mut self) -> Self {
        self.enabled = Some(None);
        self
    }
}
