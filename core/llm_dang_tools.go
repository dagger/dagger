package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

// The Dang builtins: a workbench for agents writing Dang, the language
// modules of the dang SDK are written in. DangCheck answers what an editor's
// language server would (diagnostics, types, members, docs) without running
// anything; DangEval runs a snippet. Both are always registered: their
// descriptions carry the workflow, since nothing else points at them.
const (
	dangCheckToolName = "DangCheck"
	dangEvalToolName  = "DangEval"
)

// DangTools is the language service behind DangCheck and DangEval. Only the
// dang SDK's implementation package (core/sdk/dang/v2) may import Dang (see
// core/sdk/dang/README.md), so it implements this and registers it with
// RegisterDangTools; core only describes the tools and dispatches to it.
//
// Each method logs its whole report to the tool call's span (ctx's), line
// for line, as a module function's prints go to its call's: that is where
// ReadLogs reads what the tool result leaves out, since the caller bounds
// the result as it bounds a module tool's output (see dangToolResult). It is
// the only copy logged: the tools are LLMTool.LogsOwnResult, so the result
// isn't logged again.
type DangTools interface {
	// Check type-checks without running anything and returns the report.
	Check(ctx context.Context, env DangToolEnv, args DangCheckArgs) (DangReport, error)
	// Eval runs a snippet and returns its report: printed output and the
	// result, or the diagnostics or failure that stopped it. An error is
	// for what keeps the tool itself from running (e.g. an unknown `at`).
	Eval(ctx context.Context, env DangToolEnv, args DangEvalArgs) (DangReport, error)
	// Reassigned names what source reassigns (`name = …`), by its syntax
	// alone, without a scope: enough to schedule a DangEval call that may
	// write `chat` as the continuation it may be (see LLMTool.Continues).
	Reassigned(source string) []string
}

// DangReport is what a Dang tool call reports.
type DangReport struct {
	// Text is the whole report, as logged to the tool call's span.
	Text string
	// Rejected says the call's source failed to parse or type-check.
	Rejected bool
}

// DangToolEnv is what a Dang tool call sees of its conversation.
type DangToolEnv struct {
	// Server is the conversation's schema.
	Server *dagql.Server

	// Workspace is the conversation's workspace, if it is bound to one: the
	// default scope binds it as `ws`, and `at` names modules and files in it.
	Workspace dagql.ObjectResult[*Workspace]

	// Chat is the conversation dispatching the call, if one does (not under
	// dagger mcp): every scope binds it as `chat`, and a snippet reassigning
	// it continues the conversation from the value assigned.
	Chat dagql.ObjectResult[*LLM]

	// Agent is the agent owning the conversation, if any: every scope binds
	// it as `me`, read-only, and null without one.
	Agent dagql.ObjectResult[*Agent]

	// Scope returns the default scope, as of the call.
	Scope func(context.Context) (*DangScope, error)

	// Write applies what a successful DangEval snippet wrote, by reassigning
	// `ws`, `chat` or a bound tool object, and summarizes it. It is called
	// while the snippet's client still holds the written values.
	Write func(context.Context, []DangWrite) (string, error)
}

// The names every scope binds besides the bound tool objects, which a tool
// object's name can't take.
const (
	DangWorkspaceBinding = "ws"
	DangChatBinding      = "chat"
	DangAgentBinding     = "me"
)

// dangReservedNames are the names the default scope binds before any tool
// object: `chat` only for a conversation's step, not a standalone server
// (dagger mcp), which has no conversation to bind.
func (m *MCP) dangReservedNames() map[string]bool {
	return map[string]bool{DangWorkspaceBinding: true, DangChatBinding: !m.standalone, DangAgentBinding: true}
}

// DangChatAloneMessage says why a snippet can't reassign `chat` together with
// another binding: the LLM it assigns is the whole state of the conversation.
const DangChatAloneMessage = "an LLM carries its own workspace and tool objects: reassign `chat` alone; its workspace and tools come with it"

// DangWrite is a value of the default scope a DangEval snippet reassigned:
// its final value, to apply.
type DangWrite struct {
	// Name is the binding: "ws", "chat", or a bound tool object's name.
	Name string
	// TypeName is its GraphQL type.
	TypeName string
	// ID is the encoded ID of its final value.
	ID string
}

// isChat reports whether w writes the conversation, rather than a tool object
// that happens to be named `chat` where the conversation isn't bound (see
// MCP.dangReservedNames).
func (w DangWrite) isChat() bool {
	return w.Name == DangChatBinding && w.TypeName == "LLM"
}

// DangScope is the default scope of a Dang tool call: the conversation's API
// and the values bound in it besides `ws`.
type DangScope struct {
	// Deps is the API: the conversation's schema (see MCP.baseServer), the
	// modules installed in its workspace as of the call included, and the
	// modules of its bound tool objects. A nested client
	// running code in the scope serves it (see
	// engine.ClientMetadata.ServedDeps).
	Deps *SchemaBuilder

	// SchemaFile is Deps' introspection JSON, as module code sees it.
	SchemaFile dagql.Result[*File]

	// Installed are the modules installed in the conversation's workspace,
	// as of the call, that loaded: the ones Deps serves. `at` an installed
	// module that isn't in the workspace's tree (a git module) resolves to
	// one of these.
	Installed []dagql.ObjectResult[*Module]

	// LoadFailures are the installed modules that failed to load, and
	// InstalledErr why none could be, if so: what `at` such a module says.
	LoadFailures []ModuleLoadFailure
	InstalledErr error

	// Bindings are the conversation's bound tool objects, as of the call.
	Bindings []DangBinding

	// BoundMods are the modules Bindings need beside an API: their own and
	// their dependencies. A module scope's nested client serves them beside
	// its module (see engine.ClientMetadata.ServedDeps).
	BoundMods []Mod

	// Skipped names the bound tool objects left out of Bindings, and why.
	Skipped []string
}

// DangBinding is a bound tool object in the default scope: a value named
// after its type, loaded from its ID, its state when the call began.
type DangBinding struct {
	// Name is what code calls it: its type name in lowerCamel case.
	Name string
	// TypeName is its GraphQL type.
	TypeName string
	// ID is its encoded ID.
	ID string
}

// DangCheckArgs are DangCheck's arguments.
type DangCheckArgs struct {
	Source string
	At     string
	Search string
}

// DangEvalArgs are DangEval's arguments.
type DangEvalArgs struct {
	Source string
	At     string
}

var (
	dangToolsMu sync.RWMutex
	dangTools   DangTools
)

// RegisterDangTools installs the implementation of the Dang builtins.
func RegisterDangTools(impl DangTools) {
	dangToolsMu.Lock()
	defer dangToolsMu.Unlock()
	dangTools = impl
}

func registeredDangTools() DangTools {
	dangToolsMu.RLock()
	defer dangToolsMu.RUnlock()
	return dangTools
}

// Descriptions of the Dang builtins. Every conversation pays for them, so
// they stay terse; they are the only guidance pointing at the tools. What
// every scope binds is the conversation's own (see dangBindingsDoc), so the
// descriptions name it as it is.
const (
	dangCheckDescriptionHead = "Type-check Dang without running anything. Scope: the core API and installed modules (as `Dagger`), or with `at` a module of the workspace, one of its .dang files, or a path:line:col in one (seeing the locals there)."
	dangCheckDescriptionTail = "Without `source`: the module's or file's errors, warnings and deprecations, as path:line:col." + "\n" +
		"With `source`: its errors, else what it is. A member reference (`xs.reduce`, `JSON.encode`, `Container.withExec`) gives the member's signature, docs and example; a type name, the type and its members; any other expression, its type, and an object's members. Probe before writing a helper (`List` lists every list method), and check after each edit, before running a @check." + "\n" +
		"`search` ranks builtins, API fields and the module's declarations by name and docs."
	dangCheckSourceDescription = "Dang to check as one more file of the scope; its last expression is described."
	dangCheckAtDescription     = "Scope: a module installed in the workspace (by name), a Dang module's directory, a .dang file of one, or path:line:col in one. Default: the workspace scope."
	dangCheckSearchDescription = "Words to find in names and docs, e.g. \"sort\" or \"escape html\"."

	dangEvalDescriptionHead = "Run Dang: printed output, then the result as `TYPE = value` and, for an object, its members. Scope as for DangCheck, without positions: `at` a module runs the snippet as one more file of it, after its top level. A type error runs nothing." + "\n" +
		"Engine calls go through the scope's API (with `at`, the module's dependencies and own API), with no access to the host, and are counted."
	dangEvalDescriptionTail   = "Objects stay lazy (shown as their query) until you select a scalar such as `.stdout` or `.sync`. Each call is standalone; wrap long runs in Timeout."
	dangEvalSourceDescription = "Dang statements to run; the last expression is the result."
	dangEvalAtDescription     = "A module installed in the workspace (by name), a Dang module's directory, or a .dang file of one. Default: the workspace scope."
)

// dangBindingsDoc is what every scope of a conversation's Dang tool calls
// binds, as the tool descriptions name it: `ws`, `chat` and `me` when the
// conversation has them, and its bound tool objects by name. A description
// changes when they do (a tool object bound or rebound to another type).
type dangBindingsDoc struct {
	ws, chat, me bool
	// tools are the bound tool objects' names, in binding order.
	tools []string
}

// dangBindingsDoc describes m's bindings as a Dang tool call would bind
// them (see dangBindings).
func (m *MCP) dangBindingsDoc() dangBindingsDoc {
	m.mu.Lock()
	bound := slices.Clone(m.boundTools)
	m.mu.Unlock()
	doc := dangBindingsDoc{
		ws: m.workspace.Self() != nil,
		// Only a conversation's step dispatches a call with a conversation
		// to bind; a standalone server (dagger mcp) has none.
		chat: !m.standalone,
		me:   m.ownerAgent != "",
	}
	seen := m.dangReservedNames()
	for _, b := range bound {
		name, ok := dangToolObjectName(b, seen)
		if ok {
			seen[name] = true
			doc.tools = append(doc.tools, name)
		}
	}
	return doc
}

// dangToolObjectName is the name a bound tool object is bound by, its type
// name in lowerCamel case, unless it isn't a module object or another
// binding (in seen) has the name.
func dangToolObjectName(b boundTool, seen map[string]bool) (string, bool) {
	objType := b.objType
	if b.object != nil {
		objType = b.object.ObjectType()
	}
	if _, ok := ModuleObjectTypeModule(objType); !ok {
		return "", false
	}
	name := LatestNamer.FieldName(b.typeName())
	return name, !seen[name]
}

// list names the bindings, each with what it is.
func (d dangBindingsDoc) list() string {
	var names []string
	if d.ws {
		names = append(names, "`ws` (your Workspace)")
	}
	if d.chat {
		names = append(names, "`chat` (this conversation, an LLM)")
	}
	if d.me {
		names = append(names, "`me` (the Agent owning this conversation; can't be reassigned)")
	}
	if len(d.tools) > 0 {
		tools := make([]string, len(d.tools))
		for i, name := range d.tools {
			tools[i] = "`" + name + "`"
		}
		names = append(names, "your tool objects "+strings.Join(tools, ", "))
	}
	return strings.Join(names, "; ")
}

// checkDescription is DangCheck's description.
func (d dangBindingsDoc) checkDescription() string {
	head := dangCheckDescriptionHead
	if list := d.list(); list != "" {
		head += " Every scope binds " + list + "."
	}
	return head + "\n" + dangCheckDescriptionTail
}

// evalDescription is DangEval's description: what writing each binding does.
func (d dangBindingsDoc) evalDescription() string {
	var writes []string
	if d.ws {
		writes = append(writes, "a reassigned `ws` (`ws = ws.withChanges(…)`) becomes your workspace")
	}
	if len(d.tools) > 0 {
		writes = append(writes, fmt.Sprintf("a reassigned tool object (`%[1]s = %[1]s.someMethod(…)`) its state", d.tools[0]))
	}
	if d.chat {
		writes = append(writes, "a reassigned `chat` (an LLM, such as one a tool method returns given `llm: chat`) the conversation; assign it alone, as its workspace and tools come with it")
	}
	text := dangEvalDescriptionHead
	if len(writes) > 0 {
		text += " Assignment is the write: if the snippet succeeds, " + strings.Join(writes, "; ") + "; nothing else is."
		if d.ws || d.chat {
			text += " A module installed through `ws` or `chat` is callable in the next call."
		}
	}
	return text + " " + dangEvalDescriptionTail
}

// loadDangTools registers DangCheck and DangEval, whenever the engine has a
// Dang implementation (always, outside of tests).
func (m *MCP) loadDangTools(srv *dagql.Server, allTools *LLMToolSet) {
	impl := registeredDangTools()
	if impl == nil {
		return
	}
	m.addDangTools(srv, allTools, impl)
}

// addDangTools registers DangCheck and DangEval, served by impl.
func (m *MCP) addDangTools(srv *dagql.Server, allTools *LLMToolSet, impl DangTools) {
	env := func(ctx context.Context) DangToolEnv {
		start := m.workspace
		return DangToolEnv{
			Server:    srv,
			Workspace: start,
			Chat:      m.currentLLM(),
			Agent:     m.dangAgent(ctx),
			Scope:     m.dangScope,
			Write: func(ctx context.Context, writes []DangWrite) (string, error) {
				return m.applyDangWrites(ctx, srv, start, writes)
			},
		}
	}
	doc := m.dangBindingsDoc()
	allTools.Add(LLMTool{
		Name:        dangCheckToolName,
		Description: doc.checkDescription(),
		ReadOnly:    true,
		// The report is the call's logs already (see DangTools).
		LogsOwnResult: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{"type": "string", "description": dangCheckSourceDescription},
				"at":     map[string]any{"type": "string", "description": dangCheckAtDescription},
				"search": map[string]any{"type": "string", "description": dangCheckSearchDescription},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		Call: ToolFunc(srv, func(ctx context.Context, args struct {
			Source string `default:""`
			At     string `default:""`
			Search string `default:""`
		}) (any, error) {
			rep, err := impl.Check(ctx, env(ctx), DangCheckArgs(args))
			if err != nil {
				return nil, err
			}
			return m.dangToolResult(ctx, rep), nil
		}),
	})
	allTools.Add(LLMTool{
		Name:        dangEvalToolName,
		Description: doc.evalDescription(),
		// The report is the call's logs already, its prints streamed as
		// they are printed (see DangTools).
		LogsOwnResult: true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{"type": "string", "description": dangEvalSourceDescription},
				"at":     map[string]any{"type": "string", "description": dangEvalAtDescription},
			},
			"required":             []string{"source"},
			"additionalProperties": false,
		},
		// A snippet reassigning `chat` replaces the conversation, as a tool
		// returning an LLM does: it runs as a continuation, handed the
		// conversation with the changes of the calls before it.
		Continues: func(arguments []byte) bool {
			var args struct {
				Source string `json:"source"`
			}
			if json.Unmarshal(arguments, &args) != nil {
				return false
			}
			return slices.Contains(impl.Reassigned(args.Source), DangChatBinding)
		},
		Call: ToolFunc(srv, func(ctx context.Context, args struct {
			Source string
			At     string `default:""`
		}) (any, error) {
			rep, err := impl.Eval(ctx, env(ctx), DangEvalArgs(args))
			if err != nil {
				return nil, err
			}
			return m.dangToolResult(ctx, rep), nil
		}),
	})
}

// dangAgent is the agent owning the conversation, as `me` binds it: a
// seed-free reference to its runtime, or none when the conversation has no
// owner or its owner doesn't run in this session.
func (m *MCP) dangAgent(ctx context.Context) dagql.ObjectResult[*Agent] {
	if m.ownerAgent == "" {
		return dagql.ObjectResult[*Agent]{}
	}
	agent, err := AgentReference(ctx, m.ownerAgent)
	if err != nil {
		slog.Debug("no agent to bind as me", "handle", m.ownerAgent, "error", err)
		return dagql.ObjectResult[*Agent]{}
	}
	return agent
}

// dangSkillNudge follows a Dang tool's report when its source failed to
// parse or type-check and the conversation hasn't read the dang-language
// skill. It stays generic: guidance on a particular mistake belongs in
// Dang's own diagnostics.
const dangSkillNudge = "See the " + dangSkillName + " skill (ReadSkill) for Dang's syntax and semantics."

// dangToolResult is what the model sees of a Dang tool's report: a module
// tool call's result (see dangSpanResult), with the report as its output.
// Only a conversation's tool call has a span of its own to point at and to
// report on; elsewhere (dagger mcp), the report is only guarded, as any tool
// result is. A report whose source failed to parse or type-check points at
// the dang-language skill, until the conversation reads it: the nudge is
// logged after the report, so the call's logs, which are all a frontend
// shows of it (see LLMTool.LogsOwnResult), carry it too.
func (m *MCP) dangToolResult(ctx context.Context, rep DangReport) string {
	var text string
	if spanID := trace.SpanContextFromContext(ctx).SpanID(); spanID.IsValid() && IsAgentToolCall(ctx) {
		text = m.dangSpanResult(ctx, spanID.String(), rep.Text)
	} else {
		text = guardToolResult(rep.Text)
	}
	if rep.Rejected && !m.hasReadSkill(dangSkillName) {
		text = strings.TrimRight(text, "\n") + "\n" + dangSkillNudge
		logDangNudge(ctx)
	}
	return text
}

// logDangNudge logs dangSkillNudge to the tool call's span, after the report.
func logDangNudge(ctx context.Context) {
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	fmt.Fprintln(stdio.Stdout, dangSkillNudge)
}

// dangSpanResult shapes a Dang tool call's result as a module tool call's
// is (see MCP.toolLogs and spanResult), with the report, which is the tool
// call span's own logs line for line (see DangTools), as the tool's output.
// When nothing nested renders -- a snippet's API calls alone surface
// nothing with the span tree hidden -- that is the report alone, bounded as
// a module tool's flat output is. When the snippet ran checks or tests, the
// trace report's CHECKS, TESTS and FAILURES sections follow it, under the
// same headings as a module tool's.
//
// The report's own bound points at ReadLogs on the span's own logs: the
// engine calls a snippet makes log beneath the span too, so its line counts
// match those, not the span's whole scope.
func (m *MCP) dangSpanResult(ctx context.Context, spanID, report string) string {
	own := func(budget int) string {
		return boundDangReport(fmt.Sprintf(`%s, scope: "own"`, spanID), report, budget)
	}
	if !toolSpanHasDescendants(ctx, spanID) {
		return own(llmToolLogsMaxBytes)
	}
	opts := toolCallReportOpts()
	// The span's own logs are the report, shown as OUTPUT.
	opts.HideLogSpans = map[string]bool{spanID: true}
	nested, err := renderTraceReport(ctx, spanID, opts)
	if err != nil {
		// Decoration must not replace the result with a telemetry failure.
		slog.Warn("incomplete Dang tool trace decoration", "span", spanID, "error", err)
		return own(llmToolLogsMaxBytes)
	}
	if strings.TrimSpace(nested.body) == "" && nested.failures == "" {
		return own(llmToolLogsMaxBytes)
	}
	// The combined result answers to the trace report's budget: leave the
	// nested sections their share of it.
	budget := max(traceReportMaxBytes-len(nested.body)-len(nested.failures)-dangReportSlack, traceReportMaxBytes/4)
	return combineSpanResult(spanID, own(budget), nested.body, nested.failures)
}

// dangReportSlack is room in a combined result for its headings, separators
// and the ReadLogs footer.
const dangReportSlack = 512

// boundDangReport bounds a Dang tool's report as flatLogs bounds a module
// tool's printed output, to budget bytes: long lines are clamped, and past
// the budget the middle gives way to "... N lines omitted (use ReadLogs(span:
// ref) to read more) ...".
func boundDangReport(ref, report string, budget int) string {
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	for i, line := range lines {
		// Clamped on a rune boundary, with room for the marker, so the
		// result guard doesn't clamp it again.
		lines[i] = clampLineBytes(line, llmLogsMaxLineLen-dangLineMarkerMax)
	}
	return strings.TrimRight(strings.Join(capLinesBytes(ref, lines, budget), "\n"), "\n")
}

// dangLineMarkerMax is room for clampLineBytes' marker on a clamped line.
const dangLineMarkerMax = 32

// dangScope is the default scope of a Dang tool call: the conversation's
// API, plus each bound tool object, under its type's lowerCamel name, with
// its module (and that module's dependencies) served beside the API so its
// methods type-check and run. A call on a binding from a snippet only returns
// its result; reassigning the binding is what rebinds the object (see
// applyDangWrites).
//
// The API is baseServer's: the conversation's workspace's installed modules
// included, as the call begins (see MCP.loadBase), so a module an earlier
// snippet installed (`ws = ws.withModule(…)`) is in it, and one it
// uninstalled or broke isn't. The scope also records what loading them
// found, for `at` and for a note when a snippet names a module that failed.
//
// Only module objects are bound: a core type's name would shadow a core
// field (`container`), and `ws` is the workspace already. A bound object's
// types are its own module's, even where the workspace installs a module of
// the same name that differs (one whose source the agent is editing). An
// object whose module can't join the API is skipped rather than failing the
// call, and so are installed modules that can't.
func (m *MCP) dangScope(ctx context.Context) (*DangScope, error) {
	ctx, base, err := m.loadBase(ctx)
	if err != nil {
		return nil, err
	}
	scope := &DangScope{InstalledErr: base.installedErr}
	boundByName, err := m.dangBindings(scope)
	if err != nil {
		return nil, err
	}
	deps := base.deps
	if installed := base.installed; installed != nil {
		scope.Installed, scope.LoadFailures = installed.Loaded, installed.Failures
		// A bound object's types are its own module's.
		var overrides []Mod
		for _, mod := range installed.Loaded {
			userMod := NewUserMod(mod)
			if boundMod, ok := boundByName[userMod.Name()]; ok {
				if same, err := boundMod.Same(userMod); err != nil || !same {
					overrides = append(overrides, boundMod)
				}
			}
		}
		deps = deps.WithPrimaryMods(overrides, nil)
	}
	if err := scope.serve(ctx, deps, base.served); err != nil {
		return nil, err
	}
	return scope, nil
}

// dangBindings adds the conversation's bound tool objects to scope, with
// their modules, and returns those modules by name.
func (m *MCP) dangBindings(scope *DangScope) (map[string]Mod, error) {
	m.mu.Lock()
	bound := slices.Clone(m.boundTools)
	m.mu.Unlock()

	seen := m.dangReservedNames()
	boundByName := map[string]Mod{}
	for _, b := range bound {
		typeName := b.typeName()
		objType := b.objType
		if b.object != nil {
			objType = b.object.ObjectType()
		}
		mod, ok := ModuleObjectTypeModule(objType)
		if !ok {
			scope.Skipped = append(scope.Skipped, typeName+": not a module object")
			continue
		}
		name, ok := dangToolObjectName(b, seen)
		if !ok {
			scope.Skipped = append(scope.Skipped, fmt.Sprintf("%s: %q is taken", typeName, name))
			continue
		}
		id := b.id
		if id == nil && b.object != nil {
			var err error
			if id, err = b.object.ID(); err != nil {
				return nil, err
			}
		}
		if id == nil {
			scope.Skipped = append(scope.Skipped, typeName+": no ID")
			continue
		}
		encoded, err := id.Encode()
		if err != nil {
			return nil, fmt.Errorf("encode bound %s: %w", typeName, err)
		}
		seen[name] = true
		scope.Bindings = append(scope.Bindings, DangBinding{Name: name, TypeName: typeName, ID: encoded})
		// Its types, not its constructor: the name is the bound value's.
		boundMods := []Mod{NewUserMod(mod)}
		if deps := mod.Self().Deps; deps != nil {
			for _, dep := range deps.Mods() {
				if dep.Name() != ModuleName {
					boundMods = append(boundMods, dep)
				}
			}
		}
		for _, bm := range boundMods {
			if _, ok := boundByName[bm.Name()]; !ok {
				boundByName[bm.Name()] = bm
			}
		}
		scope.BoundMods = append(scope.BoundMods, boundMods...)
	}
	return boundByName, nil
}

// serve sets scope's API: base (served with the installed modules) and the
// bound objects' modules, else as many of them as fit beside served, the
// bound objects', which worked before the installed modules joined it,
// first.
func (scope *DangScope) serve(ctx context.Context, base, served *SchemaBuilder) error {
	withBound := func(deps *SchemaBuilder) *SchemaBuilder {
		for _, bm := range scope.BoundMods {
			deps = deps.With(bm, InstallOpts{SkipConstructor: true})
		}
		return deps
	}
	type candidate struct {
		deps                *SchemaBuilder
		installed, bindings bool
	}
	candidates := []candidate{{withBound(base), true, true}}
	if base != served {
		candidates = append(candidates, candidate{withBound(served), false, true})
	}
	if len(scope.Bindings) > 0 {
		candidates = append(candidates, candidate{base, true, false})
		if base != served {
			candidates = append(candidates, candidate{served, false, false})
		}
	}
	var firstErr error
	for _, c := range candidates {
		file, err := c.deps.SchemaIntrospectionJSONFileForModule(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		scope.Deps, scope.SchemaFile = c.deps, file
		if !c.installed {
			scope.Installed = nil
			scope.InstalledErr = fmt.Errorf("serve the installed modules: %w", firstErr)
		}
		if !c.bindings {
			for _, b := range scope.Bindings {
				scope.Skipped = append(scope.Skipped, fmt.Sprintf("%s: %v", b.TypeName, firstErr))
			}
			scope.Bindings, scope.BoundMods = nil, nil
		}
		return nil
	}
	return firstErr
}

// applyDangWrites applies what a DangEval snippet wrote: assignment is the
// write. The final `ws` becomes the conversation's workspace, as a tool
// returning a Workspace makes it (commits and resets included); a reassigned
// tool object is rebound as a tool call returning its own type rebinds it;
// the final `chat` becomes the conversation the step resumes from, as a tool
// returning an LLM makes it (see adoptLLM). start is the workspace the
// snippet began with. It summarizes what was written.
//
// Writes are all or nothing: every final value is loaded and validated, and
// each tool object's new state computed, before anything changes; then they
// are committed together (see commitDangWrites), and a commit that fails
// undoes the ones before it. `chat` is written alone, since the LLM carries
// its own workspace and tool objects; `me` is never written. The Dang side
// refuses both before running a snippet; this refuses them again.
func (m *MCP) applyDangWrites(ctx context.Context, srv *dagql.Server, start dagql.ObjectResult[*Workspace], writes []DangWrite) (string, error) {
	if len(writes) == 0 {
		return "0 writes", nil
	}
	if err := checkDangWrites(writes); err != nil {
		return "", err
	}
	var commits []dangCommit
	for _, w := range writes {
		var id call.ID
		if err := id.Decode(w.ID); err != nil {
			return "", fmt.Errorf("decode the final %s: %w", w.Name, err)
		}
		final, err := srv.Load(ctx, &id)
		if err != nil {
			return "", fmt.Errorf("load the final %s: %w", w.Name, err)
		}
		switch {
		case w.Name == DangWorkspaceBinding:
			ws, ok := dagql.UnwrapAs[dagql.ObjectResult[*Workspace]](final)
			if !ok {
				return "", fmt.Errorf("write ws: got a %s, not a Workspace", final.Type().Name())
			}
			if start.Self() == nil {
				return "", fmt.Errorf("write ws: no workspace bound")
			}
			commits = append(commits, dangCommit{name: w.Name, apply: func() (string, error) {
				return m.writeDangWorkspace(ctx, srv, ws)
			}})
			continue
		case w.isChat():
			next, ok := dagql.UnwrapAs[dagql.ObjectResult[*LLM]](final)
			if !ok {
				return "", fmt.Errorf("write chat: got a %s, not an LLM", final.Type().Name())
			}
			commits = append(commits, dangCommit{name: w.Name, apply: func() (string, error) {
				return m.writeDangChat(ctx, srv, next)
			}})
			continue
		}
		recv, ok, err := m.boundToolObject(ctx, srv, w.TypeName)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("write %s: no bound %s", w.Name, w.TypeName)
		}
		state, changed, err := m.fieldwiseState(ctx, srv, recv, final)
		if err != nil {
			return "", fmt.Errorf("write %s: %w", w.Name, err)
		}
		name, typeName := w.Name, w.TypeName
		commits = append(commits, dangCommit{name: name, apply: func() (string, error) {
			if !changed {
				return name + " state unchanged", nil
			}
			if err := m.rebindBoundTool(typeName, state); err != nil {
				return "", err
			}
			return name + " state updated", nil
		}})
	}
	if err := m.guardStateChange(); err != nil {
		return "", err
	}
	return m.commitDangWrites(commits)
}

// dangCommit is one resolved write of a DangEval snippet, ready to apply.
type dangCommit struct {
	name string
	// apply makes the write and summarizes it: a first line, then any
	// detail (a workspace's patch).
	apply func() (string, error)
}

// commitDangWrites applies resolved writes in order. If one fails, the
// workspace, the bindings and the state-changed mark are restored to what
// they were before the first, so a failed call has written nothing.
func (m *MCP) commitDangWrites(commits []dangCommit) (string, error) {
	m.mu.Lock()
	prevBound := slices.Clone(m.boundTools)
	prevChanged := m.stateChanged
	m.mu.Unlock()
	prevWorkspace := m.workspace

	var lines, details []string
	for _, c := range commits {
		out, err := c.apply()
		if err != nil {
			m.workspace = prevWorkspace
			m.mu.Lock()
			m.boundTools = prevBound
			m.stateChanged = prevChanged
			m.mu.Unlock()
			return "", fmt.Errorf("write %s: %w (nothing was written)", c.name, err)
		}
		line, detail, _ := strings.Cut(out, "\n")
		lines = append(lines, line)
		if detail != "" {
			details = append(details, detail)
		}
	}
	return strings.Join(append([]string{strings.Join(lines, " · ")}, details...), "\n"), nil
}

// writeDangWorkspace makes final, a Workspace a snippet assigned to `ws`,
// the conversation's workspace, as a tool returning it does
// (rebindWorkspace): its commits, resets and file edits all carry over, and
// the summary says what changed.
func (m *MCP) writeDangWorkspace(ctx context.Context, srv *dagql.Server, final dagql.ObjectResult[*Workspace]) (string, error) {
	summary, err := m.rebindWorkspace(ctx, srv, final)
	if err != nil {
		return "", err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "ws unchanged", nil
	}
	line := "ws written"
	if n := strings.Count(summary, "diff --git "); n > 0 {
		files := "files"
		if n == 1 {
			files = "file"
		}
		line = fmt.Sprintf("ws written (%d %s)", n, files)
	}
	return line + "\n" + summary, nil
}

// checkDangWrites refuses writes no snippet may make: `me`, which is
// read-only, and `chat` beside any other binding (see DangChatAloneMessage).
func checkDangWrites(writes []DangWrite) error {
	var others []string
	chat := false
	for _, w := range writes {
		switch {
		case w.Name == DangAgentBinding:
			return fmt.Errorf("write me: `me` is read-only (nothing was written)")
		case w.isChat():
			chat = true
		default:
			others = append(others, "`"+w.Name+"`")
		}
	}
	if chat && len(others) > 0 {
		return fmt.Errorf("write chat with %s: %s (nothing was written)", strings.Join(others, ", "), DangChatAloneMessage)
	}
	return nil
}

// writeDangChat makes next, an LLM a snippet assigned to `chat`, the
// conversation the step resumes from, as a tool returning it does
// (adoptLLM): its workspace, tools and history all carry over, from the next
// call on, and the summary says what changed.
func (m *MCP) writeDangChat(ctx context.Context, srv *dagql.Server, next dagql.ObjectResult[*LLM]) (string, error) {
	summary, err := m.adoptLLM(ctx, srv, next)
	if err != nil {
		return "", err
	}
	if summary = strings.TrimSpace(summary); summary == "" {
		return "chat written", nil
	}
	return "chat written\n" + summary, nil
}
