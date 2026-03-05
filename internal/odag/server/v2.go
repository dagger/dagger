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
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

const (
	derivationVersionV2 = "odag-v2alpha1"
	v2DefaultLimit      = 200
	v2MaxLimit          = 2000
	v2TraceScanLimit    = 10000
)

type v2Query struct {
	TraceID         string
	FromUnixNano    int64
	ToUnixNano      int64
	IncludeInternal bool
	Limit           int
	Offset          int
}

type v2Span struct {
	ID            string           `json:"id"`
	TraceID       string           `json:"traceID"`
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
	ID                    string   `json:"id"`
	TraceID               string   `json:"traceID"`
	SpanID                string   `json:"spanID"`
	ParentCallID          string   `json:"parentCallID,omitempty"`
	ClientID              string   `json:"clientID,omitempty"`
	Name                  string   `json:"name"`
	StartUnixNano         int64    `json:"startUnixNano"`
	EndUnixNano           int64    `json:"endUnixNano"`
	StatusCode            string   `json:"statusCode"`
	ReturnType            string   `json:"returnType,omitempty"`
	TopLevel              bool     `json:"topLevel"`
	CallDepth             int      `json:"callDepth"`
	ParentChainIncomplete bool     `json:"parentChainIncomplete,omitempty"`
	ReceiverSnapshotID    string   `json:"receiverSnapshotID,omitempty"`
	ArgSnapshotIDs        []string `json:"argSnapshotIDs,omitempty"`
	OutputSnapshotID      string   `json:"outputSnapshotID,omitempty"`
	DerivedOperation      string   `json:"derivedOperation,omitempty"`
	Internal              bool     `json:"internal,omitempty"`
}

type v2FieldRef struct {
	Path       string `json:"path"`
	SnapshotID string `json:"snapshotID"`
}

type v2ObjectSnapshot struct {
	SnapshotID        string         `json:"snapshotID"`
	TypeName          string         `json:"typeName,omitempty"`
	OutputState       map[string]any `json:"outputState,omitempty"`
	FieldRefs         []v2FieldRef   `json:"fieldRefs,omitempty"`
	FirstSeenUnixNano int64          `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64          `json:"lastSeenUnixNano"`
	TraceIDs          []string       `json:"traceIDs,omitempty"`
	ProducedByCallIDs []string       `json:"producedByCallIDs,omitempty"`
}

type v2ObjectBinding struct {
	BindingID         string   `json:"bindingID"`
	TraceID           string   `json:"traceID"`
	TypeName          string   `json:"typeName"`
	Alias             string   `json:"alias"`
	ScopeSpanID       string   `json:"scopeSpanID,omitempty"`
	CurrentSnapshotID string   `json:"currentSnapshotID,omitempty"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
	Archived          bool     `json:"archived"`
	SnapshotHistory   []string `json:"snapshotHistory,omitempty"`
	ActivityCallIDs   []string `json:"activityCallIDs,omitempty"`
}

type v2Mutation struct {
	ID             string `json:"id"`
	TraceID        string `json:"traceID"`
	BindingID      string `json:"bindingID"`
	CauseCallID    string `json:"causeCallID"`
	ScopeSpanID    string `json:"scopeSpanID,omitempty"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	StartUnixNano  int64  `json:"startUnixNano"`
	EndUnixNano    int64  `json:"endUnixNano"`
	StatusCode     string `json:"statusCode"`
	PrevSnapshotID string `json:"prevSnapshotID,omitempty"`
	NextSnapshotID string `json:"nextSnapshotID,omitempty"`
	Visible        bool   `json:"visible"`
	Internal       bool   `json:"internal,omitempty"`
}

type v2Session struct {
	ID                string `json:"id"`
	TraceID           string `json:"traceID"`
	Status            string `json:"status"`
	Open              bool   `json:"open"`
	FirstSeenUnixNano int64  `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64  `json:"lastSeenUnixNano"`
}

type v2Client struct {
	ID                string   `json:"id"`
	TraceID           string   `json:"traceID"`
	SessionID         string   `json:"sessionID"`
	SpanID            string   `json:"spanID,omitempty"`
	Name              string   `json:"name,omitempty"`
	CommandArgs       []string `json:"commandArgs,omitempty"`
	ServiceName       string   `json:"serviceName,omitempty"`
	ScopeName         string   `json:"scopeName,omitempty"`
	FirstSeenUnixNano int64    `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64    `json:"lastSeenUnixNano"`
}

type v2SpanEnvelope struct {
	Resource   map[string]any   `json:"resource"`
	Scope      map[string]any   `json:"scope"`
	Attributes map[string]any   `json:"attributes"`
	Events     []map[string]any `json:"events"`
}

func (s *Server) handleV2Spans(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q.TraceID)
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
		spans, err := s.store.ListTraceSpans(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("list spans for trace %s: %v", traceID, err), http.StatusInternalServerError)
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
			items = append(items, v2Span{
				ID:            spanKey(traceID, sp.SpanID),
				TraceID:       traceID,
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

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q.TraceID)
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
		proj, err := s.projectTraceWithOptions(r.Context(), traceID, transform.ProjectOptions{
			IncludeInternal: q.IncludeInternal,
			ApplyKeepRules:  false,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("project trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		clientID := deriveTraceClientID(traceID, proj)
		for _, event := range proj.Events {
			if event.RawKind != "call" {
				continue
			}
			if !intersectsTime(event.StartUnixNano, event.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			argSet := map[string]struct{}{}
			for _, in := range event.Inputs {
				if in.StateDigest == "" {
					continue
				}
				argSet[in.StateDigest] = struct{}{}
			}
			argSnapshotIDs := make([]string, 0, len(argSet))
			for digest := range argSet {
				argSnapshotIDs = append(argSnapshotIDs, digest)
			}
			sort.Strings(argSnapshotIDs)
			items = append(items, v2Call{
				ID:                    event.SpanID,
				TraceID:               traceID,
				SpanID:                event.SpanID,
				ParentCallID:          event.ParentCallSpanID,
				ClientID:              clientID,
				Name:                  event.Name,
				StartUnixNano:         event.StartUnixNano,
				EndUnixNano:           event.EndUnixNano,
				StatusCode:            event.StatusCode,
				ReturnType:            event.ReturnType,
				TopLevel:              event.TopLevel,
				CallDepth:             event.CallDepth,
				ParentChainIncomplete: event.ParentChainIncomplete,
				ReceiverSnapshotID:    event.ReceiverStateDigest,
				ArgSnapshotIDs:        argSnapshotIDs,
				OutputSnapshotID:      event.OutputStateDigest,
				DerivedOperation:      event.Operation,
				Internal:              event.Internal,
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

func (s *Server) handleV2ObjectSnapshots(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q.TraceID)
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
		fieldRefSet map[string]struct{}
	}
	byID := map[string]*snapshotAgg{}

	for _, traceID := range traceIDs {
		proj, err := s.projectTraceWithOptions(r.Context(), traceID, transform.ProjectOptions{
			IncludeInternal: q.IncludeInternal,
			ApplyKeepRules:  false,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("project trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, obj := range proj.Objects {
			for _, st := range obj.StateHistory {
				if st.StateDigest == "" {
					continue
				}
				if !intersectsTime(st.StartUnixNano, st.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
					continue
				}
				agg := byID[st.StateDigest]
				if agg == nil {
					agg = &snapshotAgg{
						item: v2ObjectSnapshot{
							SnapshotID:        st.StateDigest,
							TypeName:          obj.TypeName,
							OutputState:       st.OutputStateJSON,
							FirstSeenUnixNano: st.StartUnixNano,
							LastSeenUnixNano:  st.EndUnixNano,
						},
						traceIDs:    map[string]struct{}{},
						callIDs:     map[string]struct{}{},
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
				if typ, ok := getV2String(st.OutputStateJSON, "type"); ok && typ != "" {
					agg.item.TypeName = typ
				}
				if agg.item.FirstSeenUnixNano == 0 || (st.StartUnixNano > 0 && st.StartUnixNano < agg.item.FirstSeenUnixNano) {
					agg.item.FirstSeenUnixNano = st.StartUnixNano
				}
				if st.EndUnixNano > agg.item.LastSeenUnixNano {
					agg.item.LastSeenUnixNano = st.EndUnixNano
				}
				agg.traceIDs[traceID] = struct{}{}
				if st.CallDigest != "" {
					agg.callIDs[st.CallDigest] = struct{}{}
				}
				for _, ref := range extractFieldRefs(st.OutputStateJSON) {
					key := ref.Path + "|" + ref.SnapshotID
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
		sort.Slice(agg.item.FieldRefs, func(i, j int) bool {
			if agg.item.FieldRefs[i].Path != agg.item.FieldRefs[j].Path {
				return agg.item.FieldRefs[i].Path < agg.item.FieldRefs[j].Path
			}
			return agg.item.FieldRefs[i].SnapshotID < agg.item.FieldRefs[j].SnapshotID
		})
		items = append(items, agg.item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].FirstSeenUnixNano != items[j].FirstSeenUnixNano {
			return items[i].FirstSeenUnixNano < items[j].FirstSeenUnixNano
		}
		return items[i].SnapshotID < items[j].SnapshotID
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

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q.TraceID)
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
		proj, err := s.projectTraceWithOptions(r.Context(), traceID, transform.ProjectOptions{
			IncludeInternal: q.IncludeInternal,
			ApplyKeepRules:  false,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("project trace %s: %v", traceID, err), http.StatusInternalServerError)
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
			snapshotHistory := make([]string, 0, len(obj.StateHistory))
			for _, st := range obj.StateHistory {
				if st.StateDigest != "" {
					snapshotHistory = append(snapshotHistory, st.StateDigest)
				}
				if st.CallDigest != "" {
					callSet[st.CallDigest] = struct{}{}
				}
			}
			items = append(items, v2ObjectBinding{
				BindingID:         objectBindingID(traceID, obj.ID),
				TraceID:           traceID,
				TypeName:          obj.TypeName,
				Alias:             obj.Alias,
				ScopeSpanID:       first.SpanID,
				CurrentSnapshotID: last.StateDigest,
				FirstSeenUnixNano: obj.FirstSeenUnixNano,
				LastSeenUnixNano:  obj.LastSeenUnixNano,
				Archived:          meta.Status != "ingesting",
				SnapshotHistory:   snapshotHistory,
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

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q.TraceID)
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
		proj, err := s.projectTraceWithOptions(r.Context(), traceID, transform.ProjectOptions{
			IncludeInternal: q.IncludeInternal,
			ApplyKeepRules:  false,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("project trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, event := range proj.Events {
			if event.Operation != "create" && event.Operation != "mutate" {
				continue
			}
			if event.ObjectID == "" {
				continue
			}
			if !intersectsTime(event.StartUnixNano, event.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			scopeSpanID := event.ParentCallSpanID
			if scopeSpanID == "" {
				scopeSpanID = event.ParentSpanID
			}
			items = append(items, v2Mutation{
				ID:             spanKey(traceID, event.SpanID),
				TraceID:        traceID,
				BindingID:      objectBindingID(traceID, event.ObjectID),
				CauseCallID:    event.SpanID,
				ScopeSpanID:    scopeSpanID,
				Name:           event.Name,
				Kind:           event.Operation,
				StartUnixNano:  event.StartUnixNano,
				EndUnixNano:    event.EndUnixNano,
				StatusCode:     event.StatusCode,
				PrevSnapshotID: event.ReceiverStateDigest,
				NextSnapshotID: event.OutputStateDigest,
				Visible:        event.Visible,
				Internal:       event.Internal,
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

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q.TraceID)
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
		if !intersectsTime(trace.FirstSeenUnixNano, trace.LastSeenUnixNano, q.FromUnixNano, q.ToUnixNano) {
			continue
		}
		items = append(items, v2Session{
			ID:                trace.TraceID,
			TraceID:           trace.TraceID,
			Status:            trace.Status,
			Open:              trace.Status == "ingesting",
			FirstSeenUnixNano: trace.FirstSeenUnixNano,
			LastSeenUnixNano:  trace.LastSeenUnixNano,
		})
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

	traceIDs, err := s.resolveV2TraceIDs(r.Context(), q.TraceID)
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
		proj, err := s.projectTraceWithOptions(r.Context(), traceID, transform.ProjectOptions{
			IncludeInternal: q.IncludeInternal,
			ApplyKeepRules:  false,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("project trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		for _, cmd := range proj.Summary.CommandSpans {
			if !intersectsTime(cmd.StartUnixNano, cmd.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			items = append(items, v2Client{
				ID:                spanKey(traceID, cmd.SpanID),
				TraceID:           traceID,
				SessionID:         traceID,
				SpanID:            cmd.SpanID,
				Name:              cmd.Name,
				CommandArgs:       cmd.CommandArgs,
				ServiceName:       cmd.ServiceName,
				ScopeName:         cmd.ScopeName,
				FirstSeenUnixNano: cmd.StartUnixNano,
				LastSeenUnixNano:  cmd.EndUnixNano,
			})
		}
		if len(proj.Summary.CommandSpans) == 0 {
			items = append(items, v2Client{
				ID:                "trace:" + traceID,
				TraceID:           traceID,
				SessionID:         traceID,
				Name:              proj.Summary.Title,
				FirstSeenUnixNano: proj.StartUnixNano,
				LastSeenUnixNano:  proj.EndUnixNano,
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

func (s *Server) resolveV2TraceIDs(ctx context.Context, traceID string) ([]string, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID != "" {
		if _, err := s.store.GetTrace(ctx, traceID); err != nil {
			return nil, err
		}
		return []string{traceID}, nil
	}

	traces, err := s.store.ListTraces(ctx, v2TraceScanLimit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(traces))
	for _, tr := range traces {
		ids = append(ids, tr.TraceID)
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

func parseV2Query(r *http.Request) (v2Query, error) {
	q := v2Query{
		TraceID: strings.TrimSpace(r.URL.Query().Get("traceID")),
		Limit:   v2DefaultLimit,
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

func deriveTraceClientID(traceID string, proj *transform.TraceProjection) string {
	if proj != nil && len(proj.Summary.CommandSpans) > 0 {
		return spanKey(traceID, proj.Summary.CommandSpans[0].SpanID)
	}
	return "trace:" + traceID
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

func looksLikeSnapshotDigest(v string) bool {
	return (strings.HasPrefix(v, "xxh3:") || strings.HasPrefix(v, "sha256:")) && len(v) > 12
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
		walkFieldValueRefs(field["value"], path, &refs)
	}
	return refs
}

func walkFieldValueRefs(v any, path string, refs *[]v2FieldRef) {
	switch x := v.(type) {
	case string:
		if looksLikeSnapshotDigest(x) {
			*refs = append(*refs, v2FieldRef{
				Path:       path,
				SnapshotID: x,
			})
		}
	case []any:
		for i, item := range x {
			walkFieldValueRefs(item, path+"["+strconv.Itoa(i)+"]", refs)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			nextPath := k
			if path != "" {
				nextPath = path + "." + k
			}
			walkFieldValueRefs(x[k], nextPath, refs)
		}
	}
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
