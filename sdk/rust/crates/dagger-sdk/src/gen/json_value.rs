//! Generated bindings owned by the GraphQL `JSONValue` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `JSONValue`."]
#[derive(Clone)]
pub struct JsonValue {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `JSONValue.contents`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct JsonValueContentsOpts {
    #[doc = "Optional line prefix\n\n`None` omits GraphQL Wire_Name `indent` and preserves engine default `String(\"  \")`."]
    pub indent: Option<String>,
    #[doc = "Pretty-print\n\n`None` omits GraphQL Wire_Name `pretty` and preserves engine default `Boolean(false)`."]
    pub pretty: Option<bool>,
}
impl JsonValueContentsOpts {
    #[doc = "Sets GraphQL argument `indent` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_indent(mut self, value: impl Into<String>) -> Self {
        self.indent = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `pretty` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_pretty(mut self, value: bool) -> Self {
        self.pretty = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for JsonValue {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for JsonValue {
    fn graphql_type() -> &'static str {
        "JSONValue"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<JsonValue> for crate::IdInput<JsonValue> {
    fn from(value: JsonValue) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<JsonValue> for crate::IdInput<super::NodeClient> {
    fn from(value: JsonValue) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl JsonValue {
    #[doc = "Decode an array from json\n\nSelects GraphQL Wire_Name `asArray` on `JSONValue`."]
    pub async fn as_array(&self) -> Result<Vec<super::JsonValue>, crate::QueryError> {
        let query = self.selection.select("asArray");
        let query = query.select("id");
        query
            .execute_reentry::<super::JsonValue, Vec<crate::Id>>(&self.session, "JSONValue")
            .await
    }
    #[doc = "Decode a boolean from json\n\nSelects GraphQL Wire_Name `asBoolean` on `JSONValue`."]
    pub async fn as_boolean(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("asBoolean");
        query.execute(&self.session).await
    }
    #[doc = "Decode an integer from json\n\nSelects GraphQL Wire_Name `asInteger` on `JSONValue`."]
    pub async fn as_integer(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("asInteger");
        query.execute(&self.session).await
    }
    #[doc = "Decode a string from json\n\nSelects GraphQL Wire_Name `asString` on `JSONValue`."]
    pub async fn as_string(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("asString");
        query.execute(&self.session).await
    }
    #[doc = "Return the value encoded as json\n\nSelects GraphQL Wire_Name `contents` on `JSONValue`."]
    pub async fn contents(&self) -> Result<crate::Json, crate::QueryError> {
        let query = self.selection.select("contents");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `contents` with a borrowed, reusable `JsonValueContentsOpts` value."]
    pub async fn contents_opts(
        &self,
        opts: &JsonValueContentsOpts,
    ) -> Result<crate::Json, crate::QueryError> {
        let query = self.selection.select("contents");
        let query = if let Some(value) = &opts.indent {
            query.arg("indent", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.pretty {
            query.arg("pretty", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Lookup the field at the given path, and return its value.\n\nSelects GraphQL Wire_Name `field` on `JSONValue`."]
    #[must_use]
    pub fn field(&self, path: Vec<impl Into<String>>) -> super::JsonValue {
        let query = self.selection.select("field");
        let path = path.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("path", path);
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "List fields of the encoded object\n\nSelects GraphQL Wire_Name `fields` on `JSONValue`."]
    pub async fn fields(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("fields");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this JSONValue.\n\nSelects GraphQL Wire_Name `id` on `JSONValue`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "Encode a boolean to json\n\nSelects GraphQL Wire_Name `newBoolean` on `JSONValue`."]
    #[must_use]
    pub fn new_boolean(&self, value: bool) -> super::JsonValue {
        let query = self.selection.select("newBoolean");
        let query = query.arg("value", value);
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Encode an integer to json\n\nSelects GraphQL Wire_Name `newInteger` on `JSONValue`."]
    #[must_use]
    pub fn new_integer(&self, value: i64) -> super::JsonValue {
        let query = self.selection.select("newInteger");
        let query = query.arg("value", value);
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Encode a string to json\n\nSelects GraphQL Wire_Name `newString` on `JSONValue`."]
    #[must_use]
    pub fn new_string(&self, value: impl Into<String>) -> super::JsonValue {
        let query = self.selection.select("newString");
        let query = query.arg("value", value.into());
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a new json value, decoded from the given content\n\nSelects GraphQL Wire_Name `withContents` on `JSONValue`."]
    #[must_use]
    pub fn with_contents(&self, contents: crate::Json) -> super::JsonValue {
        let query = self.selection.select("withContents");
        let query = query.arg("contents", contents);
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Set a new field at the given path\n\nSelects GraphQL Wire_Name `withField` on `JSONValue`."]
    #[must_use]
    pub fn with_field(
        &self,
        path: Vec<impl Into<String>>,
        value: impl Into<crate::IdInput<super::JsonValue>>,
    ) -> super::JsonValue {
        let query = self.selection.select("withField");
        let path = path.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("path", path);
        let query = query.arg_id_input("value", value.into());
        super::JsonValue {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for JsonValue {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
