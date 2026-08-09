//! Generated bindings owned by the GraphQL `Function` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Function represents a resolver provided by a Module.\n\nA function always evaluates against a parent object and is given a set of named arguments."]
#[derive(Clone)]
pub struct Function {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Function.withArg`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FunctionWithArgOpts {
    #[doc = "`None` omits GraphQL Wire_Name `defaultAddress` and preserves engine default `String(\"\")`."]
    pub default_address: Option<String>,
    #[doc = "If the argument is a Directory or File type, default to load path from context directory, relative to root directory.\n\n`None` omits GraphQL Wire_Name `defaultPath` and preserves engine default `String(\"\")`."]
    pub default_path: Option<String>,
    #[doc = "A default value to use for this argument if not explicitly set by the caller, if any\n\n`None` omits GraphQL Wire_Name `defaultValue`."]
    pub default_value: Option<crate::Json>,
    #[doc = "If deprecated, the reason or migration path.\n\n`None` omits GraphQL Wire_Name `deprecated`."]
    pub deprecated: Option<String>,
    #[doc = "A doc string for the argument, if any\n\n`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
    #[doc = "Patterns to ignore when loading the contextual argument value.\n\n`None` omits GraphQL Wire_Name `ignore` and preserves engine default `List(\\[\\])`."]
    pub ignore: Option<Vec<String>>,
    #[doc = "The source map for the argument definition.\n\n`None` omits GraphQL Wire_Name `sourceMap`."]
    pub source_map: Option<crate::IdInput<super::SourceMap>>,
}
impl FunctionWithArgOpts {
    #[doc = "Sets GraphQL argument `defaultAddress` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_default_address(mut self, value: impl Into<String>) -> Self {
        self.default_address = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `defaultPath` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_default_path(mut self, value: impl Into<String>) -> Self {
        self.default_path = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `defaultValue` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_default_value(mut self, value: crate::Json) -> Self {
        self.default_value = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `deprecated` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_deprecated(mut self, value: impl Into<String>) -> Self {
        self.deprecated = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `ignore` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ignore(mut self, value: Vec<impl Into<String>>) -> Self {
        self.ignore = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `sourceMap` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_map(mut self, value: crate::IdInput<super::SourceMap>) -> Self {
        self.source_map = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Function.withCachePolicy`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FunctionWithCachePolicyOpts {
    #[doc = "The TTL for the cache policy, if applicable. Provided as a duration string, e.g. \"5m\", \"1h30s\".\n\n`None` omits GraphQL Wire_Name `timeToLive`."]
    pub time_to_live: Option<String>,
}
impl FunctionWithCachePolicyOpts {
    #[doc = "Sets GraphQL argument `timeToLive` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_time_to_live(mut self, value: impl Into<String>) -> Self {
        self.time_to_live = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Function.withDeprecated`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct FunctionWithDeprecatedOpts {
    #[doc = "Reason or migration path describing the deprecation.\n\n`None` omits GraphQL Wire_Name `reason`."]
    pub reason: Option<String>,
}
impl FunctionWithDeprecatedOpts {
    #[doc = "Sets GraphQL argument `reason` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_reason(mut self, value: impl Into<String>) -> Self {
        self.reason = Some(value.into());
        self
    }
}
impl crate::IntoID<crate::Id> for Function {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Function {
    fn graphql_type() -> &'static str {
        "Function"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Function> for crate::IdInput<Function> {
    fn from(value: Function) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Function> for crate::IdInput<super::NodeClient> {
    fn from(value: Function) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Function {
    #[doc = "Arguments accepted by the function, if any.\n\nSelects GraphQL Wire_Name `args` on `Function`."]
    pub async fn args(&self) -> Result<Vec<super::FunctionArg>, crate::QueryError> {
        let query = self.selection.select("args");
        let query = query.select("id");
        query
            .execute_reentry::<super::FunctionArg, Vec<crate::Id>>(&self.session, "FunctionArg")
            .await
    }
    #[doc = "The reason this function is deprecated, if any.\n\nSelects GraphQL Wire_Name `deprecated` on `Function`."]
    pub async fn deprecated(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("deprecated");
        query.execute(&self.session).await
    }
    #[doc = "A doc string for the function, if any.\n\nSelects GraphQL Wire_Name `description` on `Function`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this Function.\n\nSelects GraphQL Wire_Name `id` on `Function`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The name of the function.\n\nSelects GraphQL Wire_Name `name` on `Function`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The type returned by the function.\n\nSelects GraphQL Wire_Name `returnType` on `Function`."]
    #[must_use]
    pub fn return_type(&self) -> super::TypeDef {
        let query = self.selection.select("returnType");
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The location of this function declaration.\n\nSelects GraphQL Wire_Name `sourceMap` on `Function`."]
    pub async fn source_map(&self) -> Result<Option<super::SourceMap>, crate::QueryError> {
        let query = self.selection.select("sourceMap");
        let query = query.select("id");
        query
            .execute_reentry::<super::SourceMap, Option<crate::Id>>(&self.session, "SourceMap")
            .await
    }
    #[doc = "If this function is provided by a module, the name of the module. Unset otherwise.\n\nSelects GraphQL Wire_Name `sourceModuleName` on `Function`."]
    pub async fn source_module_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(&self.session).await
    }
    #[doc = "Returns the function with the provided argument\n\nSelects GraphQL Wire_Name `withArg` on `Function`."]
    #[must_use]
    pub fn with_arg(
        &self,
        name: impl Into<String>,
        type_def: impl Into<crate::IdInput<super::TypeDef>>,
    ) -> super::Function {
        let query = self.selection.select("withArg");
        let query = query.arg("name", name.into());
        let query = query.arg_id_input("typeDef", type_def.into());
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withArg` with a borrowed, reusable `FunctionWithArgOpts` value."]
    #[must_use]
    pub fn with_arg_opts(
        &self,
        name: impl Into<String>,
        type_def: impl Into<crate::IdInput<super::TypeDef>>,
        opts: &FunctionWithArgOpts,
    ) -> super::Function {
        let query = self.selection.select("withArg");
        let query = if let Some(value) = &opts.default_address {
            query.arg("defaultAddress", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.default_path {
            query.arg("defaultPath", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.default_value {
            query.arg("defaultValue", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.deprecated {
            query.arg("deprecated", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.ignore {
            query.arg("ignore", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.source_map {
            query.arg_id_input("sourceMap", value.clone())
        } else {
            query
        };
        let query = query.arg_id_input("typeDef", type_def.into());
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns the function updated to use the provided cache policy.\n\nSelects GraphQL Wire_Name `withCachePolicy` on `Function`."]
    #[must_use]
    pub fn with_cache_policy(&self, policy: super::FunctionCachePolicy) -> super::Function {
        let query = self.selection.select("withCachePolicy");
        let query = query.arg("policy", policy);
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withCachePolicy` with a borrowed, reusable `FunctionWithCachePolicyOpts` value."]
    #[must_use]
    pub fn with_cache_policy_opts(
        &self,
        policy: super::FunctionCachePolicy,
        opts: &FunctionWithCachePolicyOpts,
    ) -> super::Function {
        let query = self.selection.select("withCachePolicy");
        let query = query.arg("policy", policy);
        let query = if let Some(value) = &opts.time_to_live {
            query.arg("timeToLive", value)
        } else {
            query
        };
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns the function with a flag indicating it's a check.\n\nSelects GraphQL Wire_Name `withCheck` on `Function`."]
    #[must_use]
    pub fn with_check(&self) -> super::Function {
        let query = self.selection.select("withCheck");
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns the function with the provided deprecation reason.\n\nSelects GraphQL Wire_Name `withDeprecated` on `Function`."]
    #[must_use]
    pub fn with_deprecated(&self) -> super::Function {
        let query = self.selection.select("withDeprecated");
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withDeprecated` with a borrowed, reusable `FunctionWithDeprecatedOpts` value."]
    #[must_use]
    pub fn with_deprecated_opts(&self, opts: &FunctionWithDeprecatedOpts) -> super::Function {
        let query = self.selection.select("withDeprecated");
        let query = if let Some(value) = &opts.reason {
            query.arg("reason", value)
        } else {
            query
        };
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns the function with the given doc string.\n\nSelects GraphQL Wire_Name `withDescription` on `Function`."]
    #[must_use]
    pub fn with_description(&self, description: impl Into<String>) -> super::Function {
        let query = self.selection.select("withDescription");
        let query = query.arg("description", description.into());
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns the function with a flag indicating it's a generator.\n\nSelects GraphQL Wire_Name `withGenerator` on `Function`."]
    #[must_use]
    pub fn with_generator(&self) -> super::Function {
        let query = self.selection.select("withGenerator");
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns the function with the given source map.\n\nSelects GraphQL Wire_Name `withSourceMap` on `Function`."]
    #[must_use]
    pub fn with_source_map(
        &self,
        source_map: impl Into<crate::IdInput<super::SourceMap>>,
    ) -> super::Function {
        let query = self.selection.select("withSourceMap");
        let query = query.arg_id_input("sourceMap", source_map.into());
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns the function with a flag indicating it returns a service for dagger up.\n\nSelects GraphQL Wire_Name `withUp` on `Function`."]
    #[must_use]
    pub fn with_up(&self) -> super::Function {
        let query = self.selection.select("withUp");
        super::Function {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Function {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
