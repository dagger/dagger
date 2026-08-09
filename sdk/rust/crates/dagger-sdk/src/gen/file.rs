//! Generated bindings owned by the GraphQL `File` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A file."]
#[derive(Clone)]
pub struct File {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `File.asEnvFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FileAsEnvFileOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" with the value of other vars\n\n`None` omits GraphQL Wire_Name `expand`.\n\n**Deprecated:** Variable expansion is now enabled by default"]
    pub expand: Option<bool>,
}
impl FileAsEnvFileOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `File.contents`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FileContentsOpts {
    #[doc = "Maximum number of lines to read\n\n`None` omits GraphQL Wire_Name `limitLines`."]
    pub limit_lines: Option<i64>,
    #[doc = "Start reading after this line\n\n`None` omits GraphQL Wire_Name `offsetLines`."]
    pub offset_lines: Option<i64>,
}
impl FileContentsOpts {
    #[doc = "Sets GraphQL argument `limitLines` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_limit_lines(mut self, value: i64) -> Self {
        self.limit_lines = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `offsetLines` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_offset_lines(mut self, value: i64) -> Self {
        self.offset_lines = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `File.digest`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FileDigestOpts {
    #[doc = "If true, exclude metadata from the digest.\n\n`None` omits GraphQL Wire_Name `excludeMetadata` and preserves engine default `Boolean(false)`."]
    pub exclude_metadata: Option<bool>,
}
impl FileDigestOpts {
    #[doc = "Sets GraphQL argument `excludeMetadata` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_exclude_metadata(mut self, value: bool) -> Self {
        self.exclude_metadata = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `File.export`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FileExportOpts {
    #[doc = "If allowParentDirPath is true, the path argument can be a directory path, in which case the file will be created in that directory.\n\n`None` omits GraphQL Wire_Name `allowParentDirPath` and preserves engine default `Boolean(false)`."]
    pub allow_parent_dir_path: Option<bool>,
}
impl FileExportOpts {
    #[doc = "Sets GraphQL argument `allowParentDirPath` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_allow_parent_dir_path(mut self, value: bool) -> Self {
        self.allow_parent_dir_path = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `File.search`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FileSearchOpts {
    #[doc = "Allow the . pattern to match newlines in multiline mode.\n\n`None` omits GraphQL Wire_Name `dotall` and preserves engine default `Boolean(false)`."]
    pub dotall: Option<bool>,
    #[doc = "Only return matching files, not lines and content\n\n`None` omits GraphQL Wire_Name `filesOnly` and preserves engine default `Boolean(false)`."]
    pub files_only: Option<bool>,
    #[doc = "`None` omits GraphQL Wire_Name `globs` and preserves engine default `List(\\[\\])`."]
    pub globs: Option<Vec<String>>,
    #[doc = "Enable case-insensitive matching.\n\n`None` omits GraphQL Wire_Name `insensitive` and preserves engine default `Boolean(false)`."]
    pub insensitive: Option<bool>,
    #[doc = "Limit the number of results to return\n\n`None` omits GraphQL Wire_Name `limit`."]
    pub limit: Option<i64>,
    #[doc = "Interpret the pattern as a literal string instead of a regular expression.\n\n`None` omits GraphQL Wire_Name `literal` and preserves engine default `Boolean(false)`."]
    pub literal: Option<bool>,
    #[doc = "Enable searching across multiple lines.\n\n`None` omits GraphQL Wire_Name `multiline` and preserves engine default `Boolean(false)`."]
    pub multiline: Option<bool>,
    #[doc = "`None` omits GraphQL Wire_Name `paths` and preserves engine default `List(\\[\\])`."]
    pub paths: Option<Vec<String>>,
    #[doc = "Skip hidden files (files starting with .).\n\n`None` omits GraphQL Wire_Name `skipHidden` and preserves engine default `Boolean(false)`."]
    pub skip_hidden: Option<bool>,
    #[doc = "Honor .gitignore, .ignore, and .rgignore files.\n\n`None` omits GraphQL Wire_Name `skipIgnored` and preserves engine default `Boolean(false)`."]
    pub skip_ignored: Option<bool>,
}
impl FileSearchOpts {
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
#[doc = "Owned optional arguments for GraphQL operation `File.withReplaced`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FileWithReplacedOpts {
    #[doc = "Replace all occurrences of the pattern.\n\n`None` omits GraphQL Wire_Name `all` and preserves engine default `Boolean(false)`."]
    pub all: Option<bool>,
    #[doc = "Replace the first match starting from the specified line.\n\n`None` omits GraphQL Wire_Name `firstFrom`."]
    pub first_from: Option<i64>,
}
impl FileWithReplacedOpts {
    #[doc = "Sets GraphQL argument `all` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_all(mut self, value: bool) -> Self {
        self.all = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `firstFrom` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_first_from(mut self, value: i64) -> Self {
        self.first_from = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for File {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for File {
    fn graphql_type() -> &'static str {
        "File"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<File> for crate::IdInput<File> {
    fn from(value: File) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<File> for crate::IdInput<super::ExportableClient> {
    fn from(value: File) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<File> for crate::IdInput<super::NodeClient> {
    fn from(value: File) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<File> for crate::IdInput<super::SyncerClient> {
    fn from(value: File) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl File {
    #[doc = "Parse as an env file\n\nSelects GraphQL Wire_Name `asEnvFile` on `File`."]
    #[must_use]
    pub fn as_env_file(&self) -> super::EnvFile {
        let query = self.selection.select("asEnvFile");
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asEnvFile` with a borrowed, reusable `FileAsEnvFileOpts` value."]
    #[must_use]
    pub fn as_env_file_opts(&self, opts: &FileAsEnvFileOpts) -> super::EnvFile {
        let query = self.selection.select("asEnvFile");
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
    #[doc = "Parse the file contents as JSON.\n\nSelects GraphQL Wire_Name `asJSON` on `File`."]
    #[must_use]
    pub fn as_json(&self) -> super::JsonValue {
        let query = self.selection.select("asJSON");
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Change the owner of the file recursively.\n\nSelects GraphQL Wire_Name `chown` on `File`."]
    #[must_use]
    pub fn chown(&self, owner: impl Into<String>) -> super::File {
        let query = self.selection.select("chown");
        let query = query.arg("owner", owner.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves the contents of the file.\n\nSelects GraphQL Wire_Name `contents` on `File`."]
    pub async fn contents(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("contents");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `contents` with a borrowed, reusable `FileContentsOpts` value."]
    pub async fn contents_opts(
        &self,
        opts: &FileContentsOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("contents");
        let query = if let Some(value) = &opts.limit_lines {
            query.arg("limitLines", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.offset_lines {
            query.arg("offsetLines", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Return the file's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.\n\nSelects GraphQL Wire_Name `digest` on `File`."]
    pub async fn digest(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("digest");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `digest` with a borrowed, reusable `FileDigestOpts` value."]
    pub async fn digest_opts(&self, opts: &FileDigestOpts) -> Result<String, crate::QueryError> {
        let query = self.selection.select("digest");
        let query = if let Some(value) = &opts.exclude_metadata {
            query.arg("excludeMetadata", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Writes the file to a file path on the host.\n\nSelects GraphQL Wire_Name `export` on `File`."]
    pub async fn export(&self, path: impl Into<String>) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `export` with a borrowed, reusable `FileExportOpts` value."]
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: &FileExportOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = if let Some(value) = &opts.allow_parent_dir_path {
            query.arg("allowParentDirPath", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this File.\n\nSelects GraphQL Wire_Name `id` on `File`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Retrieves the name of the file.\n\nSelects GraphQL Wire_Name `name` on `File`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "Searches for content matching the given regular expression or literal string.\n\nUses Rust regex syntax; escape literal ., \\[, \\], {, }, | with backslashes.\n\nSelects GraphQL Wire_Name `search` on `File`."]
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
    #[doc = "Executes GraphQL operation `search` with a borrowed, reusable `FileSearchOpts` value."]
    pub async fn search_opts(
        &self,
        pattern: impl Into<String>,
        opts: &FileSearchOpts,
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
    #[doc = "Retrieves the size of the file, in bytes.\n\nSelects GraphQL Wire_Name `size` on `File`."]
    pub async fn size(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("size");
        query.execute(&self.session).await
    }
    #[doc = "Return file status\n\nSelects GraphQL Wire_Name `stat` on `File`."]
    pub async fn stat(&self) -> Result<Option<super::Stat>, crate::QueryError> {
        let query = self.selection.select("stat");
        let query = query.select("id");
        query
            .execute_reentry::<super::Stat, Option<crate::Id>>(&self.session, "Stat")
            .await
    }
    #[doc = "Force evaluation in the engine.\n\nSelects GraphQL Wire_Name `sync` on `File`."]
    pub async fn sync(&self) -> Result<super::File, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::File>(
            &self.session,
            id,
            "File",
        ))
    }
    #[doc = "Retrieves this file with its name set to the given name.\n\nSelects GraphQL Wire_Name `withName` on `File`."]
    #[must_use]
    pub fn with_name(&self, name: impl Into<String>) -> super::File {
        let query = self.selection.select("withName");
        let query = query.arg("name", name.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves the file with content replaced with the given text.\n\nIf 'all' is true, all occurrences of the pattern will be replaced.\n\nIf 'firstAfter' is specified, only the first match starting at the specified line will be replaced.\n\nIf neither are specified, and there are multiple matches for the pattern, this will error.\n\nIf there are no matches for the pattern, this will error.\n\nSelects GraphQL Wire_Name `withReplaced` on `File`."]
    #[must_use]
    pub fn with_replaced(
        &self,
        replacement: impl Into<String>,
        search: impl Into<String>,
    ) -> super::File {
        let query = self.selection.select("withReplaced");
        let query = query.arg("replacement", replacement.into());
        let query = query.arg("search", search.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withReplaced` with a borrowed, reusable `FileWithReplacedOpts` value."]
    #[must_use]
    pub fn with_replaced_opts(
        &self,
        replacement: impl Into<String>,
        search: impl Into<String>,
        opts: &FileWithReplacedOpts,
    ) -> super::File {
        let query = self.selection.select("withReplaced");
        let query = if let Some(value) = &opts.all {
            query.arg("all", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.first_from {
            query.arg("firstFrom", value)
        } else {
            query
        };
        let query = query.arg("replacement", replacement.into());
        let query = query.arg("search", search.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this file with its created/modified timestamps set to the given time.\n\nSelects GraphQL Wire_Name `withTimestamps` on `File`."]
    #[must_use]
    pub fn with_timestamps(&self, timestamp: i64) -> super::File {
        let query = self.selection.select("withTimestamps");
        let query = query.arg("timestamp", timestamp);
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Exportable for File {
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
impl super::Node for File {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for File {
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
