package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

const v2CoreModuleRef = "core"

type v2Function struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	CallName          string   `json:"callName"`
	ReceiverType      string   `json:"receiverType,omitempty"`
	ReceiverTypeID    string   `json:"receiverTypeID,omitempty"`
	ModuleRef         string   `json:"moduleRef"`
	OriginalName      string   `json:"originalName,omitempty"`
	Description       string   `json:"description,omitempty"`
	ReturnType        string   `json:"returnType,omitempty"`
	ReturnTypeID      string   `json:"returnTypeID,omitempty"`
	ArgNames          []string `json:"argNames,omitempty"`
	ArgCount          int      `json:"argCount"`
	CallCount         int      `json:"callCount"`
	SnapshotCount     int      `json:"snapshotCount"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
	TraceCount        int      `json:"traceCount"`
	SessionCount      int      `json:"sessionCount"`
	ClientCount       int      `json:"clientCount"`
	TraceIDs          []string `json:"traceIDs"`
	SessionIDs        []string `json:"sessionIDs"`
	ClientIDs         []string `json:"clientIDs"`
	CallIDs           []string `json:"callIDs,omitempty"`
	SnapshotDagqlIDs  []string `json:"snapshotDagqlIDs,omitempty"`
}

type v2FunctionAggregate struct {
	item             v2Function
	traceIDs         map[string]struct{}
	sessionIDs       map[string]struct{}
	clientIDs        map[string]struct{}
	callIDs          map[string]struct{}
	snapshotDagqlIDs map[string]struct{}
}

type v2FunctionObservation struct {
	ID             string
	Name           string
	CallName       string
	ReceiverType   string
	ReceiverTypeID string
	ModuleRef      string
	OriginalName   string
	Description    string
	ReturnType     string
	ReturnTypeID   string
	ArgNames       []string
}

type v2TraceFunctionSnapshotIndex struct{}

type v2FunctionIdentity struct {
	ID        string
	ModuleRef string
	CallName  string
}

var v2KnownCoreQueryFunctionNames = map[string]struct{}{
	"_contextDirectory":   {},
	"cacheVolume":         {},
	"container":           {},
	"currentFunctionCall": {},
	"directory":           {},
	"engine":              {},
	"file":                {},
	"function":            {},
	"git":                 {},
	"http":                {},
	"loadModule":          {},
	"moduleSource":        {},
	"secret":              {},
	"service":             {},
	"setSecret":           {},
	"socket":              {},
	"typeDef":             {},
	"version":             {},
}

func (s *Server) handleV2Functions(w http.ResponseWriter, r *http.Request) {
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

	byID := map[string]*v2FunctionAggregate{}
	for _, traceID := range traceIDs {
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		classifier, err := buildV2TraceObjectTypeClassifier(traceID, spans, proj, scopeIdx)
		if err != nil {
			http.Error(w, fmt.Sprintf("build object type classifier for trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		spanByID := make(map[string]store.SpanRecord, len(spans))
		for _, sp := range spans {
			spanByID[sp.SpanID] = sp
		}
		for _, event := range proj.Events {
			if event.RawKind != "call" {
				continue
			}
			if !q.IncludeInternal && event.Internal {
				continue
			}
			if !intersectsTime(event.StartUnixNano, event.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			sessionID := scopeIdx.SessionIDForSpan(event.SpanID)
			clientID := scopeIdx.ClientIDForSpan(event.SpanID)
			if !matchesV2Scope(q, traceID, sessionID, clientID) {
				continue
			}
			var payload *callpbv1.Call
			if sp, ok := spanByID[event.SpanID]; ok {
				payload = serviceSpanCallPayload(sp)
			}
			obs := v2FunctionObservationFromCall(event, payload, sessionID, clientID, classifier)
			if obs == nil {
				continue
			}
			recordV2FunctionObservation(byID, *obs, traceID, sessionID, clientID, spanKey(traceID, event.SpanID), "", event.StartUnixNano, event.EndUnixNano)
		}
		for _, obj := range proj.Objects {
			for _, st := range obj.StateHistory {
				if st.StateDigest == "" {
					continue
				}
				if !q.IncludeInternal {
					if event := findProjectionEvent(proj, st.SpanID); event != nil && event.Internal {
						continue
					}
				}
				if !intersectsTime(st.StartUnixNano, st.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
					continue
				}
				sessionID := scopeIdx.SessionIDForSpan(st.SpanID)
				clientID := scopeIdx.ClientIDForSpan(st.SpanID)
				if !matchesV2Scope(q, traceID, sessionID, clientID) {
					continue
				}
				obs := v2FunctionObservationFromSnapshot(st.OutputStateJSON, sessionID, clientID, classifier)
				if obs == nil {
					continue
				}
				recordV2FunctionObservation(byID, *obs, traceID, sessionID, clientID, "", st.StateDigest, st.StartUnixNano, st.EndUnixNano)
			}
		}
	}

	items := make([]v2Function, 0, len(byID))
	for _, agg := range byID {
		agg.item.TraceIDs = setToSortedSlice(agg.traceIDs)
		agg.item.SessionIDs = setToSortedSlice(agg.sessionIDs)
		agg.item.ClientIDs = setToSortedSlice(agg.clientIDs)
		agg.item.CallIDs = setToSortedSlice(agg.callIDs)
		agg.item.SnapshotDagqlIDs = setToSortedSlice(agg.snapshotDagqlIDs)
		agg.item.TraceCount = len(agg.item.TraceIDs)
		agg.item.SessionCount = len(agg.item.SessionIDs)
		agg.item.ClientCount = len(agg.item.ClientIDs)
		agg.item.CallCount = len(agg.item.CallIDs)
		agg.item.SnapshotCount = len(agg.item.SnapshotDagqlIDs)
		agg.item.ArgCount = len(agg.item.ArgNames)
		items = append(items, agg.item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastSeenUnixNano != items[j].LastSeenUnixNano {
			return items[i].LastSeenUnixNano > items[j].LastSeenUnixNano
		}
		if items[i].ModuleRef != items[j].ModuleRef {
			return items[i].ModuleRef < items[j].ModuleRef
		}
		return items[i].CallName < items[j].CallName
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func v2FunctionObservationFromCall(
	event transform.MutationEvent,
	payload *callpbv1.Call,
	sessionID string,
	clientID string,
	classifier *v2TraceObjectTypeClassifier,
) *v2FunctionObservation {
	identity := v2FunctionIdentityForCall(event.Name, sessionID, clientID, classifier, nil)
	if identity.ID == "" || identity.CallName == "" {
		return nil
	}
	receiverType, fieldName := v2CallNameParts(identity.CallName)
	receiverTypeID, _ := classifier.classify(receiverType, sessionID, clientID, true)
	returnType := strings.TrimSpace(event.ReturnType)
	returnTypeID, _ := classifier.classify(returnType, sessionID, clientID, true)
	return &v2FunctionObservation{
		ID:             identity.ID,
		Name:           fieldName,
		CallName:       identity.CallName,
		ReceiverType:   receiverType,
		ReceiverTypeID: receiverTypeID,
		ModuleRef:      identity.ModuleRef,
		ReturnType:     returnType,
		ReturnTypeID:   returnTypeID,
		ArgNames:       v2FunctionArgNamesFromCallPayload(payload),
	}
}

func v2FunctionObservationFromSnapshot(
	outputState map[string]any,
	sessionID string,
	clientID string,
	classifier *v2TraceObjectTypeClassifier,
) *v2FunctionObservation {
	if strings.TrimSpace(v2OutputStateTypeName(outputState)) != "Function" {
		return nil
	}
	name := v2OutputStateFieldString(outputState, "Name")
	if name == "" {
		return nil
	}
	receiverType := strings.TrimSpace(v2OutputStateFieldString(outputState, "ParentOriginalName"))
	if receiverType == "" {
		receiverType = strings.TrimSpace(v2OutputStateFieldString(outputState, "ParentName"))
	}
	if receiverType == "" {
		receiverType = "Query"
	}
	callName := receiverType + "." + name
	receiverTypeID, _ := classifier.classify(receiverType, sessionID, clientID, true)
	returnType, _ := v2FunctionReturnObjectType(outputState)
	returnTypeID, _ := classifier.classify(returnType, sessionID, clientID, true)
	moduleRef := v2FunctionModuleRef(receiverType, name, returnType, sessionID, clientID, classifier)
	return &v2FunctionObservation{
		ID:             v2FunctionID(callName, moduleRef),
		Name:           name,
		CallName:       callName,
		ReceiverType:   receiverType,
		ReceiverTypeID: receiverTypeID,
		ModuleRef:      moduleRef,
		OriginalName:   v2OutputStateFieldString(outputState, "OriginalName"),
		Description:    v2OutputStateFieldString(outputState, "Description"),
		ReturnType:     returnType,
		ReturnTypeID:   returnTypeID,
		ArgNames:       v2FunctionSnapshotArgNames(outputState),
	}
}

func recordV2FunctionObservation(
	byID map[string]*v2FunctionAggregate,
	obs v2FunctionObservation,
	traceID string,
	sessionID string,
	clientID string,
	callID string,
	snapshotDagqlID string,
	startUnixNano int64,
	endUnixNano int64,
) {
	if obs.ID == "" || obs.CallName == "" {
		return
	}
	agg := byID[obs.ID]
	if agg == nil {
		agg = &v2FunctionAggregate{
			item: v2Function{
				ID:                obs.ID,
				Name:              obs.Name,
				CallName:          obs.CallName,
				ReceiverType:      obs.ReceiverType,
				ModuleRef:         v2NormalizedFunctionModuleRef(obs.ModuleRef),
				OriginalName:      obs.OriginalName,
				Description:       obs.Description,
				ReturnType:        obs.ReturnType,
				ReturnTypeID:      obs.ReturnTypeID,
				ArgNames:          append([]string(nil), obs.ArgNames...),
				FirstSeenUnixNano: startUnixNano,
				LastSeenUnixNano:  endUnixNano,
			},
			traceIDs:         map[string]struct{}{},
			sessionIDs:       map[string]struct{}{},
			clientIDs:        map[string]struct{}{},
			callIDs:          map[string]struct{}{},
			snapshotDagqlIDs: map[string]struct{}{},
		}
		byID[obs.ID] = agg
	}
	if agg.item.FirstSeenUnixNano == 0 || (startUnixNano > 0 && startUnixNano < agg.item.FirstSeenUnixNano) {
		agg.item.FirstSeenUnixNano = startUnixNano
	}
	if endUnixNano > agg.item.LastSeenUnixNano {
		agg.item.LastSeenUnixNano = endUnixNano
	}
	if agg.item.Name == "" && obs.Name != "" {
		agg.item.Name = obs.Name
	}
	if agg.item.CallName == "" && obs.CallName != "" {
		agg.item.CallName = obs.CallName
	}
	if agg.item.ReceiverType == "" && obs.ReceiverType != "" {
		agg.item.ReceiverType = obs.ReceiverType
	}
	if agg.item.ModuleRef == "" {
		agg.item.ModuleRef = v2NormalizedFunctionModuleRef(obs.ModuleRef)
	}
	if agg.item.OriginalName == "" && obs.OriginalName != "" {
		agg.item.OriginalName = obs.OriginalName
	}
	if agg.item.Description == "" && obs.Description != "" {
		agg.item.Description = obs.Description
	}
	if agg.item.ReturnType == "" && obs.ReturnType != "" {
		agg.item.ReturnType = obs.ReturnType
	}
	if agg.item.ReceiverTypeID == "" && obs.ReceiverTypeID != "" {
		agg.item.ReceiverTypeID = obs.ReceiverTypeID
	}
	if agg.item.ReturnTypeID == "" && obs.ReturnTypeID != "" {
		agg.item.ReturnTypeID = obs.ReturnTypeID
	}
	if len(obs.ArgNames) > len(agg.item.ArgNames) {
		agg.item.ArgNames = append([]string(nil), obs.ArgNames...)
	}
	if traceID != "" {
		agg.traceIDs[traceID] = struct{}{}
	}
	if sessionID != "" {
		agg.sessionIDs[sessionID] = struct{}{}
	}
	if clientID != "" {
		agg.clientIDs[clientID] = struct{}{}
	}
	if callID != "" {
		agg.callIDs[callID] = struct{}{}
	}
	if snapshotDagqlID != "" {
		agg.snapshotDagqlIDs[snapshotDagqlID] = struct{}{}
	}
}

func buildV2TraceFunctionSnapshotIndex(_ *transform.TraceProjection, _ *derive.ScopeIndex, _ *v2TraceObjectTypeClassifier) *v2TraceFunctionSnapshotIndex {
	return &v2TraceFunctionSnapshotIndex{}
}

func v2FunctionIdentityForCall(
	callName string,
	sessionID string,
	clientID string,
	classifier *v2TraceObjectTypeClassifier,
	_ *v2TraceFunctionSnapshotIndex,
) v2FunctionIdentity {
	callName = strings.TrimSpace(callName)
	if callName == "" {
		return v2FunctionIdentity{}
	}
	receiverType, fieldName := v2CallNameParts(callName)
	if fieldName == "" {
		return v2FunctionIdentity{}
	}
	returnType := ""
	moduleRef := v2FunctionModuleRef(receiverType, fieldName, returnType, sessionID, clientID, classifier)
	return v2FunctionIdentity{
		ID:        v2FunctionID(callName, moduleRef),
		ModuleRef: moduleRef,
		CallName:  callName,
	}
}

func v2FunctionID(callName, moduleRef string) string {
	moduleRef = v2NormalizedFunctionModuleRef(moduleRef)
	callName = strings.TrimSpace(callName)
	return "function:" + moduleRef + "#" + callName
}

func v2NormalizedFunctionModuleRef(moduleRef string) string {
	moduleRef = strings.TrimSpace(moduleRef)
	if moduleRef == "" {
		return v2CoreModuleRef
	}
	return moduleRef
}

func v2CallNameParts(callName string) (receiverType string, fieldName string) {
	callName = strings.TrimSpace(callName)
	if callName == "" {
		return "", ""
	}
	dot := strings.LastIndex(callName, ".")
	if dot < 0 {
		return "", callName
	}
	receiverType = strings.TrimSpace(callName[:dot])
	fieldName = strings.TrimSpace(callName[dot+1:])
	return receiverType, fieldName
}

func v2FunctionModuleRef(receiverType, fieldName, returnType, sessionID, clientID string, classifier *v2TraceObjectTypeClassifier) string {
	receiverType = strings.TrimSpace(receiverType)
	fieldName = strings.TrimSpace(fieldName)
	if receiverType == "" || receiverType == "Query" {
		if v2IsKnownCoreQueryFunctionName(fieldName) {
			return v2CoreModuleRef
		}
		if classifier != nil {
			if moduleRef := classifier.moduleRefForScope(sessionID, clientID); moduleRef != "" {
				return moduleRef
			}
			_, moduleRefs := classifier.classify(returnType, sessionID, clientID, true)
			if len(moduleRefs) > 0 {
				return moduleRefs[0]
			}
		}
		return v2CoreModuleRef
	}
	if classifier != nil {
		_, moduleRefs := classifier.classify(receiverType, sessionID, clientID, true)
		if len(moduleRefs) > 0 {
			return moduleRefs[0]
		}
	}
	return v2CoreModuleRef
}

func v2IsKnownCoreQueryFunctionName(name string) bool {
	_, ok := v2KnownCoreQueryFunctionNames[strings.TrimSpace(name)]
	return ok
}

func v2FunctionArgNamesFromCallPayload(call *callpbv1.Call) []string {
	if call == nil {
		return nil
	}
	names := make([]string, 0, len(call.GetArgs()))
	seen := map[string]struct{}{}
	for _, arg := range call.GetArgs() {
		if arg == nil {
			continue
		}
		name := strings.TrimSpace(arg.GetName())
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func v2FunctionSnapshotArgNames(outputState map[string]any) []string {
	field := v2OutputStateFieldMap(outputState, "Args")
	items, _ := field["value"].([]any)
	names := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		value, _ := item.(map[string]any)
		name, _ := getV2String(value, "Name")
		name = strings.TrimSpace(name)
		if name == "" {
			name, _ = getV2String(value, "OriginalName")
			name = strings.TrimSpace(name)
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

var (
	_ *derive.ScopeIndex
	_ *transform.TraceProjection
)
