package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

const (
	derivationVersionV2 = "odag-v2alpha2"
	v2DefaultLimit      = 200
	v2MaxLimit          = 2000
	v2TraceScanLimit    = 10000
)

type v2Query struct {
	TraceID         string
	SessionID       string
	ClientID        string
	FunctionID      string
	FromUnixNano    int64
	ToUnixNano      int64
	IncludeInternal bool
	Limit           int
	Offset          int
}

type v2Span struct {
	ID            string           `json:"id"`
	TraceID       string           `json:"traceID"`
	SessionID     string           `json:"sessionID,omitempty"`
	ClientID      string           `json:"clientID,omitempty"`
	SpanID        string           `json:"spanID"`
	ParentSpanID  string           `json:"parentSpanID,omitempty"`
	Name          string           `json:"name"`
	StartUnixNano int64            `json:"startUnixNano"`
	EndUnixNano   int64            `json:"endUnixNano"`
	StatusCode    string           `json:"statusCode"`
	StatusMessage string           `json:"statusMessage,omitempty"`
	Attributes    map[string]any   `json:"attributes,omitempty"`
	Resource      map[string]any   `json:"resource,omitempty"`
	Scope         map[string]any   `json:"scope,omitempty"`
	Events        []map[string]any `json:"events,omitempty"`
	Internal      bool             `json:"internal,omitempty"`
}

type v2Call struct {
	ID                    string      `json:"id"`
	TraceID               string      `json:"traceID"`
	SessionID             string      `json:"sessionID,omitempty"`
	SpanID                string      `json:"spanID"`
	ParentCallID          string      `json:"parentCallID,omitempty"`
	ClientID              string      `json:"clientID,omitempty"`
	Name                  string      `json:"name"`
	StartUnixNano         int64       `json:"startUnixNano"`
	EndUnixNano           int64       `json:"endUnixNano"`
	StatusCode            string      `json:"statusCode"`
	ReturnType            string      `json:"returnType,omitempty"`
	TopLevel              bool        `json:"topLevel"`
	CallDepth             int         `json:"callDepth"`
	ParentChainIncomplete bool        `json:"parentChainIncomplete,omitempty"`
	ReceiverDagqlID       string      `json:"receiverDagqlID,omitempty"`
	ReceiverIsQuery       bool        `json:"receiverIsQuery,omitempty"`
	ArgDagqlIDs           []string    `json:"argDagqlIDs,omitempty"`
	Args                  []v2CallArg `json:"args,omitempty"`
	FunctionID            string      `json:"functionID,omitempty"`
	ReturnTypeID          string      `json:"returnTypeID,omitempty"`
	OutputDagqlID         string      `json:"outputDagqlID,omitempty"`
	DerivedOperation      string      `json:"derivedOperation,omitempty"`
	Internal              bool        `json:"internal,omitempty"`
}

type v2CallArg struct {
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`
	DagqlID string `json:"dagqlID,omitempty"`
	Value   any    `json:"value,omitempty"`
}

type v2FieldRef struct {
	Path    string `json:"path"`
	DagqlID string `json:"dagqlID"`
}

type v2ObjectSnapshot struct {
	DagqlID           string         `json:"dagqlID"`
	TypeName          string         `json:"typeName,omitempty"`
	TypeID            string         `json:"typeID,omitempty"`
	OutputState       map[string]any `json:"outputState,omitempty"`
	FieldRefs         []v2FieldRef   `json:"fieldRefs,omitempty"`
	SessionIDs        []string       `json:"sessionIDs,omitempty"`
	ClientIDs         []string       `json:"clientIDs,omitempty"`
	FirstSeenUnixNano int64          `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64          `json:"lastSeenUnixNano"`
	TraceIDs          []string       `json:"traceIDs,omitempty"`
	ProducedByCallIDs []string       `json:"producedByCallIDs,omitempty"`
}

type v2ObjectBinding struct {
	BindingID         string   `json:"bindingID"`
	ObjectID          string   `json:"objectID"`
	TraceID           string   `json:"traceID"`
	SessionID         string   `json:"sessionID,omitempty"`
	ClientIDs         []string `json:"clientIDs,omitempty"`
	TypeName          string   `json:"typeName"`
	Alias             string   `json:"alias"`
	ScopeSpanID       string   `json:"scopeSpanID,omitempty"`
	CurrentDagqlID    string   `json:"currentDagqlID,omitempty"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
	Archived          bool     `json:"archived"`
	DagqlHistory      []string `json:"dagqlHistory,omitempty"`
	ActivityCallIDs   []string `json:"activityCallIDs,omitempty"`
}

type v2Mutation struct {
	ID            string `json:"id"`
	TraceID       string `json:"traceID"`
	SessionID     string `json:"sessionID,omitempty"`
	ClientID      string `json:"clientID,omitempty"`
	BindingID     string `json:"bindingID"`
	CauseCallID   string `json:"causeCallID"`
	ScopeSpanID   string `json:"scopeSpanID,omitempty"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	StartUnixNano int64  `json:"startUnixNano"`
	EndUnixNano   int64  `json:"endUnixNano"`
	StatusCode    string `json:"statusCode"`
	PrevDagqlID   string `json:"prevDagqlID,omitempty"`
	NextDagqlID   string `json:"nextDagqlID,omitempty"`
	Visible       bool   `json:"visible"`
	Internal      bool   `json:"internal,omitempty"`
}

type v2Session struct {
	ID                string `json:"id"`
	TraceID           string `json:"traceID"`
	RootClientID      string `json:"rootClientID,omitempty"`
	Fallback          bool   `json:"fallback,omitempty"`
	Status            string `json:"status"`
	Open              bool   `json:"open"`
	FirstSeenUnixNano int64  `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64  `json:"lastSeenUnixNano"`
}

type v2Client struct {
	ID                string   `json:"id"`
	TraceID           string   `json:"traceID"`
	SessionID         string   `json:"sessionID"`
	ParentClientID    string   `json:"parentClientID,omitempty"`
	RootClientID      string   `json:"rootClientID,omitempty"`
	SpanID            string   `json:"spanID,omitempty"`
	Name              string   `json:"name,omitempty"`
	CommandArgs       []string `json:"commandArgs,omitempty"`
	ServiceName       string   `json:"serviceName,omitempty"`
	ServiceVersion    string   `json:"serviceVersion,omitempty"`
	ScopeName         string   `json:"scopeName,omitempty"`
	SDKName           string   `json:"sdkName,omitempty"`
	SDKVersion        string   `json:"sdkVersion,omitempty"`
	ClientVersion     string   `json:"clientVersion,omitempty"`
	ClientOS          string   `json:"clientOS,omitempty"`
	ClientArch        string   `json:"clientArch,omitempty"`
	ClientMachineID   string   `json:"clientMachineID,omitempty"`
	ClientKind        string   `json:"clientKind,omitempty"`
	PrimaryModuleRef  string   `json:"primaryModuleRef,omitempty"`
	CallCount         int      `json:"callCount"`
	TopLevelCallCount int      `json:"topLevelCallCount"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
}

type v2SpanEnvelope struct {
	Resource   map[string]any   `json:"resource"`
	Scope      map[string]any   `json:"scope"`
	Attributes map[string]any   `json:"attributes"`
	Events     []map[string]any `json:"events"`
	Links      []map[string]any `json:"links"`
}

func (s *Server) handleV2Spans(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]v2Span, 0)
	for _, traceID := range traceIDs {
		spans, _, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
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
			items = append(items, v2Span{
				ID:            spanKey(traceID, sp.SpanID),
				TraceID:       traceID,
				SessionID:     sessionID,
				ClientID:      clientID,
				SpanID:        sp.SpanID,
				ParentSpanID:  sp.ParentSpanID,
				Name:          sp.Name,
				StartUnixNano: sp.StartUnixNano,
				EndUnixNano:   sp.EndUnixNano,
				StatusCode:    sp.StatusCode,
				StatusMessage: sp.StatusMessage,
				Attributes:    env.Attributes,
				Resource:      env.Resource,
				Scope:         env.Scope,
				Events:        env.Events,
				Internal:      internal,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].StartUnixNano != items[j].StartUnixNano {
			return items[i].StartUnixNano < items[j].StartUnixNano
		}
		if items[i].TraceID != items[j].TraceID {
			return items[i].TraceID < items[j].TraceID
		}
		return items[i].SpanID < items[j].SpanID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func (s *Server) handleV2Calls(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]v2Call, 0)
	for _, traceID := range traceIDs {
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		typeClassifier, err := buildV2TraceObjectTypeClassifier(traceID, spans, proj, scopeIdx)
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
			clientID := scopeIdx.ClientIDForSpan(event.SpanID)
			sessionID := scopeIdx.SessionIDForSpan(event.SpanID)
			if !matchesV2Scope(q, traceID, sessionID, clientID) {
				continue
			}
			argSet := map[string]struct{}{}
			for _, in := range event.Inputs {
				if in.StateDigest == "" {
					continue
				}
				argSet[in.StateDigest] = struct{}{}
			}
			argDagqlIDs := make([]string, 0, len(argSet))
			for digest := range argSet {
				argDagqlIDs = append(argDagqlIDs, digest)
			}
			sort.Strings(argDagqlIDs)
			args := fallbackV2CallArgs(argDagqlIDs)
			if sp, ok := spanByID[event.SpanID]; ok {
				args = decodeV2CallArgs(sp, argDagqlIDs)
			}
			returnTypeID, _ := typeClassifier.classify(event.ReturnType, sessionID, clientID, true)
			functionObservation := v2FunctionObservationFromCall(event, nil, sessionID, clientID, typeClassifier)
			if q.FunctionID != "" && (functionObservation == nil || functionObservation.ID != q.FunctionID) {
				continue
			}
			callID := spanKey(traceID, event.SpanID)
			parentCallID := ""
			if event.ParentCallSpanID != "" {
				parentCallID = spanKey(traceID, event.ParentCallSpanID)
			}
			items = append(items, v2Call{
				ID:                    callID,
				TraceID:               traceID,
				SessionID:             sessionID,
				SpanID:                event.SpanID,
				ParentCallID:          parentCallID,
				ClientID:              clientID,
				Name:                  event.Name,
				StartUnixNano:         event.StartUnixNano,
				EndUnixNano:           event.EndUnixNano,
				StatusCode:            event.StatusCode,
				ReturnType:            event.ReturnType,
				TopLevel:              event.TopLevel,
				CallDepth:             event.CallDepth,
				ParentChainIncomplete: event.ParentChainIncomplete,
				ReceiverDagqlID:       event.ReceiverStateDigest,
				ReceiverIsQuery:       event.ReceiverIsQuery,
				ArgDagqlIDs:           argDagqlIDs,
				Args:                  args,
				FunctionID: func() string {
					if functionObservation != nil {
						return functionObservation.ID
					}
					return ""
				}(),
				ReturnTypeID:     returnTypeID,
				OutputDagqlID:    event.OutputStateDigest,
				DerivedOperation: event.Operation,
				Internal:         event.Internal,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].StartUnixNano != items[j].StartUnixNano {
			return items[i].StartUnixNano < items[j].StartUnixNano
		}
		if items[i].TraceID != items[j].TraceID {
			return items[i].TraceID < items[j].TraceID
		}
		return items[i].SpanID < items[j].SpanID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func decodeV2CallArgs(sp store.SpanRecord, argDagqlIDs []string) []v2CallArg {
	call := serviceSpanCallPayload(sp)
	if call == nil {
		return fallbackV2CallArgs(argDagqlIDs)
	}

	args := make([]v2CallArg, 0, max(len(call.GetArgs()), len(argDagqlIDs)))
	seenDagqlIDs := make(map[string]struct{}, len(argDagqlIDs))
	for index, arg := range call.GetArgs() {
		if arg == nil {
			continue
		}
		name := strings.TrimSpace(arg.GetName())
		if name == "" {
			name = fmt.Sprintf("arg %d", index+1)
		}
		callArg := v2CallArg{Name: name}
		kind, value, dagqlID := decodeV2CallArgLiteral(arg.GetValue())
		callArg.Kind = kind
		if dagqlID != "" {
			callArg.DagqlID = dagqlID
			seenDagqlIDs[dagqlID] = struct{}{}
		} else if kind != "null" {
			callArg.Value = value
		}
		args = append(args, callArg)
	}

	fallbackIndex := 0
	for _, dagqlID := range argDagqlIDs {
		if dagqlID == "" {
			continue
		}
		if _, ok := seenDagqlIDs[dagqlID]; ok {
			continue
		}
		fallbackIndex++
		args = append(args, v2CallArg{
			Name:    fmt.Sprintf("input %d", fallbackIndex),
			Kind:    "object",
			DagqlID: dagqlID,
		})
	}
	return args
}

func fallbackV2CallArgs(argDagqlIDs []string) []v2CallArg {
	if len(argDagqlIDs) == 0 {
		return nil
	}
	args := make([]v2CallArg, 0, len(argDagqlIDs))
	for index, dagqlID := range argDagqlIDs {
		if dagqlID == "" {
			continue
		}
		args = append(args, v2CallArg{
			Name:    fmt.Sprintf("input %d", index+1),
			Kind:    "object",
			DagqlID: dagqlID,
		})
	}
	return args
}

func decodeV2CallArgLiteral(lit *callpbv1.Literal) (kind string, value any, dagqlID string) {
	if lit == nil {
		return "null", nil, ""
	}
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		return "object", nil, v.CallDigest
	case *callpbv1.Literal_Null:
		return "null", nil, ""
	case *callpbv1.Literal_Bool:
		return "bool", v.Bool, ""
	case *callpbv1.Literal_Enum:
		return "enum", v.Enum, ""
	case *callpbv1.Literal_Int:
		return "int", v.Int, ""
	case *callpbv1.Literal_Float:
		return "float", v.Float, ""
	case *callpbv1.Literal_String_:
		return "string", v.String_, ""
	case *callpbv1.Literal_List:
		return "list", serviceLiteralToJSON(lit), ""
	case *callpbv1.Literal_Object:
		return "object-literal", serviceLiteralToJSON(lit), ""
	default:
		return "literal", serviceLiteralToJSON(lit), ""
	}
}

func (s *Server) handleV2ObjectSnapshots(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	type snapshotAgg struct {
		item        v2ObjectSnapshot
		traceIDs    map[string]struct{}
		callIDs     map[string]struct{}
		sessionIDs  map[string]struct{}
		clientIDs   map[string]struct{}
		fieldRefSet map[string]struct{}
	}
	byID := map[string]*snapshotAgg{}

	for _, traceID := range traceIDs {
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		typeClassifier, err := buildV2TraceObjectTypeClassifier(traceID, spans, proj, scopeIdx)
		if err != nil {
			http.Error(w, fmt.Sprintf("build object type classifier for trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
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
				typeName := v2ResolvedOutputStateTypeName(obj.TypeName, st.OutputStateJSON)
				typeID, _ := typeClassifier.classify(typeName, sessionID, clientID, true)
				agg := byID[st.StateDigest]
				if agg == nil {
					agg = &snapshotAgg{
						item: v2ObjectSnapshot{
							DagqlID:           st.StateDigest,
							TypeName:          typeName,
							TypeID:            typeID,
							OutputState:       st.OutputStateJSON,
							FirstSeenUnixNano: st.StartUnixNano,
							LastSeenUnixNano:  st.EndUnixNano,
						},
						traceIDs:    map[string]struct{}{},
						callIDs:     map[string]struct{}{},
						sessionIDs:  map[string]struct{}{},
						clientIDs:   map[string]struct{}{},
						fieldRefSet: map[string]struct{}{},
					}
					if agg.item.TypeName == "" {
						agg.item.TypeName = "Object"
					}
					byID[st.StateDigest] = agg
				}
				if agg.item.OutputState == nil && st.OutputStateJSON != nil {
					agg.item.OutputState = st.OutputStateJSON
				}
				agg.item.TypeName = typeName
				if typeID != "" {
					agg.item.TypeID = typeID
				}
				if agg.item.FirstSeenUnixNano == 0 || (st.StartUnixNano > 0 && st.StartUnixNano < agg.item.FirstSeenUnixNano) {
					agg.item.FirstSeenUnixNano = st.StartUnixNano
				}
				if st.EndUnixNano > agg.item.LastSeenUnixNano {
					agg.item.LastSeenUnixNano = st.EndUnixNano
				}
				agg.traceIDs[traceID] = struct{}{}
				if callID := spanKey(traceID, st.SpanID); st.SpanID != "" {
					agg.callIDs[callID] = struct{}{}
				}
				if sessionID != "" {
					agg.sessionIDs[sessionID] = struct{}{}
				}
				if clientID != "" {
					agg.clientIDs[clientID] = struct{}{}
				}
				for _, ref := range extractFieldRefs(st.OutputStateJSON) {
					key := ref.Path + "|" + ref.DagqlID
					if _, ok := agg.fieldRefSet[key]; ok {
						continue
					}
					agg.fieldRefSet[key] = struct{}{}
					agg.item.FieldRefs = append(agg.item.FieldRefs, ref)
				}
			}
		}
	}

	items := make([]v2ObjectSnapshot, 0, len(byID))
	for _, agg := range byID {
		agg.item.TraceIDs = setToSortedSlice(agg.traceIDs)
		agg.item.ProducedByCallIDs = setToSortedSlice(agg.callIDs)
		agg.item.SessionIDs = setToSortedSlice(agg.sessionIDs)
		agg.item.ClientIDs = setToSortedSlice(agg.clientIDs)
		sort.Slice(agg.item.FieldRefs, func(i, j int) bool {
			if agg.item.FieldRefs[i].Path != agg.item.FieldRefs[j].Path {
				return agg.item.FieldRefs[i].Path < agg.item.FieldRefs[j].Path
			}
			return agg.item.FieldRefs[i].DagqlID < agg.item.FieldRefs[j].DagqlID
		})
		items = append(items, agg.item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].FirstSeenUnixNano != items[j].FirstSeenUnixNano {
			return items[i].FirstSeenUnixNano < items[j].FirstSeenUnixNano
		}
		return items[i].DagqlID < items[j].DagqlID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func (s *Server) handleV2ObjectBindings(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]v2ObjectBinding, 0)
	for _, traceID := range traceIDs {
		meta, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		_, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, obj := range proj.Objects {
			if len(obj.StateHistory) == 0 {
				continue
			}
			first := obj.StateHistory[0]
			last := obj.StateHistory[len(obj.StateHistory)-1]
			if !intersectsTime(first.StartUnixNano, last.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			callSet := map[string]struct{}{}
			clientSet := map[string]struct{}{}
			sessionSet := map[string]struct{}{}
			dagqlHistory := make([]string, 0, len(obj.StateHistory))
			matchedScope := !hasV2ScopeFilter(q)
			for _, st := range obj.StateHistory {
				if !q.IncludeInternal {
					if event := findProjectionEvent(proj, st.SpanID); event != nil && event.Internal {
						continue
					}
				}
				if st.StateDigest != "" {
					dagqlHistory = append(dagqlHistory, st.StateDigest)
				}
				if st.SpanID != "" {
					callSet[spanKey(traceID, st.SpanID)] = struct{}{}
				}
				sessionID := scopeIdx.SessionIDForSpan(st.SpanID)
				clientID := scopeIdx.ClientIDForSpan(st.SpanID)
				if sessionID != "" {
					sessionSet[sessionID] = struct{}{}
				}
				if clientID != "" {
					clientSet[clientID] = struct{}{}
				}
				if matchesV2Scope(q, traceID, sessionID, clientID) {
					matchedScope = true
				}
			}
			if !matchedScope {
				continue
			}
			sessionIDs := setToSortedSlice(sessionSet)
			items = append(items, v2ObjectBinding{
				BindingID:         objectBindingID(traceID, obj.ID),
				ObjectID:          obj.ID,
				TraceID:           traceID,
				SessionID:         firstSortedValue(sessionIDs),
				ClientIDs:         setToSortedSlice(clientSet),
				TypeName:          obj.TypeName,
				Alias:             obj.Alias,
				ScopeSpanID:       first.SpanID,
				CurrentDagqlID:    last.StateDigest,
				FirstSeenUnixNano: obj.FirstSeenUnixNano,
				LastSeenUnixNano:  obj.LastSeenUnixNano,
				Archived:          meta.Status != "ingesting",
				DagqlHistory:      dagqlHistory,
				ActivityCallIDs:   setToSortedSlice(callSet),
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].FirstSeenUnixNano != items[j].FirstSeenUnixNano {
			return items[i].FirstSeenUnixNano < items[j].FirstSeenUnixNano
		}
		return items[i].BindingID < items[j].BindingID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func (s *Server) handleV2Mutations(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]v2Mutation, 0)
	for _, traceID := range traceIDs {
		_, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, event := range proj.Events {
			if event.Operation != "create" && event.Operation != "mutate" {
				continue
			}
			if !q.IncludeInternal && event.Internal {
				continue
			}
			if event.ObjectID == "" {
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
			scopeSpanID := event.ParentCallSpanID
			if scopeSpanID == "" {
				scopeSpanID = event.ParentSpanID
			}
			items = append(items, v2Mutation{
				ID:            spanKey(traceID, event.SpanID),
				TraceID:       traceID,
				SessionID:     sessionID,
				ClientID:      clientID,
				BindingID:     objectBindingID(traceID, event.ObjectID),
				CauseCallID:   spanKey(traceID, event.SpanID),
				ScopeSpanID:   scopeSpanID,
				Name:          event.Name,
				Kind:          event.Operation,
				StartUnixNano: event.StartUnixNano,
				EndUnixNano:   event.EndUnixNano,
				StatusCode:    event.StatusCode,
				PrevDagqlID:   event.ReceiverStateDigest,
				NextDagqlID:   event.OutputStateDigest,
				Visible:       event.Visible,
				Internal:      event.Internal,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].StartUnixNano != items[j].StartUnixNano {
			return items[i].StartUnixNano < items[j].StartUnixNano
		}
		return items[i].ID < items[j].ID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func (s *Server) handleV2Sessions(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]v2Session, 0, len(traceIDs))
	for _, traceID := range traceIDs {
		trace, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		_, _, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, session := range scopeIdx.Sessions {
			if !intersectsTime(session.FirstSeenUnixNano, session.LastSeenUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			if q.SessionID != "" && session.ID != q.SessionID {
				continue
			}
			if q.ClientID != "" && scopeIdx.SessionIDForClient(q.ClientID) != session.ID {
				continue
			}
			items = append(items, v2Session{
				ID:                session.ID,
				TraceID:           trace.TraceID,
				RootClientID:      session.RootClientID,
				Fallback:          session.Fallback,
				Status:            trace.Status,
				Open:              trace.Status == "ingesting",
				FirstSeenUnixNano: session.FirstSeenUnixNano,
				LastSeenUnixNano:  session.LastSeenUnixNano,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].FirstSeenUnixNano != items[j].FirstSeenUnixNano {
			return items[i].FirstSeenUnixNano < items[j].FirstSeenUnixNano
		}
		return items[i].ID < items[j].ID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func (s *Server) handleV2Clients(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "trace not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
		return
	}

	items := make([]v2Client, 0)
	for _, traceID := range traceIDs {
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		callStats := collectV2ClientCallStats(traceID, proj, scopeIdx, q)
		moduleIndex, err := buildV2TraceModuleIndex(traceID, spans, proj, scopeIdx)
		if err != nil {
			http.Error(w, fmt.Sprintf("build module index for trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, client := range scopeIdx.Clients {
			if !intersectsTime(client.FirstSeenUnixNano, client.LastSeenUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			if !matchesV2Scope(q, traceID, client.SessionID, client.ID) {
				continue
			}
			stats := callStats[client.ID]
			items = append(items, v2Client{
				ID:                client.ID,
				TraceID:           traceID,
				SessionID:         client.SessionID,
				ParentClientID:    client.ParentClientID,
				RootClientID:      client.RootClientID,
				SpanID:            client.SpanID,
				Name:              client.Name,
				CommandArgs:       client.CommandArgs,
				ServiceName:       client.ServiceName,
				ServiceVersion:    client.ServiceVersion,
				ScopeName:         client.ScopeName,
				SDKName:           client.SDKName,
				SDKVersion:        client.SDKVersion,
				ClientVersion:     client.ClientVersion,
				ClientOS:          client.ClientOS,
				ClientArch:        client.ClientArch,
				ClientMachineID:   client.ClientMachineID,
				ClientKind:        client.ClientKind,
				PrimaryModuleRef:  moduleIndex.PreferredRefByClient[client.ID],
				CallCount:         stats.CallCount,
				TopLevelCallCount: stats.TopLevelCallCount,
				FirstSeenUnixNano: client.FirstSeenUnixNano,
				LastSeenUnixNano:  client.LastSeenUnixNano,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].FirstSeenUnixNano != items[j].FirstSeenUnixNano {
			return items[i].FirstSeenUnixNano < items[j].FirstSeenUnixNano
		}
		return items[i].ID < items[j].ID
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

type v2ClientCallStats struct {
	CallCount         int
	TopLevelCallCount int
}

func collectV2ClientCallStats(traceID string, proj *transform.TraceProjection, scopeIdx *derive.ScopeIndex, q v2Query) map[string]v2ClientCallStats {
	stats := map[string]v2ClientCallStats{}
	if proj == nil || scopeIdx == nil {
		return stats
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
		clientID := scopeIdx.ClientIDForSpan(event.SpanID)
		sessionID := scopeIdx.SessionIDForSpan(event.SpanID)
		if !matchesV2Scope(q, traceID, sessionID, clientID) {
			continue
		}
		if clientID == "" {
			continue
		}
		entry := stats[clientID]
		entry.CallCount++
		if event.TopLevel {
			entry.TopLevelCallCount++
		}
		stats[clientID] = entry
	}
	return stats
}

func (s *Server) resolveV2TraceIDs(ctx context.Context, q v2Query) ([]string, error) {
	traceID := strings.TrimSpace(q.TraceID)
	switch {
	case traceID != "":
		if _, err := s.store.GetTrace(ctx, traceID); err != nil {
			return nil, err
		}
		return []string{traceID}, nil
	case q.ClientID != "":
		traceID = derive.TraceIDFromClientID(q.ClientID)
		if traceID != "" {
			if _, err := s.store.GetTrace(ctx, traceID); err != nil {
				return nil, err
			}
			return []string{traceID}, nil
		}
	case q.SessionID != "":
		traceID = derive.TraceIDFromSessionID(q.SessionID)
		if traceID != "" {
			if _, err := s.store.GetTrace(ctx, traceID); err != nil {
				return nil, err
			}
			return []string{traceID}, nil
		}
	}

	traces, err := s.store.ListTraces(ctx, v2TraceScanLimit)
	if err != nil {
		return nil, err
	}
	if q.ClientID == "" && q.SessionID == "" {
		ids := make([]string, 0, len(traces))
		for _, tr := range traces {
			ids = append(ids, tr.TraceID)
		}
		return ids, nil
	}

	ids := make([]string, 0, len(traces))
	for _, tr := range traces {
		_, _, scopeIdx, err := s.loadV2TraceScope(ctx, tr.TraceID)
		if err != nil {
			return nil, err
		}
		if !scopeIndexMatchesV2Query(scopeIdx, q) {
			continue
		}
		ids = append(ids, tr.TraceID)
	}
	if len(ids) == 0 {
		return nil, store.ErrNotFound
	}
	return ids, nil
}

func (s *Server) projectTraceWithOptions(ctx context.Context, traceID string, opts transform.ProjectOptions) (*transform.TraceProjection, error) {
	spans, err := s.store.ListTraceSpans(ctx, traceID)
	if err != nil {
		return nil, err
	}
	return transform.ProjectTraceWithOptions(traceID, spans, opts)
}

func buildV2TraceScope(traceID string, spans []store.SpanRecord) (*transform.TraceProjection, *derive.ScopeIndex, error) {
	proj, err := transform.ProjectTraceWithOptions(traceID, spans, transform.ProjectOptions{
		IncludeInternal: true,
		ApplyKeepRules:  false,
	})
	if err != nil {
		return nil, nil, err
	}
	return proj, derive.BuildScopeIndex(traceID, spans, proj), nil
}

func (s *Server) loadV2TraceScope(ctx context.Context, traceID string) ([]store.SpanRecord, *transform.TraceProjection, *derive.ScopeIndex, error) {
	scope, err := s.v2ScopeCache.loadOrCompute(ctx, traceID, func(ctx context.Context) (v2TraceScope, error) {
		spans, err := s.store.ListTraceSpans(ctx, traceID)
		if err != nil {
			return v2TraceScope{}, err
		}
		proj, scopeIdx, err := buildV2TraceScope(traceID, spans)
		if err != nil {
			return v2TraceScope{}, err
		}
		return v2TraceScope{
			spans:    spans,
			proj:     proj,
			scopeIdx: scopeIdx,
		}, nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return scope.spans, scope.proj, scope.scopeIdx, nil
}

func parseV2Query(r *http.Request) (v2Query, error) {
	q := v2Query{
		TraceID:    strings.TrimSpace(r.URL.Query().Get("traceID")),
		SessionID:  strings.TrimSpace(r.URL.Query().Get("sessionID")),
		ClientID:   strings.TrimSpace(r.URL.Query().Get("clientID")),
		FunctionID: strings.TrimSpace(r.URL.Query().Get("functionID")),
		Limit:      v2DefaultLimit,
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return v2Query{}, fmt.Errorf("invalid limit")
		}
		if v > v2MaxLimit {
			v = v2MaxLimit
		}
		q.Limit = v
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return v2Query{}, fmt.Errorf("invalid cursor")
		}
		q.Offset = v
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return v2Query{}, fmt.Errorf("invalid from")
		}
		q.FromUnixNano = v
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return v2Query{}, fmt.Errorf("invalid to")
		}
		q.ToUnixNano = v
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("includeInternal")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return v2Query{}, fmt.Errorf("invalid includeInternal")
		}
		q.IncludeInternal = v
	}
	return q, nil
}

func decodeV2SpanEnvelope(dataJSON string) (v2SpanEnvelope, error) {
	if dataJSON == "" {
		return v2SpanEnvelope{}, nil
	}
	var env v2SpanEnvelope
	if err := json.Unmarshal([]byte(dataJSON), &env); err != nil {
		return v2SpanEnvelope{}, err
	}
	return env, nil
}

func intersectsTime(startUnixNano, endUnixNano, fromUnixNano, toUnixNano int64) bool {
	if startUnixNano < 0 {
		startUnixNano = 0
	}
	if endUnixNano <= 0 {
		endUnixNano = startUnixNano
	}
	if fromUnixNano > 0 && endUnixNano < fromUnixNano {
		return false
	}
	if toUnixNano > 0 && startUnixNano > toUnixNano {
		return false
	}
	return true
}

func spanKey(traceID, spanID string) string {
	return traceID + "/" + spanID
}

func objectBindingID(traceID, objectID string) string {
	return traceID + "/" + objectID
}

func getV2Bool(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	raw, ok := m[key]
	if !ok {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		if v == "true" {
			return true, true
		}
		if v == "false" {
			return false, true
		}
	}
	return false, false
}

func getV2String(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	raw, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	return s, ok
}

func extractFieldRefs(state map[string]any) []v2FieldRef {
	if state == nil {
		return nil
	}
	fieldsRaw, ok := state["fields"]
	if !ok {
		return nil
	}
	fields, ok := fieldsRaw.(map[string]any)
	if !ok {
		return nil
	}
	refs := make([]v2FieldRef, 0)
	for fallbackName, rawField := range fields {
		field, ok := rawField.(map[string]any)
		if !ok {
			continue
		}
		path := fallbackName
		if name, ok := field["name"].(string); ok && name != "" {
			path = name
		}
		values, ok := field["refs"].([]any)
		if !ok || len(values) == 0 {
			continue
		}
		for _, value := range values {
			ref, ok := value.(string)
			if !ok || ref == "" {
				continue
			}
			refs = append(refs, v2FieldRef{
				Path:    path,
				DagqlID: ref,
			})
		}
	}
	return refs
}

func setToSortedSlice(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func hasV2ScopeFilter(q v2Query) bool {
	return q.SessionID != "" || q.ClientID != ""
}

func scopeIndexMatchesV2Query(idx *derive.ScopeIndex, q v2Query) bool {
	if idx == nil {
		return false
	}
	if q.ClientID != "" && !scopeIndexHasClient(idx, q.ClientID) {
		return false
	}
	if q.SessionID != "" && !scopeIndexHasSession(idx, q.SessionID) {
		return false
	}
	return true
}

func scopeIndexHasClient(idx *derive.ScopeIndex, clientID string) bool {
	if idx == nil || clientID == "" {
		return false
	}
	if _, ok := idx.ClientByID[clientID]; ok {
		return true
	}
	for _, spanClientID := range idx.SpanClientIDs {
		if spanClientID == clientID {
			return true
		}
	}
	return false
}

func scopeIndexHasSession(idx *derive.ScopeIndex, sessionID string) bool {
	if idx == nil || sessionID == "" {
		return false
	}
	if _, ok := idx.SessionByID[sessionID]; ok {
		return true
	}
	for _, spanSessionID := range idx.SpanSessionIDs {
		if spanSessionID == sessionID {
			return true
		}
	}
	return false
}

func matchesV2Scope(q v2Query, traceID, sessionID, clientID string) bool {
	if q.TraceID != "" && q.TraceID != traceID {
		return false
	}
	if q.SessionID != "" && q.SessionID != sessionID {
		return false
	}
	if q.ClientID != "" && q.ClientID != clientID {
		return false
	}
	return true
}

func findProjectionEvent(proj *transform.TraceProjection, spanID string) *transform.MutationEvent {
	if proj == nil || spanID == "" {
		return nil
	}
	for i := range proj.Events {
		if proj.Events[i].SpanID == spanID {
			return &proj.Events[i]
		}
	}
	return nil
}

func firstSortedValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func paginate[T any](items []T, offset int, limit int) ([]T, string) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = v2DefaultLimit
	}
	if offset >= len(items) {
		return []T{}, ""
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[offset:end], next
}
