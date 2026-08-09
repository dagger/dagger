//! Generated bindings owned by the GraphQL `ImageMediaTypes` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Mediatypes to use in published or exported image metadata."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum ImageMediaTypes {
    #[doc = "GraphQL enum value `DockerMediaTypes`."]
    #[serde(rename = "DockerMediaTypes", alias = "DOCKER")]
    DockerMediaTypes,
    #[doc = "GraphQL enum value `OCIMediaTypes`."]
    #[serde(rename = "OCIMediaTypes", alias = "OCI")]
    OciMediaTypes,
}
