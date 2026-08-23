package core

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/util/patchpreview"
	telemetry "github.com/dagger/otel-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// A frontend for LLM tool calling
type LLMTool struct {
	// Tool name
	Name string `json:"name"`
	// Name of the tool's provider, if any: the MCP server for MCP tools, or the
	// bound object's type name for object tools. Only a name registered in
	// MCP.mcpServers routes calls through MCP server syncing; otherwise it's
	// display metadata (telemetry's tool-server attribute).
	Server string
	// Tool description
	Description string `json:"description"`
	// Tool argument schema. Key is argument name. Value is unmarshalled json-schema for the argument.
	Schema map[string]any `json:"schema"`
	// Whether the tool schema is strict.
	// https://platform.openai.com/docs/guides/structured-outputs?api-mode=chat
	Strict bool `json:"-"`
	// Whether we should hide the LLM tool call span in favor of just showing its
	// child spans.
	HideSelf bool `json:"-"`
	// Whether the tool is read-only (from MCP ReadOnlyHint annotation)
	ReadOnly bool `json:"-"`
	// Whether the tool returns a Changeset. Changeset-returning tools can execute
	// in parallel against the same workspace; CallBatch merges their results
	// before updating the workspace.
	ReturnsChangeset bool `json:"-"`
	// GraphQL API field that this tool corresponds to
	Field *ast.FieldDefinition `json:"-"`
	// Function implementing the tool.
	Call LLMToolFunc `json:"-"`
}

type LLMToolFunc = func(context.Context, any) (any, error)

type LLMToolSet = dagui.OrderedSet[string, LLMTool]

func NewLLMToolSet() *LLMToolSet {
	return dagui.NewOrderedSet[string, LLMTool](func(t LLMTool) string {
		return t.Name
	})
}

// Internal implementation of the MCP standard,
// for exposing a Dagger environment to a LLM via tool calling.
type MCP struct {
	// workspace is the Workspace the LLM is bound to, if any. It is the source of
	// the LLM's schema (MCP.Server) and the target of workspace-mutating tool
	// results (Changeset overlays); the binding also threads the workspace into
	// tool dispatch so contextual (+defaultPath) and Workspace-typed args resolve
	// against it.
	workspace dagql.ObjectResult[*Workspace]
	// boundTools are the objects bound via LLM.withTools. Each eligible method of
	// a bound object becomes a tool; a tool that returns the bound object's own
	// type rebinds it as the new agent state (hack/designs/workspace-agents.md). At most one
	// binding per object type is kept.
	boundTools []boundTool
	// The last value returned by a function.
	lastResult dagql.Typed
	// Indicates that the model has returned
	returned bool
	// skillDirs are skill directories installed via LLM.withSkills, surfaced to
	// the model through ListSkills/ReadSkill alongside the engine-embedded and
	// workspace-discovered skills.
	skillDirs []dagql.ObjectResult[*Directory]
	// selfLLM is the conversation dispatching the current step's tool calls —
	// inst + withResponse, i.e. up to and including the in-flight tool call.
	// step() sets it before CallBatch; MCP.Call threads it into tool dispatch
	// (LLMToContext) so a tool's `LLM!` argument auto-fills with it. Transient:
	// cleared by Clone, never persisted.
	selfLLM dagql.ObjectResult[*LLM]
	// continuation is an LLM returned by a tool during this step (see
	// applyStateReturn / adoptLLM). When set, step() appends the turn's tool
	// results to IT rather than to the LLM that made the call, so the loop
	// resumes from the returned conversation — env, tools, prompts and all.
	// Transient: cleared by Clone.
	continuation dagql.ObjectResult[*LLM]
	// Configured MCP servers.
	mcpServers map[string]*MCPServerConfig
	// Persistent MCP sessions.
	mcpSessions map[string]*mcp.ClientSession
	// Synchronize any concurrent tool call results.
	mu *sync.Mutex
}

// MCPServerConfig represents configuration for an external MCP server
type MCPServerConfig struct {
	// Name of the MCP server
	Name string

	// Command to run the MCP server
	Service dagql.ObjectResult[*Service]
}

func (srv *MCPServerConfig) Dial(ctx context.Context) (_ *mcp.ClientSession, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "start mcp server: "+srv.Name, telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)
	return mcp.NewClient(&mcp.Implementation{
		Title:   "Dagger",
		Version: engine.Version,
	}, nil).Connect(ctx, &ServiceMCPTransport{
		Service: srv.Service,
	}, nil)
}

func newMCP() *MCP {
	return &MCP{
		mcpServers:  make(map[string]*MCPServerConfig),
		mcpSessions: map[string]*mcp.ClientSession{},
		mu:          &sync.Mutex{},
	}
}

func (m *MCP) DefaultSystemPrompt(ctx context.Context) (string, error) {
	// The agent acts through the methods of the objects it's bound to via
	// LLM.withTools (hack/designs/workspace-agents.md), so there is no default harness prompt to
	// teach — each tool is self-describing, and an agent module supplies its own
	// system prompts (e.g. Doug.agent adds provider + reminder prompts).
	return "", nil
}

func (m *MCP) Clone() *MCP {
	cp := *m
	cp.boundTools = slices.Clone(cp.boundTools)
	cp.skillDirs = slices.Clone(cp.skillDirs)
	cp.mcpServers = maps.Clone(cp.mcpServers)
	cp.mcpSessions = maps.Clone(cp.mcpSessions)
	cp.returned = false
	// Per-step scratch state: the dispatching conversation and any continuation
	// a tool returned belong to the step that set them, not to the clone.
	cp.selfLLM = dagql.ObjectResult[*LLM]{}
	cp.continuation = dagql.ObjectResult[*LLM]{}
	cp.mu = &sync.Mutex{}
	return &cp
}

// SetSelfLLM records the conversation dispatching this step's tool calls, so
// MCP.Call can hand it to a tool that declares an `LLM!` argument. Called by
// step() on its transient MCP clone, immediately before CallBatch.
func (m *MCP) SetSelfLLM(llm dagql.ObjectResult[*LLM]) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selfLLM = llm
}

// Continuation returns the LLM a tool returned during this step, if any. step()
// resumes from it instead of the LLM that made the call.
func (m *MCP) Continuation() dagql.ObjectResult[*LLM] {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.continuation
}

// currentLLM returns the conversation dispatching this step's tool calls.
func (m *MCP) currentLLM() dagql.ObjectResult[*LLM] {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selfLLM
}

func (m *MCP) Returned() bool {
	return m.returned
}

func (m *MCP) LastResult() dagql.Typed {
	return m.lastResult
}

// Server returns the GraphQL schema the LLM sees — the schema its Dang scripts
// evaluate against and the schema tools introspect. When the LLM is bound to a
// Workspace (via LLM.withWorkspace), the schema derives from THAT workspace's
// served modules, so the model sees exactly what the Dagger CLI would serve for
// its own workspace, not the outer client's. Absent a binding it falls back to
// the env's served deps.
func (m *MCP) Server(ctx context.Context) (*dagql.Server, error) {
	if m.workspace.Self() != nil {
		return WorkspaceServedSchema(ctx, m.workspace)
	}
	// No workspace bound (e.g. a synthetic context with no current workspace):
	// fall back to the current client's served deps — the same schema the CLI
	// serves.
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	deps, err := query.CurrentServedDeps(ctx)
	if err != nil {
		return nil, err
	}
	return deps.Schema(ctx)
}

func (m *MCP) WithMCPServer(srv *MCPServerConfig) *MCP {
	m = m.Clone()
	m.mcpServers[srv.Name] = srv
	return m
}

// WithSkills installs a directory of skills, discovered via its SKILL.md files
// and surfaced to the model through ListSkills/ReadSkill.
func (m *MCP) WithSkills(dir dagql.ObjectResult[*Directory]) *MCP {
	m = m.Clone()
	m.skillDirs = append(m.skillDirs, dir)
	return m
}

func (m *MCP) Tools(ctx context.Context) ([]LLMTool, error) {
	srv, err := m.Server(ctx)
	if err != nil {
		return nil, err
	}

	allTools := NewLLMToolSet()

	// The LLM acts through the methods of the objects it's bound to via
	// LLM.withTools (hack/designs/workspace-agents.md): each eligible method becomes a tool,
	// and a method that returns the bound object's own type rebinds it as the new
	// state. These are loaded first so a bound method overrides a builtin of the
	// same name. External MCP tools, skills, and the ReadLogs builtin also apply.
	if err := m.loadObjectTools(ctx, srv, allTools); err != nil {
		return nil, err
	}
	if err := m.loadMCPTools(ctx, allTools); err != nil {
		return nil, err
	}
	m.loadSkillTools(srv, allTools)
	m.loadBuiltins(srv, allTools)
	return allTools.Order, nil
}

func (m *MCP) syncMCPSessions(ctx context.Context) error {
	stop := maps.Clone(m.mcpSessions)
	for _, mcpSrv := range m.mcpServers {
		delete(stop, mcpSrv.Name)
		if _, ok := m.mcpSessions[mcpSrv.Name]; ok {
			continue
		}
		sess, err := mcpSrv.Dial(ctx)
		if err != nil {
			return fmt.Errorf("dial mcp %q: %w", mcpSrv.Name, err)
		}
		m.mcpSessions[mcpSrv.Name] = sess
	}
	for name, srv := range stop {
		if err := srv.Close(); err != nil {
			return err
		}
		delete(m.mcpSessions, name)
	}
	return nil
}

func (m *MCP) loadMCPTools(ctx context.Context, allTools *LLMToolSet) error {
	if err := m.syncMCPSessions(ctx); err != nil {
		return err
	}
	for serverName, sess := range m.mcpSessions {
		for tool, err := range sess.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			schema, err := toAny(tool.InputSchema)
			if err != nil {
				return err
			}
			if schema["properties"] == nil {
				// OpenAI is very particular; it wants there to always be properties,
				// even if empty.
				schema["properties"] = map[string]any{}
			}

			// Check if the tool is read-only from MCP annotations
			isReadOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint

			allTools.Add(LLMTool{
				Name:        tool.Name,
				Server:      serverName,
				Description: tool.Description,
				Schema:      schema,
				ReadOnly:    isReadOnly,
				Call: func(ctx context.Context, args any) (any, error) {
					res, err := sess.CallTool(ctx, &mcp.CallToolParams{
						Name:      tool.Name,
						Arguments: args,
					})
					if err != nil {
						return nil, fmt.Errorf("call tool %q on mcp %q: %w", tool.Name, serverName, err)
					}

					var out string
					for _, content := range res.Content {
						switch x := content.(type) {
						case *mcp.TextContent:
							out += x.Text
						default:
							out += fmt.Sprintf("WARNING: unsupported content type %T", x)
						}
					}
					if res.StructuredContent != nil {
						str, err := toolStructuredResponse(res.StructuredContent)
						if err != nil {
							return nil, err
						}
						out += str
					}
					if res.IsError {
						return "", errors.New(out)
					}
					return out, nil
				},
			})
		}
	}
	return nil
}

func (m *MCP) summarizePatch(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) string {
	// Try to return the raw patch so the LLM can see the actual diff.
	// Fall back to a structured summary for large changesets.
	var rawPatch string
	if err := srv.Select(ctx, changes, &rawPatch, dagql.Selector{
		View:  srv.View,
		Field: "asPatch",
	}, dagql.Selector{
		View:  srv.View,
		Field: "contents",
	}); err == nil && rawPatch != "" && strings.Count(rawPatch, "\n") <= 100 {
		return rawPatch
	}

	const summaryWidth = 80

	var stats []*DiffStat
	if err := srv.Select(ctx, changes, &stats, dagql.Selector{
		View:  srv.View,
		Field: "diffStats",
	}); err != nil {
		return fmt.Sprintf("WARNING: failed to fetch patch summary: %s", err)
	}

	entries := make([]patchpreview.Entry, len(stats))
	for i, s := range stats {
		entries[i] = patchpreview.Entry{Path: s.Path, Kind: string(s.Kind), Added: s.AddedLines, Removed: s.RemovedLines}
		if s.OldPath != nil {
			entries[i].OldPath = *s.OldPath
		}
	}
	return patchpreview.SummarizeString(entries, summaryWidth)
}

func toAny(v any) (res map[string]any, rerr error) {
	pl, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return res, json.Unmarshal(pl, &res)
}

// ToolFunc reuses our regular GraphQL args handling sugar for tools.
func ToolFunc[T any](srv *dagql.Server, fn func(context.Context, T) (any, error)) func(context.Context, any) (any, error) {
	return func(ctx context.Context, args any) (any, error) {
		vals, ok := args.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid arguments: %T", args)
		}
		var t T
		specs, err := dagql.InputSpecsForType(t, true)
		if err != nil {
			return nil, err
		}
		inputs := map[string]dagql.Input{}
		for _, spec := range specs.Inputs(srv.View) {
			var input dagql.Input
			if arg, provided := vals[spec.Name]; provided {
				input, err = spec.Type.Decoder().DecodeInput(arg)
				if err != nil {
					return nil, fmt.Errorf("decode arg %q (%+v): %w", spec.Name, arg, err)
				}
			} else if spec.Default != nil {
				input = spec.Default
			} else if spec.Type.Type().NonNull {
				return nil, fmt.Errorf("required argument %s not provided", spec.Name)
			}
			inputs[spec.Name] = input
		}
		if err := specs.Decode(inputs, &t, srv.View); err != nil {
			return nil, err
		}
		return fn(ctx, t)
	}
}

type changesetCaptureKey struct{}

type changesetCapture struct {
	changes dagql.ObjectResult[*Changeset]
}

// applyStateReturn implements the state-mutation convention shared by tool calls
// and Dang eval results. Three kinds of value advance the agent's state:
//
//   - a Changeset overlays onto the bound workspace (via Workspace.withChanges,
//     yielding a new immutable overlay Workspace) so the agent's edits accumulate
//     across turns.
//   - a Workspace *replaces* the bound one — a tool that produces a whole new
//     workspace (e.g. a checkout or install) makes it the agent's current
//     workspace, mirroring the Changeset convention.
//   - an LLM *replaces the conversation*: the tool acts as a continuation and
//     the loop resumes from the returned LLM — its env, tools, system prompts
//     and history — instead of the one that made the call. Since an LLM binds a
//     workspace, this subsumes the Workspace case. See adoptLLM.
//
// Either way it summarizes what changed. step() persists a new workspace via a
// withWorkspace selector, or resumes from the continuation, so the change
// survives history rebuilds. It reports handled=false for any other value so the
// caller can fall through to normal object/scalar output.
func (m *MCP) applyStateReturn(ctx context.Context, srv *dagql.Server, val dagql.Typed) (handled bool, out string, err error) {
	if next, ok := dagql.UnwrapAs[dagql.ObjectResult[*LLM]](val); ok {
		out, err := m.adoptLLM(ctx, next)
		return true, out, err
	}
	if changes, ok := dagql.UnwrapAs[dagql.ObjectResult[*Changeset]](val); ok {
		if capture, ok := ctx.Value(changesetCaptureKey{}).(*changesetCapture); ok {
			capture.changes = changes
			return true, m.summarizePatch(ctx, srv, changes), nil
		}
		if err := m.applyChangeset(ctx, srv, changes); err != nil {
			return true, "", err
		}
		return true, m.summarizePatch(ctx, srv, changes), nil
	}
	if ws, ok := dagql.UnwrapAs[dagql.ObjectResult[*Workspace]](val); ok {
		out, err := m.rebindWorkspace(ctx, srv, ws)
		return true, out, err
	}
	return false, "", nil
}

// rebindWorkspace makes a tool-returned Workspace the LLM's current workspace,
// the sibling of applyChangeset for the replace (rather than overlay) case. It
// summarizes the diff from the previous workspace so the model sees what the tool
// changed, reusing the Changeset patch summary.
func (m *MCP) rebindWorkspace(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*Workspace]) (string, error) {
	prev := m.workspace
	m.workspace = ws
	if prev.Self() == nil {
		// No prior workspace to diff against (e.g. the LLM was unbound); just adopt
		// it without a patch summary.
		return "Set the current workspace.", nil
	}
	before, err := workspaceRoot(ctx, srv, prev)
	if err != nil {
		return "", err
	}
	after, err := workspaceRoot(ctx, srv, ws)
	if err != nil {
		return "", err
	}
	beforeID, err := before.ID()
	if err != nil {
		return "", err
	}
	var changes dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, after, &changes, dagql.Selector{
		View:  srv.View,
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*Directory](beforeID)},
		},
	}); err != nil {
		return "", err
	}
	return m.summarizePatch(ctx, srv, changes), nil
}

// adoptLLM makes a tool-returned LLM the conversation the agent loop resumes
// from — the continuation ring of the state-return convention. The tool is
// handed the current conversation through an auto-injected `LLM!` argument
// (see loadLLMArg), transforms it, and returns the result; step() then appends
// this turn's tool results to the RETURNED LLM instead of the one that made the
// call, so the swap takes effect mid-turn without restarting the session.
//
// ANY LLM may be adopted — there is no lineage gate. This mirrors
// rebindWorkspace, the sibling ring of the same convention: a tool may return
// any workspace at all, and what makes that safe is not prevention but
// VISIBILITY (a patch summary the model reads). Continuations are written by
// the env's author, so refusing a "suspicious" history protects nobody, while
// a lineage rule would block the uses this exists for: self-compaction,
// summarize-and-restart, handing a sub-agent's conversation back.
//
// What remains:
//
//   - eager validation: the returned LLM's env/tools are loaded HERE rather
//     than lazily on the next turn, so e.g. installing a module that fails to
//     load fails the tool call instead of bricking the loop. A failure here is
//     an ordinary failed tool call: the agent survives, the old conversation
//     stands.
//   - one per turn: LLMs do not merge the way Changesets do, so at most one
//     continuation may be adopted per batch of tool calls.
//   - visibility: the string returned here is the model's notice of what
//     changed — which tools came and went, and whether the conversation
//     history itself was replaced. A swap is never silent.
//
// One mechanical detail the caller handles rather than this function: step()
// appends the turn's tool results to the adopted LLM, and a tool-result block
// is only valid where the matching tool call exists in the history. See
// toolResultSelectors in llm.go, which degrades an orphaned result to a plain
// user message.
func (m *MCP) adoptLLM(ctx context.Context, next dagql.ObjectResult[*LLM]) (string, error) {
	if next.Self() == nil {
		return "", fmt.Errorf("cannot continue from a null LLM")
	}
	current, ok := LLMFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("cannot continue: no conversation is bound to this tool call")
	}

	// Load the new toolset now, so a broken env fails the tool call rather than
	// the next turn. Doubles as the material for the summary below.
	after, err := next.Self().mcp.Tools(ctx)
	if err != nil {
		return "", fmt.Errorf("returned LLM's tools failed to load: %w", err)
	}
	before, err := current.Self().mcp.Tools(ctx)
	if err != nil {
		// Non-fatal: without the old toolset we just can't diff it.
		slog.Warn("failed to load current LLM tools for continuation summary", "error", err)
		before = nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.continuation.Self() != nil {
		return "", fmt.Errorf("a conversation-replacing tool call already ran this turn; only one is allowed")
	}
	m.continuation = next
	return summarizeContinuation(current.Self(), next.Self(), before, after), nil
}

// historyPreserved reports whether next's message history still contains
// current's, unchanged, as a prefix — i.e. the conversation was transformed
// (install/reload) rather than replaced.
//
// This is NOT a gate: it is an input to the adoption summary, so the model is
// told when its history changed shape. Comparing histories by VALUE rather
// than chasing the dagql ID chain is deliberate: a Result's ID is an opaque
// runtime handle, and an LLM is usually transformed by being PASSED to
// something (`agents.compose(base: llm)`) rather than received by it, so ID
// ancestry is both awkward to compute and easy to get wrong.
func historyPreserved(current, next *LLM) bool {
	if current == nil || next == nil {
		return false
	}
	if len(next.Messages) < len(current.Messages) {
		return false
	}
	for i, msg := range current.Messages {
		if !messagesEqual(msg, next.Messages[i]) {
			return false
		}
	}
	return true
}

func messagesEqual(a, b *LLMMessage) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Role != b.Role || len(a.Content) != len(b.Content) {
		return false
	}
	for i, blockA := range a.Content {
		blockB := b.Content[i]
		if blockA == blockB {
			continue
		}
		if blockA == nil || blockB == nil {
			return false
		}
		if blockA.Kind != blockB.Kind ||
			blockA.Text != blockB.Text ||
			blockA.CallID != blockB.CallID ||
			blockA.ToolName != blockB.ToolName ||
			string(blockA.Arguments) != string(blockB.Arguments) ||
			blockA.Errored != blockB.Errored ||
			blockA.Signature != blockB.Signature {
			return false
		}
	}
	return true
}

// summarizeContinuation is the model's notice of what a continuation changed:
// the toolset diff, plus a line about the conversation history when it was
// replaced rather than extended. When the history is preserved — the common
// install/reload case — it says nothing extra.
func summarizeContinuation(current, next *LLM, before, after []LLMTool) string {
	summary := summarizeToolsetChange(before, after)
	if current == nil || next == nil || historyPreserved(current, next) {
		return summary
	}
	return summary + "\n" + fmt.Sprintf(
		"Conversation history replaced: %d messages -> %d messages.",
		len(current.Messages), len(next.Messages))
}

// summarizeToolsetChange reports how a continuation changed the agent's
// toolset — the thing the model most needs to know after an install/reload.
func summarizeToolsetChange(before, after []LLMTool) string {
	beforeNames := make(map[string]bool, len(before))
	for _, t := range before {
		beforeNames[t.Name] = true
	}
	afterNames := make(map[string]bool, len(after))
	for _, t := range after {
		afterNames[t.Name] = true
	}
	var added, removed []string
	for _, t := range after {
		if !beforeNames[t.Name] {
			added = append(added, t.Name)
		}
	}
	for _, t := range before {
		if !afterNames[t.Name] {
			removed = append(removed, t.Name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	lines := []string{"Continuing from the returned conversation."}
	if len(added) > 0 {
		lines = append(lines, "Tools added: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		lines = append(lines, "Tools removed: "+strings.Join(removed, ", "))
	}
	if len(added) == 0 && len(removed) == 0 {
		lines = append(lines, fmt.Sprintf("Toolset unchanged (%d tools).", len(after)))
	}
	return strings.Join(lines, "\n")
}

// applyChangeset overlays a Changeset onto the bound workspace and updates
// m.workspace to the new overlay Workspace.
func (m *MCP) applyChangeset(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) error {
	if m.workspace.Self() == nil {
		return fmt.Errorf("cannot apply changes: no workspace bound")
	}
	normalized, err := normalizeChangesetToPatch(ctx, srv, changes)
	if err != nil {
		// Fall back to the raw changeset: normalization is a durability
		// upgrade for saved sessions, not a correctness requirement for the
		// live one.
		slog.Warn("failed to normalize changeset to patch form", "error", err)
		normalized = changes
	}
	changesID, err := normalized.ID()
	if err != nil {
		return fmt.Errorf("get changeset ID: %w", err)
	}
	var newWS dagql.ObjectResult[*Workspace]
	if err := srv.Select(ctx, m.workspace, &newWS, dagql.Selector{
		View:  srv.View,
		Field: "withChanges",
		Args: []dagql.NamedInput{
			{Name: "changes", Value: dagql.NewID[*Changeset](changesID)},
		},
	}); err != nil {
		return err
	}
	m.workspace = newWS
	return nil
}

// normalizeChangesetToPatch rewrites a changeset into pure patch data:
// after = before.withPatch(patch, onConflict: LEAVE_CONFLICT_MARKERS),
// changes = after.changes(from: before).
//
// A tool-built changeset's After is an operation chain (e.g.
// File.withReplaced) rooted at live workspace reads. Replaying those
// operations when a saved session is loaded fails once the files have moved
// on (the search text is gone), or silently re-applies them when it hasn't.
// Capturing the patch now — while the content the operations ran against is
// known — makes the recorded overlay pure data, and its replay a tolerant
// application: hunks that fit apply, hunks that don't leave conflict markers
// for the agent to resolve.
func normalizeChangesetToPatch(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) (dagql.ObjectResult[*Changeset], error) {
	var patchText string
	if err := srv.Select(ctx, changes, &patchText, dagql.Selector{
		View:  srv.View,
		Field: "asPatch",
	}, dagql.Selector{
		View:  srv.View,
		Field: "contents",
	}); err != nil {
		return changes, fmt.Errorf("render changeset as patch: %w", err)
	}
	if patchText == "" {
		return changes, nil
	}
	before := changes.Self().Before
	if before.Self() == nil {
		return changes, fmt.Errorf("changeset has no before directory")
	}
	beforeID, err := before.ID()
	if err != nil {
		return changes, err
	}
	var patched dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, before, &patched, dagql.Selector{
		View:  srv.View,
		Field: "withPatch",
		Args: []dagql.NamedInput{
			{Name: "patch", Value: dagql.NewString(patchText)},
			{Name: "onConflict", Value: PatchConflictLeaveMarkers},
		},
	}); err != nil {
		return changes, fmt.Errorf("apply patch to before: %w", err)
	}
	patched, err = reconcileDirsAfterPatch(ctx, srv, changes, patched)
	if err != nil {
		return changes, fmt.Errorf("reconcile directories: %w", err)
	}
	var normalized dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, patched, &normalized, dagql.Selector{
		View:  srv.View,
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*Directory](beforeID)},
		},
	}); err != nil {
		return changes, fmt.Errorf("rebuild changeset from patch: %w", err)
	}
	return normalized, nil
}

// reconcileDirsAfterPatch restores directory-only changes a git patch cannot
// express. Git tracks files, not directories: an empty directory added by the
// changeset is invisible to `git diff`, so applying the patch to Before
// silently drops it — and since the normalized changeset replaces the original
// on the live workspace binding, the loss would not be confined to the saved
// form. The patch fully covers file content, so the residue between the
// patched tree and the changeset's real After can only be directories; any
// file-level residue means the patch did not reproduce the changeset, and
// normalization is abandoned (the caller falls back to the raw changeset).
func reconcileDirsAfterPatch(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset], patched dagql.ObjectResult[*Directory]) (dagql.ObjectResult[*Directory], error) {
	// Cheap gate: the changeset's own paths are already computed (memoized by
	// asPatch). Directories carry a trailing slash; if none changed, the patch
	// covered everything and there is no residue to look for.
	origPaths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return patched, fmt.Errorf("compute changeset paths: %w", err)
	}
	dirChanged := slices.ContainsFunc(
		slices.Concat(origPaths.Added, origPaths.AllRemoved),
		func(p string) bool { return strings.HasSuffix(p, "/") },
	)
	if !dirChanged {
		return patched, nil
	}

	residual, err := NewChangeset(ctx, patched, changes.Self().After)
	if err != nil {
		return patched, err
	}
	paths, err := residual.ComputePaths(ctx)
	if err != nil {
		return patched, fmt.Errorf("compute patch residue: %w", err)
	}
	if len(paths.Modified) > 0 || len(paths.Renamed) > 0 {
		return patched, fmt.Errorf("patch did not reproduce changeset content: modified %v, renamed %v", paths.Modified, paths.Renamed)
	}
	for _, p := range slices.Concat(paths.Added, paths.AllRemoved) {
		if !strings.HasSuffix(p, "/") {
			return patched, fmt.Errorf("patch did not reproduce changeset file %q", p)
		}
	}
	// Recorded as selectors on the overlay chain, so they are pure data like
	// the patch itself, and replay tolerantly: withNewDirectory is mkdir -p,
	// withoutDirectory ignores an already-missing path.
	for _, dir := range paths.Added {
		if err := srv.Select(ctx, patched, &patched, dagql.Selector{
			View:  srv.View,
			Field: "withNewDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(strings.TrimSuffix(dir, "/"))},
			},
		}); err != nil {
			return patched, fmt.Errorf("restore directory %q: %w", dir, err)
		}
	}
	for _, dir := range paths.Removed {
		if err := srv.Select(ctx, patched, &patched, dagql.Selector{
			View:  srv.View,
			Field: "withoutDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(strings.TrimSuffix(dir, "/"))},
			},
		}); err != nil {
			return patched, fmt.Errorf("drop directory %q: %w", dir, err)
		}
	}
	return patched, nil
}

// workspaceDirectory returns the bound workspace's root directory, for
// operations (like external MCP-server sync) that need a plain Directory.
func (m *MCP) workspaceDirectory(ctx context.Context, srv *dagql.Server) (dagql.ObjectResult[*Directory], error) {
	return workspaceRoot(ctx, srv, m.workspace)
}

// workspaceRoot returns the given workspace's root directory as a plain
// Directory, e.g. for diffing two workspaces.
func workspaceRoot(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*Workspace]) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]
	err := srv.Select(ctx, ws, &dir, dagql.Selector{
		View:  srv.View,
		Field: "directory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(".")},
		},
	})
	return dir, err
}

// applyWorkspaceSnapshot overlays the difference between before and after (the
// pre- and post-run workspace filesystem, e.g. edits made by an external MCP
// server) onto the bound workspace.
func (m *MCP) applyWorkspaceSnapshot(ctx context.Context, srv *dagql.Server, before, after dagql.ObjectResult[*Directory]) error {
	beforeID, err := before.ID()
	if err != nil {
		return err
	}
	var changes dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, after, &changes, dagql.Selector{
		View:  srv.View,
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*Directory](beforeID)},
		},
	}); err != nil {
		return err
	}
	return m.applyChangeset(ctx, srv, changes)
}

func (m *MCP) outputToLLM(ctx context.Context, srv *dagql.Server, val dagql.Typed) (string, error) {
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		// Describe the object (its type + trivial scalar fields) without minting
		// a handle: objects are referenced by the names they're bound to (a `let`
		// within a script, or a WithObject injection), not by a Type#N handle.
		return m.describeObject(ctx, srv, obj)
	}

	result, err := m.sanitizeResult(val)
	if err != nil {
		return "", fmt.Errorf("failed to simplify result: %w", err)
	}

	if str, ok := result.(string); ok {
		// Return string content directly, without wrapping it in JSON.
		return str, nil
	}

	if result == nil {
		// No response; just show logs, if any (handled above).
		return "", nil
	}

	// Handle scalars, arrays, etc
	return toolStructuredResponse(map[string]any{
		"result": result,
	})
}

func (m *MCP) sanitizeResult(val dagql.Typed) (any, error) {
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		// A nested object (e.g. inside a list) has no handle; surface its type
		// name rather than dumping a full ID.
		return obj.Type().Name(), nil
	}

	if anyRes, ok := dagql.UnwrapAs[dagql.AnyResult](val); ok {
		// Unwrap any Result[T]s so we don't encode a giant ID
		return m.sanitizeResult(anyRes.Unwrap())
	}

	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		// Handle arrays by sanitizing each value
		var res []any
		for i := 1; i <= list.Len(); i++ {
			val, err := list.Nth(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get ID for object %d: %w", i, err)
			}
			simpl, err := m.sanitizeResult(val)
			if err != nil {
				return nil, fmt.Errorf("failed to simplify list element %d: %w", i, err)
			}
			res = append(res, simpl)
		}
		return res, nil
	}

	if str, ok := dagql.UnwrapAs[dagql.String](val); ok {
		// Handle strings by guarding against non-utf8 payloads.
		bytes := []byte(str.String())
		if !utf8.Valid(bytes) {
			return map[string]any{
				"type":   "non-utf8-string",
				"bytes":  len(bytes),
				"digest": digest.FromBytes(bytes),
			}, nil
		}
		// Return string content directly, without wrapping it in JSON.
		return str.String(), nil
	}

	if val == (Void{}) {
		// Represent Void as null. It's usually a 'null Void', but handle this
		// anyway for sanity's sake.
		return nil, nil
	}

	// Nothing else fishy, trust its marshaling
	return val, nil
}

// LookupTool looks for a tool identified by a name.
func (m *MCP) LookupTool(name string, tools []LLMTool) (*LLMTool, error) {
	var tool *LLMTool
	for _, t := range tools {
		if t.Name == name {
			tool = &t
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("tool %q is not available", name)
	}
	return tool, nil
}

func (m *MCP) Call(ctx context.Context, tools []LLMTool, toolCall *LLMToolCall) (res string, failed bool) {
	tool, err := m.LookupTool(toolCall.Name, tools)
	if err != nil {
		return guardToolResult(err.Error()), true
	}

	args := map[string]any{}
	if len(toolCall.Arguments) > 0 {
		if err := json.Unmarshal(toolCall.Arguments, &args); err != nil {
			return guardToolResult(fmt.Sprintf("failed to parse tool arguments: %s", err)), true
		}
	}

	var toolArgNames []string
	var toolArgValues []string
	if requiredArgs, ok := tool.Schema["required"].([]string); ok {
		for _, arg := range requiredArgs {
			val, ok := args[arg]
			if !ok {
				continue
			}
			if str, ok := val.(string); ok {
				toolArgNames = append(toolArgNames, arg)
				toolArgValues = append(toolArgValues, str)
			}
		}
	}
	toolName := tool.Name
	if tool.Server != "" {
		// External MCP tools may come prefixed `<server>_`; collision-namespaced
		// object tools are prefixed `<gqlFieldName(server)>_` (their Server is
		// the bound type name). Trim either so the span shows the bare tool name
		// alongside the server attribute.
		toolName = strings.TrimPrefix(toolName, tool.Server+"_")
		toolName = strings.TrimPrefix(toolName, gqlFieldName(tool.Server)+"_")
	}
	span := trace.SpanFromContext(ctx)
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.LLMToolAttr, toolName),
		attribute.StringSlice(telemetry.LLMToolArgNamesAttr, toolArgNames),
		attribute.StringSlice(telemetry.LLMToolArgValuesAttr, toolArgValues),
	}
	if tool.HideSelf {
		// Hide spans which are better represented by the child spans that they
		// spawn, i.e. CallMethod, ChainMethods, or direct object-method tools.
		attrs = append(attrs, attribute.Bool(telemetry.UIPassthroughAttr, true))
	}
	if tool.Server != "" {
		attrs = append(attrs, attribute.String(telemetry.LLMToolServerAttr, tool.Server))
	}
	span.SetAttributes(attrs...)

	var telemetryErr error
	defer telemetry.EndWithCause(span, &telemetryErr)
	defer func() {
		if failed {
			telemetryErr = fmt.Errorf("tool call %q failed", tool.Name)
		}
	}()

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.Bool(telemetry.LogsVerboseAttr, true))
	defer stdio.Close()

	defer func() {
		// Bound the result before anything observes it: everything downstream
		// must see exactly what the LLM sees, and this is the one place every
		// tool result funnels through. That means the telemetry copy below,
		// and the LLMToolResultTokensAttr estimate endToolCallDisplay stamps
		// from the string we return.
		res = guardToolResult(res)
		// write final result to telemetry so we see exactly what the LLM sees
		fmt.Fprintln(stdio.Stdout, res)
	}()

	toolCtx := ctx
	if m.workspace.Self() != nil {
		// Bind the LLM's Workspace so the tool's contextual (+defaultPath) and
		// Workspace-typed args resolve against it, not the ambient workspace.
		toolCtx = WorkspaceToContext(toolCtx, m.workspace)
	}
	if self := m.currentLLM(); self.Self() != nil {
		// Bind the conversation making this call so the tool's LLM-typed args
		// auto-fill with it — the continuation hook (see adoptLLM). Note this
		// must happen here rather than on the loop's ctx: a tool call runs under
		// its display span's context, captured while the response streamed.
		toolCtx = LLMToContext(toolCtx, self)
	}
	result, err := tool.Call(toolCtx, args)
	if err != nil {
		return toolErrorMessage(err), true
	}

	switch v := result.(type) {
	case string:
		return v, false
	default:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("Failed to marshal result: %s", err), true
		}
		return string(jsonBytes), false
	}
}

// llmToolResultMaxBytes is the total byte budget for a single tool result:
// the last-resort bound on how much one call can inject into the model's
// context (and into the persisted conversation).
//
// 48 KiB is roughly 12k tokens, and deliberately generous -- 3x the trace
// report budget. A result can legitimately carry a report that is already at
// its own 16 KiB budget PLUS the tool's own OUTPUT section and, for a
// state-returning method, a patch summary on top (see spanResult and
// routeObjectMethodResult), so a 16 KiB result budget would fight a report
// sitting exactly at its own. Reading a large source file is a legitimate
// result too: a tool's own offset/limit arguments are the pagination
// mechanism, this is only the safety net under them, and it should fire on
// accidents rather than on ordinary work -- a base64 blob on one line, a
// dumped database, a 300 KB JSON file read "15 lines" at a time. For
// reference, the pi agent bounds tool output at 50 KB or 2000 lines,
// whichever it hits first.
const llmToolResultMaxBytes = 48 * 1024

// llmToolResultHeadBytes is how much of llmToolResultMaxBytes the head keeps
// when the middle has to go: the same two-thirds split as the trace report
// and the captured-log cap. The head usually holds the answer, but plenty of
// results end with the part that matters most -- the last hunk of a diff, a
// trailing error -- so the tail is not a token gesture.
const llmToolResultHeadBytes = llmToolResultMaxBytes * 2 / 3

// guardToolResult bounds what a single tool call can hand back to the model:
// every line clamped to llmLogsMaxLineLen bytes, the whole result to
// llmToolResultMaxBytes with the middle dropped on line boundaries.
//
// The other size guards in here cover captured LOGS (limitIndirectLines,
// capLinesBytes) and rendered reports (guardTraceReport); nothing covered a
// tool's return VALUE, so e.g. a file read of a 6-line JSON file whose second
// line is a 400 KB base64 blob sailed past its own `limit: 15` and landed
// verbatim in the conversation. This is the one guard every tool result
// passes through, whatever produced it.
//
// Unlike a log capture there is no ReadLogs escape hatch for a return value --
// it was never persisted anywhere else -- so the marker steers the model back
// to the tool with a narrower request instead. A result already within both
// limits is returned byte-identical.
func guardToolResult(res string) string {
	return guardText(res, textGuard{
		maxBytes:   llmToolResultMaxBytes,
		maxLineLen: llmLogsMaxLineLen,
		headBytes:  llmToolResultHeadBytes,
		marker: func(lines, bytes int) string {
			return fmt.Sprintf("... %d lines (%d bytes) omitted from the middle of this result (re-run the call more narrowly to see them) ...",
				lines, bytes)
		},
	})
}

// CallBatch executes a batch of tool calls, handling MCP server syncing efficiently by
// grouping calls by destructiveness and server to avoid workspace conflicts
// toolCallCtx returns the display span context a tool call's arguments streamed
// into, so the tool's execution nests beneath it. Every provider — including
// replay — builds one display span per tool call (see displayPhases), so the
// fallback to ctx only applies to a provider that returns no display spans at
// all (none today) or a call ID the provider never announced.
func toolCallCtx(ctx context.Context, displays map[string]toolCallDisplay, callID string) context.Context {
	if tc, ok := displays[callID]; ok {
		return tc.Ctx
	}
	return ctx
}

// endToolCallDisplay ends a tool call's display span once the tool returns,
// marking it errored if the call failed. It also stamps the span with an
// estimated token count for the result the tool fed back into context, so the
// TUI can flag tool calls whose output is an outsized driver of context growth.
// No-op when there's no display span.
func endToolCallDisplay(displays map[string]toolCallDisplay, callID string, errored bool, result string) {
	if tc, ok := displays[callID]; ok {
		if tokens := estimateTextTokens(len(result)); tokens > 0 {
			tc.Span.SetAttributes(
				attribute.Int64(telemetryattrs.LLMToolResultTokensAttr, tokens),
			)
		}
		if errored {
			tc.Span.SetStatus(codes.Error, result)
		}
		tc.Span.End()
	}
}

func (m *MCP) CallBatch(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	// Group tool calls by their characteristics
	readOnlyMCPCalls := make(map[string][]*LLMToolCall)    // server -> read-only calls
	destructiveMCPCalls := make(map[string][]*LLMToolCall) // server -> destructive calls
	regularCalls := make([]*LLMToolCall, 0)
	changesetCalls := make([]*LLMToolCall, 0)
	destructiveCalls := make([]*LLMToolCall, 0)

	for _, toolCall := range toolCalls {
		tool, err := m.LookupTool(toolCall.Name, tools)
		if err != nil {
			// Couldn't find the tool, just call it regularly and let it fail with the
			// tool not found (or ambiguous) error
			regularCalls = append(regularCalls, toolCall)
			continue
		}

		// Object tools set Server to their bound type name for display, so a
		// non-empty Server alone doesn't make this an MCP tool — only a
		// registered MCP server does.
		if _, isMCPTool := m.mcpServers[tool.Server]; !isMCPTool {
			// Changeset-returning tools are evaluated in parallel against the same
			// workspace, then merged before the workspace is updated.
			if tool.ReturnsChangeset {
				changesetCalls = append(changesetCalls, toolCall)
			} else if tool.ReadOnly {
				regularCalls = append(regularCalls, toolCall)
			} else {
				destructiveCalls = append(destructiveCalls, toolCall)
			}
			continue
		}

		// This is an MCP tool call - check if it's read-only using the stored field
		if tool.ReadOnly {
			readOnlyMCPCalls[tool.Server] = append(readOnlyMCPCalls[tool.Server], toolCall)
		} else {
			destructiveMCPCalls[tool.Server] = append(destructiveMCPCalls[tool.Server], toolCall)
		}
	}

	var allResults []*LLMMessage

	// 1. Execute destructive non-MCP calls sequentially (they replace shared state).
	for _, call := range destructiveCalls {
		result, isError := m.Call(toolCallCtx(ctx, toolCallDisplays, call.CallID), tools, call)
		endToolCallDisplay(toolCallDisplays, call.CallID, isError, result)
		allResults = append(allResults, &LLMMessage{
			Role: LLMMessageRoleUser,
			Content: []*LLMContentBlock{{
				Kind:    LLMContentToolResult,
				Text:    result,
				CallID:  call.CallID,
				Errored: isError,
			}},
		})
	}

	// 2. Execute Changeset-returning calls in parallel and merge their changes.
	if len(changesetCalls) > 0 {
		allResults = append(allResults, m.callBatchChangesets(ctx, tools, changesetCalls, toolCallDisplays)...)
	}

	// 3. Execute destructive MCP calls one server at a time to avoid workspace conflicts
	for serverName, calls := range destructiveMCPCalls {
		serverResults := m.callBatchMCPServer(ctx, tools, calls, serverName, toolCallDisplays)
		allResults = append(allResults, serverResults...)
	}

	// 4. Execute all regular read-only (non-MCP) calls in parallel
	if len(regularCalls) > 0 {
		allResults = append(allResults, m.callBatchRegular(ctx, tools, regularCalls, toolCallDisplays)...)
	}

	// 5. Execute all read-only MCP calls in parallel (safe across servers)
	var readOnlyToolCalls []*LLMToolCall
	for _, calls := range readOnlyMCPCalls {
		readOnlyToolCalls = append(readOnlyToolCalls, calls...)
	}
	if len(readOnlyToolCalls) > 0 {
		allResults = append(allResults, m.callBatchRegular(ctx, tools, readOnlyToolCalls, toolCallDisplays)...)
	}

	return allResults
}

// callBatchMCPServer executes a batch of calls for a single MCP server with proper workspace syncing
func (m *MCP) callBatchMCPServer(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, serverName string, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	mcpSrv, ok := m.mcpServers[serverName]
	if !ok {
		// Fall back to individual calls if server not found
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	if _, ok := m.mcpSessions[serverName]; !ok {
		// Fall back to individual calls if session not found
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	ctr := mcpSrv.Service.Self().Container
	if ctr.Self() == nil || ctr.Self().Config.WorkingDir == "" || ctr.Self().Config.WorkingDir == "/" {
		// No workspace syncing needed - execute normally
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	// Use runAndSnapshotChanges to sync workspace and execute all tool calls atomically
	query, err := CurrentQuery(ctx)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	serviceDigest, err := mcpSrv.Service.ContentPreferredDigest(ctx)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	running, err := query.Services(ctx)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	runningSvc, err := running.Get(ctx, serviceDigest, false)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	// Snapshotting the workspace requires a bound workspace to diff against and
	// overlay back onto; without one, run the tools without syncing.
	if m.workspace.Self() == nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	srv, err := m.Server(ctx)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	sourceDir, err := m.workspaceDirectory(ctx, srv)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	var results []*LLMMessage
	snapshot, hasChanges, err := mcpSrv.Service.Self().runAndSnapshotChanges(
		ctx,
		runningSvc,
		ctr.Self().Config.WorkingDir,
		sourceDir,
		func() error {
			// Execute all tool calls for this server in parallel within the synced context
			results = m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
			return nil
		})

	if err != nil {
		// Fall back to individual calls if sync fails
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	// Apply workspace changes if any were made
	if hasChanges {
		if err := m.applyWorkspaceSnapshot(ctx, srv, sourceDir, snapshot); err != nil {
			slog.Error("failed to update workspace after MCP server batch", "server", serverName, "error", err)
		}
	}

	return results
}

// callBatchChangesets evaluates Changeset-returning tools concurrently without
// mutating the workspace, merges the successful results, then applies the merged
// Changeset once. Each tool still receives its own patch summary.
func (m *MCP) callBatchChangesets(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	type callResult struct {
		message *LLMMessage
		capture *changesetCapture
		failed  bool
	}

	calls := pool.NewWithResults[callResult]()
	for _, toolCall := range toolCalls {
		calls.Go(func() callResult {
			capture := new(changesetCapture)
			callCtx := context.WithValue(toolCallCtx(ctx, toolCallDisplays, toolCall.CallID), changesetCaptureKey{}, capture)
			content, failed := m.Call(callCtx, tools, toolCall)
			return callResult{
				message: &LLMMessage{
					Role: LLMMessageRoleUser,
					Content: []*LLMContentBlock{{
						Kind:    LLMContentToolResult,
						Text:    content,
						CallID:  toolCall.CallID,
						Errored: failed,
					}},
				},
				capture: capture,
				failed:  failed,
			}
		})
	}
	callResults := calls.Wait()

	changes := make([]dagql.ObjectResult[*Changeset], 0, len(callResults))
	for _, result := range callResults {
		if !result.failed && result.capture.changes.Self() != nil {
			changes = append(changes, result.capture.changes)
		}
	}

	var mergeErr error
	var conflictNote string
	if len(changes) > 0 {
		srv, err := m.Server(ctx)
		if err != nil {
			mergeErr = err
		} else {
			merged, note, err := mergeChangesets(ctx, srv, changes)
			if err == nil {
				conflictNote = note
				err = m.applyChangeset(ctx, srv, merged)
			}
			mergeErr = err
		}
	}

	messages := make([]*LLMMessage, len(callResults))
	for i, result := range callResults {
		block := result.message.Content[0]
		contributed := !result.failed && result.capture.changes.Self() != nil
		switch {
		case mergeErr != nil && contributed:
			block.Text = fmt.Sprintf("failed to merge parallel changesets: %s", mergeErr)
			block.Errored = true
		case conflictNote != "" && contributed:
			// The changes did land, so this is not a failed tool call — but the
			// merged result has conflict markers in it, which the agent must
			// resolve before building on top of them.
			block.Text += "\n\n" + conflictNote
		}
		endToolCallDisplay(toolCallDisplays, block.CallID, block.Errored, block.Text)
		messages[i] = result.message
	}
	return messages
}

// mergeChangesets combines the changesets produced by a batch of parallel tool
// calls into one.
//
// The fast path is git's octopus merge (Changeset.withChangesets), which is
// efficient and gives full git merge semantics — including rename detection —
// but it refuses to resolve any content-level conflict. Worse, "conflict" there
// includes merely *adjacent* edits, and a single conflicting pair fails the
// whole batch. Discarding the result would throw away every participating
// call's work, which is the expensive part: the agents have already done their
// reasoning, edits and verification by the time we get here.
//
// So on failure, fall back to replaying the changesets as patches onto their
// common base with PatchConflictLeaveMarkers, which applies every hunk that
// fits and leaves git-style conflict markers where it doesn't. The work is
// preserved and the agent gets a tree it can inspect and repair, rather than an
// error and an empty workspace. The returned note is non-empty in that case, so
// the caller can tell the agent to resolve the markers.
func mergeChangesets(ctx context.Context, srv *dagql.Server, changes []dagql.ObjectResult[*Changeset]) (dagql.ObjectResult[*Changeset], string, error) {
	if len(changes) == 1 {
		return changes[0], "", nil
	}

	merged, mergeErr := octopusMergeChangesets(ctx, srv, changes)
	if mergeErr == nil {
		return merged, "", nil
	}

	merged, err := patchMergeChangesets(ctx, srv, changes)
	if err != nil {
		// Preserving the work didn't pan out either; report the original merge
		// failure, with the fallback's own failure as context.
		return dagql.ObjectResult[*Changeset]{}, "", errors.Join(mergeErr, err)
	}
	return merged, conflictMarkerNote(mergeErr), nil
}

// conflictMarkerNote tells the agent what happened to its changes when the
// clean merge failed. The underlying git error names the conflicting paths
// (e.g. "CONFLICT (content): Merge conflict in foo.go"), which is the most
// useful part, so it is quoted verbatim.
func conflictMarkerNote(mergeErr error) string {
	return fmt.Sprintf(`NOTE: parallel edits from this batch overlapped, so they could not be merged cleanly.
Rather than discarding them, every change that applied cleanly was kept, and
git-style conflict markers (<<<<<<< / ======= / >>>>>>>) were left where it did not.
Search the workspace for conflict markers and resolve them before building on these changes.

The merge reported:
%s`, mergeErr)
}

func octopusMergeChangesets(ctx context.Context, srv *dagql.Server, changes []dagql.ObjectResult[*Changeset]) (dagql.ObjectResult[*Changeset], error) {
	otherIDs := make(dagql.ArrayInput[dagql.ID[*Changeset]], len(changes)-1)
	for i, changeset := range changes[1:] {
		id, err := changeset.ID()
		if err != nil {
			return dagql.ObjectResult[*Changeset]{}, fmt.Errorf("get changeset %d ID: %w", i+1, err)
		}
		otherIDs[i] = dagql.NewID[*Changeset](id)
	}

	var merged dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, changes[0], &merged, dagql.Selector{
		View:  srv.View,
		Field: "withChangesets",
		Args: []dagql.NamedInput{
			{Name: "changes", Value: otherIDs},
		},
	}); err != nil {
		return dagql.ObjectResult[*Changeset]{}, err
	}
	return merged, nil
}

// patchMergeChangesets replays each changeset as a patch onto their shared base
// directory, in order, tolerating hunks that no longer apply by leaving conflict
// markers. It is the conflict-preserving fallback for octopusMergeChangesets.
//
// Parallel tool calls in one batch all diff against the same workspace
// snapshot, so changes[0]'s Before is the common base for all of them.
func patchMergeChangesets(ctx context.Context, srv *dagql.Server, changes []dagql.ObjectResult[*Changeset]) (dagql.ObjectResult[*Changeset], error) {
	base := changes[0].Self().Before
	if base.Self() == nil {
		return dagql.ObjectResult[*Changeset]{}, fmt.Errorf("changeset has no before directory")
	}
	baseID, err := base.ID()
	if err != nil {
		return dagql.ObjectResult[*Changeset]{}, err
	}

	current := base
	for i, changeset := range changes {
		var patch dagql.ObjectResult[*File]
		if err := srv.Select(ctx, changeset, &patch, dagql.Selector{
			View:  srv.View,
			Field: "asPatch",
		}); err != nil {
			return dagql.ObjectResult[*Changeset]{}, fmt.Errorf("render changeset %d as patch: %w", i, err)
		}
		patchID, err := patch.ID()
		if err != nil {
			return dagql.ObjectResult[*Changeset]{}, err
		}
		var patched dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, current, &patched, dagql.Selector{
			View:  srv.View,
			Field: "withPatchFile",
			Args: []dagql.NamedInput{
				{Name: "patch", Value: dagql.NewID[*File](patchID)},
				{Name: "onConflict", Value: PatchConflictLeaveMarkers},
			},
		}); err != nil {
			return dagql.ObjectResult[*Changeset]{}, fmt.Errorf("apply changeset %d as patch: %w", i, err)
		}
		current = patched
	}

	var merged dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, current, &merged, dagql.Selector{
		View:  srv.View,
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*Directory](baseID)},
		},
	}); err != nil {
		return dagql.ObjectResult[*Changeset]{}, fmt.Errorf("rebuild changeset from patched base: %w", err)
	}
	return merged, nil
}

// callBatchRegular is the original parallel execution logic without MCP-specific syncing
func (m *MCP) callBatchRegular(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	// Run tool calls in parallel using the existing pool logic
	toolCallsPool := pool.NewWithResults[*LLMMessage]()
	for _, toolCall := range toolCalls {
		toolCallsPool.Go(func() *LLMMessage {
			content, isError := m.Call(toolCallCtx(ctx, toolCallDisplays, toolCall.CallID), tools, toolCall)
			endToolCallDisplay(toolCallDisplays, toolCall.CallID, isError, content)
			return &LLMMessage{
				Role: LLMMessageRoleUser, // Anthropic only allows tool call results in user messages
				Content: []*LLMContentBlock{{
					Kind:    LLMContentToolResult,
					Text:    content,
					CallID:  toolCall.CallID,
					Errored: isError,
				}},
			}
		})
	}
	return toolCallsPool.Wait()
}

// stableIDDigest returns a stable identity digest for an ID in either form.
// Recipe IDs use their recipe digest (unchanged behavior); handle-form IDs
// (post-evaluation cache handles) have no recipe digest, so derive one from
// their engine result ID — the same identity the engine uses to compare handle
// objects. Both the store (WithObject) and the lookup (Binding.Digest) use this,
// so object dedup stays consistent.
func stableIDDigest(id *call.ID) digest.Digest {
	if id == nil {
		return digest.FromString("")
	}
	if id.IsHandle() {
		return digest.FromString(fmt.Sprintf("engine-result:%d", id.EngineResultID()))
	}
	return id.Digest()
}

const llmLogsMaxLineLen = 2000
const llmLogsBatchSize = 1000

// captureLogs returns nicely Heroku-formatted lines of all logs emitted
// beneath the given span. When excludeServiceLogs is set, logs from
// long-lived service exec spans are skipped — they enter tool-call subtrees
// via cause links and would otherwise drown out deliberate print output.
func (m *MCP) captureLogs(ctx context.Context, spanID string, excludeServiceLogs bool) ([]string, error) {
	captured, err := m.captureLogLines(ctx, spanID, excludeServiceLogs, false)
	if err != nil {
		return nil, err
	}
	if len(captured.lines) == 0 {
		return nil, nil
	}
	texts := make([]string, len(captured.lines))
	for i, line := range captured.lines {
		texts[i] = line.text
	}
	return texts, nil
}

// capturedLine is one assembled log line, tagged with whether it was printed
// by the captured span itself (or one of its direct children — where a tool
// function's own print output lands) rather than by nested work deeper in the
// subtree. Tool results keep direct output in full and abridge the rest.
type capturedLine struct {
	text   string
	direct bool
}

// capturedOutput is one capture: the assembled lines, plus the set of spans
// whose records were classified `direct`.
//
// The span set is what lets a rendered trace report and a flat "own output"
// section coexist without printing the same line twice: the report is told to
// suppress the inline logs of exactly these spans, because the caller prints
// them itself, verbatim. Deriving it here — rather than re-deriving "the root
// and its children" in the renderer against dagui's own (cause-link-folded)
// parentage — keeps the two halves classified by one rule, so no line can be
// both hidden and unprinted.
type capturedOutput struct {
	lines []capturedLine
	// directSpans holds hex span IDs; empty when nothing direct was captured.
	directSpans map[string]bool
}

// captureLogLines is captureLogs' structured form: the same filtering and
// line assembly, but each line retains its direct/nested provenance.
func (m *MCP) captureLogLines(ctx context.Context, spanID string, excludeServiceLogs, ownOnly bool) (capturedOutput, error) {
	out := capturedOutput{directSpans: map[string]bool{}}
	root, err := CurrentQuery(ctx)
	if err != nil {
		return out, err
	}
	mainMeta, err := root.MainClientCallerMetadata(ctx)
	if err != nil {
		return out, fmt.Errorf("get main client caller metadata: %w", err)
	}
	q, err := root.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return out, err
	}
	defer q.Close()

	// segments accumulates log bodies in arrival order — one per record, each
	// tagged with its provenance; lines are assembled from them afterwards,
	// since a single log record needn't be line-aligned. Records are NOT
	// coalesced here: appending onto an accumulated string goes quadratic on
	// long same-provenance runs, and assembleLines merges across record
	// boundaries anyway.
	var segments []capturedLine

	// internalSpans skips subtrees hidden as internal, mirroring the TUI's
	// roll-up behavior.
	internalSpans := newInternalSpanFilter(q, spanID, excludeServiceLogs)

	var lastLogID int64

	for {
		logs, err := q.Read().SelectLogsBeneathSpan(ctx, clientdb.SelectLogsBeneathSpanParams{
			ID:     lastLogID,
			SpanID: sql.NullString{Valid: true, String: spanID},
			Limit:  llmLogsBatchSize,
		})
		if err != nil {
			return out, err
		}
		if len(logs) == 0 {
			break
		}
		// The batch was selected against the subtree as of a moment ago; make
		// sure the filter classifies against a set at least that fresh.
		internalSpans.refresh()

		for _, log := range logs {
			lastLogID = log.ID

			var logAttrs []*otlpcommonv1.KeyValue
			if err := clientdb.UnmarshalProtoJSONs(log.Attributes, &otlpcommonv1.KeyValue{}, &logAttrs); err != nil {
				slog.Warn("failed to unmarshal log attributes", "error", err)
				continue
			}

			// Call payloads are an internal binary transport, not log output.
			// Their content type reserves the record, including malformed ones,
			// so classify them before span handling or body conversion can
			// surface them to an LLM.
			var callPayload bool
			for _, attr := range logAttrs {
				if attr.Key == telemetry.ContentTypeAttr {
					callPayload = attr.Value.GetStringValue() == telemetryattrs.CallPayloadContentType
					break
				}
			}
			if callPayload {
				continue
			}

			var skip bool
		dance:
			for _, attr := range logAttrs {
				switch attr.Key {
				case telemetry.StdioEOFAttr, telemetry.LogsVerboseAttr, telemetry.LogsGlobalAttr:
					if attr.Value.GetBoolValue() {
						skip = true
						break dance
					}
				}
			}
			if skip {
				// don't generate a line for EOF events
				continue
			}

			// Logs we can't locate are treated as nested work: abridging them is
			// the conservative default.
			var direct bool
			if log.SpanID.Valid {
				if !log.TraceID.Valid {
					return out, fmt.Errorf("log %d has a span ID without a trace ID", log.ID)
				}
				hidden, d, err := internalSpans.classifyLogSpan(ctx, log.TraceID.String, log.SpanID.String)
				if err != nil {
					return out, err
				}
				if hidden {
					continue
				}
				direct = d && (!ownOnly || log.SpanID.String == spanID)
				if direct {
					out.directSpans[log.SpanID.String] = true
				}
			}

			var bodyPb otlpcommonv1.AnyValue
			if err := proto.Unmarshal(log.Body, &bodyPb); err != nil {
				slog.Warn("failed to unmarshal log body", "error", err, "client", mainMeta.ClientID, "log", log.ID)
				continue
			}
			var text string
			switch x := bodyPb.GetValue().(type) {
			case *otlpcommonv1.AnyValue_StringValue:
				text = x.StringValue
			default:
				// Only string bodies are log text. Bytes and structured values
				// are data transports (whatever produced them), and must never
				// be stringified into something an LLM reads as output.
				continue
			}
			if text == "" {
				continue
			}
			segments = append(segments, capturedLine{text: text, direct: direct})
		}
	}
	out.lines = assembleLines(segments)
	return out, nil
}

// assembleLines splits accumulated log segments into lines, carrying a line
// that straddles segments across the boundary. A line's provenance is that of
// the segment that started it — log records aren't guaranteed to be
// line-aligned, though a Dang `print` (Fprintln to the span's stdout) is.
func assembleLines(segments []capturedLine) []capturedLine {
	var lines []capturedLine
	// pending accumulates a line across segment boundaries; its provenance is
	// claimed by the first segment to contribute actual text, so the empty
	// chunk that trails a newline-terminated record doesn't hand the next
	// record's line to the wrong span.
	var pending strings.Builder
	var pendingDirect, pendingSet bool
	for _, seg := range segments {
		chunks := strings.Split(seg.text, "\n")
		for i, chunk := range chunks {
			if chunk != "" {
				if !pendingSet {
					pendingDirect = seg.direct
					pendingSet = true
				}
				pending.WriteString(chunk)
			}
			if i < len(chunks)-1 {
				// a "\n" followed this chunk: the line is complete
				direct := seg.direct
				if pendingSet {
					direct = pendingDirect
				}
				lines = append(lines, capturedLine{text: pending.String(), direct: direct})
				pending.Reset()
				pendingDirect, pendingSet = false, false
			}
		}
	}
	if pending.Len() > 0 {
		lines = append(lines, capturedLine{text: pending.String(), direct: pendingDirect})
	}
	// ensure trailing linebreaks don't contribute to line limits
	for len(lines) > 0 && lines[len(lines)-1].text == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// internalSpanFilter classifies the spans of a log capture (classifyLogSpan),
// chiefly by whether they sit within a subtree marked internal
// (dagger.io/ui.internal) beneath the captured root span, so their logs can
// be skipped — mirroring how the TUI refuses to roll logs up across an
// internal span. When skipServices is set, service exec spans
// (dagger.io/service) are filtered the same way, keeping long-lived service
// noise out of tool-result captures. Results are memoized per span so
// captureLogs doesn't re-walk the parent chain for every log line.
type internalSpanFilter struct {
	db           *clientdb.DB
	root         string
	skipServices bool
	memo         map[string]bool
	// subtree is the set of spans the capture scopes to — the same walk the
	// log selection uses (the captured root, its cause-link targets, and both
	// sets' subtrees). It bounds beneathInternal's ancestor walk: spans join
	// the capture via cause links (e.g. a service exec span parented under
	// whatever call triggered the start), so their parent chains leave the
	// subtree without ever passing through the root, and internal-ness out
	// there is not between the log and the capture root.
	subtree map[string]struct{}
}

func newInternalSpanFilter(db *clientdb.DB, rootSpanID string, skipServices bool) *internalSpanFilter {
	return &internalSpanFilter{
		db:           db,
		root:         rootSpanID,
		skipServices: skipServices,
		memo:         map[string]bool{},
		subtree:      db.SpanLogScope(rootSpanID),
	}
}

// refresh re-snapshots the captured subtree, so spans that arrived since the
// last batch (the set only grows) are classified against current containment.
// Call it after each log-batch fetch: the filter's set must be at least as
// fresh as the selection that produced the batch, or a batch's own log spans
// could read as outside the capture. Memoized answers survive a refresh that
// found nothing new; a grown subtree drops them, since a walk that previously
// stopped at the subtree's edge may now continue further.
func (f *internalSpanFilter) refresh() {
	subtree := f.db.SpanLogScope(f.root)
	if len(subtree) == len(f.subtree) {
		return
	}
	f.subtree = subtree
	f.memo = map[string]bool{}
}

// classifyLogSpan locates a log record's span and decides how the capture
// treats its logs: hidden entirely (an LLM span's own prompt/response noise,
// or a span beneath one hidden as internal), or kept — and, when kept,
// whether the record counts as the captured root's direct output (the root
// itself or one of its direct children, where a tool function's own print
// output lands).
func (f *internalSpanFilter) classifyLogSpan(ctx context.Context, traceID, spanID string) (hidden, direct bool, err error) {
	span, err := f.db.Read().SelectSpan(ctx, clientdb.SelectSpanParams{
		TraceID: traceID,
		SpanID:  spanID,
	})
	if err != nil {
		return false, false, err
	}
	var spanAttrs []*otlpcommonv1.KeyValue
	if err := clientdb.UnmarshalProtoJSONs(span.Attributes, &otlpcommonv1.KeyValue{}, &spanAttrs); err != nil {
		slog.Warn("failed to unmarshal span attributes", "error", err)
		return true, false, nil
	}
	for _, attr := range spanAttrs {
		if attr.Key == telemetry.LLMRoleAttr || attr.Key == telemetry.LLMToolAttr {
			// don't show logs from the LLM spans themselves
			return true, false, nil
		}
	}
	internal, err := f.beneathInternal(ctx, traceID, spanID)
	if err != nil {
		return false, false, err
	}
	if internal {
		// don't surface logs from spans hidden as internal
		return true, false, nil
	}
	direct = spanID == f.root ||
		(span.ParentSpanID.Valid && span.ParentSpanID.String == f.root)
	return false, direct, nil
}

// beneathInternal reports whether the given span, or any ancestor within the
// captured subtree, is marked internal. Internal-ness outside the subtree
// doesn't hide the logs beneath it — neither at or above the root
// (explicitly capturing an internal span's subtree still returns its logs)
// nor on the unrelated ancestors of a cause-linked span (a service exec
// span's parent chain runs through whatever call triggered the start, never
// through the capture root; an internal span up there is not between the log
// and the capture).
func (f *internalSpanFilter) beneathInternal(ctx context.Context, traceID, spanID string) (bool, error) {
	if spanID == "" || spanID == f.root {
		return false, nil
	}
	if _, ok := f.subtree[spanID]; !ok {
		// The walk has left the captured subtree: same rule as reaching the
		// root, whatever is out here doesn't hide the capture's logs.
		return false, nil
	}
	if internal, ok := f.memo[spanID]; ok {
		return internal, nil
	}
	span, err := f.db.Read().SelectSpan(ctx, clientdb.SelectSpanParams{
		TraceID: traceID,
		SpanID:  spanID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// span not stored; nothing to hide
			f.memo[spanID] = false
			return false, nil
		}
		return false, err
	}
	var spanAttrs []*otlpcommonv1.KeyValue
	if err := clientdb.UnmarshalProtoJSONs(span.Attributes, &otlpcommonv1.KeyValue{}, &spanAttrs); err != nil {
		slog.Warn("failed to unmarshal span attributes", "error", err)
		f.memo[spanID] = false
		return false, nil
	}
	var internal bool
	for _, attr := range spanAttrs {
		if attr.Key == telemetry.UIInternalAttr && attr.Value.GetBoolValue() {
			internal = true
			break
		}
		if f.skipServices && attr.Key == telemetryattrs.ServiceAttr && attr.Value.GetBoolValue() {
			internal = true
			break
		}
	}
	if !internal && f.skipServices {
		// Service stdio log records are tied to the *install* span
		// (Container.asService and friends) rather than the service's exec
		// span itself: core/service.go routes them there via the executor
		// cause context (logTargetCtx). Detect install spans by their
		// cause-linked service exec child and filter them the same way.
		internal, err = f.serviceInstallSpan(ctx, traceID, spanID)
		if err != nil {
			return false, err
		}
	}
	if !internal && span.ParentSpanID.Valid {
		internal, err = f.beneathInternal(ctx, traceID, span.ParentSpanID.String)
		if err != nil {
			return false, err
		}
	}
	f.memo[spanID] = internal
	return internal, nil
}

// serviceInstallSpan reports whether a service's long-lived exec span
// (dagger.io/service) cause-links to the given span — i.e. whether the span
// is one of the API spans that installed a Service value. Service stdio log
// records are tied to those install spans (see core/service.go), so
// filtering service logs means filtering the install spans' subtrees.
//
// That is deliberately coarse: when a Service comes from a module function
// call, that call is the install span, so the function's own construction
// logs are filtered along with the service stdio. Acceptable for a
// tool-result capture — the tool's own prints sit outside the install span,
// and ReadLogs remains the deliberate path to anything filtered.
func (f *internalSpanFilter) serviceInstallSpan(ctx context.Context, traceID, spanID string) (bool, error) {
	for _, childID := range f.db.CausalChildren(spanID) {
		child, err := f.db.Read().SelectSpan(ctx, clientdb.SelectSpanParams{
			TraceID: traceID,
			SpanID:  childID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// linking span not stored (or in another trace); can't tell
				continue
			}
			return false, err
		}
		var childAttrs []*otlpcommonv1.KeyValue
		if err := clientdb.UnmarshalProtoJSONs(child.Attributes, &otlpcommonv1.KeyValue{}, &childAttrs); err != nil {
			slog.Warn("failed to unmarshal span attributes", "error", err)
			continue
		}
		for _, attr := range childAttrs {
			if attr.Key == telemetryattrs.ServiceAttr && attr.Value.GetBoolValue() {
				return true, nil
			}
		}
	}
	return false, nil
}

func toolErrorMessage(err error) string {
	errResponse := err.Error()
	// propagate error values to the model
	var extErr dagql.ExtendedError
	if errors.As(err, &extErr) {
		// TODO: return a structured error object instead?
		var exts []string
		for k, v := range extErr.Extensions() {
			if k == "traceparent" || k == "baggage" {
				// silence this one
				continue
			}
			var ext strings.Builder
			fmt.Fprintf(&ext, "<%s>\n", k)

			switch v := v.(type) {
			case string:
				ext.WriteString(v)
			default:
				jsonBytes, err := json.Marshal(v)
				if err != nil {
					fmt.Fprintf(&ext, "error marshalling value: %s", err.Error())
				} else {
					ext.Write(jsonBytes)
				}
			}

			fmt.Fprintf(&ext, "\n</%s>", k)

			exts = append(exts, ext.String())
		}
		if len(exts) > 0 {
			sort.Strings(exts)
			errResponse += "\n\n" + strings.Join(exts, "\n\n")
		}
	}
	return errResponse
}

func (m *MCP) loadBuiltins(srv *dagql.Server, allTools *LLMToolSet) {
	allTools.Add(LLMTool{
		Name: "ReadLogs",
		Description: "Read the logs beneath a span: exec output, service logs, prints. Can filter with grep pattern or read the last N lines." + "\n" +
			"Span IDs come from tool results, ListServices, or [traceparent:traceID-spanID] markers in errors (pasting the whole marker works).",
		ReadOnly: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"span": map[string]any{
					"type":        "string",
					"description": "Span ID to query logs beneath, recursively",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Number of lines to read from the end.",
					"minimum":     1,
					"default":     100,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Number of lines to skip from the end. If not specified, starts from the end.",
					"minimum":     0,
				},
				"grep": map[string]any{
					"type":        "string",
					"description": "Grep pattern to filter logs. If specified, only lines matching this pattern will be returned.",
				},
			},
			"required":             []string{"span"},
			"additionalProperties": false,
		},
		Strict: false,
		Call:   m.readLogsTool(srv),
	})
	allTools.Add(LLMTool{
		Name: "ReadTrace",
		Description: "Render the trace report for a span, check, or test: the span tree, plus the CHECKS and TESTS sections, exactly as they appear at the end of a run." + "\n" +
			"Tool results are abridged; this is how you see the full detail behind one - pass the span ID from a report's footer, or the name of a check or test you saw run." + "\n" +
			"Prefer ReadTrace when you want the shape of what ran (which steps, which checks/tests, where it failed); use ReadLogs when you want the raw log lines of a span." + "\n" +
			"When a name matches several spans, the most recent one is rendered.",
		ReadOnly: true, // Read-only operation
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"span": map[string]any{
					"type":        "string",
					"description": "Span ID (hex) to render the report for, scoped to that span's subtree.",
				},
				"check": map[string]any{
					"type":        "string",
					"description": "Check name to render the report for, e.g. \"shellcheck:check\".",
				},
				"test": map[string]any{
					"type":        "string",
					"description": "Test case or suite name to render the report for.",
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: false,
		Call:   m.readTraceTool(srv),
	})
	allTools.Add(LLMTool{
		Name: "ListServices",
		Description: "List the services in this session: hostname, exposed ports, state (running or exited), and span IDs." + "\n" +
			"Read a service's logs with ReadLogs(span: <spanID>) — useful for tailing a server or engine that runs as a service." + "\n" +
			"Services that exited — crashes included — stay listed with state \"exited\" and any exit code/error, so their logs remain reachable via ReadLogs." + "\n" +
			"installSpanIDs are the API calls that produced the service (e.g. Container.asService); they work with ReadLogs too.",
		ReadOnly: true,
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: false,
		Call:   m.listServicesTool(srv),
	})
}

func (m *MCP) listServicesTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, _ struct{}) (any, error) {
		root, err := CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		mainMeta, err := root.MainClientCallerMetadata(ctx)
		if err != nil {
			return nil, fmt.Errorf("get main client caller metadata: %w", err)
		}
		svcs, err := root.Services(ctx)
		if err != nil {
			return nil, err
		}

		type serviceInfo struct {
			Hostname       string   `json:"hostname"`
			Ports          []string `json:"ports,omitempty"`
			SpanID         string   `json:"spanID,omitempty"`
			InstallSpanIDs []string `json:"installSpanIDs,omitempty"`
			State          string   `json:"state"`
			ExitCode       *int     `json:"exitCode,omitempty"`
			ExitError      string   `json:"exitError,omitempty"`
		}
		// makeInfo renders the fields running and exited services share; both
		// expose the same span-context accessors.
		type spannedService interface {
			ServiceSpanContext() trace.SpanContext
			InstallSpanContexts() []trace.SpanContext
		}
		makeInfo := func(host string, ports []Port, state string, svc spannedService) serviceInfo {
			info := serviceInfo{Hostname: host, State: state}
			for _, port := range ports {
				desc := fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network())
				if port.Description != nil && *port.Description != "" {
					desc += " (" + *port.Description + ")"
				}
				info.Ports = append(info.Ports, desc)
			}
			if spanCtx := svc.ServiceSpanContext(); spanCtx.HasSpanID() {
				info.SpanID = spanCtx.SpanID().String()
			}
			for _, installCtx := range svc.InstallSpanContexts() {
				if !installCtx.HasSpanID() {
					continue
				}
				info.InstallSpanIDs = append(info.InstallSpanIDs, installCtx.SpanID().String())
			}
			return info
		}
		running := svcs.RunningServices(mainMeta.SessionID)
		exited := svcs.ExitedServices(mainMeta.SessionID)
		infos := make([]serviceInfo, 0, len(running)+len(exited))
		for _, svc := range running {
			infos = append(infos, makeInfo(svc.Host, svc.Ports, "running", svc))
		}
		// Exited services follow the running ones: they stay listed so their
		// span handles remain usable (e.g. for ReadLogs) after a crash.
		for _, svc := range exited {
			info := makeInfo(svc.Host, svc.Ports, "exited", svc)
			if svc.ExitErr != nil {
				info.ExitError = svc.ExitErr.Error()
			}
			if svc.ExitCode >= 0 {
				exitCode := svc.ExitCode
				info.ExitCode = &exitCode
			}
			infos = append(infos, info)
		}
		return toolStructuredResponse(map[string]any{
			"services": infos,
		})
	})
}

func (m *MCP) readLogsTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Span   string
		Offset int    `default:"0"`
		Limit  int    `default:"100"`
		Grep   string `default:""`
	}) (any, error) {
		spanID := normalizeSpanArg(args.Span)
		// Include service logs: ReadLogs is the deliberate affordance for
		// reading them (e.g. via span IDs from ListServices).
		logs, err := m.captureLogs(ctx, spanID, false)
		if err != nil {
			return nil, fmt.Errorf("failed to capture logs: %w", err)
		}
		if len(logs) == 0 {
			// An empty capture is only an error when the span itself is
			// unknown: a known span with nothing logged yet is a normal
			// answer (e.g. tailing a service that hasn't printed).
			known, err := m.spanKnown(ctx, spanID)
			if err != nil {
				slog.Warn("failed to check span existence", "span", spanID, "error", err)
			} else if !known {
				return nil, fmt.Errorf("span %q not found in this session's telemetry; use a span ID from a tool result, ListServices, or an error's traceparent", spanID)
			}
			return fmt.Sprintf("(no logs beneath span %s yet)", spanID), nil
		}
		return renderReadLogs(spanID, logs, args.Offset, args.Limit, args.Grep)
	})
}

// normalizeSpanArg extracts a span ID from the forms agents actually paste:
// a bare hex ID, the "span=<hex>" rendering from reports, or a traceparent
// ("[traceparent:<traceID>-<spanID>]" error-origin markers, or the W3C
// 00-<traceID>-<spanID>-<flags> form). Unrecognized input passes through
// untouched, to be reported as not found.
func normalizeSpanArg(arg string) string {
	arg = strings.TrimSpace(arg)
	arg = strings.Trim(arg, "[]")
	arg = strings.TrimPrefix(arg, "traceparent:")
	arg = strings.TrimPrefix(arg, "span=")
	if isHexID(arg, 16) {
		return arg
	}
	parts := strings.Split(arg, "-")
	for i, part := range parts {
		if isHexID(part, 16) && i > 0 && isHexID(parts[i-1], 32) {
			return part
		}
	}
	return arg
}

func isHexID(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// spanKnown reports whether the span ID appears in the session's recorded
// telemetry, so an empty ReadLogs capture can distinguish a quiet span from a
// mistyped one.
func (m *MCP) spanKnown(ctx context.Context, spanID string) (bool, error) {
	traceID := trace.SpanContextFromContext(ctx).TraceID()
	if !traceID.IsValid() {
		// no trace to check against; treat the span as plausible
		return true, nil
	}
	root, err := CurrentQuery(ctx)
	if err != nil {
		return false, err
	}
	mainMeta, err := root.MainClientCallerMetadata(ctx)
	if err != nil {
		return false, fmt.Errorf("get main client caller metadata: %w", err)
	}
	q, err := root.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return false, err
	}
	defer q.Close()
	if _, err := q.Read().SelectSpan(ctx, clientdb.SelectSpanParams{
		TraceID: traceID.String(),
		SpanID:  spanID,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// renderReadLogs shapes captured log lines into a ReadLogs result: trims the
// last offset lines, applies the grep filter, numbers the lines, and caps the
// output. The error and empty cases carry the numbers an agent needs to
// recover — how many lines exist, how many were searched.
func renderReadLogs(spanID string, logs []string, offset, limit int, grepPattern string) (string, error) {
	if offset < 0 {
		offset = 0
	}
	// Trim the last offset lines
	if offset >= len(logs) {
		return "", fmt.Errorf("offset %d skips all %d available lines; retry with a smaller offset (0 reads the tail)", offset, len(logs))
	}
	logs = logs[:len(logs)-offset]

	// Apply grep filter if specified
	if grepPattern != "" {
		re, err := regexp.Compile(grepPattern)
		if err != nil {
			return "", fmt.Errorf("invalid grep pattern %q: %w", grepPattern, err)
		}
		var filteredLogs []string
		for i, line := range logs {
			if re.MatchString(line) {
				filteredLogs = append(filteredLogs, fmt.Sprintf("%6d→%s", i+1, line))
			}
		}
		if len(filteredLogs) == 0 {
			return fmt.Sprintf("(no matches for %q in the %d lines beneath span %s)", grepPattern, len(logs), spanID), nil
		}
		logs = filteredLogs
	} else {
		for i, line := range logs {
			logs[i] = fmt.Sprintf("%6d→%s", i+1, line)
		}
	}

	// Apply line limit if specified
	logs = limitLines(spanID, logs, limit, llmLogsMaxLineLen)

	return strings.Join(logs, "\n"), nil
}

// readTraceTool renders the pretty trace report for a span, check or test --
// in the same shape a tool call's own result is rendered as (the target's own
// output, then the report), so what the reader gets back is in the vocabulary
// it already sees, just scoped to the target it asked about.
func (m *MCP) readTraceTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Span  string `default:""`
		Check string `default:""`
		Test  string `default:""`
	}) (any, error) {
		target := traceTarget{
			Span:  args.Span,
			Check: args.Check,
			Test:  args.Test,
		}
		spanID, err := resolveTraceTarget(ctx, target)
		if err != nil {
			return nil, err
		}
		if result := m.spanResult(ctx, spanID, readTraceReportOpts(target)); result != "" {
			return result, nil
		}
		// A subtree can legitimately render to nothing (dagui hides internal,
		// passthrough and encapsulated spans); say so rather than returning an
		// empty result, and point at the path that does show raw output.
		return fmt.Sprintf("(no trace report for span %s; use ReadLogs(span: %s) to read its logs)",
			spanID, spanID), nil
	})
}

// readTraceReportOpts picks the render options that suit the KIND of target
// ReadTrace was given.
//
// A span target keeps the tool-call options: the caller named that subtree, so
// it wants that subtree unwrapped.
//
// A test or check target does not. ExpandWrappers' unwrap ("descend through
// wrapper spans to the first real work, then stop") is tuned for a tool-call
// scope, where the scope root is a roll-up boundary that would otherwise
// swallow the tool's output. A test or check span is not that: it is the head
// of a roll-up the report already knows how to summarise, and force-expanding
// it enumerates every dagql field call beneath -- measured on
// ReadTrace(test: "TestToolLogsExcludeService") as hundreds of rows of
// LLMMessage.role / LLMContentBlock.text micro-spans, which are the test's own
// API traffic, not conversation. Left to the normal IsExpanded rules, those
// collapse and the TESTS / CHECKS roll-ups (with their failing-case logs) are
// what the reader gets -- the CLI's end-of-run report for that target. The
// target's own logs still reach the reader: spanResult prints them as OUTPUT --
// but only the records on the target span ITSELF (OwnOutputOnly). The depth-1
// rule that OUTPUT normally uses exists because a module function's print lands
// one hop below the tool-call span; a named target has no such indirection, and
// its direct children ARE the nested work -- for a suite, its cases, whose logs
// belong to the TESTS roll-up rather than hoisted into (and duplicated out of)
// OUTPUT.
func readTraceReportOpts(target traceTarget) traceReportOpts {
	opts := toolCallReportOpts()
	opts.OwnOutputOnly = true
	// ReadTrace is the "show me the shape of what ran" tool: it keeps the span
	// tree the tool-call result drops.
	opts.HideSpanTree = false
	if target.Span == "" {
		opts.ExpandWrappers = false
	}
	return opts
}

// describeObject renders an object result for the model: its type plus any
// trivial (cheap, scalar) fields. It deliberately mints no reference handle —
// objects are referenced by the names they're bound to (a `let` within a script
// or a WithObject injection), and a bare object can be rebuilt from its
// expression (Dagger is content-addressed), so this is purely informational.
func (m *MCP) describeObject(ctx context.Context, srv *dagql.Server, target dagql.AnyObjectResult) (string, error) {
	schema := srv.Schema()
	typeName := target.Type().Name()
	res := map[string]any{
		"type": typeName,
	}
	data := map[string]any{}
	for _, field := range schema.Types[typeName].Fields {
		trivial := field.Directives.ForName(trivialFieldDirectiveName) != nil
		if !trivial {
			continue
		}
		var val dagql.AnyResult
		err := srv.Select(ctx, target, &val, dagql.Selector{
			View:  srv.View,
			Field: field.Name,
		})
		if err != nil {
			return "", err
		}
		if _, isObj := srv.ObjectType(val.Type().Name()); isObj {
			// skip any fields that reference objects, to avoid dumping entire
			// ModuleObjects
			continue
		}
		datum, err := m.sanitizeResult(val)
		if err != nil {
			return "", err
		}
		data[field.Name] = datum
	}
	if len(data) > 0 {
		res["data"] = data
	}
	return toolStructuredResponse(res)
}

// WorkspaceID returns the call.ID of the bound workspace, or nil if the LLM is
// not bound to a workspace. Used by step() to detect (and persist) an in-step
// workspace change, e.g. a Changeset overlaid by a tool.
func (m *MCP) WorkspaceID() (*call.ID, error) {
	if m.workspace.Self() == nil {
		return nil, nil
	}
	return m.workspace.ID()
}

func toolStructuredResponse(val any) (string, error) {
	str := new(strings.Builder)
	enc := json.NewEncoder(str)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(val); err != nil {
		return "", fmt.Errorf("failed to encode response %T: %w", val, err)
	}
	return str.String(), nil
}

func limitLines(spanID string, logs []string, limit, maxLineLen int) []string {
	if limit > 0 && len(logs) > limit {
		snipped := fmt.Sprintf("... %d lines omitted (use ReadLogs(span: %s) to read more) ...", len(logs)-limit, spanID)
		logs = append([]string{snipped}, logs[len(logs)-limit:]...)
	}
	for i, line := range logs {
		if len(line) > maxLineLen {
			logs[i] = line[:maxLineLen] + fmt.Sprintf("[... %d chars truncated]", len(line)-maxLineLen)
		}
	}
	return logs
}

// limitIndirectLines abridges a captured log stream for a tool result: lines
// the tool printed itself survive in full, while logs from nested work
// underneath it are limited to the last `limit` lines, with each dropped run
// replaced by a count. A tool's report is deliberate output and stays intact
// no matter how noisy the work beneath it was; nested logs remain fully
// readable via ReadLogs. "In full" still answers to the last-resort total
// byte cap (llmToolLogsMaxBytes) — deliberate output is unbounded by lines,
// not by bytes.
func limitIndirectLines(spanID string, lines []capturedLine, limit, maxLineLen int) []string {
	// Indirect lines are kept from the tail: the most recent nested output is
	// the most relevant (e.g. the error that ended a build).
	keepFrom := 0
	if limit > 0 {
		var indirect int
		for _, line := range lines {
			if !line.direct {
				indirect++
			}
		}
		if indirect > limit {
			keepFrom = indirect - limit
		}
	}

	var out []string
	var seen, dropped int
	flush := func() {
		if dropped == 0 {
			return
		}
		out = append(out, fmt.Sprintf("... %d lines omitted (use ReadLogs(span: %s) to read more) ...",
			dropped, spanID))
		dropped = 0
	}
	for _, line := range lines {
		if line.direct {
			flush()
			out = append(out, line.text)
			continue
		}
		if seen < keepFrom {
			seen++
			dropped++
			continue
		}
		seen++
		flush()
		out = append(out, line.text)
	}
	flush()

	for i, line := range out {
		if len(line) > maxLineLen {
			out[i] = line[:maxLineLen] + fmt.Sprintf("[... %d chars truncated]", len(line)-maxLineLen)
		}
	}
	return capLinesBytes(spanID, out, llmToolLogsMaxBytes)
}

// llmToolLogsMaxBytes is the total byte budget for a tool result's captured
// logs. Direct output survives line-based abridging by design — a tool's
// report is the point of the call — but a runaway print (a cat'd file,
// megabytes of dumped state) shouldn't ride into the model's context
// wholesale. 16 KiB is roughly 4k tokens: far above any deliberate report,
// low enough that an accident can't crowd out the conversation.
const llmToolLogsMaxBytes = 16 * 1024

// capLinesBytes bounds the total size of a tool-log capture as a last-resort
// safeguard, by bytes so that long lines count for what they cost. The
// middle is dropped rather than the tail — a report's opening and its
// conclusion both carry signal — behind the usual counted ReadLogs marker,
// with the head taking the larger share. At least one line survives on each
// side; the per-line char cap upstream keeps that from busting the budget.
func capLinesBytes(spanID string, lines []string, maxBytes int) []string {
	if maxBytes <= 0 {
		return lines
	}
	total := 0
	for _, line := range lines {
		total += len(line) + 1 // +1 for the newline that rejoins it
	}
	if total <= maxBytes {
		return lines
	}
	headBudget := maxBytes * 2 / 3
	tailBudget := maxBytes - headBudget
	head, spent := 0, 0
	for head < len(lines) {
		cost := len(lines[head]) + 1
		if spent+cost > headBudget && head > 0 {
			break
		}
		spent += cost
		head++
	}
	tail, spent := len(lines), 0
	for tail > head {
		cost := len(lines[tail-1]) + 1
		if spent+cost > tailBudget && tail < len(lines) {
			break
		}
		spent += cost
		tail--
	}
	if head >= tail {
		// the kept head and tail already meet; nothing left to drop
		return lines
	}
	out := make([]string, 0, head+(len(lines)-tail)+1)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("... %d lines omitted (use ReadLogs(span: %s) to read more) ...",
		tail-head, spanID))
	out = append(out, lines[tail:]...)
	return out
}

// Hide functions from the largest and most commonly used core types, to prevent
// tool bloat
