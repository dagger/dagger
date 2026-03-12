package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2Module struct {
	ID                string   `json:"id"`
	Ref               string   `json:"ref"`
	ResolvedRefs      []string `json:"resolvedRefs,omitempty"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
	TraceCount        int      `json:"traceCount"`
	SessionCount      int      `json:"sessionCount"`
	ClientCount       int      `json:"clientCount"`
	TraceIDs          []string `json:"traceIDs"`
	SessionIDs        []string `json:"sessionIDs"`
	ClientIDs         []string `json:"clientIDs"`
	CallIDs           []string `json:"callIDs,omitempty"`
}

type v2ModuleAggregate struct {
	item         v2Module
	traceIDs     map[string]struct{}
	sessionIDs   map[string]struct{}
	clientIDs    map[string]struct{}
	resolvedRefs map[string]struct{}
	callIDs      map[string]struct{}
}

type v2ModuleIndex struct {
	ByRef                 map[string]*v2TraceModuleRef
	PreferredRefByClient  map[string]string
	PreferredRefBySession map[string]string
}

type v2TraceModuleRef struct {
	Ref               string
	ResolvedRefs      map[string]struct{}
	FirstSeenUnixNano int64
	LastSeenUnixNano  int64
	SessionIDs        map[string]struct{}
	ClientIDs         map[string]struct{}
	CallIDs           map[string]struct{}
}

func (s *Server) handleV2Modules(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if err == store.ErrNotFound {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	byRef := map[string]*v2ModuleAggregate{}
	for _, traceID := range traceIDs {
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		index, err := buildV2TraceModuleIndex(traceID, spans, proj, scopeIdx)
		if err != nil {
			http.Error(w, fmt.Sprintf("build module index for trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for ref, item := range index.ByRef {
			if !intersectsTime(item.FirstSeenUnixNano, item.LastSeenUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			if hasV2ScopeFilter(q) && !moduleMatchesScope(q, traceID, item) {
				continue
			}
			agg := byRef[ref]
			if agg == nil {
				agg = &v2ModuleAggregate{
					item: v2Module{
						ID:                "module:" + ref,
						Ref:               ref,
						FirstSeenUnixNano: item.FirstSeenUnixNano,
						LastSeenUnixNano:  item.LastSeenUnixNano,
					},
					traceIDs:     map[string]struct{}{},
					sessionIDs:   map[string]struct{}{},
					clientIDs:    map[string]struct{}{},
					resolvedRefs: map[string]struct{}{},
					callIDs:      map[string]struct{}{},
				}
				byRef[ref] = agg
			}
			if agg.item.FirstSeenUnixNano == 0 || (item.FirstSeenUnixNano > 0 && item.FirstSeenUnixNano < agg.item.FirstSeenUnixNano) {
				agg.item.FirstSeenUnixNano = item.FirstSeenUnixNano
			}
			if item.LastSeenUnixNano > agg.item.LastSeenUnixNano {
				agg.item.LastSeenUnixNano = item.LastSeenUnixNano
			}
			agg.traceIDs[traceID] = struct{}{}
			for sessionID := range item.SessionIDs {
				agg.sessionIDs[sessionID] = struct{}{}
			}
			for clientID := range item.ClientIDs {
				agg.clientIDs[clientID] = struct{}{}
			}
			for resolvedRef := range item.ResolvedRefs {
				agg.resolvedRefs[resolvedRef] = struct{}{}
			}
			for callID := range item.CallIDs {
				agg.callIDs[callID] = struct{}{}
			}
		}
	}

	items := make([]v2Module, 0, len(byRef))
	for _, agg := range byRef {
		agg.item.TraceIDs = setToSortedSlice(agg.traceIDs)
		agg.item.SessionIDs = setToSortedSlice(agg.sessionIDs)
		agg.item.ClientIDs = setToSortedSlice(agg.clientIDs)
		agg.item.ResolvedRefs = setToSortedSlice(agg.resolvedRefs)
		agg.item.CallIDs = setToSortedSlice(agg.callIDs)
		agg.item.TraceCount = len(agg.item.TraceIDs)
		agg.item.SessionCount = len(agg.item.SessionIDs)
		agg.item.ClientCount = len(agg.item.ClientIDs)
		items = append(items, agg.item)
	}

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

func buildV2TraceModuleIndex(traceID string, spans []store.SpanRecord, proj *transform.TraceProjection, scopeIdx *derive.ScopeIndex) (*v2ModuleIndex, error) {
	index := &v2ModuleIndex{
		ByRef:                 map[string]*v2TraceModuleRef{},
		PreferredRefByClient:  map[string]string{},
		PreferredRefBySession: map[string]string{},
	}
	if scopeIdx == nil {
		return index, nil
	}

	refsByClient := map[string]map[string]*v2TraceModuleRef{}
	explicitRefByClient := map[string]string{}
	addEvidence := func(clientID, sessionID, normalizedRef, resolvedRef string, startUnixNano, endUnixNano int64) {
		clientID = strings.TrimSpace(clientID)
		normalizedRef = strings.TrimSpace(normalizedRef)
		if clientID == "" || normalizedRef == "" {
			return
		}
		byRef := refsByClient[clientID]
		if byRef == nil {
			byRef = map[string]*v2TraceModuleRef{}
			refsByClient[clientID] = byRef
		}
		item := byRef[normalizedRef]
		if item == nil {
			item = &v2TraceModuleRef{
				Ref:          normalizedRef,
				ResolvedRefs: map[string]struct{}{},
				SessionIDs:   map[string]struct{}{},
				ClientIDs:    map[string]struct{}{},
				CallIDs:      map[string]struct{}{},
			}
			byRef[normalizedRef] = item
		}
		item.ClientIDs[clientID] = struct{}{}
		if sessionID != "" {
			item.SessionIDs[sessionID] = struct{}{}
		}
		if resolvedRef != "" {
			item.ResolvedRefs[resolvedRef] = struct{}{}
		}
		if item.FirstSeenUnixNano == 0 || (startUnixNano > 0 && startUnixNano < item.FirstSeenUnixNano) {
			item.FirstSeenUnixNano = startUnixNano
		}
		if endUnixNano > item.LastSeenUnixNano {
			item.LastSeenUnixNano = endUnixNano
		}
	}

	for _, client := range scopeIdx.Clients {
		if normalized, resolved := moduleRefFromCommandArgs(client.CommandArgs); normalized != "" {
			explicitRefByClient[client.ID] = normalized
			addEvidence(client.ID, client.SessionID, normalized, resolved, client.FirstSeenUnixNano, client.LastSeenUnixNano)
		}
	}

	for _, sp := range spans {
		clientID := scopeIdx.ClientIDForSpan(sp.SpanID)
		sessionID := scopeIdx.SessionIDForSpan(sp.SpanID)
		if clientID == "" {
			continue
		}
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			return nil, err
		}
		if normalized, resolved := moduleRefFromSpan(sp.Name, env.Attributes); normalized != "" {
			addEvidence(clientID, sessionID, normalized, resolved, sp.StartUnixNano, sp.EndUnixNano)
		}
	}

	for clientID, ref := range explicitRefByClient {
		if ref != "" {
			index.PreferredRefByClient[clientID] = ref
		}
	}

	for clientID, byRef := range refsByClient {
		if len(byRef) != 1 {
			continue
		}
		for ref := range byRef {
			if ref != "" && index.PreferredRefByClient[clientID] == "" {
				index.PreferredRefByClient[clientID] = ref
			}
		}
	}

	for {
		progress := false
		for _, client := range scopeIdx.Clients {
			if index.PreferredRefByClient[client.ID] != "" {
				continue
			}
			parentID := strings.TrimSpace(client.ParentClientID)
			if parentID != "" {
				if ref := strings.TrimSpace(index.PreferredRefByClient[parentID]); ref != "" {
					index.PreferredRefByClient[client.ID] = ref
					progress = true
					continue
				}
			}
			rootID := strings.TrimSpace(client.RootClientID)
			if rootID != "" && rootID != client.ID {
				if ref := strings.TrimSpace(index.PreferredRefByClient[rootID]); ref != "" {
					index.PreferredRefByClient[client.ID] = ref
					progress = true
				}
			}
		}
		if !progress {
			break
		}
	}

	rootRefsBySession := map[string]map[string]struct{}{}
	allRefsBySession := map[string]map[string]struct{}{}
	addSessionRef := func(target map[string]map[string]struct{}, sessionID, ref string) {
		sessionID = strings.TrimSpace(sessionID)
		ref = strings.TrimSpace(ref)
		if sessionID == "" || ref == "" {
			return
		}
		refs := target[sessionID]
		if refs == nil {
			refs = map[string]struct{}{}
			target[sessionID] = refs
		}
		refs[ref] = struct{}{}
	}
	for _, client := range scopeIdx.Clients {
		ref := strings.TrimSpace(index.PreferredRefByClient[client.ID])
		if ref == "" {
			continue
		}
		addSessionRef(allRefsBySession, client.SessionID, ref)
		if client.ParentClientID == "" || client.RootClientID == client.ID {
			addSessionRef(rootRefsBySession, client.SessionID, ref)
		}
	}
	for sessionID, refs := range rootRefsBySession {
		if len(refs) != 1 {
			continue
		}
		for ref := range refs {
			index.PreferredRefBySession[sessionID] = ref
		}
	}
	for sessionID, refs := range allRefsBySession {
		if index.PreferredRefBySession[sessionID] != "" || len(refs) != 1 {
			continue
		}
		for ref := range refs {
			index.PreferredRefBySession[sessionID] = ref
		}
	}

	for _, event := range proj.Events {
		if event.RawKind != "call" || !isModulePreludeCall(event.Name) {
			continue
		}
		clientID := scopeIdx.ClientIDForSpan(event.SpanID)
		ref := strings.TrimSpace(index.PreferredRefByClient[clientID])
		if ref == "" {
			continue
		}
		item := refsByClient[clientID][ref]
		if item == nil {
			continue
		}
		item.CallIDs[spanKey(traceID, event.SpanID)] = struct{}{}
		if item.FirstSeenUnixNano == 0 || (event.StartUnixNano > 0 && event.StartUnixNano < item.FirstSeenUnixNano) {
			item.FirstSeenUnixNano = event.StartUnixNano
		}
		if event.EndUnixNano > item.LastSeenUnixNano {
			item.LastSeenUnixNano = event.EndUnixNano
		}
	}

	for _, byRef := range refsByClient {
		for ref, item := range byRef {
			agg := index.ByRef[ref]
			if agg == nil {
				agg = &v2TraceModuleRef{
					Ref:          ref,
					ResolvedRefs: map[string]struct{}{},
					SessionIDs:   map[string]struct{}{},
					ClientIDs:    map[string]struct{}{},
					CallIDs:      map[string]struct{}{},
				}
				index.ByRef[ref] = agg
			}
			if agg.FirstSeenUnixNano == 0 || (item.FirstSeenUnixNano > 0 && item.FirstSeenUnixNano < agg.FirstSeenUnixNano) {
				agg.FirstSeenUnixNano = item.FirstSeenUnixNano
			}
			if item.LastSeenUnixNano > agg.LastSeenUnixNano {
				agg.LastSeenUnixNano = item.LastSeenUnixNano
			}
			for resolvedRef := range item.ResolvedRefs {
				agg.ResolvedRefs[resolvedRef] = struct{}{}
			}
			for sessionID := range item.SessionIDs {
				agg.SessionIDs[sessionID] = struct{}{}
			}
			for clientID := range item.ClientIDs {
				agg.ClientIDs[clientID] = struct{}{}
			}
			for callID := range item.CallIDs {
				agg.CallIDs[callID] = struct{}{}
			}
		}
	}

	return index, nil
}

func moduleMatchesScope(q v2Query, traceID string, item *v2TraceModuleRef) bool {
	if item == nil {
		return false
	}
	if q.TraceID != "" && q.TraceID != traceID {
		return false
	}
	if q.SessionID != "" {
		if _, ok := item.SessionIDs[q.SessionID]; !ok {
			return false
		}
	}
	if q.ClientID != "" {
		if _, ok := item.ClientIDs[q.ClientID]; !ok {
			return false
		}
	}
	return true
}

func moduleRefFromSpan(spanName string, attrs map[string]any) (normalized string, resolved string) {
	if moduleRef, ok := getV2String(attrs, telemetry.ModuleRefAttr); ok {
		normalized = normalizeV2ModuleRef(moduleRef)
		if normalized != "" {
			return normalized, strings.TrimSpace(moduleRef)
		}
	}
	lower := strings.ToLower(strings.TrimSpace(spanName))
	if strings.HasPrefix(lower, "load module: ") {
		raw := strings.TrimSpace(spanName[len("load module: "):])
		normalized = normalizeV2ModuleRef(raw)
		if normalized != "" {
			return normalized, strings.TrimSpace(raw)
		}
	}
	return "", ""
}

func moduleRefFromCommandArgs(args []string) (normalized string, resolved string) {
	for i, arg := range args {
		switch {
		case arg == "-m" || arg == "--module":
			if i+1 >= len(args) {
				return "", ""
			}
			resolved = strings.TrimSpace(args[i+1])
		case strings.HasPrefix(arg, "--module="):
			resolved = strings.TrimSpace(strings.TrimPrefix(arg, "--module="))
		}
		if resolved != "" {
			normalized = normalizeV2ModuleRef(resolved)
			if normalized != "" {
				return normalized, resolved
			}
		}
	}
	return "", ""
}

func normalizeV2ModuleRef(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if normalized := normalizeGitRemoteRef(raw); normalized != "" {
		return normalized
	}
	return raw
}
