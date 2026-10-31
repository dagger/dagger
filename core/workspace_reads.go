package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/vektah/gqlparser/v2/ast"
)

const workspaceReadsNamespace = "workspace-reads/v1"

type workspaceReadScope struct {
	Recipe   string `json:"recipe"`
	Identity string `json:"identity"`
	Context  string `json:"context"`
}

type workspaceReadSummary struct {
	Selectors []provenance.Selector `json:"selectors,omitempty"`
	Scope     *workspaceReadScope   `json:"scope,omitempty"`
	Unknown   bool                  `json:"unknown,omitempty"`
	Access    bool                  `json:"access,omitempty"`
}

func (s workspaceReadSummary) hasSourceReads() bool {
	return slices.ContainsFunc(s.Selectors, func(selector provenance.Selector) bool {
		return selector.Kind == provenance.WorkspaceRead
	})
}

type workspaceReadRecorder struct {
	mu                sync.Mutex
	summary           workspaceReadSummary
	moduleRun         bool
	explicitWorkspace bool
	sourceRead        bool
}

type workspaceReadContextKey struct{}
type workspaceReadSuppressedKey struct{}

func suppressWorkspaceReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, workspaceReadSuppressedKey{}, true)
}

func currentWorkspaceReads(ctx context.Context) *workspaceReadRecorder {
	if suppressed, _ := ctx.Value(workspaceReadSuppressedKey{}).(bool); suppressed {
		return nil
	}
	if record, ok := ctx.Value(workspaceReadContextKey{}).(*workspaceReadRecorder); ok {
		return record
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil
	}
	fn, err := query.CurrentFunctionCall(ctx)
	if err != nil || fn == nil {
		return nil
	}
	return fn.workspaceReads
}

func (r *workspaceReadRecorder) merge(summary workspaceReadSummary, access bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.Selectors = append(r.summary.Selectors, summary.Selectors...)
	r.summary.Unknown = r.summary.Unknown || summary.Unknown
	if access {
		r.summary.Access = r.summary.Access || summary.Access
	}
	if summary.Scope != nil {
		if r.summary.Scope == nil {
			copy := *summary.Scope
			r.summary.Scope = &copy
		} else if r.summary.Scope.Identity != summary.Scope.Identity || r.summary.Scope.Context != summary.Scope.Context {
			r.summary.Unknown = true
		}
	}
}

func (r *workspaceReadRecorder) snapshot() workspaceReadSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := r.summary
	summary.Selectors = provenance.Normalize(summary.Selectors)
	if summary.Scope != nil {
		copy := *summary.Scope
		summary.Scope = &copy
	}
	if r.moduleRun && summary.Access && !r.explicitWorkspace {
		summary.Unknown = true
	}
	return summary
}

// RecordWorkspaceRead is called before resolving a source request, including
// requests that fail and are handled by the module's own control flow.
func RecordWorkspaceRead(ctx context.Context, resolved string, filter CopyFilter) {
	r := currentWorkspaceReads(ctx)
	if r == nil {
		return
	}
	selector := provenance.Selector{Path: path.Clean("/" + resolved), Include: slices.Clone(filter.Include), Exclude: slices.Clone(filter.Exclude), Kind: provenance.WorkspaceRead}
	r.mu.Lock()
	r.sourceRead = true
	r.mu.Unlock()
	r.merge(workspaceReadSummary{Selectors: []provenance.Selector{selector}, Access: true}, true)
}

// ObserveWorkspaceReads records normal calls independently of telemetry. The
// active recorder also rides FunctionCall across SDK client boundaries.
func ObserveWorkspaceReads(ctx context.Context, req *dagql.CallRequest) (context.Context, func(dagql.AnyResult, bool, error)) {
	if suppressed, _ := ctx.Value(workspaceReadSuppressedKey{}).(bool); suppressed {
		return ctx, nil
	}
	parent := currentWorkspaceReads(ctx)
	// IDs are opaque references. SDKs request them to pass object arguments.
	if req == nil || req.ResultCall == nil || (req.Module == nil && (req.Field == "id" || (req.ReceiverTypeName == "Query" && req.Field == "node"))) {
		return ctx, nil
	}
	protocol := req.ReceiverTypeName == "FunctionCall" ||
		(req.ReceiverTypeName == "Query" && (req.Field == "currentFunctionCall" || req.Field == "currentModule"))
	if parent != nil && req.Module == nil && req.ReceiverTypeName != "Workspace" && !protocol && !dagql.IsSkipped(ctx) && !dagql.IsInternal(ctx) {
		volatile := req.TTL != 0 || (req.DoNotCache && !req.DeduplicateTelemetry && req.Field != "sync")
		for _, input := range req.ImplicitInputs {
			if input.Name == dagql.PerCallInput.Name || input.Name == dagql.PerSessionInput.Name || input.Name == dagql.PerClientInput.Name {
				volatile = true
			}
		}
		if volatile {
			parent.merge(workspaceReadSummary{Unknown: true}, false)
		}
	}
	ambient := parent != nil && ((req.ReceiverTypeName == "Query" && req.Field == "currentWorkspace") || req.ReceiverTypeName == "Host")
	if req.Module == nil && req.ReceiverTypeName != "Workspace" && !ambient {
		return ctx, nil
	}
	if dagql.IsSkipped(ctx) && parent == nil {
		return ctx, nil
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		if parent != nil {
			parent.merge(workspaceReadSummary{Unknown: true}, false)
		}
		return ctx, nil
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ctx, nil
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return ctx, nil
	}
	inspectCtx := suppressWorkspaceReads(ctx)
	record := &workspaceReadRecorder{}
	key, keyErr := workspaceCallKey(inspectCtx, req)
	refs, err := workspaceRefs(inspectCtx, req.ResultCall)
	if err != nil {
		record.summary.Unknown = true
	}
	record.explicitWorkspace = len(refs) > 0
	if req.Module != nil && (keyErr != nil || key == "") {
		record.summary.Unknown = true
	}
	// A Workspace receiver is a source boundary, not an opaque module input.
	if req.ReceiverTypeName == "Workspace" && req.Receiver != nil {
		refs = append(refs, req.Receiver)
	}
	for _, ref := range refs {
		value, err := workspaceValueForRef(inspectCtx, srv, cache, ref)
		if err != nil {
			record.merge(workspaceReadSummary{Unknown: true}, false)
			continue
		}
		scope, err := workspaceScopeForValue(inspectCtx, value)
		record.merge(workspaceReadSummary{Scope: scope, Unknown: err != nil || scope == nil}, false)
	}
	inputs := workspaceInputReads(inspectCtx, cache, md.SessionID, req.ResultCall, map[*dagql.ResultCall]bool{})
	record.merge(inputs, false)
	if req.ReceiverTypeName == "Workspace" && req.Field != "directory" && req.Field != "file" {
		record.merge(workspaceReadSummary{Selectors: []provenance.Selector{conservativeProvenance(provenance.WorkspaceRead)}, Unknown: true, Access: true}, true)
	}
	if ambient {
		record.merge(workspaceReadSummary{Unknown: true, Access: true}, true)
	}
	ctx = context.WithValue(ctx, workspaceReadContextKey{}, record)
	return ctx, func(result dagql.AnyResult, executed bool, callErr error) {
		record.mu.Lock()
		sourceRead := record.sourceRead
		record.mu.Unlock()
		hit := !executed
		if hit {
			payload, ok, err := cache.CallMetadata(inspectCtx, md.SessionID, workspaceReadsNamespace, req.ResultCall)
			if err == nil && ok {
				var saved workspaceReadSummary
				if json.Unmarshal(payload, &saved) == nil {
					record.merge(saved, true)
				} else {
					record.merge(workspaceReadSummary{Unknown: true}, false)
				}
			} else if req.Module != nil || req.ReceiverTypeName == "Workspace" {
				// Input-equivalent cache hits can have a different origin recipe.
				// A complete source-free observation needs no workspace baseline.
				found := false
				if req.Module != nil && keyErr == nil && key != "" {
					candidates, err := cache.CallMetadataCandidates(inspectCtx, md.SessionID, workspaceReadsNamespace, key)
					if err == nil {
						for _, candidate := range candidates {
							var saved workspaceReadSummary
							if json.Unmarshal(candidate.Payload, &saved) == nil && !saved.Unknown && !saved.hasSourceReads() && !saved.Access {
								saved.Scope = nil
								record.merge(saved, false)
								found = true
								break
							}
						}
					}
				}
				if !found {
					record.merge(workspaceReadSummary{Unknown: true}, false)
				}
			}
		}
		// A fresh source call supplied its selector before any nested filesystem
		// operation. Missing metadata on an older cached source remains unknown.
		if req.ReceiverTypeName == "Workspace" && (req.Field == "directory" || req.Field == "file") && !sourceRead && !hit {
			record.merge(workspaceReadSummary{Unknown: true}, false)
		}
		summary := record.snapshot()
		if parent != nil {
			observed := summary
			if !observed.hasSourceReads() {
				observed.Scope = nil
			}
			parent.merge(observed, true)
		}
		if callErr != nil || result == nil || context.Cause(ctx) != nil {
			return
		}
		if hit {
			return
		}
		if len(summary.Selectors) == 0 && req.Module == nil && !summary.Unknown {
			return
		}
		payload, err := json.Marshal(summary)
		if err == nil {
			_ = cache.StoreCallMetadata(inspectCtx, md.SessionID, srv, req, workspaceReadsNamespace, key, payload, result)
		}
	}
}

func workspaceValueForRef(ctx context.Context, srv *dagql.Server, cache *dagql.Cache, ref *dagql.ResultCallRef) (dagql.AnyResult, error) {
	frame, err := cache.ResultCallForRef(ref)
	if err != nil {
		return nil, err
	}
	if ref.ResultID != 0 {
		return srv.Load(ctx, call.NewEngineResultID(ref.ResultID, call.NewType(&ast.Type{NamedType: "Workspace", NonNull: true})))
	}
	id, err := frame.RecipeID(ctx)
	if err != nil {
		return nil, err
	}
	return srv.Load(ctx, id)
}

func workspaceScopeForValue(ctx context.Context, value dagql.AnyResult) (*workspaceReadScope, error) {
	ws, ok := dagql.UnwrapAs[*Workspace](value)
	if !ok || !ws.HasContentIdentity() {
		return nil, nil
	}
	identity, ok, err := ws.ContentIdentity(ctx)
	if err != nil || !ok {
		return nil, err
	}
	id, err := value.RecipeID(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := id.Encode()
	if err != nil {
		return nil, err
	}
	contextWS := ws.Clone()
	contextWS.source = &WorkspaceSourceDirectory{}
	contextWS.rootfs = dagql.ObjectResult[*Directory]{}
	// Address is observable through Workspace fields, which remain broad. Source
	// reads use the resolved cwd and boundary, not a requested Git version string.
	contextWS.Address = ""
	contextDigest, _, err := contextWS.ContentIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return &workspaceReadScope{Recipe: encoded, Identity: identity.String(), Context: contextDigest.String()}, nil
}

func workspaceInputReads(ctx context.Context, cache *dagql.Cache, session string, frame *dagql.ResultCall, seen map[*dagql.ResultCall]bool) workspaceReadSummary {
	var out workspaceReadSummary
	merge := func(in workspaceReadSummary) {
		out.Selectors = append(out.Selectors, in.Selectors...)
		out.Unknown = out.Unknown || in.Unknown
		if in.Scope != nil && in.hasSourceReads() {
			if out.Scope == nil {
				out.Scope = in.Scope
			} else if out.Scope.Identity != in.Scope.Identity || out.Scope.Context != in.Scope.Context {
				out.Unknown = true
			}
		}
	}
	var visit func(*dagql.ResultCallRef)
	var literal func(*dagql.ResultCallLiteral)
	visit = func(ref *dagql.ResultCallRef) {
		if ref == nil {
			return
		}
		input, err := cache.ResultCallForRef(ref)
		if err != nil {
			out.Unknown = true
			return
		}
		if input.Type != nil && (input.Type.NamedType == "Workspace" || input.Type.NamedType == "Query") {
			return
		}
		if seen[input] {
			return
		}
		seen[input] = true
		payload, found, err := cache.CallMetadata(ctx, session, workspaceReadsNamespace, input)
		if err != nil {
			out.Unknown = true
			return
		}
		if found {
			var saved workspaceReadSummary
			if json.Unmarshal(payload, &saved) != nil {
				out.Unknown = true
				return
			}
			merge(saved)
		} else if input.Module != nil || (input.Type != nil && (input.Type.NamedType == "File" || input.Type.NamedType == "Directory")) {
			out.Unknown = true
		}
		// Source APIs can adopt an existing result. Retain their recorded origins
		// even when the shared result describes the underlying producer instead.
		aliases, err := cache.CallMetadataForResult(ctx, session, workspaceReadsNamespace, ref)
		if err != nil {
			out.Unknown = true
			return
		}
		for _, alias := range aliases {
			var saved workspaceReadSummary
			if json.Unmarshal(alias.Payload, &saved) != nil {
				out.Unknown = true
				continue
			}
			merge(saved)
		}
		if !found {
			merge(workspaceInputReads(ctx, cache, session, input, seen))
		}
	}
	literal = func(value *dagql.ResultCallLiteral) {
		if value == nil {
			return
		}
		visit(value.ResultRef)
		for _, item := range value.ListItems {
			literal(item)
		}
		for _, field := range value.ObjectFields {
			literal(field.Value)
		}
	}
	visit(frame.Receiver)
	for _, arg := range slices.Concat(frame.Args, frame.ImplicitInputs) {
		literal(arg.Value)
	}
	return out
}

func validateWorkspaceReads(ctx context.Context, srv *dagql.Server, current dagql.ObjectResult[*Workspace], summary workspaceReadSummary) bool {
	if summary.Unknown || summary.Scope == nil || current.Self() == nil {
		return false
	}
	scope, err := workspaceScopeForValue(ctx, current)
	if err != nil || scope == nil || scope.Context != summary.Scope.Context {
		return false
	}
	if !workspaceSelectorsUnaliased(ctx, srv, current.Self(), summary.Selectors) {
		return false
	}
	if scope.Identity == summary.Scope.Identity {
		return true
	}
	var id call.ID
	if id.Decode(summary.Scope.Recipe) != nil {
		return false
	}
	oldValue, err := srv.Load(ctx, &id)
	if err != nil {
		return false
	}
	old, ok := dagql.UnwrapAs[*Workspace](oldValue)
	if !ok {
		return false
	}
	oldIdentity, ok, err := old.ContentIdentity(ctx)
	if err != nil || !ok || oldIdentity.String() != summary.Scope.Identity {
		return false
	}
	if !workspaceSelectorsUnaliased(ctx, srv, old, summary.Selectors) {
		return false
	}
	before, ok := old.SourceDirectory()
	if !ok {
		return false
	}
	after, ok := current.Self().SourceDirectory()
	if !ok {
		return false
	}
	changes, err := NewChangeset(ctx, before, after)
	if err != nil {
		return false
	}
	paths, err := changes.ComputePaths(ctx)
	if err != nil {
		return false
	}
	for _, file := range slices.Concat(paths.Modified, paths.Added, paths.AllRemoved) {
		var match bool
		if strings.HasSuffix(file, "/") {
			match, err = provenance.AffectedByPath(summary.Selectors, file, nil)
		} else {
			match, err = provenance.AffectedByFile(summary.Selectors, file, nil)
		}
		if err != nil || match {
			return false
		}
	}
	return true
}

// A selector cannot describe reads through a symlink without the symlink's
// entire resolution chain. Validate prefixes before using a narrow read set.
func workspaceSelectorsUnaliased(ctx context.Context, srv *dagql.Server, ws *Workspace, selectors []provenance.Selector) bool {
	root, ok := ws.SourceDirectory()
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, selector := range selectors {
		prefix := ""
		for _, part := range strings.Split(strings.Trim(selector.Path, "/"), "/") {
			if part == "" {
				continue
			}
			prefix = path.Join(prefix, part)
			if seen[prefix] {
				continue
			}
			seen[prefix] = true
			stat, err := root.Self().Stat(ctx, root, srv, prefix, true)
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			if err != nil || stat.FileType == FileTypeSymlink {
				return false
			}
		}
	}
	return true
}
