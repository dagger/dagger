//! Generated bindings owned by the GraphQL `Service` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A content-addressed service providing TCP connectivity."]
#[derive(Clone)]
pub struct Service {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Service.endpoint`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ServiceEndpointOpts {
    #[doc = "The exposed port number for the endpoint\n\n`None` omits GraphQL Wire_Name `port`."]
    pub port: Option<i64>,
    #[doc = "Return a URL with the given scheme, eg. http for [http://](http://)\n\n`None` omits GraphQL Wire_Name `scheme` and preserves engine default `String(\"\")`."]
    pub scheme: Option<String>,
}
impl ServiceEndpointOpts {
    #[doc = "Sets GraphQL argument `port` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_port(mut self, value: i64) -> Self {
        self.port = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `scheme` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_scheme(mut self, value: impl Into<String>) -> Self {
        self.scheme = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Service.stop`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ServiceStopOpts {
    #[doc = "Immediately kill the service without waiting for a graceful exit\n\n`None` omits GraphQL Wire_Name `kill` and preserves engine default `Boolean(false)`."]
    pub kill: Option<bool>,
}
impl ServiceStopOpts {
    #[doc = "Sets GraphQL argument `kill` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_kill(mut self, value: bool) -> Self {
        self.kill = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Service.terminal`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ServiceTerminalOpts {
    #[doc = "`None` omits GraphQL Wire_Name `cmd` and preserves engine default `List(\\[\\])`."]
    pub cmd: Option<Vec<String>>,
}
impl ServiceTerminalOpts {
    #[doc = "Sets GraphQL argument `cmd` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cmd(mut self, value: Vec<impl Into<String>>) -> Self {
        self.cmd = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Service.up`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ServiceUpOpts {
    #[doc = "List of frontend/backend port mappings to forward.\n\nFrontend is the port accepting traffic on the host, backend is the service port.\n\n`None` omits GraphQL Wire_Name `ports` and preserves engine default `List(\\[\\])`."]
    pub ports: Option<Vec<super::PortForward>>,
    #[doc = "Bind each tunnel port to a random port on the host.\n\n`None` omits GraphQL Wire_Name `random` and preserves engine default `Boolean(false)`."]
    pub random: Option<bool>,
}
impl ServiceUpOpts {
    #[doc = "Sets GraphQL argument `ports` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ports(mut self, value: Vec<super::PortForward>) -> Self {
        self.ports = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `random` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_random(mut self, value: bool) -> Self {
        self.random = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for Service {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Service {
    fn graphql_type() -> &'static str {
        "Service"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Service> for crate::IdInput<Service> {
    fn from(value: Service) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Service> for crate::IdInput<super::NodeClient> {
    fn from(value: Service) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Service> for crate::IdInput<super::SyncerClient> {
    fn from(value: Service) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Service {
    #[doc = "Retrieves an endpoint that clients can use to reach this container.\n\nIf no port is specified, the first exposed port is used. If none exist an error is returned.\n\nIf a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.\n\nSelects GraphQL Wire_Name `endpoint` on `Service`."]
    pub async fn endpoint(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("endpoint");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `endpoint` with a borrowed, reusable `ServiceEndpointOpts` value."]
    pub async fn endpoint_opts(
        &self,
        opts: &ServiceEndpointOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("endpoint");
        let query = if let Some(value) = &opts.port {
            query.arg("port", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.scheme {
            query.arg("scheme", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Retrieves a hostname which can be used by clients to reach this container.\n\nSelects GraphQL Wire_Name `hostname` on `Service`."]
    pub async fn hostname(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("hostname");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Service.\n\nSelects GraphQL Wire_Name `id` on `Service`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Retrieves the list of ports provided by the service.\n\nSelects GraphQL Wire_Name `ports` on `Service`."]
    pub async fn ports(&self) -> Result<Vec<super::Port>, crate::QueryError> {
        let query = self.selection.select("ports");
        let query = query.select("id");
        query
            .execute_reentry::<super::Port, Vec<crate::Id>>(&self.session, "Port")
            .await
    }
    #[doc = "Start the service and wait for its health checks to succeed.\n\nServices bound to a Container do not need to be manually started.\n\nSelects GraphQL Wire_Name `start` on `Service`."]
    pub async fn start(&self) -> Result<super::Service, crate::QueryError> {
        let query = self.selection.select("start");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Service>(
            &self.session,
            id,
            "Service",
        ))
    }
    #[doc = "Stop the service.\n\nSelects GraphQL Wire_Name `stop` on `Service`."]
    pub async fn stop(&self) -> Result<super::Service, crate::QueryError> {
        let query = self.selection.select("stop");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Service>(
            &self.session,
            id,
            "Service",
        ))
    }
    #[doc = "Executes GraphQL operation `stop` with a borrowed, reusable `ServiceStopOpts` value."]
    pub async fn stop_opts(
        &self,
        opts: &ServiceStopOpts,
    ) -> Result<super::Service, crate::QueryError> {
        let query = self.selection.select("stop");
        let query = if let Some(value) = &opts.kill {
            query.arg("kill", value)
        } else {
            query
        };
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Service>(
            &self.session,
            id,
            "Service",
        ))
    }
    #[doc = "Forces evaluation of the pipeline in the engine.\n\nSelects GraphQL Wire_Name `sync` on `Service`."]
    pub async fn sync(&self) -> Result<super::Service, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Service>(
            &self.session,
            id,
            "Service",
        ))
    }
    #[doc = "Selects GraphQL Wire_Name `terminal` on `Service`."]
    #[must_use]
    pub fn terminal(&self) -> super::Service {
        let query = self.selection.select("terminal");
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `terminal` with a borrowed, reusable `ServiceTerminalOpts` value."]
    #[must_use]
    pub fn terminal_opts(&self, opts: &ServiceTerminalOpts) -> super::Service {
        let query = self.selection.select("terminal");
        let query = if let Some(value) = &opts.cmd {
            query.arg("cmd", value)
        } else {
            query
        };
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates a tunnel that forwards traffic from the caller's network to this service.\n\nSelects GraphQL Wire_Name `up` on `Service`."]
    pub async fn up(&self) -> Result<(), crate::QueryError> {
        let query = self.selection.select("up");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `up` with a borrowed, reusable `ServiceUpOpts` value."]
    pub async fn up_opts(&self, opts: &ServiceUpOpts) -> Result<(), crate::QueryError> {
        let query = self.selection.select("up");
        let query = if let Some(value) = &opts.ports {
            query.arg("ports", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.random {
            query.arg("random", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Configures a hostname which can be used by clients within the session to reach this container.\n\nSelects GraphQL Wire_Name `withHostname` on `Service`."]
    #[must_use]
    pub fn with_hostname(&self, hostname: impl Into<String>) -> super::Service {
        let query = self.selection.select("withHostname");
        let query = query.arg("hostname", hostname.into());
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Service {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for Service {
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
