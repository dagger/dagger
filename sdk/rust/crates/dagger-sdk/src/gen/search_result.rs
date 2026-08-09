//! Generated bindings owned by the GraphQL `SearchResult` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "Lazy handle for GraphQL object `SearchResult`."]
#[derive(Clone)]
pub struct SearchResult {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
impl crate::IntoID<crate::Id> for SearchResult {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for SearchResult {
    fn graphql_type() -> &'static str {
        "SearchResult"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<SearchResult> for crate::IdInput<SearchResult> {
    fn from(value: SearchResult) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<SearchResult> for crate::IdInput<super::NodeClient> {
    fn from(value: SearchResult) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl SearchResult {
    #[doc = "The byte offset of this line within the file.\n\nSelects GraphQL Wire_Name `absoluteOffset` on `SearchResult`."]
    pub async fn absolute_offset(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("absoluteOffset");
        query.execute(&self.session).await
    }
    #[doc = "The path to the file that matched.\n\nSelects GraphQL Wire_Name `filePath` on `SearchResult`."]
    pub async fn file_path(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("filePath");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this SearchResult.\n\nSelects GraphQL Wire_Name `id` on `SearchResult`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The first line that matched.\n\nSelects GraphQL Wire_Name `lineNumber` on `SearchResult`."]
    pub async fn line_number(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("lineNumber");
        query.execute(&self.session).await
    }
    #[doc = "The line content that matched.\n\nSelects GraphQL Wire_Name `matchedLines` on `SearchResult`."]
    pub async fn matched_lines(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("matchedLines");
        query.execute(&self.session).await
    }
    #[doc = "Sub-match positions and content within the matched lines.\n\nSelects GraphQL Wire_Name `submatches` on `SearchResult`."]
    pub async fn submatches(&self) -> Result<Vec<super::SearchSubmatch>, crate::QueryError> {
        let query = self.selection.select("submatches");
        let query = query.select("id");
        query
            .execute_reentry::<super::SearchSubmatch, Vec<crate::Id>>(
                &self.session,
                "SearchSubmatch",
            )
            .await
    }
}
impl super::Node for SearchResult {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
