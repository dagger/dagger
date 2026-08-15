//! Generated bindings owned by the GraphQL `FileType` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"501b57e0476dee5881b99a064c3c04173134ecc7"}
#[doc = "File type."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum FileType {
    #[doc = "directory file type"]
    #[serde(rename = "DIRECTORY", alias = "DIRECTORY_TYPE")]
    Directory,
    #[doc = "regular file type"]
    #[serde(rename = "REGULAR", alias = "REGULAR_TYPE")]
    Regular,
    #[doc = "symlink file type"]
    #[serde(rename = "SYMLINK", alias = "SYMLINK_TYPE")]
    Symlink,
    #[doc = "unknown file type"]
    #[serde(rename = "UNKNOWN")]
    Unknown,
}
