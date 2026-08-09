//! Generated bindings owned by the GraphQL `CurrentModuleAsSDKModule` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A workspace-local module managed by the current SDK."]
#[derive(Clone)]
pub struct CurrentModuleAsSdkModule {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for CurrentModuleAsSdkModule {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for CurrentModuleAsSdkModule {
    fn graphql_type() -> &'static str {
        "CurrentModuleAsSDKModule"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<CurrentModuleAsSdkModule> for crate::IdInput<CurrentModuleAsSdkModule> {
    fn from(value: CurrentModuleAsSdkModule) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<CurrentModuleAsSdkModule> for crate::IdInput<super::NodeClient> {
    fn from(value: CurrentModuleAsSdkModule) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl CurrentModuleAsSdkModule {
    #[doc = "A unique identifier for this CurrentModuleAsSDKModule.\n\nSelects GraphQL Wire_Name `id` on `CurrentModuleAsSDKModule`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Workspace-root-relative path to the managed module.\n\nSelects GraphQL Wire_Name `path` on `CurrentModuleAsSDKModule`."]
    pub async fn path(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("path");
        query.execute(&self.session).await
    }
}
impl super::Node for CurrentModuleAsSdkModule {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
