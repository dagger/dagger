//! Generated bindings owned by the GraphQL `RegistryProtocol` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Transport protocol to use for registry operations."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum RegistryProtocol {
    #[doc = "GraphQL enum value `HTTP`."]
    #[serde(rename = "HTTP")]
    Http,
    #[doc = "GraphQL enum value `HTTPS`."]
    #[serde(rename = "HTTPS")]
    Https,
}
