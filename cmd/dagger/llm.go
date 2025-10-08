package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/bubbles/key"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/syntax"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core/openrouter"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
)

type interpreterMode int

const (
	modeUnset interpreterMode = iota
	modePrompt
	modeShell
)

func (m interpreterMode) String() string {
	switch m {
	case modeUnset:
		return "unset"
	case modePrompt:
		return "prompt"
	case modeShell:
		return "shell"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}

func (m interpreterMode) ContentType() string {
	switch m {
	case modeShell:
		return "text/x-shellscript"
	case modePrompt:
		return "text/markdown"
	default:
		return "text/plain"
	}
}

type LLMSession struct {
	// undo       *LLMSession
	dag        *dagger.Client
	llm        *dagger.LLM
	models     openrouter.Models
	model      string
	skipEnv    map[string]bool
	syncedVars map[string]digest.Digest
	shell      *shellCallHandler

	beforeFS     *dagger.Directory
	beforeFSTime time.Time
	afterFS      *dagger.Directory

	plumbingCtx  context.Context
	plumbingSpan trace.Span
}

func NewLLMSession(ctx context.Context, dag *dagger.Client, llmModel string, shellHandler *shellCallHandler) (*LLMSession, error) {
	s := &LLMSession{
		dag:        dag,
		model:      llmModel,
		syncedVars: map[string]digest.Digest{},
		skipEnv: map[string]bool{
			// these vars are set by the sh package
			"GID":    true,
			"UID":    true,
			"EUID":   true,
			"OPTIND": true,
			"IFS":    true,
			// the rest should be filtered out already by skipping the first batch
			// (sourced from os.Environ)
		},
		shell: shellHandler,
	}

	// Allocate a span to tuck all the internal plumbing into, so it doesn't
	// clutter the top-level prior to receiving the Revealed spans
	s.plumbingCtx, s.plumbingSpan = Tracer().Start(ctx, "LLM plumbing", telemetry.Internal())

	// TODO: cache this
	models, err := openrouter.FetchModels(ctx)
	if err != nil {
		return nil, err
	}
	s.models = models

	// don't sync the initial env vars
	for k := range shellHandler.runner.Env.Each {
		s.skipEnv[k] = true
	}

	s.reset()

	return s, nil
}

func (s *LLMSession) reset() {
	s.updateLLMAndAgentVar(
		s.dag.LLM(dagger.LLMOpts{Model: s.model}).
			WithEnv(s.dag.Env(dagger.EnvOpts{
				Privileged: true,
				Writable:   true,
			})))
}

func (s *LLMSession) Fork() *LLMSession {
	// FIXME: this was a half-baked feature, currently does more harm than good
	// because we lose partial progress on interrupt
	//
	// see https://github.com/dagger/dagger/pull/10765
	return s
	// cp := *s
	// cp.undo = s
	// return &cp
}

func (s *LLMSession) WithPrompt(ctx context.Context, input string) (*LLMSession, error) {
	s = s.Fork()

	if err := s.syncVarsToLLM(); err != nil {
		return s, err
	}

	resolvedModel, err := s.llm.Model(s.plumbingCtx)
	if err != nil {
		return nil, err
	}
	s.model = resolvedModel

	prompted := s.llm.WithPrompt(input)

	for {
		// update the sidebar after every step, not after the entire loop
		prompted, err = prompted.Step(ctx)
		if err != nil {
			return s, err
		}

		s.updateLLMAndAgentVar(prompted)

		if err := s.updateSidebar(prompted); err != nil {
			return s, err
		}

		hasMore, err := prompted.HasPrompt(s.plumbingCtx)
		if err != nil {
			return s, err
		}
		if !hasMore {
			break
		}
	}

	if err := s.syncVarsFromLLM(); err != nil {
		return s, err
	}

	return s, nil
}

var dbg *log.Logger

func init() {
	if fn := os.Getenv("DAGUI_DEBUG"); fn != "" {
		debugFile, _ := os.Create(fn)
		dbg = log.New(debugFile, "", 0)
	} else {
		dbg = log.New(io.Discard, "", 0)
	}
}

const (
	agentVar     = "agent"
	lastValueVar = "_"
)

func (s *LLMSession) updateLLMAndAgentVar(llm *dagger.LLM) error {
	s.llm = llm

	ctx := s.plumbingCtx

	// figure out what the model resolved to
	model, err := s.llm.Model(ctx)
	if err != nil {
		return err
	}
	s.model = model

	if err := s.assignShell(ctx, agentVar, s.llm); err != nil {
		return err
	}
	return nil
}

func (s *LLMSession) updateSidebar(llm *dagger.LLM) error {
	inputTokens, err := llm.TokenUsage().InputTokens(s.plumbingCtx)
	if err != nil {
		return err
	}
	outputTokens, err := llm.TokenUsage().OutputTokens(s.plumbingCtx)
	if err != nil {
		return err
	}
	cacheReads, err := llm.TokenUsage().CachedTokenReads(s.plumbingCtx)
	if err != nil {
		return err
	}
	cacheWrites, err := llm.TokenUsage().CachedTokenWrites(s.plumbingCtx)
	if err != nil {
		return err
	}
	lines := []string{
		termenv.String(s.model).Foreground(termenv.ANSIMagenta).Bold().String(),
	}

	if opts.Verbosity > dagui.ShowInternalVerbosity {
		if inputTokens > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Tokens In: ",
					inputTokens))
		}
		if outputTokens > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Tokens Out:",
					outputTokens))
		}
		if cacheReads > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Cache Reads: ",
					cacheReads))
		}
		if cacheWrites > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Cache Writes:",
					cacheWrites))
		}
	}

	if m := s.models.Lookup(s.model); m != nil {
		inputCost := m.Pricing.Prompt.Cost(inputTokens)
		outputCost := m.Pricing.Completion.Cost(outputTokens)
		cacheReadCost := m.Pricing.InputCacheRead.Cost(cacheReads)
		cacheWriteCost := m.Pricing.InputCacheWrite.Cost(cacheWrites)
		totalCost := inputCost + outputCost + cacheReadCost + cacheWriteCost
		if totalCost > 0 {
			contextUsage := int(float64(inputTokens) / float64(m.ContextLength) * 100)
			contextStyle := termenv.String("%d%%").Bold()
			if contextUsage > 80 {
				contextStyle = contextStyle.Foreground(termenv.ANSIYellow)
			}
			if contextUsage > 90 {
				contextStyle = contextStyle.Foreground(termenv.ANSIRed)
			}
			if contextUsage > 100 {
				contextStyle = contextStyle.Foreground(termenv.ANSIBrightRed)
			}
			lines = append(lines,
				fmt.Sprintf("Cost: "+termenv.String("$%0.2f").Bold().String(),
					totalCost),
				fmt.Sprintf("Context: "+contextStyle.String(),
					contextUsage),
			)
		}
	}

	Frontend.SetSidebarContent(idtui.SidebarSection{
		Title:   "LLM",
		Content: strings.Join(lines, "\n"),
	})

	if s.beforeFS == nil {
		s.beforeFS = s.llm.Env().Workspace()
		s.beforeFSTime = time.Now()
	}

	s.afterFS = llm.Env().Workspace()

	dirDiff := s.afterFS.Changes(s.beforeFS)

	preview, err := idtui.PreviewPatch(s.plumbingCtx, dirDiff)
	if err != nil {
		return err
	}

	if preview != nil {
		Frontend.SetSidebarContent(idtui.SidebarSection{
			Title: "Changes",
			ContentFunc: func(width int) string {
				var buf strings.Builder
				out := idtui.NewOutput(&buf)
				if err := preview.Summarize(out, width); err != nil {
					return "ERROR: " + err.Error()
				}
				return buf.String()
			},
			KeyMap: []key.Binding{
				key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "sync")),
			},
		})
	}

	return err
}

func (s *LLMSession) syncVarsToLLM() error {
	ctx := s.plumbingCtx

	// TODO: overlay? bad scaling characteristics. maybe overkill anyway
	oldVars := s.syncedVars
	s.syncedVars = make(map[string]digest.Digest)
	maps.Copy(s.syncedVars, oldVars)

	if value, ok := s.shell.runner.Vars[agentVar]; ok {
		if key := GetStateKey(value.String()); key != "" {
			st, err := s.shell.state.Load(key)
			if err != nil {
				return err
			}
			// NB: don't need to use updateLLMAndAgentVar here, since this is coming
			// from the agent var
			s.llm = s.llm.WithGraphQLQuery(st.QueryBuilder(s.dag))
		}
	}

	syncedEnvQ := s.dag.QueryBuilder().
		Select("loadEnvFromID").
		Arg("id", s.llm.Env())

	var changed bool
	for name, value := range s.shell.runner.Vars {
		if name == agentVar {
			// handled separately
			continue
		}
		if name == lastValueVar {
			// don't sync the auto-last-value var back to the LLM
			continue
		}
		if s.skipEnv[name] {
			continue
		}

		if s.syncedVars[name] == dagql.HashFrom(value.String()) {
			continue
		}

		dbg.Printf("syncing var %q => llm env\n", name)

		changed = true

		if key := GetStateKey(value.String()); key != "" {
			st, err := s.shell.state.Load(key)
			if err != nil {
				return err
			}
			q := st.QueryBuilder(s.dag)
			modDef := s.shell.GetDef(st)
			typeDef, err := st.GetTypeDef(modDef)
			if err != nil {
				return err
			}
			if typeDef.AsFunctionProvider() != nil {
				var id string
				if err := q.Select("id").Bind(&id).Execute(ctx); err != nil {
					return err
				}
				digest, err := idDigest(id)
				if err != nil {
					return err
				}
				typeName := typeDef.Name()
				syncedEnvQ = syncedEnvQ.
					Select(fmt.Sprintf("with%sInput", typeName)).
					Arg("name", name).
					Arg("description", ""). // TODO
					Arg("value", id)
				s.syncedVars[name] = digest
			}
		} else {
			s.syncedVars[name] = dagql.HashFrom(value.String())
			syncedEnvQ = syncedEnvQ.
				Select("withStringInput").
				Arg("name", name).
				Arg("description", ""). // TODO
				Arg("value", value.String())
		}
	}
	if !changed {
		return nil
	}
	var envID dagger.EnvID
	if err := syncedEnvQ.Select("id").Bind(&envID).Execute(ctx); err != nil {
		return err
	}
	s.updateLLMAndAgentVar(s.llm.WithEnv(s.dag.LoadEnvFromID(envID)))
	return nil
}

func (s *LLMSession) syncVarsFromLLM() error {
	ctx := s.plumbingCtx

	outputs, err := s.llm.Env().Outputs(ctx)
	if err != nil {
		return err
	}

	assign := func(bnd *dagger.Binding) error {
		name, err := bnd.Name(ctx)
		if err != nil {
			return err
		}
		typeName, err := bnd.TypeName(ctx)
		if err != nil {
			return err
		}
		isNull, err := bnd.IsNull(ctx)
		if err != nil {
			return err
		}
		if isNull {
			return nil
		}
		switch typeName {
		case "", "Query", "Void":
			return nil
		case "String":
			str, err := bnd.AsString(ctx)
			if err != nil {
				return err
			}
			s.assignShellString(ctx, name, str)
			return nil
		default:
			var objID string
			if err :=
				s.dag.QueryBuilder().
					Select("loadBindingFromID").
					Arg("id", bnd).
					Select("as" + typeName).
					Select("id").
					Bind(&objID).
					Execute(ctx); err != nil {
				return err
			}
			return s.assignShell(ctx, name, &dynamicObject{objID, typeName})
		}
	}

	// assign all outputs
	for _, output := range outputs {
		if err := assign(&output); err != nil {
			return err
		}
	}

	// assign last value
	return assign(s.llm.BindResult(lastValueVar))
}

type dagqlObject interface {
	XXX_GraphQLType() string
	XXX_GraphQLID(context.Context) (string, error)
}

type dynamicObject struct {
	id       string
	typeName string
}

func (do *dynamicObject) XXX_GraphQLType() string { //nolint:staticcheck
	return do.typeName
}

func (do *dynamicObject) XXX_GraphQLID(ctx context.Context) (string, error) { //nolint:staticcheck
	return do.id, nil
}

func (s *LLMSession) assignShell(ctx context.Context, name string, idable dagqlObject) error {
	val, err := s.toShell(ctx, idable)
	if err != nil {
		return err
	}
	s.assignShellString(ctx, name, val)
	return nil
}

func (s *LLMSession) assignShellString(ctx context.Context, name string, val string) {
	if len(val) > 100 {
		slog.Debug("value is too long", "name", name, "value", val)
		return
	}
	quot, err := syntax.Quote(val, syntax.LangBash)
	if err != nil {
		slog.Error("failed to quote value", "name", name, "value", val, "error", err.Error())
		return
	}
	if err := s.shell.Eval(ctx, fmt.Sprintf("%s=%s", name, quot)); err != nil {
		slog.Error("failed to assign value", "name", name, "quoted", quot, "error", err.Error())
	}
}

func (s *LLMSession) toShell(ctx context.Context, idable dagqlObject) (string, error) {
	typeName := idable.XXX_GraphQLType()
	objID, err := idable.XXX_GraphQLID(ctx)
	if err != nil {
		return "", err
	}
	st := ShellState{
		Calls: []FunctionCall{
			{
				Object: "Query",
				Name:   "load" + typeName + "FromID",
				Arguments: map[string]any{
					"id": objID,
				},
				ReturnObject: typeName,
			},
		},
	}
	key := s.shell.state.Store(st)
	return newStateToken(key), nil
}

func (s *LLMSession) Clear() *LLMSession {
	s = s.Fork()
	s.reset()
	return s
}

var compact = `Please summarize our conversation so far into a concise context that:

1. Preserves all critical information including:
   - Key questions asked and answers provided
   - Important code snippets and their purposes
   - Project structure and technical details
   - Decisions made and rationales

2. Condenses or removes:
   - Verbose explanations
   - Redundant information
   - Preliminary explorations that didn't lead anywhere
   - Courtesy exchanges and non-technical chat

3. Maintains awareness of file changes:
   - Track which files have been viewed, created, or modified
   - Remember the current state of important files
   - Preserve knowledge of project structure

4. Formats the summary in a structured way:
   - Project context (language, framework, objectives)
   - Current task status
   - Key technical details discovered
   - Next steps or pending questions

Present this summary in a compact form that retains all essential context needed to continue our work effectively, then continue our conversation from this point forward as if we had the complete conversation history.

This will be a note to yourself, not shown to the user, so prioritize your own understanding and don't ask any questions, because they won't be seen by anyone.
`

func (s *LLMSession) Compact(ctx context.Context) (_ *LLMSession, rerr error) {
	ctx, span := Tracer().Start(ctx, "compact", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })
	summary, err := s.llm.WithPrompt(compact).LastReply(ctx)
	if err != nil {
		return s, err
	}
	fresh := s.dag.LLM(dagger.LLMOpts{
		Model: s.model,
	})
	compacted, err := fresh.WithPrompt(summary).Sync(ctx)
	if err != nil {
		return s, err
	}
	s = s.Fork()
	s.updateLLMAndAgentVar(compacted)
	return s, nil
}

func (s *LLMSession) History(ctx context.Context) (*LLMSession, error) {
	history, err := s.llm.History(ctx)
	if err != nil {
		return s, err
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	for _, h := range history {
		fmt.Fprintln(stdio.Stdout, h)
	}
	return s, nil
}

func (s *LLMSession) Model(model string) (*LLMSession, error) {
	s = s.Fork()
	s.updateLLMAndAgentVar(s.llm.WithModel(model))
	model, err := s.llm.Model(s.plumbingCtx)
	if err != nil {
		return nil, err
	}
	s.model = model
	return s, nil
}

func (s *LLMSession) SyncFromLocal(ctx context.Context) (rerr error) {
	if s.llm == nil {
		return fmt.Errorf("no LLM session active")
	}

	if s.beforeFSTime.IsZero() {
		return nil
	}

	ctx, span := Tracer().Start(ctx, "syncing local changes",
		telemetry.Reveal())
	defer telemetry.End(span, func() error { return rerr })
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)

	var pathsToUpload []string

	// Look for paths modified since the last sync, and only upload those.
	if err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// ignore errors
			return err
		}
		if d.IsDir() {
			// nothing to do for directories
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(s.beforeFSTime) {
			slog.Info(
				"file changed since last sync",
				"path", path,
				"syncTime", s.beforeFSTime,
				"mtime", info.ModTime(),
			)
			pathsToUpload = append(pathsToUpload, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk looking for modifications: %w", err)
	}

	// The model's workspace has its own changes since last sync, so if we're
	// syncing from local, we need to revert them
	if s.afterFS != nil && s.beforeFS != nil {
		changes := s.afterFS.Changes(s.beforeFS)
		modified, err := changes.ModifiedPaths(ctx)
		if err != nil {
			return err
		}
		removed, err := changes.RemovedPaths(ctx)
		if err != nil {
			return err
		}
		if len(modified) > 0 {
			slog.Info("reverting LLM-modified files", "modified", modified)
			pathsToUpload = append(pathsToUpload, modified...)
		}
		if len(removed) > 0 {
			slog.Info("restoring LLM-removed files", "removed", removed)
			pathsToUpload = append(pathsToUpload, removed...)
		}
	}

	if len(pathsToUpload) == 0 {
		slog.Warn("no changes detected")
		return nil
	}

	slog.Info("syncing changed files", "paths", pathsToUpload)

	localChanges, err := s.dag.Host().Directory(".", dagger.HostDirectoryOpts{
		Include:   pathsToUpload,
		NoCache:   true,
		Gitignore: true,
	}).Sync(ctx)
	if err != nil {
		return nil
	}

	currentFS := s.afterFS
	if currentFS == nil {
		currentFS = s.beforeFS
	}

	withChanges := currentFS.WithDirectory(".", localChanges)

	newLLM := s.llm.WithEnv(
		s.llm.Env().WithWorkspace(withChanges),
	)

	dirDiff := withChanges.Changes(currentFS)

	// Add an LLM prompt as a cue to the model so it knows what files changed.
	preview, err := idtui.PreviewPatch(s.plumbingCtx, dirDiff)
	if err != nil {
		return err
	}

	var buf strings.Builder
	out := termenv.NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	if err := preview.Summarize(out, 80); err != nil {
		slog.Warn("failed to summarize uploaded changes", "error", err)
	} else {
		newLLM = newLLM.WithPrompt(
			fmt.Sprintf("I have made the following changes:\n\n```\n%s\n```", buf.String()),
		)
	}

	// Display a patch so the user can verify what changes got uploaded.
	patch, err := dirDiff.AsPatch().Contents(ctx)
	if err != nil {
		return fmt.Errorf("get patch: %w", err)
	}
	tokens, err := lexers.Get("diff").Tokenise(nil, patch)
	if err != nil {
		return err
	}
	if err := formatters.TTY16.Format(stdio.Stdout, idtui.TTYStyle(), tokens); err != nil {
		return err
	}

	s.updateLLMAndAgentVar(newLLM)

	// reset before/after state
	s.beforeFS = withChanges
	s.beforeFSTime = time.Now()
	s.afterFS = nil

	// Update sidebar to show sync success
	Frontend.SetSidebarContent(idtui.SidebarSection{
		Title:   "Changes",
		Content: "", // empty content will hide it
	})

	return nil
}

func (s *LLMSession) SyncToLocal(ctx context.Context) error {
	if s.llm == nil {
		return fmt.Errorf("no LLM session active")
	}

	if s.afterFS == nil {
		return fmt.Errorf("nothing to sync")
	}

	if _, err := s.afterFS.Changes(s.beforeFS).Export(ctx, "."); err != nil {
		return err
	}

	s.beforeFS = s.afterFS
	s.beforeFSTime = time.Now()

	// Update sidebar to show sync success
	Frontend.SetSidebarContent(idtui.SidebarSection{
		Title:   "Changes",
		Content: "", // empty content will hide it
	})

	return nil
}

// sessionMetadata stores metadata about a saved LLM session
type sessionMetadata struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	Timestamp string `json:"timestamp"`
}

// getSessionDir returns the directory where LLM sessions are stored, creating it if necessary
func getSessionDir() (string, error) {
	// Use XDG_STATE_HOME if set, otherwise fall back to ~/.local/state
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		stateHome = filepath.Join(homeDir, ".local", "state")
	}

	sessionDir := filepath.Join(stateHome, "dagger", "llm-sessions")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create session directory: %w", err)
	}

	return sessionDir, nil
}

// SaveSession saves the current LLM session to disk
func (s *LLMSession) SaveSession(ctx context.Context, name string) error {
	if s.llm == nil {
		return fmt.Errorf("no LLM session to save")
	}

	sessionDir, err := getSessionDir()
	if err != nil {
		return err
	}

	// Get the LLM ID
	llmID, err := s.llm.ID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get LLM ID: %w", err)
	}

	// Create session metadata
	metadata := sessionMetadata{
		Name:      name,
		Model:     s.model,
		Timestamp: fmt.Sprintf("%d", time.Now().Unix()),
	}

	// Save the session ID
	sessionFile := filepath.Join(sessionDir, name+".json")
	data := map[string]interface{}{
		"id":       string(llmID),
		"metadata": metadata,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session data: %w", err)
	}

	if err := os.WriteFile(sessionFile, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	slog.Debug("saved LLM session", "name", name, "file", sessionFile)
	return nil
}

// AutoSaveSession automatically saves the session with an LLM-generated name
func (s *LLMSession) AutoSaveSession(ctx context.Context) error {
	if s.llm == nil {
		return fmt.Errorf("no LLM session to save")
	}

	// Generate a session name using a lightweight LLM
	name, err := s.generateSessionName(ctx)
	if err != nil {
		return fmt.Errorf("failed to generate session name: %w", err)
	}

	return s.SaveSession(ctx, name)
}

// generateSessionName uses a lightweight LLM to generate a session name based on conversation history
func (s *LLMSession) generateSessionName(ctx context.Context) (string, error) {
	// Get conversation history
	history, err := s.llm.History(s.plumbingCtx)
	if err != nil {
		return "", fmt.Errorf("failed to get conversation history: %w", err)
	}

	if len(history) == 0 {
		return "empty-session", nil
	}

	// Get the current provider to select an appropriate lightweight model
	provider, err := s.llm.Provider(s.plumbingCtx)
	if err != nil {
		return "", fmt.Errorf("failed to get LLM provider: %w", err)
	}

	// Select a lightweight model based on the provider
	var lightweightModel string
	switch provider {
	case "anthropic":
		lightweightModel = "claude-3-5-haiku-20241022"
	case "openai":
		lightweightModel = "gpt-4o-mini"
	case "google":
		lightweightModel = "gemini-2.0-flash-thinking-exp-1219"
	case "meta":
		lightweightModel = "llama-3.2-3b-instruct"
	case "mistral":
		lightweightModel = "mistral-small-latest"
	case "deepseek":
		lightweightModel = "deepseek-chat"
	default:
		return "", fmt.Errorf("unsupported provider: %s", provider)
	}

	slog.Debug("using lightweight model for session naming", "provider", provider, "model", lightweightModel)

	// Use a small, fast model for name generation
	namingLLM := s.dag.LLM(dagger.LLMOpts{
		Model: lightweightModel,
	})

	// Build a compact summary of the conversation for the naming prompt
	// Take the first few and last few messages to keep context small
	var historySnippet strings.Builder
	maxMessages := 6 // First 3 and last 3 messages
	if len(history) <= maxMessages {
		for _, msg := range history {
			historySnippet.WriteString(msg)
			historySnippet.WriteString("\n\n")
		}
	} else {
		// First 3 messages
		for i := 0; i < 3; i++ {
			historySnippet.WriteString(history[i])
			historySnippet.WriteString("\n\n")
		}
		historySnippet.WriteString("...\n\n")
		// Last 3 messages
		for i := len(history) - 3; i < len(history); i++ {
			historySnippet.WriteString(history[i])
			historySnippet.WriteString("\n\n")
		}
	}

	prompt := fmt.Sprintf(`Based on this conversation history, generate a short, descriptive session name (2-5 words, lowercase with hyphens, no special characters except hyphens).
The name should capture the main topic or task being discussed.

Example good names:
- "fix-docker-build-error"
- "add-user-authentication"
- "debug-memory-leak"
- "implement-caching-layer"

Conversation history:
%s

Respond with ONLY the session name, nothing else.`, historySnippet.String())

	// Generate the name
	nameReply, err := namingLLM.WithPrompt(prompt).LastReply(s.plumbingCtx)
	if err != nil {
		return "", fmt.Errorf("failed to generate name: %w", err)
	}

	// Clean up the name
	name := strings.TrimSpace(nameReply)
	name = strings.ToLower(name)
	// Remove any quotes or extra characters
	name = strings.Trim(name, "\"'`\n\r ")
	// Replace spaces with hyphens
	name = strings.ReplaceAll(name, " ", "-")
	// Remove any non-alphanumeric characters except hyphens
	var cleaned strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	name = cleaned.String()

	// Ensure the name isn't too long
	if len(name) > 50 {
		name = name[:50]
	}

	// Ensure the name isn't empty
	if name == "" {
		name = fmt.Sprintf("session-%d", time.Now().Unix())
	}

	slog.Debug("generated session name", "name", name)
	return name, nil
}

// LoadSession loads an LLM session from disk
func (s *LLMSession) LoadSession(ctx context.Context, name string) error {
	sessionDir, err := getSessionDir()
	if err != nil {
		return err
	}

	sessionFile := filepath.Join(sessionDir, name+".json")
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %q not found", name)
		}
		return fmt.Errorf("failed to read session file: %w", err)
	}

	var sessionData map[string]any
	if err := json.Unmarshal(data, &sessionData); err != nil {
		return fmt.Errorf("failed to unmarshal session data: %w", err)
	}

	llmID, ok := sessionData["id"].(string)
	if !ok || llmID == "" {
		return fmt.Errorf("invalid session data: missing or invalid id")
	}

	// Load the LLM from its ID
	loadedLLM := s.dag.LoadLLMFromID(dagger.LLMID(llmID))
	s.updateLLMAndAgentVar(loadedLLM)
	s.updateSidebar(loadedLLM)

	slog.Debug("loaded LLM session", "name", name, "file", sessionFile)
	return nil
}

// ListSessions returns a list of saved session names
func ListSessions() ([]string, error) {
	sessionDir, err := getSessionDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read session directory: %w", err)
	}

	var sessions []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			name := strings.TrimSuffix(entry.Name(), ".json")
			sessions = append(sessions, name)
		}
	}

	return sessions, nil
}
