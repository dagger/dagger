//! Generated bindings owned by the GraphQL `CurrentModule` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Reflective module API provided to functions at runtime."]
#[derive(Clone)]
pub struct CurrentModule {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `CurrentModule.asSDK`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct CurrentModuleAsSdkOpts {
    #[doc = "The workspace to resolve SDK-role data against. Defaults to the current workspace.\n\n`None` omits GraphQL Wire_Name `workspace`."]
    pub workspace: Option<crate::IdInput<super::Workspace>>,
}
impl CurrentModuleAsSdkOpts {
    #[doc = "Sets GraphQL argument `workspace` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_workspace(mut self, value: crate::IdInput<super::Workspace>) -> Self {
        self.workspace = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `CurrentModule.generators`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct CurrentModuleGeneratorsOpts {
    #[doc = "Only include generators matching the specified patterns\n\n`None` omits GraphQL Wire_Name `include`."]
    pub include: Option<Vec<String>>,
}
impl CurrentModuleGeneratorsOpts {
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `CurrentModule.workdir`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct CurrentModuleWorkdirOpts {
    #[doc = "Exclude artifacts that match the given pattern (e.g., \\[\"node_modules/\", \".git*\"\\]).\n\n`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "Apply .gitignore filter rules inside the directory\n\n`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "Include only artifacts that match the given pattern (e.g., \\[\"app/\", \"package.*\"\\]).\n\n`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
}
impl CurrentModuleWorkdirOpts {
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
}
impl crate::IntoID<crate::Id> for CurrentModule {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for CurrentModule {
    fn graphql_type() -> &'static str {
        "CurrentModule"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<CurrentModule> for crate::IdInput<CurrentModule> {
    fn from(value: CurrentModule) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<CurrentModule> for crate::IdInput<super::NodeClient> {
    fn from(value: CurrentModule) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl CurrentModule {
    #[doc = "Treat the currently executing module as an SDK installed in the given workspace, exposing the modules and clients it manages.\n\nErrors if the current module is not installed as an SDK in this workspace.\n\nSelects GraphQL Wire_Name `asSDK` on `CurrentModule`."]
    #[must_use]
    pub fn as_sdk(&self) -> super::CurrentModuleAsSdk {
        let query = self.selection.select("asSDK");
        super::CurrentModuleAsSdk {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asSDK` with a borrowed, reusable `CurrentModuleAsSdkOpts` value."]
    #[must_use]
    pub fn as_sdk_opts(&self, opts: &CurrentModuleAsSdkOpts) -> super::CurrentModuleAsSdk {
        let query = self.selection.select("asSDK");
        let query = if let Some(value) = &opts.workspace {
            query.arg_id_input("workspace", value.clone())
        } else {
            query
        };
        super::CurrentModuleAsSdk {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The dependencies of the module.\n\nSelects GraphQL Wire_Name `dependencies` on `CurrentModule`."]
    pub async fn dependencies(&self) -> Result<Vec<super::Module>, crate::QueryError> {
        let query = self.selection.select("dependencies");
        let query = query.select("id");
        query
            .execute_reentry::<super::Module, Vec<crate::Id>>(&self.session, "Module")
            .await
    }
    #[doc = "The generated files and directories made on top of the module source's context directory.\n\nSelects GraphQL Wire_Name `generatedContextDirectory` on `CurrentModule`."]
    #[must_use]
    pub fn generated_context_directory(&self) -> super::Directory {
        let query = self.selection.select("generatedContextDirectory");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return all generators defined by the module\n\nSelects GraphQL Wire_Name `generators` on `CurrentModule`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn generators(&self) -> super::GeneratorGroup {
        let query = self.selection.select("generators");
        super::GeneratorGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `generators` with a borrowed, reusable `CurrentModuleGeneratorsOpts` value.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn generators_opts(&self, opts: &CurrentModuleGeneratorsOpts) -> super::GeneratorGroup {
        let query = self.selection.select("generators");
        let query = if let Some(value) = &opts.include {
            query.arg("include", value)
        } else {
            query
        };
        super::GeneratorGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this CurrentModule.\n\nSelects GraphQL Wire_Name `id` on `CurrentModule`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the module being executed in\n\nSelects GraphQL Wire_Name `name` on `CurrentModule`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).\n\nSelects GraphQL Wire_Name `source` on `CurrentModule`."]
    #[must_use]
    pub fn source(&self) -> super::Directory {
        let query = self.selection.select("source");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.\n\nSelects GraphQL Wire_Name `workdir` on `CurrentModule`."]
    #[must_use]
    pub fn workdir(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("workdir");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `workdir` with a borrowed, reusable `CurrentModuleWorkdirOpts` value."]
    #[must_use]
    pub fn workdir_opts(
        &self,
        path: impl Into<String>,
        opts: &CurrentModuleWorkdirOpts,
    ) -> super::Directory {
        let query = self.selection.select("workdir");
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
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.\n\nSelects GraphQL Wire_Name `workdirFile` on `CurrentModule`."]
    #[must_use]
    pub fn workdir_file(&self, path: impl Into<String>) -> super::File {
        let query = self.selection.select("workdirFile");
        let query = query.arg("path", path.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for CurrentModule {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
