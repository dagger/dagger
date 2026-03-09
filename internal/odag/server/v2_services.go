package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/transform"
)

type v2Service struct {
	ID                    string              `json:"id"`
	TraceID               string              `json:"traceID"`
	SessionID             string              `json:"sessionID,omitempty"`
	ClientID              string              `json:"clientID,omitempty"`
	Name                  string              `json:"name"`
	Kind                  string              `json:"kind,omitempty"`
	Status                string              `json:"status"`
	DagqlID               string              `json:"dagqlID"`
	StartUnixNano         int64               `json:"startUnixNano"`
	LastActivityUnixNano  int64               `json:"lastActivityUnixNano"`
	CreatedByCallID       string              `json:"createdByCallID,omitempty"`
	CreatedByCallName     string              `json:"createdByCallName,omitempty"`
	ImageRef              string              `json:"imageRef,omitempty"`
	CustomHostname        string              `json:"customHostname,omitempty"`
	ContainerDagqlID      string              `json:"containerDagqlID,omitempty"`
	TunnelUpstreamDagqlID string              `json:"tunnelUpstreamDagqlID,omitempty"`
	PipelineID            string              `json:"pipelineID,omitempty"`
	PipelineClientID      string              `json:"pipelineClientID,omitempty"`
	PipelineCommand       string              `json:"pipelineCommand,omitempty"`
	Activity              []v2ServiceActivity `json:"activity,omitempty"`
}

type v2ServiceActivity struct {
	CallID        string `json:"callID"`
	Name          string `json:"name"`
	Role          string `json:"role,omitempty"`
	Status        string `json:"status"`
	StatusCode    string `json:"statusCode,omitempty"`
	StartUnixNano int64  `json:"startUnixNano"`
	EndUnixNano   int64  `json:"endUnixNano"`
	SessionID     string `json:"sessionID,omitempty"`
	ClientID      string `json:"clientID,omitempty"`
}

func (s *Server) handleV2Services(w http.ResponseWriter, r *http.Request) {
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

	items := make([]v2Service, 0)
	for _, traceID := range traceIDs {
		traceMeta, err := s.store.GetTrace(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("get trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		_, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		items = append(items, collectV2Services(traceMeta.Status, traceID, q, proj, scopeIdx)...)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastActivityUnixNano != items[j].LastActivityUnixNano {
			return items[i].LastActivityUnixNano > items[j].LastActivityUnixNano
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

func collectV2Services(traceStatus, traceID string, q v2Query, proj *transform.TraceProjection, scopeIdx *derive.ScopeIndex) []v2Service {
	if proj == nil || scopeIdx == nil {
		return nil
	}

	pipelinesByClient := map[string][]v2CLIRun{}
	for _, pipeline := range collectV2CLIRuns(traceStatus, traceID, q, proj, scopeIdx) {
		if pipeline.ClientID == "" {
			continue
		}
		pipelinesByClient[pipeline.ClientID] = append(pipelinesByClient[pipeline.ClientID], pipeline)
	}

	outputActivities := map[string][]v2ServiceActivity{}
	receiverActivities := map[string][]v2ServiceActivity{}
	inputActivities := map[string][]v2ServiceActivity{}
	callEventsByID := map[string]transform.MutationEvent{}
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

		callID := spanKey(traceID, event.SpanID)
		callEventsByID[callID] = event
		status := deriveV2ServiceCallStatus(event.StatusCode)
		activity := v2ServiceActivity{
			CallID:        callID,
			Name:          event.Name,
			Status:        status,
			StatusCode:    event.StatusCode,
			StartUnixNano: event.StartUnixNano,
			EndUnixNano:   event.EndUnixNano,
			SessionID:     sessionID,
			ClientID:      clientID,
		}
		if event.OutputStateDigest != "" {
			outputActivities[event.OutputStateDigest] = append(outputActivities[event.OutputStateDigest], withServiceRole(activity, "producer"))
		}
		if event.ReceiverStateDigest != "" {
			receiverActivities[event.ReceiverStateDigest] = append(receiverActivities[event.ReceiverStateDigest], withServiceRole(activity, "lifecycle"))
		}
		for _, input := range event.Inputs {
			if input.StateDigest == "" {
				continue
			}
			inputActivities[input.StateDigest] = append(inputActivities[input.StateDigest], withServiceRole(activity, "consumer"))
		}
	}

	items := make([]v2Service, 0)
	for _, obj := range proj.Objects {
		if obj.TypeName != "Service" {
			continue
		}
		for _, st := range obj.StateHistory {
			if st.StateDigest == "" {
				continue
			}
			if !intersectsTime(st.StartUnixNano, st.EndUnixNano, q.FromUnixNano, q.ToUnixNano) {
				continue
			}
			if !q.IncludeInternal {
				if event := findProjectionEvent(proj, st.SpanID); event != nil && event.Internal {
					continue
				}
			}
			sessionID := scopeIdx.SessionIDForSpan(st.SpanID)
			clientID := scopeIdx.ClientIDForSpan(st.SpanID)
			if !matchesV2Scope(q, traceID, sessionID, clientID) {
				continue
			}

			activities := mergeServiceActivities(outputActivities[st.StateDigest], receiverActivities[st.StateDigest], inputActivities[st.StateDigest])
			sort.Slice(activities, func(i, j int) bool {
				if activities[i].StartUnixNano != activities[j].StartUnixNano {
					return activities[i].StartUnixNano > activities[j].StartUnixNano
				}
				return activities[i].CallID < activities[j].CallID
			})

			createdByCallID := spanKey(traceID, st.SpanID)
			createdByCallName := ""
			if event, ok := callEventsByID[createdByCallID]; ok {
				createdByCallName = event.Name
			}
			if len(activities) > 0 {
				for _, activity := range activities {
					if activity.Role == "producer" {
						createdByCallID = activity.CallID
						createdByCallName = activity.Name
						break
					}
				}
			}

			containerDagqlID := serviceRefForPath(st.OutputStateJSON, "Container")
			tunnelUpstreamDagqlID := serviceRefForPath(st.OutputStateJSON, "TunnelUpstream")
			customHostname := serviceFieldString(st.OutputStateJSON, "CustomHostname")
			imageRef := serviceContainerImageRef(st.OutputStateJSON)
			kind := classifyV2ServiceKind(st.OutputStateJSON, containerDagqlID, tunnelUpstreamDagqlID)
			name := serviceDisplayName(customHostname, imageRef, kind, st.StateDigest)
			lastActivityUnixNano := serviceLastActivity(st.EndUnixNano, activities)
			status := deriveV2ServiceStatus(traceStatus, activities)
			pipeline := servicePipelineForActivities(pipelinesByClient, callEventsByID, activities)

			item := v2Service{
				ID:                    "service:" + traceID + "/" + st.StateDigest,
				TraceID:               traceID,
				SessionID:             sessionID,
				ClientID:              clientID,
				Name:                  name,
				Kind:                  kind,
				Status:                status,
				DagqlID:               st.StateDigest,
				StartUnixNano:         st.StartUnixNano,
				LastActivityUnixNano:  lastActivityUnixNano,
				CreatedByCallID:       createdByCallID,
				CreatedByCallName:     createdByCallName,
				ImageRef:              imageRef,
				CustomHostname:        customHostname,
				ContainerDagqlID:      containerDagqlID,
				TunnelUpstreamDagqlID: tunnelUpstreamDagqlID,
				Activity:              activities,
			}
			if pipeline != nil {
				item.PipelineID = pipeline.ID
				item.PipelineClientID = pipeline.ClientID
				item.PipelineCommand = pipeline.Command
			}
			items = append(items, item)
		}
	}

	return items
}

func withServiceRole(activity v2ServiceActivity, role string) v2ServiceActivity {
	activity.Role = role
	return activity
}

func mergeServiceActivities(groups ...[]v2ServiceActivity) []v2ServiceActivity {
	priority := map[string]int{
		"producer":  3,
		"lifecycle": 2,
		"consumer":  1,
	}
	byID := map[string]v2ServiceActivity{}
	for _, group := range groups {
		for _, activity := range group {
			current, ok := byID[activity.CallID]
			if !ok || priority[activity.Role] > priority[current.Role] {
				byID[activity.CallID] = activity
			}
		}
	}
	out := make([]v2ServiceActivity, 0, len(byID))
	for _, activity := range byID {
		out = append(out, activity)
	}
	return out
}

func deriveV2ServiceCallStatus(statusCode string) string {
	if statusCode != "" && statusCode != "STATUS_CODE_OK" && statusCode != "STATUS_CODE_UNSET" && statusCode != "OK" {
		return "failed"
	}
	if statusCode == "STATUS_CODE_UNSET" {
		return "running"
	}
	return "ready"
}

func deriveV2ServiceStatus(traceStatus string, activities []v2ServiceActivity) string {
	lifecycle := make([]v2ServiceActivity, 0, len(activities))
	for _, activity := range activities {
		if activity.Role == "lifecycle" {
			lifecycle = append(lifecycle, activity)
		}
	}
	sort.Slice(lifecycle, func(i, j int) bool {
		if lifecycle[i].StartUnixNano != lifecycle[j].StartUnixNano {
			return lifecycle[i].StartUnixNano > lifecycle[j].StartUnixNano
		}
		return lifecycle[i].CallID < lifecycle[j].CallID
	})
	if len(lifecycle) > 0 {
		latest := lifecycle[0]
		if latest.Status == "running" || latest.EndUnixNano == 0 {
			return "running"
		}
		if latest.Status == "failed" {
			return "failed"
		}
		return "ready"
	}
	if traceStatus == "ingesting" {
		return "running"
	}
	return "created"
}

func serviceFieldMap(outputState map[string]any, name string) map[string]any {
	fields, _ := outputState["fields"].(map[string]any)
	field, _ := fields[name].(map[string]any)
	return field
}

func serviceFieldString(outputState map[string]any, name string) string {
	field := serviceFieldMap(outputState, name)
	value, _ := field["value"].(string)
	return strings.TrimSpace(value)
}

func serviceFieldRefs(outputState map[string]any, name string) []string {
	field := serviceFieldMap(outputState, name)
	rawRefs, _ := field["refs"].([]any)
	if len(rawRefs) == 0 {
		if refs, ok := field["refs"].([]string); ok {
			return refs
		}
		return nil
	}
	out := make([]string, 0, len(rawRefs))
	for _, raw := range rawRefs {
		if text, ok := raw.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func serviceRefForPath(outputState map[string]any, path string) string {
	refs := serviceFieldRefs(outputState, path)
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

func serviceContainerImageRef(outputState map[string]any) string {
	field := serviceFieldMap(outputState, "Container")
	value, _ := field["value"].(map[string]any)
	imageRef, _ := value["ImageRef"].(string)
	return strings.TrimSpace(imageRef)
}

func classifyV2ServiceKind(outputState map[string]any, containerDagqlID, tunnelUpstreamDagqlID string) string {
	hostSockets, _ := serviceFieldMap(outputState, "HostSockets")["value"].([]any)
	tunnelPorts, _ := serviceFieldMap(outputState, "TunnelPorts")["value"].([]any)
	switch {
	case len(hostSockets) > 0:
		return "socket"
	case tunnelUpstreamDagqlID != "" || len(tunnelPorts) > 0:
		return "tunnel"
	case containerDagqlID != "":
		return "container"
	default:
		return "service"
	}
}

func serviceDisplayName(customHostname, imageRef, kind, dagqlID string) string {
	if customHostname != "" {
		return customHostname
	}
	if imageRef != "" {
		ref := imageRef
		if idx := strings.Index(ref, "@"); idx > 0 {
			ref = ref[:idx]
		}
		if idx := strings.LastIndex(ref, "/"); idx >= 0 && idx+1 < len(ref) {
			return ref[idx+1:]
		}
		return ref
	}
	base := kind
	if base == "" {
		base = "Service"
	} else {
		base = strings.ToUpper(base[:1]) + base[1:]
	}
	return base + " " + shortServiceID(dagqlID)
}

func shortServiceID(dagqlID string) string {
	if len(dagqlID) <= 12 {
		return dagqlID
	}
	return dagqlID[:12]
}

func serviceLastActivity(base int64, activities []v2ServiceActivity) int64 {
	last := base
	for _, activity := range activities {
		end := activity.EndUnixNano
		if end <= 0 {
			end = activity.StartUnixNano
		}
		if end > last {
			last = end
		}
	}
	return last
}

func servicePipelineForActivities(
	pipelinesByClient map[string][]v2CLIRun,
	callEventsByID map[string]transform.MutationEvent,
	activities []v2ServiceActivity,
) *v2CLIRun {
	for _, activity := range activities {
		if activity.ClientID == "" {
			continue
		}
		event, ok := callEventsByID[activity.CallID]
		if !ok {
			continue
		}
		if pipeline := workspaceOpPipelineForEvent(pipelinesByClient[activity.ClientID], event); pipeline != nil {
			return pipeline
		}
	}
	return nil
}
