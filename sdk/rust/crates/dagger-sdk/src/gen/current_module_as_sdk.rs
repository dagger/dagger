//! Generated bindings owned by the GraphQL `CurrentModuleAsSDK` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The SDK-role data for the currently executing module, as installed in the active workspace."]
#[derive(Clone)]
pub struct CurrentModuleAsSdk {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for CurrentModuleAsSdk {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for CurrentModuleAsSdk {
    fn graphql_type() -> &'static str {
        "CurrentModuleAsSDK"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<CurrentModuleAsSdk> for crate::IdInput<CurrentModuleAsSdk> {
    fn from(value: CurrentModuleAsSdk) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<CurrentModuleAsSdk> for crate::IdInput<super::NodeClient> {
    fn from(value: CurrentModuleAsSdk) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl CurrentModuleAsSdk {
    #[doc = "The generated clients this SDK produces in the workspace.\n\nSelects GraphQL Wire_Name `clients` on `CurrentModuleAsSDK`."]
    pub async fn clients(&self) -> Result<Vec<super::CurrentModuleAsSdkClient>, crate::QueryError> {
        let query = self.selection.select("clients");
        let query = query.select("id");
        query
            .execute_reentry::<super::CurrentModuleAsSdkClient, Vec<crate::Id>>(
                &self.session,
                "CurrentModuleAsSDKClient",
            )
            .await
    }
    #[doc = "A unique identifier for this CurrentModuleAsSDK.\n\nSelects GraphQL Wire_Name `id` on `CurrentModuleAsSDK`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The workspace-local modules this SDK authors and manages.\n\nSelects GraphQL Wire_Name `modules` on `CurrentModuleAsSDK`."]
    pub async fn modules(&self) -> Result<Vec<super::CurrentModuleAsSdkModule>, crate::QueryError> {
        let query = self.selection.select("modules");
        let query = query.select("id");
        query
            .execute_reentry::<super::CurrentModuleAsSdkModule, Vec<crate::Id>>(
                &self.session,
                "CurrentModuleAsSDKModule",
            )
            .await
    }
    #[doc = "The user-facing name of this SDK in the workspace.\n\nSelects GraphQL Wire_Name `name` on `CurrentModuleAsSDK`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
}
impl super::Node for CurrentModuleAsSdk {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
