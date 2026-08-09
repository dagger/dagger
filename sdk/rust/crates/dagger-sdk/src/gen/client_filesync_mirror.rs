//! Generated bindings owned by the GraphQL `ClientFilesyncMirror` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An internal persistent filesync mirror."]
#[derive(Clone)]
pub struct ClientFilesyncMirror {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for ClientFilesyncMirror {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for ClientFilesyncMirror {
    fn graphql_type() -> &'static str {
        "ClientFilesyncMirror"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<ClientFilesyncMirror> for crate::IdInput<ClientFilesyncMirror> {
    fn from(value: ClientFilesyncMirror) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<ClientFilesyncMirror> for crate::IdInput<super::NodeClient> {
    fn from(value: ClientFilesyncMirror) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl ClientFilesyncMirror {
    #[doc = "A unique identifier for this ClientFilesyncMirror.\n\nSelects GraphQL Wire_Name `id` on `ClientFilesyncMirror`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
}
impl super::Node for ClientFilesyncMirror {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
