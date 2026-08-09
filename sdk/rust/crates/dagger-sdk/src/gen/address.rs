//! Generated bindings owned by the GraphQL `Address` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A standardized address to load containers, directories, secrets, and other object types. Address format depends on the type, and is validated at type selection."]
#[derive(Clone)]
pub struct Address {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Address.directory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct AddressDirectoryOpts {
    #[doc = "`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
    #[doc = "`None` omits GraphQL Wire_Name `noCache` and preserves engine default `Boolean(false)`."]
    pub no_cache: Option<bool>,
}
impl AddressDirectoryOpts {
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
#[doc = "Owned optional arguments for GraphQL operation `Address.file`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct AddressFileOpts {
    #[doc = "`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
    #[doc = "`None` omits GraphQL Wire_Name `noCache` and preserves engine default `Boolean(false)`."]
    pub no_cache: Option<bool>,
}
impl AddressFileOpts {
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
impl crate::IntoID<crate::Id> for Address {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Address {
    fn graphql_type() -> &'static str {
        "Address"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Address> for crate::IdInput<Address> {
    fn from(value: Address) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Address> for crate::IdInput<super::NodeClient> {
    fn from(value: Address) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Address {
    #[doc = "Load a container from the address.\n\nSelects GraphQL Wire_Name `container` on `Address`."]
    #[must_use]
    pub fn container(&self) -> super::Container {
        let query = self.selection.select("container");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a directory from the address.\n\nSelects GraphQL Wire_Name `directory` on `Address`."]
    #[must_use]
    pub fn directory(&self) -> super::Directory {
        let query = self.selection.select("directory");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `directory` with a borrowed, reusable `AddressDirectoryOpts` value."]
    #[must_use]
    pub fn directory_opts(&self, opts: &AddressDirectoryOpts) -> super::Directory {
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
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a file from the address.\n\nSelects GraphQL Wire_Name `file` on `Address`."]
    #[must_use]
    pub fn file(&self) -> super::File {
        let query = self.selection.select("file");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `file` with a borrowed, reusable `AddressFileOpts` value."]
    #[must_use]
    pub fn file_opts(&self, opts: &AddressFileOpts) -> super::File {
        let query = self.selection.select("file");
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
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a git ref (branch, tag or commit) from the address.\n\nSelects GraphQL Wire_Name `gitRef` on `Address`."]
    #[must_use]
    pub fn git_ref(&self) -> super::GitRef {
        let query = self.selection.select("gitRef");
        super::GitRef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a git repository from the address.\n\nSelects GraphQL Wire_Name `gitRepository` on `Address`."]
    #[must_use]
    pub fn git_repository(&self) -> super::GitRepository {
        let query = self.selection.select("gitRepository");
        super::GitRepository {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this Address.\n\nSelects GraphQL Wire_Name `id` on `Address`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Load a secret from the address.\n\nSelects GraphQL Wire_Name `secret` on `Address`."]
    #[must_use]
    pub fn secret(&self) -> super::Secret {
        let query = self.selection.select("secret");
        super::Secret {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a service from the address.\n\nSelects GraphQL Wire_Name `service` on `Address`."]
    #[must_use]
    pub fn service(&self) -> super::Service {
        let query = self.selection.select("service");
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a local socket from the address.\n\nSelects GraphQL Wire_Name `socket` on `Address`."]
    #[must_use]
    pub fn socket(&self) -> super::Socket {
        let query = self.selection.select("socket");
        super::Socket {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The address value\n\nSelects GraphQL Wire_Name `value` on `Address`."]
    pub async fn value(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("value");
        query.execute(&self.session).await
    }
    #[doc = "Load a volume from the address.\n\nSelects GraphQL Wire_Name `volume` on `Address`."]
    #[must_use]
    pub fn volume(&self) -> super::Volume {
        let query = self.selection.select("volume");
        super::Volume {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Address {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
