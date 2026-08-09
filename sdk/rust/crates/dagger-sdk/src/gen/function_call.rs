//! Generated bindings owned by the GraphQL `FunctionCall` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An active function call."]
#[derive(Clone)]
pub struct FunctionCall {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for FunctionCall {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for FunctionCall {
    fn graphql_type() -> &'static str {
        "FunctionCall"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<FunctionCall> for crate::IdInput<FunctionCall> {
    fn from(value: FunctionCall) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<FunctionCall> for crate::IdInput<super::NodeClient> {
    fn from(value: FunctionCall) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl FunctionCall {
    #[doc = "A unique identifier for this FunctionCall.\n\nSelects GraphQL Wire_Name `id` on `FunctionCall`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The argument values the function is being invoked with.\n\nSelects GraphQL Wire_Name `inputArgs` on `FunctionCall`."]
    pub async fn input_args(&self) -> Result<Vec<super::FunctionCallArgValue>, crate::QueryError> {
        let query = self.selection.select("inputArgs");
        let query = query.select("id");
        query
            .execute_reentry::<super::FunctionCallArgValue, Vec<crate::Id>>(
                &self.session,
                "FunctionCallArgValue",
            )
            .await
    }
    #[doc = "The name of the function being called.\n\nSelects GraphQL Wire_Name `name` on `FunctionCall`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object.\n\nSelects GraphQL Wire_Name `parent` on `FunctionCall`."]
    pub async fn parent(&self) -> Result<crate::Json, crate::QueryError> {
        let query = self.selection.select("parent");
        query.execute(&self.session).await
    }
    #[doc = "The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module.\n\nSelects GraphQL Wire_Name `parentName` on `FunctionCall`."]
    pub async fn parent_name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("parentName");
        query.execute(&self.session).await
    }
    #[doc = "Return an error from the function.\n\nSelects GraphQL Wire_Name `returnError` on `FunctionCall`."]
    pub async fn return_error(
        &self,
        error: impl Into<crate::IdInput<super::Error>>,
    ) -> Result<(), crate::QueryError> {
        let query = self.selection.select("returnError");
        let query = query.arg_id_input("error", error.into());
        query.execute(&self.session).await
    }
    #[doc = "Set the return value of the function call to the provided value.\n\nSelects GraphQL Wire_Name `returnValue` on `FunctionCall`."]
    pub async fn return_value(&self, value: crate::Json) -> Result<(), crate::QueryError> {
        let query = self.selection.select("returnValue");
        let query = query.arg("value", value);
        query.execute(&self.session).await
    }
}
impl super::Node for FunctionCall {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
