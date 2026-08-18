package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

type llmSchema struct {
}

var _ SchemaResolvers = &llmSchema{}

func (s llmSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("llm", s.llm).
			WithInput(dagql.PerSessionInput).
			Experimental("LLM support is not yet stabilized").
			Doc(`Initialize a new LLM conversation.`).
			Args(
				dagql.Arg("model").Doc(
					`The model to converse with, e.g. "claude-sonnet-4-5" or "gpt-5.4". Defaults to the configured default model.`),
				dagql.Arg("provider").Doc(
					`The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.`).
					View(AfterVersion("v1.0.0-0")),
				dagql.Arg("maxAPICalls").Doc("Cap the number of API calls for this LLM").
					View(BeforeVersion("v1.0.0-0")),
			),
	}.Install(srv)
	dagql.Fields[*core.LLM]{
		dagql.Func("model", s.model).
			Doc("The model the conversation is running against, after resolving any configured default."),
		dagql.Func("provider", s.provider).
			Doc(`The provider serving the model, e.g. "anthropic", "openai", "google", or "local".`),
		dagql.Func("contextWindow", s.contextWindow).
			View(AfterVersion("v1.0.0-0")).
			Doc("The model's total context window in tokens, or null if unknown (e.g. a local or uncatalogued model)."),
		dagql.Func("messages", s.messages).
			View(AfterVersion("v1.0.0-0")).
			Doc("The full message history, as structured messages."),
		dagql.Func("transcript", s.transcript).
			View(AfterVersion("v1.0.0-0")).
			Doc("The message history rendered as a plain-text transcript, suitable for feeding back to an LLM (e.g. for summarization)."),
		dagql.Func("withoutMessageHistory", s.withoutMessageHistory).
			Doc("Clear the message history, keeping only the system prompts."),
		dagql.Func("withoutSystemPrompts", s.withoutSystemPrompts).
			Doc("Clear the user-added system prompts, keeping only the default system prompt."),
		dagql.Func("lastReply", s.lastReply).
			Doc("The text of the model's most recent reply."),
		dagql.Func("withWorkspace", s.withWorkspace).
			View(AfterVersion("v1.0.0-0")).
			Doc("Bind the LLM to a workspace, exposing its modules as tools exactly as the Dagger CLI would serve them for that workspace.").
			Args(
				dagql.Arg("workspace").Doc("The workspace to work in."),
			),
		dagql.Func("workspace", s.workspace).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return the workspace the LLM is bound to."),
		dagql.Func("withModel", s.withModel).
			Doc("Change the model for the rest of the conversation. The message history is preserved; the new model takes effect on the next step.").
			Args(
				dagql.Arg("model").Doc(`The model to use, e.g. "claude-sonnet-4-5" or "gpt-5.4".`),
				dagql.Arg("provider").Doc(
					`The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.`).
					View(AfterVersion("v1.0.0-0")),
			),
		dagql.Func("reasoningEffort", s.reasoningEffort).
			View(AfterVersion("v1.0.0-0")).
			Doc(`The reasoning effort in use, e.g. "low", "medium", or "high". Empty or "none" when reasoning is disabled.`),
		dagql.Func("withReasoningEffort", s.withReasoningEffort).
			View(AfterVersion("v1.0.0-0")).
			Doc("Change the reasoning effort for the rest of the conversation, overriding any configured default. The message history is preserved; the new effort takes effect on the next step.").
			Args(
				dagql.Arg("effort").Doc(
					`The reasoning effort, e.g. "low", "medium", or "high"; "none" disables reasoning. Supported levels are model-specific — some models also accept e.g. "minimal", "xhigh", or "max".`),
			),
		dagql.Func("withPrompt", s.withPrompt).
			Doc("Queue a user prompt, to be sent to the model on the next step or loop.").
			Args(
				dagql.Arg("prompt").Doc("The prompt to send"),
			),
		dagql.Func("__mcp", func(ctx context.Context, self *core.LLM, _ struct{}) (dagql.Nullable[core.Void], error) {
			currentSrv, err := core.CurrentDagqlServer(ctx)
			if err != nil {
				return dagql.Null[core.Void](), err
			}
			return dagql.Null[core.Void](), self.MCP(ctx, currentSrv)
		}).
			Doc("instantiates an mcp server"),
		dagql.Func("withPromptFile", s.withPromptFile).
			Doc("Queue a file's contents as a user prompt, like withPrompt.").
			Args(
				dagql.Arg("file").Doc("The file to read the prompt from"),
			),
		dagql.Func("withSystemPrompt", s.withSystemPrompt).
			Doc("Add a system prompt, instructing the model across the whole conversation.").
			Args(
				dagql.Arg("prompt").Doc("The system prompt to send"),
			),
		dagql.Func("withResponse", s.withResponse).
			View(AfterVersion("v1.0.0-0")).
			Doc("Append an assistant response to the message history without calling the model, e.g. to reconstruct a conversation from another source.").
			Args(
				dagql.Arg("content").Doc("The response content"),
				dagql.Arg("inputTokens").Doc("Uncached input tokens sent"),
				dagql.Arg("outputTokens").Doc("Tokens received from the model, including text and tool calls"),
				dagql.Arg("cachedTokenReads").Doc("Cached input tokens read"),
				dagql.Arg("cachedTokenWrites").Doc("Cached input tokens written"),
				dagql.Arg("totalTokens").Doc("Total tokens consumed by this response"),
			),
		dagql.Func("withToolResult", s.withToolResult).
			View(AfterVersion("v1.0.0-0")).
			Doc("Append the result of a tool call to the message history.").
			Args(
				dagql.Arg("callId").Doc("The ID of the tool call this result responds to"),
				dagql.Arg("content").Doc("The content returned by the tool"),
				dagql.Arg("errored").Doc("Whether the tool call resulted in an error"),
			),
		dagql.Func("withTools", s.withTools).
			View(AfterVersion("v1.0.0-0")).
			Doc("Expose an object's methods as tools. Every eligible method of the bound object becomes a tool; a tool that returns this object's own type replaces it as the new state. Repeatable to bind several objects.").
			Args(
				// @expectedType(Node) lets a statically typed caller (e.g. Dang) pass
				// any object where this ID! is wanted, since every object implements
				// the universal Node interface; the value is conveyed as its id.
				dagql.Arg("object").Doc("The object whose methods become tools.").
					Directive(dagql.ExpectedTypeDirective("Node")).
					// The bound object's value is not needed to reconstruct the
					// conversation on replay — only its type, to expose its methods
					// as tools — so carry it by reference and load it lazily. This
					// keeps restoring a persisted session from re-running the call
					// that produced the object (which may have side effects or may
					// no longer be reproducible).
					LazyRef(),
				dagql.Arg("except").Doc("Method names to exclude from the toolset (e.g. constructors, entrypoints)."),
			),
		dagql.Func("withoutDefaultSystemPrompt", s.withoutDefaultSystemPrompt).
			Doc("Disable the default system prompt"),
		dagql.Func("withMCPServer", s.withMCPServer).
			Doc("Add an external MCP server to the LLM").
			Args(
				dagql.Arg("name").Doc("The name of the MCP server"),
				dagql.Arg("service").Doc("The MCP service to run and communicate with over stdio"),
			),
		dagql.Func("withSkills", s.withSkills).
			View(AfterVersion("v1.0.0-0")).
			Doc("Install skills from a directory, adding them to the skills the model discovers with ListSkills and reads with ReadSkill. " +
				"Each skill is a directory containing a SKILL.md with name and description frontmatter, discovered anywhere in the tree. " +
				"Installed skills take precedence over skills discovered in the workspace, but cannot shadow the engine's built-in skills.").
			Args(
				dagql.Arg("directory").Doc("A directory containing skills, each a subdirectory holding a SKILL.md."),
			),
		dagql.Func("skills", s.skills).
			View(AfterVersion("v1.0.0-0")).
			Doc("The skills visible to the model, exactly as the ListSkills tool serves them: engine-embedded skills, skills installed with withSkills, and skills discovered in the workspace."),
		dagql.NodeFunc("sync", func(ctx context.Context, self dagql.ObjectResult[*core.LLM], _ struct{}) (res dagql.ID[*core.LLM], _ error) {
			id, err := self.ID()
			if err != nil {
				return res, err
			}
			return dagql.NewID[*core.LLM](id), nil
		}).
			Doc("Force evaluation of the conversation's pending operations (prompts, steps, loops) in the engine."),
		dagql.NodeFunc("portableID", func(ctx context.Context, self dagql.ObjectResult[*core.LLM], _ struct{}) (dagql.AnyID, error) {
			recipe, err := self.Self().PortableRecipe(ctx)
			if err != nil {
				return dagql.AnyID{}, err
			}
			id, err := recipe.RecipeID(ctx)
			if err != nil {
				return dagql.AnyID{}, err
			}
			return dagql.NewAnyID(id), nil
		}).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("An ID describes the current attached result and must not be served from cache.").
			Doc("A portable, self-contained ID for the conversation that node() can resolve in any session. " +
				"Unlike id, which may return an engine-local runtime handle valid only within the current session, " +
				"this returns the recipe form suitable for persisting and later restoring the conversation. " +
				"The recipe is flattened: bindings superseded during the session (workspace overlays recorded by " +
				"each mutating tool call, and re-bound toolsets) are dropped, while the current workspace binding — " +
				"including any pending, un-exported edits — is preserved."),
		dagql.NodeFunc("replay", s.replay).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerCallInput).
			Doc("Re-emit telemetry spans for the full message history, so a loaded conversation displays in the TUI."),
		dagql.NodeFunc("loop", s.loop).
			Doc("Send the queued prompt and step the model against the available tools, until it ends its turn: a reply with no tool calls and nothing left queued.").
			Args(
				dagql.Arg("maxSteps").Doc("Cap the number of steps. The loop fails if the cap is reached before the model ends its turn.").
					View(AfterVersion("v1.0.0-0")),
				dagql.Arg("maxTokens").Doc("Cap the model's output tokens on each step. Defaults to the model's maximum.").
					View(AfterVersion("v1.0.0-0")),
			),
		dagql.NodeFunc("step", s.step).
			Doc("Advance the conversation by a single step: send the queued prompt or tool results to the model, evaluate any tool calls it makes, and queue their results. Use loop to step until the model ends its turn.").
			Args(
				dagql.Arg("maxTokens").Doc("Cap the model's output tokens for this step. Defaults to the model's maximum.").
					View(AfterVersion("v1.0.0-0")),
			),
		dagql.NodeFunc("spawn", s.spawn).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("Every spawn mints a distinct agent instance.").
			Doc(`Spawn the conversation as an agent: a startable, addressable evaluation loop seeded with this conversation's state, tools, and workspace.`,
				`Every spawn mints a unique agent instance — two spawns of an identical conversation are two distinct agents, like two calls to a process spawn. The returned ID is pinned to the instance (via the agent lookup field), so re-loading it re-addresses the same agent from any request in the session.`).
			Args(
				dagql.Arg("name").Doc("Display label for the agent — telemetry and error messages; carries no identity. Defaults to a short name derived from the conversation."),
			),
		// agent is deliberately cached (no DoNotCache): the instance ID
		// argument pins the lookup to one spawned instance, so the same
		// chain always denotes the same agent value — which is exactly what
		// lets spawn pin its result's identity by re-exec: re-loading a
		// spawned agent's ID replays …llm!agent(id:…) and lands on the same
		// value, never re-minting an instance.
		dagql.NodeFunc("agent", s.agent).
			View(AfterVersion("v1.0.0-0")).
			Doc(`Rehydrate a spawned agent's handle from its instance ID.`,
				`This is the lookup spawn pins its result's identity through: the returned handle's ID is an honest, replayable chain denoting the one instance the spawn minted. It never creates an instance itself.`).
			Args(
				dagql.Arg("id").Doc("The agent instance ID, as minted by the spawn that created the agent."),
				dagql.Arg("name").Doc("The agent's display name, as recorded by the spawn."),
			),
		dagql.Func("hasPending", s.hasPending).
			View(AfterVersion("v1.0.0-0")).
			Doc("Report whether anything is queued to send to the model: an unsent prompt or unevaluated tool results. When true, another step will do work; when false, the turn is complete."),
		dagql.Func("fork", s.fork).
			View(AfterVersion("v1.0.0-0")).
			Doc("Fork the conversation, so that otherwise-identical follow-ups evaluate independently instead of deduplicating to a single cached result.").
			Args(
				dagql.Arg("label").Doc(`A label distinguishing this fork from its siblings, e.g. "attempt-2" when retrying a flaky evaluation.`),
			),
		// attempt is superseded in v1 by fork, but remains visible to pre-v1
		// module views (e.g. the evaluator module).
		dagql.Func("attempt", s.attempt).
			View(BeforeVersion("v1.0.0-0")).
			Doc("create a branch in the LLM's history"),
		dagql.Func("tools", s.tools).
			Doc("Render documentation for the tools currently exposed to the model."),
		dagql.Func("tokenUsage", s.tokenUsage).
			Doc("The cumulative token usage, summed across every API call in the conversation."),
		dagql.Func("contextTokens", s.contextTokens).
			View(AfterVersion("v1.0.0-0")).
			Doc("estimated number of tokens currently occupying the context window; unlike tokenUsage this is not cumulative over the session"),
	}.Install(srv)
	// The conversation data types are pure in-memory values whose accessors
	// only unwrap struct fields; observers (e.g. the CLI's context
	// visualizer) re-read the whole ever-growing conversation repeatedly, so
	// accessor spans here would be pure noise in quadratically-growing
	// volume. Suppress their telemetry entirely.
	noAccessorTelemetry := dagql.InstallOpts{NoTelemetryAccessors: true}
	dagql.Fields[*core.LLMTokenUsage]{}.Install(srv, noAccessorTelemetry)
	// The content-block message model is only visible to v1+ module views;
	// installing the classes with a view gate also gates their generated
	// ID/load fields and Env/Binding extensions.
	srv.InstallObject(dagql.NewClass[*core.LLMMessage](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.LLMContentBlock](srv).View(AfterVersion("v1.0.0-0")))
	srv.InstallObject(dagql.NewClass[*core.LLMSkill](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.LLMMessage]{}.Install(srv, noAccessorTelemetry)
	dagql.Fields[*core.LLMContentBlock]{}.Install(srv, noAccessorTelemetry)
	dagql.Fields[*core.LLMSkill]{}.Install(srv, noAccessorTelemetry)
	core.LLMMessageRoles.Install(srv, AfterVersion("v1.0.0-0"))
	core.LLMContentBlockKinds.Install(srv, AfterVersion("v1.0.0-0"))
	dagql.MustInputSpec(core.LLMContentBlockInput{}).Install(srv, AfterVersion("v1.0.0-0"))
}

func (s *llmSchema) withWorkspace(ctx context.Context, llm *core.LLM, args struct {
	Workspace dagql.ID[*core.Workspace]
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := args.Workspace.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	return llm.WithWorkspace(ws), nil
}

func (s *llmSchema) workspace(ctx context.Context, llm *core.LLM, args struct{}) (res dagql.ObjectResult[*core.Workspace], _ error) {
	ws := llm.Workspace()
	if ws.Self() == nil {
		// llm() starts unbound; a workspace is only present once the caller
		// binds one via withWorkspace. Return an error rather than a
		// zero-value Workspace!, which nil-derefs in the Workspace field
		// resolvers and crashes the engine.
		return res, fmt.Errorf("no workspace is bound to this LLM (bind one with withWorkspace)")
	}
	return ws, nil
}

func (s *llmSchema) model(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return "", err
	}
	return ep.Model, nil
}

func (s *llmSchema) contextWindow(ctx context.Context, llm *core.LLM, args struct{}) (dagql.Nullable[dagql.Int], error) {
	none := dagql.Null[dagql.Int]()
	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return none, err
	}
	if ep.ContextWindow <= 0 {
		return none, nil
	}
	return dagql.NonNull(dagql.NewInt(int(ep.ContextWindow))), nil
}

func (s *llmSchema) provider(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return "", err
	}
	return string(ep.Provider), nil
}

func (s *llmSchema) lastReply(ctx context.Context, llm *core.LLM, args struct{}) (dagql.String, error) {
	reply, _ := llm.LastReply()
	return dagql.NewString(reply), nil
}

func (s *llmSchema) withModel(ctx context.Context, llm *core.LLM, args struct {
	Model    string
	Provider dagql.Optional[dagql.String]
}) (*core.LLM, error) {
	return llm.WithModel(args.Model, args.Provider.Value.String()), nil
}

func (s *llmSchema) reasoningEffort(ctx context.Context, llm *core.LLM, args struct{}) (string, error) {
	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return "", err
	}
	return ep.ReasoningEffort, nil
}

func (s *llmSchema) withReasoningEffort(ctx context.Context, llm *core.LLM, args struct {
	Effort string
}) (*core.LLM, error) {
	return llm.WithReasoningEffort(args.Effort), nil
}

func (s *llmSchema) withPrompt(ctx context.Context, llm *core.LLM, args struct {
	Prompt string
}) (*core.LLM, error) {
	return llm.WithPrompt(args.Prompt), nil
}

func (s *llmSchema) withSystemPrompt(ctx context.Context, llm *core.LLM, args struct {
	Prompt string
}) (*core.LLM, error) {
	return llm.WithSystemPrompt(args.Prompt), nil
}

func (s *llmSchema) withResponse(ctx context.Context, llm *core.LLM, args struct {
	Content           []dagql.InputObject[core.LLMContentBlockInput]
	InputTokens       int64 `default:"0"`
	OutputTokens      int64 `default:"0"`
	CachedTokenReads  int64 `default:"0"`
	CachedTokenWrites int64 `default:"0"`
	TotalTokens       int64 `default:"0"`
}) (*core.LLM, error) {
	blocks := make([]*core.LLMContentBlock, len(args.Content))
	for i, input := range args.Content {
		blocks[i] = input.Value.ToLLMContentBlock()
	}
	return llm.WithResponse(blocks, core.LLMTokenUsage{
		InputTokens:       args.InputTokens,
		OutputTokens:      args.OutputTokens,
		CachedTokenReads:  args.CachedTokenReads,
		CachedTokenWrites: args.CachedTokenWrites,
		TotalTokens:       args.TotalTokens,
	}), nil
}

func (s *llmSchema) withToolResult(ctx context.Context, llm *core.LLM, args struct {
	CallID  string `name:"callId"`
	Content string
	Errored bool
}) (*core.LLM, error) {
	return llm.WithToolResult(args.CallID, args.Content, args.Errored), nil
}

func (s *llmSchema) withTools(ctx context.Context, llm *core.LLM, args struct {
	Object dagql.AnyID
	Except []string `default:"[]"`
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	id, err := args.Object.ID()
	if err != nil {
		return nil, err
	}
	// Resolve the bound object's type from its ID without evaluating it, so the
	// toolset can be built lazily. For a user-module type absent from the current
	// bootstrap schema, ObjectTypeForID rebuilds its defining schema from the
	// call's module provenance. The object itself is loaded only when a tool is
	// actually invoked on it (see MCP.boundToolObject). This is what lets a
	// persisted session restore a binding whose object has side effects or is no
	// longer reproducible without re-running its construction.
	if id.Type() != nil {
		objType, ok, err := srv.ObjectTypeForID(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			return llm.WithLazyTools(id, objType, args.Except), nil
		}
	}
	// Fall back to eager loading if the type isn't resolvable structurally.
	obj, err := srv.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	return llm.WithTools(obj, args.Except), nil
}

func (s *llmSchema) withoutDefaultSystemPrompt(ctx context.Context, llm *core.LLM, args struct{}) (*core.LLM, error) {
	return llm.WithoutDefaultSystemPrompt(), nil
}

func (s *llmSchema) withMCPServer(ctx context.Context, llm *core.LLM, args struct {
	Name    string
	Service core.ServiceID
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := args.Service.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	return llm.WithMCPServer(args.Name, svc), nil
}

func (s *llmSchema) withSkills(ctx context.Context, llm *core.LLM, args struct {
	Directory core.DirectoryID
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := args.Directory.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	return llm.WithSkills(dir), nil
}

func (s *llmSchema) skills(ctx context.Context, llm *core.LLM, _ struct{}) ([]*core.LLMSkill, error) {
	return llm.Skills(ctx)
}

func (s *llmSchema) withPromptFile(ctx context.Context, llm *core.LLM, args struct {
	File core.FileID
}) (*core.LLM, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	file, err := args.File.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, file); err != nil {
		return nil, err
	}
	prompt, err := file.Self().Contents(ctx, file, nil, nil)
	if err != nil {
		return nil, err
	}
	return llm.WithPrompt(string(prompt)), nil
}

func (s *llmSchema) loop(ctx context.Context, parent dagql.ObjectResult[*core.LLM], args struct {
	MaxSteps  dagql.Optional[dagql.Int] `name:"maxSteps"`
	MaxTokens dagql.Optional[dagql.Int] `name:"maxTokens"`
}) (dagql.ObjectResult[*core.LLM], error) {
	return parent.Self().Loop(ctx, parent, int(args.MaxSteps.Value), int(args.MaxTokens.Value))
}

func (s *llmSchema) step(ctx context.Context, parent dagql.ObjectResult[*core.LLM], args struct {
	MaxTokens dagql.Optional[dagql.Int] `name:"maxTokens"`
}) (dagql.ObjectResult[*core.LLM], error) {
	return parent.Self().Step(ctx, parent, int(args.MaxTokens.Value))
}

// spawn mints a unique agent instance from the conversation. Instance
// identity is minted here — where instances are born — never from caller
// entropy: the resolver generates the instance ID, then pins it by re-exec
// (design §9, the same trick send uses for message identity): a real Select
// through the pure agent(id:) lookup on the same receiver yields a handle
// whose ID is the honest, replayable chain `…llm!agent(id:"…", name:"…")` —
// re-addressable from any request in the session, and carrying the unique
// instance ID into the value's content digest, so every spawn gets a fresh
// runtime registry entry (a dismissed name can never resolve to a
// predecessor's tombstone). spawn is DoNotCache and ID-returning like every
// imperative verb: lazy clients force the mint exactly once and re-hydrate
// the handle from the ID, which replays the lookup, not the spawn.
//
// The registry entry is created HERE, not lazily on first use: spawn is
// mint-create-pin, as rehydrate is adopt-create-pin. Since a registry miss on
// send is an error rather than a constructor (resume-from-trace §4.2 — a miss
// used to boot an amnesiac twin from the seed), the two verbs that create an
// instance are the only two that create its entry, and every other verb
// addresses one that exists.
func (s *llmSchema) spawn(ctx context.Context, parent dagql.ObjectResult[*core.LLM], args struct {
	Name dagql.Optional[dagql.String]
}) (res dagql.Result[core.AgentID], _ error) {
	name := args.Name.Value.String()
	if name == "" {
		// Derive a short display name from the seed conversation's recipe
		// digest — a readable default label, with no identity role.
		// (parent.ID().Digest() would panic here: a post-evaluation LLM
		// carries a handle-form ID with no digest — see core/llm.go's
		// llmCallDigest derivation for the same dance.)
		dig, err := parent.RecipeDigest(ctx)
		if err != nil {
			return res, fmt.Errorf("llm recipe digest: %w", err)
		}
		enc := dig.Encoded()
		if len(enc) > 8 {
			enc = enc[:8]
		}
		name = "agent-" + enc
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, err
	}
	var pinned dagql.ObjectResult[*core.Agent]
	if err := srv.Select(ctx, parent, &pinned, dagql.Selector{
		Field: "agent",
		Args: []dagql.NamedInput{
			{
				Name:  "id",
				Value: dagql.NewString(identity.NewID()),
			},
			{
				Name:  "name",
				Value: dagql.NewString(name),
			},
		},
	}); err != nil {
		return res, err
	}
	agents, err := agentRuntimes(ctx)
	if err != nil {
		return res, err
	}
	if _, err := agents.GetOrCreate(ctx, pinned); err != nil {
		return res, err
	}
	pinnedID, err := pinned.ID()
	if err != nil {
		return res, fmt.Errorf("agent ID: %w", err)
	}
	return dagql.NewResultForCurrentCall(ctx, dagql.NewID[*core.Agent](pinnedID))
}

// agent is the pure lookup spawn pins instance identity through: it
// reconstructs the agent value from the (id, name) literals on the chain,
// touching no runtime state — a cold re-Select of a spawned agent's ID lands
// here and projects IDLE-from-absence like any never-started agent.
func (s *llmSchema) agent(ctx context.Context, parent dagql.ObjectResult[*core.LLM], args struct {
	ID   string
	Name string
}) (*core.Agent, error) {
	return &core.Agent{
		Seed:       parent,
		InstanceID: args.ID,
		Name:       args.Name,
	}, nil
}

func (s *llmSchema) replay(ctx context.Context, parent dagql.ObjectResult[*core.LLM], _ struct{}) (res dagql.ID[*core.LLM], _ error) {
	parent.Self().Replay(ctx)
	id, err := parent.ID()
	if err != nil {
		return res, err
	}
	return dagql.NewID[*core.LLM](id), nil
}

func (s *llmSchema) hasPending(ctx context.Context, llm *core.LLM, args struct{}) (bool, error) {
	return llm.HasPending(), nil
}

func (s *llmSchema) fork(_ context.Context, llm *core.LLM, _ struct {
	Label string
}) (*core.LLM, error) {
	// The label participates in the returned object's ID, which is what makes
	// the fork evaluate independently; the state itself is just a clone.
	return llm.Clone(), nil
}

// attempt is the pre-v1 spelling of fork.
func (s *llmSchema) attempt(_ context.Context, llm *core.LLM, _ struct {
	Number int
}) (*core.LLM, error) {
	return llm.Clone(), nil
}

func (s *llmSchema) llm(ctx context.Context, parent dagql.ObjectResult[*core.Query], args struct {
	Model    dagql.Optional[dagql.String]
	Provider dagql.Optional[dagql.String]
	// Legacy cap on API calls, only exposed to pre-v1 module views; v1+
	// callers pass maxSteps to loop() instead.
	MaxAPICalls dagql.Optional[dagql.Int] `name:"maxAPICalls"`
}) (inst dagql.ObjectResult[*core.LLM], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	model := args.Model.Value.String()
	provider := args.Provider.Value.String()
	if model == "" {
		// No model requested: resolve the configured default and re-call this
		// field with it pinned, the way Container.from re-calls itself with
		// the digested ref. The recorded ID then names the model the
		// conversation actually runs against, so a saved session resumes on
		// its own model rather than whatever default the resuming
		// environment happens to configure.
		defModel, defProvider, routeErr := parent.Self().DefaultLLMRoute(ctx, provider)
		switch {
		case routeErr != nil:
			// Routing reads the client's environment; a failure there should
			// surface at first use, as it did before pinning existed — not
			// break constructing the object.
			slog.Warn("could not resolve default LLM route; leaving llm() unpinned", "error", routeErr)
		case defModel != "":
			llmArgs := []dagql.NamedInput{
				{Name: "model", Value: dagql.Opt(dagql.NewString(defModel))},
			}
			if defProvider != "" {
				llmArgs = append(llmArgs, dagql.NamedInput{
					Name:  "provider",
					Value: dagql.Opt(dagql.NewString(defProvider)),
				})
			}
			var pinned dagql.ObjectResult[*core.LLM]
			if err := srv.Select(ctx, parent, &pinned, dagql.Selector{
				Field: "llm",
				Args:  llmArgs,
			}); err != nil {
				return inst, err
			}
			if args.MaxAPICalls.Valid && args.MaxAPICalls.Value.Int() > 0 {
				// The legacy knob only exists in pre-v1 views while provider
				// only exists in v1+, so no single view lets the inner call
				// carry both. Apply it to this outer call's own result; it is
				// deliberately not part of the durable recipe anyway.
				llm := pinned.Self().WithMaxAPICalls(args.MaxAPICalls.Value.Int())
				return dagql.NewObjectResultForCurrentCall(ctx, srv, llm)
			}
			return pinned, nil
		}
	}
	llm, err := parent.Self().NewLLM(ctx, model, provider)
	if err != nil {
		return inst, err
	}
	if args.MaxAPICalls.Valid && args.MaxAPICalls.Value.Int() > 0 {
		llm = llm.WithMaxAPICalls(args.MaxAPICalls.Value.Int())
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, llm)
}

func (s *llmSchema) messages(_ context.Context, llm *core.LLM, _ struct{}) ([]*core.LLMMessage, error) {
	// tokenUsage is a non-null field, so messages that never had usage
	// recorded (prompts, tool results) must serve zeros rather than nil.
	msgs := make([]*core.LLMMessage, len(llm.Messages))
	for i, msg := range llm.Messages {
		if msg.TokenUsage == nil {
			msg = msg.Clone()
			msg.TokenUsage = &core.LLMTokenUsage{}
		}
		msgs[i] = msg
	}
	return msgs, nil
}

func (s *llmSchema) transcript(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	return llm.Transcript(), nil
}

func (s *llmSchema) tools(ctx context.Context, llm *core.LLM, _ struct{}) (string, error) {
	return llm.ToolsDoc(ctx)
}

func (s *llmSchema) tokenUsage(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLMTokenUsage, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	return llm.TokenUsage(ctx, srv)
}

func (s *llmSchema) contextTokens(ctx context.Context, llm *core.LLM, _ struct{}) (int, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return 0, err
	}
	return llm.ContextTokens(ctx, srv)
}

func (s *llmSchema) withoutMessageHistory(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLM, error) {
	return llm.WithoutMessageHistory(), nil
}

func (s *llmSchema) withoutSystemPrompts(ctx context.Context, llm *core.LLM, _ struct{}) (*core.LLM, error) {
	return llm.WithoutSystemPrompts(), nil
}
