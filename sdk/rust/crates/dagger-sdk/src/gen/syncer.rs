//! Generated bindings owned by the GraphQL `Syncer` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An object that can be force-evaluated.\n\nCalling sync ensures that the object's entire dependency DAG has been evaluated, returning the object's ID once complete."]
pub trait Syncer: Clone + Send + Sync {
    #[doc = "Selects GraphQL Wire_Name `id` on `Syncer`."]
    fn id(&self)
    -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send;
    #[doc = "Selects GraphQL Wire_Name `sync` on `Syncer`."]
    fn sync(
        &self,
    ) -> impl core::future::Future<Output = Result<super::SyncerClient, crate::QueryError>> + Send;
}
#[doc = "Lazy client handle for GraphQL interface `Syncer`."]
#[derive(Clone)]
pub struct SyncerClient {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for SyncerClient {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for SyncerClient {
    fn graphql_type() -> &'static str {
        "Syncer"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<SyncerClient> for crate::IdInput<SyncerClient> {
    fn from(value: SyncerClient) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<SyncerClient> for crate::IdInput<super::NodeClient> {
    fn from(value: SyncerClient) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl SyncerClient {
    #[doc = "Selects GraphQL Wire_Name `id` on `Syncer`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Selects GraphQL Wire_Name `sync` on `Syncer`."]
    pub async fn sync(&self) -> Result<super::SyncerClient, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::SyncerClient>(
            &self.session,
            id,
            "Syncer",
        ))
    }
}
impl super::Syncer for SyncerClient {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
    fn sync(
        &self,
    ) -> impl core::future::Future<Output = Result<super::SyncerClient, crate::QueryError>> + Send
    {
        let query = self.selection.select("sync");
        let session = self.session.clone();
        async move {
            let id: crate::Id = query.execute(&session).await?;
            Ok(crate::query::reenter::<super::SyncerClient>(
                &session, id, "Syncer",
            ))
        }
    }
}
impl super::Node for SyncerClient {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
