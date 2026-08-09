//! Generated bindings owned by the GraphQL `Workspace` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A Dagger workspace detected from the current working directory or constructed from a Directory."]
#[derive(Clone)]
pub struct Workspace {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.checks`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceChecksOpts {
    #[doc = "Only include checks matching the specified patterns\n\n`None` omits GraphQL Wire_Name `include`."]
    pub include: Option<Vec<String>>,
    #[doc = "When true, only return annotated check functions; exclude generate-as-checks\n\n`None` omits GraphQL Wire_Name `noGenerate`."]
    pub no_generate: Option<bool>,
    #[doc = "When true, only return generate-as-checks; exclude annotated check functions\n\n`None` omits GraphQL Wire_Name `onlyGenerate`."]
    pub only_generate: Option<bool>,
    #[doc = "Skip checks matching the specified patterns\n\n`None` omits GraphQL Wire_Name `skip`."]
    pub skip: Option<Vec<String>>,
}
impl WorkspaceChecksOpts {
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
    #[doc = "Sets GraphQL argument `onlyGenerate` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_only_generate(mut self, value: bool) -> Self {
        self.only_generate = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `skip` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_skip(mut self, value: Vec<impl Into<String>>) -> Self {
        self.skip = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.configRead`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceConfigReadOpts {
    #[doc = "Dotted key path (e.g. modules.greeter.source). Empty for full config.\n\n`None` omits GraphQL Wire_Name `key` and preserves engine default `String(\"\")`."]
    pub key: Option<String>,
}
impl WorkspaceConfigReadOpts {
    #[doc = "Sets GraphQL argument `key` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_key(mut self, value: impl Into<String>) -> Self {
        self.key = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.directory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceDirectoryOpts {
    #[doc = "Exclude artifacts that match the given pattern (e.g., \\[\"node_modules/\", \".git*\"\\]).\n\n`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "Apply .gitignore filter rules inside the directory.\n\n`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "Include only artifacts that match the given pattern (e.g., \\[\"app/\", \"package.*\"\\]).\n\n`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
}
impl WorkspaceDirectoryOpts {
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
#[doc = "Owned optional arguments for GraphQL operation `Workspace.findUp`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceFindUpOpts {
    #[doc = "Path to start the search from. Relative paths resolve from the workspace cwd; absolute paths resolve from the workspace root.\n\n`None` omits GraphQL Wire_Name `from` and preserves engine default `String(\".\")`."]
    pub from: Option<String>,
}
impl WorkspaceFindUpOpts {
    #[doc = "Sets GraphQL argument `from` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_from(mut self, value: impl Into<String>) -> Self {
        self.from = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.generators`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceGeneratorsOpts {
    #[doc = "Only include generators matching the specified patterns\n\n`None` omits GraphQL Wire_Name `include`."]
    pub include: Option<Vec<String>>,
}
impl WorkspaceGeneratorsOpts {
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.search`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceSearchOpts {
    #[doc = "Allow the . pattern to match newlines in multiline mode.\n\n`None` omits GraphQL Wire_Name `dotall` and preserves engine default `Boolean(false)`."]
    pub dotall: Option<bool>,
    #[doc = "Only return matching files, not lines and content\n\n`None` omits GraphQL Wire_Name `filesOnly` and preserves engine default `Boolean(false)`."]
    pub files_only: Option<bool>,
    #[doc = "Glob patterns to match (e.g., \"*.md\")\n\n`None` omits GraphQL Wire_Name `globs` and preserves engine default `List(\\[\\])`."]
    pub globs: Option<Vec<String>>,
    #[doc = "Enable case-insensitive matching.\n\n`None` omits GraphQL Wire_Name `insensitive` and preserves engine default `Boolean(false)`."]
    pub insensitive: Option<bool>,
    #[doc = "Limit the number of results to return\n\n`None` omits GraphQL Wire_Name `limit`."]
    pub limit: Option<i64>,
    #[doc = "Interpret the pattern as a literal string instead of a regular expression.\n\n`None` omits GraphQL Wire_Name `literal` and preserves engine default `Boolean(false)`."]
    pub literal: Option<bool>,
    #[doc = "Enable searching across multiple lines.\n\n`None` omits GraphQL Wire_Name `multiline` and preserves engine default `Boolean(false)`."]
    pub multiline: Option<bool>,
    #[doc = "Directory or file paths to search\n\n`None` omits GraphQL Wire_Name `paths` and preserves engine default `List(\\[\\])`."]
    pub paths: Option<Vec<String>>,
    #[doc = "Skip hidden files (files starting with .).\n\n`None` omits GraphQL Wire_Name `skipHidden` and preserves engine default `Boolean(false)`."]
    pub skip_hidden: Option<bool>,
    #[doc = "Honor .gitignore, .ignore, and .rgignore files.\n\n`None` omits GraphQL Wire_Name `skipIgnored` and preserves engine default `Boolean(false)`."]
    pub skip_ignored: Option<bool>,
}
impl WorkspaceSearchOpts {
    #[doc = "Sets GraphQL argument `dotall` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_dotall(mut self, value: bool) -> Self {
        self.dotall = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `filesOnly` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_files_only(mut self, value: bool) -> Self {
        self.files_only = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `globs` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_globs(mut self, value: Vec<impl Into<String>>) -> Self {
        self.globs = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `insensitive` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insensitive(mut self, value: bool) -> Self {
        self.insensitive = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `limit` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_limit(mut self, value: i64) -> Self {
        self.limit = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `literal` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_literal(mut self, value: bool) -> Self {
        self.literal = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `multiline` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_multiline(mut self, value: bool) -> Self {
        self.multiline = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `paths` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_paths(mut self, value: Vec<impl Into<String>>) -> Self {
        self.paths = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `skipHidden` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_skip_hidden(mut self, value: bool) -> Self {
        self.skip_hidden = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `skipIgnored` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_skip_ignored(mut self, value: bool) -> Self {
        self.skip_ignored = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.services`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceServicesOpts {
    #[doc = "Only include services matching the specified patterns\n\n`None` omits GraphQL Wire_Name `include`."]
    pub include: Option<Vec<String>>,
}
impl WorkspaceServicesOpts {
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withConfigEnv`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithConfigEnvOpts {
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
}
impl WorkspaceWithConfigEnvOpts {
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withConfigValue`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithConfigValueOpts {
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
    #[doc = "List value to set. Elements are stored verbatim, with no auto-detection. Mutually exclusive with value.\n\n`None` omits GraphQL Wire_Name `values`."]
    pub values: Option<Vec<String>>,
}
impl WorkspaceWithConfigValueOpts {
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `values` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_values(mut self, value: Vec<impl Into<String>>) -> Self {
        self.values = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withInitClient`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithInitClientOpts {
    #[doc = "SDK-specific init arguments.\n\n`None` omits GraphQL Wire_Name `args`."]
    pub args: Option<crate::Json>,
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
    #[doc = "Skip running the SDK's generators for the new client.\n\n`None` omits GraphQL Wire_Name `noGenerate` and preserves engine default `Boolean(false)`."]
    pub no_generate: Option<bool>,
}
impl WorkspaceWithInitClientOpts {
    #[doc = "Sets GraphQL argument `args` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_args(mut self, value: crate::Json) -> Self {
        self.args = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `noGenerate` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_generate(mut self, value: bool) -> Self {
        self.no_generate = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withInitModule`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithInitModuleOpts {
    #[doc = "SDK-specific init arguments.\n\n`None` omits GraphQL Wire_Name `args`."]
    pub args: Option<crate::Json>,
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
    #[doc = "Additional include patterns for the module.\n\n`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
    #[doc = "Skip running the SDK's generators for the new module.\n\n`None` omits GraphQL Wire_Name `noGenerate` and preserves engine default `Boolean(false)`."]
    pub no_generate: Option<bool>,
    #[doc = "Workspace-relative path for the new module.\n\n`None` omits GraphQL Wire_Name `path` and preserves engine default `String(\"\")`."]
    pub path: Option<String>,
    #[doc = "Source subpath within the new module.\n\n`None` omits GraphQL Wire_Name `source` and preserves engine default `String(\"\")`."]
    pub source: Option<String>,
}
impl WorkspaceWithInitModuleOpts {
    #[doc = "Sets GraphQL argument `args` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_args(mut self, value: crate::Json) -> Self {
        self.args = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
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
    #[doc = "Sets GraphQL argument `path` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_path(mut self, value: impl Into<String>) -> Self {
        self.path = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `source` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source(mut self, value: impl Into<String>) -> Self {
        self.source = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withModule`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithModuleOpts {
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
    #[doc = "Override name for the installed module entry.\n\n`None` omits GraphQL Wire_Name `name` and preserves engine default `String(\"\")`."]
    pub name: Option<String>,
}
impl WorkspaceWithModuleOpts {
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `name` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_name(mut self, value: impl Into<String>) -> Self {
        self.name = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withNewFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithNewFileOpts {
    #[doc = "Permissions of the new file.\n\n`None` omits GraphQL Wire_Name `permissions` and preserves engine default `Int(420)`."]
    pub permissions: Option<i64>,
}
impl WorkspaceWithNewFileOpts {
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withSDK`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithSdkOpts {
    #[doc = "User-facing SDK name to persist under `\\[modules.&lt;name&gt;.as-sdk\\] name = ...`.\n\n`None` omits GraphQL Wire_Name `asSdkName` and preserves engine default `String(\"\")`."]
    pub as_sdk_name: Option<String>,
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
    #[doc = "Override name for the installed SDK entry.\n\n`None` omits GraphQL Wire_Name `name` and preserves engine default `String(\"\")`."]
    pub name: Option<String>,
}
impl WorkspaceWithSdkOpts {
    #[doc = "Sets GraphQL argument `asSdkName` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_as_sdk_name(mut self, value: impl Into<String>) -> Self {
        self.as_sdk_name = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `name` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_name(mut self, value: impl Into<String>) -> Self {
        self.name = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withoutConfigEnv`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithoutConfigEnvOpts {
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
}
impl WorkspaceWithoutConfigEnvOpts {
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withoutConfigValue`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithoutConfigValueOpts {
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
}
impl WorkspaceWithoutConfigValueOpts {
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withoutModule`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithoutModuleOpts {
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
}
impl WorkspaceWithoutModuleOpts {
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Workspace.withoutSDK`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct WorkspaceWithoutSdkOpts {
    #[doc = "Write to the workspace config directory at the workspace cwd.\n\n`None` omits GraphQL Wire_Name `here` and preserves engine default `Boolean(false)`."]
    pub here: Option<bool>,
}
impl WorkspaceWithoutSdkOpts {
    #[doc = "Sets GraphQL argument `here` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_here(mut self, value: bool) -> Self {
        self.here = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for Workspace {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Workspace {
    fn graphql_type() -> &'static str {
        "Workspace"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Workspace> for crate::IdInput<Workspace> {
    fn from(value: Workspace) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Workspace> for crate::IdInput<super::NodeClient> {
    fn from(value: Workspace) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Workspace {
    #[doc = "Canonical Dagger address of the workspace location, or an opaque identity for synthetic workspaces.\n\nSelects GraphQL Wire_Name `address` on `Workspace`."]
    pub async fn address(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("address");
        query.execute(&self.session).await
    }
    #[doc = "Return this workspace's pending overlay changes.\n\nSelects GraphQL Wire_Name `changes` on `Workspace`."]
    #[must_use]
    pub fn changes(&self) -> super::Changeset {
        let query = self.selection.select("changes");
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return all checks from modules loaded in the workspace.\n\nSelects GraphQL Wire_Name `checks` on `Workspace`."]
    #[must_use]
    pub fn checks(&self) -> super::CheckGroup {
        let query = self.selection.select("checks");
        super::CheckGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `checks` with a borrowed, reusable `WorkspaceChecksOpts` value."]
    #[must_use]
    pub fn checks_opts(&self, opts: &WorkspaceChecksOpts) -> super::CheckGroup {
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
        let query = if let Some(value) = &opts.only_generate {
            query.arg("onlyGenerate", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.skip {
            query.arg("skip", value)
        } else {
            query
        };
        super::CheckGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Selected native workspace config file relative to the workspace cwd, if any.\n\nSelects GraphQL Wire_Name `configFile` on `Workspace`."]
    pub async fn config_file(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("configFile");
        query.execute(&self.session).await
    }
    #[doc = "Read a configuration value from dagger.toml.\n\nIf key is empty, returns the full config.\n\nIf key points to a scalar, returns the value.\n\nIf key points to a table, returns flattened dotted-key output.\n\nSelects GraphQL Wire_Name `configRead` on `Workspace`."]
    pub async fn config_read(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("configRead");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `configRead` with a borrowed, reusable `WorkspaceConfigReadOpts` value."]
    pub async fn config_read_opts(
        &self,
        opts: &WorkspaceConfigReadOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("configRead");
        let query = if let Some(value) = &opts.key {
            query.arg("key", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Current location within the workspace root.\n\nThe workspace root is returned as \"/\".\n\nRelative paths in workspace APIs resolve from here.\n\nSelects GraphQL Wire_Name `cwd` on `Workspace`."]
    pub async fn cwd(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("cwd");
        query.execute(&self.session).await
    }
    #[doc = "Returns a Directory from the workspace.\n\nRelative paths resolve from the workspace cwd. Absolute paths resolve from the workspace root.\n\nSelects GraphQL Wire_Name `directory` on `Workspace`."]
    #[must_use]
    pub fn directory(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("directory");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `directory` with a borrowed, reusable `WorkspaceDirectoryOpts` value."]
    #[must_use]
    pub fn directory_opts(
        &self,
        path: impl Into<String>,
        opts: &WorkspaceDirectoryOpts,
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
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "List named environments defined in the workspace configuration.\n\nSelects GraphQL Wire_Name `envList` on `Workspace`."]
    pub async fn env_list(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("envList");
        query.execute(&self.session).await
    }
    #[doc = "Write this workspace's pending changes to its local Git workspace.\n\nSelects GraphQL Wire_Name `export` on `Workspace`."]
    pub async fn export(&self) -> Result<(), crate::QueryError> {
        let query = self.selection.select("export");
        query.execute(&self.session).await
    }
    #[doc = "Returns a File from the workspace.\n\nRelative paths resolve from the workspace cwd. Absolute paths resolve from the workspace root.\n\nSelects GraphQL Wire_Name `file` on `Workspace`."]
    #[must_use]
    pub fn file(&self, path: impl Into<String>) -> super::File {
        let query = self.selection.select("file");
        let query = query.arg("path", path.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Search for a file or directory by walking up from the start path within the workspace.\n\nReturns the absolute workspace path if found, or null if not found.\n\nRelative start paths resolve from the workspace cwd.\n\nThe search stops at the workspace root and will not traverse above it.\n\nSelects GraphQL Wire_Name `findUp` on `Workspace`."]
    pub async fn find_up(
        &self,
        name: impl Into<String>,
    ) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("findUp");
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `findUp` with a borrowed, reusable `WorkspaceFindUpOpts` value."]
    pub async fn find_up_opts(
        &self,
        name: impl Into<String>,
        opts: &WorkspaceFindUpOpts,
    ) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("findUp");
        let query = if let Some(value) = &opts.from {
            query.arg("from", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Return all generators from modules loaded in the workspace.\n\nSelects GraphQL Wire_Name `generators` on `Workspace`."]
    #[must_use]
    pub fn generators(&self) -> super::GeneratorGroup {
        let query = self.selection.select("generators");
        super::GeneratorGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `generators` with a borrowed, reusable `WorkspaceGeneratorsOpts` value."]
    #[must_use]
    pub fn generators_opts(&self, opts: &WorkspaceGeneratorsOpts) -> super::GeneratorGroup {
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
    #[doc = "Git state for this workspace. Errors if the workspace is not in a git repository.\n\nSelects GraphQL Wire_Name `git` on `Workspace`."]
    #[must_use]
    pub fn git(&self) -> super::WorkspaceGit {
        let query = self.selection.select("git");
        super::WorkspaceGit {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a list of files and directories that match the given pattern.\n\nPatterns match paths relative to the workspace root.\n\nSelects GraphQL Wire_Name `glob` on `Workspace`."]
    pub async fn glob(&self, pattern: impl Into<String>) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("glob");
        let query = query.arg("pattern", pattern.into());
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Workspace.\n\nSelects GraphQL Wire_Name `id` on `Workspace`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Plan the explicit migration needed for the current workspace.\n\nThe returned plan has an empty changeset and no steps when no migration is needed.\n\nSelects GraphQL Wire_Name `migrate` on `Workspace`."]
    #[must_use]
    pub fn migrate(&self) -> super::WorkspaceMigration {
        let query = self.selection.select("migrate");
        super::WorkspaceMigration {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a module defined in the workspace configuration.\n\nSelects GraphQL Wire_Name `module` on `Workspace`."]
    #[must_use]
    pub fn module(&self, name: impl Into<String>) -> super::WorkspaceModule {
        let query = self.selection.select("module");
        let query = query.arg("name", name.into());
        super::WorkspaceModule {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load a module source from a path within the workspace.\n\nRelative paths (e.g., \"foo\") resolve from the workspace cwd; absolute paths (e.g., \"/foo\") resolve from the workspace root.\n\nFails if the path does not point to an initialized module.\n\nSelects GraphQL Wire_Name `moduleSource` on `Workspace`."]
    #[must_use]
    pub fn module_source(&self, path: impl Into<String>) -> super::ModuleSource {
        let query = self.selection.select("moduleSource");
        let query = query.arg("path", path.into());
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "List modules defined in the workspace configuration.\n\nSelects GraphQL Wire_Name `modules` on `Workspace`."]
    pub async fn modules(&self) -> Result<Vec<super::WorkspaceModule>, crate::QueryError> {
        let query = self.selection.select("modules");
        let query = query.select("id");
        query
            .execute_reentry::<super::WorkspaceModule, Vec<crate::Id>>(
                &self.session,
                "WorkspaceModule",
            )
            .await
    }
    #[doc = "An installed SDK, by name.\n\nSelects GraphQL Wire_Name `sdk` on `Workspace`."]
    #[must_use]
    pub fn sdk(&self, name: impl Into<String>) -> super::WorkspaceSdk {
        let query = self.selection.select("sdk");
        let query = query.arg("name", name.into());
        super::WorkspaceSdk {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Installed SDKs.\n\nSelects GraphQL Wire_Name `sdks` on `Workspace`."]
    pub async fn sdks(&self) -> Result<Vec<super::WorkspaceSdk>, crate::QueryError> {
        let query = self.selection.select("sdks");
        let query = query.select("id");
        query
            .execute_reentry::<super::WorkspaceSdk, Vec<crate::Id>>(&self.session, "WorkspaceSDK")
            .await
    }
    #[doc = "Searches for content matching the given regular expression or literal string.\n\nUses Rust regex syntax; escape literal ., \\[, \\], {, }, | with backslashes.\n\nRuns ripgrep on the client host, falling back to grep if unavailable.\n\nSelects GraphQL Wire_Name `search` on `Workspace`."]
    pub async fn search(
        &self,
        pattern: impl Into<String>,
    ) -> Result<Vec<super::SearchResult>, crate::QueryError> {
        let query = self.selection.select("search");
        let query = query.arg("pattern", pattern.into());
        let query = query.select("id");
        query
            .execute_reentry::<super::SearchResult, Vec<crate::Id>>(&self.session, "SearchResult")
            .await
    }
    #[doc = "Executes GraphQL operation `search` with a borrowed, reusable `WorkspaceSearchOpts` value."]
    pub async fn search_opts(
        &self,
        pattern: impl Into<String>,
        opts: &WorkspaceSearchOpts,
    ) -> Result<Vec<super::SearchResult>, crate::QueryError> {
        let query = self.selection.select("search");
        let query = if let Some(value) = &opts.dotall {
            query.arg("dotall", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.files_only {
            query.arg("filesOnly", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.globs {
            query.arg("globs", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insensitive {
            query.arg("insensitive", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.limit {
            query.arg("limit", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.literal {
            query.arg("literal", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.multiline {
            query.arg("multiline", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.paths {
            query.arg("paths", value)
        } else {
            query
        };
        let query = query.arg("pattern", pattern.into());
        let query = if let Some(value) = &opts.skip_hidden {
            query.arg("skipHidden", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.skip_ignored {
            query.arg("skipIgnored", value)
        } else {
            query
        };
        let query = query.select("id");
        query
            .execute_reentry::<super::SearchResult, Vec<crate::Id>>(&self.session, "SearchResult")
            .await
    }
    #[doc = "Return all services from modules loaded in the workspace.\n\nSelects GraphQL Wire_Name `services` on `Workspace`."]
    #[must_use]
    pub fn services(&self) -> super::UpGroup {
        let query = self.selection.select("services");
        super::UpGroup {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `services` with a borrowed, reusable `WorkspaceServicesOpts` value."]
    #[must_use]
    pub fn services_opts(&self, opts: &WorkspaceServicesOpts) -> super::UpGroup {
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
    #[doc = "Return this workspace with a changeset applied, without mutating the source.\n\nSelects GraphQL Wire_Name `withChanges` on `Workspace`."]
    #[must_use]
    pub fn with_changes(
        &self,
        changes: impl Into<crate::IdInput<super::Changeset>>,
    ) -> super::Workspace {
        let query = self.selection.select("withChanges");
        let query = query.arg_id_input("changes", changes.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a named config environment created.\n\nSelects GraphQL Wire_Name `withConfigEnv` on `Workspace`."]
    #[must_use]
    pub fn with_config_env(&self, name: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withConfigEnv");
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withConfigEnv` with a borrowed, reusable `WorkspaceWithConfigEnvOpts` value."]
    #[must_use]
    pub fn with_config_env_opts(
        &self,
        name: impl Into<String>,
        opts: &WorkspaceWithConfigEnvOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withConfigEnv");
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a configuration value written.\n\nSelects GraphQL Wire_Name `withConfigValue` on `Workspace`."]
    #[must_use]
    pub fn with_config_value(
        &self,
        key: impl Into<String>,
        value: impl Into<String>,
    ) -> super::Workspace {
        let query = self.selection.select("withConfigValue");
        let query = query.arg("key", key.into());
        let query = query.arg("value", value.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withConfigValue` with a borrowed, reusable `WorkspaceWithConfigValueOpts` value."]
    #[must_use]
    pub fn with_config_value_opts(
        &self,
        key: impl Into<String>,
        value: impl Into<String>,
        opts: &WorkspaceWithConfigValueOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withConfigValue");
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = query.arg("key", key.into());
        let query = query.arg("value", value.into());
        let query = if let Some(value) = &opts.values {
            query.arg("values", value)
        } else {
            query
        };
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a generated API client initialized.\n\nThe SDK's generators run for the new client, so the returned workspace carries its generated bindings.\n\nSelects GraphQL Wire_Name `withInitClient` on `Workspace`."]
    #[must_use]
    pub fn with_init_client(
        &self,
        module: impl Into<String>,
        path: impl Into<String>,
        sdk: impl Into<String>,
    ) -> super::Workspace {
        let query = self.selection.select("withInitClient");
        let query = query.arg("module", module.into());
        let query = query.arg("path", path.into());
        let query = query.arg("sdk", sdk.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withInitClient` with a borrowed, reusable `WorkspaceWithInitClientOpts` value."]
    #[must_use]
    pub fn with_init_client_opts(
        &self,
        module: impl Into<String>,
        path: impl Into<String>,
        sdk: impl Into<String>,
        opts: &WorkspaceWithInitClientOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withInitClient");
        let query = if let Some(value) = &opts.args {
            query.arg("args", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = query.arg("module", module.into());
        let query = if let Some(value) = &opts.no_generate {
            query.arg("noGenerate", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = query.arg("sdk", sdk.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a new module initialized.\n\nThe SDK's generators run for the new module, so the returned workspace carries the generated code it needs to be loadable.\n\nSelects GraphQL Wire_Name `withInitModule` on `Workspace`."]
    #[must_use]
    pub fn with_init_module(
        &self,
        name: impl Into<String>,
        sdk: impl Into<String>,
    ) -> super::Workspace {
        let query = self.selection.select("withInitModule");
        let query = query.arg("name", name.into());
        let query = query.arg("sdk", sdk.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withInitModule` with a borrowed, reusable `WorkspaceWithInitModuleOpts` value."]
    #[must_use]
    pub fn with_init_module_opts(
        &self,
        name: impl Into<String>,
        sdk: impl Into<String>,
        opts: &WorkspaceWithInitModuleOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withInitModule");
        let query = if let Some(value) = &opts.args {
            query.arg("args", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.include {
            query.arg("include", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.no_generate {
            query.arg("noGenerate", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.path {
            query.arg("path", value)
        } else {
            query
        };
        let query = query.arg("sdk", sdk.into());
        let query = if let Some(value) = &opts.source {
            query.arg("source", value)
        } else {
            query
        };
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a module installed in its config.\n\nSelects GraphQL Wire_Name `withModule` on `Workspace`."]
    #[must_use]
    pub fn with_module(&self, r#ref: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withModule");
        let query = query.arg("ref", r#ref.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withModule` with a borrowed, reusable `WorkspaceWithModuleOpts` value."]
    #[must_use]
    pub fn with_module_opts(
        &self,
        r#ref: impl Into<String>,
        opts: &WorkspaceWithModuleOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withModule");
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.name {
            query.arg("name", value)
        } else {
            query
        };
        let query = query.arg("ref", r#ref.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a directory added, without mutating the source.\n\nSelects GraphQL Wire_Name `withNewDirectory` on `Workspace`."]
    #[must_use]
    pub fn with_new_directory(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Directory>>,
    ) -> super::Workspace {
        let query = self.selection.select("withNewDirectory");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a new or replaced file, without mutating the source.\n\nSelects GraphQL Wire_Name `withNewFile` on `Workspace`."]
    #[must_use]
    pub fn with_new_file(
        &self,
        contents: impl Into<String>,
        path: impl Into<String>,
    ) -> super::Workspace {
        let query = self.selection.select("withNewFile");
        let query = query.arg("contents", contents.into());
        let query = query.arg("path", path.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withNewFile` with a borrowed, reusable `WorkspaceWithNewFileOpts` value."]
    #[must_use]
    pub fn with_new_file_opts(
        &self,
        contents: impl Into<String>,
        path: impl Into<String>,
        opts: &WorkspaceWithNewFileOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withNewFile");
        let query = query.arg("contents", contents.into());
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with an SDK installed in its config.\n\nSelects GraphQL Wire_Name `withSDK` on `Workspace`."]
    #[must_use]
    pub fn with_sdk(&self, r#ref: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withSDK");
        let query = query.arg("ref", r#ref.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withSDK` with a borrowed, reusable `WorkspaceWithSdkOpts` value."]
    #[must_use]
    pub fn with_sdk_opts(
        &self,
        r#ref: impl Into<String>,
        opts: &WorkspaceWithSdkOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withSDK");
        let query = if let Some(value) = &opts.as_sdk_name {
            query.arg("asSdkName", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.name {
            query.arg("name", value)
        } else {
            query
        };
        let query = query.arg("ref", r#ref.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with refreshed lockfile state.\n\nSelects GraphQL Wire_Name `withUpdatedLock` on `Workspace`."]
    #[must_use]
    pub fn with_updated_lock(&self) -> super::Workspace {
        let query = self.selection.select("withUpdatedLock");
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with its working directory pointed at the given workspace-relative path.\n\nSelects GraphQL Wire_Name `withWorkdir` on `Workspace`."]
    #[must_use]
    pub fn with_workdir(&self, path: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withWorkdir");
        let query = query.arg("path", path.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a named config environment removed.\n\nSelects GraphQL Wire_Name `withoutConfigEnv` on `Workspace`."]
    #[must_use]
    pub fn without_config_env(&self, name: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withoutConfigEnv");
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutConfigEnv` with a borrowed, reusable `WorkspaceWithoutConfigEnvOpts` value."]
    #[must_use]
    pub fn without_config_env_opts(
        &self,
        name: impl Into<String>,
        opts: &WorkspaceWithoutConfigEnvOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withoutConfigEnv");
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a configuration value removed.\n\nErrors when the key is not currently set.\n\nSelects GraphQL Wire_Name `withoutConfigValue` on `Workspace`."]
    #[must_use]
    pub fn without_config_value(&self, key: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withoutConfigValue");
        let query = query.arg("key", key.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutConfigValue` with a borrowed, reusable `WorkspaceWithoutConfigValueOpts` value."]
    #[must_use]
    pub fn without_config_value_opts(
        &self,
        key: impl Into<String>,
        opts: &WorkspaceWithoutConfigValueOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withoutConfigValue");
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = query.arg("key", key.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a directory removed, without mutating the source.\n\nSelects GraphQL Wire_Name `withoutDirectory` on `Workspace`."]
    #[must_use]
    pub fn without_directory(&self, path: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withoutDirectory");
        let query = query.arg("path", path.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a file removed, without mutating the source.\n\nSelects GraphQL Wire_Name `withoutFile` on `Workspace`."]
    #[must_use]
    pub fn without_file(&self, path: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withoutFile");
        let query = query.arg("path", path.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with a module removed from its config.\n\nSelects GraphQL Wire_Name `withoutModule` on `Workspace`."]
    #[must_use]
    pub fn without_module(&self, name: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withoutModule");
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutModule` with a borrowed, reusable `WorkspaceWithoutModuleOpts` value."]
    #[must_use]
    pub fn without_module_opts(
        &self,
        name: impl Into<String>,
        opts: &WorkspaceWithoutModuleOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withoutModule");
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return this workspace with an SDK removed from its config.\n\nSelects GraphQL Wire_Name `withoutSDK` on `Workspace`."]
    #[must_use]
    pub fn without_sdk(&self, name: impl Into<String>) -> super::Workspace {
        let query = self.selection.select("withoutSDK");
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutSDK` with a borrowed, reusable `WorkspaceWithoutSdkOpts` value."]
    #[must_use]
    pub fn without_sdk_opts(
        &self,
        name: impl Into<String>,
        opts: &WorkspaceWithoutSdkOpts,
    ) -> super::Workspace {
        let query = self.selection.select("withoutSDK");
        let query = if let Some(value) = &opts.here {
            query.arg("here", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Workspace {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
