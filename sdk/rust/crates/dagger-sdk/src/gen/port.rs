//! Generated bindings owned by the GraphQL `Port` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A port exposed by a container."]
#[derive(Clone)]
pub struct Port {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for Port {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Port {
    fn graphql_type() -> &'static str {
        "Port"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Port> for crate::IdInput<Port> {
    fn from(value: Port) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Port> for crate::IdInput<super::NodeClient> {
    fn from(value: Port) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Port {
    #[doc = "The port description.\n\nSelects GraphQL Wire_Name `description` on `Port`."]
    pub async fn description(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "Skip the health check when run as a service.\n\nSelects GraphQL Wire_Name `experimentalSkipHealthcheck` on `Port`."]
    pub async fn experimental_skip_healthcheck(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("experimentalSkipHealthcheck");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Port.\n\nSelects GraphQL Wire_Name `id` on `Port`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The port number.\n\nSelects GraphQL Wire_Name `port` on `Port`."]
    pub async fn port(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("port");
        query.execute(&self.session).await
    }
    #[doc = "The transport layer protocol.\n\nSelects GraphQL Wire_Name `protocol` on `Port`."]
    pub async fn protocol(&self) -> Result<super::NetworkProtocol, crate::QueryError> {
        let query = self.selection.select("protocol");
        query.execute(&self.session).await
    }
}
impl super::Node for Port {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
