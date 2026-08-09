//! Generated bindings owned by the GraphQL `EnvFile` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A collection of environment variables."]
#[derive(Clone)]
pub struct EnvFile {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `EnvFile.get`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct EnvFileGetOpts {
    #[doc = "Return the value exactly as written to the file. No quote removal or variable expansion\n\n`None` omits GraphQL Wire_Name `raw`."]
    pub raw: Option<bool>,
}
impl EnvFileGetOpts {
    #[doc = "Sets GraphQL argument `raw` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_raw(mut self, value: bool) -> Self {
        self.raw = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `EnvFile.variables`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct EnvFileVariablesOpts {
    #[doc = "Return values exactly as written to the file. No quote removal or variable expansion\n\n`None` omits GraphQL Wire_Name `raw`."]
    pub raw: Option<bool>,
}
impl EnvFileVariablesOpts {
    #[doc = "Sets GraphQL argument `raw` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_raw(mut self, value: bool) -> Self {
        self.raw = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for EnvFile {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for EnvFile {
    fn graphql_type() -> &'static str {
        "EnvFile"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<EnvFile> for crate::IdInput<EnvFile> {
    fn from(value: EnvFile) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<EnvFile> for crate::IdInput<super::NodeClient> {
    fn from(value: EnvFile) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl EnvFile {
    #[doc = "Return as a file\n\nSelects GraphQL Wire_Name `asFile` on `EnvFile`."]
    #[must_use]
    pub fn as_file(&self) -> super::File {
        let query = self.selection.select("asFile");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Check if a variable exists\n\nSelects GraphQL Wire_Name `exists` on `EnvFile`."]
    pub async fn exists(&self, name: impl Into<String>) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("exists");
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Lookup a variable (last occurrence wins) and return its value, or an empty string\n\nSelects GraphQL Wire_Name `get` on `EnvFile`."]
    pub async fn get(&self, name: impl Into<String>) -> Result<String, crate::QueryError> {
        let query = self.selection.select("get");
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `get` with a borrowed, reusable `EnvFileGetOpts` value."]
    pub async fn get_opts(
        &self,
        name: impl Into<String>,
        opts: &EnvFileGetOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("get");
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.raw {
            query.arg("raw", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this EnvFile.\n\nSelects GraphQL Wire_Name `id` on `EnvFile`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Filters variables by prefix and removes the pref from keys. Variables without the prefix are excluded. For example, with the prefix \"MY_APP_\" and variables: MY_APP_TOKEN=topsecret MY_APP_NAME=hello FOO=bar the resulting environment will contain: TOKEN=topsecret NAME=hello\n\nSelects GraphQL Wire_Name `namespace` on `EnvFile`."]
    #[must_use]
    pub fn namespace(&self, prefix: impl Into<String>) -> super::EnvFile {
        let query = self.selection.select("namespace");
        let query = query.arg("prefix", prefix.into());
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return all variables\n\nSelects GraphQL Wire_Name `variables` on `EnvFile`."]
    pub async fn variables(&self) -> Result<Vec<super::EnvVariable>, crate::QueryError> {
        let query = self.selection.select("variables");
        let query = query.select("id");
        query
            .execute_reentry::<super::EnvVariable, Vec<crate::Id>>(&self.session, "EnvVariable")
            .await
    }
    #[doc = "Executes GraphQL operation `variables` with a borrowed, reusable `EnvFileVariablesOpts` value."]
    pub async fn variables_opts(
        &self,
        opts: &EnvFileVariablesOpts,
    ) -> Result<Vec<super::EnvVariable>, crate::QueryError> {
        let query = self.selection.select("variables");
        let query = if let Some(value) = &opts.raw {
            query.arg("raw", value)
        } else {
            query
        };
        let query = query.select("id");
        query
            .execute_reentry::<super::EnvVariable, Vec<crate::Id>>(&self.session, "EnvVariable")
            .await
    }
    #[doc = "Add a variable\n\nSelects GraphQL Wire_Name `withVariable` on `EnvFile`."]
    #[must_use]
    pub fn with_variable(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
    ) -> super::EnvFile {
        let query = self.selection.select("withVariable");
        let query = query.arg("name", name.into());
        let query = query.arg("value", value.into());
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Remove all occurrences of the named variable\n\nSelects GraphQL Wire_Name `withoutVariable` on `EnvFile`."]
    #[must_use]
    pub fn without_variable(&self, name: impl Into<String>) -> super::EnvFile {
        let query = self.selection.select("withoutVariable");
        let query = query.arg("name", name.into());
        super::EnvFile {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for EnvFile {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
