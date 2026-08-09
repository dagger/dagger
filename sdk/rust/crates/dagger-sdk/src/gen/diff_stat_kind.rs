//! Generated bindings owned by the GraphQL `DiffStatKind` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The type of change for a diff stat entry."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum DiffStatKind {
    #[doc = "A file or directory was added."]
    #[serde(rename = "ADDED")]
    Added,
    #[doc = "A file was modified."]
    #[serde(rename = "MODIFIED")]
    Modified,
    #[doc = "A file or directory was removed."]
    #[serde(rename = "REMOVED")]
    Removed,
    #[doc = "A file was renamed."]
    #[serde(rename = "RENAMED")]
    Renamed,
}
