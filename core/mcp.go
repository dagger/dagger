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
	// MCP server name providing the tool, if any
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
	cp.mcpServers = maps.Clone(cp.mcpServers)
	cp.mcpSessions = maps.Clone(cp.mcpSessions)
	cp.returned = false
	cp.mu = &sync.Mutex{}
	return &cp
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
	// same name. External MCP tools and the ReadLogs builtin also apply.
	if err := m.loadObjectTools(ctx, srv, allTools); err != nil {
		return nil, err
	}
	if err := m.loadMCPTools(ctx, allTools); err != nil {
		return nil, err
	}
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

func displayArgs(args any) string {
	switch args := args.(type) {
	case nil:
		return ""
	case map[string]any:
		var sb strings.Builder
		sb.WriteString("(")
		argList := make([]string, 0, len(args))
		for key, value := range args {
			argList = append(argList, fmt.Sprintf("%s: %v", key, value))
		}
		sort.Strings(argList)
		sb.WriteString(strings.Join(argList, ", "))
		sb.WriteString(")")
		return sb.String()
	default:
		return fmt.Sprintf(" %v", args)
	}
}

// applyStateReturn implements the state-mutation convention shared by tool calls
// and Dang eval results: returning a Changeset overlays it onto the bound
// workspace (via Workspace.withChanges, yielding a new immutable overlay
// Workspace) so the agent's edits accumulate across turns, and summarizes the
// patch. step() persists the resulting workspace via a withWorkspace selector so
// the overlay survives history rebuilds. It reports handled=false for any other
// value so the caller can fall through to normal object/scalar output.
func (m *MCP) applyStateReturn(ctx context.Context, srv *dagql.Server, val dagql.Typed) (handled bool, out string, err error) {
	changes, ok := dagql.UnwrapAs[dagql.ObjectResult[*Changeset]](val)
	if !ok {
		return false, "", nil
	}
	if err := m.applyChangeset(ctx, srv, changes); err != nil {
		return true, "", err
	}
	return true, m.summarizePatch(ctx, srv, changes), nil
}

// applyChangeset overlays a Changeset onto the bound workspace and updates
// m.workspace to the new overlay Workspace.
func (m *MCP) applyChangeset(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) error {
	if m.workspace.Self() == nil {
		return fmt.Errorf("cannot apply changes: no workspace bound")
	}
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
	return nil
}

// workspaceDirectory returns the bound workspace's root directory, for
// operations (like external MCP-server sync) that need a plain Directory.
func (m *MCP) workspaceDirectory(ctx context.Context, srv *dagql.Server) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]
	err := srv.Select(ctx, m.workspace, &dir, dagql.Selector{
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

	result, err := m.sanitizeResult(ctx, val)
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

func (m *MCP) sanitizeResult(ctx context.Context, val dagql.Typed) (any, error) {
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		// A nested object (e.g. inside a list) has no handle; surface its type
		// name rather than dumping a full ID.
		return obj.Type().Name(), nil
	}

	if anyRes, ok := dagql.UnwrapAs[dagql.AnyResult](val); ok {
		// Unwrap any Result[T]s so we don't encode a giant ID
		return m.sanitizeResult(ctx, anyRes.Unwrap())
	}

	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		// Handle arrays by sanitizing each value
		var res []any
		for i := 1; i <= list.Len(); i++ {
			val, err := list.Nth(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get ID for object %d: %w", i, err)
			}
			simpl, err := m.sanitizeResult(ctx, val)
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
		return err.Error(), true
	}

	args := map[string]any{}
	if len(toolCall.Arguments) > 0 {
		if err := json.Unmarshal(toolCall.Arguments, &args); err != nil {
			return fmt.Sprintf("failed to parse tool arguments: %s", err), true
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
		toolName = strings.TrimPrefix(toolName, tool.Server+"_")
	}
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
	ctx, span := Tracer(ctx).Start(ctx,
		fmt.Sprintf("%s%s", tool.Name, displayArgs(args)),
		telemetry.ActorEmoji("🤖"),
		telemetry.Reveal(),
		trace.WithAttributes(attrs...),
	)

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
		// write final result to telemetry so we see exactly what the LLM sees
		fmt.Fprintln(stdio.Stdout, res)
	}()

	toolCtx := ctx
	if m.workspace.Self() != nil {
		// Bind the LLM's Workspace so the tool's contextual (+defaultPath) and
		// Workspace-typed args resolve against it, not the ambient workspace.
		toolCtx = WorkspaceToContext(toolCtx, m.workspace)
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

// CallBatch executes a batch of tool calls, handling MCP server syncing efficiently by
// grouping calls by destructiveness and server to avoid workspace conflicts
// toolCallCtx returns the display span context a tool call's arguments streamed
// into, so the tool's execution nests beneath it. Falls back to ctx when no
// display span exists (e.g. replay or a provider that doesn't stream).
func toolCallCtx(ctx context.Context, displays map[string]toolCallDisplay, callID string) context.Context {
	if tc, ok := displays[callID]; ok {
		return tc.Ctx
	}
	return ctx
}

// endToolCallDisplay ends a tool call's display span once the tool returns,
// marking it errored if the call failed. No-op when there's no display span.
func endToolCallDisplay(displays map[string]toolCallDisplay, callID string, errored bool, errMsg string) {
	if tc, ok := displays[callID]; ok {
		if errored {
			tc.Span.SetStatus(codes.Error, errMsg)
		}
		tc.Span.End()
	}
}

func (m *MCP) CallBatch(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	// Group tool calls by their characteristics
	readOnlyMCPCalls := make(map[string][]*LLMToolCall)    // server -> read-only calls
	destructiveMCPCalls := make(map[string][]*LLMToolCall) // server -> destructive calls
	regularCalls := make([]*LLMToolCall, 0)
	destructiveCalls := make([]*LLMToolCall, 0)

	for _, toolCall := range toolCalls {
		tool, err := m.LookupTool(toolCall.Name, tools)
		if err != nil {
			// Couldn't find the tool, just call it regularly and let it fail with the
			// tool not found (or ambiguous) error
			regularCalls = append(regularCalls, toolCall)
			continue
		}

		if tool.Server == "" {
			// Regular tool call (not MCP)
			// Check if it modifies state (returns Env or Changeset)
			if tool.ReadOnly {
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

	// 1. Execute destructive non-MCP calls sequentially (they modify Env/Changeset state)
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

	// 2. Execute destructive MCP calls one server at a time to avoid workspace conflicts
	for serverName, calls := range destructiveMCPCalls {
		serverResults := m.callBatchMCPServer(ctx, tools, calls, serverName, toolCallDisplays)
		allResults = append(allResults, serverResults...)
	}

	// 3. Execute all regular read-only (non-MCP) calls in parallel
	if len(regularCalls) > 0 {
		allResults = append(allResults, m.callBatchRegular(ctx, tools, regularCalls, toolCallDisplays)...)
	}

	// 4. Execute all read-only MCP calls in parallel (safe across servers)
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

// captureLogs returns nicely Heroku-formatted lines of all logs seen since the
// last capture.
func (m *MCP) captureLogs(ctx context.Context, spanID string) ([]string, error) {
	root, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	mainMeta, err := root.MainClientCallerMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("get main client caller metadata: %w", err)
	}
	q, err := root.ClientTelemetry(ctx, mainMeta.SessionID, mainMeta.ClientID)
	if err != nil {
		return nil, err
	}
	defer q.Close()

	buf := new(strings.Builder)

	var lastLogID int64

	for {
		logs, err := q.SelectLogsBeneathSpan(ctx, clientdb.SelectLogsBeneathSpanParams{
			ID:     lastLogID,
			SpanID: sql.NullString{Valid: true, String: spanID},
			Limit:  llmLogsBatchSize,
		})
		if err != nil {
			return nil, err
		}
		if len(logs) == 0 {
			break
		}

		for _, log := range logs {
			lastLogID = log.ID

			var logAttrs []*otlpcommonv1.KeyValue
			if err := clientdb.UnmarshalProtoJSONs(log.Attributes, &otlpcommonv1.KeyValue{}, &logAttrs); err != nil {
				slog.Warn("failed to unmarshal log attributes", "error", err)
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

			if log.SpanID.Valid {
				span, err := q.SelectSpan(ctx, clientdb.SelectSpanParams{
					TraceID: log.TraceID.String,
					SpanID:  log.SpanID.String,
				})
				if err != nil {
					return nil, err
				}
				var spanAttrs []*otlpcommonv1.KeyValue
				if err := clientdb.UnmarshalProtoJSONs(span.Attributes, &otlpcommonv1.KeyValue{}, &spanAttrs); err != nil {
					slog.Warn("failed to unmarshal span attributes", "error", err)
					continue
				}
				var isNoise bool
				for _, attr := range spanAttrs {
					if attr.Key == telemetry.LLMRoleAttr || attr.Key == telemetry.LLMToolAttr {
						isNoise = true
						break
					}
				}
				if isNoise {
					// don't show logs from the LLM spans themselves
					continue
				}
			}

			var bodyPb otlpcommonv1.AnyValue
			if err := proto.Unmarshal(log.Body, &bodyPb); err != nil {
				slog.Warn("failed to unmarshal log body", "error", err, "client", mainMeta.ClientID, "log", log.ID)
				continue
			}
			switch x := bodyPb.GetValue().(type) {
			case *otlpcommonv1.AnyValue_StringValue:
				fmt.Fprint(buf, x.StringValue)
			case *otlpcommonv1.AnyValue_BytesValue:
				buf.Write(x.BytesValue)
			default:
				// default to something troubleshootable
				fmt.Fprintf(buf, "UNHANDLED: %+v", x)
			}
		}
	}
	if buf.Len() == 0 {
		return nil, nil
	}
	return strings.Split(
		// ensure trailing linebreaks don't contribute to line limits
		strings.TrimRight(buf.String(), "\n"),
		"\n",
	), nil
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
		Description: "Read logs from the most recent execution. Can filter with grep pattern or read the last N lines." + "\n" +
			"When you see traceparent:traceID-spanID in an error, use ReadLogs to read the logs for spanID",
		ReadOnly: true, // Read-only operation
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
}

func (m *MCP) readLogsTool(srv *dagql.Server) LLMToolFunc {
	return ToolFunc(srv, func(ctx context.Context, args struct {
		Span   string
		Offset int    `default:"0"`
		Limit  int    `default:"100"`
		Grep   string `default:""`
	}) (any, error) {
		logs, err := m.captureLogs(ctx, args.Span)
		if err != nil {
			return nil, fmt.Errorf("failed to capture logs: %w", err)
		}

		// Trim the last Offset lines
		if args.Offset >= len(logs) {
			return nil, fmt.Errorf("offset %d is beyond log length %d", args.Offset, len(logs))
		}
		logs = logs[:len(logs)-args.Offset]

		// Apply grep filter if specified
		if args.Grep != "" {
			re, err := regexp.Compile(args.Grep)
			if err != nil {
				return nil, fmt.Errorf("invalid grep pattern %q: %w", args.Grep, err)
			}
			var filteredLogs []string
			for i, line := range logs {
				if re.MatchString(line) {
					filteredLogs = append(filteredLogs, fmt.Sprintf("%6d→%s", i+1, line))
				}
			}
			logs = filteredLogs
		} else {
			for i, line := range logs {
				logs[i] = fmt.Sprintf("%6d→%s", i+1, line)
			}
		}

		// Apply line limit if specified
		logs = limitLines(args.Span, logs, args.Limit, llmLogsMaxLineLen)

		return strings.Join(logs, "\n"), nil
	})
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
		datum, err := m.sanitizeResult(ctx, val)
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

// Hide functions from the largest and most commonly used core types, to prevent
// tool bloat
