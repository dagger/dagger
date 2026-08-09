//! Generated bindings owned by the GraphQL `Directory` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A directory."]
#[derive(Clone)]
pub struct Directory {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.asModule`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryAsModuleOpts {
    #[doc = "An optional subpath of the directory which contains the module's configuration file.\n\nIf not set, the module source code is loaded from the root of the directory.\n\n`None` omits GraphQL Wire_Name `sourceRootPath` and preserves engine default `String(\".\")`."]
    pub source_root_path: Option<String>,
}
impl DirectoryAsModuleOpts {
    #[doc = "Sets GraphQL argument `sourceRootPath` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_root_path(mut self, value: impl Into<String>) -> Self {
        self.source_root_path = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.asModuleSource`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryAsModuleSourceOpts {
    #[doc = "An optional subpath of the directory which contains the module's configuration file.\n\nIf not set, the module source code is loaded from the root of the directory.\n\n`None` omits GraphQL Wire_Name `sourceRootPath` and preserves engine default `String(\".\")`."]
    pub source_root_path: Option<String>,
}
impl DirectoryAsModuleSourceOpts {
    #[doc = "Sets GraphQL argument `sourceRootPath` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_root_path(mut self, value: impl Into<String>) -> Self {
        self.source_root_path = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.asWorkspace`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryAsWorkspaceOpts {
    #[doc = "Current working directory inside the workspace root. Defaults to the workspace root.\n\n`None` omits GraphQL Wire_Name `cwd` and preserves engine default `String(\"/\")`."]
    pub cwd: Option<String>,
}
impl DirectoryAsWorkspaceOpts {
    #[doc = "Sets GraphQL argument `cwd` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cwd(mut self, value: impl Into<String>) -> Self {
        self.cwd = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.dockerBuild`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryDockerBuildOpts {
    #[doc = "Build arguments to use in the build.\n\n`None` omits GraphQL Wire_Name `buildArgs` and preserves engine default `List(\\[\\])`."]
    pub build_args: Option<Vec<super::BuildArg>>,
    #[doc = "Path to the Dockerfile to use (e.g., \"frontend.Dockerfile\").\n\n`None` omits GraphQL Wire_Name `dockerfile` and preserves engine default `String(\"Dockerfile\")`."]
    pub dockerfile: Option<String>,
    #[doc = "If set, skip the automatic init process injected into containers created by RUN statements.\n\nThis should only be used if the user requires that their exec processes be the pid 1 process in the container. Otherwise it may result in unexpected behavior.\n\n`None` omits GraphQL Wire_Name `noInit` and preserves engine default `Boolean(false)`."]
    pub no_init: Option<bool>,
    #[doc = "The platform to build.\n\n`None` omits GraphQL Wire_Name `platform`."]
    pub platform: Option<crate::Platform>,
    #[doc = "Secrets to pass to the build.\n\nThey will be mounted at /run/secrets/\\[secret-name\\].\n\n`None` omits GraphQL Wire_Name `secrets` and preserves engine default `List(\\[\\])`."]
    pub secrets: Option<Vec<crate::IdInput<super::Secret>>>,
    #[doc = "A socket to use for SSH authentication during the build\n\n(e.g., for Dockerfile RUN --mount=type=ssh instructions).\n\nTypically obtained via host.unixSocket() pointing to the SSH_AUTH_SOCK.\n\n`None` omits GraphQL Wire_Name `ssh`."]
    pub ssh: Option<crate::IdInput<super::Socket>>,
    #[doc = "Target build stage to build.\n\n`None` omits GraphQL Wire_Name `target` and preserves engine default `String(\"\")`."]
    pub target: Option<String>,
}
impl DirectoryDockerBuildOpts {
    #[doc = "Sets GraphQL argument `buildArgs` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_build_args(mut self, value: Vec<super::BuildArg>) -> Self {
        self.build_args = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `dockerfile` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_dockerfile(mut self, value: impl Into<String>) -> Self {
        self.dockerfile = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `noInit` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_init(mut self, value: bool) -> Self {
        self.no_init = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `platform` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_platform(mut self, value: crate::Platform) -> Self {
        self.platform = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `secrets` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_secrets(mut self, value: Vec<crate::IdInput<super::Secret>>) -> Self {
        self.secrets = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `ssh` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ssh(mut self, value: crate::IdInput<super::Socket>) -> Self {
        self.ssh = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `target` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_target(mut self, value: impl Into<String>) -> Self {
        self.target = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.entries`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryEntriesOpts {
    #[doc = "Location of the directory to look at (e.g., \"/src\").\n\n`None` omits GraphQL Wire_Name `path`."]
    pub path: Option<String>,
}
impl DirectoryEntriesOpts {
    #[doc = "Sets GraphQL argument `path` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_path(mut self, value: impl Into<String>) -> Self {
        self.path = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.exists`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryExistsOpts {
    #[doc = "If specified, do not follow symlinks.\n\n`None` omits GraphQL Wire_Name `doNotFollowSymlinks` and preserves engine default `Boolean(false)`."]
    pub do_not_follow_symlinks: Option<bool>,
    #[doc = "If specified, also validate the type of file (e.g. \"REGULAR_TYPE\", \"DIRECTORY_TYPE\", or \"SYMLINK_TYPE\").\n\n`None` omits GraphQL Wire_Name `expectedType`."]
    pub expected_type: Option<super::ExistsType>,
}
impl DirectoryExistsOpts {
    #[doc = "Sets GraphQL argument `doNotFollowSymlinks` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_do_not_follow_symlinks(mut self, value: bool) -> Self {
        self.do_not_follow_symlinks = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `expectedType` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expected_type(mut self, value: super::ExistsType) -> Self {
        self.expected_type = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.export`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryExportOpts {
    #[doc = "If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.\n\n`None` omits GraphQL Wire_Name `wipe` and preserves engine default `Boolean(false)`."]
    pub wipe: Option<bool>,
}
impl DirectoryExportOpts {
    #[doc = "Sets GraphQL argument `wipe` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_wipe(mut self, value: bool) -> Self {
        self.wipe = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.filter`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryFilterOpts {
    #[doc = "If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: \\[\"node_modules/\", \".git*\", \".env\"\\]\n\n`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "If set, apply .gitignore rules when filtering the directory.\n\n`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., \\[\"app/\", \"package.*\"\\]).\n\n`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
}
impl DirectoryFilterOpts {
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
#[doc = "Owned optional arguments for GraphQL operation `Directory.search`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectorySearchOpts {
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
impl DirectorySearchOpts {
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
#[doc = "Owned optional arguments for GraphQL operation `Directory.stat`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryStatOpts {
    #[doc = "If specified, do not follow symlinks.\n\n`None` omits GraphQL Wire_Name `doNotFollowSymlinks` and preserves engine default `Boolean(false)`."]
    pub do_not_follow_symlinks: Option<bool>,
}
impl DirectoryStatOpts {
    #[doc = "Sets GraphQL argument `doNotFollowSymlinks` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_do_not_follow_symlinks(mut self, value: bool) -> Self {
        self.do_not_follow_symlinks = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.terminal`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryTerminalOpts {
    #[doc = "If set, override the container's default terminal command and invoke these command arguments instead.\n\n`None` omits GraphQL Wire_Name `cmd` and preserves engine default `List(\\[\\])`."]
    pub cmd: Option<Vec<String>>,
    #[doc = "If set, override the default container used for the terminal.\n\n`None` omits GraphQL Wire_Name `container`."]
    pub container: Option<crate::IdInput<super::Container>>,
    #[doc = "Provides Dagger access to the executed command.\n\n`None` omits GraphQL Wire_Name `experimentalPrivilegedNesting` and preserves engine default `Boolean(false)`."]
    pub experimental_privileged_nesting: Option<bool>,
    #[doc = "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.\n\n`None` omits GraphQL Wire_Name `insecureRootCapabilities` and preserves engine default `Boolean(false)`."]
    pub insecure_root_capabilities: Option<bool>,
}
impl DirectoryTerminalOpts {
    #[doc = "Sets GraphQL argument `cmd` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cmd(mut self, value: Vec<impl Into<String>>) -> Self {
        self.cmd = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `container` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_container(mut self, value: crate::IdInput<super::Container>) -> Self {
        self.container = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `experimentalPrivilegedNesting` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_privileged_nesting(mut self, value: bool) -> Self {
        self.experimental_privileged_nesting = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureRootCapabilities` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_root_capabilities(mut self, value: bool) -> Self {
        self.insecure_root_capabilities = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.withDirectory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryWithDirectoryOpts {
    #[doc = "Exclude artifacts that match the given pattern (e.g., \\[\"node_modules/\", \".git*\"\\]).\n\n`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "Apply .gitignore filter rules inside the directory\n\n`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "Include only artifacts that match the given pattern (e.g., \\[\"app/\", \"package.*\"\\]).\n\n`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
    #[doc = "A user:group to set for the copied directory and its contents.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Permission given to the copied directory and contents (e.g., 0755).\n\n`None` omits GraphQL Wire_Name `permissions`."]
    pub permissions: Option<i64>,
}
impl DirectoryWithDirectoryOpts {
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
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.withFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryWithFileOpts {
    #[doc = "A user:group to set for the copied directory and its contents.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Permission given to the copied file (e.g., 0600).\n\n`None` omits GraphQL Wire_Name `permissions`."]
    pub permissions: Option<i64>,
}
impl DirectoryWithFileOpts {
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.withFiles`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryWithFilesOpts {
    #[doc = "Permission given to the copied files (e.g., 0600).\n\n`None` omits GraphQL Wire_Name `permissions`."]
    pub permissions: Option<i64>,
}
impl DirectoryWithFilesOpts {
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.withNewDirectory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryWithNewDirectoryOpts {
    #[doc = "Permission granted to the created directory (e.g., 0777).\n\n`None` omits GraphQL Wire_Name `permissions` and preserves engine default `Int(420)`."]
    pub permissions: Option<i64>,
}
impl DirectoryWithNewDirectoryOpts {
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.withNewFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryWithNewFileOpts {
    #[doc = "Permissions of the new file. Example: 0600\n\n`None` omits GraphQL Wire_Name `permissions` and preserves engine default `Int(420)`."]
    pub permissions: Option<i64>,
}
impl DirectoryWithNewFileOpts {
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.withPatch`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryWithPatchOpts {
    #[doc = "How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.\n\n`None` omits GraphQL Wire_Name `onConflict` and preserves engine default `Enum(SchemaName(\"FAIL\"))`."]
    pub on_conflict: Option<super::PatchConflict>,
}
impl DirectoryWithPatchOpts {
    #[doc = "Sets GraphQL argument `onConflict` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_on_conflict(mut self, value: super::PatchConflict) -> Self {
        self.on_conflict = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Directory.withPatchFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct DirectoryWithPatchFileOpts {
    #[doc = "How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.\n\n`None` omits GraphQL Wire_Name `onConflict` and preserves engine default `Enum(SchemaName(\"FAIL\"))`."]
    pub on_conflict: Option<super::PatchConflict>,
}
impl DirectoryWithPatchFileOpts {
    #[doc = "Sets GraphQL argument `onConflict` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_on_conflict(mut self, value: super::PatchConflict) -> Self {
        self.on_conflict = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for Directory {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Directory {
    fn graphql_type() -> &'static str {
        "Directory"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Directory> for crate::IdInput<Directory> {
    fn from(value: Directory) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Directory> for crate::IdInput<super::ExportableClient> {
    fn from(value: Directory) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Directory> for crate::IdInput<super::NodeClient> {
    fn from(value: Directory) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Directory> for crate::IdInput<super::SyncerClient> {
    fn from(value: Directory) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Directory {
    #[doc = "Converts this directory to a local git repository\n\nSelects GraphQL Wire_Name `asGit` on `Directory`."]
    #[must_use]
    pub fn as_git(&self) -> super::GitRepository {
        let query = self.selection.select("asGit");
        super::GitRepository {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load the directory as a Dagger module source\n\nSelects GraphQL Wire_Name `asModule` on `Directory`."]
    #[must_use]
    pub fn as_module(&self) -> super::Module {
        let query = self.selection.select("asModule");
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asModule` with a borrowed, reusable `DirectoryAsModuleOpts` value."]
    #[must_use]
    pub fn as_module_opts(&self, opts: &DirectoryAsModuleOpts) -> super::Module {
        let query = self.selection.select("asModule");
        let query = if let Some(value) = &opts.source_root_path {
            query.arg("sourceRootPath", value)
        } else {
            query
        };
        super::Module {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Load the directory as a Dagger module source\n\nSelects GraphQL Wire_Name `asModuleSource` on `Directory`."]
    #[must_use]
    pub fn as_module_source(&self) -> super::ModuleSource {
        let query = self.selection.select("asModuleSource");
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asModuleSource` with a borrowed, reusable `DirectoryAsModuleSourceOpts` value."]
    #[must_use]
    pub fn as_module_source_opts(&self, opts: &DirectoryAsModuleSourceOpts) -> super::ModuleSource {
        let query = self.selection.select("asModuleSource");
        let query = if let Some(value) = &opts.source_root_path {
            query.arg("sourceRootPath", value)
        } else {
            query
        };
        super::ModuleSource {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Creates a synthetic workspace from this directory.\n\nSelects GraphQL Wire_Name `asWorkspace` on `Directory`."]
    #[must_use]
    pub fn as_workspace(&self) -> super::Workspace {
        let query = self.selection.select("asWorkspace");
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asWorkspace` with a borrowed, reusable `DirectoryAsWorkspaceOpts` value."]
    #[must_use]
    pub fn as_workspace_opts(&self, opts: &DirectoryAsWorkspaceOpts) -> super::Workspace {
        let query = self.selection.select("asWorkspace");
        let query = if let Some(value) = &opts.cwd {
            query.arg("cwd", value)
        } else {
            query
        };
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return the difference between this directory and another directory, typically an older snapshot.\n\nThe difference is encoded as a changeset, which also tracks removed files, and can be applied to other directories.\n\nSelects GraphQL Wire_Name `changes` on `Directory`."]
    #[must_use]
    pub fn changes(&self, from: impl Into<crate::IdInput<super::Directory>>) -> super::Changeset {
        let query = self.selection.select("changes");
        let query = query.arg_id_input("from", from.into());
        super::Changeset {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Change the owner of the directory contents recursively.\n\nSelects GraphQL Wire_Name `chown` on `Directory`."]
    #[must_use]
    pub fn chown(&self, owner: impl Into<String>, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("chown");
        let query = query.arg("owner", owner.into());
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return the difference between this directory and an another directory. The difference is encoded as a directory.\n\nSelects GraphQL Wire_Name `diff` on `Directory`."]
    #[must_use]
    pub fn diff(&self, other: impl Into<crate::IdInput<super::Directory>>) -> super::Directory {
        let query = self.selection.select("diff");
        let query = query.arg_id_input("other", other.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return the directory's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.\n\nSelects GraphQL Wire_Name `digest` on `Directory`."]
    pub async fn digest(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("digest");
        query.execute(&self.session).await
    }
    #[doc = "Retrieves a directory at the given path.\n\nSelects GraphQL Wire_Name `directory` on `Directory`."]
    #[must_use]
    pub fn directory(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("directory");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Use Dockerfile compatibility to build a container from this directory. Only use this function for Dockerfile compatibility. Otherwise use the native Container type directly, it is feature-complete and supports all Dockerfile features.\n\nSelects GraphQL Wire_Name `dockerBuild` on `Directory`."]
    #[must_use]
    pub fn docker_build(&self) -> super::Container {
        let query = self.selection.select("dockerBuild");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `dockerBuild` with a borrowed, reusable `DirectoryDockerBuildOpts` value."]
    #[must_use]
    pub fn docker_build_opts(&self, opts: &DirectoryDockerBuildOpts) -> super::Container {
        let query = self.selection.select("dockerBuild");
        let query = if let Some(value) = &opts.build_args {
            query.arg("buildArgs", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.dockerfile {
            query.arg("dockerfile", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.no_init {
            query.arg("noInit", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.platform {
            query.arg("platform", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.secrets {
            query.arg_id_input("secrets", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.ssh {
            query.arg_id_input("ssh", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.target {
            query.arg("target", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a list of files and directories at the given path.\n\nSelects GraphQL Wire_Name `entries` on `Directory`."]
    pub async fn entries(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("entries");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `entries` with a borrowed, reusable `DirectoryEntriesOpts` value."]
    pub async fn entries_opts(
        &self,
        opts: &DirectoryEntriesOpts,
    ) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("entries");
        let query = if let Some(value) = &opts.path {
            query.arg("path", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "check if a file or directory exists\n\nSelects GraphQL Wire_Name `exists` on `Directory`."]
    pub async fn exists(&self, path: impl Into<String>) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("exists");
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `exists` with a borrowed, reusable `DirectoryExistsOpts` value."]
    pub async fn exists_opts(
        &self,
        path: impl Into<String>,
        opts: &DirectoryExistsOpts,
    ) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("exists");
        let query = if let Some(value) = &opts.do_not_follow_symlinks {
            query.arg("doNotFollowSymlinks", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.expected_type {
            query.arg("expectedType", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "Writes the contents of the directory to a path on the host.\n\nSelects GraphQL Wire_Name `export` on `Directory`."]
    pub async fn export(&self, path: impl Into<String>) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `export` with a borrowed, reusable `DirectoryExportOpts` value."]
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: &DirectoryExportOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.wipe {
            query.arg("wipe", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Retrieve a file at the given path.\n\nSelects GraphQL Wire_Name `file` on `Directory`."]
    #[must_use]
    pub fn file(&self, path: impl Into<String>) -> super::File {
        let query = self.selection.select("file");
        let query = query.arg("path", path.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with some paths included or excluded\n\nSelects GraphQL Wire_Name `filter` on `Directory`."]
    #[must_use]
    pub fn filter(&self) -> super::Directory {
        let query = self.selection.select("filter");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `filter` with a borrowed, reusable `DirectoryFilterOpts` value."]
    #[must_use]
    pub fn filter_opts(&self, opts: &DirectoryFilterOpts) -> super::Directory {
        let query = self.selection.select("filter");
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
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Search up the directory tree for a file or directory, and return its path. If no match, return null\n\nSelects GraphQL Wire_Name `findUp` on `Directory`."]
    pub async fn find_up(
        &self,
        name: impl Into<String>,
        start: impl Into<String>,
    ) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("findUp");
        let query = query.arg("name", name.into());
        let query = query.arg("start", start.into());
        query.execute(&self.session).await
    }
    #[doc = "Returns a list of files and directories that matche the given pattern.\n\nSelects GraphQL Wire_Name `glob` on `Directory`."]
    pub async fn glob(&self, pattern: impl Into<String>) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("glob");
        let query = query.arg("pattern", pattern.into());
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Directory.\n\nSelects GraphQL Wire_Name `id` on `Directory`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Returns the name of the directory.\n\nSelects GraphQL Wire_Name `name` on `Directory`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "Searches for content matching the given regular expression or literal string.\n\nUses Rust regex syntax; escape literal ., \\[, \\], {, }, | with backslashes.\n\nSelects GraphQL Wire_Name `search` on `Directory`."]
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
    #[doc = "Executes GraphQL operation `search` with a borrowed, reusable `DirectorySearchOpts` value."]
    pub async fn search_opts(
        &self,
        pattern: impl Into<String>,
        opts: &DirectorySearchOpts,
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
    #[doc = "Return file status\n\nSelects GraphQL Wire_Name `stat` on `Directory`."]
    pub async fn stat(
        &self,
        path: impl Into<String>,
    ) -> Result<Option<super::Stat>, crate::QueryError> {
        let query = self.selection.select("stat");
        let query = query.arg("path", path.into());
        let query = query.select("id");
        query
            .execute_reentry::<super::Stat, Option<crate::Id>>(&self.session, "Stat")
            .await
    }
    #[doc = "Executes GraphQL operation `stat` with a borrowed, reusable `DirectoryStatOpts` value."]
    pub async fn stat_opts(
        &self,
        path: impl Into<String>,
        opts: &DirectoryStatOpts,
    ) -> Result<Option<super::Stat>, crate::QueryError> {
        let query = self.selection.select("stat");
        let query = if let Some(value) = &opts.do_not_follow_symlinks {
            query.arg("doNotFollowSymlinks", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = query.select("id");
        query
            .execute_reentry::<super::Stat, Option<crate::Id>>(&self.session, "Stat")
            .await
    }
    #[doc = "Force evaluation in the engine.\n\nSelects GraphQL Wire_Name `sync` on `Directory`."]
    pub async fn sync(&self) -> Result<super::Directory, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Directory>(
            &self.session,
            id,
            "Directory",
        ))
    }
    #[doc = "Opens an interactive terminal in new container with this directory mounted inside.\n\nSelects GraphQL Wire_Name `terminal` on `Directory`."]
    #[must_use]
    pub fn terminal(&self) -> super::Directory {
        let query = self.selection.select("terminal");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `terminal` with a borrowed, reusable `DirectoryTerminalOpts` value."]
    #[must_use]
    pub fn terminal_opts(&self, opts: &DirectoryTerminalOpts) -> super::Directory {
        let query = self.selection.select("terminal");
        let query = if let Some(value) = &opts.cmd {
            query.arg("cmd", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.container {
            query.arg_id_input("container", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.experimental_privileged_nesting {
            query.arg("experimentalPrivilegedNesting", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_root_capabilities {
            query.arg("insecureRootCapabilities", value)
        } else {
            query
        };
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a directory with changes from another directory applied to it.\n\nSelects GraphQL Wire_Name `withChanges` on `Directory`."]
    #[must_use]
    pub fn with_changes(
        &self,
        changes: impl Into<crate::IdInput<super::Changeset>>,
    ) -> super::Directory {
        let query = self.selection.select("withChanges");
        let query = query.arg_id_input("changes", changes.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with a directory added\n\nSelects GraphQL Wire_Name `withDirectory` on `Directory`."]
    #[must_use]
    pub fn with_directory(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Directory>>,
    ) -> super::Directory {
        let query = self.selection.select("withDirectory");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withDirectory` with a borrowed, reusable `DirectoryWithDirectoryOpts` value."]
    #[must_use]
    pub fn with_directory_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Directory>>,
        opts: &DirectoryWithDirectoryOpts,
    ) -> super::Directory {
        let query = self.selection.select("withDirectory");
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
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        let query = query.arg_id_input("source", source.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Raise an error.\n\nSelects GraphQL Wire_Name `withError` on `Directory`."]
    #[must_use]
    pub fn with_error(&self, err: impl Into<String>) -> super::Directory {
        let query = self.selection.select("withError");
        let query = query.arg("err", err.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this directory plus the contents of the given file copied to the given path.\n\nSelects GraphQL Wire_Name `withFile` on `Directory`."]
    #[must_use]
    pub fn with_file(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::File>>,
    ) -> super::Directory {
        let query = self.selection.select("withFile");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withFile` with a borrowed, reusable `DirectoryWithFileOpts` value."]
    #[must_use]
    pub fn with_file_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::File>>,
        opts: &DirectoryWithFileOpts,
    ) -> super::Directory {
        let query = self.selection.select("withFile");
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        let query = query.arg_id_input("source", source.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this directory plus the contents of the given files copied to the given path.\n\nSelects GraphQL Wire_Name `withFiles` on `Directory`."]
    #[must_use]
    pub fn with_files(
        &self,
        path: impl Into<String>,
        sources: Vec<crate::IdInput<super::File>>,
    ) -> super::Directory {
        let query = self.selection.select("withFiles");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("sources", sources);
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withFiles` with a borrowed, reusable `DirectoryWithFilesOpts` value."]
    #[must_use]
    pub fn with_files_opts(
        &self,
        path: impl Into<String>,
        sources: Vec<crate::IdInput<super::File>>,
        opts: &DirectoryWithFilesOpts,
    ) -> super::Directory {
        let query = self.selection.select("withFiles");
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        let query = query.arg_id_input("sources", sources);
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this directory plus a new directory created at the given path.\n\nSelects GraphQL Wire_Name `withNewDirectory` on `Directory`."]
    #[must_use]
    pub fn with_new_directory(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("withNewDirectory");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withNewDirectory` with a borrowed, reusable `DirectoryWithNewDirectoryOpts` value."]
    #[must_use]
    pub fn with_new_directory_opts(
        &self,
        path: impl Into<String>,
        opts: &DirectoryWithNewDirectoryOpts,
    ) -> super::Directory {
        let query = self.selection.select("withNewDirectory");
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with a new file added\n\nSelects GraphQL Wire_Name `withNewFile` on `Directory`."]
    #[must_use]
    pub fn with_new_file(
        &self,
        contents: impl Into<String>,
        path: impl Into<String>,
    ) -> super::Directory {
        let query = self.selection.select("withNewFile");
        let query = query.arg("contents", contents.into());
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withNewFile` with a borrowed, reusable `DirectoryWithNewFileOpts` value."]
    #[must_use]
    pub fn with_new_file_opts(
        &self,
        contents: impl Into<String>,
        path: impl Into<String>,
        opts: &DirectoryWithNewFileOpts,
    ) -> super::Directory {
        let query = self.selection.select("withNewFile");
        let query = query.arg("contents", contents.into());
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this directory with the given Git-compatible patch applied.\n\nSelects GraphQL Wire_Name `withPatch` on `Directory`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn with_patch(&self, patch: impl Into<String>) -> super::Directory {
        let query = self.selection.select("withPatch");
        let query = query.arg("patch", patch.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withPatch` with a borrowed, reusable `DirectoryWithPatchOpts` value.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn with_patch_opts(
        &self,
        patch: impl Into<String>,
        opts: &DirectoryWithPatchOpts,
    ) -> super::Directory {
        let query = self.selection.select("withPatch");
        let query = if let Some(value) = &opts.on_conflict {
            query.arg("onConflict", value)
        } else {
            query
        };
        let query = query.arg("patch", patch.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this directory with the given Git-compatible patch file applied.\n\nSelects GraphQL Wire_Name `withPatchFile` on `Directory`.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn with_patch_file(
        &self,
        patch: impl Into<crate::IdInput<super::File>>,
    ) -> super::Directory {
        let query = self.selection.select("withPatchFile");
        let query = query.arg_id_input("patch", patch.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withPatchFile` with a borrowed, reusable `DirectoryWithPatchFileOpts` value.\n\n**Experimental:** This API is highly experimental and may be removed or replaced entirely."]
    #[must_use]
    pub fn with_patch_file_opts(
        &self,
        patch: impl Into<crate::IdInput<super::File>>,
        opts: &DirectoryWithPatchFileOpts,
    ) -> super::Directory {
        let query = self.selection.select("withPatchFile");
        let query = if let Some(value) = &opts.on_conflict {
            query.arg("onConflict", value)
        } else {
            query
        };
        let query = query.arg_id_input("patch", patch.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with a symlink\n\nSelects GraphQL Wire_Name `withSymlink` on `Directory`."]
    #[must_use]
    pub fn with_symlink(
        &self,
        link_name: impl Into<String>,
        target: impl Into<String>,
    ) -> super::Directory {
        let query = self.selection.select("withSymlink");
        let query = query.arg("linkName", link_name.into());
        let query = query.arg("target", target.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this directory with all file/dir timestamps set to the given time.\n\nSelects GraphQL Wire_Name `withTimestamps` on `Directory`."]
    #[must_use]
    pub fn with_timestamps(&self, timestamp: i64) -> super::Directory {
        let query = self.selection.select("withTimestamps");
        let query = query.arg("timestamp", timestamp);
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with a subdirectory removed\n\nSelects GraphQL Wire_Name `withoutDirectory` on `Directory`."]
    #[must_use]
    pub fn without_directory(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("withoutDirectory");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with a file removed\n\nSelects GraphQL Wire_Name `withoutFile` on `Directory`."]
    #[must_use]
    pub fn without_file(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("withoutFile");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with files removed\n\nSelects GraphQL Wire_Name `withoutFiles` on `Directory`."]
    #[must_use]
    pub fn without_files(&self, paths: Vec<impl Into<String>>) -> super::Directory {
        let query = self.selection.select("withoutFiles");
        let paths = paths.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("paths", paths);
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Exportable for Directory {
    fn export(
        &self,
        path: impl Into<String> + Send,
    ) -> impl core::future::Future<Output = Result<String, crate::QueryError>> + Send {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Node for Directory {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for Directory {
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
