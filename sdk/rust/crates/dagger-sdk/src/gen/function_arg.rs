//! Generated bindings owned by the GraphQL `FunctionArg` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An argument accepted by a function.\n\nThis is a specification for an argument at function definition time, not an argument passed at function call time."]
#[derive(Clone)]
pub struct FunctionArg {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for FunctionArg {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for FunctionArg {
    fn graphql_type() -> &'static str {
        "FunctionArg"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<FunctionArg> for crate::IdInput<FunctionArg> {
    fn from(value: FunctionArg) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<FunctionArg> for crate::IdInput<super::NodeClient> {
    fn from(value: FunctionArg) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl FunctionArg {
    #[doc = "Only applies to arguments of type Container. If the argument is not set, load it from the given address (e.g. alpine:latest)\n\nSelects GraphQL Wire_Name `defaultAddress` on `FunctionArg`."]
    pub async fn default_address(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("defaultAddress");
        query.execute(&self.session).await
    }
    #[doc = "Only applies to arguments of type File or Directory. If the argument is not set, load it from the given path in the context directory\n\nSelects GraphQL Wire_Name `defaultPath` on `FunctionArg`."]
    pub async fn default_path(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("defaultPath");
        query.execute(&self.session).await
    }
    #[doc = "A default value to use for this argument when not explicitly set by the caller, if any.\n\nSelects GraphQL Wire_Name `defaultValue` on `FunctionArg`."]
    pub async fn default_value(&self) -> Result<crate::Json, crate::QueryError> {
        let query = self.selection.select("defaultValue");
        query.execute(&self.session).await
    }
    #[doc = "The reason this function is deprecated, if any.\n\nSelects GraphQL Wire_Name `deprecated` on `FunctionArg`."]
    pub async fn deprecated(&self) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("deprecated");
        query.execute(&self.session).await
    }
    #[doc = "A doc string for the argument, if any.\n\nSelects GraphQL Wire_Name `description` on `FunctionArg`."]
    pub async fn description(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("description");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this FunctionArg.\n\nSelects GraphQL Wire_Name `id` on `FunctionArg`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Only applies to arguments of type Directory. The ignore patterns are applied to the input directory, and matching entries are filtered out, in a cache-efficient manner.\n\nSelects GraphQL Wire_Name `ignore` on `FunctionArg`."]
    pub async fn ignore(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("ignore");
        query.execute(&self.session).await
    }
    #[doc = "The name of the argument in lowerCamelCase format.\n\nSelects GraphQL Wire_Name `name` on `FunctionArg`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The location of this arg declaration.\n\nSelects GraphQL Wire_Name `sourceMap` on `FunctionArg`."]
    pub async fn source_map(&self) -> Result<Option<super::SourceMap>, crate::QueryError> {
        let query = self.selection.select("sourceMap");
        let query = query.select("id");
        query
            .execute_reentry::<super::SourceMap, Option<crate::Id>>(&self.session, "SourceMap")
            .await
    }
    #[doc = "The type of the argument.\n\nSelects GraphQL Wire_Name `typeDef` on `FunctionArg`."]
    #[must_use]
    pub fn type_def(&self) -> super::TypeDef {
        let query = self.selection.select("typeDef");
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for FunctionArg {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
