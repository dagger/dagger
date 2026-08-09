//! Generated bindings owned by the GraphQL `LLM` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A conversation with a large language model (LLM): queue prompts, expose tools, and step the model until it completes its turn."]
#[derive(Clone)]
pub struct Llm {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `LLM.loop`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct LlmLoopOpts {
    #[doc = "Cap the number of steps. The loop fails if the cap is reached before the model ends its turn.\n\n`None` omits GraphQL Wire_Name `maxSteps`."]
    pub max_steps: Option<i64>,
    #[doc = "Cap the model's output tokens on each step. Defaults to the model's maximum.\n\n`None` omits GraphQL Wire_Name `maxTokens`."]
    pub max_tokens: Option<i64>,
}
impl LlmLoopOpts {
    #[doc = "Sets GraphQL argument `maxSteps` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_max_steps(mut self, value: i64) -> Self {
        self.max_steps = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `maxTokens` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_max_tokens(mut self, value: i64) -> Self {
        self.max_tokens = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `LLM.step`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct LlmStepOpts {
    #[doc = "Cap the model's output tokens for this step. Defaults to the model's maximum.\n\n`None` omits GraphQL Wire_Name `maxTokens`."]
    pub max_tokens: Option<i64>,
}
impl LlmStepOpts {
    #[doc = "Sets GraphQL argument `maxTokens` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_max_tokens(mut self, value: i64) -> Self {
        self.max_tokens = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `LLM.withModel`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct LlmWithModelOpts {
    #[doc = "The provider serving the model, e.g. \"openai\". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.\n\n`None` omits GraphQL Wire_Name `provider`."]
    pub provider: Option<String>,
}
impl LlmWithModelOpts {
    #[doc = "Sets GraphQL argument `provider` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_provider(mut self, value: impl Into<String>) -> Self {
        self.provider = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `LLM.withResponse`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct LlmWithResponseOpts {
    #[doc = "Cached input tokens read\n\n`None` omits GraphQL Wire_Name `cachedTokenReads` and preserves engine default `Int(0)`."]
    pub cached_token_reads: Option<i64>,
    #[doc = "Cached input tokens written\n\n`None` omits GraphQL Wire_Name `cachedTokenWrites` and preserves engine default `Int(0)`."]
    pub cached_token_writes: Option<i64>,
    #[doc = "Uncached input tokens sent\n\n`None` omits GraphQL Wire_Name `inputTokens` and preserves engine default `Int(0)`."]
    pub input_tokens: Option<i64>,
    #[doc = "Tokens received from the model, including text and tool calls\n\n`None` omits GraphQL Wire_Name `outputTokens` and preserves engine default `Int(0)`."]
    pub output_tokens: Option<i64>,
    #[doc = "Total tokens consumed by this response\n\n`None` omits GraphQL Wire_Name `totalTokens` and preserves engine default `Int(0)`."]
    pub total_tokens: Option<i64>,
}
impl LlmWithResponseOpts {
    #[doc = "Sets GraphQL argument `cachedTokenReads` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cached_token_reads(mut self, value: i64) -> Self {
        self.cached_token_reads = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `cachedTokenWrites` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cached_token_writes(mut self, value: i64) -> Self {
        self.cached_token_writes = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inputTokens` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_input_tokens(mut self, value: i64) -> Self {
        self.input_tokens = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `outputTokens` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_output_tokens(mut self, value: i64) -> Self {
        self.output_tokens = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `totalTokens` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_total_tokens(mut self, value: i64) -> Self {
        self.total_tokens = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `LLM.withTools`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct LlmWithToolsOpts {
    #[doc = "Method names to exclude from the toolset (e.g. constructors, entrypoints).\n\n`None` omits GraphQL Wire_Name `except` and preserves engine default `List(\\[\\])`."]
    pub except: Option<Vec<String>>,
}
impl LlmWithToolsOpts {
    #[doc = "Sets GraphQL argument `except` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_except(mut self, value: Vec<impl Into<String>>) -> Self {
        self.except = Some(value.into_iter().map(Into::into).collect());
        self
    }
}
impl crate::IntoID<crate::Id> for Llm {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Llm {
    fn graphql_type() -> &'static str {
        "LLM"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Llm> for crate::IdInput<Llm> {
    fn from(value: Llm) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Llm> for crate::IdInput<super::NodeClient> {
    fn from(value: Llm) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Llm> for crate::IdInput<super::SyncerClient> {
    fn from(value: Llm) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Llm {
    #[doc = "estimated number of tokens currently occupying the context window; unlike tokenUsage this is not cumulative over the session\n\nSelects GraphQL Wire_Name `contextTokens` on `LLM`."]
    pub async fn context_tokens(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("contextTokens");
        query.execute(&self.session).await
    }
    #[doc = "The model's total context window in tokens, or null if unknown (e.g. a local or uncatalogued model).\n\nSelects GraphQL Wire_Name `contextWindow` on `LLM`."]
    pub async fn context_window(&self) -> Result<Option<i64>, crate::QueryError> {
        let query = self.selection.select("contextWindow");
        query.execute(&self.session).await
    }
    #[doc = "Fork the conversation, so that otherwise-identical follow-ups evaluate independently instead of deduplicating to a single cached result.\n\nSelects GraphQL Wire_Name `fork` on `LLM`."]
    #[must_use]
    pub fn fork(&self, label: impl Into<String>) -> super::Llm {
        let query = self.selection.select("fork");
        let query = query.arg("label", label.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Report whether anything is queued to send to the model: an unsent prompt or unevaluated tool results. When true, another step will do work; when false, the turn is complete.\n\nSelects GraphQL Wire_Name `hasPending` on `LLM`."]
    pub async fn has_pending(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("hasPending");
        query.execute(&self.session).await
    }
    #[doc = "A unique identifier for this LLM.\n\nSelects GraphQL Wire_Name `id` on `LLM`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The text of the model's most recent reply.\n\nSelects GraphQL Wire_Name `lastReply` on `LLM`."]
    pub async fn last_reply(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("lastReply");
        query.execute(&self.session).await
    }
    #[doc = "Send the queued prompt and step the model against the available tools, until it ends its turn: a reply with no tool calls and nothing left queued.\n\nSelects GraphQL Wire_Name `loop` on `LLM`."]
    #[must_use]
    pub fn r#loop(&self) -> super::Llm {
        let query = self.selection.select("loop");
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `loop` with a borrowed, reusable `LlmLoopOpts` value."]
    #[must_use]
    pub fn loop_opts(&self, opts: &LlmLoopOpts) -> super::Llm {
        let query = self.selection.select("loop");
        let query = if let Some(value) = &opts.max_steps {
            query.arg("maxSteps", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.max_tokens {
            query.arg("maxTokens", value)
        } else {
            query
        };
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The full message history, as structured messages.\n\nSelects GraphQL Wire_Name `messages` on `LLM`."]
    pub async fn messages(&self) -> Result<Vec<super::LlmMessage>, crate::QueryError> {
        let query = self.selection.select("messages");
        let query = query.select("id");
        query
            .execute_reentry::<super::LlmMessage, Vec<crate::Id>>(&self.session, "LLMMessage")
            .await
    }
    #[doc = "The model the conversation is running against, after resolving any configured default.\n\nSelects GraphQL Wire_Name `model` on `LLM`."]
    pub async fn model(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("model");
        query.execute(&self.session).await
    }
    #[doc = "A portable, self-contained ID for the conversation that node() can resolve in any session. Unlike id, which may return an engine-local runtime handle valid only within the current session, this returns the recipe form suitable for persisting and later restoring the conversation.\n\nSelects GraphQL Wire_Name `portableID` on `LLM`."]
    pub async fn portable_id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("portableID");
        query.execute(&self.session).await
    }
    #[doc = "The provider serving the model, e.g. \"anthropic\", \"openai\", \"google\", or \"local\".\n\nSelects GraphQL Wire_Name `provider` on `LLM`."]
    pub async fn provider(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("provider");
        query.execute(&self.session).await
    }
    #[doc = "Re-emit telemetry spans for the full message history, so a loaded conversation displays in the TUI.\n\nSelects GraphQL Wire_Name `replay` on `LLM`."]
    pub async fn replay(&self) -> Result<super::Llm, crate::QueryError> {
        let query = self.selection.select("replay");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Llm>(
            &self.session,
            id,
            "LLM",
        ))
    }
    #[doc = "Advance the conversation by a single step: send the queued prompt or tool results to the model, evaluate any tool calls it makes, and queue their results. Use loop to step until the model ends its turn.\n\nSelects GraphQL Wire_Name `step` on `LLM`."]
    #[must_use]
    pub fn step(&self) -> super::Llm {
        let query = self.selection.select("step");
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `step` with a borrowed, reusable `LlmStepOpts` value."]
    #[must_use]
    pub fn step_opts(&self, opts: &LlmStepOpts) -> super::Llm {
        let query = self.selection.select("step");
        let query = if let Some(value) = &opts.max_tokens {
            query.arg("maxTokens", value)
        } else {
            query
        };
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Force evaluation of the conversation's pending operations (prompts, steps, loops) in the engine.\n\nSelects GraphQL Wire_Name `sync` on `LLM`."]
    pub async fn sync(&self) -> Result<super::Llm, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Llm>(
            &self.session,
            id,
            "LLM",
        ))
    }
    #[doc = "The cumulative token usage, summed across every API call in the conversation.\n\nSelects GraphQL Wire_Name `tokenUsage` on `LLM`."]
    #[must_use]
    pub fn token_usage(&self) -> super::LlmTokenUsage {
        let query = self.selection.select("tokenUsage");
        super::LlmTokenUsage {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Render documentation for the tools currently exposed to the model.\n\nSelects GraphQL Wire_Name `tools` on `LLM`."]
    pub async fn tools(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("tools");
        query.execute(&self.session).await
    }
    #[doc = "The message history rendered as a plain-text transcript, suitable for feeding back to an LLM (e.g. for summarization).\n\nSelects GraphQL Wire_Name `transcript` on `LLM`."]
    pub async fn transcript(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("transcript");
        query.execute(&self.session).await
    }
    #[doc = "Add an external MCP server to the LLM\n\nSelects GraphQL Wire_Name `withMCPServer` on `LLM`."]
    #[must_use]
    pub fn with_mcp_server(
        &self,
        name: impl Into<String>,
        service: impl Into<crate::IdInput<super::Service>>,
    ) -> super::Llm {
        let query = self.selection.select("withMCPServer");
        let query = query.arg("name", name.into());
        let query = query.arg_id_input("service", service.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Change the model for the rest of the conversation. The message history is preserved; the new model takes effect on the next step.\n\nSelects GraphQL Wire_Name `withModel` on `LLM`."]
    #[must_use]
    pub fn with_model(&self, model: impl Into<String>) -> super::Llm {
        let query = self.selection.select("withModel");
        let query = query.arg("model", model.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withModel` with a borrowed, reusable `LlmWithModelOpts` value."]
    #[must_use]
    pub fn with_model_opts(&self, model: impl Into<String>, opts: &LlmWithModelOpts) -> super::Llm {
        let query = self.selection.select("withModel");
        let query = query.arg("model", model.into());
        let query = if let Some(value) = &opts.provider {
            query.arg("provider", value)
        } else {
            query
        };
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Queue a user prompt, to be sent to the model on the next step or loop.\n\nSelects GraphQL Wire_Name `withPrompt` on `LLM`."]
    #[must_use]
    pub fn with_prompt(&self, prompt: impl Into<String>) -> super::Llm {
        let query = self.selection.select("withPrompt");
        let query = query.arg("prompt", prompt.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Queue a file's contents as a user prompt, like withPrompt.\n\nSelects GraphQL Wire_Name `withPromptFile` on `LLM`."]
    #[must_use]
    pub fn with_prompt_file(&self, file: impl Into<crate::IdInput<super::File>>) -> super::Llm {
        let query = self.selection.select("withPromptFile");
        let query = query.arg_id_input("file", file.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Append an assistant response to the message history without calling the model, e.g. to reconstruct a conversation from another source.\n\nSelects GraphQL Wire_Name `withResponse` on `LLM`."]
    #[must_use]
    pub fn with_response(&self, content: Vec<super::LlmContentBlockInput>) -> super::Llm {
        let query = self.selection.select("withResponse");
        let query = query.arg("content", content);
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withResponse` with a borrowed, reusable `LlmWithResponseOpts` value."]
    #[must_use]
    pub fn with_response_opts(
        &self,
        content: Vec<super::LlmContentBlockInput>,
        opts: &LlmWithResponseOpts,
    ) -> super::Llm {
        let query = self.selection.select("withResponse");
        let query = if let Some(value) = &opts.cached_token_reads {
            query.arg("cachedTokenReads", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.cached_token_writes {
            query.arg("cachedTokenWrites", value)
        } else {
            query
        };
        let query = query.arg("content", content);
        let query = if let Some(value) = &opts.input_tokens {
            query.arg("inputTokens", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.output_tokens {
            query.arg("outputTokens", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.total_tokens {
            query.arg("totalTokens", value)
        } else {
            query
        };
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Add a system prompt, instructing the model across the whole conversation.\n\nSelects GraphQL Wire_Name `withSystemPrompt` on `LLM`."]
    #[must_use]
    pub fn with_system_prompt(&self, prompt: impl Into<String>) -> super::Llm {
        let query = self.selection.select("withSystemPrompt");
        let query = query.arg("prompt", prompt.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Append the result of a tool call to the message history.\n\nSelects GraphQL Wire_Name `withToolResult` on `LLM`."]
    #[must_use]
    pub fn with_tool_result(
        &self,
        call_id: impl Into<String>,
        content: impl Into<String>,
        errored: bool,
    ) -> super::Llm {
        let query = self.selection.select("withToolResult");
        let query = query.arg("callId", call_id.into());
        let query = query.arg("content", content.into());
        let query = query.arg("errored", errored);
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Expose an object's methods as tools. Every eligible method of the bound object becomes a tool; a tool that returns this object's own type replaces it as the new state. Repeatable to bind several objects.\n\nSelects GraphQL Wire_Name `withTools` on `LLM`."]
    #[must_use]
    pub fn with_tools(&self, object: impl Into<crate::IdInput<super::NodeClient>>) -> super::Llm {
        let query = self.selection.select("withTools");
        let query = query.arg_id_input("object", object.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withTools` with a borrowed, reusable `LlmWithToolsOpts` value."]
    #[must_use]
    pub fn with_tools_opts(
        &self,
        object: impl Into<crate::IdInput<super::NodeClient>>,
        opts: &LlmWithToolsOpts,
    ) -> super::Llm {
        let query = self.selection.select("withTools");
        let query = if let Some(value) = &opts.except {
            query.arg("except", value)
        } else {
            query
        };
        let query = query.arg_id_input("object", object.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Bind the LLM to a workspace, exposing its modules as tools exactly as the Dagger CLI would serve them for that workspace.\n\nSelects GraphQL Wire_Name `withWorkspace` on `LLM`."]
    #[must_use]
    pub fn with_workspace(
        &self,
        workspace: impl Into<crate::IdInput<super::Workspace>>,
    ) -> super::Llm {
        let query = self.selection.select("withWorkspace");
        let query = query.arg_id_input("workspace", workspace.into());
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Disable the default system prompt\n\nSelects GraphQL Wire_Name `withoutDefaultSystemPrompt` on `LLM`."]
    #[must_use]
    pub fn without_default_system_prompt(&self) -> super::Llm {
        let query = self.selection.select("withoutDefaultSystemPrompt");
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Clear the message history, keeping only the system prompts.\n\nSelects GraphQL Wire_Name `withoutMessageHistory` on `LLM`."]
    #[must_use]
    pub fn without_message_history(&self) -> super::Llm {
        let query = self.selection.select("withoutMessageHistory");
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Clear the user-added system prompts, keeping only the default system prompt.\n\nSelects GraphQL Wire_Name `withoutSystemPrompts` on `LLM`."]
    #[must_use]
    pub fn without_system_prompts(&self) -> super::Llm {
        let query = self.selection.select("withoutSystemPrompts");
        super::Llm {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return the workspace the LLM is bound to.\n\nSelects GraphQL Wire_Name `workspace` on `LLM`."]
    #[must_use]
    pub fn workspace(&self) -> super::Workspace {
        let query = self.selection.select("workspace");
        super::Workspace {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for Llm {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for Llm {
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
