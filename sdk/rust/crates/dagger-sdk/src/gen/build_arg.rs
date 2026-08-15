//! Generated bindings owned by the GraphQL `BuildArg` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"501b57e0476dee5881b99a064c3c04173134ecc7"}
#[doc = "Key value object that represents a build argument."]
#[derive(Clone, Debug, PartialEq, serde :: Deserialize, serde :: Serialize)]
#[non_exhaustive]
pub struct BuildArg {
    #[doc = "The build argument name."]
    #[serde(rename = "name")]
    pub name: String,
    #[doc = "The build argument value."]
    #[serde(rename = "value")]
    pub value: String,
}
impl BuildArg {
    #[doc = "Creates `BuildArg` with every required GraphQL input field."]
    #[must_use]
    pub fn new(name: String, value: String) -> Self {
        Self { name, value }
    }
}
