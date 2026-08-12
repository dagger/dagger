//! Generated bindings owned by GraphQL coordinate `MinimalState`.
// @generated {"format":"dagger-rust-standalone-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:9ee1f1eeccf3db6eacb6690f6c097fe6ff27d366c4598025d7df1ddc99134787","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Client fixture enum MinimalState."]
#[derive(
    Clone,
    Copy,
    Debug,
    Eq,
    Hash,
    PartialEq,
    dagger_sdk::__private::serde::Deserialize,
    dagger_sdk::__private::serde::Serialize,
)]
#[serde(crate = "dagger_sdk::__private::serde")]
pub enum State {
    #[doc = "Client fixture value BUSY."]
    #[serde(rename = "BUSY")]
    Busy,
    #[doc = "Client fixture value READY."]
    #[serde(rename = "READY")]
    Ready,
}
