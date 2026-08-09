//! Generated bindings owned by the GraphQL `ExistsType` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "File type."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum ExistsType {
    #[doc = "Tests path is a directory"]
    #[serde(rename = "DIRECTORY_TYPE")]
    DirectoryType,
    #[doc = "Tests path is a regular file"]
    #[serde(rename = "REGULAR_TYPE")]
    RegularType,
    #[doc = "Tests path is a symlink"]
    #[serde(rename = "SYMLINK_TYPE")]
    SymlinkType,
}
