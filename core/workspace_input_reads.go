package core

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/dagql"
)

type workspaceInputReads struct {
	mu        sync.Mutex
	summary   workspaceInputSummary
	workspace *Workspace
}

func workspaceInputSourceWorkspace(ctx context.Context) *Workspace {
	if ws, ok := WorkspaceFromContext(ctx); ok && ws.Self() != nil {
		return ws.Self()
	}
	if reads := currentWorkspaceInputReads(ctx); reads != nil {
		return reads.workspace
	}
	return nil
}

func (r *workspaceInputReads) add(summary workspaceInputSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.merge(summary)
}

func (r *workspaceInputReads) snapshot() workspaceInputSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary.normalized()
}

type workspaceInputCall struct {
	request  *dagql.CallRequest
	reads    *workspaceInputReads
	view     *workspaceInputSummary
	prepared bool
	resolved atomic.Bool
}

func (c *workspaceInputCall) summary() workspaceInputSummary {
	if c.view != nil {
		return *c.view
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
		case "snapshot", "git", "withWorkdir", "withConfigPaths", "withConfigEnvironment",
			"withNewFile", "withFile", "withNewDirectory", "withDirectory", "withoutFile", "withoutFiles", "withoutDirectory",
			"withMountedDirectory", "withMountedFile", "withoutMount":
			return true
		}
	case "Directory", "GitRef", "GitRepository":
		return req.Field == "asWorkspace"
	}
	return false
}

func withWorkspaceInputSetup(ctx context.Context) context.Context {
	observation, ok := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall)
	if !ok {
		return ctx
	}
	ctx = context.WithValue(ctx, workspaceInputSourceContextKey{}, observation.reads.snapshot())
	return context.WithValue(ctx, workspaceInputBoundaryKey{}, false)
}

type workspaceInputFact struct {
	workspaceInputSummary
	Operation string `json:"operation"`
	Complete  bool   `json:"complete"`
}

func workspaceInputOperation(ctx context.Context, frame *dagql.ResultCall) string {
	var module, ref, pin, receiver string
	if frame.Module != nil {
		module, ref, pin = frame.Module.Name, frame.Module.Ref, frame.Module.Pin
	}
	if frame.Receiver != nil {
		if cache, err := dagql.EngineCache(ctx); err == nil {
			if call, err := cache.ResultCallForRef(frame.Receiver); err == nil && call != nil && call.Type != nil {
				receiver = call.Type.NamedType
			}
		}
	}
	key, _ := json.Marshal([]string{module, ref, pin, receiver, frame.Field})
	return string(key)
}

type workspaceInputCallKey struct{}
type workspaceInputReadsKey struct{}
type workspaceInputBoundaryKey struct{}

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
	ctx = context.WithValue(ctx, workspaceInputReadsKey{}, observation.reads)
	ctx = context.WithValue(ctx, workspaceInputBoundaryKey{}, false)
	return context.WithValue(ctx, workspaceInputSourceContextKey{}, struct{}{})
}

func publishWorkspaceInputReads(ctx context.Context) {
	observation, ok := ctx.Value(workspaceInputCallKey{}).(*workspaceInputCall)
	if !ok {
		return
	}
	appendWorkspaceInputFact(ctx, observation.request, observation.summary(), true)
	observation.resolved.Store(true)
}

func appendWorkspaceInputFact(ctx context.Context, req *dagql.CallRequest, summary workspaceInputSummary, complete bool) {
	payload, _ := json.Marshal(workspaceInputFact{workspaceInputSummary: summary, Operation: workspaceInputOperation(ctx, req.ResultCall), Complete: complete})
	req.ResultFacts = append(req.ResultFacts, dagql.ResultFact{Namespace: workspaceInputsNamespace, Payload: payload})
}

func workspaceInputReadCompletion(ctx context.Context, parent *workspaceInputReads, observation *workspaceInputCall) func(dagql.AnyResult, error) {
	if parent == nil {
		return nil
	}
	return func(_ dagql.AnyResult, callErr error) {
		summary := observation.summary()
		req := observation.request
		operation := workspaceInputOperation(ctx, req.ResultCall)
		covered := observation.prepared || observation.resolved.Load()
		if callErr == nil && !observation.resolved.Load() && observation.view == nil {
			collector, err := newWorkspaceInputCollector(ctx)
			if err != nil {
				summary.Issues = append(summary.Issues, "cannot inspect called result inputs: "+err.Error())
			} else {
				ref := &dagql.ResultCallRef{Call: req.ResultCall}
				collector.legacySources(ref)
				summary.merge(collector.result)
				facts, complete, err := collector.cache.ResultFacts(ctx, collector.session, ref, workspaceInputsNamespace)
				if err != nil {
					summary.Issues = append(summary.Issues, "cannot inspect cached source queries: "+err.Error())
				} else {
					var typeName string
					if req.Type != nil {
						typeName = req.Type.NamedType
					}
					_, object := collector.server.ObjectType(typeName)
					_, iface := collector.server.InterfaceType(typeName)
					for _, fact := range facts {
						var inputs workspaceInputFact
						if err := json.Unmarshal(fact.Payload, &inputs); err != nil {
							summary.Issues = append(summary.Issues, "cannot decode cached source queries: "+err.Error())
							continue
						}
						matches := inputs.Operation == operation
						if matches || object || iface {
							summary.merge(inputs.workspaceInputSummary)
						}
						covered = covered || matches && inputs.Complete
					}
					if !complete {
						summary.Issues = append(summary.Issues, "cached call has incomplete source origins")
					}
				}
			}
		}
		if !covered {
			summary.Issues = append(summary.Issues, "call "+req.Field+" has no complete source query capture")
		}
		parent.add(summary)
	}
}
