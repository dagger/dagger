//! Generated bindings owned by the GraphQL `ImageLayerCompression` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"501b57e0476dee5881b99a064c3c04173134ecc7"}
#[doc = "Compression algorithm to use for image layers."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum ImageLayerCompression {
    #[doc = "GraphQL enum value `EStarGZ`."]
    #[serde(rename = "EStarGZ", alias = "ESTARGZ")]
    EStarGz,
    #[doc = "GraphQL enum value `Gzip`."]
    #[serde(rename = "Gzip", alias = "GZIP")]
    Gzip,
    #[doc = "GraphQL enum value `Uncompressed`."]
    #[serde(rename = "Uncompressed", alias = "UNCOMPRESSED")]
    Uncompressed,
    #[doc = "GraphQL enum value `Zstd`."]
    #[serde(rename = "Zstd", alias = "ZSTD")]
    Zstd,
}
