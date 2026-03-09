package server

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2GitRemote struct {
	ID                string                `json:"id"`
	Ref               string                `json:"ref"`
	Host              string                `json:"host,omitempty"`
	LatestResolvedRef string                `json:"latestResolvedRef,omitempty"`
	TraceCount        int                   `json:"traceCount"`
	SessionCount      int                   `json:"sessionCount"`
	PipelineCount     int                   `json:"pipelineCount"`
	SpanCount         int                   `json:"spanCount"`
	FirstSeenUnixNano int64                 `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64                 `json:"lastSeenUnixNano"`
	SourceKinds       []string              `json:"sourceKinds,omitempty"`
	Pipelines         []v2GitRemotePipeline `json:"pipelines,omitempty"`
}

type v2GitRemotePipeline struct {
	PipelineID    string `json:"pipelineID"`
	TraceID       string `json:"traceID"`
	ClientID      string `json:"clientID,omitempty"`
	SessionID     string `json:"sessionID,omitempty"`
	Command       string `json:"command,omitempty"`
	StartUnixNano int64  `json:"startUnixNano"`
}

type gitRemoteIdentity struct {
	ref      string
	resolved string
}

type v2GitRemoteAggregate struct {
	item          v2GitRemote
	traceIDs      map[string]struct{}
	sessionIDs    map[string]struct{}
	sourceKindSet map[string]struct{}
	pipelineSet   map[string]struct{}
}

func (s *Server) handleV2GitRemotes(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	byRef := map[string]*v2GitRemoteAggregate{}
	for _, traceID := range traceIDs {
		traceMeta, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		collectV2GitRemotes(byRef, traceMeta.Status, traceID, q, spans, proj, scopeIdx)
	}

	items := finalizeV2GitRemotes(byRef)
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastSeenUnixNano != items[j].LastSeenUnixNano {
			return items[i].LastSeenUnixNano > items[j].LastSeenUnixNano
		}
		return items[i].Ref < items[j].Ref
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func collectV2GitRemotes(
	byRef map[string]*v2GitRemoteAggregate,
	traceStatus string,
	traceID string,
	q v2Query,
	spans []store.SpanRecord,
	proj *transform.TraceProjection,
	scopeIdx *derive.ScopeIndex,
) {
	if proj == nil || scopeIdx == nil {
		return
	}

	pipelinesByClient := map[string][]v2CLIRun{}
	for _, pipeline := range collectV2CLIRuns(traceStatus, traceID, q, proj, scopeIdx) {
		if pipeline.ClientID == "" {
			continue
		}
		pipelinesByClient[pipeline.ClientID] = append(pipelinesByClient[pipeline.ClientID], pipeline)
	}

	stateRefs := buildGitRemoteStateRefs(spans)
	for _, sp := range spans {
		if !intersectsTime(sp.StartUnixNano, sp.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
			continue
		}
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			continue
		}
		internal, _ := getV2Bool(env.Attributes, telemetry.UIInternalAttr)
		if !q.IncludeInternal && internal {
			continue
		}

		sessionID := scopeIdx.SessionIDForSpan(sp.SpanID)
		clientID := scopeIdx.ClientIDForSpan(sp.SpanID)
		if !matchesV2Scope(q, traceID, sessionID, clientID) {
			continue
		}

		ref, resolved, sourceKind := detectGitRemoteRef(sp.Name, env.Attributes, stateRefs)
		if ref == "" {
			continue
		}
		agg := byRef[ref]
		if agg == nil {
			agg = &v2GitRemoteAggregate{
				item: v2GitRemote{
					ID:                "git-remote:" + ref,
					Ref:               ref,
					Host:              gitRemoteHost(ref),
					LatestResolvedRef: resolved,
					FirstSeenUnixNano: sp.StartUnixNano,
					LastSeenUnixNano:  spanLastSeen(sp),
				},
				traceIDs:      map[string]struct{}{},
				sessionIDs:    map[string]struct{}{},
				sourceKindSet: map[string]struct{}{},
				pipelineSet:   map[string]struct{}{},
			}
			byRef[ref] = agg
		}

		agg.item.SpanCount++
		agg.traceIDs[traceID] = struct{}{}
		if sessionID != "" {
			agg.sessionIDs[sessionID] = struct{}{}
		}
		if sourceKind != "" {
			agg.sourceKindSet[sourceKind] = struct{}{}
		}
		if agg.item.FirstSeenUnixNano == 0 || (sp.StartUnixNano > 0 && sp.StartUnixNano < agg.item.FirstSeenUnixNano) {
			agg.item.FirstSeenUnixNano = sp.StartUnixNano
		}
		lastSeen := spanLastSeen(sp)
		if lastSeen > agg.item.LastSeenUnixNano {
			agg.item.LastSeenUnixNano = lastSeen
			if resolved != "" {
				agg.item.LatestResolvedRef = resolved
			}
		} else if agg.item.LatestResolvedRef == "" && resolved != "" {
			agg.item.LatestResolvedRef = resolved
		}

		if pipeline := gitRemotePipelineForSpan(pipelinesByClient[clientID], sp.StartUnixNano, spanLastSeen(sp)); pipeline != nil {
			if _, ok := agg.pipelineSet[pipeline.ID]; !ok {
				agg.pipelineSet[pipeline.ID] = struct{}{}
				agg.item.Pipelines = append(agg.item.Pipelines, v2GitRemotePipeline{
					PipelineID:    pipeline.ID,
					TraceID:       pipeline.TraceID,
					ClientID:      pipeline.ClientID,
					SessionID:     pipeline.SessionID,
					Command:       pipeline.Command,
					StartUnixNano: pipeline.StartUnixNano,
				})
			}
		}
	}
}

func finalizeV2GitRemotes(byRef map[string]*v2GitRemoteAggregate) []v2GitRemote {
	items := make([]v2GitRemote, 0, len(byRef))
	for _, agg := range byRef {
		agg.item.TraceCount = len(agg.traceIDs)
		agg.item.SessionCount = len(agg.sessionIDs)
		agg.item.PipelineCount = len(agg.pipelineSet)
		agg.item.SourceKinds = setToSortedSlice(agg.sourceKindSet)
		sort.Slice(agg.item.Pipelines, func(i, j int) bool {
			if agg.item.Pipelines[i].StartUnixNano != agg.item.Pipelines[j].StartUnixNano {
				return agg.item.Pipelines[i].StartUnixNano > agg.item.Pipelines[j].StartUnixNano
			}
			return agg.item.Pipelines[i].PipelineID < agg.item.Pipelines[j].PipelineID
		})
		items = append(items, agg.item)
	}
	return items
}

func buildGitRemoteStateRefs(spans []store.SpanRecord) map[string]gitRemoteIdentity {
	ordered := append([]store.SpanRecord(nil), spans...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartUnixNano != ordered[j].StartUnixNano {
			return ordered[i].StartUnixNano < ordered[j].StartUnixNano
		}
		return ordered[i].SpanID < ordered[j].SpanID
	})

	out := map[string]gitRemoteIdentity{}
	for _, sp := range ordered {
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			continue
		}
		payload, _ := getV2String(env.Attributes, telemetry.DagCallAttr)
		if payload == "" {
			continue
		}
		call, err := decodeWorkspaceOpCallPayload(payload)
		if err != nil || call == nil {
			continue
		}
		outputDagqlID, _ := getV2String(env.Attributes, telemetry.DagOutputAttr)
		ident := gitRemoteIdentity{}
		if ref, resolved := gitRemoteRefFromCall(sp.Name, call); ref != "" {
			ident = gitRemoteIdentity{ref: ref, resolved: resolved}
		} else if receiver := strings.TrimSpace(call.GetReceiverDigest()); receiver != "" {
			ident = out[receiver]
		}
		if outputDagqlID != "" && ident.ref != "" {
			out[outputDagqlID] = ident
		}
	}
	return out
}

func detectGitRemoteRef(spanName string, attrs map[string]any, stateRefs map[string]gitRemoteIdentity) (normalized string, resolved string, sourceKind string) {
	if moduleRef, ok := getV2String(attrs, telemetry.ModuleRefAttr); ok && moduleRef != "" {
		normalized = normalizeGitRemoteRef(moduleRef)
		if normalized != "" {
			return normalized, strings.TrimSpace(moduleRef), "module-ref"
		}
	}

	lower := strings.ToLower(strings.TrimSpace(spanName))
	if strings.HasPrefix(lower, "load module: ") {
		raw := strings.TrimSpace(spanName[len("load module: "):])
		normalized = normalizeGitRemoteRef(raw)
		if normalized != "" {
			return normalized, strings.TrimSpace(raw), "load-module"
		}
	}

	payload, _ := getV2String(attrs, telemetry.DagCallAttr)
	if payload != "" {
		call, err := decodeWorkspaceOpCallPayload(payload)
		if err == nil && call != nil {
			if ref, raw := gitRemoteRefFromCall(spanName, call); ref != "" {
				return ref, raw, "query-git"
			}
			if ident := stateRefs[strings.TrimSpace(call.GetReceiverDigest())]; ident.ref != "" {
				return ident.ref, ident.resolved, "git-call"
			}
		}
	}

	outputDagqlID, _ := getV2String(attrs, telemetry.DagOutputAttr)
	if ident := stateRefs[strings.TrimSpace(outputDagqlID)]; ident.ref != "" {
		return ident.ref, ident.resolved, "git-object"
	}

	return "", "", ""
}

func gitRemoteRefFromCall(spanName string, call *callpbv1.Call) (string, string) {
	if call == nil {
		return "", ""
	}
	if call.GetReceiverDigest() != "" {
		return "", ""
	}
	if !strings.EqualFold(strings.TrimSpace(spanName), "Query.git") && !strings.EqualFold(strings.TrimSpace(call.GetField()), "git") {
		return "", ""
	}
	for _, argName := range []string{"url", "remote"} {
		raw := workspaceOpCallArgString(call, argName)
		if ref := normalizeGitRemoteRef(raw); ref != "" {
			return ref, strings.TrimSpace(raw)
		}
	}
	return "", ""
}

func normalizeGitRemoteRef(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !looksLikeGitRemote(raw) {
		return ""
	}
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err == nil && u.Host != "" {
			path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
			if path != "" {
				return u.Host + "/" + path
			}
			return u.Host
		}
	case strings.Contains(raw, "@") && strings.Contains(raw, ":") && strings.Index(raw, "@") < strings.Index(raw, ":"):
		hostPart := raw[strings.Index(raw, "@")+1:]
		host, path, ok := strings.Cut(hostPart, ":")
		if ok {
			path = strings.TrimSuffix(strings.TrimPrefix(path, "/"), ".git")
			if path != "" {
				return host + "/" + path
			}
			return host
		}
	}
	if idx := strings.LastIndex(raw, "@"); idx > 0 {
		raw = raw[:idx]
	}
	return strings.TrimSuffix(raw, ".git")
}

func looksLikeGitRemote(raw string) bool {
	switch {
	case strings.Contains(raw, "github.com/"):
		return true
	case strings.Contains(raw, "gitlab.com/"):
		return true
	case strings.Contains(raw, "://"):
		return true
	case strings.Contains(raw, "git@"):
		return true
	default:
		return false
	}
}

func gitRemoteHost(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err == nil {
			return u.Host
		}
	}
	if host, _, ok := strings.Cut(ref, "/"); ok {
		return host
	}
	return ref
}

func gitRemotePipelineForSpan(pipelines []v2CLIRun, startUnixNano, endUnixNano int64) *v2CLIRun {
	var fallback *v2CLIRun
	for i := range pipelines {
		pipeline := &pipelines[i]
		if fallback == nil {
			fallback = pipeline
		}
		if pipeline.StartUnixNano <= startUnixNano && pipeline.EndUnixNano >= endUnixNano {
			return pipeline
		}
	}
	return fallback
}

func spanLastSeen(sp store.SpanRecord) int64 {
	if sp.EndUnixNano > 0 {
		return sp.EndUnixNano
	}
	return sp.StartUnixNano
}
