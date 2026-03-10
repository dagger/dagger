package server

import (
	"fmt"
	"net/http"
	"sort"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
)

type v2Check struct {
	ID            string             `json:"id"`
	TraceID       string             `json:"traceID"`
	SessionID     string             `json:"sessionID,omitempty"`
	ClientID      string             `json:"clientID,omitempty"`
	SpanID        string             `json:"spanID,omitempty"`
	Name          string             `json:"name"`
	SpanName      string             `json:"spanName,omitempty"`
	Status        string             `json:"status"`
	StatusCode    string             `json:"statusCode,omitempty"`
	StartUnixNano int64              `json:"startUnixNano"`
	EndUnixNano   int64              `json:"endUnixNano"`
	Evidence      []v2EntityEvidence `json:"evidence,omitempty"`
	Relations     []v2EntityRelation `json:"relations,omitempty"`
}

func (s *Server) handleV2Checks(w http.ResponseWriter, r *http.Request) {
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

	items := make([]v2Check, 0)
	for _, traceID := range traceIDs {
		traceMeta, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		spans, _, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		items = append(items, collectV2Checks(traceMeta.Status, traceID, q, spans, scopeIdx)...)
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

func collectV2Checks(traceStatus, traceID string, q v2Query, spans []store.SpanRecord, scopeIdx *derive.ScopeIndex) []v2Check {
	if scopeIdx == nil {
		return nil
	}
	items := make([]v2Check, 0)
	for _, sp := range spans {
		if !intersectsTime(sp.StartUnixNano, spanLastSeen(sp), q.FromUnixNano, q.ToUnixNano) {
			continue
		}
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			continue
		}
		checkName, ok := getV2String(env.Attributes, telemetry.CheckNameAttr)
		if !ok || checkName == "" {
			continue
		}
		sessionID := scopeIdx.SessionIDForSpan(sp.SpanID)
		clientID := scopeIdx.ClientIDForSpan(sp.SpanID)
		if !matchesV2Scope(q, traceID, sessionID, clientID) {
			continue
		}
		passed, hasPassed := getV2Bool(env.Attributes, telemetry.CheckPassedAttr)
		status := deriveV2CheckStatus(traceStatus, hasPassed, passed, sp.StatusCode)
		items = append(items, v2Check{
			ID:            "check:" + traceID + "/" + sp.SpanID,
			TraceID:       traceID,
			SessionID:     sessionID,
			ClientID:      clientID,
			SpanID:        spanKey(traceID, sp.SpanID),
			Name:          checkName,
			SpanName:      sp.Name,
			Status:        status,
			StatusCode:    sp.StatusCode,
			StartUnixNano: sp.StartUnixNano,
			EndUnixNano:   spanLastSeen(sp),
			Evidence:      buildV2CheckEvidence(checkName, hasPassed, passed),
			Relations:     buildV2CheckRelations(sessionID, clientID),
		})
	}
	return items
}

func deriveV2CheckStatus(traceStatus string, hasPassed, passed bool, statusCode string) string {
	if hasPassed {
		if passed {
			return "ready"
		}
		return "failed"
	}
	if statusCode != "" && statusCode != "STATUS_CODE_OK" && statusCode != "OK" {
		return "failed"
	}
	if traceStatus == "ingesting" {
		return "running"
	}
	return "ready"
}

func buildV2CheckEvidence(name string, hasPassed, passed bool) []v2EntityEvidence {
	note := "Explicit check span."
	if hasPassed {
		if passed {
			note = "Explicit check span reported pass=true."
		} else {
			note = "Explicit check span reported pass=false."
		}
	}
	return []v2EntityEvidence{
		{
			Kind:       "Check span",
			Confidence: "high",
			Source:     telemetry.CheckNameAttr,
			Note:       note + " " + name,
		},
	}
}

func buildV2CheckRelations(sessionID, clientID string) []v2EntityRelation {
	relations := make([]v2EntityRelation, 0, 2)
	if sessionID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "owned-by",
			Target:     sessionID,
			TargetKind: "session",
			Note:       "Check ran inside one derived session.",
		})
	}
	if clientID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "owned-by",
			Target:     clientID,
			TargetKind: "client",
			Note:       "Check ran inside one execution client.",
		})
	}
	return relations
}
