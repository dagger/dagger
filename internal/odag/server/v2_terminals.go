package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2Terminal struct {
	ID              string               `json:"id"`
	TraceID         string               `json:"traceID"`
	SessionID       string               `json:"sessionID,omitempty"`
	ClientID        string               `json:"clientID,omitempty"`
	CallID          string               `json:"callID,omitempty"`
	Name            string               `json:"name"`
	CallName        string               `json:"callName,omitempty"`
	EntryLabel      string               `json:"entryLabel,omitempty"`
	Status          string               `json:"status"`
	StatusCode      string               `json:"statusCode,omitempty"`
	StartUnixNano   int64                `json:"startUnixNano"`
	EndUnixNano     int64                `json:"endUnixNano"`
	ReceiverDagqlID string               `json:"receiverDagqlID,omitempty"`
	OutputDagqlID   string               `json:"outputDagqlID,omitempty"`
	ActivityCount   int                  `json:"activityCount"`
	ExecCount       int                  `json:"execCount"`
	ActivityNames   []string             `json:"activityNames,omitempty"`
	Activities      []v2TerminalActivity `json:"activities,omitempty"`
	Evidence        []v2EntityEvidence   `json:"evidence,omitempty"`
	Relations       []v2EntityRelation   `json:"relations,omitempty"`
}

type v2TerminalActivity struct {
	SpanID        string `json:"spanID"`
	Name          string `json:"name"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status"`
	StartUnixNano int64  `json:"startUnixNano"`
	EndUnixNano   int64  `json:"endUnixNano"`
}

func (s *Server) handleV2Terminals(w http.ResponseWriter, r *http.Request) {
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

	items := make([]v2Terminal, 0)
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
		items = append(items, collectV2Terminals(traceMeta.Status, traceID, q, spans, proj, scopeIdx)...)
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

func collectV2Terminals(
	traceStatus, traceID string,
	q v2Query,
	spans []store.SpanRecord,
	proj *transform.TraceProjection,
	scopeIdx *derive.ScopeIndex,
) []v2Terminal {
	if proj == nil || scopeIdx == nil {
		return nil
	}

	items := make([]v2Terminal, 0)
	for _, event := range proj.Events {
		if event.RawKind != "call" || strings.TrimSpace(event.Name) != "Container.terminal" {
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

		entryLabel := terminalEntryLabel(spans, scopeIdx, clientID, sessionID, event)
		activities := collectV2TerminalActivities(traceStatus, traceID, spans, scopeIdx, clientID, sessionID, event)
		status, statusCode := deriveV2TerminalStatus(traceStatus, event.StatusCode, activities)
		name := entryLabel
		if strings.TrimSpace(name) == "" {
			name = "Container terminal"
		}

		items = append(items, v2Terminal{
			ID:              "terminal:" + traceID + "/" + event.SpanID,
			TraceID:         traceID,
			SessionID:       sessionID,
			ClientID:        clientID,
			CallID:          spanKey(traceID, event.SpanID),
			Name:            name,
			CallName:        event.Name,
			EntryLabel:      entryLabel,
			Status:          status,
			StatusCode:      statusCode,
			StartUnixNano:   event.StartUnixNano,
			EndUnixNano:     event.EndUnixNano,
			ReceiverDagqlID: event.ReceiverStateDigest,
			OutputDagqlID:   event.OutputStateDigest,
			ActivityCount:   len(activities),
			ExecCount:       countV2TerminalActivitiesByKind(activities, "exec"),
			ActivityNames:   summarizeV2TerminalActivityNames(activities),
			Activities:      activities,
			Evidence:        buildV2TerminalEvidence(entryLabel, activities),
			Relations:       buildV2TerminalRelations(sessionID, clientID, event),
		})
	}
	return items
}

func terminalEntryLabel(
	spans []store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	clientID, sessionID string,
	terminal transform.MutationEvent,
) string {
	spanByID := v2SpanByID(spans)
	if label := terminalAncestorLabel(spanByID, terminal.SpanID, terminal.Name); strings.TrimSpace(label) != "" {
		return label
	}

	var best *store.SpanRecord
	for _, sp := range spans {
		if sp.SpanID == terminal.SpanID || !terminalMatchesScope(scopeIdx, clientID, sessionID, sp.SpanID) {
			continue
		}
		name := strings.TrimSpace(sp.Name)
		if name == "" || strings.EqualFold(name, terminal.Name) || !strings.Contains(strings.ToLower(name), "terminal") {
			continue
		}
		if sp.StartUnixNano > terminal.StartUnixNano || spanLastSeen(sp) < terminal.EndUnixNano {
			continue
		}
		if best == nil ||
			sp.StartUnixNano < best.StartUnixNano ||
			(sp.StartUnixNano == best.StartUnixNano && spanLastSeen(sp) > spanLastSeen(*best)) ||
			(sp.StartUnixNano == best.StartUnixNano && spanLastSeen(sp) == spanLastSeen(*best) && sp.SpanID > best.SpanID) {
			candidate := sp
			best = &candidate
		}
	}
	if best == nil {
		return ""
	}
	return strings.TrimSpace(best.Name)
}

func terminalAncestorLabel(spanByID map[string]store.SpanRecord, spanID, terminalName string) string {
	for parentID := spanByID[spanID].ParentSpanID; parentID != ""; {
		parent, ok := spanByID[parentID]
		if !ok {
			break
		}
		name := strings.TrimSpace(parent.Name)
		if name != "" && strings.Contains(strings.ToLower(name), "terminal") && !strings.EqualFold(name, terminalName) {
			return name
		}
		parentID = parent.ParentSpanID
	}
	return ""
}

func collectV2TerminalActivities(
	traceStatus, traceID string,
	spans []store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	clientID, sessionID string,
	terminal transform.MutationEvent,
) []v2TerminalActivity {
	spanByID := v2SpanByID(spans)
	items := make([]v2TerminalActivity, 0)
	for _, sp := range spans {
		if sp.SpanID == terminal.SpanID {
			continue
		}
		if !terminalActivityInScope(spanByID, scopeIdx, clientID, sessionID, terminal, sp) {
			continue
		}
		kind := classifyV2TerminalActivity(sp.Name)
		if kind == "" {
			continue
		}
		items = append(items, v2TerminalActivity{
			SpanID:        spanKey(traceID, sp.SpanID),
			Name:          sp.Name,
			Kind:          kind,
			Status:        deriveV2RegistryActivityStatus(traceStatus, sp.StatusCode),
			StartUnixNano: sp.StartUnixNano,
			EndUnixNano:   spanLastSeen(sp),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartUnixNano != items[j].StartUnixNano {
			return items[i].StartUnixNano < items[j].StartUnixNano
		}
		return items[i].SpanID < items[j].SpanID
	})
	return items
}

func terminalActivityInScope(
	spanByID map[string]store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
	clientID, sessionID string,
	terminal transform.MutationEvent,
	sp store.SpanRecord,
) bool {
	if terminalDescendsFrom(spanByID, sp.SpanID, terminal.SpanID) {
		return sp.StartUnixNano >= terminal.StartUnixNano
	}
	if !terminalMatchesScope(scopeIdx, clientID, sessionID, sp.SpanID) {
		return false
	}
	if sp.StartUnixNano < terminal.StartUnixNano || sp.StartUnixNano > terminal.EndUnixNano {
		return false
	}
	return spanLastSeen(sp) >= terminal.StartUnixNano
}

func terminalDescendsFrom(spanByID map[string]store.SpanRecord, spanID, ancestorID string) bool {
	for parentID := spanByID[spanID].ParentSpanID; parentID != ""; {
		if parentID == ancestorID {
			return true
		}
		parent, ok := spanByID[parentID]
		if !ok {
			break
		}
		parentID = parent.ParentSpanID
	}
	return false
}

func v2SpanByID(spans []store.SpanRecord) map[string]store.SpanRecord {
	out := make(map[string]store.SpanRecord, len(spans))
	for _, sp := range spans {
		out[sp.SpanID] = sp
	}
	return out
}

func terminalMatchesScope(scopeIdx *derive.ScopeIndex, clientID, sessionID, spanID string) bool {
	if scopeIdx == nil {
		return false
	}
	if clientID != "" && scopeIdx.ClientIDForSpan(spanID) == clientID {
		return true
	}
	return sessionID != "" && scopeIdx.SessionIDForSpan(spanID) == sessionID
}

func classifyV2TerminalActivity(name string) string {
	raw := strings.TrimSpace(name)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "exec "):
		return "exec"
	case strings.Contains(lower, "terminal"):
		return "terminal"
	default:
		return ""
	}
}

func deriveV2TerminalStatus(traceStatus, callStatus string, activities []v2TerminalActivity) (string, string) {
	status := deriveV2RegistryActivityStatus(traceStatus, callStatus)
	statusCode := callStatus
	for _, activity := range activities {
		if activity.Status == "failed" {
			return "failed", "STATUS_CODE_ERROR"
		}
	}
	return status, statusCode
}

func countV2TerminalActivitiesByKind(items []v2TerminalActivity, kind string) int {
	count := 0
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

func summarizeV2TerminalActivityNames(items []v2TerminalActivity) []string {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func buildV2TerminalEvidence(entryLabel string, activities []v2TerminalActivity) []v2EntityEvidence {
	evidence := []v2EntityEvidence{
		{
			Kind:       "Function call",
			Confidence: "high",
			Source:     "Container.terminal",
			Note:       "Explicit terminal entrypoint call.",
		},
	}
	if entryLabel != "" {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Enclosing span",
			Confidence: "medium",
			Source:     entryLabel,
			Note:       "A containing terminal span labels the broader interactive surface.",
		})
	}
	if len(activities) > 0 {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Exec activity",
			Confidence: "high",
			Source:     fmt.Sprintf("%d spans", len(activities)),
			Note:       "Descendant exec spans stayed inside the same terminal window.",
		})
	}
	return evidence
}

func buildV2TerminalRelations(sessionID, clientID string, terminal transform.MutationEvent) []v2EntityRelation {
	relations := make([]v2EntityRelation, 0, 3)
	if sessionID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "owned-by",
			Target:     sessionID,
			TargetKind: "session",
			Note:       "Terminal activity stays inside one session lane.",
		})
	}
	if clientID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "owned-by",
			Target:     clientID,
			TargetKind: "client",
			Note:       "The terminal call belongs to one execution client.",
		})
	}
	if terminal.OutputStateDigest != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "returns",
			Target:     terminal.OutputStateDigest,
			TargetKind: "dagql-state",
			Note:       "Terminal returns a container object.",
		})
	}
	return relations
}
