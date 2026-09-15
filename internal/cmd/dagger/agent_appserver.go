package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/codexapp"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

// `dagger agent --app-server` serves the Codex app-server protocol over stdio
// instead of opening the interactive prompt: the same composed agents, driven
// by an IDE or app that speaks Codex's JSON-RPC dialect. The protocol lives in
// the codexapp package; this file is the Dagger side of it -- the backend that
// mints conversations from the session machinery the TUI uses (LLMSession,
// sessionAgent) and answers the questions that need an engine.

var agentAppServer bool

// prepareAppServerFrontend keeps stdout clear for the protocol. By the time a
// command runs, Main has already resolved an "auto" progress mode to a
// concrete one (the pretty TUI on a terminal, the report frontend off one),
// so the choice to override is made on whether the user asked for a mode
// explicitly, not on what the variable holds.
func prepareAppServerFrontend(cmd *cobra.Command) error {
	explicit := os.Getenv("DAGGER_PROGRESS") != ""
	if flag := cmd.Flags().Lookup("progress"); flag != nil && flag.Changed {
		explicit = true
	}
	return applyAppServerProgress(explicit)
}

// applyAppServerProgress installs the frontend app-server mode runs with. An
// explicit mode is honoured, except the pretty TUI, which would fight the
// client for the terminal; an auto-resolved one is replaced with a plain
// stream on stderr, like `dagger mcp`, since the report frontend would say
// nothing until the client hangs up.
func applyAppServerProgress(explicit bool) error {
	if explicit {
		if progress == "tty" {
			return fmt.Errorf("--app-server uses stdout for the protocol; use --progress=plain")
		}
		return nil
	}
	progress = "plain"
	Frontend = idtui.NewPlain(stderr)
	return nil
}

// teeFrontend feeds the item stream the same live telemetry the frontend
// renders. Installed on the global before the engine session wires its
// exporters, so the stream sees every span and log the session produces.
type teeFrontend struct {
	idtui.Frontend
	spans sdktrace.SpanExporter
	logs  sdklog.Exporter
}

func (fe teeFrontend) SpanExporter() sdktrace.SpanExporter {
	return codexapp.TeeSpanExporters(fe.Frontend.SpanExporter(), fe.spans)
}

func (fe teeFrontend) LogExporter() sdklog.Exporter {
	return codexapp.TeeLogExporters(fe.Frontend.LogExporter(), fe.logs)
}

func runAgentAppServer(ctx context.Context, include []string) error {
	conn := codexapp.NewConn(stdin, stdout)
	stream := codexapp.NewItemStream(conn)
	Frontend = teeFrontend{Frontend: Frontend, spans: stream, logs: stream}
	return withEngine(ctx, client.Params{LoadWorkspaceModules: true}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		llmID, err := composeAgents(ctx, dag, include)
		if err != nil {
			return err
		}
		backend, err := newAppServerBackend(ctx, dag, dagger.Ref[*dagger.LLM](dag, dagger.ID(llmID)))
		if err != nil {
			return err
		}
		srv := codexapp.NewServer(conn, backend, stream)
		return srv.Serve(ctx)
	})
}

// appServerBackend implements codexapp.Backend on an LLMSession: every
// protocol thread is one of the session's conversations, and the thread id is
// the conversation's save identity, so threads persist as the same session
// files `dagger agent -r` resumes.
type appServerBackend struct {
	ctx     context.Context
	dag     *dagger.Client
	session *LLMSession
	// base is the composed agent group every new thread starts from.
	base *dagger.LLM
	info codexapp.ServerInfo

	mu    sync.Mutex
	convs map[*sessionAgent]*appServerConversation
}

func newAppServerBackend(ctx context.Context, dag *dagger.Client, base *dagger.LLM) (*appServerBackend, error) {
	// No shell handler and no frontend: nothing here has a prompt to redraw,
	// and the session tolerates both being absent.
	session, err := NewLLMSession(ctx, dag, "", nil, nil)
	if err != nil {
		return nil, err
	}
	b := &appServerBackend{
		ctx:     ctx,
		dag:     dag,
		session: session,
		base:    base,
		info: codexapp.ServerInfo{
			Version: engine.Version,
			Cwd:     workdir,
			Home:    filepath.Dir(llmconfig.ConfigFile),
		},
		convs: map[*sessionAgent]*appServerConversation{},
	}
	// Auto-save after every step under the thread's own identity, so each
	// thread maps to one session file (the TUI's save identity is
	// session-wide, which would make several threads fight over one file).
	session.onStep = func(a *sessionAgent) {
		b.mu.Lock()
		conv := b.convs[a]
		b.mu.Unlock()
		if conv == nil || conv.ephemeral {
			return
		}
		if _, err := a.AutoSaveSession(ctx, conv.Name(), conv.id); err != nil {
			slog.Warn("failed to auto-save thread", "thread", conv.id, "error", err)
		}
	}
	return b, nil
}

func (b *appServerBackend) Info() codexapp.ServerInfo { return b.info }

func (b *appServerBackend) NewThread(ctx context.Context, opts codexapp.ThreadOptions) (codexapp.Conversation, error) {
	llm := b.base
	if opts.Model != "" {
		llm = llm.WithModel(opts.Model, dagger.LLMWithModelOpts{Provider: opts.Provider})
	}
	if opts.Instructions != "" {
		llm = llm.WithSystemPrompt(opts.Instructions)
	}
	agent := b.session.NewConversation(defaultAgentName)
	if err := agent.setInitialLLM(llm); err != nil {
		return nil, err
	}
	return b.track(agent, opts.ID, opts.Ephemeral), nil
}

func (b *appServerBackend) LoadThread(ctx context.Context, threadID string) (codexapp.Conversation, error) {
	agent := b.session.NewConversation(defaultAgentName)
	if err := agent.setInitialLLM(b.base); err != nil {
		return nil, err
	}
	if err := agent.LoadSession(ctx, ctx, threadID); err != nil {
		return nil, err
	}
	return b.track(agent, threadID, false), nil
}

func (b *appServerBackend) track(agent *sessionAgent, id string, ephemeral bool) *appServerConversation {
	conv := &appServerConversation{backend: b, agent: agent, id: id, ephemeral: ephemeral}
	b.mu.Lock()
	b.convs[agent] = conv
	b.mu.Unlock()
	return conv
}

func (b *appServerBackend) SavedThreads(context.Context) ([]codexapp.SavedThread, error) {
	sessions, err := ListSessions()
	if err != nil {
		return nil, err
	}
	threads := make([]codexapp.SavedThread, 0, len(sessions))
	for _, meta := range sessions {
		createdAt, _ := time.Parse(time.RFC3339, meta.CreatedAt)
		threads = append(threads, codexapp.SavedThread{
			ID:        meta.LLMID, // ListSessions carries the file UUID here
			Name:      meta.Name,
			Model:     meta.Model,
			CreatedAt: createdAt,
		})
	}
	return threads, nil
}

func (b *appServerBackend) SavedThreadHistory(ctx context.Context, threadID string) ([]codexapp.Turn, error) {
	meta, err := readSessionMetadata(threadID)
	if err != nil {
		return nil, err
	}
	return llmHistoryTurns(ctx, b.dag, dagger.ID(meta.LLMID))
}

// Models lists the catalog models of every configured provider, marking the
// composed LLM's model as the default.
func (b *appServerBackend) Models(ctx context.Context) ([]codexapp.Model, error) {
	defaultModel, defaultProvider, _ := b.baseModel(ctx)
	cfg, err := llmconfig.Load()
	if err != nil {
		slog.Debug("could not load LLM config for model list", "error", err)
	}
	var providers []string
	if cfg != nil {
		for name, provider := range cfg.LLM.Providers {
			if provider.Enabled {
				providers = append(providers, name)
			}
		}
	}
	sort.Strings(providers)
	var models []codexapp.Model
	for _, provider := range providers {
		for _, m := range llmconfig.ModelsForProvider(provider) {
			models = append(models, catalogModel(provider, m, provider == defaultProvider && m.ID == defaultModel))
		}
	}
	if len(models) == 0 && defaultModel != "" {
		models = append(models, codexapp.Model{
			ID:                        defaultModel,
			Model:                     defaultModel,
			DisplayName:               defaultModel,
			Description:               defaultProvider,
			IsDefault:                 true,
			DefaultReasoningEffort:    "none",
			SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{},
			InputModalities:           []string{"text"},
		})
	}
	return models, nil
}

func (b *appServerBackend) baseModel(ctx context.Context) (model, provider string, err error) {
	model, err = b.base.Model(ctx)
	if err != nil {
		return "", "", err
	}
	provider, err = b.base.Provider(ctx)
	if err != nil {
		return model, "", err
	}
	return model, provider, nil
}

func catalogModel(provider string, m llmconfig.ModelInfo, isDefault bool) codexapp.Model {
	efforts := make([]codexapp.ReasoningEffortOption, 0, len(m.ReasoningLevels))
	for _, level := range m.ReasoningLevels {
		efforts = append(efforts, codexapp.ReasoningEffortOption{ReasoningEffort: level, Description: level + " reasoning effort"})
	}
	defaultEffort := m.DefaultReasoningEffort
	if defaultEffort == "" {
		defaultEffort = "none"
	}
	return codexapp.Model{
		ID:                        m.ID,
		Model:                     m.ID,
		DisplayName:               m.Label,
		Description:               provider,
		IsDefault:                 isDefault,
		DefaultReasoningEffort:    defaultEffort,
		SupportedReasoningEfforts: efforts,
		InputModalities:           []string{"text"},
	}
}

// appServerConversation is one thread: a session conversation plus its save
// identity.
type appServerConversation struct {
	backend   *appServerBackend
	agent     *sessionAgent
	id        string
	ephemeral bool

	nameL sync.Mutex
	name  string
}

var _ codexapp.Conversation = (*appServerConversation)(nil)

func (c *appServerConversation) Name() string {
	c.nameL.Lock()
	defer c.nameL.Unlock()
	return c.name
}

func (c *appServerConversation) SetName(name string) {
	c.nameL.Lock()
	c.name = name
	c.nameL.Unlock()
}

func (c *appServerConversation) Model(ctx context.Context) (string, string, error) {
	model, err := c.agent.llm.Model(ctx)
	if err != nil {
		return "", "", err
	}
	provider, err := c.agent.llm.Provider(ctx)
	if err != nil {
		return model, "", err
	}
	return model, provider, nil
}

func (c *appServerConversation) AgentHandle() string {
	c.agent.agentL.Lock()
	defer c.agent.agentL.Unlock()
	return c.agent.agentHandle
}

func (c *appServerConversation) Prompt(ctx context.Context, input string) error {
	err := c.agent.WithPrompt(ctx, input)
	if errors.Is(err, errAgentInterrupted) {
		return codexapp.ErrInterrupted
	}
	return err
}

func (c *appServerConversation) Steer(input string) bool { return c.agent.Submit(input) }

func (c *appServerConversation) Interrupt() bool { return c.agent.Interrupt() }

func (c *appServerConversation) LastReply(ctx context.Context) (string, error) {
	return c.agent.llm.LastReply(ctx)
}

func (c *appServerConversation) History(ctx context.Context) ([]codexapp.Turn, error) {
	id, err := c.agent.llm.ID(ctx)
	if err != nil {
		return nil, err
	}
	return llmHistoryTurns(ctx, c.backend.dag, id)
}

func (c *appServerConversation) PendingDiff(ctx context.Context) (string, error) {
	baseline := c.agent.lastSynced()
	if baseline == nil || c.agent.llm == nil {
		return "", nil
	}
	changes := c.agent.llm.Workspace().Changes(dagger.WorkspaceChangesOpts{From: baseline})
	return changes.AsPatch().Contents(ctx)
}

func (c *appServerConversation) ExportChanges(ctx context.Context) error {
	return c.agent.ExportChanges(ctx)
}

const llmUsageQuery = `query LLMUsage($id: LLMID!) {
  loadLLMFromID(id: $id) {
    contextWindow
    tokenUsage {
      inputTokens
      outputTokens
      cachedTokenReads
      cachedTokenWrites
    }
  }
}`

func (c *appServerConversation) TokenUsage(ctx context.Context) (codexapp.TokenUsageBreakdown, int64, error) {
	id, err := c.agent.llm.ID(ctx)
	if err != nil {
		return codexapp.TokenUsageBreakdown{}, 0, err
	}
	var res struct {
		LoadLLMFromID struct {
			ContextWindow int64
			TokenUsage    struct {
				InputTokens       int64
				OutputTokens      int64
				CachedTokenReads  int64
				CachedTokenWrites int64
			}
		}
	}
	if err := c.backend.dag.Do(ctx, &dagger.Request{
		Query:     llmUsageQuery,
		OpName:    "LLMUsage",
		Variables: map[string]any{"id": string(id)},
	}, &dagger.Response{Data: &res}); err != nil {
		return codexapp.TokenUsageBreakdown{}, 0, err
	}
	u := res.LoadLLMFromID.TokenUsage
	// Codex counts cached tokens within the input total; Dagger keeps them
	// apart, so fold them back in.
	input := u.InputTokens + u.CachedTokenReads + u.CachedTokenWrites
	return codexapp.TokenUsageBreakdown{
		InputTokens:       input,
		CachedInputTokens: u.CachedTokenReads,
		OutputTokens:      u.OutputTokens,
		TotalTokens:       input + u.OutputTokens,
	}, res.LoadLLMFromID.ContextWindow, nil
}

const llmHistoryQuery = `query LLMHistory($id: LLMID!) {
  loadLLMFromID(id: $id) {
    messages {
      role
      content {
        kind
        text
        toolName
        arguments
        callId
        errored
      }
    }
  }
}`

// llmHistoryTurns renders a conversation's message history as protocol turns:
// each user prompt opens a turn, and the assistant blocks that follow are its
// items, with tool results folded into the calls they answer.
func llmHistoryTurns(ctx context.Context, dag *dagger.Client, id dagger.ID) ([]codexapp.Turn, error) {
	var res struct {
		LoadLLMFromID struct {
			Messages []struct {
				Role    string
				Content []struct {
					Kind      string
					Text      string
					ToolName  string
					Arguments string
					CallID    string
					Errored   bool
				}
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     llmHistoryQuery,
		OpName:    "LLMHistory",
		Variables: map[string]any{"id": string(id)},
	}, &dagger.Response{Data: &res}); err != nil {
		return nil, err
	}

	turns := []codexapp.Turn{}
	calls := map[string]*codexapp.McpToolCallItem{}
	var current *codexapp.Turn
	flush := func() {
		if current != nil {
			turns = append(turns, *current)
			current = nil
		}
	}
	for _, msg := range res.LoadLLMFromID.Messages {
		switch msg.Role {
		case string(dagger.LLMMessageRoleUser):
			var text []string
			for _, block := range msg.Content {
				switch block.Kind {
				case string(dagger.LLMContentBlockKindText):
					text = append(text, block.Text)
				case string(dagger.LLMContentBlockKindToolResult):
					if call, ok := calls[block.CallID]; ok {
						if result := strings.TrimSpace(block.Text); result != "" {
							call.Result = codexapp.TextResult(result)
						}
						if block.Errored {
							call.Status = codexapp.ToolCallStatusFailed
							call.Error = &codexapp.McpToolCallError{Message: strings.TrimSpace(block.Text)}
						}
					}
				}
			}
			if len(text) == 0 {
				continue
			}
			flush()
			current = &codexapp.Turn{
				ID:     codexapp.NewID(),
				Status: codexapp.TurnStatusCompleted,
				Items: []codexapp.ThreadItem{
					codexapp.NewUserMessageItem(codexapp.NewID(), []codexapp.UserInput{codexapp.TextInput(strings.Join(text, "\n"))}),
				},
			}
		case string(dagger.LLMMessageRoleAssistant):
			if current == nil {
				current = &codexapp.Turn{ID: codexapp.NewID(), Status: codexapp.TurnStatusCompleted, Items: []codexapp.ThreadItem{}}
			}
			for _, block := range msg.Content {
				switch block.Kind {
				case string(dagger.LLMContentBlockKindText):
					current.Items = append(current.Items, codexapp.NewAgentMessageItem(codexapp.NewID(), block.Text))
				case string(dagger.LLMContentBlockKindThinking):
					current.Items = append(current.Items, codexapp.NewReasoningItem(codexapp.NewID(), []string{block.Text}))
				case string(dagger.LLMContentBlockKindToolCall):
					call := &codexapp.McpToolCallItem{
						Type:      codexapp.ItemTypeMcpToolCall,
						ID:        codexapp.NewID(),
						Server:    codexapp.DaggerToolServer,
						Tool:      block.ToolName,
						Arguments: codexapp.ToolArguments(block.Arguments),
						Status:    codexapp.ToolCallStatusCompleted,
					}
					calls[block.CallID] = call
					current.Items = append(current.Items, call)
				}
			}
		}
	}
	flush()
	return turns, nil
}
