//! Generated bindings owned by the GraphQL `CacheSharingMode` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Sharing mode of the cache volume."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum CacheSharingMode {
    #[doc = "Shares the cache volume amongst many build pipelines, but will serialize the writes"]
    #[serde(rename = "LOCKED")]
    Locked,
    #[doc = "Keeps a cache volume for a single build pipeline"]
    #[serde(rename = "PRIVATE")]
    Private,
    #[doc = "Shares the cache volume amongst many build pipelines"]
    #[serde(rename = "SHARED")]
    Shared,
}
