//! Generated bindings owned by the GraphQL `PatchConflict` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"501b57e0476dee5881b99a064c3c04173134ecc7"}
#[doc = "How to handle patch hunks that no longer apply to the target content."]
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq, serde :: Deserialize, serde :: Serialize)]
pub enum PatchConflict {
    #[doc = "Fail the operation if any part of the patch does not apply."]
    #[serde(rename = "FAIL")]
    Fail,
    #[doc = "Apply the hunks that fit and insert conflict markers where hunks no longer match, instead of failing."]
    #[serde(rename = "LEAVE_CONFLICT_MARKERS")]
    LeaveConflictMarkers,
}
