//! Generated bindings owned by the GraphQL `Module` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A Dagger module."]
#[derive(Clone)]
pub struct Module {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Module.checks`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ModuleChecksOpts {
    #[doc = "Only include checks matching the specified patterns\n\n`None` omits GraphQL Wire_Name `include`."]
    pub include: Option<Vec<String>>,
    #[doc = "When true, only return annotated check functions; exclude generate-as-checks\n\n`None` omits GraphQL Wire_Name `noGenerate`."]
    pub no_generate: Option<bool>,
}
impl ModuleChecksOpts {
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `noGenerate` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_generate(mut self, value: bool) -> Self {
        self.no_generate = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Module.generators`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ModuleGeneratorsOpts {
    #[doc = "Only include generators matching the specified patterns\n\n`None` omits GraphQL Wire_Name `include`."]
    pub include: Option<Vec<String>>,
}
impl ModuleGeneratorsOpts {
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Module.serve`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ModuleServeOpts {
    #[doc = "Install the module as the entrypoint, promoting its main-object methods onto the Query root\n\n`None` omits GraphQL Wire_Name `entrypoint`."]
    pub entrypoint: Option<bool>,
    #[doc = "Expose the dependencies of this module to the client\n\n`None` omits GraphQL Wire_Name `includeDependencies`."]
    pub include_dependencies: Option<bool>,
}
impl ModuleServeOpts {
    #[doc = "Sets GraphQL argument `entrypoint` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_entrypoint(mut self, value: bool) -> Self {
        self.entrypoint = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `includeDependencies` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include_dependencies(mut self, value: bool) -> Self {
        self.include_dependencies = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Module.services`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ModuleServicesOpts {
    #[doc = "Only include services matching the specified patterns\n\n`None` omits GraphQL Wire_Name `include`."]
    pub include: Option<Vec<String>>,
}
impl ModuleServicesOpts {
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
impl crate::IntoID<crate::Id> for Module {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Module {
    fn graphql_type() -> &'static str {
        "Module"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Module> for crate::IdInput<Module> {
    fn from(value: Module) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Module> for crate::IdInput<super::NodeClient> {
    fn from(value: Module) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Module> for crate::IdInput<super::SyncerClient> {
    fn from(value: Module) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Module {
    #[doc = "Return the check defined by the module with the given name. Must match to exactly one check.\n\nSelects GraphQL Wire_Name `check` on `Module`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn check(&self, name: impl Into<String>) -> super::Check {
        let query = self.selection.select("check");
        let query = query.arg("name", name.into());
        super::Check {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return all checks defined by the module\n\nSelects GraphQL Wire_Name `checks` on `Module`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn checks(&self) -> super::CheckGroup {
        let query = self.selection.select("checks");
        super::CheckGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `checks` with a borrowed, reusable `ModuleChecksOpts` value.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn checks_opts(&self, opts: &ModuleChecksOpts) -> super::CheckGroup {
        let query = self.selection.select("checks");
        let query = if let Some(value) = &opts.include {
            query.arg("include", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.no_generate {
            query.arg("noGenerate", value)
        } else {
            query
        };
        super::CheckGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The dependencies of the module.\n\nSelects GraphQL Wire_Name `dependencies` on `Module`."]
    pub async fn dependencies(&self) -> Result<Vec<super::Module>, crate::QueryError> {
        let query = self.selection.select("dependencies");
        let query = query.select("id");
        query
            .execute_reentry::<super::Module, Vec<crate::Id>>(&self.session, "Module")
            .await
    }
    #[doc = "The doc string of the module, if any\n\nSelects GraphQL Wire_Name `description` on `Module`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "Enumerations served by this module.\n\nSelects GraphQL Wire_Name `enums` on `Module`."]
    pub async fn enums(&self) -> Result<Vec<super::TypeDef>, crate::QueryError> {
        let query = self.selection.select("enums");
        let query = query.select("id");
        query
            .execute_reentry::<super::TypeDef, Vec<crate::Id>>(&self.session, "TypeDef")
            .await
    }
    #[doc = "The generated files and directories made on top of the module source's context directory.\n\nSelects GraphQL Wire_Name `generatedContextDirectory` on `Module`."]
    #[must_use]
    pub fn generated_context_directory(&self) -> super::Directory {
        let query = self.selection.select("generatedContextDirectory");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return the generator defined by the module with the given name. Must match to exactly one generator.\n\nSelects GraphQL Wire_Name `generator` on `Module`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn generator(&self, name: impl Into<String>) -> super::Generator {
        let query = self.selection.select("generator");
        let query = query.arg("name", name.into());
        super::Generator {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return all generators defined by the module\n\nSelects GraphQL Wire_Name `generators` on `Module`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn generators(&self) -> super::GeneratorGroup {
        let query = self.selection.select("generators");
        super::GeneratorGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `generators` with a borrowed, reusable `ModuleGeneratorsOpts` value.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn generators_opts(&self, opts: &ModuleGeneratorsOpts) -> super::GeneratorGroup {
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
    #[doc = "A unique identifier for this Module.\n\nSelects GraphQL Wire_Name `id` on `Module`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Interfaces served by this module.\n\nSelects GraphQL Wire_Name `interfaces` on `Module`."]
    pub async fn interfaces(&self) -> Result<Vec<super::TypeDef>, crate::QueryError> {
        let query = self.selection.select("interfaces");
        let query = query.select("id");
        query
            .execute_reentry::<super::TypeDef, Vec<crate::Id>>(&self.session, "TypeDef")
            .await
    }
    #[doc = "The introspection schema JSON file for this module.\n\nThis file represents the schema visible to the module's source code, including all core types and those from the dependencies.\n\nNote: this is in the context of a module, so some core types may be hidden.\n\nSelects GraphQL Wire_Name `introspectionSchemaJSON` on `Module`."]
    #[must_use]
    pub fn introspection_schema_json(&self) -> super::File {
        let query = self.selection.select("introspectionSchemaJSON");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The name of the module\n\nSelects GraphQL Wire_Name `name` on `Module`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "Objects served by this module.\n\nSelects GraphQL Wire_Name `objects` on `Module`."]
    pub async fn objects(&self) -> Result<Vec<super::TypeDef>, crate::QueryError> {
        let query = self.selection.select("objects");
        let query = query.select("id");
        query
            .execute_reentry::<super::TypeDef, Vec<crate::Id>>(&self.session, "TypeDef")
            .await
    }
    #[doc = "The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.\n\nSelects GraphQL Wire_Name `runtime` on `Module`."]
    pub async fn runtime(&self) -> Result<Option<super::Container>, crate::QueryError> {
        let query = self.selection.select("runtime");
        let query = query.select("id");
        query
            .execute_reentry::<super::Container, Option<crate::Id>>(&self.session, "Container")
            .await
    }
    #[doc = "The SDK config used by this module.\n\nSelects GraphQL Wire_Name `sdk` on `Module`."]
    pub async fn sdk(&self) -> Result<Option<super::SdkConfig>, crate::QueryError> {
        let query = self.selection.select("sdk");
        let query = query.select("id");
        query
            .execute_reentry::<super::SdkConfig, Option<crate::Id>>(&self.session, "SDKConfig")
            .await
    }
    #[doc = "Serve a module's API in the current session.\n\nNote: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.\n\nSelects GraphQL Wire_Name `serve` on `Module`."]
    pub async fn serve(&self) -> Result<(), crate::QueryError> {
        let query = self.selection.select("serve");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `serve` with a borrowed, reusable `ModuleServeOpts` value."]
    pub async fn serve_opts(&self, opts: &ModuleServeOpts) -> Result<(), crate::QueryError> {
        let query = self.selection.select("serve");
        let query = if let Some(value) = &opts.entrypoint {
            query.arg("entrypoint", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.include_dependencies {
            query.arg("includeDependencies", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Return all services defined by the module\n\nSelects GraphQL Wire_Name `services` on `Module`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn services(&self) -> super::UpGroup {
        let query = self.selection.select("services");
        super::UpGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `services` with a borrowed, reusable `ModuleServicesOpts` value.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn services_opts(&self, opts: &ModuleServicesOpts) -> super::UpGroup {
        let query = self.selection.select("services");
        let query = if let Some(value) = &opts.include {
            query.arg("include", value)
        } else {
            query
        };
        super::UpGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The source for the module.\n\nSelects GraphQL Wire_Name `source` on `Module`."]
    pub async fn source(&self) -> Result<Option<super::ModuleSource>, crate::QueryError> {
        let query = self.selection.select("source");
        let query = query.select("id");
        query
            .execute_reentry::<super::ModuleSource, Option<crate::Id>>(
                &self.session,
                "ModuleSource",
            )
            .await
    }
    #[doc = "Forces evaluation of the module, including any loading into the engine and associated validation.\n\nSelects GraphQL Wire_Name `sync` on `Module`."]
    pub async fn sync(&self) -> Result<super::Module, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Module>(
            &self.session,
            id,
            "Module",
        ))
    }
    #[doc = "User-defined default values, loaded from local .env files.\n\nSelects GraphQL Wire_Name `userDefaults` on `Module`."]
    #[must_use]
    pub fn user_defaults(&self) -> super::EnvFile {
        let query = self.selection.select("userDefaults");
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves the module with the given description\n\nSelects GraphQL Wire_Name `withDescription` on `Module`."]
    #[must_use]
    pub fn with_description(&self, description: impl Into<String>) -> super::Module {
        let query = self.selection.select("withDescription");
        let query = query.arg("description", description.into());
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "This module plus the given Enum type and associated values\n\nSelects GraphQL Wire_Name `withEnum` on `Module`."]
    #[must_use]
    pub fn with_enum(&self, r#enum: impl Into<crate::IdInput<super::TypeDef>>) -> super::Module {
        let query = self.selection.select("withEnum");
        let query = query.arg_id_input("enum", r#enum.into());
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "This module plus the given Interface type and associated functions\n\nSelects GraphQL Wire_Name `withInterface` on `Module`."]
    #[must_use]
    pub fn with_interface(
        &self,
        iface: impl Into<crate::IdInput<super::TypeDef>>,
    ) -> super::Module {
        let query = self.selection.select("withInterface");
        let query = query.arg_id_input("iface", iface.into());
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "This module plus the given Object type and associated functions.\n\nSelects GraphQL Wire_Name `withObject` on `Module`."]
    #[must_use]
    pub fn with_object(&self, object: impl Into<crate::IdInput<super::TypeDef>>) -> super::Module {
        let query = self.selection.select("withObject");
        let query = query.arg_id_input("object", object.into());
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Module {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for Module {
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
