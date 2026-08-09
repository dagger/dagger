//! Generated bindings owned by the GraphQL `Host` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Information about the host environment."]
#[derive(Clone)]
pub struct Host {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Host.directory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct HostDirectoryOpts {
    #[doc = "Exclude artifacts that match the given pattern (e.g., \\[\"node_modules/\", \".git*\"\\]).\n\n`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "Apply .gitignore filter rules inside the directory\n\n`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "Include only artifacts that match the given pattern (e.g., \\[\"app/\", \"package.*\"\\]).\n\n`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
    #[doc = "If true, the directory will always be reloaded from the host.\n\n`None` omits GraphQL Wire_Name `noCache` and preserves engine default `Boolean(false)`."]
    pub no_cache: Option<bool>,
}
impl HostDirectoryOpts {
    #[doc = "Sets GraphQL argument `exclude` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_exclude(mut self, value: Vec<impl Into<String>>) -> Self {
        self.exclude = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `gitignore` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_gitignore(mut self, value: bool) -> Self {
        self.gitignore = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `noCache` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_cache(mut self, value: bool) -> Self {
        self.no_cache = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Host.file`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct HostFileOpts {
    #[doc = "If true, the file will always be reloaded from the host.\n\n`None` omits GraphQL Wire_Name `noCache` and preserves engine default `Boolean(false)`."]
    pub no_cache: Option<bool>,
}
impl HostFileOpts {
    #[doc = "Sets GraphQL argument `noCache` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_cache(mut self, value: bool) -> Self {
        self.no_cache = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Host.findUp`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct HostFindUpOpts {
    #[doc = "`None` omits GraphQL Wire_Name `noCache` and preserves engine default `Boolean(false)`."]
    pub no_cache: Option<bool>,
}
impl HostFindUpOpts {
    #[doc = "Sets GraphQL argument `noCache` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_cache(mut self, value: bool) -> Self {
        self.no_cache = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Host.service`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct HostServiceOpts {
    #[doc = "Upstream host to forward traffic to.\n\n`None` omits GraphQL Wire_Name `host` and preserves engine default `String(\"localhost\")`."]
    pub host: Option<String>,
}
impl HostServiceOpts {
    #[doc = "Sets GraphQL argument `host` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_host(mut self, value: impl Into<String>) -> Self {
        self.host = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Host.tunnel`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct HostTunnelOpts {
    #[doc = "Map each service port to the same port on the host, as if the service were running natively.\n\nNote: enabling may result in port conflicts.\n\n`None` omits GraphQL Wire_Name `native` and preserves engine default `Boolean(false)`."]
    pub native: Option<bool>,
    #[doc = "Configure explicit port forwarding rules for the tunnel.\n\nIf a port's frontend is unspecified or 0, a random port will be chosen by the host.\n\nIf no ports are given, all of the service's ports are forwarded. If native is true, each port maps to the same port on the host. If native is false, each port maps to a random port chosen by the host.\n\nIf ports are given and native is true, the ports are additive.\n\n`None` omits GraphQL Wire_Name `ports` and preserves engine default `List(\\[\\])`."]
    pub ports: Option<Vec<super::PortForward>>,
}
impl HostTunnelOpts {
    #[doc = "Sets GraphQL argument `native` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_native(mut self, value: bool) -> Self {
        self.native = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `ports` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ports(mut self, value: Vec<super::PortForward>) -> Self {
        self.ports = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for Host {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Host {
    fn graphql_type() -> &'static str {
        "Host"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Host> for crate::IdInput<Host> {
    fn from(value: Host) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Host> for crate::IdInput<super::NodeClient> {
    fn from(value: Host) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Host {
    #[doc = "Accesses a container image on the host.\n\nSelects GraphQL Wire_Name `containerImage` on `Host`."]
    #[must_use]
    pub fn container_image(&self, name: impl Into<String>) -> super::Container {
        let query = self.selection.select("containerImage");
        let query = query.arg("name", name.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Accesses a directory on the host.\n\nSelects GraphQL Wire_Name `directory` on `Host`."]
    #[must_use]
    pub fn directory(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("directory");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `directory` with a borrowed, reusable `HostDirectoryOpts` value."]
    #[must_use]
    pub fn directory_opts(
        &self,
        path: impl Into<String>,
        opts: &HostDirectoryOpts,
    ) -> super::Directory {
        let query = self.selection.select("directory");
        let query = if let Some(value) = &opts.exclude {
            query.arg("exclude", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.gitignore {
            query.arg("gitignore", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.include {
            query.arg("include", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.no_cache {
            query.arg("noCache", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Accesses a file on the host.\n\nSelects GraphQL Wire_Name `file` on `Host`."]
    #[must_use]
    pub fn file(&self, path: impl Into<String>) -> super::File {
        let query = self.selection.select("file");
        let query = query.arg("path", path.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `file` with a borrowed, reusable `HostFileOpts` value."]
    #[must_use]
    pub fn file_opts(&self, path: impl Into<String>, opts: &HostFileOpts) -> super::File {
        let query = self.selection.select("file");
        let query = if let Some(value) = &opts.no_cache {
            query.arg("noCache", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Search for a file or directory by walking up the tree from system workdir. Return its relative path. If no match, return null\n\nSelects GraphQL Wire_Name `findUp` on `Host`."]
    pub async fn find_up(
        &self,
        name: impl Into<String>,
    ) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("findUp");
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `findUp` with a borrowed, reusable `HostFindUpOpts` value."]
    pub async fn find_up_opts(
        &self,
        name: impl Into<String>,
        opts: &HostFindUpOpts,
    ) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("findUp");
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.no_cache {
            query.arg("noCache", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Host.\n\nSelects GraphQL Wire_Name `id` on `Host`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Creates a service that forwards traffic to a specified address via the host.\n\nSelects GraphQL Wire_Name `service` on `Host`."]
    #[must_use]
    pub fn service(&self, ports: Vec<super::PortForward>) -> super::Service {
        let query = self.selection.select("service");
        let query = query.arg("ports", ports);
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `service` with a borrowed, reusable `HostServiceOpts` value."]
    #[must_use]
    pub fn service_opts(
        &self,
        ports: Vec<super::PortForward>,
        opts: &HostServiceOpts,
    ) -> super::Service {
        let query = self.selection.select("service");
        let query = if let Some(value) = &opts.host {
            query.arg("host", value)
        } else {
            query
        };
        let query = query.arg("ports", ports);
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates a tunnel that forwards traffic from the host to a service.\n\nSelects GraphQL Wire_Name `tunnel` on `Host`."]
    #[must_use]
    pub fn tunnel(&self, service: impl Into<crate::IdInput<super::Service>>) -> super::Service {
        let query = self.selection.select("tunnel");
        let query = query.arg_id_input("service", service.into());
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `tunnel` with a borrowed, reusable `HostTunnelOpts` value."]
    #[must_use]
    pub fn tunnel_opts(
        &self,
        service: impl Into<crate::IdInput<super::Service>>,
        opts: &HostTunnelOpts,
    ) -> super::Service {
        let query = self.selection.select("tunnel");
        let query = if let Some(value) = &opts.native {
            query.arg("native", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.ports {
            query.arg("ports", value)
        } else {
            query
        };
        let query = query.arg_id_input("service", service.into());
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Accesses a Unix socket on the host.\n\nSelects GraphQL Wire_Name `unixSocket` on `Host`."]
    #[must_use]
    pub fn unix_socket(&self, path: impl Into<String>) -> super::Socket {
        let query = self.selection.select("unixSocket");
        let query = query.arg("path", path.into());
        super::Socket {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Host {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
