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
	"github.com/google/go-containerregistry/pkg/name"
)

type v2Registry struct {
	ID                string               `json:"id"`
	Ref               string               `json:"ref"`
	Host              string               `json:"host,omitempty"`
	Repository        string               `json:"repository,omitempty"`
	LatestRef         string               `json:"latestRef,omitempty"`
	TraceCount        int                  `json:"traceCount"`
	SessionCount      int                  `json:"sessionCount"`
	PipelineCount     int                  `json:"pipelineCount"`
	ActivityCount     int                  `json:"activityCount"`
	FirstSeenUnixNano int64                `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64                `json:"lastSeenUnixNano"`
	LastOperation     string               `json:"lastOperation,omitempty"`
	SourceKinds       []string             `json:"sourceKinds,omitempty"`
	Activities        []v2RegistryActivity `json:"activities,omitempty"`
}

type v2RegistryActivity struct {
	SpanID                string `json:"spanID"`
	TraceID               string `json:"traceID"`
	SessionID             string `json:"sessionID,omitempty"`
	ClientID              string `json:"clientID,omitempty"`
	Name                  string `json:"name"`
	SourceKind            string `json:"sourceKind,omitempty"`
	Status                string `json:"status"`
	StartUnixNano         int64  `json:"startUnixNano"`
	EndUnixNano           int64  `json:"endUnixNano"`
	Method                string `json:"method,omitempty"`
	URL                   string `json:"url,omitempty"`
	Path                  string `json:"path,omitempty"`
	Operation             string `json:"operation,omitempty"`
	PipelineID            string `json:"pipelineID,omitempty"`
	PipelineTraceID       string `json:"pipelineTraceID,omitempty"`
	PipelineClientID      string `json:"pipelineClientID,omitempty"`
	PipelineSessionID     string `json:"pipelineSessionID,omitempty"`
	PipelineCommand       string `json:"pipelineCommand,omitempty"`
	PipelineStartUnixNano int64  `json:"pipelineStartUnixNano,omitempty"`
}

type registryIdentity struct {
	ref        string
	host       string
	repository string
	latestRef  string
	sourceKind string
	operation  string
}

type v2RegistryAggregate struct {
	item          v2Registry
	traceIDs      map[string]struct{}
	sessionIDs    map[string]struct{}
	pipelineSet   map[string]struct{}
	sourceKindSet map[string]struct{}
}

func (s *Server) handleV2Registries(w http.ResponseWriter, r *http.Request) {
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

	byRef := map[string]*v2RegistryAggregate{}
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
		collectV2Registries(byRef, traceMeta.Status, traceID, q, spans, proj, scopeIdx)
	}

	items := finalizeV2Registries(byRef)
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

func collectV2Registries(
	byRef map[string]*v2RegistryAggregate,
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
	for _, pipeline := range collectV2CLIRuns(traceStatus, traceID, q, spans, proj, scopeIdx) {
		if pipeline.ClientID == "" {
			continue
		}
		pipelinesByClient[pipeline.ClientID] = append(pipelinesByClient[pipeline.ClientID], pipeline)
	}

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

		ident := detectRegistryIdentity(sp.Name, env.Attributes)
		if ident.ref == "" {
			continue
		}

		agg := byRef[ident.ref]
		if agg == nil {
			agg = &v2RegistryAggregate{
				item: v2Registry{
					ID:                "registry:" + ident.ref,
					Ref:               ident.ref,
					Host:              ident.host,
					Repository:        ident.repository,
					LatestRef:         ident.latestRef,
					FirstSeenUnixNano: sp.StartUnixNano,
					LastSeenUnixNano:  spanLastSeen(sp),
					LastOperation:     ident.operation,
				},
				traceIDs:      map[string]struct{}{},
				sessionIDs:    map[string]struct{}{},
				pipelineSet:   map[string]struct{}{},
				sourceKindSet: map[string]struct{}{},
			}
			byRef[ident.ref] = agg
		}

		agg.item.ActivityCount++
		agg.traceIDs[traceID] = struct{}{}
		if sessionID != "" {
			agg.sessionIDs[sessionID] = struct{}{}
		}
		if ident.sourceKind != "" {
			agg.sourceKindSet[ident.sourceKind] = struct{}{}
		}
		if agg.item.Host == "" {
			agg.item.Host = ident.host
		}
		if agg.item.Repository == "" {
			agg.item.Repository = ident.repository
		}
		if agg.item.FirstSeenUnixNano == 0 || (sp.StartUnixNano > 0 && sp.StartUnixNano < agg.item.FirstSeenUnixNano) {
			agg.item.FirstSeenUnixNano = sp.StartUnixNano
		}
		lastSeen := spanLastSeen(sp)
		if lastSeen >= agg.item.LastSeenUnixNano {
			agg.item.LastSeenUnixNano = lastSeen
			if ident.latestRef != "" {
				agg.item.LatestRef = ident.latestRef
			}
			if ident.operation != "" {
				agg.item.LastOperation = ident.operation
			}
		}

		activity := v2RegistryActivity{
			SpanID:        spanKey(traceID, sp.SpanID),
			TraceID:       traceID,
			SessionID:     sessionID,
			ClientID:      clientID,
			Name:          sp.Name,
			SourceKind:    ident.sourceKind,
			Status:        deriveV2RegistryActivityStatus(traceStatus, sp.StatusCode),
			StartUnixNano: sp.StartUnixNano,
			EndUnixNano:   sp.EndUnixNano,
			Method:        registryHTTPMethod(env.Attributes),
			URL:           registryURL(env.Attributes),
			Path:          registryURLPath(env.Attributes),
			Operation:     ident.operation,
		}
		if pipeline := registryPipelineForSpan(pipelinesByClient[clientID], sp.StartUnixNano, spanLastSeen(sp)); pipeline != nil {
			if _, ok := agg.pipelineSet[pipeline.ID]; !ok {
				agg.pipelineSet[pipeline.ID] = struct{}{}
			}
			activity.PipelineID = pipeline.ID
			activity.PipelineTraceID = pipeline.TraceID
			activity.PipelineClientID = pipeline.ClientID
			activity.PipelineSessionID = pipeline.SessionID
			activity.PipelineCommand = pipeline.Command
			activity.PipelineStartUnixNano = pipeline.StartUnixNano
		}
		agg.item.Activities = append(agg.item.Activities, activity)
	}
}

func finalizeV2Registries(byRef map[string]*v2RegistryAggregate) []v2Registry {
	items := make([]v2Registry, 0, len(byRef))
	for _, agg := range byRef {
		agg.item.TraceCount = len(agg.traceIDs)
		agg.item.SessionCount = len(agg.sessionIDs)
		agg.item.PipelineCount = len(agg.pipelineSet)
		agg.item.SourceKinds = setToSortedSlice(agg.sourceKindSet)
		sort.Slice(agg.item.Activities, func(i, j int) bool {
			if agg.item.Activities[i].StartUnixNano != agg.item.Activities[j].StartUnixNano {
				return agg.item.Activities[i].StartUnixNano > agg.item.Activities[j].StartUnixNano
			}
			return agg.item.Activities[i].SpanID < agg.item.Activities[j].SpanID
		})
		if len(agg.item.Activities) > 50 {
			agg.item.Activities = agg.item.Activities[:50]
		}
		items = append(items, agg.item)
	}
	return items
}

func detectRegistryIdentity(spanName string, attrs map[string]any) registryIdentity {
	if raw := registryRefFromResolveSpan(spanName); raw != "" {
		ident := normalizeRegistryImageRef(raw)
		if ident.ref != "" {
			ident.latestRef = strings.TrimSpace(raw)
			ident.sourceKind = "resolve"
			ident.operation = "resolve"
			return ident
		}
	}

	payload, _ := getV2String(attrs, telemetry.DagCallAttr)
	if payload != "" {
		call, err := decodeWorkspaceOpCallPayload(payload)
		if err == nil && call != nil {
			if raw, sourceKind, operation := registryRefFromCall(spanName, call); raw != "" {
				ident := normalizeRegistryImageRef(raw)
				if ident.ref != "" {
					ident.latestRef = strings.TrimSpace(raw)
					ident.sourceKind = sourceKind
					ident.operation = operation
					return ident
				}
			}
		}
	}

	if rawURL := registryURL(attrs); rawURL != "" {
		ident := registryIdentityFromURL(rawURL, attrs)
		if ident.ref != "" {
			return ident
		}
	}

	return registryIdentity{}
}

func registryRefFromResolveSpan(name string) string {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(strings.ToLower(name), "resolving ") {
		return ""
	}
	return strings.TrimSpace(name[len("resolving "):])
}

func registryRefFromCall(spanName string, call *callpbv1.Call) (raw string, sourceKind string, operation string) {
	if call == nil {
		return "", "", ""
	}
	field := strings.TrimSpace(call.GetField())
	switch field {
	case "from":
		if raw = workspaceOpCallArgString(call, "address", "ref"); raw != "" {
			return raw, "call-from", "from"
		}
	case "publish":
		if raw = workspaceOpCallArgString(call, "address", "ref"); raw != "" {
			return raw, "call-publish", "publish"
		}
	case "withRegistryAuth":
		if raw = workspaceOpCallArgString(call, "address"); raw != "" {
			return raw, "call-auth", "auth"
		}
	case "withoutRegistryAuth":
		if raw = workspaceOpCallArgString(call, "address"); raw != "" {
			return raw, "call-auth", "auth"
		}
	}
	nameLower := strings.ToLower(strings.TrimSpace(spanName))
	if strings.Contains(nameLower, "publish") {
		if raw = workspaceOpCallArgString(call, "address", "ref"); raw != "" {
			return raw, "call-publish", "publish"
		}
	}
	return "", "", ""
}

func registryIdentityFromURL(rawURL string, attrs map[string]any) registryIdentity {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u == nil {
		return registryIdentity{}
	}

	host := strings.TrimSpace(u.Host)
	if host == "" {
		host, _ = getV2String(attrs, "server.address")
	}
	host = canonicalRegistryHost(host)

	if repo := registryRepositoryFromScope(u.Query()); repo != "" {
		canonicalHost := hostFromRegistryQuery(host, u.Query())
		return normalizeRegistryRepository(canonicalHost, repo, canonicalHost+"/"+strings.Trim(repo, "/"), "auth", "auth")
	}

	if repo := registryRepositoryFromPath(u.Path); repo != "" {
		return normalizeRegistryRepository(host, repo, host+"/"+strings.Trim(repo, "/"), "http", registryOperationFromPath(u.Path))
	}

	return registryIdentity{}
}

func normalizeRegistryImageRef(raw string) registryIdentity {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return registryIdentity{}
	}
	if idx := strings.Index(raw, "@"); idx > 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return registryIdentity{}
	}

	if ref, err := name.ParseReference(raw, name.WeakValidation); err == nil {
		repo := ref.Context()
		return normalizeRegistryRepository(repo.RegistryStr(), repo.RepositoryStr(), raw, "", "")
	}
	if repo, err := name.NewRepository(raw, name.WeakValidation); err == nil {
		return normalizeRegistryRepository(repo.RegistryStr(), repo.RepositoryStr(), raw, "", "")
	}
	return registryIdentity{}
}

func normalizeRegistryRepository(host, repository, latestRef, sourceKind, operation string) registryIdentity {
	host = canonicalRegistryHost(host)
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	if host == "" || repository == "" {
		return registryIdentity{}
	}
	return registryIdentity{
		ref:        host + "/" + repository,
		host:       host,
		repository: repository,
		latestRef:  strings.TrimSpace(latestRef),
		sourceKind: sourceKind,
		operation:  operation,
	}
}

func canonicalRegistryHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")
	switch strings.ToLower(host) {
	case "registry-1.docker.io", "registry.docker.io", "auth.docker.io", "index.docker.io":
		return "docker.io"
	default:
		return host
	}
}

func hostFromRegistryQuery(host string, query url.Values) string {
	if service := canonicalRegistryHost(strings.TrimSpace(query.Get("service"))); service != "" {
		return service
	}
	return host
}

func registryRepositoryFromPath(path string) string {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/v2/") {
		return ""
	}
	trimmed := strings.Trim(strings.TrimPrefix(path, "/v2/"), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	for i, part := range parts {
		switch part {
		case "manifests", "blobs", "tags", "referrers":
			if i == 0 {
				return ""
			}
			return strings.Join(parts[:i], "/")
		}
	}
	return ""
}

func registryRepositoryFromScope(query url.Values) string {
	for _, scope := range query["scope"] {
		scope = strings.TrimSpace(scope)
		if !strings.HasPrefix(scope, "repository:") {
			continue
		}
		rest := strings.TrimPrefix(scope, "repository:")
		if idx := strings.LastIndex(rest, ":"); idx > 0 {
			rest = rest[:idx]
		}
		rest = strings.Trim(rest, "/")
		if rest != "" {
			return rest
		}
	}
	return ""
}

func registryOperationFromPath(path string) string {
	path = strings.TrimSpace(path)
	switch {
	case strings.Contains(path, "/manifests/"):
		return "manifest"
	case strings.Contains(path, "/blobs/"):
		return "blob"
	case strings.Contains(path, "/tags/"):
		return "tags"
	case strings.Contains(path, "/referrers/"):
		return "referrers"
	default:
		return "request"
	}
}

func registryURL(attrs map[string]any) string {
	for _, key := range []string{"url.full", "http.url"} {
		if value, ok := getV2String(attrs, key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func registryURLPath(attrs map[string]any) string {
	rawURL := registryURL(attrs)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return ""
	}
	return u.Path
}

func registryHTTPMethod(attrs map[string]any) string {
	for _, key := range []string{"http.request.method", "http.method"} {
		if value, ok := getV2String(attrs, key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func deriveV2RegistryActivityStatus(traceStatus, statusCode string) string {
	if statusCode != "" && statusCode != "STATUS_CODE_OK" && statusCode != "STATUS_CODE_UNSET" && statusCode != "OK" {
		return "failed"
	}
	if traceStatus == "ingesting" {
		return "running"
	}
	return "ready"
}

func registryPipelineForSpan(pipelines []v2CLIRun, startUnixNano, endUnixNano int64) *v2CLIRun {
	for i := range pipelines {
		pipeline := &pipelines[i]
		if pipeline.StartUnixNano > 0 && startUnixNano > 0 && startUnixNano < pipeline.StartUnixNano {
			continue
		}
		if pipeline.EndUnixNano > 0 && endUnixNano > pipeline.EndUnixNano {
			continue
		}
		return pipeline
	}
	return nil
}
