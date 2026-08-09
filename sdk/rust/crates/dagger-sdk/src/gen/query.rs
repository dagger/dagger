//! Generated bindings owned by the GraphQL `Query` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The root of the DAG."]
#[derive(Clone)]
pub struct Query {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Query.cacheVolume`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryCacheVolumeOpts {
    #[doc = "A user:group to set for the cache volume root.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Sharing mode of the cache volume.\n\n`None` omits GraphQL Wire_Name `sharing` and preserves engine default `Enum(SchemaName(\"SHARED\"))`."]
    pub sharing: Option<super::CacheSharingMode>,
    #[doc = "Identifier of the directory to use as the cache volume's root.\n\n`None` omits GraphQL Wire_Name `source`."]
    pub source: Option<crate::IdInput<super::Directory>>,
}
impl QueryCacheVolumeOpts {
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sharing` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_sharing(mut self, value: super::CacheSharingMode) -> Self {
        self.sharing = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `source` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source(mut self, value: crate::IdInput<super::Directory>) -> Self {
        self.source = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.container`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryContainerOpts {
    #[doc = "Platform to initialize the container with. Defaults to the native platform of the current engine\n\n`None` omits GraphQL Wire_Name `platform`."]
    pub platform: Option<crate::Platform>,
}
impl QueryContainerOpts {
    #[doc = "Sets GraphQL argument `platform` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_platform(mut self, value: crate::Platform) -> Self {
        self.platform = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.currentTypeDefs`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryCurrentTypeDefsOpts {
    #[doc = "Strip core API functions from the Query type, leaving only module-sourced functions (constructors, entrypoint proxies, etc.).\n\nCore types (Container, Directory, etc.) are kept so return types and method chaining still work.\n\n`None` omits GraphQL Wire_Name `hideCore`."]
    pub hide_core: Option<bool>,
    #[doc = "Return the full referenced typedef closure instead of only top-level served typedefs.\n\n`None` omits GraphQL Wire_Name `returnAllTypes` and preserves engine default `Boolean(false)`."]
    pub return_all_types: Option<bool>,
}
impl QueryCurrentTypeDefsOpts {
    #[doc = "Sets GraphQL argument `hideCore` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_hide_core(mut self, value: bool) -> Self {
        self.hide_core = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `returnAllTypes` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_return_all_types(mut self, value: bool) -> Self {
        self.return_all_types = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.engineVolume`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryEngineVolumeOpts {
    #[doc = "Optional existing subdirectory within the volume payload to mount.\n\n`None` omits GraphQL Wire_Name `subdir`."]
    pub subdir: Option<String>,
}
impl QueryEngineVolumeOpts {
    #[doc = "Sets GraphQL argument `subdir` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_subdir(mut self, value: impl Into<String>) -> Self {
        self.subdir = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.envFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryEnvFileOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" with the value of other vars\n\n`None` omits GraphQL Wire_Name `expand`.\n\n**Deprecated:** Variable expansion is now enabled by default"]
    pub expand: Option<bool>,
}
impl QueryEnvFileOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.file`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryFileOpts {
    #[doc = "Permissions of the new file. Example: 0600\n\n`None` omits GraphQL Wire_Name `permissions` and preserves engine default `Int(420)`."]
    pub permissions: Option<i64>,
}
impl QueryFileOpts {
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.git`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryGitOpts {
    #[doc = "A service which must be started before the repo is fetched.\n\n`None` omits GraphQL Wire_Name `experimentalServiceHost`."]
    pub experimental_service_host: Option<crate::IdInput<super::Service>>,
    #[doc = "Secret used to populate the Authorization HTTP header\n\n`None` omits GraphQL Wire_Name `httpAuthHeader`."]
    pub http_auth_header: Option<crate::IdInput<super::Secret>>,
    #[doc = "Secret used to populate the password during basic HTTP Authorization\n\n`None` omits GraphQL Wire_Name `httpAuthToken`."]
    pub http_auth_token: Option<crate::IdInput<super::Secret>>,
    #[doc = "Username used to populate the password during basic HTTP Authorization\n\n`None` omits GraphQL Wire_Name `httpAuthUsername` and preserves engine default `String(\"\")`."]
    pub http_auth_username: Option<String>,
    #[doc = "DEPRECATED: Set to true to keep .git directory.\n\n`None` omits GraphQL Wire_Name `keepGitDir` and preserves engine default `Boolean(true)`.\n\n**Deprecated:** Set to true to keep .git directory."]
    pub keep_git_dir: Option<bool>,
    #[doc = "Set SSH auth socket\n\n`None` omits GraphQL Wire_Name `sshAuthSocket`."]
    pub ssh_auth_socket: Option<crate::IdInput<super::Socket>>,
    #[doc = "Set SSH known hosts\n\n`None` omits GraphQL Wire_Name `sshKnownHosts` and preserves engine default `String(\"\")`."]
    pub ssh_known_hosts: Option<String>,
}
impl QueryGitOpts {
    #[doc = "Sets GraphQL argument `experimentalServiceHost` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_service_host(mut self, value: crate::IdInput<super::Service>) -> Self {
        self.experimental_service_host = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `httpAuthHeader` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_http_auth_header(mut self, value: crate::IdInput<super::Secret>) -> Self {
        self.http_auth_header = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `httpAuthToken` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_http_auth_token(mut self, value: crate::IdInput<super::Secret>) -> Self {
        self.http_auth_token = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `httpAuthUsername` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_http_auth_username(mut self, value: impl Into<String>) -> Self {
        self.http_auth_username = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `keepGitDir` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_keep_git_dir(mut self, value: bool) -> Self {
        self.keep_git_dir = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `sshAuthSocket` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ssh_auth_socket(mut self, value: crate::IdInput<super::Socket>) -> Self {
        self.ssh_auth_socket = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `sshKnownHosts` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ssh_known_hosts(mut self, value: impl Into<String>) -> Self {
        self.ssh_known_hosts = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.http`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryHttpOpts {
    #[doc = "Secret used to populate the Authorization HTTP header\n\n`None` omits GraphQL Wire_Name `authHeader`."]
    pub auth_header: Option<crate::IdInput<super::Secret>>,
    #[doc = "Expected digest of the downloaded content (e.g., \"sha256:...\").\n\n`None` omits GraphQL Wire_Name `checksum`."]
    pub checksum: Option<String>,
    #[doc = "A service which must be started before the URL is fetched.\n\n`None` omits GraphQL Wire_Name `experimentalServiceHost`."]
    pub experimental_service_host: Option<crate::IdInput<super::Service>>,
    #[doc = "File name to use for the file. Defaults to the last part of the URL.\n\n`None` omits GraphQL Wire_Name `name`."]
    pub name: Option<String>,
    #[doc = "Permissions to set on the file.\n\n`None` omits GraphQL Wire_Name `permissions`."]
    pub permissions: Option<i64>,
}
impl QueryHttpOpts {
    #[doc = "Sets GraphQL argument `authHeader` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_auth_header(mut self, value: crate::IdInput<super::Secret>) -> Self {
        self.auth_header = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `checksum` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_checksum(mut self, value: impl Into<String>) -> Self {
        self.checksum = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `experimentalServiceHost` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_service_host(mut self, value: crate::IdInput<super::Service>) -> Self {
        self.experimental_service_host = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `name` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_name(mut self, value: impl Into<String>) -> Self {
        self.name = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.llm`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryLlmOpts {
    #[doc = "The model to converse with, e.g. \"claude-sonnet-4-5\" or \"gpt-5.4\". Defaults to the configured default model.\n\n`None` omits GraphQL Wire_Name `model`."]
    pub model: Option<String>,
    #[doc = "The provider serving the model, e.g. \"openai\". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.\n\n`None` omits GraphQL Wire_Name `provider`."]
    pub provider: Option<String>,
}
impl QueryLlmOpts {
    #[doc = "Sets GraphQL argument `model` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_model(mut self, value: impl Into<String>) -> Self {
        self.model = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `provider` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_provider(mut self, value: impl Into<String>) -> Self {
        self.provider = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.moduleSource`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QueryModuleSourceOpts {
    #[doc = "If true, do not error out if the provided ref string is a local path and does not exist yet. Useful when initializing new modules in directories that don't exist yet.\n\n`None` omits GraphQL Wire_Name `allowNotExists` and preserves engine default `Boolean(false)`."]
    pub allow_not_exists: Option<bool>,
    #[doc = "If true, do not attempt to find a module config file in a parent directory of the provided path. Only relevant for local module sources.\n\n`None` omits GraphQL Wire_Name `disableFindUp` and preserves engine default `Boolean(false)`."]
    pub disable_find_up: Option<bool>,
    #[doc = "The pinned version of the module source\n\n`None` omits GraphQL Wire_Name `refPin` and preserves engine default `String(\"\")`."]
    pub ref_pin: Option<String>,
    #[doc = "If set, error out if the ref string is not of the provided requireKind.\n\n`None` omits GraphQL Wire_Name `requireKind`."]
    pub require_kind: Option<super::ModuleSourceKind>,
}
impl QueryModuleSourceOpts {
    #[doc = "Sets GraphQL argument `allowNotExists` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_allow_not_exists(mut self, value: bool) -> Self {
        self.allow_not_exists = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `disableFindUp` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_disable_find_up(mut self, value: bool) -> Self {
        self.disable_find_up = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `refPin` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ref_pin(mut self, value: impl Into<String>) -> Self {
        self.ref_pin = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `requireKind` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_require_kind(mut self, value: super::ModuleSourceKind) -> Self {
        self.require_kind = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.secret`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QuerySecretOpts {
    #[doc = "If set, the given string will be used as the cache key for this secret. This means that any secrets with the same cache key will be considered equivalent in terms of cache lookups, even if they have different URIs or plaintext values.\n\nFor example, two secrets with the same cache key provided as secret env vars to other wise equivalent containers will result in the container withExecs hitting the cache for each other.\n\nIf not set, the cache key for the secret will be derived from its plaintext value as looked up when the secret is constructed.\n\n`None` omits GraphQL Wire_Name `cacheKey`."]
    pub cache_key: Option<String>,
}
impl QuerySecretOpts {
    #[doc = "Sets GraphQL argument `cacheKey` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cache_key(mut self, value: impl Into<String>) -> Self {
        self.cache_key = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Query.sshfsVolume`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct QuerySshfsVolumeOpts {
    #[doc = "Optional cache equivalence key. If set, volumes with the same cacheKey may be considered equivalent for cache lookups, still subject to their resource dependencies.\n\n`None` omits GraphQL Wire_Name `cacheKey`."]
    pub cache_key: Option<String>,
    #[doc = "Service to use as the SSHFS network endpoint while verifying the original host key.\n\n`None` omits GraphQL Wire_Name `experimentalServiceHost`."]
    pub experimental_service_host: Option<crate::IdInput<super::Service>>,
    #[doc = "Disable SSH host key verification. This is insecure and must be explicitly opted into.\n\n`None` omits GraphQL Wire_Name `insecureSkipHostKeyCheck` and preserves engine default `Boolean(false)`."]
    pub insecure_skip_host_key_check: Option<bool>,
    #[doc = "known_hosts material used to verify the remote host key. Required unless insecureSkipHostKeyCheck is true.\n\n`None` omits GraphQL Wire_Name `knownHosts`."]
    pub known_hosts: Option<crate::IdInput<super::Secret>>,
}
impl QuerySshfsVolumeOpts {
    #[doc = "Sets GraphQL argument `cacheKey` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cache_key(mut self, value: impl Into<String>) -> Self {
        self.cache_key = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `experimentalServiceHost` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_service_host(mut self, value: crate::IdInput<super::Service>) -> Self {
        self.experimental_service_host = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureSkipHostKeyCheck` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_skip_host_key_check(mut self, value: bool) -> Self {
        self.insecure_skip_host_key_check = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `knownHosts` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_known_hosts(mut self, value: crate::IdInput<super::Secret>) -> Self {
        self.known_hosts = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for Query {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Query {
    fn graphql_type() -> &'static str {
        "Query"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Query> for crate::IdInput<Query> {
    fn from(value: Query) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Query> for crate::IdInput<super::NodeClient> {
    fn from(value: Query) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Query {
    #[doc = "initialize an address to load directories, containers, secrets or other object types.\n\nSelects GraphQL Wire_Name `address` on `Query`."]
    #[must_use]
    pub fn address(&self, value: impl Into<String>) -> super::Address {
        let query = self.selection.select("address");
        let query = query.arg("value", value.into());
        super::Address {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Constructs a cache volume for a given cache key.\n\nSelects GraphQL Wire_Name `cacheVolume` on `Query`."]
    #[must_use]
    pub fn cache_volume(&self, key: impl Into<String>) -> super::CacheVolume {
        let query = self.selection.select("cacheVolume");
        let query = query.arg("key", key.into());
        super::CacheVolume {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `cacheVolume` with a borrowed, reusable `QueryCacheVolumeOpts` value."]
    #[must_use]
    pub fn cache_volume_opts(
        &self,
        key: impl Into<String>,
        opts: &QueryCacheVolumeOpts,
    ) -> super::CacheVolume {
        let query = self.selection.select("cacheVolume");
        let query = query.arg("key", key.into());
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.sharing {
            query.arg("sharing", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.source {
            query.arg_id_input("source", value.clone())
        } else {
            query
        };
        super::CacheVolume {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates an empty changeset\n\nSelects GraphQL Wire_Name `changeset` on `Query`."]
    #[must_use]
    pub fn changeset(&self) -> super::Changeset {
        let query = self.selection.select("changeset");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Dagger Cloud configuration and state\n\nSelects GraphQL Wire_Name `cloud` on `Query`."]
    #[must_use]
    pub fn cloud(&self) -> super::Cloud {
        let query = self.selection.select("cloud");
        super::Cloud {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates a scratch container, with no image or metadata.\n\nTo pull an image, follow up with the \"from\" function.\n\nSelects GraphQL Wire_Name `container` on `Query`."]
    #[must_use]
    pub fn container(&self) -> super::Container {
        let query = self.selection.select("container");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `container` with a borrowed, reusable `QueryContainerOpts` value."]
    #[must_use]
    pub fn container_opts(&self, opts: &QueryContainerOpts) -> super::Container {
        let query = self.selection.select("container");
        let query = if let Some(value) = &opts.platform {
            query.arg("platform", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The FunctionCall context that the SDK caller is currently executing in.\n\nIf the caller is not currently executing in a function, this will return an error.\n\nSelects GraphQL Wire_Name `currentFunctionCall` on `Query`."]
    #[must_use]
    pub fn current_function_call(&self) -> super::FunctionCall {
        let query = self.selection.select("currentFunctionCall");
        super::FunctionCall {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The module currently being served in the session, if any.\n\nSelects GraphQL Wire_Name `currentModule` on `Query`."]
    #[must_use]
    pub fn current_module(&self) -> super::CurrentModule {
        let query = self.selection.select("currentModule");
        super::CurrentModule {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The object that received the current module function call, as a Node. Errors when there is no current call, or the call is top-level (e.g. a module constructor).\n\nSelects GraphQL Wire_Name `currentNode` on `Query`."]
    #[must_use]
    pub fn current_node(&self) -> super::NodeClient {
        let query = self.selection.select("currentNode");
        super::NodeClient {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The TypeDef representations of the objects currently being served in the session.\n\nSelects GraphQL Wire_Name `currentTypeDefs` on `Query`."]
    pub async fn current_type_defs(&self) -> Result<Vec<super::TypeDef>, crate::QueryError> {
        let query = self.selection.select("currentTypeDefs");
        let query = query.select("id");
        query
            .execute_reentry::<super::TypeDef, Vec<crate::Id>>(&self.session, "TypeDef")
            .await
    }
    #[doc = "Executes GraphQL operation `currentTypeDefs` with a borrowed, reusable `QueryCurrentTypeDefsOpts` value."]
    pub async fn current_type_defs_opts(
        &self,
        opts: &QueryCurrentTypeDefsOpts,
    ) -> Result<Vec<super::TypeDef>, crate::QueryError> {
        let query = self.selection.select("currentTypeDefs");
        let query = if let Some(value) = &opts.hide_core {
            query.arg("hideCore", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.return_all_types {
            query.arg("returnAllTypes", value)
        } else {
            query
        };
        let query = query.select("id");
        query
            .execute_reentry::<super::TypeDef, Vec<crate::Id>>(&self.session, "TypeDef")
            .await
    }
    #[doc = "Detect and return the current workspace.\n\nSelects GraphQL Wire_Name `currentWorkspace` on `Query`.\n\n**Experimental:** Highly experimental API extracted from a more ambitious workspace implementation."]
    #[must_use]
    pub fn current_workspace(&self) -> super::Workspace {
        let query = self.selection.select("currentWorkspace");
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The default platform of the engine.\n\nSelects GraphQL Wire_Name `defaultPlatform` on `Query`."]
    pub async fn default_platform(&self) -> Result<crate::Platform, crate::QueryError> {
        let query = self.selection.select("defaultPlatform");
        query.execute(&self.session).await
    }
    #[doc = "Creates an empty directory.\n\nSelects GraphQL Wire_Name `directory` on `Query`."]
    #[must_use]
    pub fn directory(&self) -> super::Directory {
        let query = self.selection.select("directory");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The Dagger engine container configuration and state\n\nSelects GraphQL Wire_Name `engine` on `Query`."]
    #[must_use]
    pub fn engine(&self) -> super::Engine {
        let query = self.selection.select("engine");
        super::Engine {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Constructs an engine-managed volume backed by operator-provided storage beneath the configured engine state root.\n\nSelects GraphQL Wire_Name `engineVolume` on `Query`."]
    #[must_use]
    pub fn engine_volume(&self, name: impl Into<String>) -> super::Volume {
        let query = self.selection.select("engineVolume");
        let query = query.arg("name", name.into());
        super::Volume {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `engineVolume` with a borrowed, reusable `QueryEngineVolumeOpts` value."]
    #[must_use]
    pub fn engine_volume_opts(
        &self,
        name: impl Into<String>,
        opts: &QueryEngineVolumeOpts,
    ) -> super::Volume {
        let query = self.selection.select("engineVolume");
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.subdir {
            query.arg("subdir", value)
        } else {
            query
        };
        super::Volume {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Initialize an environment file\n\nSelects GraphQL Wire_Name `envFile` on `Query`."]
    #[must_use]
    pub fn env_file(&self) -> super::EnvFile {
        let query = self.selection.select("envFile");
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `envFile` with a borrowed, reusable `QueryEnvFileOpts` value."]
    #[must_use]
    pub fn env_file_opts(&self, opts: &QueryEnvFileOpts) -> super::EnvFile {
        let query = self.selection.select("envFile");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Create a new error.\n\nSelects GraphQL Wire_Name `error` on `Query`."]
    #[must_use]
    pub fn error(&self, message: impl Into<String>) -> super::Error {
        let query = self.selection.select("error");
        let query = query.arg("message", message.into());
        super::Error {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates a file with the specified contents.\n\nSelects GraphQL Wire_Name `file` on `Query`."]
    #[must_use]
    pub fn file(&self, contents: impl Into<String>, name: impl Into<String>) -> super::File {
        let query = self.selection.select("file");
        let query = query.arg("contents", contents.into());
        let query = query.arg("name", name.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `file` with a borrowed, reusable `QueryFileOpts` value."]
    #[must_use]
    pub fn file_opts(
        &self,
        contents: impl Into<String>,
        name: impl Into<String>,
        opts: &QueryFileOpts,
    ) -> super::File {
        let query = self.selection.select("file");
        let query = query.arg("contents", contents.into());
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates a function.\n\nSelects GraphQL Wire_Name `function` on `Query`."]
    #[must_use]
    pub fn function(
        &self,
        name: impl Into<String>,
        return_type: impl Into<crate::IdInput<super::TypeDef>>,
    ) -> super::Function {
        let query = self.selection.select("function");
        let query = query.arg("name", name.into());
        let query = query.arg_id_input("returnType", return_type.into());
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Create a code generation result, given a directory containing the generated code.\n\nSelects GraphQL Wire_Name `generatedCode` on `Query`."]
    #[must_use]
    pub fn generated_code(
        &self,
        code: impl Into<crate::IdInput<super::Directory>>,
    ) -> super::GeneratedCode {
        let query = self.selection.select("generatedCode");
        let query = query.arg_id_input("code", code.into());
        super::GeneratedCode {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Queries a Git repository.\n\nSelects GraphQL Wire_Name `git` on `Query`."]
    #[must_use]
    pub fn git(&self, url: impl Into<String>) -> super::GitRepository {
        let query = self.selection.select("git");
        let query = query.arg("url", url.into());
        super::GitRepository {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `git` with a borrowed, reusable `QueryGitOpts` value."]
    #[must_use]
    pub fn git_opts(&self, url: impl Into<String>, opts: &QueryGitOpts) -> super::GitRepository {
        let query = self.selection.select("git");
        let query = if let Some(value) = &opts.experimental_service_host {
            query.arg_id_input("experimentalServiceHost", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.http_auth_header {
            query.arg_id_input("httpAuthHeader", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.http_auth_token {
            query.arg_id_input("httpAuthToken", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.http_auth_username {
            query.arg("httpAuthUsername", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.keep_git_dir {
            query.arg("keepGitDir", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.ssh_auth_socket {
            query.arg_id_input("sshAuthSocket", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.ssh_known_hosts {
            query.arg("sshKnownHosts", value)
        } else {
            query
        };
        let query = query.arg("url", url.into());
        super::GitRepository {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Queries the host environment.\n\nSelects GraphQL Wire_Name `host` on `Query`."]
    #[must_use]
    pub fn host(&self) -> super::Host {
        let query = self.selection.select("host");
        super::Host {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a file containing an http remote url content.\n\nSelects GraphQL Wire_Name `http` on `Query`."]
    #[must_use]
    pub fn http(&self, url: impl Into<String>) -> super::File {
        let query = self.selection.select("http");
        let query = query.arg("url", url.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `http` with a borrowed, reusable `QueryHttpOpts` value."]
    #[must_use]
    pub fn http_opts(&self, url: impl Into<String>, opts: &QueryHttpOpts) -> super::File {
        let query = self.selection.select("http");
        let query = if let Some(value) = &opts.auth_header {
            query.arg_id_input("authHeader", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.checksum {
            query.arg("checksum", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.experimental_service_host {
            query.arg_id_input("experimentalServiceHost", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.name {
            query.arg("name", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        let query = query.arg("url", url.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this Query.\n\nSelects GraphQL Wire_Name `id` on `Query`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Initialize a JSON value\n\nSelects GraphQL Wire_Name `json` on `Query`."]
    #[must_use]
    pub fn json(&self) -> super::JsonValue {
        let query = self.selection.select("json");
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Initialize a new LLM conversation.\n\nSelects GraphQL Wire_Name `llm` on `Query`.\n\n**Experimental:** LLM support is not yet stabilized"]
    #[must_use]
    pub fn llm(&self) -> super::Llm {
        let query = self.selection.select("llm");
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `llm` with a borrowed, reusable `QueryLlmOpts` value.\n\n**Experimental:** LLM support is not yet stabilized"]
    #[must_use]
    pub fn llm_opts(&self, opts: &QueryLlmOpts) -> super::Llm {
        let query = self.selection.select("llm");
        let query = if let Some(value) = &opts.model {
            query.arg("model", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.provider {
            query.arg("provider", value)
        } else {
            query
        };
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Create a new module.\n\nSelects GraphQL Wire_Name `module` on `Query`."]
    #[must_use]
    pub fn module(&self) -> super::Module {
        let query = self.selection.select("module");
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Create a new module source instance from a source ref string\n\nSelects GraphQL Wire_Name `moduleSource` on `Query`."]
    #[must_use]
    pub fn module_source(&self, ref_string: impl Into<String>) -> super::ModuleSource {
        let query = self.selection.select("moduleSource");
        let query = query.arg("refString", ref_string.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `moduleSource` with a borrowed, reusable `QueryModuleSourceOpts` value."]
    #[must_use]
    pub fn module_source_opts(
        &self,
        ref_string: impl Into<String>,
        opts: &QueryModuleSourceOpts,
    ) -> super::ModuleSource {
        let query = self.selection.select("moduleSource");
        let query = if let Some(value) = &opts.allow_not_exists {
            query.arg("allowNotExists", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.disable_find_up {
            query.arg("disableFindUp", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.ref_pin {
            query.arg("refPin", value)
        } else {
            query
        };
        let query = query.arg("refString", ref_string.into());
        let query = if let Some(value) = &opts.require_kind {
            query.arg("requireKind", value)
        } else {
            query
        };
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load any object by its ID.\n\nSelects GraphQL Wire_Name `node` on `Query`."]
    pub async fn node(
        &self,
        id: crate::Id,
    ) -> Result<Option<super::NodeClient>, crate::QueryError> {
        let query = self.selection.select("node");
        let query = query.arg("id", id);
        let query = query.select("id");
        query
            .execute_reentry::<super::NodeClient, Option<crate::Id>>(&self.session, "Node")
            .await
    }
    #[doc = "Load a GraphQL introspection schema for merging.\n\nSelects GraphQL Wire_Name `schema` on `Query`."]
    #[must_use]
    pub fn schema(&self, json: crate::Json) -> super::Schema {
        let query = self.selection.select("schema");
        let query = query.arg("json", json);
        super::Schema {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates a new secret.\n\nSelects GraphQL Wire_Name `secret` on `Query`."]
    #[must_use]
    pub fn secret(&self, uri: impl Into<String>) -> super::Secret {
        let query = self.selection.select("secret");
        let query = query.arg("uri", uri.into());
        super::Secret {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `secret` with a borrowed, reusable `QuerySecretOpts` value."]
    #[must_use]
    pub fn secret_opts(&self, uri: impl Into<String>, opts: &QuerySecretOpts) -> super::Secret {
        let query = self.selection.select("secret");
        let query = if let Some(value) = &opts.cache_key {
            query.arg("cacheKey", value)
        } else {
            query
        };
        let query = query.arg("uri", uri.into());
        super::Secret {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Sets a secret given a user defined name to its plaintext and returns the secret.\n\nThe plaintext value is limited to a size of 128000 bytes.\n\nSelects GraphQL Wire_Name `setSecret` on `Query`."]
    #[must_use]
    pub fn set_secret(
        &self,
        name: impl Into<String>,
        plaintext: impl Into<String>,
    ) -> super::Secret {
        let query = self.selection.select("setSecret");
        let query = query.arg("name", name.into());
        let query = query.arg("plaintext", plaintext.into());
        super::Secret {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates source map metadata.\n\nSelects GraphQL Wire_Name `sourceMap` on `Query`."]
    #[must_use]
    pub fn source_map(
        &self,
        column: i64,
        filename: impl Into<String>,
        line: i64,
    ) -> super::SourceMap {
        let query = self.selection.select("sourceMap");
        let query = query.arg("column", column);
        let query = query.arg("filename", filename.into());
        let query = query.arg("line", line);
        super::SourceMap {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Constructs an SSHFS volume.\n\nSelects GraphQL Wire_Name `sshfsVolume` on `Query`."]
    #[must_use]
    pub fn sshfs_volume(
        &self,
        endpoint: impl Into<String>,
        private_key: impl Into<crate::IdInput<super::Secret>>,
    ) -> super::Volume {
        let query = self.selection.select("sshfsVolume");
        let query = query.arg("endpoint", endpoint.into());
        let query = query.arg_id_input("privateKey", private_key.into());
        super::Volume {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `sshfsVolume` with a borrowed, reusable `QuerySshfsVolumeOpts` value."]
    #[must_use]
    pub fn sshfs_volume_opts(
        &self,
        endpoint: impl Into<String>,
        private_key: impl Into<crate::IdInput<super::Secret>>,
        opts: &QuerySshfsVolumeOpts,
    ) -> super::Volume {
        let query = self.selection.select("sshfsVolume");
        let query = if let Some(value) = &opts.cache_key {
            query.arg("cacheKey", value)
        } else {
            query
        };
        let query = query.arg("endpoint", endpoint.into());
        let query = if let Some(value) = &opts.experimental_service_host {
            query.arg_id_input("experimentalServiceHost", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_skip_host_key_check {
            query.arg("insecureSkipHostKeyCheck", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.known_hosts {
            query.arg_id_input("knownHosts", value.clone())
        } else {
            query
        };
        let query = query.arg_id_input("privateKey", private_key.into());
        super::Volume {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Create a new TypeDef.\n\nSelects GraphQL Wire_Name `typeDef` on `Query`."]
    #[must_use]
    pub fn type_def(&self) -> super::TypeDef {
        let query = self.selection.select("typeDef");
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Get the current Dagger Engine version.\n\nSelects GraphQL Wire_Name `version` on `Query`."]
    pub async fn version(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("version");
        query.execute(&self.session).await
    }
}
impl super::Node for Query {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
