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
	Logs                  []v2ServiceLog      `json:"logs,omitempty"`
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

type v2ServiceLog struct {
	ID           string `json:"id"`
	SpanID       string `json:"spanID,omitempty"`
	TimeUnixNano int64  `json:"timeUnixNano"`
	Level        string `json:"level,omitempty"`
	Source       string `json:"source,omitempty"`
	Message      string `json:"message"`
	Kind         string `json:"kind,omitempty"`
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
		spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), traceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("load trace %s: %v", traceID, err), http.StatusInternalServerError)
			return
		}
		items = append(items, collectV2Services(traceMeta.Status, traceID, q, spans, proj, scopeIdx)...)
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

func collectV2Services(traceStatus, traceID string, q v2Query, spans []store.SpanRecord, proj *transform.TraceProjection, scopeIdx *derive.ScopeIndex) []v2Service {
	if proj == nil || scopeIdx == nil {
		return nil
	}

	spanByID := make(map[string]store.SpanRecord, len(spans))
	childSpanIDs := make(map[string][]string, len(spans))
	for _, sp := range spans {
		spanByID[sp.SpanID] = sp
		if sp.ParentSpanID != "" {
			childSpanIDs[sp.ParentSpanID] = append(childSpanIDs[sp.ParentSpanID], sp.SpanID)
		}
	}

	pipelinesByClient := map[string][]v2CLIRun{}
	for _, pipeline := range collectV2CLIRuns(traceStatus, traceID, q, spans, proj, scopeIdx) {
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
				Logs:                  collectV2ServiceLogs(traceID, q, spanByID, childSpanIDs, activities),
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

func collectV2ServiceLogs(
	traceID string,
	q v2Query,
	spanByID map[string]store.SpanRecord,
	childSpanIDs map[string][]string,
	activities []v2ServiceActivity,
) []v2ServiceLog {
	if len(activities) == 0 {
		return nil
	}

	visited := map[string]struct{}{}
	logs := make([]v2ServiceLog, 0)
	logKeys := map[string]struct{}{}
	for _, activity := range activities {
		spanID := serviceActivitySpanID(traceID, activity.CallID)
		if spanID == "" {
			continue
		}
		stack := []string{spanID}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if current == "" {
				continue
			}
			if _, seen := visited[current]; seen {
				continue
			}
			visited[current] = struct{}{}

			sp, ok := spanByID[current]
			if !ok {
				continue
			}
			env, err := decodeV2SpanEnvelope(sp.DataJSON)
			if err == nil {
				for _, line := range serviceSpanLogEntries(sp, env, q.IncludeInternal) {
					key := fmt.Sprintf("%s|%d|%s|%s|%s", line.Kind, line.TimeUnixNano, line.Source, line.Level, line.Message)
					if _, exists := logKeys[key]; exists {
						continue
					}
					logKeys[key] = struct{}{}
					logs = append(logs, line)
				}
			}
			children := childSpanIDs[current]
			for i := len(children) - 1; i >= 0; i-- {
				stack = append(stack, children[i])
			}
		}
	}

	sort.Slice(logs, func(i, j int) bool {
		if logs[i].TimeUnixNano != logs[j].TimeUnixNano {
			return logs[i].TimeUnixNano < logs[j].TimeUnixNano
		}
		if logs[i].SpanID != logs[j].SpanID {
			return logs[i].SpanID < logs[j].SpanID
		}
		return logs[i].ID < logs[j].ID
	})
	return logs
}

func serviceActivitySpanID(traceID, callID string) string {
	prefix := traceID + "/"
	if strings.HasPrefix(callID, prefix) {
		return strings.TrimPrefix(callID, prefix)
	}
	return ""
}

func serviceSpanLogEntries(sp store.SpanRecord, env v2SpanEnvelope, includeInternal bool) []v2ServiceLog {
	internal, _ := getV2Bool(env.Attributes, telemetry.UIInternalAttr)
	lines := make([]v2ServiceLog, 0, 3)
	seenMessages := map[string]struct{}{}

	for idx, event := range env.Events {
		line, ok := serviceEventLogLine(sp, event, idx)
		if !ok {
			continue
		}
		if !includeInternal && internal && line.Level != "error" {
			continue
		}
		seenMessages[line.Message] = struct{}{}
		lines = append(lines, line)
	}

	if msg := strings.TrimSpace(sp.StatusMessage); msg != "" {
		if _, exists := seenMessages[msg]; !exists {
			line := v2ServiceLog{
				ID:           fmt.Sprintf("%s/status", sp.SpanID),
				SpanID:       sp.SpanID,
				TimeUnixNano: serviceLogTime(sp.EndUnixNano, sp.StartUnixNano),
				Level:        serviceStatusLevel(sp.StatusCode),
				Source:       sp.Name,
				Message:      msg,
				Kind:         "status",
			}
			if includeInternal || !internal || line.Level == "error" {
				lines = append(lines, line)
			}
		}
	}

	if !serviceSpanIsDagCall(env.Attributes) && serviceSpanLooksLikeProcessLog(sp.Name) {
		if msg := strings.TrimSpace(sp.Name); msg != "" {
			line := v2ServiceLog{
				ID:           fmt.Sprintf("%s/span", sp.SpanID),
				SpanID:       sp.SpanID,
				TimeUnixNano: serviceLogTime(sp.StartUnixNano, sp.EndUnixNano),
				Level:        "info",
				Source:       serviceSpanLogSource(sp.Name),
				Message:      msg,
				Kind:         "span",
			}
			if includeInternal || !internal {
				lines = append(lines, line)
			}
		}
	}

	return lines
}

func serviceEventLogLine(sp store.SpanRecord, event map[string]any, idx int) (v2ServiceLog, bool) {
	name, _ := getV2String(event, "name")
	if name == "" {
		return v2ServiceLog{}, false
	}
	attrs, _ := event["attributes"].(map[string]any)
	timeUnixNano, _ := event["time_unix_nano"].(float64)
	message := ""
	level := "info"
	switch {
	case name == "exception":
		if text, ok := getV2String(attrs, "exception.message"); ok && strings.TrimSpace(text) != "" {
			message = strings.TrimSpace(text)
		} else if text, ok := getV2String(attrs, "exception.type"); ok && strings.TrimSpace(text) != "" {
			message = strings.TrimSpace(text)
		} else {
			message = "exception"
		}
		level = "error"
	case strings.HasPrefix(strings.ToLower(name), "log"):
		if text, ok := getV2String(attrs, "message"); ok && strings.TrimSpace(text) != "" {
			message = strings.TrimSpace(text)
		} else if text, ok := getV2String(attrs, "log.message"); ok && strings.TrimSpace(text) != "" {
			message = strings.TrimSpace(text)
		}
	}
	if strings.TrimSpace(message) == "" {
		return v2ServiceLog{}, false
	}
	return v2ServiceLog{
		ID:           fmt.Sprintf("%s/event/%d", sp.SpanID, idx),
		SpanID:       sp.SpanID,
		TimeUnixNano: int64(timeUnixNano),
		Level:        level,
		Source:       sp.Name,
		Message:      message,
		Kind:         "event",
	}, true
}

func serviceStatusLevel(statusCode string) string {
	switch strings.TrimSpace(statusCode) {
	case "STATUS_CODE_ERROR", "ERROR":
		return "error"
	default:
		return "info"
	}
}

func serviceSpanLogSource(name string) string {
	if strings.HasPrefix(name, "exec ") {
		return "process"
	}
	return "span"
}

func serviceSpanLooksLikeProcessLog(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "exec ") {
		return true
	}
	if strings.Contains(name, "stdout") || strings.Contains(name, "stderr") {
		return true
	}
	return false
}

func serviceSpanIsDagCall(attrs map[string]any) bool {
	call, ok := getV2String(attrs, telemetry.DagCallAttr)
	return ok && strings.TrimSpace(call) != ""
}

func serviceLogTime(primary, fallback int64) int64 {
	if primary > 0 {
		return primary
	}
	return fallback
}
