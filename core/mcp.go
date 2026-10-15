package core

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
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
	// Whether we should hide the LLM tool call span in favor of just showing its
	// child spans.
	HideSelf bool `json:"-"`
	// Whether the tool is pure: it changes neither the agent's state (bound
	// workspace, bindings, conversation) nor the outside world, so CallBatch
	// may run it concurrently with the pure calls next to it. Any other tool is
	// a sequential step, run alone in the position it was written. Unset means
	// sequential, the safe default. Object tools derive it from their return
	// type and cache policy or DoNotCache mark (toolsForBoundObject);
	// MCP-server tools from their ReadOnlyHint annotation.
	ReadOnly bool `json:"-"`
	// Whether the tool returns an LLM — a continuation (see MCP.adoptLLM).
	// CallBatch runs these after every other call in the turn, so the
	// conversation they receive already reflects the turn's effects.
	ReturnsLLM bool `json:"-"`
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
	// workspace is the Workspace the LLM is bound to, if any. It selects the
	// base schema in baseServer and receives workspace-mutating tool results
	// (Changeset overlays). The binding also threads the workspace into tool
	// dispatch so contextual (+defaultPath) and Workspace-typed args resolve
	// against it. Bound module tools retain their own defining schemas.
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
	skillDirs []ownedSkillDirectory
	// selfLLM is the conversation dispatching the current step's tool calls —
	// inst + withResponse, i.e. up to and including the in-flight tool call.
	// The object-tool adapter passes it explicitly to hidden LLM arguments.
	// Transient: cleared by Clone, never persisted.
	selfLLM dagql.ObjectResult[*LLM]
	// continuation is an LLM returned by a tool during this step (see
	// applyStateReturn / adoptLLM). When set, step() appends the turn's tool
	// results to IT rather than to the LLM that made the call, so the loop
	// resumes from the returned conversation — env, tools, prompts and all.
	// CallBatch stops once one is adopted; step() runs the turn's remaining
	// calls on the continued conversation. Transient: cleared by Clone.
	continuation dagql.ObjectResult[*LLM]
	// stateChanged records that a tool call changed the bound workspace or
	// bindings since selfLLM was set — this MCP has diverged from the
	// conversation an LLM! argument would receive. A continuation adopted in
	// that state would silently drop the divergence (step() resumes from the
	// continuation, not from this MCP), so adoptLLM refuses it. step() folds
	// the changes into a fresh selfLLM before the continuation phase, which
	// resets this (see SetSelfLLM). Transient: cleared by Clone.
	stateChanged bool
	// standalone marks an MCP serving tools without a driving conversation
	// (dagger mcp): no selfLLM will ever be set, so LLM-typed tool arguments
	// have nothing to be filled from and are treated as unsatisfiable (see
	// implicitToolArgs). Unlike the per-step scratch above, it survives Clone.
	standalone bool
	// scopeBase is the conversation a standalone server (dagger mcp) serves,
	// with its bindings recorded. No step sets selfLLM there, so tool-argument
	// addresses resolve in this conversation's scope instead (see
	// MCP.scopeLLM). Like standalone, it survives Clone.
	scopeBase dagql.ObjectResult[*LLM]
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
	cp.stateChanged = false
	cp.mu = &sync.Mutex{}
	return &cp
}

// Standalone returns a copy that serves tools without a driving conversation,
// e.g. to an external MCP client (dagger mcp). LLM-typed tool arguments are
// then unsatisfiable: a method requiring one is not offered at all, and an
// optional one is exposed by ID like any other object argument (see
// implicitToolArgs).
func (m *MCP) Standalone() *MCP {
	m = m.Clone()
	m.standalone = true
	return m
}

// SetSelfLLM records the conversation dispatching this step's tool calls, so
// the object-tool adapter can pass it explicitly to an `LLM!` argument. Called
// by step() on its transient MCP clone: first with the response itself, then —
// before CallBatch runs a continuation — with the workspace and binding
// changes of the calls before it folded in, so a continuation transforms the
// state the turn actually produced, and finally, once a continuation is
// adopted, with that continuation on the MCP running the rest of the turn.
// The conversation is in sync with this MCP at each point by construction,
// so the divergence flag resets.
func (m *MCP) SetSelfLLM(llm dagql.ObjectResult[*LLM]) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selfLLM = llm
	m.stateChanged = false
}

// errContinuationAdopted is returned by the state rings (applyChangeset,
// rebindWorkspace, rebindBoundTool) once a continuation has been adopted on
// this MCP: step() resumes from the continuation, so a change made here after
// it would be dropped without a trace. CallBatch stops at a continuation and
// step() runs the rest of the turn on the continued conversation, so this only
// fires for a change racing the adoption itself.
var errContinuationAdopted = errors.New("a conversation-replacing tool call already ran this turn; re-issue this call in the next turn so it applies to the continued conversation")

// guardStateChange is called by the state rings before they mutate this MCP.
func (m *MCP) guardStateChange() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.continuation.Self() != nil {
		return errContinuationAdopted
	}
	return nil
}

// markStateChanged is called by the state rings after they mutate this MCP.
func (m *MCP) markStateChanged() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateChanged = true
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

// baseServer provides the schema for core tools and dispatch. Bound module
// tools retain their own defining schemas. Value workspaces use only core here;
// their modules are loaded from their trees during explicit agent composition.
// Live workspaces load their modules best-effort: a broken module is left out
// of the schema (and listed by FindArtifacts as a load failure) instead of
// failing every step.
func (m *MCP) baseServer(ctx context.Context) (*dagql.Server, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	var deps *SchemaBuilder
	switch {
	case m.workspace.Self() == nil:
		deps, err = query.CurrentServedDeps(ctx)
	case m.workspace.Self().IsValueWorkspace():
		deps, err = query.DefaultDeps(ctx)
	default:
		ctx, err = loadWorkspaceOwnerContext(ctx, m.workspace)
		if err != nil {
			return nil, err
		}
		deps, err = query.CurrentServedDeps(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("load tool base schema dependencies: %w", err)
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
	return m.withSkillsOwner(dir, "")
}

func (m *MCP) withSkillsOwner(dir dagql.ObjectResult[*Directory], owner string) *MCP {
	m = m.Clone()
	m.skillDirs = append(m.skillDirs, ownedSkillDirectory{Directory: dir, Owner: owner})
	return m
}

func (m *MCP) Tools(ctx context.Context) ([]LLMTool, error) {
	srv, err := m.baseServer(ctx)
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
	m.loadArtifactTools(srv, allTools)
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
	// Serve servers in a fixed order: the tool list is part of every
	// request's cached prefix, and a map walk would reshuffle it per call.
	for _, serverName := range slices.Sorted(maps.Keys(m.mcpSessions)) {
		sess := m.mcpSessions[serverName]
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

					// Keep native content intact; CallContent applies validation and
					// bounds text without ever truncating binary payloads.
					return res, nil
				},
			})
		}
	}
	return nil
}

const (
	// patchSummaryMaxPaths bounds the metadata candidates inspected before
	// deciding whether to materialize a patch or per-file diff stats. Large
	// regenerations and moves must not trigger content verification or rename
	// detection merely to decide that the summary is too large.
	patchSummaryMaxPaths = 200
	// patchSummaryMaxBytes bounds the patch summarizePatch will read into
	// memory to show verbatim. Larger patches fall back to diff stats without
	// loading the full contents (cf. llmToolLogsMaxBytes for tool logs).
	patchSummaryMaxBytes = 64 * 1024
	// patchSummaryMaxLines is the longest patch shown verbatim to the model;
	// anything longer becomes a diff-stat summary.
	patchSummaryMaxLines = 100
)

// changesetTooLarge checks a metadata upper bound before any full path
// computation, which would verify content and detect renames. An oversized
// result deliberately has no exact count or path list.
func changesetTooLarge(ctx context.Context, changes dagql.ObjectResult[*Changeset]) (bool, error) {
	return changes.Self().PathCountExceeds(ctx, patchSummaryMaxPaths)
}

func (m *MCP) summarizePatch(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) string {
	tooLarge, err := changesetTooLarge(ctx, changes)
	if err != nil {
		return fmt.Sprintf("WARNING: failed to bound changed paths: %s", err)
	}
	if tooLarge {
		return patchBudgetExceeded()
	}

	// Inspect stats before generating a patch. Keep these transient entries
	// local rather than publishing a dagql object per path for the preview.
	stats, err := changes.Self().DiffStats(ctx)
	if err != nil {
		return fmt.Sprintf("WARNING: failed to fetch patch summary: %s", err)
	}
	if len(stats) == 0 {
		return ""
	}
	if smallTextChangeset(stats) {
		var patch dagql.ObjectResult[*File]
		if err := srv.Select(ctx, changes, &patch, dagql.Selector{
			View: srv.View, Field: "asPatch",
		}); err == nil {
			if r, err := patch.Self().Open(ctx, patch); err == nil {
				preview, ok := readPatchPreview(r)
				r.Close()
				if ok {
					return preview
				}
			}
		}
	}
	return summarizeDiffStats(stats)
}

// summarizeAppliedPatch is summarizePatch for a changeset applied as a patch
// rendered against the workspace (Changeset.RenderPatchOnto): that patch is
// what actually changed in the workspace, so it is the one shown, within the
// same bounds, rather than a second render of the changeset's own.
func (m *MCP) summarizeAppliedPatch(ctx context.Context, changes dagql.ObjectResult[*Changeset], patch []byte) string {
	tooLarge, err := changesetTooLarge(ctx, changes)
	if err != nil {
		return fmt.Sprintf("WARNING: failed to bound changed paths: %s", err)
	}
	if tooLarge {
		return patchBudgetExceeded()
	}
	if preview, ok := readPatchPreview(bytes.NewReader(patch)); ok {
		return preview
	}
	stats, err := changes.Self().DiffStats(ctx)
	if err != nil {
		return fmt.Sprintf("WARNING: failed to fetch patch summary: %s", err)
	}
	if len(stats) == 0 {
		return ""
	}
	return summarizeDiffStats(stats)
}

func patchBudgetExceeded() string {
	return fmt.Sprintf("The change exceeds the %d-path inspection budget; patch omitted. File contents and renames were not inspected for this summary.", patchSummaryMaxPaths)
}

func summarizeDiffStats(stats []*DiffStat) string {
	const summaryWidth = 80

	entries := make([]patchpreview.Entry, len(stats))
	for i, s := range stats {
		entries[i] = patchpreview.Entry{Path: s.Path, Kind: string(s.Kind), Added: s.AddedLines, Removed: s.RemovedLines}
		if s.OldPath != nil {
			entries[i].OldPath = *s.OldPath
		}
	}
	return patchpreview.SummarizeString(entries, summaryWidth)
}

// smallTextChangeset avoids generating obviously large or binary patches.
// Renames are excluded because asPatch encodes them as delete+add, which can
// include an entire file even when numstat reports zero changed lines.
func smallTextChangeset(stats []*DiffStat) bool {
	lines := 0
	for _, stat := range stats {
		if strings.HasSuffix(stat.Path, "/") {
			// Directory entries accompany ordinary files but have no patch payload.
			continue
		}
		// Binary files and path-only fallback stats both report zero lines.
		if stat.Kind == DiffStatKindRenamed || (stat.AddedLines == 0 && stat.RemovedLines == 0) {
			return false
		}
		lines += stat.AddedLines + stat.RemovedLines
		if lines > patchSummaryMaxLines {
			return false
		}
	}
	return true
}

// readPatchPreview bounds bytes as well as lines: a single minified line can
// exceed File.contents' limit. One extra byte distinguishes a complete preview
// from truncation; binary payloads must never become automatic previews.
func readPatchPreview(r io.Reader) (string, bool) {
	buf, err := io.ReadAll(io.LimitReader(r, patchSummaryMaxBytes+1))
	if err != nil || len(buf) == 0 || len(buf) > patchSummaryMaxBytes {
		return "", false
	}
	patch := string(buf)
	if strings.Count(patch, "\n") > patchSummaryMaxLines || strings.Contains(patch, "\nGIT binary patch\n") {
		return "", false
	}
	return patch, true
}

const gitDiffContentType = "text/x-diff"

// toolResultContentType classifies authoritative Git patches returned by tools.
// Changeset.asPatch always starts with a diff --git header; summaries and other
// tool output do not. Keeping this classification at the point that emits the
// model-visible result lets every frontend render the same engine-side value
// without reconstructing edits from tool arguments.
func toolResultContentType(result string) string {
	if strings.HasPrefix(result, "diff --git ") {
		return gitDiffContentType
	}
	return ""
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

// applyStateReturn implements the state-mutation convention shared by tool calls
// and Dang eval results. Three kinds of value advance the agent's state:
//
//   - a Changeset overlays onto the bound workspace (via Workspace.withChanges,
//     yielding a new immutable overlay Workspace) so the agent's edits accumulate
//     across turns. It is evaluated first: one that cannot be (e.g. an edit
//     whose search string is ambiguous) fails as this call's own error and is
//     never applied.
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
		out, err := m.adoptLLM(ctx, srv, next)
		return true, out, err
	}
	if changes, ok := dagql.UnwrapAs[dagql.ObjectResult[*Changeset]](val); ok {
		// A tool's Changeset is usually lazy — an edit is a File.withReplaced
		// nobody has run yet — so evaluate it here: a broken one must fail
		// this call rather than surface later, from inside the workspace it
		// was overlaid onto.
		if changes.Self() != nil {
			if err := changes.Self().Evaluate(ctx); err != nil {
				return true, "", err
			}
		}
		out, err := m.applyChangeset(ctx, srv, changes)
		if err != nil {
			return true, "", err
		}
		return true, out, nil
	}
	if ws, ok := dagql.UnwrapAs[dagql.ObjectResult[*Workspace]](val); ok {
		out, err := m.rebindWorkspace(ctx, srv, ws)
		return true, out, err
	}
	return false, "", nil
}

// rebindWorkspace makes a tool-returned Workspace the LLM's current workspace,
// the sibling of applyChangeset for the replace (rather than overlay) case. It
// tells the model what the swap changed — see summarizeWorkspaceChange: a
// patch when the new workspace builds on the old one's base, a history move
// when both come from the same repository, and a plain replacement notice
// otherwise.
func (m *MCP) rebindWorkspace(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*Workspace]) (string, error) {
	if err := m.guardStateChange(); err != nil {
		return "", err
	}
	prev := m.workspace
	m.workspace = ws
	m.markStateChanged()
	if prev.Self() == nil {
		// No prior workspace to compare against (e.g. the LLM was unbound);
		// just adopt it without a change summary.
		return "Set the current workspace.", nil
	}
	return m.summarizeWorkspaceChange(ctx, srv, prev, ws)
}

// summarizeWorkspaceChange tells the model what a workspace swap changed. It
// classifies how the two workspaces relate FIRST (WorkspaceRelation) and only
// diffs files when they share a base, where the diff is bounded by the tool's
// own edits. Workspaces from the same git remote are reported as a history
// move (commits, not files), and unrelated ones as a replacement notice: a
// file diff between two different repositories, or two distant points of one,
// would upload the old host tree in full and hand the model a patch that
// describes nothing it can act on.
func (m *MCP) summarizeWorkspaceChange(ctx context.Context, srv *dagql.Server, prev, ws dagql.ObjectResult[*Workspace]) (string, error) {
	switch WorkspaceRelation(ctx, prev.Self(), ws.Self()) {
	case WorkspaceRelationSameBase:
		summary, err := m.summarizeWorkspaceEdits(ctx, srv, prev, ws)
		if err != nil {
			return "", err
		}
		if mounts := summarizeMountChanges(prev.Self(), ws.Self()); mounts != "" {
			if summary == "" {
				return mounts, nil
			}
			return mounts + "\n\n" + summary, nil
		}
		return summary, nil
	case WorkspaceRelationSameOrigin:
		return m.summarizeWorkspaceMove(ctx, srv, prev, ws), nil
	default:
		return summarizeWorkspaceReplaced(prev.Self(), ws.Self()), nil
	}
}

// summarizeWorkspaceEdits renders the patch between two workspaces on the
// same base. It goes through Workspace.changes(from:), which compares overlay
// changesets over a sparse host view (only the paths either side touched)
// instead of uploading both roots. Measure the summary from the workspace root:
// re-rooting changes to a nested cwd inspects every path before our budget check
// and rejects edits outside that cwd.
func (m *MCP) summarizeWorkspaceEdits(ctx context.Context, srv *dagql.Server, prev, ws dagql.ObjectResult[*Workspace]) (string, error) {
	if ws.Self().Cwd != "" && ws.Self().Cwd != "." {
		if err := srv.Select(ctx, ws, &ws, dagql.Selector{
			View: srv.View, Field: "withWorkdir",
			Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString(".")}},
		}); err != nil {
			return "", err
		}
	}
	prevID, err := prev.ID()
	if err != nil {
		return "", err
	}
	var changes dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, ws, &changes, dagql.Selector{
		View:  srv.View,
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.Opt(dagql.NewID[*Workspace](prevID))},
		},
	}); err != nil {
		slog.Warn("failed to compare workspaces; diffing roots instead", "error", err)
		return m.summarizeWorkspaceRootDiff(ctx, srv, prev, ws)
	}
	return m.summarizeWorkspaceDiff(ctx, srv, prev.Self(), ws.Self(), changes.Self().Before, changes.Self().After)
}

// summarizeWorkspaceRootDiff renders the patch from one workspace's root to
// another's. Only for workspaces on the same base: for anything else this
// uploads and diffs entire trees.
func (m *MCP) summarizeWorkspaceRootDiff(ctx context.Context, srv *dagql.Server, prev, ws dagql.ObjectResult[*Workspace]) (string, error) {
	before, err := workspaceRoot(ctx, srv, prev)
	if err != nil {
		return "", err
	}
	after, err := workspaceRoot(ctx, srv, ws)
	if err != nil {
		return "", err
	}
	return m.summarizeWorkspaceDiff(ctx, srv, prev.Self(), ws.Self(), before, after)
}

// summarizeWorkspaceDiff filters both sparse comparisons and the full-root
// fallback before inspecting paths. Mounted content is a read-only attachment,
// not a pending edit; Git metadata is not a working-tree edit either.
func (m *MCP) summarizeWorkspaceDiff(ctx context.Context, srv *dagql.Server, prev, ws *Workspace, before, after dagql.ObjectResult[*Directory]) (string, error) {
	excluded := append(unionMountPoints(prev, ws), ".git")
	var err error
	if before, err = withoutMountPoints(ctx, srv, before, excluded); err != nil {
		return "", err
	}
	if after, err = withoutMountPoints(ctx, srv, after, excluded); err != nil {
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

// unionMountPoints returns every root-relative path excluded from a workspace
// comparison, including mounts present on only one side of the transition.
func unionMountPoints(a, b *Workspace) []string {
	points := append(a.MountPoints(), b.MountPoints()...)
	slices.Sort(points)
	return slices.Compact(points)
}

// withoutMountPoints removes directories and files alike, ignoring missing
// paths, so file mounts and a worktree's .git file are excluded too.
func withoutMountPoints(ctx context.Context, srv *dagql.Server, dir dagql.ObjectResult[*Directory], points []string) (dagql.ObjectResult[*Directory], error) {
	for _, point := range points {
		if err := srv.Select(ctx, dir, &dir, dagql.Selector{
			View: srv.View, Field: "withoutDirectory",
			Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString(point)}},
		}); err != nil {
			return dir, fmt.Errorf("exclude workspace path %q: %w", point, err)
		}
	}
	return dir, nil
}

// summarizeMountChanges reports topology, never the attached files. Replacing
// content at an existing mount point does not turn it into a pending edit.
func summarizeMountChanges(prev, next *Workspace) string {
	before, after := prev.MountPoints(), next.MountPoints()
	var lines []string
	for _, point := range after {
		if !slices.Contains(before, point) {
			lines = append(lines, fmt.Sprintf("Mounted (read-only): %s", point))
		}
	}
	for _, point := range before {
		if !slices.Contains(after, point) {
			lines = append(lines, fmt.Sprintf("Unmounted: %s", point))
		}
	}
	return strings.Join(lines, "\n")
}

// workspaceMoveLogLimit caps the commits counted on each side of a workspace
// move; beyond it the count is reported as "N+".
const workspaceMoveLogLimit = 100

// summarizeWorkspaceMove describes a swap between two checkouts of the same
// repository as a move through its history rather than a file diff. It is
// best-effort throughout: resolving a local checkout's HEAD and counting the
// commits between the two heads both call out (host git, a fetch of both
// refs), and any failure degrades to whatever identity the workspace values
// carry on their own.
func (m *MCP) summarizeWorkspaceMove(ctx context.Context, srv *dagql.Server, prev, ws dagql.ObjectResult[*Workspace]) string {
	from, prevHead := describeWorkspaceWithHead(ctx, srv, prev)
	to, nextHead := describeWorkspaceWithHead(ctx, srv, ws)
	return renderWorkspaceMove(from, to, workspaceMoveCounts(ctx, srv, prevHead, nextHead))
}

// workspaceMoveDistance is the commit distance between two heads of the same
// repository, when it could be computed.
type workspaceMoveDistance struct {
	known         bool
	ahead, behind int
}

func renderWorkspaceMove(from, to WorkspaceIdentity, dist workspaceMoveDistance) string {
	location := to.Location()
	if to.Origin == "" && from.Origin != "" {
		location = from.Location()
	}
	revision := func(id WorkspaceIdentity) string {
		if rev := id.Revision(); rev != "" {
			return rev
		}
		if id.Address != "" {
			return id.Address
		}
		return "(unknown revision)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Workspace moved: %s %s -> %s.", location, revision(from), revision(to))
	if dist.known {
		count := func(n int) string {
			if n > workspaceMoveLogLimit {
				return fmt.Sprintf("%d+", workspaceMoveLogLimit)
			}
			return fmt.Sprint(n)
		}
		fmt.Fprintf(&b, " The new checkout is %s commit(s) ahead of and %s behind the previous one.", count(dist.ahead), count(dist.behind))
	}
	b.WriteString(" Files were not diffed; the previous checkout is no longer reachable through your tools.")
	return b.String()
}

// summarizeWorkspaceReplaced is the notice for a swap to an unrelated
// workspace, the workspace counterpart of "Conversation history replaced".
// Nothing is diffed: there is no shared base for a patch to be measured from.
func summarizeWorkspaceReplaced(prev, ws *Workspace) string {
	return fmt.Sprintf("Workspace replaced: %s -> %s. Files were not diffed; the previous workspace is no longer reachable through your tools.",
		DescribeWorkspace(prev), DescribeWorkspace(ws))
}

// describeWorkspaceWithHead returns a workspace's identity, filled in with its
// HEAD commit when Workspace.git.head can resolve one, and that head for
// callers that go on to compare histories. The head is zero when unavailable.
func describeWorkspaceWithHead(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*Workspace]) (WorkspaceIdentity, dagql.ObjectResult[*GitRef]) {
	id := DescribeWorkspace(ws.Self())
	var head dagql.ObjectResult[*GitRef]
	if err := srv.Select(ctx, ws, &head,
		dagql.Selector{View: srv.View, Field: "git"},
		dagql.Selector{View: srv.View, Field: "head"},
	); err != nil {
		slog.Debug("failed to resolve workspace head for move summary", "error", err)
		return id, dagql.ObjectResult[*GitRef]{}
	}
	return id.WithGitRef(head.Self()), head
}

// workspaceMoveCounts counts the commits each head has that the other lacks,
// via GitRef.log(base:), capped at workspaceMoveLogLimit+1 per side so the
// caller can say "100+". Unknown when either head is missing or a log fails.
func workspaceMoveCounts(ctx context.Context, srv *dagql.Server, prevHead, nextHead dagql.ObjectResult[*GitRef]) workspaceMoveDistance {
	if prevHead.Self() == nil || nextHead.Self() == nil {
		return workspaceMoveDistance{}
	}
	if prevHead.Self().Ref != nil && nextHead.Self().Ref != nil && prevHead.Self().Ref.SHA == nextHead.Self().Ref.SHA {
		return workspaceMoveDistance{known: true}
	}
	ahead, err := countCommitsSince(ctx, srv, nextHead, prevHead)
	if err != nil {
		slog.Debug("failed to count commits ahead for move summary", "error", err)
		return workspaceMoveDistance{}
	}
	behind, err := countCommitsSince(ctx, srv, prevHead, nextHead)
	if err != nil {
		slog.Debug("failed to count commits behind for move summary", "error", err)
		return workspaceMoveDistance{}
	}
	return workspaceMoveDistance{known: true, ahead: ahead, behind: behind}
}

func countCommitsSince(ctx context.Context, srv *dagql.Server, head, base dagql.ObjectResult[*GitRef]) (int, error) {
	baseID, err := base.ID()
	if err != nil {
		return 0, err
	}
	var commits dagql.ObjectResultArray[*GitCommit]
	if err := srv.Select(ctx, head, &commits, dagql.Selector{
		View:  srv.View,
		Field: "log",
		Args: []dagql.NamedInput{
			{Name: "limit", Value: dagql.NewInt(workspaceMoveLogLimit + 1)},
			{Name: "base", Value: dagql.Opt(dagql.NewID[*GitRef](baseID))},
		},
	}); err != nil {
		return 0, err
	}
	return len(commits), nil
}

// adoptLLM makes a tool-returned LLM the conversation the agent loop resumes
// from — the continuation ring of the state-return convention. The object-tool
// adapter passes the current conversation directly to the tool's hidden `LLM!`
// argument; the tool transforms it and returns the result. step() then appends
// this turn's tool results to the RETURNED LLM instead of the one that made the
// call, so the swap takes effect mid-turn without restarting the session.
//
// ANY LLM may be adopted — there is no lineage requirement. This mirrors
// rebindWorkspace, the sibling ring of the same convention: a tool may return
// any workspace at all, and what makes that safe is not prevention but
// VISIBILITY (a notice the model reads: a patch when the workspace builds on
// the old one, a history move or replacement notice when it does not — see
// summarizeWorkspaceChange). Continuations are written by
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
//   - one per MCP: LLMs do not compose the way Changesets do, so at most one
//     continuation may be adopted on an MCP. A later continuation in the same
//     turn runs on the continued conversation's MCP instead (see toolDispatch),
//     transforming the first one's result.
//   - visibility: the string returned here is the model's notice of what
//     changed — which tools came and went, and whether the conversation
//     history itself was replaced. A swap is never silent.
//
// Two mechanical details the caller handles rather than this function:
//
//   - ordering: a continuation runs in its written position. Before it runs,
//     step() folds the workspace and binding changes of the calls before it
//     into the conversation it receives, so a continuation transforms what
//     the turn produced; CallBatch then stops, and step() runs the calls
//     after it on the continued conversation. If one is adopted without that
//     fold (e.g. returned some other way than a ReturnsLLM tool) the
//     stateChanged check below refuses it rather than let it drop earlier
//     work.
//   - tool results: step() appends the turn's tool results to the adopted LLM,
//     and a tool-result block is only valid where the matching tool call exists
//     in the history. See toolResultSelectors in llm.go, which degrades an
//     orphaned result to a plain user message.
func (m *MCP) adoptLLM(ctx context.Context, srv *dagql.Server, next dagql.ObjectResult[*LLM]) (string, error) {
	if next.Self() == nil {
		return "", fmt.Errorf("cannot continue from a null LLM")
	}
	current := m.currentLLM()
	if current.Self() == nil {
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

	// Computed before taking the lock: it evaluates dagql calls.
	wsNote := m.summarizeContinuationWorkspace(ctx, srv, current.Self(), next.Self())

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.continuation.Self() != nil {
		return "", fmt.Errorf("a conversation-replacing tool call already ran this turn; only one is allowed")
	}
	if m.stateChanged {
		return "", fmt.Errorf("another state-changing tool call already ran this turn, and the conversation handed to this call does not reflect it; call the conversation-replacing tool in a turn of its own")
	}
	m.continuation = next
	summary := summarizeContinuation(current.Self(), next.Self(), before, after)
	if wsNote != "" {
		summary += "\n" + wsNote
	}
	return summary, nil
}

// summarizeContinuationWorkspace reports how the continuation's bound
// workspace differs from the one the tool was handed, the same way
// rebindWorkspace reports a workspace swap: a continuation derived from its
// input (a reload, or the input plus a marker file) shows just the delta,
// while one bound to another checkout of the same repository, or to an
// unrelated workspace, gets a history-move or replacement notice rather than
// a diff. Empty when the workspace is unchanged or either side has none.
func (m *MCP) summarizeContinuationWorkspace(ctx context.Context, srv *dagql.Server, current, next *LLM) string {
	if current == nil || next == nil || current.mcp == nil || next.mcp == nil {
		return ""
	}
	prev, ws := current.mcp.workspace, next.mcp.workspace
	if prev.Self() == nil || ws.Self() == nil {
		return ""
	}
	prevID, err := prev.ID()
	if err != nil {
		return ""
	}
	wsID, err := ws.ID()
	if err != nil {
		return ""
	}
	if stableIDDigest(prevID) == stableIDDigest(wsID) {
		return ""
	}
	summary, err := m.summarizeWorkspaceChange(ctx, srv, prev, ws)
	if err != nil {
		slog.Warn("failed to summarize continuation workspace change", "error", err)
		return "Workspace changed."
	}
	return "Workspace changed:\n" + summary
}

// historyPreserved reports whether next's message history still contains
// current's, unchanged, as a prefix — i.e. the conversation was transformed
// (install/reload) rather than replaced.
//
// This is NOT an acceptance condition: it informs the adoption summary, so
// the model is told when its history changed shape. Comparing histories by VALUE rather
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
	return slices.EqualFunc(a.Content, b.Content, llmContentEqual)
}

func llmContentEqual(a, b *LLMContentBlock) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Kind == b.Kind && a.Text == b.Text && a.CallID == b.CallID &&
		a.ToolName == b.ToolName && string(a.Arguments) == string(b.Arguments) &&
		a.Errored == b.Errored && a.Signature == b.Signature &&
		a.MIMEType == b.MIMEType && slices.Equal(a.Data, b.Data) &&
		slices.EqualFunc(a.Content, b.Content, llmContentEqual)
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

// applyChangeset overlays a Changeset onto the bound workspace, updates
// m.workspace to the new overlay Workspace, and tells the model what changed.
//
// How the overlay is recorded decides what restoring the conversation, and
// every commit, recompose and module load built from its workspace, replays.
// The changeset's own recipe is whatever its producer chose: a tool call, and
// often a container's output, e.g. a generator's source tree after codegen
// against a dev engine. So:
//
//   - A host-backed workspace reads the client's checkout, so a conversation
//     built on one cannot be reproduced anyway: the raw changeset is applied.
//   - Otherwise the workspace is already in the engine, and the changeset is
//     applied as a patch rendered against it (Changeset.RenderPatchOnto) with
//     Workspace.withPatchFile. The recorded overlay is the prior workspace plus
//     the patch: the producer is not replayed, and applying it compares no
//     trees. The patch is also what the model is shown. This holds for a file
//     edit's changeset too, though its own recipe would be cheap to replay:
//     that recipe diffs Before and After, a full-tree diff the first read of
//     the workspace would pay when Before is the whole root, while the patch
//     is rendered for the tool result anyway.
//   - Unless that patch is larger than EmbeddedPatchMaxBytes or carries a
//     binary file (see EmbedPatch): the patch is inlined in the recipe, and
//     so in every trace that records it, and a build output (`go build`,
//     `go test -c`) would fill telemetry with megabytes of base85. Such a
//     changeset is applied raw, so restoring the conversation reruns its
//     producer instead, assuming it is hermetic. Should it not be, the
//     patches recorded after it leave conflict markers where they no longer
//     fit, rather than fail the restore (see applyChangesetPatch).
//
// Either way, a changeset that touches .git is refused
// (refuseGitMetadataChanges).
//
// A tool's changeset is measured from the workspace cwd, as Workspace.changes
// measures them and `dagger generate` applies them: a changeset returned from
// a function applies wherever its caller stands. Workspace.withChanges and
// withPatchFile apply at the root, so it is placed at the cwd first.
func (m *MCP) applyChangeset(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) (string, error) {
	if m.workspace.Self() == nil {
		return "", fmt.Errorf("cannot apply changes: no workspace bound")
	}
	if err := m.guardStateChange(); err != nil {
		return "", err
	}
	// A successful command need not change any files. Do not retain its
	// Changeset (or even its Before recipe) as an overlay: that would make
	// restoring the conversation evaluate the command again. This bounded
	// check distinguishes directory-only edits from an actual no-op.
	if changed, err := changes.Self().PathCountExceeds(ctx, 0); err == nil && !changed {
		return "", nil
	}
	if err := refuseGitMetadataChanges(ctx, changes); err != nil {
		return "", err
	}
	ws := m.workspace.Self()
	root, inEngine := ws.SourceDirectory()
	inEngine = inEngine && !ws.ClientLocalBase()
	prefix := workspaceCwdPrefix(ws)

	if inEngine {
		out, err := m.applyChangesetPatch(ctx, srv, root, prefix, changes)
		if !PatchNotEmbeddable(err) {
			return out, err
		}
		slog.Debug("changeset patch not embeddable; applying the raw changeset",
			"reason", err, "max", EmbeddedPatchMaxBytes)
	}
	placed, err := changesetAt(ctx, srv, changes, prefix)
	if err != nil {
		return "", err
	}
	if err := m.overlayChangeset(ctx, srv, placed); err != nil {
		return "", err
	}
	return m.summarizePatch(ctx, srv, changes), nil
}

// refuseGitMetadataChanges fails for a changeset that touches a .git path, at
// the workspace root or nested (a vendored checkout's .git). Tools must not
// change git state through a changeset: as a patch `git apply` refuses such
// paths, while one carrying binary objects would be applied raw, so the
// outcome would hinge on the content. A tool that means to change git state
// returns a Workspace instead.
func refuseGitMetadataChanges(ctx context.Context, changes dagql.ObjectResult[*Changeset]) error {
	paths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("compute changeset paths: %w", err)
	}
	for _, p := range slices.Concat(paths.Added, paths.Modified, paths.AllRemoved) {
		if isGitMetadataPath(p) {
			return fmt.Errorf("changeset touches %q: tools must not modify .git; to change git state, return a Workspace instead (e.g. one built from a GitRepository with withContents)", strings.TrimSuffix(p, "/"))
		}
	}
	return nil
}

// isGitMetadataPath reports whether a changeset path is, or is beneath, a
// .git entry. A trailing slash (a directory) is ignored.
func isGitMetadataPath(p string) bool {
	for part := range strings.SplitSeq(strings.Trim(p, "/"), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

// workspaceCwdPrefix returns a workspace's cwd as a path relative to its root,
// "." for the root itself, whichever way the cwd is spelled ("", ".", "/",
// "sub" or "/sub").
func workspaceCwdPrefix(ws *Workspace) string {
	cwd := path.Clean("/" + ws.Cwd)
	if cwd == "/" {
		return "."
	}
	return strings.TrimPrefix(cwd, "/")
}

// changesetAt places a changeset measured from a directory of the workspace
// at that directory, for Workspace.withChanges, which applies at the root:
// both sides are copied there in otherwise empty trees.
func changesetAt(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset], prefix string) (dagql.ObjectResult[*Changeset], error) {
	if prefix == "." || prefix == "" {
		return changes, nil
	}
	place := func(dir dagql.ObjectResult[*Directory]) (dagql.ObjectResult[*Directory], error) {
		var placed dagql.ObjectResult[*Directory]
		dirID, err := dir.ID()
		if err != nil {
			return placed, err
		}
		err = srv.Select(ctx, srv.Root(), &placed,
			dagql.Selector{View: srv.View, Field: "directory"},
			dagql.Selector{View: srv.View, Field: "withDirectory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(prefix)},
				{Name: "source", Value: dagql.NewID[*Directory](dirID)},
			}},
		)
		return placed, err
	}
	var placed dagql.ObjectResult[*Changeset]
	before, err := place(changes.Self().Before)
	if err != nil {
		return placed, fmt.Errorf("place changeset at %q: %w", prefix, err)
	}
	after, err := place(changes.Self().After)
	if err != nil {
		return placed, fmt.Errorf("place changeset at %q: %w", prefix, err)
	}
	beforeID, err := before.ID()
	if err != nil {
		return placed, err
	}
	if err := srv.Select(ctx, after, &placed, dagql.Selector{
		View:  srv.View,
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*Directory](beforeID)},
		},
	}); err != nil {
		return placed, fmt.Errorf("place changeset at %q: %w", prefix, err)
	}
	return placed, nil
}

// overlayChangeset applies a changeset as is, with Workspace.withChanges.
func (m *MCP) overlayChangeset(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) error {
	changesID, err := changes.ID()
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
	m.markStateChanged()
	return nil
}

// applyChangesetPatch applies a changeset to an in-engine workspace as a patch
// rendered against the workspace's root, with Workspace.withPatchFile. The
// changeset applies at prefix, the workspace cwd it was measured from.
//
// The patch starts from the workspace's own content, so applying it
// reproduces what Workspace.withChanges would by construction: there is
// nothing to check and nothing to fall back to. Only a changeset that touches
// a workspace mount is refused, as withChanges refuses it; a patch that is too
// large or carries binary content fails with ErrPatchTooLarge or
// ErrPatchBinary, for the caller to apply it raw. Should git still refuse the
// patch, e.g. a file it would create beyond a symbolic link, the call fails
// and the bound workspace stays as it was.
//
// The patch is recorded with onConflict LEAVE_CONFLICT_MARKERS rather than
// FAIL. Applied now, it fits by construction; it can only stop fitting when
// the conversation is restored and an earlier step, applied raw (see
// applyChangeset), replays a producer that is not hermetic. Hunks that no
// longer fit then leave conflict markers instead of failing the whole
// restore; a file git cannot patch at all still fails it.
func (m *MCP) applyChangesetPatch(ctx context.Context, srv *dagql.Server, root dagql.ObjectResult[*Directory], prefix string, changes dagql.ObjectResult[*Changeset]) (string, error) {
	paths, err := changes.Self().ComputePaths(ctx)
	if err != nil {
		return "", fmt.Errorf("compute changeset paths: %w", err)
	}
	for _, p := range slices.Concat(paths.Added, paths.Modified, paths.AllRemoved) {
		p = path.Join(prefix, strings.TrimSuffix(p, "/"))
		if m.workspace.Self().MountedPath(p) {
			return "", fmt.Errorf("workspace path %q is a read-only mount and cannot be modified", p)
		}
	}
	rendered, err := changes.Self().RenderPatchOnto(ctx, root, prefix, EmbeddedPatchMaxBytes)
	if err != nil {
		if PatchNotEmbeddable(err) {
			return "", err
		}
		return "", fmt.Errorf("render changeset patch: %w", err)
	}
	if rendered.IsEmpty() {
		// The workspace already holds what the changeset would leave.
		return "", nil
	}

	newWS, err := ApplyPatchOnto(ctx, srv, m.workspace, rendered, "changeset.patch", PatchConflictLeaveMarkers)
	if err != nil {
		return "", err
	}
	// withPatchFile is lazy, and nothing else here runs it. Force the new
	// root now, so a patch git cannot apply fails this call and leaves the
	// binding where it was, rather than surfacing from whatever reads the
	// workspace next (often the next changeset, rendered against it).
	if newRoot, ok := newWS.Self().SourceDirectory(); ok && newRoot.Self() != nil {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return "", err
		}
		if err := cache.Evaluate(ctx, newRoot); err != nil {
			return "", fmt.Errorf("apply changeset patch to the workspace: %w", err)
		}
	}
	m.workspace = newWS
	m.markStateChanged()
	shown := rendered.Patch
	if len(rendered.RemovedFiles) > 0 {
		// The patch does not show what was removed; the stats do.
		shown = nil
	}
	return m.summarizeAppliedPatch(ctx, changes, shown), nil
}

// workspaceDirectory returns the bound workspace's root directory, for
// operations (like external MCP-server sync) that need a plain Directory.
func (m *MCP) workspaceDirectory(ctx context.Context, srv *dagql.Server) (dagql.ObjectResult[*Directory], error) {
	return workspaceRoot(ctx, srv, m.workspace)
}

// workspaceRoot returns the given workspace's root directory as a plain
// Directory, e.g. for diffing two workspaces. It addresses the root as "/":
// Workspace.directory resolves relative paths from the workspace cwd, so "."
// would return the cwd subtree and misalign the diff between two workspaces
// with different cwds, or a cwd-measured snapshot against a root-measured
// overlay.
func workspaceRoot(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*Workspace]) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]
	err := srv.Select(ctx, ws, &dir, dagql.Selector{
		View:  srv.View,
		Field: "directory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/")},
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
	_, err = m.applyChangeset(ctx, srv, changes)
	return err
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

// toolSpanAttrs are the attributes that identify a tool on its call span: the
// bare tool name, the server providing it, and whether the span hides itself
// in favor of its children.
//
// The tool-call display span sets them when it starts (see
// displayPhases.StartToolCall), not just when the tool runs: live telemetry
// exports a span only at start and end, so attributes set in between stay
// invisible until the tool finishes.
func toolSpanAttrs(tool *LLMTool) []attribute.KeyValue {
	toolName := tool.Name
	if tool.Server != "" {
		// External MCP tools may come prefixed `<server>_`; collision-namespaced
		// object tools are prefixed `<gqlFieldName(server)>_` (their Server is
		// the bound type name). Trim either so the span shows the bare tool name
		// alongside the server attribute.
		toolName = strings.TrimPrefix(toolName, tool.Server+"_")
		toolName = strings.TrimPrefix(toolName, gqlFieldName(tool.Server)+"_")
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.LLMToolAttr, toolName),
	}
	if tool.HideSelf {
		// Hide spans which are better represented by the child spans that they
		// spawn, i.e. CallMethod, ChainMethods, or direct object-method tools.
		attrs = append(attrs, attribute.Bool(telemetry.UIPassthroughAttr, true))
	}
	if tool.Server != "" {
		attrs = append(attrs, attribute.String(telemetry.LLMToolServerAttr, tool.Server))
	}
	return attrs
}

func toolArgHeaderValue(name string, value any) (string, bool) {
	if value == nil {
		return "", false
	}
	if str, ok := value.(string); ok {
		return str, true
	}
	switch name {
	case "offset", "limit":
		return fmt.Sprint(value), true
	case "args":
		values, ok := value.([]any)
		if !ok {
			return "", false
		}
		parts := make([]string, 0, len(values))
		for _, value := range values {
			parts = append(parts, fmt.Sprint(value))
		}
		return strings.Join(parts, " "), true
	default:
		return "", false
	}
}

// Call returns the plain-text projection for callers that only need text.
// Conversation dispatch uses CallContent so media never passes through this view.
func (m *MCP) Call(ctx context.Context, tools []LLMTool, toolCall *LLMToolCall) (string, bool) {
	result := m.CallContent(ctx, tools, toolCall)
	return result.ContentText(), result.Errored
}

// CallContent executes a tool and preserves its ordered, typed result blocks.
func (m *MCP) CallContent(ctx context.Context, tools []LLMTool, toolCall *LLMToolCall) (res *LLMContentBlock) {
	errorResult := func(text string) *LLMContentBlock {
		return &LLMContentBlock{Kind: LLMContentToolResult, CallID: toolCall.CallID, Text: guardToolResult(text), Errored: true}
	}
	tool, err := m.LookupTool(toolCall.Name, tools)
	if err != nil {
		return errorResult(err.Error())
	}

	args := map[string]any{}
	if len(toolCall.Arguments) > 0 {
		if err := json.Unmarshal(toolCall.Arguments, &args); err != nil {
			return errorResult(fmt.Sprintf("failed to parse tool arguments: %s", err))
		}
	}

	var toolArgNames []string
	var toolArgValues []string
	seenToolArgs := map[string]bool{}
	appendToolArg := func(name string) {
		if seenToolArgs[name] {
			return
		}
		val, ok := toolArgHeaderValue(name, args[name])
		if !ok {
			return
		}
		toolArgNames = append(toolArgNames, name)
		toolArgValues = append(toolArgValues, val)
		seenToolArgs[name] = true
	}
	if requiredArgs, ok := tool.Schema["required"].([]string); ok {
		for _, arg := range requiredArgs {
			appendToolArg(arg)
		}
	}
	// Header-specific optional values: Read's pagination controls and generic
	// argv arrays are useful context even though they are not required args.
	for _, arg := range []string{"offset", "limit", "args"} {
		appendToolArg(arg)
	}
	span := trace.SpanFromContext(ctx)
	attrs := append(toolSpanAttrs(tool),
		attribute.StringSlice(telemetry.LLMToolArgNamesAttr, toolArgNames),
		attribute.StringSlice(telemetry.LLMToolArgValuesAttr, toolArgValues),
	)
	span.SetAttributes(attrs...)

	var telemetryErr error
	defer telemetry.EndWithCause(span, &telemetryErr)
	defer func() {
		if res.Errored {
			telemetryErr = fmt.Errorf("tool call %q failed", tool.Name)
		}
	}()

	defer func() {
		// Validate media independently from text bounding: truncating base64
		// would corrupt it, and treating it as text would inflate token counts.
		if err := res.Validate(); err != nil {
			res = errorResult(fmt.Sprintf("invalid tool content: %s", err))
		}
		guardToolContent(res)

		emitToolResultLogs(ctx, res)
	}()

	toolCtx := context.WithValue(ctx, agentToolCallKey{}, true)
	if m.workspace.Self() != nil {
		// Bind the LLM's Workspace so the tool's contextual (+defaultPath) and
		// Workspace-typed args resolve against it, not the ambient workspace.
		toolCtx = WorkspaceToContext(toolCtx, m.workspace)
	}
	result, err := tool.Call(toolCtx, args)
	if err != nil {
		return errorResult(m.toolErrorResponse(ctx, err))
	}

	res = &LLMContentBlock{Kind: LLMContentToolResult, CallID: toolCall.CallID}
	switch v := result.(type) {
	case string:
		res.Text = v
	case *mcp.CallToolResult:
		res.Content, err = mcpContentBlocks(v)
		if err != nil {
			return errorResult(err.Error())
		}
		res.Errored = v.IsError
	case *LLMContentBlock:
		if v == nil {
			return errorResult("tool returned a nil content block")
		}
		if v.Kind == LLMContentToolResult {
			res = v.Clone()
			res.CallID = toolCall.CallID
		} else {
			res.Content = []*LLMContentBlock{v.Clone()}
		}
	case []*LLMContentBlock:
		for _, block := range v {
			if block == nil {
				return errorResult("tool returned a nil content block")
			}
			res.Content = append(res.Content, block.Clone())
		}
	default:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return errorResult(fmt.Sprintf("Failed to marshal result: %s", err))
		}
		res.Text = string(jsonBytes)
	}
	return res
}

// mcpContentBlocks translates MCP wire content without flattening media into
// text. Resource links remain descriptions, not implicitly fetched resources.
func mcpContentBlocks(result *mcp.CallToolResult) ([]*LLMContentBlock, error) {
	if result == nil {
		return nil, fmt.Errorf("MCP tool returned a nil result")
	}
	var blocks []*LLMContentBlock
	mediaBytes := 0
	for i, content := range result.Content {
		var block *LLMContentBlock
		var mediaData []byte
		switch c := content.(type) {
		case *mcp.TextContent:
			if c != nil {
				block = &LLMContentBlock{Kind: LLMContentText, Text: c.Text}
			}
		case *mcp.ImageContent:
			if c != nil {
				block = &LLMContentBlock{Kind: LLMContentImage, MIMEType: c.MIMEType}
				mediaData = c.Data
			}
		case *mcp.AudioContent:
			if c != nil {
				block = &LLMContentBlock{Kind: LLMContentAudio, MIMEType: c.MIMEType}
				mediaData = c.Data
			}
		case *mcp.EmbeddedResource:
			if c != nil && c.Resource != nil {
				r := c.Resource
				if r.Blob == nil {
					block = &LLMContentBlock{Kind: LLMContentText, Text: r.Text}
				} else {
					if r.Text != "" {
						return nil, fmt.Errorf("MCP resource %q contains both text and binary data", r.URI)
					}
					kind := LLMContentDocument
					switch {
					case strings.HasPrefix(r.MIMEType, "image/"):
						kind = LLMContentImage
					case strings.HasPrefix(r.MIMEType, "audio/"):
						kind = LLMContentAudio
					}
					block = &LLMContentBlock{Kind: kind, MIMEType: r.MIMEType}
					mediaData = r.Blob
				}
			}
		case *mcp.ResourceLink:
			if c != nil {
				block = &LLMContentBlock{Kind: LLMContentText, Text: fmt.Sprintf("[resource link: %s (%s)]\n%s\n%s", c.Name, c.MIMEType, c.URI, c.Description)}
			}
		default:
			return nil, fmt.Errorf("unsupported MCP content type %T at index %d", content, i)
		}
		if block == nil {
			return nil, fmt.Errorf("nil MCP content at index %d", i)
		}
		if block.Kind == LLMContentImage || block.Kind == LLMContentAudio || block.Kind == LLMContentDocument {
			if len(mediaData) > MaxLLMMediaBytes-mediaBytes {
				return nil, fmt.Errorf("MCP tool media exceeds %d bytes", MaxLLMMediaBytes)
			}
			mediaBytes += len(mediaData)
			block.Data = dagql.NewBytes(mediaData)
		}
		blocks = append(blocks, block)
	}
	if result.StructuredContent != nil {
		text, err := toolStructuredResponse(result.StructuredContent)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, &LLMContentBlock{Kind: LLMContentText, Text: text})
	}
	return blocks, nil
}

// guardToolContent shares one text budget across a result's child blocks.
// Media bytes are validated separately and are never truncated.
func guardToolContent(result *LLMContentBlock) {
	if len(result.Content) == 0 {
		result.Text = guardToolResult(result.Text)
		return
	}
	remaining := llmToolResultMaxBytes
	omitted := false
	bound := func(text string) string {
		if text == "" {
			return ""
		}
		if remaining < 256 {
			if omitted {
				return ""
			}
			omitted = true
			return "[additional tool text omitted; re-run the call more narrowly]"
		}
		text = guardText(text, textGuard{
			maxBytes:   remaining - 128,
			maxLineLen: llmLogsMaxLineLen,
			headBytes:  (remaining - 128) * 2 / 3,
			marker: func(lines, bytes int) string {
				return fmt.Sprintf("... %d lines (%d bytes) omitted (re-run the call more narrowly) ...", lines, bytes)
			},
		})
		remaining -= len(text)
		return text
	}
	result.Text = bound(result.Text)
	for _, block := range result.Content {
		if block.Kind == LLMContentText {
			block.Text = bound(block.Text)
		}
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

// toolCallCtx returns the display span context a tool call's arguments streamed
// into, so the tool's execution nests beneath it. Every provider — including
// the recorded-response provider — builds one display span per tool call (see
// displayPhases), so the fallback to ctx only applies when a provider returns
// no display spans (none today) or never announced the call ID.
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

// CallBatch runs a turn's tool calls in the order they were written, until one
// of them adopts a continuation. It returns one result per call it ran, in
// call order, and the calls written after the continuation, which it leaves
// for the caller to run on the continued conversation (see toolDispatch): a
// continuation replaces the conversation — its workspace, bindings and
// toolset — so the calls after it are written against that conversation, not
// this one. `[checkout, log]` logs the new checkout. When no continuation is
// adopted, every call runs and rest is empty.
//
// Models emit a turn's tool calls as an ordered list and read it as a script,
// so calls take effect in the order written:
//
//   - A pure call (LLMTool.ReadOnly) changes nothing, so a run of consecutive
//     pure calls executes concurrently.
//   - Any other call is a sequential step: it runs alone, in its position,
//     after everything written before it has landed — a Changeset it returns
//     is applied before the next call starts.
//   - Consecutive calls to one MCP server whose working directory mirrors the
//     workspace share a single sync (callBatchMCPServer), read-only ones
//     included, so they see the tree the calls before them produced. Within
//     the sync the same rules apply: pure runs concurrent, the rest one at a
//     time.
//   - Every call runs, whether or not an earlier one failed: each result
//     reports its own call, which is the contract models are trained on. A
//     call written against a failed one's effect fails on its own terms (the
//     edit's search string is missing, the test sees the old tree), and the
//     model reads both results together.
//   - A continuation (a call whose tool returns an LLM, see
//     isContinuationCall) is a sequential step like any other, in its
//     position. beforeContinuation, if set, runs right before it, so the
//     caller can hand it a conversation carrying the effects of the calls
//     before it. If that fails, the continuation doesn't run: it would
//     replace the conversation with one missing the turn's work.
func (m *MCP) CallBatch(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, toolCallDisplays map[string]toolCallDisplay, beforeContinuation func(context.Context) error) (results []*LLMMessage, rest []*LLMToolCall) {
	results = make([]*LLMMessage, len(toolCalls))
	position := make(map[*LLMToolCall]int, len(toolCalls))
	for i, call := range toolCalls {
		position[call] = i
	}
	record := func(call *LLMToolCall, result *LLMContentBlock) {
		endToolCallDisplay(toolCallDisplays, call.CallID, result.Errored, result.ContentText())
		results[position[call]] = &LLMMessage{
			Role:    LLMMessageRoleUser, // Anthropic only allows tool call results in user messages
			Content: []*LLMContentBlock{result},
		}
	}
	call := func(call *LLMToolCall) *LLMContentBlock {
		if beforeContinuation != nil && m.isContinuationCall(call, tools) {
			if err := beforeContinuation(ctx); err != nil {
				return &LLMContentBlock{
					Kind:    LLMContentToolResult,
					CallID:  call.CallID,
					Text:    fmt.Sprintf("not run: failed to carry this turn's changes into the conversation: %s", err),
					Errored: true,
				}
			}
		}
		return m.CallContent(toolCallCtx(ctx, toolCallDisplays, call.CallID), tools, call)
	}

	// runSteps executes a plan, handing each call's result to emit.
	var runSteps func(steps []batchStep, emit func(*LLMToolCall, *LLMContentBlock))
	runSteps = func(steps []batchStep, emit func(*LLMToolCall, *LLMContentBlock)) {
		for _, step := range steps {
			switch {
			case step.mcpServer != "":
				// One sync for the whole run; within it the calls follow the
				// same rules as the batch itself. Results are held back
				// until the sync has finished, since that is when the calls'
				// effects have (or haven't) reached the workspace.
				inner := m.planCalls(tools, step.calls, false)
				var mu sync.Mutex
				held := make(map[*LLMToolCall]*LLMContentBlock, len(step.calls))
				synced, err := m.callBatchMCPServer(ctx, step.mcpServer, func() {
					runSteps(inner, func(call *LLMToolCall, result *LLMContentBlock) {
						mu.Lock()
						defer mu.Unlock()
						held[call] = result
					})
				})
				if err != nil {
					annotateMCPSyncFailure(step.mcpServer, inner, held, synced, err)
				}
				for _, call := range step.calls {
					emit(call, held[call])
				}
			case step.pure:
				calls := pool.New()
				for _, c := range step.calls {
					calls.Go(func() { emit(c, call(c)) })
				}
				calls.Wait()
			default:
				emit(step.calls[0], call(step.calls[0]))
			}
		}
	}
	// The top-level steps run one at a time so the batch can stop at the
	// first continuation: the steps are consecutive runs of calls, so what
	// remains after it is a suffix of toolCalls.
	ran := 0
	for _, step := range m.planBatch(tools, toolCalls) {
		runSteps([]batchStep{step}, record)
		ran += len(step.calls)
		if m.Continuation().Self() != nil {
			break
		}
	}
	return results[:ran], toolCalls[ran:]
}

// isContinuationCall reports whether call is to a tool that returns an LLM — a
// continuation (see adoptLLM) — directly or wrapped in the Timeout builtin.
func (m *MCP) isContinuationCall(call *LLMToolCall, tools []LLMTool) bool {
	tool, err := m.planningTool(call, tools)
	return err == nil && tool.ReturnsLLM
}

// annotateMCPSyncFailure rewrites the results of an MCP server's calls after
// their workspace sync failed, so the model doesn't build on effects that
// never reached the workspace. If the calls ran unsynced (synced is false),
// every result is suspect: the server saw a stale tree and nothing it wrote
// was captured. If the sync failed after they ran, the reads stand but the
// changes the other calls made were not carried back.
func annotateMCPSyncFailure(server string, plan []batchStep, results map[*LLMToolCall]*LLMContentBlock, synced bool, err error) {
	for _, step := range plan {
		if synced && step.pure {
			continue
		}
		var note string
		if synced {
			note = fmt.Sprintf("WARNING: the changes this call made in MCP server %q could not be carried back into the workspace: %s", server, err)
		} else {
			note = fmt.Sprintf("WARNING: the workspace could not be synced into MCP server %q, so this call ran against a stale tree and any changes it made were not captured: %s", server, err)
		}
		for _, call := range step.calls {
			res := results[call]
			if res == nil {
				continue
			}
			if res.Text != "" {
				res.Text += "\n\n"
			}
			res.Text += note
			res.Errored = true
		}
	}
}

// batchStep is one step of a CallBatch schedule.
type batchStep struct {
	calls []*LLMToolCall
	// pure: the calls run concurrently. Otherwise the step is sequential and
	// holds a single call, unless mcpServer is set.
	pure bool
	// mcpServer: consecutive calls to this MCP server, run within a single
	// workspace sync. The calls themselves are planned again inside it, so
	// pure is unset here.
	mcpServer string
}

// planBatch groups calls, in the order written, into CallBatch steps: runs of
// consecutive pure calls, runs of consecutive calls to the same MCP server,
// and single sequential calls. A call to an unknown tool is sequential, the
// safe default for a call nothing is known about.
func (m *MCP) planBatch(tools []LLMTool, toolCalls []*LLMToolCall) []batchStep {
	return m.planCalls(tools, toolCalls, true)
}

// planCalls is planBatch, optionally without the MCP-server grouping: inside a
// server's sync the calls are planned by purity alone.
//
// A server's read-only calls join its run too. They read the server's working
// directory, which only mirrors the bound workspace while a sync is in
// progress; outside of one they would read whatever the last sync left there,
// not the tree the calls before them produced.
func (m *MCP) planCalls(tools []LLMTool, toolCalls []*LLMToolCall, groupServers bool) []batchStep {
	var steps []batchStep
	for _, call := range toolCalls {
		var pure bool
		var server string
		if tool, err := m.planningTool(call, tools); err == nil {
			pure = tool.ReadOnly
			// Object tools set Server to their bound type name for display,
			// so only a registered MCP server makes this an MCP tool.
			if _, isMCP := m.mcpServers[tool.Server]; isMCP && groupServers {
				server = tool.Server
			}
		}
		if n := len(steps); n > 0 {
			last := &steps[n-1]
			if server != "" && server == last.mcpServer {
				last.calls = append(last.calls, call)
				continue
			}
			if server == "" && pure && last.pure && last.mcpServer == "" {
				last.calls = append(last.calls, call)
				continue
			}
		}
		steps = append(steps, batchStep{calls: []*LLMToolCall{call}, pure: pure && server == "", mcpServer: server})
	}
	return steps
}

// planningTool returns the tool whose purity and server decide how call is
// scheduled: the tool it names, or, for the Timeout builtin, the tool it
// wraps. A deadline changes nothing about what the wrapped call reads or
// writes, so `Timeout(ReadLogs)` runs alongside other reads rather than as a
// barrier. A Timeout whose target can't be resolved plans as Timeout itself,
// sequential, and fails on its own when it runs.
func (m *MCP) planningTool(call *LLMToolCall, tools []LLMTool) (*LLMTool, error) {
	tool, err := m.LookupTool(call.Name, tools)
	if err != nil {
		return nil, err
	}
	if tool.Name != timeoutToolName {
		return tool, nil
	}
	if wrapped := m.timeoutTarget(call, tools); wrapped != nil {
		return wrapped, nil
	}
	return tool, nil
}

// timeoutTarget returns the tool a Timeout call wraps, or nil when its
// arguments don't name one that exists. Timeout itself reports the details
// when it runs; the planner only needs to know whether to schedule as the
// target.
func (m *MCP) timeoutTarget(call *LLMToolCall, tools []LLMTool) *LLMTool {
	var args struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(call.Arguments, &args) != nil || args.Tool == "" {
		return nil
	}
	wrapped, err := m.LookupTool(args.Tool, tools)
	if err != nil {
		return nil
	}
	return wrapped
}

// mcpServerSyncsWorkspace reports whether calls to the named MCP server run
// with the bound workspace synced into the server's working directory: the
// server is registered with a live session, its container has a working
// directory to mirror the workspace into, and there is a workspace to mirror.
func (m *MCP) mcpServerSyncsWorkspace(serverName string) bool {
	mcpSrv, ok := m.mcpServers[serverName]
	if !ok {
		return false
	}
	if _, ok := m.mcpSessions[serverName]; !ok {
		return false
	}
	if mcpSrv.Service.Self() == nil {
		return false
	}
	ctr := mcpSrv.Service.Self().Container
	if ctr.Self() == nil || ctr.Self().Config.WorkingDir == "" || ctr.Self().Config.WorkingDir == "/" {
		return false
	}
	// Snapshotting the workspace requires a bound workspace to diff against
	// and overlay back onto.
	return m.workspace.Self() != nil
}

// callBatchMCPServer runs calls to one MCP server — run executes them — with
// the bound workspace synced into the server's working directory, then
// overlays whatever the calls changed there back onto the workspace. run
// executes exactly once either way: when there is nothing to sync with (no
// working directory, no bound workspace) or the sync can't be set up, it runs
// without the sync, and synced reports which. A non-nil error means the sync
// failed — before run if synced is false, after it otherwise — and the caller
// tells the model, since the calls' results alone would misreport what
// reached the workspace.
func (m *MCP) callBatchMCPServer(ctx context.Context, serverName string, run func()) (synced bool, err error) {
	err = m.syncMCPServerWorkspace(ctx, serverName, func() {
		synced = true
		run()
	})
	if err != nil {
		slog.Error("failed to sync workspace with MCP server", "server", serverName, "synced", synced, "error", err)
	}
	if !synced {
		run()
	}
	return synced, err
}

// syncMCPServerWorkspace is callBatchMCPServer's sync. It returns without
// calling run when there is nothing to sync.
func (m *MCP) syncMCPServerWorkspace(ctx context.Context, serverName string, run func()) error {
	if !m.mcpServerSyncsWorkspace(serverName) {
		return nil
	}
	mcpSrv := m.mcpServers[serverName]
	ctr := mcpSrv.Service.Self().Container

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	serviceDigest, err := mcpSrv.Service.ContentPreferredDigest(ctx)
	if err != nil {
		return err
	}
	running, err := query.Services(ctx)
	if err != nil {
		return err
	}
	runningSvc, err := running.Get(ctx, serviceDigest, false)
	if err != nil {
		return err
	}
	srv, err := m.baseServer(ctx)
	if err != nil {
		return err
	}
	sourceDir, err := m.workspaceDirectory(ctx, srv)
	if err != nil {
		return err
	}

	snapshot, hasChanges, err := mcpSrv.Service.Self().runAndSnapshotChanges(
		ctx,
		runningSvc,
		ctr.Self().Config.WorkingDir,
		sourceDir,
		func() error {
			run()
			return nil
		})
	if err != nil {
		return err
	}
	if hasChanges {
		if err := m.applyWorkspaceSnapshot(ctx, srv, sourceDir, snapshot); err != nil {
			return fmt.Errorf("update workspace after MCP server calls: %w", err)
		}
	}
	return nil
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

// capturedLine is one assembled log line, tagged with whether it was printed
// by the captured span itself (or one of its direct children — where a tool
// function's own print output lands) rather than by nested work deeper in the
// subtree. Tool results keep direct output in full and abridge the rest.
type capturedLine struct {
	text      string
	direct    bool
	producer  logProducer
	timestamp int64 // timestamp of the first contributing record
}

// capturedSegment retains the producer of a single log record until assembly.
// A missing stdio stream is its own stream (zero), distinct from stdout/stderr.
type capturedSegment struct {
	text      string
	direct    bool
	producer  logProducer
	timestamp int64
}

type logProducer struct {
	traceID string
	spanID  string
	stream  int64
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

// logCaptureScope narrows the legacy causal scope to own or raw-descendant
// producers. A nil set leaves the causal capture unrestricted.
func logCaptureScope(db *clientdb.DB, spanID string, scope ...string) map[string]struct{} {
	if len(scope) == 0 || scope[0] == "causal" {
		return nil
	}
	if scope[0] != "descendants" {
		return map[string]struct{}{spanID: {}}
	}
	return db.SpanParentSubtree(spanID)
}

// captureLogLines assembles logs with producer and direct/nested provenance.
// excludeServiceLogs keeps long-lived service output out of tool results.
//
// The capture is a snapshot: its spans and log rows are resolved from the
// store's indexes once, up front, and the rows are then read in batches. Re-
// resolving the scope per batch made a capture quadratic in its size -- a CI
// trace's root (~220k spans, ~410k log rows) spent minutes re-walking its
// subtree and re-sorting its row IDs for every 1000-row page.
func (m *MCP) captureLogLines(ctx context.Context, spanID string, excludeServiceLogs, ownOnly bool, scope ...string) (capturedOutput, error) {
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
	q = inspectionStoreForSpan(q, spanID)

	// internalSpans skips subtrees hidden as internal, mirroring the TUI's
	// roll-up behavior. Its subtree is the capture's full log scope, even
	// when the producers are narrowed below: internal-ness is judged along
	// the same containment either way.
	logScope := q.SpanLogScope(spanID)
	internalSpans := newInternalSpanFilterInScope(q, spanID, excludeServiceLogs, logScope)
	producers := logScope
	if allowed := logCaptureScope(q, spanID, scope...); allowed != nil {
		producers = make(map[string]struct{}, len(allowed))
		for id := range allowed {
			if _, ok := logScope[id]; ok {
				producers[id] = struct{}{}
			}
		}
	}
	rowIDs := q.LogRowIDsForSpans(producers)

	// segments accumulates log bodies in database record order, retaining the
	// producer across batch boundaries. Records are NOT coalesced here:
	// appending onto an accumulated string goes quadratic on long runs, and
	// assembleLines merges same-producer fragments across records anyway.
	var segments []capturedSegment

	for start := 0; start < len(rowIDs); start += llmLogsBatchSize {
		logs, err := q.Read().SelectLogRows(ctx, rowIDs[start:min(start+llmLogsBatchSize, len(rowIDs))])
		if err != nil {
			return out, err
		}

		for _, log := range logs {
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
			var stream int64
		dance:
			for _, attr := range logAttrs {
				switch attr.Key {
				case telemetry.StdioStreamAttr:
					stream = attr.Value.GetIntValue()
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
			segments = append(segments, capturedSegment{
				text: text, direct: direct, timestamp: log.Timestamp,
				producer: logProducer{traceID: log.TraceID.String, spanID: log.SpanID.String, stream: stream},
			})
		}
	}
	out.lines = assembleLines(segments)
	return out, nil
}

// assembleLines joins fragments only within the same trace, span, and stdio
// stream. Completed lines are ordered by their newline's record; unterminated
// fragments are merged at their last contributing record. Lines from the same
// record keep their text order. This is record order, not timestamp order.
// A line retains the direct/nested attribution of its first actual text.
func assembleLines(segments []capturedSegment) []capturedLine {
	type partialLine struct {
		text      strings.Builder
		direct    bool
		last      int
		timestamp int64
	}
	type orderedLine struct {
		capturedLine
		record int
	}
	type producerKey struct {
		logProducer
		unknownRecord int
	}
	pending := map[producerKey]*partialLine{}
	var ordered []orderedLine
	for record, seg := range segments {
		key := producerKey{logProducer: seg.producer}
		if key.traceID == "" || key.spanID == "" {
			// Without a complete producer identity, even adjacent records
			// cannot safely be assumed to continue each other's output.
			key.unknownRecord = record + 1
		}
		p := pending[key]
		if p == nil {
			p = &partialLine{}
			pending[key] = p
		}
		chunks := strings.Split(seg.text, "\n")
		for i, chunk := range chunks {
			if chunk != "" {
				if p.text.Len() == 0 {
					p.direct = seg.direct
					p.timestamp = seg.timestamp
				}
				p.text.WriteString(chunk)
				p.last = record
			}
			if i < len(chunks)-1 {
				// A newline completes only this producer's pending line.
				direct, timestamp := seg.direct, seg.timestamp
				if p.text.Len() > 0 {
					direct, timestamp = p.direct, p.timestamp
				}
				ordered = append(ordered, orderedLine{capturedLine{p.text.String(), direct, seg.producer, timestamp}, record})
				p.text.Reset()
			}
		}
	}
	for key, p := range pending {
		if p.text.Len() > 0 {
			ordered = append(ordered, orderedLine{capturedLine{p.text.String(), p.direct, key.logProducer, p.timestamp}, p.last})
		}
	}
	// Each record belongs to exactly one producer, so trailing fragments have
	// distinct positions. Stable sorting keeps any completed lines from that
	// record before its trailing fragment, independent of map iteration order.
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].record < ordered[j].record })
	var lines []capturedLine
	for _, line := range ordered {
		lines = append(lines, line.capturedLine)
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
// captureLogLines doesn't re-walk the parent chain for every log line.
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
	// classified memoizes classifyLogSpan per span: a capture asks about the
	// same span once per log record, and each answer costs a span read plus
	// an attribute decode.
	classified map[logSpanKey]logSpanClass
}

type logSpanKey struct{ traceID, spanID string }

type logSpanClass struct{ hidden, direct bool }

func newInternalSpanFilter(db *clientdb.DB, rootSpanID string, skipServices bool) *internalSpanFilter {
	return newInternalSpanFilterInScope(db, rootSpanID, skipServices, db.SpanLogScope(rootSpanID))
}

// newInternalSpanFilterInScope is newInternalSpanFilter over a log scope the
// caller already resolved.
func newInternalSpanFilterInScope(db *clientdb.DB, rootSpanID string, skipServices bool, subtree map[string]struct{}) *internalSpanFilter {
	return &internalSpanFilter{
		db:           db,
		root:         rootSpanID,
		skipServices: skipServices,
		memo:         map[string]bool{},
		subtree:      subtree,
		classified:   map[logSpanKey]logSpanClass{},
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
	f.classified = map[logSpanKey]logSpanClass{}
}

// classifyLogSpan locates a log record's span and decides how the capture
// treats its logs: hidden entirely (an LLM span's own prompt/response noise,
// or a span beneath one hidden as internal), or kept — and, when kept,
// whether the record counts as the captured root's direct output (the root
// itself or one of its direct children, where a tool function's own print
// output lands). Answers are memoized per span.
func (f *internalSpanFilter) classifyLogSpan(ctx context.Context, traceID, spanID string) (hidden, direct bool, err error) {
	key := logSpanKey{traceID: traceID, spanID: spanID}
	if class, ok := f.classified[key]; ok {
		return class.hidden, class.direct, nil
	}
	hidden, direct, err = f.classifyLogSpanUncached(ctx, traceID, spanID)
	if err != nil {
		return false, false, err
	}
	f.classified[key] = logSpanClass{hidden: hidden, direct: direct}
	return hidden, direct, nil
}

func (f *internalSpanFilter) classifyLogSpanUncached(ctx context.Context, traceID, spanID string) (hidden, direct bool, err error) {
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

// toolErrorResponse connects failures to the same bounded log capture used by
// successful tool results. Errors bypass routeObjectMethodResult, and Call's
// defer only writes the result to telemetry; neither captures failure logs.
// Scope to the first useful error origin, not an ambient MCP session, and keep
// the original message and trace marker even when telemetry is unavailable.
func (m *MCP) toolErrorResponse(ctx context.Context, err error) string {
	response := err.Error()
	for _, origin := range telemetry.ParseErrorOrigins(response) {
		if logs := m.spanResult(ctx, origin.SpanID().String(), toolCallReportOpts()); logs != "" {
			return response + "\n\n" + ansi.Strip(logs)
		}
	}
	return response
}

func (m *MCP) loadBuiltins(srv *dagql.Server, allTools *LLMToolSet) {
	allTools.Add(LLMTool{
		Name: "LoadTrace",
		Description: "Load a historical trace from Dagger Cloud into this session for inspection. Use this when asked to investigate a trace ID, Cloud trace URL, or `dagger cloud traces view <id>`; do not run the interactive CLI.\n" +
			"Uses the connecting client's Cloud authentication. No recipes are executed or agents restored. Returns root span IDs for ReadTrace and ReadLogs; FindSpans, FindCalls and InspectCall also see loaded traces. Repeated loads reuse the snapshot; failed loads import nothing.",
		ReadOnly: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"trace": map[string]any{"type": "string", "description": "Trace ID (32 hex characters), a pasted dagger cloud traces view <id> command, or a Dagger Cloud trace URL."},
			},
			"required":             []string{"trace"},
			"additionalProperties": false,
		},
		Call: m.loadTraceTool(srv),
	})
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
					"description": "Maximum output lines including context (capped at 500 and a byte budget). Reads the tail by default; fromLine reads forward. Returned calls page earlier/next.",
					"minimum":     1,
					"default":     100,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Number of lines to skip from the end. If not specified, starts from the end.",
					"minimum":     0,
				},
				"fromLine": map[string]any{
					"type": "integer", "minimum": 1,
					"description": "One-based line address to read forward from (before grep); mutually exclusive with offset. Use with limit for a range. Stable for a fixed historical span/scope; live logs may grow.",
				},
				"context": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 100, "default": 0,
					"description": "Lines before and after each grep match. Overlapping windows merge; limit includes context.",
				},
				"scope": map[string]any{
					"type": "string", "enum": []string{"own", "descendants", "causal"}, "default": "causal",
					"description": "own: only this producer; descendants: raw parent-child subtree; causal: also cause-linked work (legacy broad scope, not proof of error causality). Internal log filtering still applies.",
				},
				"grep": map[string]any{
					"type":        "string",
					"description": "Regex over log text; context includes neighboring lines. Addresses and producer span/stream/time are retained.",
				},
			},
			"required":             []string{"span"},
			"additionalProperties": false,
		},
		Call: m.readLogsTool(srv),
	})
	allTools.Add(LLMTool{
		Name: "ReadTrace",
		Description: "Read the trace of this session or a trace imported with LoadTrace at a span, in one of three views." + "\n" +
			"- report (default): a bounded trace report with recorded error origins first, failed check/test links separately, and diagnostic output. Failed scopes collapse successful work; follow the supplied span IDs to expand a branch rather than searching again." + "\n" +
			"- inspect: one span in depth -- status, error and its origins, timing, the flags that shape how the UI treats it (internal, passthrough, roll-up, ...), the parent chain up to the root, and its direct children. Use it to navigate up and down from a span, or to answer why a span is hidden or its logs didn't show." + "\n" +
			"- timings: the span's raw-parent subtree as a chronological wall-time table (span, parent, start offset, duration, name), internal spans included; cause links are not traversed. Durations are each span's own wall interval, may overlap, and are not total execution or CPU self time. Imported spans with unrecorded completion have unknown duration." + "\n" +
			"Pass a span ID from a report's footer or use FindSpans first to find a check, test, service, or other step by name, then pass its span ID here." + "\n" +
			"Use ReadLogs when you want the raw log lines beneath a span.",
		ReadOnly: true, // Read-only operation
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"span": map[string]any{
					"type":        "string",
					"description": "Span ID (hex) to read, scoped to that span's subtree.",
				},
				"view": map[string]any{
					"type":        "string",
					"enum":        []string{traceViewReport, traceViewInspect, traceViewTimings},
					"description": "Which view to render.",
					"default":     traceViewReport,
				},
				"minDuration": map[string]any{
					"type":        "string",
					"description": "timings view only: hide spans shorter than this Go duration, e.g. \"10ms\" or \"1s\". Spans with unknown timing are kept.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "timings view only: maximum number of rows (0 = unlimited).",
					"minimum":     0,
					"default":     200,
				},
			},
			"required":             []string{"span"},
			"additionalProperties": false,
		},
		Call: m.readTraceTool(srv),
	})
	allTools.Add(LLMTool{
		Name: "FindSpans",
		Description: "Find spans in this session and traces imported with LoadTrace by name: one line per match -- span ID, status (ERROR: the span errored; FAIL: a failure rides on one of its links; run; ok), name -- oldest start time first (ties: trace ID, then span ID; unknown starts first), with running services tagged by hostname." + "\n" +
			"This is how you get a span ID for something you didn't get a handle to: a step you saw in a report, a service, a check or test, a nested call. Then ReadTrace (report, inspect, timings) or ReadLogs it." + "\n" +
			"Matching is a substring test on span name, full test identity/ancestor path, or service hostname. Rows include bounded breadcrumbs; matching uses the full text. Empty query matches all. Only the newest limit matches are returned; offset skips that many newest matches after filtering, and the result gives an exact next-page call. Status filtering is navigation, never proof of error causality.",
		ReadOnly: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Substring of span name, full test identity/ancestor path, or service hostname. Empty matches every span.",
					"default":     "",
				},
				"span": map[string]any{
					"type":        "string",
					"description": "Restrict the search to this span's subtree (hex span ID). Empty searches the whole session.",
				},
				"status": map[string]any{
					"type": "string", "enum": []string{"", "ERROR", "FAIL", "failed", "run", "ok"}, "default": "",
					"description": "Filter by displayed span status; failed includes ERROR and FAIL. Does not establish causality or classify a span as a test.",
				},
				"offset": map[string]any{
					"type": "integer", "minimum": 0, "default": 0,
					"description": "Matching spans to skip from newest after query/status filtering. Use the returned next-page call; ordering is fixed for historical traces, live results may grow.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum matches to return; the newest are kept (also bounded by a byte budget).",
					"minimum":     1,
					"default":     findSpansDefaultLimit,
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		Call: m.findSpansTool(srv),
	})
	allTools.Add(LLMTool{
		Name: "InspectCall",
		Description: "Inspect the recipe (dagql call ID) behind a call in this session or a trace imported with LoadTrace: the whole chain of API calls that produced a value, rebuilt from telemetry." + "\n" +
			"Pass the call digest (xxh3:...) named by an engine error, a FindCalls line, or ReadTrace's inspect view -- or the span ID of the call. Views:" + "\n" +
			"- chain (default): every selector on the receiver chain, as the TUI renders it." + "\n" +
			"- tree: the chain with ID-valued arguments expanded inline (each withDirectory/withTools/... argument hangs a whole other chain), numbered, with digests." + "\n" +
			"- stats: distinct calls, chain depth, module provenance, and per-call expansion counts -- how many times a loader that walks the recipe without deduplicating would re-execute each call. The view for \"why did this run that call N times?\"." + "\n" +
			"- find: every call in the recipe whose Type.field name matches `find` (a regexp), with its path, arguments and referrers." + "\n" +
			"`diff` structurally compares this recipe against another digest's instead: size, where the chains diverge, calls only on either side. Use it for \"why did this miss the cache / how do these two differ\"." + "\n" +
			"A frame the client never received is reported with the frame that referenced it.",
		ReadOnly: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"digest": map[string]any{
					"type":        "string",
					"description": "Call digest to inspect, e.g. \"xxh3:9d2f...\".",
				},
				"span": map[string]any{
					"type":        "string",
					"description": "Alternatively, the span ID (hex) of the call to inspect.",
				},
				"view": map[string]any{
					"type":        "string",
					"enum":        []string{callViewChain, callViewTree, callViewStats, callViewFind},
					"description": "Which view to render.",
					"default":     callViewChain,
				},
				"find": map[string]any{
					"type":        "string",
					"description": "find view only: regexp matched against each call's Type.field name (e.g. \"withExec\" or \"Container\\\\.from\").",
				},
				"depth": map[string]any{
					"type":        "integer",
					"description": "tree view only: recurse at most this many levels into ID arguments (0 = unlimited).",
					"minimum":     0,
					"default":     0,
				},
				"diff": map[string]any{
					"type":        "string",
					"description": "Digest of another call to structurally diff this one against (ignores `view`).",
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		Call: m.inspectCallTool(srv),
	})
	allTools.Add(LLMTool{
		Name: "FindCalls",
		Description: "Content-search every dagql call in this session and traces imported with LoadTrace: one line per match -- \"<digest>  field(args) -> Type  recv=<receiver digest>\" -- sorted by digest." + "\n" +
			"This is how you find which call references a path, image, module or value, and how you walk a chain: grep for the digest another line names as its receiver or argument, then InspectCall it." + "\n" +
			"`query` searches the full rendered line before truncation. Displayed strings are capped at 200 characters, with excerpts around deep matches and explicit omitted-character counts. Lines are capped at 4 KiB and responses at 32 KiB; narrow the query if matches are omitted.",
		ReadOnly: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Regexp matched against each full call line before display truncation.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum matches to return.",
					"minimum":     1,
					"default":     findCallsDefaultLimit,
				},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
		Call: m.findCallsTool(srv),
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
		Call: m.listServicesTool(srv),
	})
	allTools.Add(LLMTool{
		Name:        timeoutToolName,
		Description: "Run one currently exposed tool with a timeout.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"duration": map[string]any{
					"type":        "string",
					"description": "Maximum duration for the nested tool as a Go duration string, for example \"30s\" or \"2m\".",
				},
				"tool": map[string]any{
					"type":        "string",
					"description": "Name of the currently exposed tool to invoke.",
				},
				"arguments": map[string]any{
					"type":                 "object",
					"description":          "Arguments to pass to the nested tool.",
					"additionalProperties": true,
				},
			},
			"required":             []string{"duration", "tool", "arguments"},
			"additionalProperties": false,
		},
		Call: m.timeoutTool(allTools),
	})
}

// timeoutToolName is the builtin that runs another tool under a deadline. The
// planner schedules a Timeout call as the tool it wraps (planningTool).
const timeoutToolName = "Timeout"

// timeoutTool runs one of the currently exposed tools under a deadline. The
// nested call is dispatched the way the loop dispatches every tool call, so it
// shows up in the trace as a tool call of its own beneath the Timeout call.
func (m *MCP) timeoutTool(allTools *LLMToolSet) LLMToolFunc {
	return func(ctx context.Context, rawArgs any) (any, error) {
		args, ok := rawArgs.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid arguments: %T", rawArgs)
		}
		durationArg, ok := args["duration"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid duration: expected string")
		}
		duration, err := time.ParseDuration(durationArg)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout duration %q: %w", durationArg, err)
		}
		toolName, ok := args["tool"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid tool: expected string")
		}
		toolArgs, ok := args["arguments"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid nested tool arguments: expected object")
		}
		if _, err := m.LookupTool(toolName, allTools.Order); err != nil {
			return nil, err
		}
		encodedArgs, err := json.Marshal(toolArgs)
		if err != nil {
			return nil, fmt.Errorf("encode nested tool arguments: %w", err)
		}
		ctx, cancel := context.WithTimeout(ctx, duration)
		defer cancel()

		// Run the nested call as the loop runs every tool call: under a
		// tool-call display span of its own, so the trace shows it as the
		// tool call it is with its arguments and logs rolled up beneath it,
		// and through MCP.Call, for the tool attributes, workspace binding and
		// result bounding that path applies.
		call := &LLMToolCall{CallID: toolName, Name: toolName, Arguments: JSON(encodedArgs)}
		displays := newDisplayPhases(ctx, "", allTools.Order)
		displays.EmitToolCall(0, call.CallID, toolName, string(encodedArgs))
		res := m.CallContent(toolCallCtx(ctx, displays.toolCalls, call.CallID), allTools.Order, call)
		endToolCallDisplay(displays.toolCalls, call.CallID, res.Errored, res.ContentText())
		if res.Errored && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("tool %q did not finish within %s: %w", toolName, duration, ctx.Err())
		}
		// Keep the legacy string return for ordinary tools, but forward media
		// and their error status intact through wrappers such as Timeout.
		if len(res.Content) == 0 {
			if res.Errored {
				return nil, errors.New(res.Text)
			}
			return res.Text, nil
		}
		return res, nil
	}
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
		Span     string
		Offset   int    `default:"0"`
		Limit    int    `default:"100"`
		Grep     string `default:""`
		FromLine int    `default:"0"`
		Context  int    `default:"0"`
		Scope    string `default:"causal"`
	}) (any, error) {
		if args.Scope != "causal" && args.Scope != "descendants" && args.Scope != "own" {
			return nil, fmt.Errorf("invalid scope %q: want own, descendants or causal", args.Scope)
		}
		opts := logPageOpts{args.Scope, args.Grep, args.Offset, args.Limit, args.FromLine, args.Context}
		if err := validateLogPageOpts(opts); err != nil {
			return nil, err
		}
		spanID := normalizeSpanArg(args.Span)
		if _, err := trace.SpanIDFromHex(spanID); err != nil {
			return nil, fmt.Errorf("invalid span ID %q: %w", spanID, err)
		}
		// Include service logs: ReadLogs is the deliberate affordance for
		// reading them (e.g. via span IDs from ListServices).
		logs, err := m.captureLogLines(ctx, spanID, false, false, args.Scope)
		if err != nil {
			return nil, fmt.Errorf("failed to capture logs: %w", err)
		}
		if len(logs.lines) == 0 {
			result, err := m.emptyLogsResult(ctx, spanID)
			return fmt.Sprintf("scope=%s: %s", args.Scope, result), err
		}
		return renderLogPage(spanID, logs.lines, opts)
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

// emptyLogsResult distinguishes quiet live telemetry from a fixed historical
// capture. Failure to inspect the store must not masquerade as "no logs".
func (m *MCP) emptyLogsResult(ctx context.Context, spanID string) (string, error) {
	store, err := traceReportClientDB(ctx)
	if err != nil {
		return "", fmt.Errorf("check log availability for span %s: %w", spanID, err)
	}
	defer store.Close()
	return emptyLogsResultIn(store, spanID)
}

func emptyLogsResultIn(store *clientdb.DB, spanID string) (string, error) {
	selected := inspectionStoreForSpan(store, spanID)
	if !selected.HasSpan(spanID) {
		return "", fmt.Errorf("span %q not found in this session's telemetry; use a span ID from a tool result, ListServices, or an error's traceparent", spanID)
	}
	if selected != store {
		return fmt.Sprintf("(no logs recorded beneath span %s in this historical trace)", spanID), nil
	}
	return fmt.Sprintf("(no logs beneath span %s yet)", spanID), nil
}

// readTraceTool reads the trace at a span in the requested
// view. The report view renders the pretty trace report in the same shape a
// tool call's own result is rendered as (the target's own output, then the
// report), so what the reader gets back is in the vocabulary it already
// sees, just scoped to the target it asked about. The inspect and timings
// views are the TUI console's span views, answered from the engine.
func (m *MCP) readTraceTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Span        string
		View        string `default:"report"`
		MinDuration string `default:""`
		Limit       int    `default:"200"`
	}) (any, error) {
		var minDuration time.Duration
		if args.MinDuration != "" {
			var err error
			minDuration, err = time.ParseDuration(args.MinDuration)
			if err != nil || minDuration < 0 {
				return nil, fmt.Errorf("invalid minDuration %q: want a non-negative Go duration such as \"10ms\"", args.MinDuration)
			}
		}
		switch args.View {
		case traceViewReport, traceViewInspect, traceViewTimings:
		default:
			return nil, fmt.Errorf("unknown view %q: want %s, %s or %s", args.View, traceViewReport, traceViewInspect, traceViewTimings)
		}
		spanID, err := resolveTraceTarget(ctx, args.Span)
		if err != nil {
			return nil, fmt.Errorf("resolve trace target: %w", err)
		}
		switch args.View {
		case traceViewInspect:
			return inspectSpan(ctx, spanID)
		case traceViewTimings:
			return spanTimings(ctx, spanID, minDuration, args.Limit)
		}
		result, err := m.inspectSpanResult(ctx, spanID, readTraceReportOpts())
		if err != nil {
			return nil, err
		}
		if result != "" {
			return result, nil
		}
		// A subtree can legitimately render to nothing (dagui hides internal,
		// passthrough and encapsulated spans); say so rather than returning an
		// empty result, and point at the paths that do show something.
		return fmt.Sprintf("(no trace report for span %s; use ReadLogs(span: %s) to read its logs, or ReadTrace(span: %s, view: \"inspect\") to see the span itself)",
			spanID, spanID, spanID), nil
	})
}

// findSpansTool searches the session's trace by span name; see findSpans.
func (m *MCP) findSpansTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Query  string `default:""`
		Span   string `default:""`
		Limit  int    `default:"100"`
		Status string `default:""`
		Offset int    `default:"0"`
	}) (any, error) {
		root := ""
		if strings.TrimSpace(args.Span) != "" {
			root = normalizeSpanArg(args.Span)
			if !isHexID(root, 16) {
				return nil, fmt.Errorf("invalid span ID %q: want a hex span ID", args.Span)
			}
		}
		if args.Limit <= 0 {
			args.Limit = findSpansDefaultLimit
		}
		return findSpans(ctx, args.Query, root, args.Status, args.Limit, args.Offset)
	})
}

// inspectCallTool rebuilds and renders the recipe behind a call digest or a
// call's span; see inspectCall.
func (m *MCP) inspectCallTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Digest string `default:""`
		Span   string `default:""`
		View   string `default:"chain"`
		Find   string `default:""`
		Depth  int    `default:"0"`
		Diff   string `default:""`
	}) (any, error) {
		opts := callInspectOpts{View: args.View, Depth: args.Depth, Diff: normalizeDigestArg(args.Diff)}
		switch args.View {
		case callViewChain, callViewTree, callViewStats, callViewFind:
		default:
			return nil, fmt.Errorf("unknown view %q: want %s, %s, %s or %s", args.View, callViewChain, callViewTree, callViewStats, callViewFind)
		}
		if args.Find != "" {
			re, err := regexp.Compile(args.Find)
			if err != nil {
				return nil, fmt.Errorf("invalid find pattern %q: %w", args.Find, err)
			}
			opts.Find = re
			if args.View == callViewChain {
				// A pattern implies the view that uses it.
				opts.View = callViewFind
			}
		}
		digest := normalizeDigestArg(args.Digest)
		span := strings.TrimSpace(args.Span)
		switch {
		case digest != "" && span != "":
			return nil, fmt.Errorf("pass either digest or span, not both")
		case digest == "" && span == "":
			return nil, fmt.Errorf("pass a call digest (xxh3:...) or the span ID of a call")
		case span != "":
			span = normalizeSpanArg(span)
			if !isHexID(span, 16) {
				return nil, fmt.Errorf("invalid span ID %q: want a hex span ID", args.Span)
			}
			clientDB, err := traceReportClientDB(ctx)
			if err != nil {
				return nil, err
			}
			defer clientDB.Close()
			read := inspectionStoreForSpan(clientDB, span)
			digest, err = spanCallDigest(ctx, read, span)
			if err != nil {
				return nil, err
			}
			return inspectCallIn(ctx, clientDB, digest, opts)
		}
		return inspectCall(ctx, digest, opts)
	})
}

// normalizeDigestArg accepts the forms a call digest gets pasted in: bare,
// or with the "digest=" / "load " prefixes engine errors and tool output
// wrap it in.
func normalizeDigestArg(arg string) string {
	arg = strings.TrimSpace(arg)
	for _, prefix := range []string{"digest=", "digest:", "load "} {
		arg = strings.TrimPrefix(arg, prefix)
	}
	return strings.TrimSpace(arg)
}

// findCallsTool content-searches the session's calls; see findCalls.
func (m *MCP) findCallsTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Query string
		Limit int `default:"200"`
	}) (any, error) {
		re, err := regexp.Compile(args.Query)
		if err != nil {
			return nil, fmt.Errorf("invalid query %q: %w", args.Query, err)
		}
		if args.Limit <= 0 {
			args.Limit = findCallsDefaultLimit
		}
		return findCalls(ctx, re, args.Limit)
	})
}

// readTraceReportOpts keeps the requested subtree visible, with only the
// target span's own logs in OUTPUT. Descendant logs belong in the report's
// roll-ups rather than being duplicated in OUTPUT.
func readTraceReportOpts() traceReportOpts {
	opts := toolCallReportOpts()
	opts.OwnOutputOnly = true
	opts.FocusFailures = true
	// ReadTrace is the "show me the shape of what ran" tool: it keeps the span
	// tree the tool-call result drops.
	opts.HideSpanTree = false
	// ...and the nested conversation, which is where the tool-call result's
	// sub-agent pointer sends the reader.
	opts.HideConversation = false
	return opts
}

// describeObject renders an object result for the model: its type plus any
// trivial (cheap, scalar) fields. It deliberately mints no reference handle —
// objects are referenced by the names they're bound to (a `let` within a script
// or a WithObject injection), and a bare object can be rebuilt from its
// expression (Dagger is content-addressed), so this is purely informational.
//
// Fields come from the object's own class, not srv's schema: a bound tool's
// method can return an object whose type srv doesn't serve, or serves only in
// an older revision (see boundTool.definingSchema).
func (m *MCP) describeObject(ctx context.Context, srv *dagql.Server, target dagql.AnyObjectResult) (string, error) {
	res := map[string]any{
		"type": target.Type().Name(),
	}
	data := map[string]any{}
	for _, spec := range target.ObjectType().FieldSpecs(srv.View) {
		trivial := slices.ContainsFunc(spec.Directives, func(d *ast.Directive) bool {
			return d.Name == trivialFieldDirectiveName
		})
		if !trivial {
			continue
		}
		var val dagql.AnyResult
		err := srv.Select(ctx, target, &val, dagql.Selector{
			View:  srv.View,
			Field: spec.Name,
		})
		if err != nil {
			return "", err
		}
		if val == nil {
			continue
		}
		if _, isObj := dagql.UnwrapAs[dagql.AnyObjectResult](val); isObj {
			// skip any fields that reference objects, to avoid dumping entire
			// ModuleObjects
			continue
		}
		datum, err := m.sanitizeResult(val)
		if err != nil {
			return "", err
		}
		data[spec.Name] = datum
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
