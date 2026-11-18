package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

const workspaceInputsNamespace = "workspace-inputs/v7"

var legacyWorkspaceInputsNamespaces = [...]string{"workspace-inputs/v1", "workspace-inputs/v2", "workspace-inputs/v3", "workspace-inputs/v4", "workspace-inputs/v5", "workspace-inputs/v6"}

// Map native Host operations to the public source query that contains them.
type workspaceInputSourceContextKey struct{}

// Facts describe prepared inputs and the queries used to construct them.
type workspaceInputSource struct {
	Workspace     string                `json:"workspace"`
	Selectors     []provenance.Selector `json:"selectors"`
	Issues        []string              `json:"issues,omitempty"`
	Captures      []string              `json:"captures,omitempty"`
	KnownBoundary bool                  `json:"knownBoundary,omitempty"`
	producer      *dagql.ResultCallRef
}

func workspaceInputKey(ws *Workspace) string {
	if ws.Address == "" {
		return ""
	}
	return ws.ClientID + "\x00" + ws.Address
}

type workspaceInputSummary struct {
	Sources    []workspaceInputSource `json:"sources,omitempty"`
	FindUps    []workspaceFindUpRule  `json:"findUps,omitempty"`
	Issues     []string               `json:"issues,omitempty"`
	reads      []workspaceInputSource
	findUps    []workspaceFindUpRule
	incomplete bool
	// Only native consumption and call completion carry these fixed events.
	contextInputs *workspaceInputSummary
}

func (s *workspaceInputSummary) merge(other workspaceInputSummary) {
	s.Sources = append(s.Sources, other.Sources...)
	s.FindUps = append(s.FindUps, other.FindUps...)
	s.Issues = append(s.Issues, other.Issues...)
	s.reads = append(s.reads, other.reads...)
	s.findUps = append(s.findUps, other.findUps...)
	s.incomplete = s.incomplete || other.incomplete
	s.mergeContextInputEvents(other.contextInputs)
}

func (s workspaceInputSummary) normalized() workspaceInputSummary {
	// Callers can normalize an immutable prepared base concurrently. Even
	// malformed sources must not append into its shared issue storage.
	s.Issues = slices.Clone(s.Issues)
	normalizeRules := func(rules []workspaceFindUpRule) []workspaceFindUpRule {
		rules = slices.Clone(rules)
		slices.SortFunc(rules, func(a, b workspaceFindUpRule) int {
			if c := strings.Compare(a.Workspace, b.Workspace); c != 0 {
				return c
			}
			if c := strings.Compare(a.Cwd, b.Cwd); c != 0 {
				return c
			}
			return strings.Compare(a.Name, b.Name)
		})
		return slices.Compact(rules)
	}
	s.FindUps = normalizeRules(s.FindUps)
	s.findUps = normalizeRules(s.findUps)
	byWorkspace := map[string]workspaceInputSource{}
	for _, source := range s.Sources {
		if source.Workspace == "" {
			s.Issues = append(s.Issues, "source input has no workspace address")
			continue
		}
		key := source.Workspace
		if source.producer != nil {
			key += fmt.Sprintf("\x00%d", source.producer.ResultID)
		}
		group, exists := byWorkspace[key]
		group.Workspace = source.Workspace
		if !exists {
			group.KnownBoundary = source.KnownBoundary
		} else {
			group.KnownBoundary = group.KnownBoundary && source.KnownBoundary
		}
		group.producer = source.producer
		group.Selectors = append(group.Selectors, source.Selectors...)
		group.Issues = append(group.Issues, source.Issues...)
		group.Captures = append(group.Captures, source.Captures...)
		byWorkspace[key] = group
	}
	s.Sources = nil
	for _, group := range byWorkspace {
		group.Selectors = provenance.Normalize(group.Selectors)
		slices.Sort(group.Issues)
		group.Issues = slices.Compact(group.Issues)
		slices.Sort(group.Captures)
		group.Captures = slices.Compact(group.Captures)
		s.Sources = append(s.Sources, group)
	}
	slices.SortFunc(s.Sources, func(a, b workspaceInputSource) int { return strings.Compare(a.Workspace, b.Workspace) })
	slices.Sort(s.Issues)
	s.Issues = slices.Compact(s.Issues)
	if len(s.reads) > 0 {
		s.reads = (workspaceInputSummary{Sources: s.reads}).normalized().Sources
	}
	if s.contextInputs != nil {
		fixed := fixedWorkspaceContextInputs(*s.contextInputs)
		s.contextInputs = &fixed
	}
	return s
}

func (s workspaceInputSummary) forWorkspace(workspace string) ([]provenance.Selector, []string) {
	var selectors []provenance.Selector
	issues := slices.Clone(s.Issues)
	if workspace == "" {
		issues = append(issues, "current workspace has no address")
	}
	sources := slices.Clone(s.Sources)
	for _, rule := range s.FindUps {
		sources = append(sources, rule.source())
	}
	for _, source := range sources {
		if source.Workspace == "" {
			issues = append(issues, "source input has no workspace address")
			continue
		}
		if source.Workspace != workspace {
			if !source.KnownBoundary {
				issues = append(issues, "source Workspace boundary has no mapping to the inspected Workspace")
			}
			continue
		}
		selectors = append(selectors, source.Selectors...)
		issues = append(issues, source.Issues...)
	}
	slices.Sort(issues)
	return provenance.Normalize(selectors), slices.Compact(issues)
}

type workspaceInputCollector struct {
	ctx                    context.Context
	cache                  *dagql.Cache
	server                 *dagql.Server
	session                string
	seen                   map[*dagql.ResultCall]bool
	result                 workspaceInputSummary
	origin                 string
	scope                  string
	evaluating             bool
	native                 bool
	retained               map[uint64]bool
	sourceProducer         *dagql.ResultCallRef
	capabilitiesIncomplete bool
	sourcePreparation      workspaceInputSummary
	references             map[uint64]*dagql.ResultCallRef
	valueVisitor           func(dagql.AnyResult) error
}

func newWorkspaceInputCollector(ctx context.Context) (*workspaceInputCollector, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	server, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return &workspaceInputCollector{ctx: ctx, cache: cache, server: server, session: md.SessionID, seen: map[*dagql.ResultCall]bool{}, retained: map[uint64]bool{}}, nil
}

// workspaceInputsForCall visits the current call's inputs without executing the
// call. Module code paths are added separately from the module's source.
func workspaceInputsForCall(ctx context.Context, frame *dagql.ResultCall) (workspaceInputSummary, error) {
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return workspaceInputSummary{}, err
	}
	collector.evaluating = true
	collector.inputs(frame)
	return collector.result.normalized(), nil
}

func (c *workspaceInputCollector) issue(format string, args ...any) {
	c.result.incomplete = true
	issue := fmt.Sprintf(format, args...)
	if c.scope != "" {
		c.result.Sources = append(c.result.Sources, workspaceInputSource{Workspace: c.scope, Issues: []string{issue}, KnownBoundary: true})
		return
	}
	c.result.Issues = append(c.result.Issues, issue)
}

func (c *workspaceInputCollector) workspace(ref *dagql.ResultCallRef) (*Workspace, error) {
	if ref == nil || ref.ResultID == 0 {
		return nil, fmt.Errorf("Workspace input has no attached value")
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID)
	if err != nil {
		return nil, err
	}
	ws, ok := dagql.UnwrapAs[*Workspace](value)
	if !ok || ws == nil {
		return nil, fmt.Errorf("Workspace input has type %T", value.Unwrap())
	}
	return ws, nil
}

func (c *workspaceInputCollector) source(ws *Workspace, selector provenance.Selector) {
	if ws.Address == "" {
		c.issue("source input has no workspace address")
		return
	}
	boundary, err := c.sourceBoundary(ws)
	if err != nil {
		c.issue("cannot determine source Workspace boundary: %v", err)
		return
	}
	c.result.Sources = append(c.result.Sources, workspaceInputSource{Workspace: boundary, Selectors: []provenance.Selector{selector}, producer: c.sourceProducer, KnownBoundary: true})
	if c.sourceProducer != nil {
		c.result.reads = append(c.result.reads, workspaceInputSource{Workspace: boundary, Selectors: []provenance.Selector{selector}, producer: c.sourceProducer, KnownBoundary: true})
	}
}

func (c *workspaceInputCollector) inputs(frame *dagql.ResultCall) {
	if frame == nil {
		return
	}
	if c.evaluating && frame.Module != nil {
		c.moduleInputs(frame.Module.ResultRef)
	}
	previous := c.origin
	c.origin = "receiver"
	if frame.Field != "" {
		c.origin += " of " + frame.Field
	}
	c.ref(frame.Receiver)
	for _, arg := range frame.Args {
		if arg != nil {
			c.origin = "argument " + arg.Name
			if frame.Field != "" {
				c.origin += " of " + frame.Field
			}
			c.literal(arg.Value)
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input != nil {
			c.origin = "engine input " + input.Name
			c.literal(input.Value)
		}
	}
	c.origin = previous
}

func (c *workspaceInputCollector) moduleInputs(ref *dagql.ResultCallRef) {
	c.rememberReference(ref)
	if ref == nil {
		c.issue("called module has no attached source")
		return
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID)
	if err != nil {
		c.issue("cannot inspect module runtime inputs: %v", err)
		return
	}
	mod, ok := dagql.UnwrapAs[*Module](value)
	if !ok {
		c.issue("called module source has type %T", value.Unwrap())
		return
	}
	if c.builtinModuleInputs(mod) {
		return
	}
	evaluating := c.evaluating
	c.evaluating = false
	c.ref(ref)
	if mod.Runtime.Valid {
		c.retainedField(mod.Runtime.Value, "module runtime")
	}
	if mod.Definition.Valid {
		c.retainedField(mod.Definition.Value, "module definition")
		if definition := mod.Definition.Value.Self(); definition != nil && definition.Runtime.Valid {
			c.retainedField(definition.Runtime.Value, "module definition runtime")
		}
	}
	c.evaluating = evaluating
	if src := mod.GetSource(); c.evaluating && !mod.Runtime.Valid && src != nil && src.SDKImpl != nil {
		_, moduleBacked := src.SDKImpl.AsModule()
		if sdk, ok := src.SDKImpl.(*persistedModuleSourceLazySDK); ok {
			moduleBacked = sdk.capabilities.ModuleBacked == nil || *sdk.capabilities.ModuleBacked
		}
		if _, runtime := src.SDKImpl.AsRuntime(); moduleBacked && runtime {
			c.issue("SDK runtime inputs have not been prepared")
		}
	}
	c.entrypointModuleInputs(mod)
}

func (c *workspaceInputCollector) builtinModuleInputs(mod *Module) bool {
	src := mod.GetSource()
	if src == nil || src.SDKImpl == nil {
		return false
	}
	if _, ok := src.SDKImpl.AsModule(); ok {
		return false
	}
	if sdk, ok := src.SDKImpl.(*persistedModuleSourceLazySDK); ok &&
		(sdk.capabilities.ModuleBacked == nil || *sdk.capabilities.ModuleBacked) {
		return false
	}
	ws := workspaceInputSourceWorkspace(c.ctx)
	if ws == nil {
		ws = src.Workspace.Self()
		if ws != nil {
			// The source owns this attached Workspace. Bind that producer
			// while collecting its regions, without turning module source
			// metadata into body query events.
			previous := c.ctx
			c.ctx = WorkspaceToContext(c.ctx, src.Workspace)
			defer func() { c.ctx = previous }()
		}
	}
	if ws == nil {
		return false
	}
	// Native SDKs load a module context to prepare its implementation. The
	// module's source regions describe those inputs, not the loader's ancestry.
	c.moduleSources(ws, mod, "module source")
	c.declaredClientSources(ws, mod)
	c.entrypointModuleInputs(mod)
	return true
}

func (c *workspaceInputCollector) entrypointModuleInputs(mod *Module) {
	if mod == nil {
		return
	}
	src := mod.GetSource()
	if src == nil || src.Entrypoint == nil {
		return
	}
	if src.EntrypointSources {
		id, err := src.ContextDirectory.ID()
		if err != nil || id == nil {
			c.issue("entrypoint source directory has no attached value")
			return
		}
		c.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
		ws := workspaceInputSourceWorkspace(c.ctx)
		boundary, boundaryErr := c.sourceBoundary(ws)
		captured := ws != nil && src.EntrypointInputsKey != "" && slices.ContainsFunc(c.result.Sources, func(source workspaceInputSource) bool {
			return boundaryErr == nil && source.Workspace == boundary && slices.Contains(source.Captures, src.EntrypointInputsKey)
		})
		if !captured {
			c.issue("entrypoint sources input has no complete source query capture")
		}
		return
	}
	var ws *Workspace
	if bound, ok := WorkspaceFromContext(c.ctx); ok {
		ws = bound.Self()
	} else {
		query, err := CurrentQuery(c.ctx)
		if err != nil {
			c.issue("cannot inspect entrypoint workspace: %v", err)
			return
		}
		ws, err = query.Server.CurrentWorkspace(c.ctx)
		if err != nil {
			c.issue("cannot inspect entrypoint workspace: %v", err)
			return
		}
	}
	if ws == nil {
		c.issue("entrypoint has no workspace")
		return
	}
	c.source(ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: "entrypoint workspace"})
}

func (c *workspaceInputCollector) literal(value *dagql.ResultCallLiteral) {
	if value == nil {
		return
	}
	c.ref(value.ResultRef)
	for _, item := range value.ListItems {
		c.literal(item)
	}
	for _, field := range value.ObjectFields {
		if field != nil {
			c.literal(field.Value)
		}
	}
}

func (c *workspaceInputCollector) ref(ref *dagql.ResultCallRef) {
	if ref == nil {
		return
	}
	c.rememberReference(ref)
	frame, err := c.cache.ResultCallForRef(ref)
	if err != nil {
		c.issue("cannot inspect input call: %v", err)
		return
	}
	if frame == nil {
		c.issue("input has no call frame")
		return
	}
	if c.seen[frame] {
		return
	}
	c.seen[frame] = true
	if ref.ResultID != 0 && frame.Nth > 0 && frame.Receiver != nil && frame.Receiver.ResultID != 0 {
		list, err := c.cache.ResultCallForRef(frame.Receiver)
		if err == nil && list != nil && list.Type != nil && list.Type.Elem != nil &&
			frame.Field == list.Field && frame.Kind == list.Kind && frame.SyntheticOp == list.SyntheticOp && reflect.DeepEqual(frame.Type, list.Type.Elem) {
			// A current list projection inherits its list's source records.
			// Copied facts still describe the list frame and its input paths.
			c.listItemDiagnostics(ref, frame)
			c.ref(frame.Receiver)
			return
		}
	}
	if frame.Module == nil && workspaceInputTransport(frame.Type) {
		return
	}
	typeName := ""
	if frame.Type != nil {
		typeName = frame.Type.NamedType
	}
	if c.native && typeName == "ModuleSource" && frame.Module == nil && frame.Field == "_implementationScoped" && ref.ResultID != 0 {
		inFunction, err := CallerInModuleFunction(c.ctx)
		if err == nil && !inFunction {
			if value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID); err == nil {
				if source, ok := value.(dagql.ObjectResult[*ModuleSource]); ok && c.builtinModuleInputs(&Module{Source: dagql.NonNull(source)}) {
					return
				}
			}
		}
	}
	nativeModule := typeName == "Module" && frame.Module == nil && (frame.Field == "module" || frame.Field == "_implementationScoped" || strings.HasPrefix(frame.Field, "with") || frame.Kind == dagql.ResultCallKindSynthetic && frame.SyntheticOp == "sdk_scope_module")
	if nativeModule && ref.ResultID != 0 {
		if value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID); err == nil {
			if mod, ok := dagql.UnwrapAs[*Module](value); ok {
				if c.builtinModuleInputs(mod) {
					return
				}
				if ws := workspaceInputSourceWorkspace(c.ctx); ws != nil {
					c.moduleSources(ws, mod, "module source")
				}
			}
		}
	}
	if typeName == "Query" {
		return
	}
	if typeName == "Host" {
		// The handle gives an unrun function access to the client's host. A
		// completed Host operation records its own source effects instead.
		if c.evaluating {
			c.issue("retained Host input has no workspace source mapping (%s)", c.origin)
		}
		return
	}
	operation := workspaceInputOperation(c.ctx, frame)
	owned := false
	captured := false
	capabilities := false
	mergePayload := func(payload json.RawMessage) {
		var fact workspaceInputFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			c.issue("cannot decode %s input origin: %v", typeName, err)
			return
		}
		if fact.Operation != operation {
			return
		}
		owned = true
		c.result.merge(c.materializeReads(fact, frame))
		captured = captured || fact.Complete
		if fact.CapabilitiesComplete {
			capabilities = true
			if c.evaluating {
				for _, retained := range fact.Retained {
					c.result.Sources = append(c.result.Sources, workspaceInputSource{Workspace: retained.Workspace, Selectors: []provenance.Selector{{Path: "/", Kind: provenance.WorkspaceRead, Origin: c.origin}}, KnownBoundary: true})
				}
			}
		}
	}
	// Cached facts remain valid after their producing request ends.
	facts, complete, err := c.cache.ResultFacts(context.WithoutCancel(c.ctx), c.session, ref, workspaceInputsNamespace)
	if err != nil {
		c.issue("cannot inspect %s input origins for %s: %v", typeName, operation, err)
	} else if !complete {
		c.issue("%s input has incomplete source origins for %s", typeName, operation)
	}
	for _, fact := range facts {
		mergePayload(fact.Payload)
	}
	c.legacySources(ref)
	if c.evaluating && ref.ResultID != 0 {
		value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID)
		if err != nil {
			c.issue("cannot inspect retained %s inputs: %v", typeName, err)
		} else if _, object := dagql.UnwrapAs[*ModuleObject](value); !(object && complete && captured && capabilities) {
			if typeName != "Workspace" || !capabilities {
				c.retainedValue(value)
			}
		}
	}
	if owned {
		if !captured {
			c.issue("%s input has incomplete source query capture for %s", typeName, operation)
		}
		// This record owns the completed operation. Its input capabilities
		// describe preparation, not access retained by its output. Unknown
		// origins remain issues; ancestry cannot resolve them by adding reads.
		return
	}
	if nativeModule {
		c.inputs(frame)
		return
	}
	if !captured && frame.Module != nil && !c.storedField(frame) {
		c.issue("%s input has no complete source query capture for %s", typeName, operation)
	}
	c.inputs(frame)
}

// Copied list facts describe another frame. Preserve their diagnostics without
// interpreting their input paths or retained capabilities against this item.
func (c *workspaceInputCollector) listItemDiagnostics(ref *dagql.ResultCallRef, frame *dagql.ResultCall) {
	facts, complete, err := c.cache.ResultFacts(context.WithoutCancel(c.ctx), c.session, ref, workspaceInputsNamespace)
	if err != nil {
		c.issue("cannot inspect list item source origins: %v", err)
	} else if !complete {
		c.issue("list item has incomplete source origins for %s", workspaceInputOperation(c.ctx, frame))
	}
	for _, record := range facts {
		var fact workspaceInputFact
		if err := json.Unmarshal(record.Payload, &fact); err != nil {
			c.issue("cannot decode list item source diagnostics: %v", err)
			continue
		}
		c.listItemFactDiagnostics(fact)
	}
	c.legacySources(ref)
}

func (c *workspaceInputCollector) listItemFactDiagnostics(fact workspaceInputFact) {
	c.result.Issues = append(c.result.Issues, fact.Issues...)
	c.result.Issues = append(c.result.Issues, fact.Prepared.Issues...)
	for _, source := range fact.Prepared.Sources {
		c.result.Issues = append(c.result.Issues, source.Issues...)
	}
	for _, read := range fact.Reads {
		c.result.Issues = append(c.result.Issues, read.Source.Issues...)
	}
	if !fact.Complete {
		c.issue("list item has incomplete source query capture for %s", fact.Operation)
	}
}

// Inline SDK objects have field call frames, but no field resolver ran. Their
// inputs are the containing object's recorded inputs.
func (c *workspaceInputCollector) storedField(frame *dagql.ResultCall) bool {
	if frame.Receiver == nil || frame.Receiver.ResultID == 0 {
		return false
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, frame.Receiver.ResultID)
	if err != nil {
		return false
	}
	obj, ok := dagql.UnwrapAs[*ModuleObject](value)
	if !ok {
		return false
	}
	_, ok = obj.TypeDef.FieldByName(frame.Field)
	return ok
}

// A declared stored field is a pure read of its exact attached owner. Check the
// installed field too: the trivial-field context can belong to an outer call.
func (c *workspaceInputCollector) verifiedStoredField(req *dagql.CallRequest) bool {
	projection := req.Kind == dagql.ResultCallKindSynthetic && len(req.Args) == 1 && req.Args[0] != nil && req.Args[0].Name == moduleStoredFieldProjectionArg
	if !projection && (!dagql.CurrentFieldIsTrivial(c.ctx) || req.Kind != dagql.ResultCallKindField) {
		return false
	}
	if req.Nth != 0 || req.Receiver == nil || req.Receiver.ResultID == 0 {
		return false
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, req.Receiver.ResultID)
	if err != nil {
		return false
	}
	obj, ok := dagql.UnwrapAs[*ModuleObject](value)
	if !ok || obj == nil || obj.TypeDef == nil {
		return false
	}
	field, ok := obj.TypeDef.FieldByName(req.Field)
	if !ok {
		return false
	}
	if projection {
		if req.SyntheticOp != field.Name || req.ReceiverTypeName != obj.Type().Name() || !reflect.DeepEqual(req.Type, dagql.NewResultCallType(field.TypeDef.Self().ToType())) ||
			req.Args[0].Value == nil || req.Args[0].Value.Kind != dagql.ResultCallLiteralKindString ||
			req.Args[0].Value.StringValue != moduleStoredFieldProjectionMarker || len(req.ImplicitInputs) != 0 {
			return false
		}
		members, err := obj.TypeDef.CollectionMembers()
		if err != nil || members != nil && members.Delta != nil && members.Delta.Name == field.Name {
			return false
		}
		if obj.Module.Self() == nil {
			return false
		}
		moduleID, err := obj.Module.ID()
		return err == nil && moduleID != nil && moduleID.EngineResultID() != 0 && req.Module != nil &&
			req.Module.Name == obj.Module.Self().Name() && req.Module.Ref == "" && req.Module.Pin == "" &&
			req.Module.ResultRef != nil && req.Module.ResultRef.Call == nil && req.Module.ResultRef.ResultID == moduleID.EngineResultID()
	}
	owner, err := c.server.ToSelectable(c.ctx, value)
	if err != nil {
		return false
	}
	spec, ok := owner.ObjectType().FieldSpec(req.Field, req.View)
	return ok && spec.Trivial
}

func (c *workspaceInputCollector) legacySources(ref *dagql.ResultCallRef) {
	for _, namespace := range legacyWorkspaceInputsNamespaces {
		facts, _, err := c.cache.ResultFacts(context.WithoutCancel(c.ctx), c.session, ref, namespace)
		if err != nil {
			c.issue("cannot inspect earlier source origins: %v", err)
			return
		}
		for _, fact := range facts {
			for _, issue := range workspaceLegacyIssues(fact.Payload) {
				c.issue("%s", issue)
			}
		}
	}
}

func workspaceLegacyIssues(payload json.RawMessage) []string {
	issues := []string{"source origins predate complete owned-input capture"}
	var summary struct {
		workspaceInputSummary
		Prepared workspaceInputSummary `json:"prepared"`
	}
	if err := json.Unmarshal(payload, &summary); err != nil {
		return append(issues, "cannot decode earlier source origin diagnostics: "+err.Error())
	}
	issues = append(issues, summary.Issues...)
	issues = append(issues, summary.Prepared.Issues...)
	for _, source := range append(summary.Sources, summary.Prepared.Sources...) {
		issues = append(issues, source.Issues...)
	}
	return issues
}

func (c *workspaceInputCollector) retainedValue(value dagql.AnyResult) {
	if value == nil || value.Unwrap() == nil {
		return
	}
	var present bool
	value, present = value.DerefValue()
	if !present || value == nil || value.Unwrap() == nil {
		return
	}
	if id, err := value.ID(); err == nil && id != nil && id.EngineResultID() != 0 {
		if c.retained[id.EngineResultID()] {
			return
		}
		c.retained[id.EngineResultID()] = true
	}
	if ws, ok := dagql.UnwrapAs[*Workspace](value); ok && ws != nil {
		id, err := value.ID()
		if err != nil || id == nil || id.EngineResultID() == 0 {
			c.issue("retained Workspace has no attached source producer")
			return
		}
		boundary, err := c.workspaceBoundary(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
		if err != nil {
			c.issue("cannot determine retained Workspace source boundary: %v", err)
			return
		}
		c.result.Sources = append(c.result.Sources, workspaceInputSource{Workspace: boundary, Selectors: []provenance.Selector{{Path: "/", Kind: provenance.WorkspaceRead, Origin: c.origin}}, KnownBoundary: true})
		return
	}
	_, sdkCapability := dagql.UnwrapAs[*WorkspaceSDK](value)
	_, moduleCapability := dagql.UnwrapAs[*WorkspaceModule](value)
	_, sourceCapability := dagql.UnwrapAs[*ModuleSource](value)
	if sdkCapability || moduleCapability || sourceCapability {
		// Use current producer bindings, including overrides for an equal
		// wrapper payload that still stores another owner's Workspace.
		err := c.workspaceValues(value, nil, false, func(_ workspaceInputPath, _ *dagql.ResultCallRef, workspace string) {
			c.result.Sources = append(c.result.Sources, workspaceInputSource{Workspace: workspace, Selectors: []provenance.Selector{{Path: "/", Kind: provenance.WorkspaceRead, Origin: c.origin}}, KnownBoundary: true})
		})
		if err != nil {
			c.issue("cannot inspect retained Workspace capability: %v", err)
		}
		return
	}
	if git, ok := dagql.UnwrapAs[*WorkspaceGit](value); ok && git != nil {
		c.retainedValue(git.Workspace)
		return
	}
	if address, ok := dagql.UnwrapAs[*Address](value); ok && address != nil {
		c.retainedValue(address.BoundWorkspace)
		return
	}
	if agent, ok := dagql.UnwrapAs[*Agent](value); ok && agent != nil {
		c.issue("Agent input has live conversation state")
		return
	}
	if llm, ok := dagql.UnwrapAs[*LLM](value); ok && llm != nil {
		c.retainedValue(llm.Workspace())
		for _, expertise := range llm.Expertise {
			c.retainedValue(expertise)
		}
		if llm.mcp != nil {
			for _, tool := range llm.mcp.boundTools {
				if tool.object != nil {
					c.retainedValue(tool.object)
				} else if tool.id != nil && tool.id.EngineResultID() != 0 {
					c.ref(&dagql.ResultCallRef{ResultID: tool.id.EngineResultID()})
				} else {
					c.issue("lazy tool %s has no materialized input scope", tool.typeName())
				}
			}
		}
		return
	}
	if artifact, ok := dagql.UnwrapAs[*Artifact](value); ok && artifact != nil {
		if artifact.Workspace.Self() != nil {
			c.retainedValue(artifact.Workspace)
		} else {
			c.retainedArtifactCall(artifact, JSON("{}"))
		}
		return
	}
	if artifacts, ok := dagql.UnwrapAs[*Artifacts](value); ok && artifacts != nil {
		for _, artifact := range append(slices.Clone(artifacts.Entries), artifacts.CollectionRoots...) {
			if artifact.Workspace.Self() != nil {
				c.retainedValue(artifact.Workspace)
			} else {
				c.retainedArtifactCall(artifact, JSON("{}"))
			}
		}
		return
	}
	if obj, ok := dagql.UnwrapAs[*ModuleObject](value); ok && obj != nil {
		if obj.Module.Self() != nil {
			id, err := obj.Module.ID()
			if err != nil || id == nil {
				c.issue("retained object module has no attached source")
			} else {
				c.moduleInputs(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
			}
		}
		for _, input := range workspaceModuleObjectInputs(obj) {
			c.retainedField(input.value, input.step)
		}
		return
	}
	if check, ok := dagql.UnwrapAs[*Check](value); ok && check != nil {
		previous := c.ctx
		if check.Workspace.Self() != nil {
			c.ctx = WorkspaceToContext(c.ctx, check.Workspace)
		}
		defer func() { c.ctx = previous }()
		if receiver := check.Receiver.Self(); receiver != nil {
			c.entrypointModuleInputs(receiver.Module.Self())
		}
		c.retainedValue(check.Receiver)
		c.retainedValue(check.Unaddressed)
		c.retainedArtifactCall(check.RemoteArtifact.Self(), check.RemoteArguments)
		c.retainedValue(check.Generator)
		for _, input := range check.Inputs {
			c.retainedField(input.Value, "argument "+input.Name+" of "+check.Function)
		}
		return
	}
	if generator, ok := dagql.UnwrapAs[*Generator](value); ok && generator != nil {
		c.retainedArtifactCall(generator.Artifact, generator.Arguments)
		return
	}
	if expertise, ok := dagql.UnwrapAs[*Expertise](value); ok && expertise != nil {
		c.retainedArtifactCall(expertise.Artifact, JSON("{}"))
		return
	}
	if array, ok := value.Unwrap().(dagql.Enumerable); ok {
		frame, err := value.ResultCall()
		if err != nil {
			c.issue("cannot inspect retained list inputs: %v", err)
			return
		}
		for i := 1; i <= array.Len(); i++ {
			item, err := array.NthValue(i, frame)
			if err != nil {
				c.issue("cannot inspect retained list item: %v", err)
				continue
			}
			c.retainedValue(item)
		}
	}
}

func (c *workspaceInputCollector) retainedField(value any, origin string) {
	previous := c.origin
	c.origin += ": " + origin
	defer func() { c.origin = previous }()
	if input, ok := value.(dagql.Input); ok {
		request := &dagql.CallRequest{ResultCall: &dagql.ResultCall{}}
		if err := request.SetImplicitInput(c.ctx, "input", input); err != nil {
			c.issue("cannot inspect retained input: %v", err)
			return
		}
		c.literal(request.ImplicitInputs[0].Value)
		return
	}
	_, _, err := attachModuleObjectValue(c.ctx, func(value dagql.AnyResult) (dagql.AnyResult, error) {
		if value == nil || value.Unwrap() == nil {
			return value, nil
		}
		id, err := value.ID()
		if err != nil {
			return nil, err
		}
		if id != nil && id.EngineResultID() != 0 {
			c.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
		} else {
			c.retainedValue(value)
		}
		return value, nil
	}, value)
	if err != nil {
		c.issue("cannot inspect retained input: %v", err)
	}
}

func (c *workspaceInputCollector) retainedArtifactCall(artifact *Artifact, arguments JSON) {
	if artifact == nil {
		return
	}
	inputs, err := artifact.CallInputs(arguments)
	if err != nil {
		c.issue("cannot inspect deferred artifact inputs: %v", err)
		return
	}
	ctx, frame, err := artifact.plannedProvenanceCall(c.ctx, inputs...)
	if err != nil {
		c.issue("cannot prepare deferred artifact inputs: %v", err)
		return
	}
	previous := c.ctx
	c.ctx = ctx
	c.inputs(frame)
	c.ctx = previous
}

// ObserveWorkspaceInputs preserves source queries and input origins across cache
// reuse. Module bodies add their queries before the result is published.
func ObserveWorkspaceInputs(ctx context.Context, req *dagql.CallRequest) (context.Context, func(), func(dagql.AnyResult, error)) {
	if req == nil || req.ResultCall == nil {
		return ctx, nil, nil
	}
	parent := currentWorkspaceInputReads(ctx)
	transport := req.Module == nil && (req.ReceiverTypeName == "FunctionCall" || req.ReceiverTypeName == "FunctionCallArgValue" || workspaceInputTransport(req.Type))
	if req.Field == "id" || (req.ReceiverTypeName == "Query" && (req.Field == "node" || req.Field == "currentFunctionCall")) || transport {
		return ctx, nil, nil
	}
	if _, inSource := ctx.Value(workspaceInputSourceContextKey{}).(workspaceInputSummary); inSource && req.Module == nil {
		// The enclosing source operation owns the effects. Its native
		// implementation still has to account for inputs imported by ID
		// before treating a completed output as produced in this scope.
		if parent == nil {
			return ctx, nil, nil
		}
		return ctx, nil, func(result dagql.AnyResult, err error) {
			if err != nil {
				result = nil
			}
			parent.checkBodyInputs(ctx, req.ResultCall, result)
		}
	}
	workspaceOperation := workspaceInputObservedOperation
	if req.Module == nil && req.ReceiverTypeName == "Workspace" {
		workspaceOperation = workspaceInputOperationClass(req.Field)
	}
	source := workspaceOperation == workspaceInputReadOperation
	gitSource := req.Module == nil && req.ReceiverTypeName == "WorkspaceGit"
	host := req.Module == nil && req.ReceiverTypeName == "Host"
	view := workspaceInputView(req)
	metadata := req.Module == nil && req.ReceiverTypeName == "Query" && req.Field == "currentModule"
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		req.ResultFactsIncomplete = true
		return ctx, nil, nil
	}
	collector.native = req.Module == nil
	storedField := collector.verifiedStoredField(req)
	switch {
	case source:
		collector.sourceCall(req)
	case gitSource:
		collector.gitSourceCall(req)
	case host:
		collector.hostCall(req)
	default:
		collector.inputs(req.ResultCall)
	}
	if workspaceOperation == workspaceInputUnknownOperation {
		collector.issue("Workspace.%s has unclassified source effects", req.Field)
	}
	if view && req.ReceiverTypeName == "Query" {
		if ws, ok := WorkspaceFromContext(ctx); ok {
			if id, err := ws.ID(); err == nil && id != nil {
				collector.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
			}
		}
	}
	if req.Module != nil {
		collector.moduleInputs(req.Module.ResultRef)
		collector.calledModuleSource(req)
	}
	summary := collector.result.normalized()
	initialReads := summary.reads
	summary.reads = nil
	if source || gitSource {
		summary.reads = initialReads
	}
	req.ResultFactsPrepared = source || gitSource || host || view || metadata || storedField
	if source || gitSource || view {
		ctx = context.WithValue(ctx, workspaceInputSourceContextKey{}, summary)
	}
	workspace, boundary := workspaceInputSourceOrigin(ctx)
	observation := &workspaceInputCall{
		request:     req,
		reads:       &workspaceInputReads{summary: summary, workspace: workspace, boundary: boundary},
		prepared:    source || gitSource || host || view || metadata || storedField,
		storedField: storedField,
		initial:     summary,
	}
	// Host rules are events from this call, rather than prepared inputs.
	if host && len(collector.result.findUps) != 0 {
		observation.initial = collector.sourcePreparation
	}
	if source || gitSource {
		observation.initial = collector.sourcePreparation
	}
	observation.bindings, err = collector.inputBindings(req.ResultCall)
	if err != nil {
		observation.initial.Issues = append(observation.initial.Issues, "cannot inspect prepared Workspace bindings: "+err.Error())
		observation.initial.incomplete = true
	}
	observation.reads.rememberInputs(collector.references)
	req.CaptureResultFacts = observation.captureFacts
	req.RebindResultFacts = observation.rebindFacts
	if req.Module == nil && req.ReceiverTypeName == "Workspace" {
		if ws, err := collector.workspace(req.Receiver); err == nil {
			observation.reads.workspace = ws
			observation.reads.boundary, _ = collector.workspaceBoundary(req.Receiver)
		}
	} else if bound, ok := WorkspaceFromContext(ctx); ok {
		observation.reads.boundary, _ = workspaceResultBoundary(ctx, bound)
	}
	if source || gitSource || view {
		observation.view = &summary
	}
	ctx = context.WithValue(ctx, workspaceInputCallKey{}, observation)
	ctx = context.WithValue(ctx, workspaceInputReadsKey{}, observation.reads)
	return ctx, func() { publishWorkspaceInputReads(ctx) }, workspaceInputReadCompletion(ctx, parent, observation)
}

func (c *workspaceInputCollector) calledModuleSource(req *dagql.CallRequest) {
	ws := workspaceInputSourceWorkspace(c.ctx)
	if ws == nil || req.Module.ResultRef == nil {
		return
	}
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, req.Module.ResultRef.ResultID)
	if err != nil {
		c.issue("cannot inspect called module source: %v", err)
		return
	}
	mod, ok := dagql.UnwrapAs[*Module](value)
	if !ok {
		c.issue("called module source has type %T", value.Unwrap())
		return
	}
	c.moduleSources(ws, mod, "module source of "+req.Field)
}

func (c *workspaceInputCollector) moduleSources(ws *Workspace, mod *Module, origin string) {
	for _, selector := range moduleSourceSelectors(ws, mod) {
		selector.Origin = origin
		c.source(ws, selector)
	}
	seen := map[*ModuleSource]bool{}
	var source func(*ModuleSource)
	source = func(src *ModuleSource) {
		if src == nil || seen[src] {
			return
		}
		seen[src] = true
		if _, mapped := moduleSourceWorkspaceRoot(ws, src); !mapped && src.Kind == ModuleSourceKindDir {
			dir := src.ContextDirectory
			if src.DirSrc != nil && src.DirSrc.OriginalContextDir.Self() != nil {
				dir = src.DirSrc.OriginalContextDir
			}
			if id, err := dir.ID(); err == nil && id != nil {
				c.ref(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
			}
		}
		for _, dep := range src.Dependencies {
			source(dep.Self())
		}
		if src.SDKImpl != nil {
			if sdk, ok := src.SDKImpl.AsModule(); ok && sdk.Self() != nil {
				source(sdk.Self().GetSource())
			}
		}
	}
	source(mod.GetSource())
}

func (c *workspaceInputCollector) gitSourceCall(req *dagql.CallRequest) {
	c.ref(req.Receiver)
	c.result.reads = nil
	c.sourcePreparation = c.result.normalized()
	value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, req.Receiver.ResultID)
	if err != nil {
		c.issue("cannot inspect Git workspace: %v", err)
		return
	}
	git, ok := dagql.UnwrapAs[*WorkspaceGit](value)
	if !ok || git == nil || git.Workspace.Self() == nil {
		c.issue("Git input has no workspace")
		return
	}
	previous := c.sourceProducer
	if id, err := git.Workspace.ID(); err == nil && id != nil {
		c.sourceProducer = &dagql.ResultCallRef{ResultID: id.EngineResultID()}
	}
	defer func() { c.sourceProducer = previous }()
	c.source(git.Workspace.Self(), provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: "Workspace.git." + req.Field})
}

func (c *workspaceInputCollector) sourceCall(req *dagql.CallRequest) {
	c.ref(req.Receiver)
	c.result.reads = nil
	c.sourcePreparation = c.result.normalized()
	previous := c.sourceProducer
	c.sourceProducer = req.Receiver
	defer func() { c.sourceProducer = previous }()
	ws, err := c.workspace(req.Receiver)
	if err != nil {
		c.issue("cannot inspect source workspace: %v", err)
		return
	}
	c.scope, err = c.workspaceBoundary(req.Receiver)
	if err != nil {
		c.issue("cannot determine source Workspace boundary: %v", err)
		return
	}
	if req.Field == "glob" {
		arg := req.Arg("pattern")
		if arg == nil || arg.Value == nil {
			c.issue("source glob has no pattern")
			return
		}
		c.source(ws, provenance.Selector{Path: "/", Include: []string{strings.TrimPrefix(arg.Value.StringValue, "/")}, Kind: provenance.WorkspaceRead, Origin: "Workspace.glob"})
		return
	}
	if req.Field == "findUp" {
		name := req.Arg("name")
		if name != nil && name.Value != nil && name.Value.Kind == dagql.ResultCallLiteralKindString && name.Value.StringValue != "." {
			from := "."
			if arg := req.Arg("from"); arg != nil && arg.Value != nil {
				from = arg.Value.StringValue
			}
			selectors, err := workspaceFindUpSelectors(name.Value.StringValue, ws.Cwd, from, "Workspace.findUp")
			if err != nil {
				c.issue("cannot resolve Workspace.findUp filtering paths: %v", err)
				return
			}
			for _, selector := range selectors {
				c.source(ws, selector)
			}
			return
		}
	}
	if req.Field != "file" && req.Field != "directory" {
		c.source(ws, provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Origin: "Workspace." + req.Field})
		return
	}
	arg := req.Arg("path")
	if arg == nil || arg.Value == nil || arg.Value.Kind != dagql.ResultCallLiteralKindString {
		c.issue("source input has no path")
		return
	}
	inputPath := path.Clean(strings.ReplaceAll(arg.Value.StringValue, `\`, "/"))
	resolved := inputPath
	if !path.IsAbs(inputPath) {
		resolved = path.Join(ws.Cwd, inputPath)
	}
	resolved, err = provenance.Path(resolved)
	if err != nil {
		c.issue("cannot resolve source input path: %v", err)
		return
	}
	selector := provenance.Selector{Path: resolved, Kind: provenance.WorkspaceRead, Origin: "Workspace." + req.Field}
	if req.Field == "directory" {
		patterns := func(name string) []string {
			var patterns []string
			if arg := req.Arg(name); arg != nil && arg.Value != nil {
				for _, item := range arg.Value.ListItems {
					if item != nil && item.Kind == dagql.ResultCallLiteralKindString {
						patterns = append(patterns, item.StringValue)
					}
				}
			}
			return patterns
		}
		selector.Include = patterns("include")
		selector.Exclude = patterns("exclude")
		gitignore := false
		if arg := req.Arg("gitignore"); arg != nil && arg.Value != nil {
			gitignore = arg.Value.BoolValue
		}
		if gitignore {
			if bounded, ok := c.boundDirectoryIgnoreRead(ws, selector); ok {
				selector = bounded
			} else {
				selector = provenance.Selector{Path: "/", Kind: provenance.WorkspaceRead, Conservative: true, Origin: "Workspace.directory"}
			}
		}
	}
	if selector.Path != "/" {
		if err := c.unaliasedSourcePath(ws, selector.Path); err != nil {
			c.issue("cannot determine source path %q: %v", selector.Path, err)
		}
	}
	c.source(ws, selector)
}

// A narrow selector cannot represent a symlink's target chain. Check every
// source prefix before treating its spelling as its complete dependency.
func (c *workspaceInputCollector) unaliasedSourcePath(ws *Workspace, sourcePath string) error {
	if ws.MountedPath(strings.TrimPrefix(sourcePath, "/")) || ws.HasMountsUnder(strings.TrimPrefix(sourcePath, "/")) {
		return fmt.Errorf("mounted input has no source path mapping")
	}
	var stat func(string) (*Stat, error)
	if root, ok := ws.SourceDirectory(); ok && root.Self() != nil {
		stat = func(prefix string) (*Stat, error) {
			return root.Self().Stat(c.ctx, root, c.server, prefix, true)
		}
	} else if ws.HostPath() != "" {
		ctx, err := WorkspaceClientContext(c.ctx, ws)
		if err != nil {
			return err
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		bk, err := query.Engine(ctx)
		if err != nil {
			return err
		}
		caller := NewCallerStatFS(bk)
		stat = func(prefix string) (*Stat, error) {
			if delta, ok := ws.OverlayDeltaRoot(); ok {
				entry, err := delta.Self().Stat(c.ctx, delta, c.server, prefix, true)
				if err == nil || !errors.Is(err, fs.ErrNotExist) || ws.OverlayPathTouched(prefix) {
					return entry, err
				}
			}
			_, entry, err := caller.Stat(ctx, filepath.Join(ws.HostPath(), filepath.FromSlash(prefix)))
			return entry, err
		}
	} else {
		return fmt.Errorf("workspace has no inspectable source")
	}
	prefix := ""
	for _, part := range strings.Split(strings.Trim(sourcePath, "/"), "/") {
		prefix = path.Join(prefix, part)
		entry, err := stat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry == nil || entry.FileType == FileTypeSymlink {
			return fmt.Errorf("source prefix %q is a symlink or has no type", prefix)
		}
	}
	return nil
}
