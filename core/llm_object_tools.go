package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
)

// This file implements the object-tools scheme (hack/designs/workspace-agents.md).
// The LLM binds one or more objects via LLM.withTools, and every eligible method
// of a bound object becomes a tool. A tool that returns the bound object's own
// type replaces the binding — so the object IS the agent's state, and a state
// update is just a method that returns a new self. This supersedes the Dang
// scripting harness (dang_eval + inspect) as the core agent interface; the Dang
// machinery stays in the tree for module authoring.

// llmToolLogsMaxLines caps the print output surfaced from a single tool call
// (object/Void returns), matching ReadLogs' default page size.
const llmToolLogsMaxLines = 100

// workspaceTypeName is the object type the engine auto-injects into a module
// function's arguments from the bound Workspace, so such arguments are hidden
// from the generated tool schema.
const workspaceTypeName = "Workspace"

// boundTool is one object bound into the LLM's toolset via withTools. Its Object
// is the receiver every generated tool selects its method on; Except lists the
// method names to omit (e.g. the module's own entrypoint/constructor).
type boundTool struct {
	Object dagql.AnyObjectResult
	Except []string
}

// WithTools binds obj's methods as tools, carrying except. At most one binding
// per object type is kept: binding an object whose type is already bound replaces
// it in place. That is the state-update shape — a method returning the bound type
// rebinds through here — so the binding list stays bounded and a recorded
// withTools selector replays to the same state deterministically.
func (m *MCP) WithTools(obj dagql.AnyObjectResult, except []string) *MCP {
	m = m.Clone()
	typeName := obj.Type().Name()
	for i, b := range m.boundTools {
		if b.Object.Type().Name() == typeName {
			m.boundTools[i] = boundTool{Object: obj, Except: except}
			return m
		}
	}
	m.boundTools = append(m.boundTools, boundTool{Object: obj, Except: except})
	return m
}

// boundToolObject returns the current bound object for a type, read under the
// lock so a state update from an earlier call in the same batch is visible to a
// later call's receiver.
func (m *MCP) boundToolObject(typeName string) (dagql.AnyObjectResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.boundTools {
		if b.Object.Type().Name() == typeName {
			return b.Object, true
		}
	}
	return nil, false
}

// rebindBoundTool replaces the object for typeName's binding with newObj — the
// same-type-return state transition. It mutates in place under the lock; step()
// then persists the transition as a withTools selector on the LLM's ID.
func (m *MCP) rebindBoundTool(typeName string, newObj dagql.AnyObjectResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, b := range m.boundTools {
		if b.Object.Type().Name() == typeName {
			m.boundTools[i].Object = newObj
			return
		}
	}
}

// boundToolBinding is a flattened snapshot of a binding: the object's ID plus its
// except list, enough for step() to rebuild a withTools selector.
type boundToolBinding struct {
	ID     *call.ID
	Except []string
}

// BoundToolBindings snapshots the current bindings' IDs and except lists so
// step() can detect a state transition (an object rebind) and persist it, the
// same way it persists a workspace overlay via withWorkspace.
func (m *MCP) BoundToolBindings() ([]boundToolBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]boundToolBinding, 0, len(m.boundTools))
	for _, b := range m.boundTools {
		id, err := b.Object.ID()
		if err != nil {
			return nil, err
		}
		out = append(out, boundToolBinding{ID: id, Except: slices.Clone(b.Except)})
	}
	return out, nil
}

// bindWorkspaceModuleTools binds each served workspace module's main object as
// a toolset, constructing it through the canonical server (which serves module
// constructors even for entrypoint modules, whose sugared schema only carries
// proxies). `dagger mcp` uses this so a workspace module's methods are served
// as MCP tools without an explicit withTools: the module entrypoint is "the
// way in" (hack/designs/workspace-agents.md). Modules whose constructors
// require arguments (beyond the auto-injected Workspace) are skipped — there
// is no one to prompt for them.
func (m *MCP) bindWorkspaceModuleTools(ctx context.Context) (*MCP, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	served, err := query.Server.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("current served deps: %w", err)
	}
	srv, err := served.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("served schema: %w", err)
	}
	canonical := srv.Canonical()
	for _, primary := range served.PrimaryMods() {
		mod := primary.ModuleResult().Self()
		if mod == nil || mod.Name() == ModuleName {
			continue
		}
		ctorName := gqlFieldName(mod.Name())
		spec, ok := canonical.Root().ObjectType().FieldSpec(ctorName, canonical.View)
		if !ok {
			continue
		}
		constructible := true
		for _, arg := range spec.Args.Inputs(canonical.View) {
			if arg.Internal || arg.Default != nil {
				continue
			}
			if !arg.Type.Type().NonNull {
				continue
			}
			if arg.Type.Type().Name() == workspaceTypeName {
				// Auto-injected from the current workspace.
				continue
			}
			constructible = false
			break
		}
		if !constructible {
			continue
		}
		var obj dagql.AnyObjectResult
		if err := canonical.Select(ctx, canonical.Root(), &obj, dagql.Selector{
			View:  canonical.View,
			Field: ctorName,
		}); err != nil {
			return nil, fmt.Errorf("construct workspace module %q: %w", mod.Name(), err)
		}
		m = m.WithTools(obj, nil)
	}
	return m, nil
}

// loadObjectTools registers one tool per eligible method of each bound object.
// It is called before the builtins so a bound method overrides a builtin of the
// same name; within the object tools a later withTools binding wins a name
// collision ("last withTools wins").
func (m *MCP) loadObjectTools(_ context.Context, srv *dagql.Server, allTools *LLMToolSet) error {
	m.mu.Lock()
	bindings := slices.Clone(m.boundTools)
	m.mu.Unlock()
	if len(bindings) == 0 {
		return nil
	}
	schema := srv.Schema()
	// Later bindings override earlier ones on a name collision; keep first-seen
	// order for stable listing.
	byName := map[string]LLMTool{}
	var order []string
	for _, b := range bindings {
		tools, err := m.toolsForBoundObject(srv, schema, b)
		if err != nil {
			return err
		}
		for _, t := range tools {
			if _, seen := byName[t.Name]; !seen {
				order = append(order, t.Name)
			}
			byName[t.Name] = t
		}
	}
	for _, name := range order {
		allTools.Add(byName[name])
	}
	return nil
}

// ToolNameCollisions reports, per tool name, the bound-object type names that
// each contribute a tool of that name — but only for names contributed by more
// than one bound object. On such a collision the last withTools binding wins
// (loadObjectTools order); the report lets callers warn about the shadowing when
// composing several agents' toolsets onto one LLM (hack/designs/workspace-agents.md §3).
func (m *MCP) ToolNameCollisions(ctx context.Context) (map[string][]string, error) {
	srv, err := m.Server(ctx)
	if err != nil {
		return nil, err
	}
	schema := srv.Schema()

	m.mu.Lock()
	bindings := slices.Clone(m.boundTools)
	m.mu.Unlock()

	contributors := map[string][]string{}
	for _, b := range bindings {
		tools, err := m.toolsForBoundObject(srv, schema, b)
		if err != nil {
			return nil, err
		}
		typeName := b.Object.Type().Name()
		for _, t := range tools {
			contributors[t.Name] = append(contributors[t.Name], typeName)
		}
	}

	collisions := map[string][]string{}
	for name, types := range contributors {
		if len(types) > 1 {
			collisions[name] = types
		}
	}
	return collisions, nil
}

// toolsForBoundObject generates the tools for a single bound object: one per
// eligible field of its schema type.
func (m *MCP) toolsForBoundObject(srv *dagql.Server, schema *ast.Schema, b boundTool) ([]LLMTool, error) {
	typeName := b.Object.Type().Name()
	def := schema.Types[typeName]
	if def == nil || (def.Kind != ast.Object && def.Kind != ast.Interface) {
		return nil, fmt.Errorf("bound object type %q is not an object in the workspace schema", typeName)
	}
	var tools []LLMTool
	for _, field := range def.Fields {
		if !objectToolEligible(field, b.Except) {
			continue
		}
		toolSchema, err := objectMethodSchema(schema, field)
		if err != nil {
			return nil, fmt.Errorf("build schema for %s.%s: %w", typeName, field.Name, err)
		}
		retType := field.Type.Name()
		tools = append(tools, LLMTool{
			Name:        field.Name,
			Field:       field,
			Description: strings.TrimSpace(field.Description),
			Schema:      toolSchema,
			// A method that mutates state — returning the bound object's own type
			// (rebinds it), a Changeset (overlays the workspace), or a Workspace
			// (replaces it) — is destructive and must run sequentially; everything
			// else is read-only.
			ReadOnly: retType != typeName && retType != "Changeset" && retType != "Workspace",
			Call:     m.callObjectMethod(srv, typeName, field),
		})
	}
	return tools, nil
}

// objectToolEligible reports whether a field becomes a tool: it must not be in
// except, must not be an internal/reserved field, and every REQUIRED argument
// must be expressible without an object handle — a required object-typed arg
// (other than the auto-injected Workspace) disqualifies it, since the model has
// no handle to pass.
func objectToolEligible(field *ast.FieldDefinition, except []string) bool {
	if slices.Contains(except, field.Name) {
		return false
	}
	if strings.HasPrefix(field.Name, "_") {
		return false
	}
	if field.Name == "id" || field.Name == "sync" {
		return false
	}
	if field.Directives.ForName("deprecated") != nil {
		return false
	}
	// An @agent method is the composition entrypoint (base: LLM!): LLM!; it is
	// never itself a tool, so hide it without requiring authors to add it to
	// `except` by hand.
	if field.Directives.ForName("agent") != nil {
		return false
	}
	for _, arg := range field.Arguments {
		if isWorkspaceArg(arg) {
			// Auto-injected from the bound Workspace; treated as optional.
			continue
		}
		required := arg.Type.NonNull && arg.DefaultValue == nil
		if required && isObjectArg(arg) {
			return false
		}
	}
	return true
}

// isObjectArg reports whether an argument is a Dagger object, which crosses the
// wire as an `ID` scalar carrying an @expectedType directive.
func isObjectArg(arg *ast.ArgumentDefinition) bool {
	return arg.Directives.ForName("expectedType") != nil
}

// isWorkspaceArg reports whether an argument is the auto-injected Workspace,
// identified by @expectedType(name: "Workspace"). Such args are filled from the
// bound Workspace and never shown to the model.
func isWorkspaceArg(arg *ast.ArgumentDefinition) bool {
	d := arg.Directives.ForName("expectedType")
	if d == nil {
		return false
	}
	name := d.Arguments.ForName("name")
	return name != nil && name.Value != nil && name.Value.Raw == workspaceTypeName
}

// objectMethodSchema builds a tool's JSON-schema parameters from a field's
// visible arguments — its scalars, enums, lists, and input objects — omitting the
// auto-injected Workspace argument. Object args (when optional) render as ID
// strings, annotated with their expected type.
func objectMethodSchema(schema *ast.Schema, field *ast.FieldDefinition) (map[string]any, error) {
	properties := map[string]any{}
	var required []string
	for _, arg := range field.Arguments {
		if isWorkspaceArg(arg) {
			continue
		}
		argSchema, err := argTypeToJSONSchema(schema, arg.Type)
		if err != nil {
			return nil, err
		}
		desc := arg.Description
		if d := arg.Directives.ForName("expectedType"); d != nil {
			if name := d.Arguments.ForName("name"); name != nil && name.Value != nil {
				if desc == "" {
					desc = fmt.Sprintf("(%s ID)", name.Value.Raw)
				} else {
					desc = fmt.Sprintf("(%s ID) %s", name.Value.Raw, desc)
				}
			}
		}
		if desc != "" {
			argSchema["description"] = desc
		}
		if arg.DefaultValue != nil {
			val, err := arg.DefaultValue.Value(nil)
			if err != nil {
				return nil, fmt.Errorf("default value for %q: %w", arg.Name, err)
			}
			argSchema["default"] = val
		}
		properties[arg.Name] = argSchema
		if arg.Type.NonNull && arg.DefaultValue == nil {
			required = append(required, arg.Name)
		}
	}
	jsonSchema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		jsonSchema["required"] = required
	}
	return jsonSchema, nil
}

// argTypeToJSONSchema converts a GraphQL argument type to a JSON-schema fragment.
// It resurrects the pre-Dang arg→schema conversion, scoped to a single argument.
func argTypeToJSONSchema(schema *ast.Schema, t *ast.Type) (map[string]any, error) {
	jsonSchema := map[string]any{}
	if t.Elem != nil {
		jsonSchema["type"] = "array"
		items, err := argTypeToJSONSchema(schema, t.Elem)
		if err != nil {
			return nil, fmt.Errorf("elem type: %w", err)
		}
		jsonSchema["items"] = items
		return jsonSchema, nil
	}
	switch t.NamedType {
	case "Int":
		jsonSchema["type"] = "integer"
	case "Float":
		jsonSchema["type"] = "number"
	case "String", "ID":
		jsonSchema["type"] = "string"
	case "Boolean":
		jsonSchema["type"] = "boolean"
	default:
		typeDef, found := schema.Types[t.NamedType]
		if !found {
			return nil, fmt.Errorf("unknown type: %q", t.NamedType)
		}
		switch typeDef.Kind {
		case ast.InputObject:
			jsonSchema["type"] = "object"
			properties := map[string]any{}
			for _, f := range typeDef.Fields {
				fieldSpec, err := argTypeToJSONSchema(schema, f.Type)
				if err != nil {
					return nil, fmt.Errorf("field %q type: %w", f.Name, err)
				}
				properties[f.Name] = fieldSpec
			}
			jsonSchema["properties"] = properties
		case ast.Enum:
			jsonSchema["type"] = "string"
			var enum []string
			for _, val := range typeDef.EnumValues {
				enum = append(enum, val.Name)
			}
			jsonSchema["enum"] = enum
		case ast.Scalar:
			jsonSchema["type"] = "string"
		default:
			return nil, fmt.Errorf("unhandled type: %s (%s)", t, typeDef.Kind)
		}
	}
	return jsonSchema, nil
}

// callObjectMethod returns the tool implementation for one method of a bound
// object. It selects the method on the CURRENT bound object (so an earlier
// same-batch state update is visible), relying on the bound Workspace already
// threaded into ctx by MCP.Call so Workspace-typed args auto-inject, then routes
// the result by type.
func (m *MCP) callObjectMethod(srv *dagql.Server, typeName string, field *ast.FieldDefinition) LLMToolFunc {
	fieldName := field.Name
	return func(ctx context.Context, rawArgs any) (any, error) {
		args, ok := rawArgs.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid arguments type: %T", rawArgs)
		}
		recv, ok := m.boundToolObject(typeName)
		if !ok {
			return nil, fmt.Errorf("no object of type %q is bound", typeName)
		}
		sel, err := buildObjectMethodSelector(srv, recv.ObjectType(), fieldName, args)
		if err != nil {
			return nil, err
		}
		var val dagql.AnyResult
		if err := srv.Select(ctx, recv, &val, sel); err != nil {
			return nil, err
		}
		return m.routeObjectMethodResult(ctx, srv, typeName, val)
	}
}

// buildObjectMethodSelector converts the model's tool arguments into a selector
// for the method. It decodes each provided argument through the field's input
// spec; the Workspace argument is omitted here and auto-injected downstream.
func buildObjectMethodSelector(srv *dagql.Server, recvType dagql.ObjectType, fieldName string, args map[string]any) (dagql.Selector, error) {
	sel := dagql.Selector{View: srv.View, Field: fieldName}
	field, ok := recvType.FieldSpec(fieldName, srv.View)
	if !ok {
		return sel, fmt.Errorf("field %q not found on %q", fieldName, recvType.TypeName())
	}
	provided := maps.Clone(args)
	for _, arg := range field.Args.Inputs(srv.View) {
		if arg.Internal {
			continue
		}
		val, ok := args[arg.Name]
		if !ok {
			continue
		}
		delete(provided, arg.Name)
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return sel, fmt.Errorf("arg %q: decode %T: %w", arg.Name, val, err)
		}
		sel.Args = append(sel.Args, dagql.NamedInput{Name: arg.Name, Value: input})
	}
	if len(provided) > 0 {
		unknown := make([]string, 0, len(provided))
		for k := range provided {
			unknown = append(unknown, k)
		}
		slices.Sort(unknown)
		return sel, fmt.Errorf("unknown arguments: %s", strings.Join(unknown, ", "))
	}
	return sel, nil
}

// routeObjectMethodResult renders a method's result for the model, per the
// return-type table in hack/designs/workspace-agents.md:
//   - Changeset: overlay onto the workspace, return the patch summary.
//   - Workspace: replace the current workspace, return the diff summary.
//   - the bound object's own type: rebind it as the new state, return its print.
//   - any other object: sync it, return its print (else a type description).
//   - Void/null: return its print, else "(done)".
//   - scalar/list/record: return the value.
func (m *MCP) routeObjectMethodResult(ctx context.Context, srv *dagql.Server, typeName string, val dagql.AnyResult) (any, error) {
	// A Changeset overlays onto the workspace (and a Workspace replaces it),
	// returning a patch summary. step() persists the resulting workspace via a
	// withWorkspace selector.
	if handled, out, err := m.applyStateReturn(ctx, srv, val); handled {
		return out, err
	}

	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		if obj.Type().Name() == typeName {
			// Same-type return: the result is the agent's new state. Rebind it
			// (step() persists this as a withTools selector); the method's own print
			// output is the response.
			m.rebindBoundTool(typeName, obj)
			return m.logsOrDone(ctx), nil
		}
		// Any other object: force it so its side effects run, and surface whatever
		// it printed; fall back to describing it by type.
		if err := m.syncObject(ctx, srv, obj); err != nil {
			return nil, err
		}
		if logs := m.toolLogs(ctx); logs != "" {
			return logs, nil
		}
		return m.describeObject(ctx, srv, obj)
	}

	if val == nil || val.Type().Name() == "Void" {
		return m.logsOrDone(ctx), nil
	}

	// Scalar, list, enum, or record: return the value directly.
	return m.outputToLLM(ctx, srv, val)
}

// syncObject forces an object result (running its side effects) when it has a
// sync field, so a tool that returns e.g. a Container executes before we read
// its logs.
func (m *MCP) syncObject(ctx context.Context, srv *dagql.Server, obj dagql.AnyObjectResult) error {
	if _, ok := obj.ObjectType().FieldSpec("sync", srv.View); !ok {
		return nil
	}
	var synced dagql.AnyResult
	return srv.Select(ctx, obj, &synced, dagql.Selector{View: srv.View, Field: "sync"})
}

// logsOrDone returns whatever the just-executed method printed, or "(done)" when
// it printed nothing.
func (m *MCP) logsOrDone(ctx context.Context) string {
	if logs := m.toolLogs(ctx); logs != "" {
		return logs
	}
	return "(done)"
}

// toolLogs captures the print output emitted beneath the current tool-call span
// (created by MCP.Call). Empty when nothing was printed.
func (m *MCP) toolLogs(ctx context.Context) string {
	spanID := trace.SpanContextFromContext(ctx).SpanID()
	if !spanID.IsValid() {
		return ""
	}
	logs, err := m.captureLogs(ctx, spanID.String())
	if err != nil || len(logs) == 0 {
		return ""
	}
	logs = limitLines(spanID.String(), logs, llmToolLogsMaxLines, llmLogsMaxLineLen)
	return strings.TrimRight(strings.Join(logs, "\n"), "\n")
}
