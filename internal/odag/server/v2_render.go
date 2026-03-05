package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2RenderMode string

const (
	v2RenderModeGlobal v2RenderMode = "global"
	v2RenderModeScope  v2RenderMode = "scope"
	v2RenderModeObject v2RenderMode = "object"
	v2RenderModeHybrid v2RenderMode = "hybrid"
)

type v2RenderResponse struct {
	DerivationVersion string                    `json:"derivationVersion"`
	Context           v2RenderContext           `json:"context"`
	Objects           []v2RenderObject          `json:"objects"`
	Calls             []v2RenderCall            `json:"calls"`
	Edges             []v2RenderEdge            `json:"edges"`
	Events            []transform.MutationEvent `json:"events"`
	ActiveCallIDs     []string                  `json:"activeCallIDs,omitempty"`
	Navigation        v2RenderNavigation        `json:"navigation"`
	Warnings          []string                  `json:"warnings,omitempty"`
}

type v2RenderContext struct {
	TraceID            string `json:"traceID"`
	TraceTitle         string `json:"traceTitle,omitempty"`
	TraceStartUnixNano int64  `json:"traceStartUnixNano"`
	TraceEndUnixNano   int64  `json:"traceEndUnixNano"`
	UnixNano           int64  `json:"unixNano"`
	Mode               string `json:"mode"`
	View               string `json:"view,omitempty"`
	KeepRulesApplied   bool   `json:"keepRulesApplied"`
	ScopeCallID        string `json:"scopeCallID,omitempty"`
	ScopeParentCallID  string `json:"scopeParentCallID,omitempty"`
	FocusObjectID      string `json:"focusObjectID,omitempty"`
	DependencyHopCount int    `json:"dependencyHopCount"`
}

type v2RenderObject struct {
	ObjectID          string         `json:"objectID"`
	BindingID         string         `json:"bindingID"`
	TypeName          string         `json:"typeName"`
	Alias             string         `json:"alias"`
	CurrentSnapshotID string         `json:"currentSnapshotID,omitempty"`
	CurrentState      map[string]any `json:"currentState,omitempty"`
	SnapshotHistory   []string       `json:"snapshotHistory,omitempty"`
	FirstSeenUnixNano int64          `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64          `json:"lastSeenUnixNano"`
	StateCount        int            `json:"stateCount"`
	MissingState      bool           `json:"missingState"`
	ReferencedByTop   bool           `json:"referencedByTop"`
	ActivityCallIDs   []string       `json:"activityCallIDs,omitempty"`
}

type v2RenderCall struct {
	CallID          string   `json:"callID"`
	Name            string   `json:"name"`
	ParentCallID    string   `json:"parentCallID,omitempty"`
	TopLevel        bool     `json:"topLevel"`
	CallDepth       int      `json:"callDepth"`
	StartUnixNano   int64    `json:"startUnixNano"`
	EndUnixNano     int64    `json:"endUnixNano"`
	StatusCode      string   `json:"statusCode"`
	Operation       string   `json:"operation,omitempty"`
	ObjectIDs       []string `json:"objectIDs,omitempty"`       // entire subtree
	DirectObjectIDs []string `json:"directObjectIDs,omitempty"` // direct mutations in this call
	ChildCallIDs    []string `json:"childCallIDs,omitempty"`
}

type v2RenderEdge struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"` // depends-on, contains-call, contains-object
	FromID        string `json:"fromID"`
	ToID          string `json:"toID"`
	Label         string `json:"label,omitempty"`
	EvidenceCount int    `json:"evidenceCount,omitempty"`
}

type v2RenderNavigation struct {
	ScopePath          []string `json:"scopePath,omitempty"`
	EnterableCallIDs   []string `json:"enterableCallIDs,omitempty"`
	EnterableObjectIDs []string `json:"enterableObjectIDs,omitempty"`
	FocusObjectCallIDs []string `json:"focusObjectCallIDs,omitempty"`
}

var v2RenderViews = map[string]v2RenderMode{
	"global": v2RenderModeGlobal,
	"scope":  v2RenderModeScope,
	"object": v2RenderModeObject,
	"hybrid": v2RenderModeHybrid,
}

func (s *Server) handleV2Render(w http.ResponseWriter, r *http.Request) {
	s.handleV2RenderResolvedMode(w, r, "", "")
}

func (s *Server) handleV2RenderView(w http.ResponseWriter, r *http.Request) {
	viewName := strings.ToLower(strings.TrimSpace(r.PathValue("view")))
	if viewName == "" {
		http.Error(w, "view is required", http.StatusBadRequest)
		return
	}
	mode, ok := v2RenderViews[viewName]
	if !ok {
		http.Error(w, "invalid view", http.StatusBadRequest)
		return
	}
	s.handleV2RenderResolvedMode(w, r, mode, viewName)
}

func (s *Server) handleV2RenderResolvedMode(w http.ResponseWriter, r *http.Request, forcedMode v2RenderMode, viewName string) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(q.TraceID) == "" {
		http.Error(w, "traceID is required", http.StatusBadRequest)
		return
	}

	mode := forcedMode
	if mode == "" {
		mode, err = parseV2RenderMode(strings.TrimSpace(r.URL.Query().Get("mode")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	scopeCallID := strings.TrimSpace(r.URL.Query().Get("scopeCallID"))
	focusObjectID := strings.TrimSpace(r.URL.Query().Get("focusObjectID"))
	dependencyHops := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("dependencyHops")); raw != "" {
		v, parseErr := strconv.Atoi(raw)
		if parseErr != nil || v < 0 || v > 4 {
			http.Error(w, "invalid dependencyHops", http.StatusBadRequest)
			return
		}
		dependencyHops = v
	}
	applyKeepRules := false
	if raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keepRules"))); raw != "" {
		switch raw {
		case "1", "true", "on", "default":
			applyKeepRules = true
		case "0", "false", "off", "none":
			applyKeepRules = false
		default:
			http.Error(w, "invalid keepRules", http.StatusBadRequest)
			return
		}
	}

	unixNano := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("t")); raw != "" {
		v, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			http.Error(w, "invalid t", http.StatusBadRequest)
			return
		}
		unixNano = v
	}

	proj, err := s.projectTraceWithOptions(r.Context(), q.TraceID, transform.ProjectOptions{
		IncludeInternal: q.IncludeInternal,
		ApplyKeepRules:  applyKeepRules,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("project trace: %v", err), http.StatusInternalServerError)
		return
	}

	if unixNano <= 0 {
		unixNano = proj.EndUnixNano
	}
	snap := transform.SnapshotAt(proj, unixNano)

	objectByID := make(map[string]transform.ObjectNode, len(snap.Objects))
	for _, obj := range snap.Objects {
		objectByID[obj.ID] = obj
	}
	focusObjectID = normalizeRenderObjectID(q.TraceID, focusObjectID)
	if focusObjectID != "" {
		if _, ok := objectByID[focusObjectID]; !ok {
			focusObjectID = ""
		}
	}

	callByID := map[string]*v2RenderCall{}
	callOrder := make([]string, 0)
	for _, event := range snap.Events {
		if event.RawKind != "call" {
			continue
		}
		if _, exists := callByID[event.SpanID]; exists {
			continue
		}
		callByID[event.SpanID] = &v2RenderCall{
			CallID:        event.SpanID,
			Name:          event.Name,
			ParentCallID:  event.ParentCallSpanID,
			TopLevel:      event.TopLevel,
			CallDepth:     event.CallDepth,
			StartUnixNano: event.StartUnixNano,
			EndUnixNano:   event.EndUnixNano,
			StatusCode:    event.StatusCode,
			Operation:     event.Operation,
		}
		callOrder = append(callOrder, event.SpanID)
	}

	directCallObjects := map[string]map[string]struct{}{}
	subtreeCallObjects := map[string]map[string]struct{}{}
	objectCalls := map[string]map[string]struct{}{}
	for _, event := range snap.Events {
		if event.ObjectID == "" || (event.Operation != "create" && event.Operation != "mutate") {
			continue
		}
		if _, ok := callByID[event.SpanID]; !ok {
			continue
		}
		addSetVal(directCallObjects, event.SpanID, event.ObjectID)
		addSetVal(objectCalls, event.ObjectID, event.SpanID)
		cur := event.SpanID
		for cur != "" {
			addSetVal(subtreeCallObjects, cur, event.ObjectID)
			parent := ""
			call := callByID[cur]
			if call != nil {
				parent = call.ParentCallID
			}
			if parent == "" || parent == cur {
				break
			}
			if _, ok := callByID[parent]; !ok {
				break
			}
			cur = parent
		}
	}

	childCalls := map[string]map[string]struct{}{}
	for callID, call := range callByID {
		if call.ParentCallID == "" {
			continue
		}
		if _, ok := callByID[call.ParentCallID]; !ok {
			continue
		}
		addSetVal(childCalls, call.ParentCallID, callID)
	}
	for callID, call := range callByID {
		call.DirectObjectIDs = setToSortedSlice(directCallObjects[callID])
		call.ObjectIDs = setToSortedSlice(subtreeCallObjects[callID])
		call.ChildCallIDs = setToSortedSlice(childCalls[callID])
	}

	if scopeCallID != "" {
		if _, ok := callByID[scopeCallID]; !ok {
			scopeCallID = ""
		}
	}

	allObjectIDs := map[string]struct{}{}
	for objectID := range objectByID {
		allObjectIDs[objectID] = struct{}{}
	}
	allCallIDs := map[string]struct{}{}
	for callID := range callByID {
		allCallIDs[callID] = struct{}{}
	}

	scopeCallIDs := map[string]struct{}{}
	scopeObjectIDs := map[string]struct{}{}
	scopePath := []string{}
	scopeParentID := ""
	if scopeCallID != "" {
		scopeCallIDs = collectCallSubtreeIDs(callByID, scopeCallID)
		for callID := range scopeCallIDs {
			for _, objectID := range setToSortedSlice(subtreeCallObjects[callID]) {
				scopeObjectIDs[objectID] = struct{}{}
			}
		}
		scopePath = collectCallPath(callByID, scopeCallID)
		if scopeCall := callByID[scopeCallID]; scopeCall != nil {
			scopeParentID = scopeCall.ParentCallID
		}
	}

	focusCallIDs := map[string]struct{}{}
	focusObjectIDs := map[string]struct{}{}
	if focusObjectID != "" {
		focusObjectIDs[focusObjectID] = struct{}{}
		for callID := range objectCalls[focusObjectID] {
			focusCallIDs[callID] = struct{}{}
			for _, objectID := range setToSortedSlice(subtreeCallObjects[callID]) {
				focusObjectIDs[objectID] = struct{}{}
			}
			cur := callID
			for cur != "" {
				call := callByID[cur]
				if call == nil || call.ParentCallID == "" {
					break
				}
				if _, ok := callByID[call.ParentCallID]; !ok {
					break
				}
				cur = call.ParentCallID
				focusCallIDs[cur] = struct{}{}
			}
		}
		if dependencyHops > 0 {
			expandObjectsByDependencies(focusObjectIDs, snap.Edges, dependencyHops)
		}
	}

	visibleObjectIDs := copySet(allObjectIDs)
	visibleCallIDs := copySet(allCallIDs)
	switch mode {
	case v2RenderModeScope:
		if len(scopeCallIDs) > 0 {
			visibleCallIDs = copySet(scopeCallIDs)
			visibleObjectIDs = copySet(scopeObjectIDs)
			if dependencyHops > 0 {
				expandObjectsByDependencies(visibleObjectIDs, snap.Edges, dependencyHops)
			}
		}
	case v2RenderModeObject:
		if len(focusObjectIDs) > 0 {
			visibleCallIDs = copySet(focusCallIDs)
			visibleObjectIDs = copySet(focusObjectIDs)
		}
	case v2RenderModeHybrid:
		switch {
		case len(scopeCallIDs) > 0 && len(focusCallIDs) > 0:
			visibleCallIDs = intersectSet(scopeCallIDs, focusCallIDs)
			visibleObjectIDs = intersectSet(scopeObjectIDs, focusObjectIDs)
		case len(scopeCallIDs) > 0:
			visibleCallIDs = copySet(scopeCallIDs)
			visibleObjectIDs = copySet(scopeObjectIDs)
		case len(focusCallIDs) > 0:
			visibleCallIDs = copySet(focusCallIDs)
			visibleObjectIDs = copySet(focusObjectIDs)
		}
		if dependencyHops > 0 {
			expandObjectsByDependencies(visibleObjectIDs, snap.Edges, dependencyHops)
		}
	}
	if len(visibleObjectIDs) == 0 {
		visibleObjectIDs = copySet(allObjectIDs)
	}
	if len(visibleCallIDs) == 0 {
		visibleCallIDs = copySet(allCallIDs)
	}

	renderObjects := make([]v2RenderObject, 0, len(visibleObjectIDs))
	for _, obj := range snap.Objects {
		if _, ok := visibleObjectIDs[obj.ID]; !ok {
			continue
		}
		currentSnapshot := ""
		currentState := map[string]any(nil)
		snapshotHistory := make([]string, 0, len(obj.StateHistory))
		for _, st := range obj.StateHistory {
			if st.StateDigest != "" {
				snapshotHistory = append(snapshotHistory, st.StateDigest)
			}
		}
		if n := len(obj.StateHistory); n > 0 {
			currentSnapshot = obj.StateHistory[n-1].StateDigest
			currentState = obj.StateHistory[n-1].OutputStateJSON
		}
		renderObjects = append(renderObjects, v2RenderObject{
			ObjectID:          obj.ID,
			BindingID:         objectBindingID(q.TraceID, obj.ID),
			TypeName:          obj.TypeName,
			Alias:             obj.Alias,
			CurrentSnapshotID: currentSnapshot,
			CurrentState:      currentState,
			SnapshotHistory:   snapshotHistory,
			FirstSeenUnixNano: obj.FirstSeenUnixNano,
			LastSeenUnixNano:  obj.LastSeenUnixNano,
			StateCount:        len(obj.StateHistory),
			MissingState:      obj.MissingState,
			ReferencedByTop:   obj.ReferencedByTop,
			ActivityCallIDs:   setToSortedSlice(objectCalls[obj.ID]),
		})
	}
	sort.Slice(renderObjects, func(i, j int) bool {
		if renderObjects[i].FirstSeenUnixNano != renderObjects[j].FirstSeenUnixNano {
			return renderObjects[i].FirstSeenUnixNano < renderObjects[j].FirstSeenUnixNano
		}
		return renderObjects[i].ObjectID < renderObjects[j].ObjectID
	})

	renderCalls := make([]v2RenderCall, 0, len(visibleCallIDs))
	for _, callID := range callOrder {
		call := callByID[callID]
		if call == nil {
			continue
		}
		if _, ok := visibleCallIDs[callID]; !ok {
			continue
		}
		renderCalls = append(renderCalls, *call)
	}
	sort.Slice(renderCalls, func(i, j int) bool {
		if renderCalls[i].StartUnixNano != renderCalls[j].StartUnixNano {
			return renderCalls[i].StartUnixNano < renderCalls[j].StartUnixNano
		}
		return renderCalls[i].CallID < renderCalls[j].CallID
	})

	renderEdges := make([]v2RenderEdge, 0)
	for _, edge := range snap.Edges {
		if _, ok := visibleObjectIDs[edge.FromObjectID]; !ok {
			continue
		}
		if _, ok := visibleObjectIDs[edge.ToObjectID]; !ok {
			continue
		}
		id := "dep:" + edge.FromObjectID + "->" + edge.ToObjectID + ":" + edge.Label
		renderEdges = append(renderEdges, v2RenderEdge{
			ID:            id,
			Kind:          "depends-on",
			FromID:        edge.FromObjectID,
			ToID:          edge.ToObjectID,
			Label:         edge.Label,
			EvidenceCount: edge.EvidenceCount,
		})
	}
	for _, call := range renderCalls {
		for _, childCallID := range call.ChildCallIDs {
			if _, ok := visibleCallIDs[childCallID]; !ok {
				continue
			}
			id := "cc:" + call.CallID + "->" + childCallID
			renderEdges = append(renderEdges, v2RenderEdge{
				ID:     id,
				Kind:   "contains-call",
				FromID: call.CallID,
				ToID:   childCallID,
			})
		}
		for _, objectID := range call.DirectObjectIDs {
			if _, ok := visibleObjectIDs[objectID]; !ok {
				continue
			}
			id := "co:" + call.CallID + "->" + objectID
			renderEdges = append(renderEdges, v2RenderEdge{
				ID:     id,
				Kind:   "contains-object",
				FromID: call.CallID,
				ToID:   objectID,
			})
		}
	}
	sort.Slice(renderEdges, func(i, j int) bool {
		if renderEdges[i].Kind != renderEdges[j].Kind {
			return renderEdges[i].Kind < renderEdges[j].Kind
		}
		if renderEdges[i].FromID != renderEdges[j].FromID {
			return renderEdges[i].FromID < renderEdges[j].FromID
		}
		if renderEdges[i].ToID != renderEdges[j].ToID {
			return renderEdges[i].ToID < renderEdges[j].ToID
		}
		return renderEdges[i].Label < renderEdges[j].Label
	})

	filteredEvents := make([]transform.MutationEvent, 0, len(snap.Events))
	for _, event := range snap.Events {
		if event.RawKind == "call" {
			if _, ok := visibleCallIDs[event.SpanID]; !ok {
				continue
			}
		}
		if event.ObjectID != "" {
			if _, ok := visibleObjectIDs[event.ObjectID]; !ok {
				continue
			}
		}
		filteredEvents = append(filteredEvents, event)
	}

	resp := v2RenderResponse{
		DerivationVersion: derivationVersionV2,
		Context: v2RenderContext{
			TraceID:            q.TraceID,
			TraceTitle:         proj.Summary.Title,
			TraceStartUnixNano: proj.StartUnixNano,
			TraceEndUnixNano:   proj.EndUnixNano,
			UnixNano:           unixNano,
			Mode:               string(mode),
			View:               viewName,
			KeepRulesApplied:   applyKeepRules,
			ScopeCallID:        scopeCallID,
			ScopeParentCallID:  scopeParentID,
			FocusObjectID:      focusObjectID,
			DependencyHopCount: dependencyHops,
		},
		Objects:       renderObjects,
		Calls:         renderCalls,
		Edges:         renderEdges,
		Events:        filteredEvents,
		ActiveCallIDs: snap.ActiveEventIDs,
		Navigation: v2RenderNavigation{
			ScopePath:          scopePath,
			EnterableCallIDs:   sortedSetKeys(visibleCallIDs),
			EnterableObjectIDs: sortedSetKeys(visibleObjectIDs),
			FocusObjectCallIDs: setToSortedSlice(objectCalls[focusObjectID]),
		},
		Warnings: proj.Warnings,
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseV2RenderMode(raw string) (v2RenderMode, error) {
	if raw == "" {
		return v2RenderModeGlobal, nil
	}
	switch v2RenderMode(raw) {
	case v2RenderModeGlobal, v2RenderModeScope, v2RenderModeObject, v2RenderModeHybrid:
		return v2RenderMode(raw), nil
	default:
		return "", fmt.Errorf("invalid mode")
	}
}

func normalizeRenderObjectID(traceID, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "obj-") {
		return raw
	}
	prefix := traceID + "/"
	if strings.HasPrefix(raw, prefix) {
		return strings.TrimPrefix(raw, prefix)
	}
	return raw
}

func collectCallSubtreeIDs(calls map[string]*v2RenderCall, rootCallID string) map[string]struct{} {
	out := map[string]struct{}{}
	if rootCallID == "" {
		return out
	}
	stack := []string{rootCallID}
	for len(stack) > 0 {
		last := len(stack) - 1
		callID := stack[last]
		stack = stack[:last]
		if _, seen := out[callID]; seen {
			continue
		}
		out[callID] = struct{}{}
		for candidateID, call := range calls {
			if call == nil || call.ParentCallID != callID {
				continue
			}
			stack = append(stack, candidateID)
		}
	}
	return out
}

func collectCallPath(calls map[string]*v2RenderCall, callID string) []string {
	if callID == "" {
		return nil
	}
	path := []string{}
	seen := map[string]struct{}{}
	cur := callID
	for cur != "" {
		if _, ok := seen[cur]; ok {
			break
		}
		seen[cur] = struct{}{}
		path = append(path, cur)
		call := calls[cur]
		if call == nil || call.ParentCallID == "" {
			break
		}
		cur = call.ParentCallID
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func addSetVal(sets map[string]map[string]struct{}, key, val string) {
	if key == "" || val == "" {
		return
	}
	set := sets[key]
	if set == nil {
		set = map[string]struct{}{}
		sets[key] = set
	}
	set[val] = struct{}{}
}

func copySet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func intersectSet(a, b map[string]struct{}) map[string]struct{} {
	if len(a) == 0 || len(b) == 0 {
		return map[string]struct{}{}
	}
	out := map[string]struct{}{}
	if len(a) > len(b) {
		a, b = b, a
	}
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func sortedSetKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func expandObjectsByDependencies(objects map[string]struct{}, edges []transform.ObjectEdge, hops int) {
	if hops <= 0 || len(objects) == 0 || len(edges) == 0 {
		return
	}

	adj := map[string]map[string]struct{}{}
	addNeighbor := func(from, to string) {
		if from == "" || to == "" {
			return
		}
		set := adj[from]
		if set == nil {
			set = map[string]struct{}{}
			adj[from] = set
		}
		set[to] = struct{}{}
	}
	for _, edge := range edges {
		addNeighbor(edge.FromObjectID, edge.ToObjectID)
		addNeighbor(edge.ToObjectID, edge.FromObjectID)
	}

	frontier := copySet(objects)
	for step := 0; step < hops; step++ {
		next := map[string]struct{}{}
		for objectID := range frontier {
			for neighborID := range adj[objectID] {
				if _, exists := objects[neighborID]; exists {
					continue
				}
				objects[neighborID] = struct{}{}
				next[neighborID] = struct{}{}
			}
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}
}
