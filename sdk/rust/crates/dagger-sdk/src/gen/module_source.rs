//! Generated bindings owned by the GraphQL `ModuleSource` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc."]
#[derive(Clone)]
pub struct ModuleSource {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for ModuleSource {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for ModuleSource {
    fn graphql_type() -> &'static str {
        "ModuleSource"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<ModuleSource> for crate::IdInput<ModuleSource> {
    fn from(value: ModuleSource) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<ModuleSource> for crate::IdInput<super::NodeClient> {
    fn from(value: ModuleSource) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<ModuleSource> for crate::IdInput<super::SyncerClient> {
    fn from(value: ModuleSource) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl ModuleSource {
    #[doc = "Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation\n\nSelects GraphQL Wire_Name `asModule` on `ModuleSource`."]
    #[must_use]
    pub fn as_module(&self) -> super::Module {
        let query = self.selection.select("asModule");
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A human readable ref string representation of this module source.\n\nSelects GraphQL Wire_Name `asString` on `ModuleSource`."]
    pub async fn as_string(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("asString");
        query.execute(&self.session).await
    }
    #[doc = "The blueprint referenced by the module source.\n\nSelects GraphQL Wire_Name `blueprint` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in dagger.toml instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in dagger.toml instead."
    )]
    #[must_use]
    pub fn blueprint(&self) -> super::ModuleSource {
        let query = self.selection.select("blueprint");
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The client-facing introspection schema JSON file for this module source.\n\nThis is the schema consumed by client codegen: unlike introspectionSchemaJSON (the module-facing schema), it hides no core types and installs this module (reached via dag.&lt;moduleName&gt;) so a generated client can bind it. The module's dependencies are excluded: a client is generated for a single module plus core, not its dependency graph.\n\nSelects GraphQL Wire_Name `clientSchemaIntrospectionJSON` on `ModuleSource`."]
    #[must_use]
    pub fn client_schema_introspection_json(&self) -> super::File {
        let query = self.selection.select("clientSchemaIntrospectionJSON");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The ref to clone the root of the git repo from. Only valid for git sources.\n\nSelects GraphQL Wire_Name `cloneRef` on `ModuleSource`."]
    pub async fn clone_ref(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("cloneRef");
        query.execute(&self.session).await
    }
    #[doc = "The resolved commit of the git repo this source points to.\n\nSelects GraphQL Wire_Name `commit` on `ModuleSource`."]
    pub async fn commit(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("commit");
        query.execute(&self.session).await
    }
    #[doc = "The clients generated for the module.\n\nSelects GraphQL Wire_Name `configClients` on `ModuleSource`."]
    pub async fn config_clients(
        &self,
    ) -> Result<Vec<super::ModuleConfigClient>, crate::QueryError> {
        let query = self.selection.select("configClients");
        let query = query.select("id");
        query
            .execute_reentry::<super::ModuleConfigClient, Vec<crate::Id>>(
                &self.session,
                "ModuleConfigClient",
            )
            .await
    }
    #[doc = "Whether an existing module config file was found.\n\nSelects GraphQL Wire_Name `configExists` on `ModuleSource`."]
    pub async fn config_exists(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("configExists");
        query.execute(&self.session).await
    }
    #[doc = "The full directory loaded for the module source, including the source code as a subdirectory.\n\nSelects GraphQL Wire_Name `contextDirectory` on `ModuleSource`."]
    #[must_use]
    pub fn context_directory(&self) -> super::Directory {
        let query = self.selection.select("contextDirectory");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The dependencies of the module source.\n\nSelects GraphQL Wire_Name `dependencies` on `ModuleSource`."]
    pub async fn dependencies(&self) -> Result<Vec<super::ModuleSource>, crate::QueryError> {
        let query = self.selection.select("dependencies");
        let query = query.select("id");
        query
            .execute_reentry::<super::ModuleSource, Vec<crate::Id>>(&self.session, "ModuleSource")
            .await
    }
    #[doc = "A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance.\n\nSelects GraphQL Wire_Name `digest` on `ModuleSource`."]
    pub async fn digest(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("digest");
        query.execute(&self.session).await
    }
    #[doc = "The directory containing the module configuration and source code (source code may be in a subdir).\n\nSelects GraphQL Wire_Name `directory` on `ModuleSource`."]
    #[must_use]
    pub fn directory(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("directory");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The engine version of the module.\n\nSelects GraphQL Wire_Name `engineVersion` on `ModuleSource`."]
    pub async fn engine_version(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("engineVersion");
        query.execute(&self.session).await
    }
    #[doc = "Generate this module's transitive local dependency closure and return the staged changes as a single changeset against the unstaged workspace root.\n\nEach local dependency is generated by its own SDK against a workspace scoped to it, carrying the dependency's own already-generated dependencies. Remote (git) dependencies are assumed committed and skipped. Overlay the result onto the workspace before generating this module; it is not this module's own generated code.\n\nSelects GraphQL Wire_Name `generateLocalDependencies` on `ModuleSource`."]
    #[must_use]
    pub fn generate_local_dependencies(
        &self,
        workspace: impl Into<crate::IdInput<super::Workspace>>,
    ) -> super::Changeset {
        let query = self.selection.select("generateLocalDependencies");
        let query = query.arg_id_input("workspace", workspace.into());
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The generated files and directories made on top of the module source's context directory, returned as a Changeset.\n\nSelects GraphQL Wire_Name `generatedContextChangeset` on `ModuleSource`."]
    #[must_use]
    pub fn generated_context_changeset(&self) -> super::Changeset {
        let query = self.selection.select("generatedContextChangeset");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The generated files and directories made on top of the module source's context directory.\n\nSelects GraphQL Wire_Name `generatedContextDirectory` on `ModuleSource`."]
    #[must_use]
    pub fn generated_context_directory(&self) -> super::Directory {
        let query = self.selection.select("generatedContextDirectory");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket).\n\nSelects GraphQL Wire_Name `htmlRepoURL` on `ModuleSource`."]
    pub async fn html_repo_url(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("htmlRepoURL");
        query.execute(&self.session).await
    }
    #[doc = "The URL to the source's git repo in a web browser. Only valid for git sources.\n\nSelects GraphQL Wire_Name `htmlURL` on `ModuleSource`."]
    pub async fn html_url(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("htmlURL");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this ModuleSource.\n\nSelects GraphQL Wire_Name `id` on `ModuleSource`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The introspection schema JSON file for this module source.\n\nThis file represents the schema visible to the module's source code, including all core types and those from the dependencies.\n\nNote: this is in the context of a module, so some core types may be hidden.\n\nSelects GraphQL Wire_Name `introspectionSchemaJSON` on `ModuleSource`."]
    #[must_use]
    pub fn introspection_schema_json(&self) -> super::File {
        let query = self.selection.select("introspectionSchemaJSON");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The kind of module source (currently local, git or dir).\n\nSelects GraphQL Wire_Name `kind` on `ModuleSource`."]
    pub async fn kind(&self) -> Result<super::ModuleSourceKind, crate::QueryError> {
        let query = self.selection.select("kind");
        query.execute(&self.session).await
    }
    #[doc = "The full absolute path to the context directory on the caller's host filesystem that this module source is loaded from. Only valid for local module sources.\n\nSelects GraphQL Wire_Name `localContextDirectoryPath` on `ModuleSource`."]
    pub async fn local_context_directory_path(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("localContextDirectoryPath");
        query.execute(&self.session).await
    }
    #[doc = "The name of the module, including any setting via the withName API.\n\nSelects GraphQL Wire_Name `moduleName` on `ModuleSource`."]
    pub async fn module_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("moduleName");
        query.execute(&self.session).await
    }
    #[doc = "The original name of the module as read from the module config file (or set for the first time with the withName API).\n\nSelects GraphQL Wire_Name `moduleOriginalName` on `ModuleSource`."]
    pub async fn module_original_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("moduleOriginalName");
        query.execute(&self.session).await
    }
    #[doc = "The original subpath used when instantiating this module source, relative to the context directory.\n\nSelects GraphQL Wire_Name `originalSubpath` on `ModuleSource`."]
    pub async fn original_subpath(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("originalSubpath");
        query.execute(&self.session).await
    }
    #[doc = "The pinned version of this module source.\n\nSelects GraphQL Wire_Name `pin` on `ModuleSource`."]
    pub async fn pin(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("pin");
        query.execute(&self.session).await
    }
    #[doc = "The import path corresponding to the root of the git repo this source points to. Only valid for git sources.\n\nSelects GraphQL Wire_Name `repoRootPath` on `ModuleSource`."]
    pub async fn repo_root_path(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("repoRootPath");
        query.execute(&self.session).await
    }
    #[doc = "The SDK configuration of the module.\n\nSelects GraphQL Wire_Name `sdk` on `ModuleSource`."]
    pub async fn sdk(&self) -> Result<Option<super::SdkConfig>, crate::QueryError> {
        let query = self.selection.select("sdk");
        let query = query.select("id");
        query
            .execute_reentry::<super::SdkConfig, Option<crate::Id>>(&self.session, "SDKConfig")
            .await
    }
    #[doc = "The path, relative to the context directory, that contains the module config.\n\nSelects GraphQL Wire_Name `sourceRootSubpath` on `ModuleSource`."]
    pub async fn source_root_subpath(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("sourceRootSubpath");
        query.execute(&self.session).await
    }
    #[doc = "The path to the directory containing the module's source code, relative to the context directory.\n\nSelects GraphQL Wire_Name `sourceSubpath` on `ModuleSource`."]
    pub async fn source_subpath(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("sourceSubpath");
        query.execute(&self.session).await
    }
    #[doc = "Forces evaluation of the module source, including any loading into the engine and associated validation.\n\nSelects GraphQL Wire_Name `sync` on `ModuleSource`."]
    pub async fn sync(&self) -> Result<super::ModuleSource, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::ModuleSource>(
            &self.session,
            id,
            "ModuleSource",
        ))
    }
    #[doc = "The toolchains referenced by the module source.\n\nSelects GraphQL Wire_Name `toolchains` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in dagger.toml instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in dagger.toml instead."
    )]
    pub async fn toolchains(&self) -> Result<Vec<super::ModuleSource>, crate::QueryError> {
        let query = self.selection.select("toolchains");
        let query = query.select("id");
        query
            .execute_reentry::<super::ModuleSource, Vec<crate::Id>>(&self.session, "ModuleSource")
            .await
    }
    #[doc = "The module's dagger.json with any in-memory edits from with* APIs applied, as a diff relative to the source's context directory.\n\nUnlike generatedContextDirectory, this does not run codegen and does not validate the engine version against the running engine, so it can be used to declare an engine requirement newer than the running engine. Loading or serving such a module still fails at moduleSource.asModule.\n\nSelects GraphQL Wire_Name `updatedConfigDirectory` on `ModuleSource`."]
    #[must_use]
    pub fn updated_config_directory(&self) -> super::Directory {
        let query = self.selection.select("updatedConfigDirectory");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "User-defined defaults read from local .env files\n\nSelects GraphQL Wire_Name `userDefaults` on `ModuleSource`."]
    #[must_use]
    pub fn user_defaults(&self) -> super::EnvFile {
        let query = self.selection.select("userDefaults");
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The specified version of the git repo this source points to.\n\nSelects GraphQL Wire_Name `version` on `ModuleSource`."]
    pub async fn version(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("version");
        query.execute(&self.session).await
    }
    #[doc = "Set a blueprint for the module source.\n\nSelects GraphQL Wire_Name `withBlueprint` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."
    )]
    #[must_use]
    pub fn with_blueprint(
        &self,
        blueprint: impl Into<crate::IdInput<super::ModuleSource>>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withBlueprint");
        let query = query.arg_id_input("blueprint", blueprint.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update the module source with a new client to generate.\n\nSelects GraphQL Wire_Name `withClient` on `ModuleSource`."]
    #[must_use]
    pub fn with_client(
        &self,
        generator: impl Into<String>,
        output_dir: impl Into<String>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withClient");
        let query = query.arg("generator", generator.into());
        let query = query.arg("outputDir", output_dir.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Append the provided dependencies to the module source's dependency list.\n\nSelects GraphQL Wire_Name `withDependencies` on `ModuleSource`."]
    #[must_use]
    pub fn with_dependencies(
        &self,
        dependencies: Vec<crate::IdInput<super::ModuleSource>>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withDependencies");
        let query = query.arg_id_input("dependencies", dependencies);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Upgrade the engine version of the module to the given value.\n\nSelects GraphQL Wire_Name `withEngineVersion` on `ModuleSource`."]
    #[must_use]
    pub fn with_engine_version(&self, version: impl Into<String>) -> super::ModuleSource {
        let query = self.selection.select("withEngineVersion");
        let query = query.arg("version", version.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Enable the experimental features for the module source.\n\nSelects GraphQL Wire_Name `withExperimentalFeatures` on `ModuleSource`."]
    #[must_use]
    pub fn with_experimental_features(
        &self,
        features: Vec<super::ModuleSourceExperimentalFeature>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withExperimentalFeatures");
        let query = query.arg("features", features);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update the module source with additional include patterns for files+directories from its context that are required for building it\n\nSelects GraphQL Wire_Name `withIncludes` on `ModuleSource`."]
    #[must_use]
    pub fn with_includes(&self, patterns: Vec<impl Into<String>>) -> super::ModuleSource {
        let query = self.selection.select("withIncludes");
        let patterns = patterns
            .into_iter()
            .map(Into::into)
            .collect::<Vec<String>>();
        let query = query.arg("patterns", patterns);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update the module source with a new name.\n\nSelects GraphQL Wire_Name `withName` on `ModuleSource`."]
    #[must_use]
    pub fn with_name(&self, name: impl Into<String>) -> super::ModuleSource {
        let query = self.selection.select("withName");
        let query = query.arg("name", name.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update the module source with a new SDK.\n\nSelects GraphQL Wire_Name `withSDK` on `ModuleSource`."]
    #[must_use]
    pub fn with_sdk(&self, source: impl Into<String>) -> super::ModuleSource {
        let query = self.selection.select("withSDK");
        let query = query.arg("source", source.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update the module source with a new source subpath.\n\nSelects GraphQL Wire_Name `withSourceSubpath` on `ModuleSource`."]
    #[must_use]
    pub fn with_source_subpath(&self, path: impl Into<String>) -> super::ModuleSource {
        let query = self.selection.select("withSourceSubpath");
        let query = query.arg("path", path.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Add toolchains to the module source.\n\nSelects GraphQL Wire_Name `withToolchains` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."
    )]
    #[must_use]
    pub fn with_toolchains(
        &self,
        toolchains: Vec<crate::IdInput<super::ModuleSource>>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withToolchains");
        let query = query.arg_id_input("toolchains", toolchains);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update the blueprint module to the latest version.\n\nSelects GraphQL Wire_Name `withUpdateBlueprint` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."
    )]
    #[must_use]
    pub fn with_update_blueprint(&self) -> super::ModuleSource {
        let query = self.selection.select("withUpdateBlueprint");
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update one or more module dependencies.\n\nSelects GraphQL Wire_Name `withUpdateDependencies` on `ModuleSource`."]
    #[must_use]
    pub fn with_update_dependencies(
        &self,
        dependencies: Vec<impl Into<String>>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withUpdateDependencies");
        let dependencies = dependencies
            .into_iter()
            .map(Into::into)
            .collect::<Vec<String>>();
        let query = query.arg("dependencies", dependencies);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update one or more toolchains.\n\nSelects GraphQL Wire_Name `withUpdateToolchains` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."
    )]
    #[must_use]
    pub fn with_update_toolchains(
        &self,
        toolchains: Vec<impl Into<String>>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withUpdateToolchains");
        let toolchains = toolchains
            .into_iter()
            .map(Into::into)
            .collect::<Vec<String>>();
        let query = query.arg("toolchains", toolchains);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Update one or more clients.\n\nSelects GraphQL Wire_Name `withUpdatedClients` on `ModuleSource`."]
    #[must_use]
    pub fn with_updated_clients(&self, clients: Vec<impl Into<String>>) -> super::ModuleSource {
        let query = self.selection.select("withUpdatedClients");
        let clients = clients.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("clients", clients);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Remove the current blueprint from the module source.\n\nSelects GraphQL Wire_Name `withoutBlueprint` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."
    )]
    #[must_use]
    pub fn without_blueprint(&self) -> super::ModuleSource {
        let query = self.selection.select("withoutBlueprint");
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Remove a client from the module source.\n\nSelects GraphQL Wire_Name `withoutClient` on `ModuleSource`."]
    #[must_use]
    pub fn without_client(&self, path: impl Into<String>) -> super::ModuleSource {
        let query = self.selection.select("withoutClient");
        let query = query.arg("path", path.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Remove the provided dependencies from the module source's dependency list.\n\nSelects GraphQL Wire_Name `withoutDependencies` on `ModuleSource`."]
    #[must_use]
    pub fn without_dependencies(
        &self,
        dependencies: Vec<impl Into<String>>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withoutDependencies");
        let dependencies = dependencies
            .into_iter()
            .map(Into::into)
            .collect::<Vec<String>>();
        let query = query.arg("dependencies", dependencies);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Disable experimental features for the module source.\n\nSelects GraphQL Wire_Name `withoutExperimentalFeatures` on `ModuleSource`."]
    #[must_use]
    pub fn without_experimental_features(
        &self,
        features: Vec<super::ModuleSourceExperimentalFeature>,
    ) -> super::ModuleSource {
        let query = self.selection.select("withoutExperimentalFeatures");
        let query = query.arg("features", features);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Remove the provided toolchains from the module source.\n\nSelects GraphQL Wire_Name `withoutToolchains` on `ModuleSource`.\n\n**Deprecated:** Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."]
    #[deprecated(
        note = "Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead."
    )]
    #[must_use]
    pub fn without_toolchains(&self, toolchains: Vec<impl Into<String>>) -> super::ModuleSource {
        let query = self.selection.select("withoutToolchains");
        let toolchains = toolchains
            .into_iter()
            .map(Into::into)
            .collect::<Vec<String>>();
        let query = query.arg("toolchains", toolchains);
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for ModuleSource {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for ModuleSource {
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
