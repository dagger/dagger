//! Generated bindings owned by the GraphQL `ChangesetMergeConflict` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Strategy to use when merging changesets with conflicting changes."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum ChangesetMergeConflict {
    #[doc = "Attempt the merge and fail if git merge fails due to conflicts"]
    #[serde(rename = "FAIL")]
    Fail,
    #[doc = "Fail before attempting merge if file-level conflicts are detected"]
    #[serde(rename = "FAIL_EARLY")]
    FailEarly,
    #[doc = "Let git create conflict markers in files. For modify/delete conflicts, keeps the modified version. Fails on binary conflicts."]
    #[serde(rename = "LEAVE_CONFLICT_MARKERS")]
    LeaveConflictMarkers,
    #[doc = "The conflict is resolved by applying the version of the calling changeset"]
    #[serde(rename = "PREFER_OURS")]
    PreferOurs,
    #[doc = "The conflict is resolved by applying the version of the other changeset"]
    #[serde(rename = "PREFER_THEIRS")]
    PreferTheirs,
}
