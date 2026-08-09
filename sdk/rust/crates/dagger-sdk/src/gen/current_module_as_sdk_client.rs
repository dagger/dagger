//! Generated bindings owned by the GraphQL `CurrentModuleAsSDKClient` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A generated client the current SDK produces in the workspace."]
#[derive(Clone)]
pub struct CurrentModuleAsSdkClient {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for CurrentModuleAsSdkClient {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for CurrentModuleAsSdkClient {
    fn graphql_type() -> &'static str {
        "CurrentModuleAsSDKClient"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<CurrentModuleAsSdkClient> for crate::IdInput<CurrentModuleAsSdkClient> {
    fn from(value: CurrentModuleAsSdkClient) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<CurrentModuleAsSdkClient> for crate::IdInput<super::NodeClient> {
    fn from(value: CurrentModuleAsSdkClient) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl CurrentModuleAsSdkClient {
    #[doc = "A unique identifier for this CurrentModuleAsSDKClient.\n\nSelects GraphQL Wire_Name `id` on `CurrentModuleAsSDKClient`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The module the client is bound to (workspace-relative path or canonical ref).\n\nSelects GraphQL Wire_Name `module` on `CurrentModuleAsSDKClient`."]
    pub async fn module(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("module");
        query.execute(&self.session).await
    }
    #[doc = "The resolved module source this client is bound to, including its dependency closure and pinned version.\n\nSelects GraphQL Wire_Name `moduleSource` on `CurrentModuleAsSDKClient`."]
    #[must_use]
    pub fn module_source(&self) -> super::ModuleSource {
        let query = self.selection.select("moduleSource");
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Workspace-root-relative path of the generated client.\n\nSelects GraphQL Wire_Name `path` on `CurrentModuleAsSDKClient`."]
    pub async fn path(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("path");
        query.execute(&self.session).await
    }
    #[doc = "The pinned version of the bound module, if any.\n\nSelects GraphQL Wire_Name `pin` on `CurrentModuleAsSDKClient`."]
    pub async fn pin(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("pin");
        query.execute(&self.session).await
    }
}
impl super::Node for CurrentModuleAsSdkClient {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
