package core

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
)

type workspaceInputReads struct {
	mu        sync.Mutex
	summary   workspaceInputSummary
	initial   *workspaceInputSummary
	workspace *Workspace
	boundary  string
	runtime   *workspaceInputSummary
	values    map[uint64]*dagql.ResultCallRef
	// Prepared bases are immutable and belong to receiver conversion memos.
	// Only values and summary contain this call's later additions.
	prepared []*workspacePreparedInputs
}

func workspaceInputSourceWorkspace(ctx context.Context) *Workspace {
	workspace, _ := workspaceInputSourceOrigin(ctx)
	return workspace
}

// The payload and boundary come from the same owned context. A payload alone
// cannot establish its source after cache reuse or a client route change.
func workspaceInputSourceOrigin(ctx context.Context) (*Workspace, string) {
	if ws, ok := WorkspaceFromContext(ctx); ok && ws.Self() != nil {
		boundary, _ := workspaceResultBoundary(ctx, ws)
		return ws.Self(), boundary
	}
	if reads := currentWorkspaceInputReads(ctx); reads != nil {
		return reads.workspace, reads.boundary
	}
	return nil, ""
}

func (r *workspaceInputReads) add(summary workspaceInputSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.merge(summary)
}

func (r *workspaceInputReads) snapshot() workspaceInputSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(r.initial)
}

func (r *workspaceInputReads) snapshotLocked(initial *workspaceInputSummary) workspaceInputSummary {
	if len(r.prepared) == 0 && initial == nil {
		return r.summary.normalized()
	}
	// Do not append into either the live holder or an immutable base.
	summary := r.summary
	summary.Sources = slices.Clone(summary.Sources)
	summary.FindUps = slices.Clone(summary.FindUps)
	summary.Issues = slices.Clone(summary.Issues)
	summary.reads = slices.Clone(summary.reads)
	summary.findUps = slices.Clone(summary.findUps)
	if initial != nil {
		summary.merge(*initial)
	}
	for _, base := range r.prepared {
		summary.merge(base.summary)
	}
	return summary.normalized()
}

func (r *workspaceInputReads) incompleteLocked() bool {
	if r.summary.incomplete || r.initial != nil && r.initial.incomplete {
		return true
	}
	for _, base := range r.prepared {
		if base.summary.incomplete {
			return true
		}
	}
	return false
}

func (r *workspaceInputReads) runtimeSnapshot() (workspaceInputSummary, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runtime == nil {
		return workspaceInputSummary{}, false
	}
	return *r.runtime, true
}

type workspaceInputCall struct {
	telemetryEnabled atomic.Bool
	telemetry        atomic.Pointer[workspaceInputTelemetry]
	request          *dagql.CallRequest
	reads            *workspaceInputReads
	view             *workspaceInputSummary
	reused           *workspaceInputSummary
	prepared         bool
	storedField      bool
	resolved         atomic.Bool
	initial          workspaceInputSummary
	bindings         []workspaceInputBinding
	boundary         string
}

func (c *workspaceInputCall) summary() workspaceInputSummary {
	if c.reused != nil {
		return c.reused.normalized()
	}
	if c.view != nil {
		summary := *c.view
		summary.Sources = slices.Clone(summary.Sources)
		summary.FindUps = slices.Clone(summary.FindUps)
		summary.Issues = slices.Clone(summary.Issues)
		live := c.reads.snapshot()
		summary.addContextInputs(live.capturedContextInputs())
		summary.Issues = append(summary.Issues, live.Issues...)
		summary.FindUps = append(summary.FindUps, live.findUps...)
		summary.findUps = append(slices.Clone(summary.findUps), live.findUps...)
		for _, source := range live.Sources {
			if len(source.Issues) != 0 {
				summary.Sources = append(summary.Sources, workspaceInputSource{Workspace: source.Workspace, Issues: source.Issues, KnownBoundary: source.KnownBoundary})
			}
		}
		summary.incomplete = summary.incomplete || live.incomplete
		return summary.normalized()
	}
	return c.reads.snapshot()
}

func workspaceInputView(req *dagql.CallRequest) bool {
	if req.Module != nil {
		return false
	}
	switch req.ReceiverTypeName {
	case "Query":
		return req.Field == "currentWorkspace"
	case "Workspace":
		switch req.Field {
		case "snapshot", "git", "withWorkdir", "withConfigPaths", "withConfigEnvironment", "__withUserConfigOverlay",
			"withNewFile", "withFile", "withNewDirectory", "withDirectory", "withoutFile", "withoutFiles", "withoutDirectory",
			"withMountedDirectory", "withMountedFile", "withoutMount":
			return true
		}
	case "Directory", "GitRef", "GitRepository":
		return req.Field == "asWorkspace"
	}
	return false
}

type workspaceInputFact struct {
	RuntimeInputs *workspaceInputSummary `json:"runtimeInputs,omitempty"`
	Issues        []string               `json:"issues,omitempty"`
	Operation     string                 `json:"operation"`
	Complete      bool                   `json:"complete"`
	Prepared      workspaceInputSummary  `json:"prepared"`
	// ContextInputs belong to the producer that consumed them. Reusing its
	// value keeps these origins even when the new caller has another module.
	ContextInputs        workspaceInputSummary    `json:"contextInputs,omitempty"`
	Reads                []workspaceBoundRead     `json:"reads,omitempty"`
	FindUps              []workspaceFindUpRule    `json:"findUps,omitempty"`
	Retained             []workspaceRetainedInput `json:"retained,omitempty"`
	CapabilitiesComplete bool                     `json:"capabilitiesComplete"`
	Boundary             string                   `json:"boundary,omitempty"`
}

func workspaceInputOperation(ctx context.Context, frame *dagql.ResultCall) string {
	var receiver string
	if frame.Receiver != nil {
		if cache, err := dagql.EngineCache(ctx); err == nil {
			if call, err := cache.ResultCallForRef(frame.Receiver); err == nil && call != nil && call.Type != nil {
				receiver = call.Type.NamedType
			}
		}
	}
	return workspaceInputOperationForReceiver(frame, receiver)
}

func workspaceInputOperationForReceiver(frame *dagql.ResultCall, receiver string) string {
	var module, ref, pin string
	if frame.Module != nil {
		module, ref, pin = frame.Module.Name, frame.Module.Ref, frame.Module.Pin
	}
	key, _ := json.Marshal([]string{module, ref, pin, receiver, frame.Field})
	return string(key)
}

type workspaceInputCallKey struct{}
type workspaceInputReadsKey struct{}

func workspaceInputTransport(typ *dagql.ResultCallType) bool {
	for typ != nil && typ.Elem != nil {
		typ = typ.Elem
	}
	return typ != nil && (typ.NamedType == "FunctionCall" || typ.NamedType == "FunctionCallArgValue")
}

func currentWorkspaceInputReads(ctx context.Context) *workspaceInputReads {
	if reads, ok := ctx.Value(workspaceInputReadsKey{}).(*workspaceInputReads); ok {
		return reads
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil
	}
	call, err := query.CurrentFunctionCall(ctx)
	if err != nil || call == nil {
		return nil
	}
	return call.workspaceInputs
}

func withWorkspaceInputFunction(ctx context.Context, call *FunctionCall) context.Context {
	observation, ok := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall)
	if !ok {
		return ctx
	}
	call.workspaceInputs = observation.reads
	// The module's client records body queries in the same holder after the
	// runtime snapshot has separated setup inputs from those body queries.
	return context.WithValue(ctx, workspaceInputReadsKey{}, observation.reads)
}

// The resolver dispatches this function for the exact observed call. Its field
// name can differ from the SDK name, for example for a collection getter.
func (fn *ModuleFunction) resolverWorkspaceInputObservation(ctx context.Context) *workspaceInputCall {
	observation, ok := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall)
	if !ok || observation.request.ResultCall != dagql.CurrentCall(ctx) ||
		observation.request.Module == nil || observation.request.Module.ResultRef == nil {
		return nil
	}
	id, err := fn.mod.ID()
	if err != nil || id == nil || id.EngineResultID() == 0 {
		return nil
	}
	ref := observation.request.Module.ResultRef
	if ref.ResultID != id.EngineResultID() {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil
		}
		frame, err := cache.ResultCallForRef(ref)
		// FieldModule scopes this exact operational producer for the caller.
		if err != nil || frame == nil || frame.Module != nil || frame.Type == nil || frame.Type.NamedType != "Module" || frame.Field != "_implementationScoped" || frame.Receiver == nil || frame.Receiver.ResultID != id.EngineResultID() {
			return nil
		}
	}
	return observation
}

func recordWorkspaceRuntimeInputs(ctx context.Context, fn *ModuleFunction, call *FunctionCall, runtime ModuleRuntime, owned *workspaceInputCall) {
	reads := currentWorkspaceInputReads(ctx)
	if reads == nil {
		return
	}
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		reads.add(workspaceInputSummary{Issues: []string{"cannot inspect function runtime inputs: " + err.Error()}})
		return
	}
	if container, ok := runtime.AsContainer(); ok && !collector.builtinModuleInputs(fn.mod.Self()) {
		collector.retainedField(container, "module runtime")
	}
	summary := reads.snapshot()
	summary.merge(collector.result)
	summary = summary.normalized()
	// Setup calls are current prepared dependencies. They are not query
	// events from the function body and must not be rebound as body reads.
	summary.reads = nil
	summary.findUps = nil
	summary.contextInputs = nil
	call.workspaceRuntimeInputs = &summary
	reads.mu.Lock()
	reads.summary.merge(collector.result)
	if owned != nil && owned.reads == reads {
		setup := reads.snapshotLocked(nil)
		setup.reads = nil
		setup.findUps = nil
		setup.contextInputs = nil
		reads.runtime = &setup
		reads.summary.FindUps = slices.Clone(summary.FindUps)
		reads.summary.reads = nil
		reads.summary.findUps = nil
		reads.summary.contextInputs = nil
	}
	reads.mu.Unlock()
	reads.rememberInputs(collector.references)
}

func publishWorkspaceInputReads(ctx context.Context) {
	observation, ok := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall)
	if !ok {
		return
	}
	observation.resolved.Store(true)
}

func appendWorkspaceInputFact(ctx context.Context, req *dagql.CallRequest, summary workspaceInputSummary, complete bool) {
	payload, _ := json.Marshal(workspaceInputFact{Prepared: summary, Operation: workspaceInputOperation(ctx, req.ResultCall), Complete: complete && !summary.incomplete})
	req.ResultFacts = append(req.ResultFacts, dagql.ResultFact{Namespace: workspaceInputsNamespace, Payload: payload})
}

func workspaceInputReadCompletion(ctx context.Context, parent *workspaceInputReads, observation *workspaceInputCall) func(dagql.AnyResult, error) {
	if parent == nil {
		return nil
	}
	return func(result dagql.AnyResult, callErr error) {
		summary := observation.summary()
		req := observation.request
		operation := workspaceInputOperation(ctx, req.ResultCall)
		covered := observation.prepared || observation.resolved.Load() || observation.reused != nil && !observation.reused.incomplete
		if callErr == nil && !observation.resolved.Load() && observation.view == nil && observation.reused == nil {
			collector, err := newWorkspaceInputCollector(ctx)
			if err != nil {
				summary.Issues = append(summary.Issues, "cannot inspect called result inputs: "+err.Error())
			} else {
				ref := &dagql.ResultCallRef{Call: req.ResultCall}
				if result != nil && result.Unwrap() != nil {
					if id, err := result.ID(); err == nil && id != nil && id.EngineResultID() != 0 {
						ref = &dagql.ResultCallRef{ResultID: id.EngineResultID()}
					}
				}
				collector.legacySources(ref)
				summary.merge(collector.result)
				facts, complete, err := collector.cache.ResultFacts(context.WithoutCancel(ctx), collector.session, ref, workspaceInputsNamespace)
				if err != nil {
					summary.Issues = append(summary.Issues, "cannot inspect cached source queries: "+err.Error())
				} else {
					for _, fact := range facts {
						var inputs workspaceInputFact
						if err := json.Unmarshal(fact.Payload, &inputs); err != nil {
							summary.Issues = append(summary.Issues, "cannot decode cached source queries: "+err.Error())
							continue
						}
						matches := inputs.Operation == operation
						if matches && !inputs.Complete {
							summary.incomplete = true
						}
						if matches {
							// Current prepared origins are already in summary.
							// Recreate body producer bindings for the caller.
							inputs.Prepared = workspaceInputSummary{}
							body := collector.materializeReads(inputs, req.ResultCall)
							body.findUps = inputs.FindUps
							body.mergeContextInputEvents(&inputs.ContextInputs)
							summary.merge(body)
						}
						covered = covered || matches && inputs.Complete
					}
					if !complete {
						summary.incomplete = true
						summary.Issues = append(summary.Issues, "cached call has incomplete source origins for "+operation)
					}
				}
			}
		}
		if !covered && req.Module != nil {
			summary.Issues = append(summary.Issues, "call has no complete source query capture for "+operation)
		}
		parent.checkBodyInputs(ctx, observation.request.ResultCall, result)
		parent.add(summary)
	}
}

// RecordWorkspaceRead records a native helper's actual source read. Call it
// before the read, including reads that can fail or find no source. The exact
// Workspace producer binds the event on cache reuse.
func RecordWorkspaceRead(ctx context.Context, workspace dagql.ObjectResult[*Workspace], selector provenance.Selector) {
	recordWorkspaceReads(ctx, workspace, func(c *workspaceInputCollector) []provenance.Selector {
		return []provenance.Selector{c.workspaceReadSelector(workspace.Self(), selector)}
	})
}

func (c *workspaceInputCollector) workspaceReadSelector(ws *Workspace, selector provenance.Selector) provenance.Selector {
	if selector.Path != "/" && c.unaliasedSourcePath(ws, selector.Path) != nil {
		// The spelling alone cannot account for a symlink target or mount.
		selector.Path = "/"
		selector.Include = nil
		selector.Exclude = nil
		selector.Conservative = true
	}
	return selector
}

func recordWorkspaceReads(ctx context.Context, workspace dagql.ObjectResult[*Workspace], selectors func(*workspaceInputCollector) []provenance.Selector) {
	reads := currentWorkspaceInputReads(ctx)
	if reads == nil {
		return
	}
	collector, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		reads.add(workspaceInputSummary{Issues: []string{"cannot record Workspace read: " + err.Error()}, incomplete: true})
		return
	}
	id, err := workspace.ID()
	if err != nil || id == nil || id.EngineResultID() == 0 || workspace.Self() == nil {
		collector.issue("Workspace read has no attached source producer")
	} else {
		collector.sourceProducer = &dagql.ResultCallRef{ResultID: id.EngineResultID()}
		for _, selector := range selectors(collector) {
			collector.source(workspace.Self(), selector)
		}
	}
	reads.add(collector.result)
}
