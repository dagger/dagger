//! Generated bindings owned by the GraphQL `ChangesetsMergeConflict` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Strategy to use when merging multiple changesets with git octopus merge."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum ChangesetsMergeConflict {
    #[doc = "Attempt the octopus merge and fail if git merge fails due to conflicts"]
    #[serde(rename = "FAIL")]
    Fail,
    #[doc = "Fail before attempting merge if file-level conflicts are detected between any changesets"]
    #[serde(rename = "FAIL_EARLY")]
    FailEarly,
}
