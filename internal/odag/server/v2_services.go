package server

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
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
	ProducerLabel         string              `json:"producerLabel,omitempty"`
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
	PipelineName          string              `json:"pipelineName,omitempty"`
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
	callEventsBySpanID := make(map[string]transform.MutationEvent, len(proj.Events))
	callEventsByOutputState := make(map[string][]transform.MutationEvent, len(proj.Events))
	for _, sp := range spans {
		spanByID[sp.SpanID] = sp
		if sp.ParentSpanID != "" {
			childSpanIDs[sp.ParentSpanID] = append(childSpanIDs[sp.ParentSpanID], sp.SpanID)
		}
	}
	for _, event := range proj.Events {
		if event.RawKind != "call" {
			continue
		}
		callEventsBySpanID[event.SpanID] = event
		if event.OutputStateDigest != "" {
			callEventsByOutputState[event.OutputStateDigest] = append(callEventsByOutputState[event.OutputStateDigest], event)
		}
	}
	linkedExecSpanIDs := serviceLinkedExecSpanIDs(traceID, spans)

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
			producerSpanID := serviceActivitySpanID(traceID, createdByCallID)
			producerSpan, hasProducerSpan := spanByID[producerSpanID]

			containerDagqlID := serviceRefForPath(st.OutputStateJSON, "Container")
			tunnelUpstreamDagqlID := serviceRefForPath(st.OutputStateJSON, "TunnelUpstream")
			customHostname := serviceFieldString(st.OutputStateJSON, "CustomHostname")
			imageRef := serviceContainerImageRef(st.OutputStateJSON)
			kind := classifyV2ServiceKind(st.OutputStateJSON, containerDagqlID, tunnelUpstreamDagqlID, createdByCallName)
			name := serviceDisplayName(customHostname, imageRef, kind, st.StateDigest, st.OutputStateJSON, producerSpan, hasProducerSpan, spanByID, childSpanIDs, linkedExecSpanIDs, activities, traceID)
			lastActivityUnixNano := serviceLastActivity(st.EndUnixNano, activities)
			status := deriveV2ServiceStatus(traceStatus, activities)
			pipeline := servicePipelineForActivities(pipelinesByClient, callEventsByID, activities)

			item := v2Service{
				ID:                    "service:" + traceID + "/" + st.StateDigest,
				TraceID:               traceID,
				SessionID:             sessionID,
				ClientID:              clientID,
				Name:                  name,
				ProducerLabel:         serviceProducerChainLabel(producerSpanID, callEventsBySpanID, callEventsByOutputState),
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
				item.PipelineName = pipeline.Name
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

func classifyV2ServiceKind(outputState map[string]any, containerDagqlID, tunnelUpstreamDagqlID, createdByCallName string) string {
	hostSockets, _ := serviceFieldMap(outputState, "HostSockets")["value"].([]any)
	tunnelPorts, _ := serviceFieldMap(outputState, "TunnelPorts")["value"].([]any)
	switch {
	case len(hostSockets) > 0:
		return "socket"
	case tunnelUpstreamDagqlID != "" || len(tunnelPorts) > 0 || strings.TrimSpace(createdByCallName) == "Host.tunnel":
		return "tunnel"
	case containerDagqlID != "" || strings.TrimSpace(createdByCallName) == "Container.asService":
		return "container"
	default:
		return "service"
	}
}

func serviceDisplayName(
	customHostname, imageRef, kind, dagqlID string,
	outputState map[string]any,
	producerSpan store.SpanRecord,
	hasProducerSpan bool,
	spanByID map[string]store.SpanRecord,
	childSpanIDs map[string][]string,
	linkedExecSpanIDs map[string][]string,
	activities []v2ServiceActivity,
	traceID string,
) string {
	var producerCall *callpbv1.Call
	if hasProducerSpan {
		producerCall = serviceSpanCallPayload(producerSpan)
	}
	switch kind {
	case "tunnel":
		if label := serviceTunnelDisplayName(outputState, producerCall); label != "" {
			return label
		}
	case "container":
		if label := serviceContainerDisplayName(producerCall, spanByID, childSpanIDs, linkedExecSpanIDs, activities, traceID); label != "" {
			if hasProducerSpan && serviceSpanHasAncestorName(spanByID, producerSpan.SpanID, "Container.terminal") {
				return "Terminal " + label
			}
			return label
		}
	}
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

func serviceContainerDisplayName(
	producerCall *callpbv1.Call,
	spanByID map[string]store.SpanRecord,
	childSpanIDs map[string][]string,
	linkedExecSpanIDs map[string][]string,
	activities []v2ServiceActivity,
	traceID string,
) string {
	if args := serviceCallArgStrings(producerCall, "args"); len(args) > 0 {
		if serviceCallArgBool(producerCall, "useEntrypoint") {
			if label := serviceExecDisplayName(spanByID, childSpanIDs, linkedExecSpanIDs, activities, traceID); label != "" {
				return label
			}
		}
		return strings.TrimSpace(strings.Join(args, " "))
	}
	return serviceExecDisplayName(spanByID, childSpanIDs, linkedExecSpanIDs, activities, traceID)
}

func serviceTunnelDisplayName(outputState map[string]any, producerCall *callpbv1.Call) string {
	ports := serviceTunnelPorts(outputState, producerCall)
	if len(ports) > 0 {
		parts := make([]string, 0, min(len(ports), 3))
		limit := len(ports)
		if limit > 2 {
			limit = 2
		}
		for _, port := range ports[:limit] {
			parts = append(parts, serviceTunnelPortLabel(port))
		}
		if len(ports) > limit {
			parts = append(parts, fmt.Sprintf("+%d more", len(ports)-limit))
		}
		return strings.Join(parts, ", ")
	}
	if serviceCallArgBool(producerCall, "native") {
		return "host:native -> service"
	}
	return ""
}

type serviceTunnelPort struct {
	Frontend int64
	Backend  int64
	Protocol string
}

func serviceTunnelPorts(outputState map[string]any, producerCall *callpbv1.Call) []serviceTunnelPort {
	if items := serviceFieldList(outputState, "TunnelPorts"); len(items) > 0 {
		return serviceTunnelPortsFromAny(items)
	}
	if value, ok := serviceCallArgValue(producerCall, "ports"); ok {
		if items, ok := value.([]any); ok {
			return serviceTunnelPortsFromAny(items)
		}
	}
	return nil
}

func serviceTunnelPortsFromAny(items []any) []serviceTunnelPort {
	ports := make([]serviceTunnelPort, 0, len(items))
	for _, item := range items {
		value, ok := item.(map[string]any)
		if !ok {
			continue
		}
		port := serviceTunnelPort{
			Frontend: serviceMapInt(value, "Frontend", "frontend"),
			Backend:  serviceMapInt(value, "Backend", "backend"),
			Protocol: serviceMapString(value, "Protocol", "protocol"),
		}
		if port.Frontend == 0 && port.Backend == 0 && port.Protocol == "" {
			continue
		}
		ports = append(ports, port)
	}
	return ports
}

func serviceTunnelPortLabel(port serviceTunnelPort) string {
	left := "host:*"
	if port.Frontend > 0 {
		left = fmt.Sprintf("host:%d", port.Frontend)
	}
	right := "service"
	if port.Backend > 0 {
		right = fmt.Sprintf("service:%d", port.Backend)
	}
	if protocol := strings.TrimSpace(port.Protocol); protocol != "" {
		return strings.ToLower(protocol) + " " + left + " -> " + right
	}
	return left + " -> " + right
}

func serviceExecDisplayName(
	spanByID map[string]store.SpanRecord,
	childSpanIDs map[string][]string,
	linkedExecSpanIDs map[string][]string,
	activities []v2ServiceActivity,
	traceID string,
) string {
	rolePriority := map[string]int{
		"lifecycle": 0,
		"producer":  1,
		"consumer":  2,
	}
	ordered := make([]v2ServiceActivity, 0, len(activities))
	ordered = append(ordered, activities...)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, okI := rolePriority[ordered[i].Role]
		pj, okJ := rolePriority[ordered[j].Role]
		if okI != okJ {
			return okI
		}
		if okI && pj != pi {
			return pi < pj
		}
		if ordered[i].StartUnixNano != ordered[j].StartUnixNano {
			return ordered[i].StartUnixNano > ordered[j].StartUnixNano
		}
		return ordered[i].CallID < ordered[j].CallID
	})

	bootstrapBest := ""
	for _, activity := range ordered {
		spanID := serviceActivitySpanID(traceID, activity.CallID)
		if spanID == "" {
			continue
		}
		label, bootstrap := serviceExecDisplayNameForSpan(spanByID, childSpanIDs, linkedExecSpanIDs, spanID)
		if label == "" {
			continue
		}
		if !bootstrap {
			return label
		}
		if bootstrapBest == "" {
			bootstrapBest = label
		}
	}
	return bootstrapBest
}

func serviceExecDisplayNameForSpan(
	spanByID map[string]store.SpanRecord,
	childSpanIDs map[string][]string,
	linkedExecSpanIDs map[string][]string,
	rootSpanID string,
) (string, bool) {
	type candidate struct {
		label         string
		bootstrap     bool
		startUnixNano int64
	}

	var best *candidate
	consider := func(label string, bootstrap bool, startUnixNano int64) {
		if label == "" {
			return
		}
		if best == nil ||
			(best.bootstrap && !bootstrap) ||
			(best.bootstrap == bootstrap && startUnixNano > best.startUnixNano) {
			copy := candidate{label: label, bootstrap: bootstrap, startUnixNano: startUnixNano}
			best = &copy
		}
	}

	visited := map[string]struct{}{}
	seenLinkedExecs := map[string]struct{}{}
	stack := []string{rootSpanID}
	for len(stack) > 0 {
		spanID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := visited[spanID]; seen {
			continue
		}
		visited[spanID] = struct{}{}

		sp, ok := spanByID[spanID]
		if !ok {
			continue
		}
		children := childSpanIDs[spanID]
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, children[i])
		}

		label, bootstrap := serviceExecSpanName(sp.Name)
		consider(label, bootstrap, sp.StartUnixNano)
		for _, linkedSpanID := range linkedExecSpanIDs[spanID] {
			if _, seen := seenLinkedExecs[linkedSpanID]; seen {
				continue
			}
			seenLinkedExecs[linkedSpanID] = struct{}{}
			linkedSpan, ok := spanByID[linkedSpanID]
			if !ok {
				continue
			}
			label, bootstrap := serviceExecSpanName(linkedSpan.Name)
			consider(label, bootstrap, linkedSpan.StartUnixNano)
		}
	}
	if best == nil {
		return "", false
	}
	return best.label, best.bootstrap
}

func serviceLinkedExecSpanIDs(traceID string, spans []store.SpanRecord) map[string][]string {
	index := make(map[string][]string)
	for _, sp := range spans {
		if label, _ := serviceExecSpanName(sp.Name); label == "" {
			continue
		}
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil || len(env.Links) == 0 {
			continue
		}
		for _, link := range env.Links {
			spanID := serviceLinkedSpanID(traceID, link)
			if spanID == "" {
				continue
			}
			index[spanID] = append(index[spanID], sp.SpanID)
		}
	}
	return index
}

func serviceLinkedSpanID(traceID string, link map[string]any) string {
	linkedTraceID, _ := link["trace_id"].(string)
	if strings.TrimSpace(linkedTraceID) != "" && linkedTraceID != traceID {
		return ""
	}
	spanID, _ := link["span_id"].(string)
	return strings.TrimSpace(spanID)
}

func serviceProducerChainLabel(
	producerSpanID string,
	callEventsBySpanID map[string]transform.MutationEvent,
	callEventsByOutputState map[string][]transform.MutationEvent,
) string {
	event, ok := callEventsBySpanID[producerSpanID]
	if !ok {
		return ""
	}
	event = serviceProducerChainSeed(event, callEventsBySpanID)

	labels := make([]string, 0, 4)
	if label := serviceProducerEventLabel(event); label != "" {
		labels = append(labels, label)
	}

	visitedStates := map[string]struct{}{}
	for stateDigest := strings.TrimSpace(event.ReceiverStateDigest); stateDigest != ""; {
		if _, seen := visitedStates[stateDigest]; seen {
			break
		}
		visitedStates[stateDigest] = struct{}{}

		producer, ok := serviceBestStateProducer(callEventsByOutputState[stateDigest])
		if !ok {
			break
		}
		if label := serviceProducerEventLabel(producer); label != "" {
			labels = append(labels, label)
		}
		stateDigest = strings.TrimSpace(producer.ReceiverStateDigest)
	}

	if len(labels) == 0 {
		return ""
	}
	slices.Reverse(labels)
	labels = serviceCompactProducerLabels(labels)
	if len(labels) == 0 {
		return ""
	}
	return strings.Join(labels, " ")
}

func serviceProducerChainSeed(
	event transform.MutationEvent,
	callEventsBySpanID map[string]transform.MutationEvent,
) transform.MutationEvent {
	if event.TopLevel {
		return event
	}

	visitedSpanIDs := map[string]struct{}{event.SpanID: {}}
	for parentSpanID := strings.TrimSpace(event.ParentCallSpanID); parentSpanID != ""; {
		parent, ok := callEventsBySpanID[parentSpanID]
		if !ok {
			break
		}
		if _, seen := visitedSpanIDs[parent.SpanID]; seen {
			break
		}
		visitedSpanIDs[parent.SpanID] = struct{}{}
		event = parent
		if event.TopLevel {
			break
		}
		parentSpanID = strings.TrimSpace(event.ParentCallSpanID)
	}
	return event
}

func serviceBestStateProducer(events []transform.MutationEvent) (transform.MutationEvent, bool) {
	if len(events) == 0 {
		return transform.MutationEvent{}, false
	}

	best := events[0]
	bestScore := serviceProducerEventScore(best)
	for _, event := range events[1:] {
		score := serviceProducerEventScore(event)
		if score > bestScore {
			best = event
			bestScore = score
			continue
		}
		if score < bestScore {
			continue
		}
		if event.StartUnixNano > best.StartUnixNano {
			best = event
			bestScore = score
			continue
		}
		if event.StartUnixNano == best.StartUnixNano && event.SpanID > best.SpanID {
			best = event
			bestScore = score
		}
	}
	return best, true
}

func serviceProducerEventScore(event transform.MutationEvent) int {
	score := 0
	if event.TopLevel {
		score += 4
	}
	if !event.Internal {
		score += 2
	}
	if !strings.HasPrefix(strings.TrimSpace(event.Name), "Query.") {
		score++
	}
	return score
}

func serviceProducerEventLabel(event transform.MutationEvent) string {
	name := strings.TrimSpace(event.Name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "Query.") {
		return ""
	}
	if serviceProducerBuilderName(name) {
		return ""
	}
	if !event.TopLevel {
		return ""
	}
	field := name
	if idx := strings.LastIndex(field, "."); idx >= 0 && idx+1 < len(field) {
		field = field[idx+1:]
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	return serviceProducerFieldLabel(field)
}

func serviceProducerBuilderName(name string) bool {
	switch strings.TrimSpace(name) {
	case "Container.asService", "Host.tunnel":
		return true
	default:
		return false
	}
}

func serviceCompactProducerLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == label {
			continue
		}
		out = append(out, label)
	}
	return out
}

func serviceProducerFieldLabel(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}

	var out strings.Builder
	prevLower := false
	prevUpper := false
	for i, r := range field {
		switch {
		case r == '_' || r == '-' || r == ' ':
			if out.Len() > 0 && out.String()[out.Len()-1] != '-' {
				out.WriteByte('-')
			}
			prevLower = false
			prevUpper = false
			continue
		case 'A' <= r && r <= 'Z':
			nextLower := false
			if i+1 < len(field) {
				next := rune(field[i+1])
				nextLower = 'a' <= next && next <= 'z'
			}
			if out.Len() > 0 && (prevLower || (prevUpper && nextLower)) && out.String()[out.Len()-1] != '-' {
				out.WriteByte('-')
			}
			out.WriteRune(r + ('a' - 'A'))
			prevLower = false
			prevUpper = true
		default:
			out.WriteRune(r)
			prevLower = 'a' <= r && r <= 'z'
			prevUpper = false
		}
	}
	return strings.Trim(out.String(), "-")
}

func serviceExecSpanName(name string) (string, bool) {
	raw := strings.TrimSpace(name)
	if !strings.HasPrefix(strings.ToLower(raw), "exec ") {
		return "", false
	}
	label := strings.TrimSpace(raw[5:])
	if label == "" {
		return "", false
	}
	bootstrap := strings.HasPrefix(strings.ToLower(label), "dagger-entrypoint.sh ")
	return label, bootstrap
}

func serviceSpanCallPayload(sp store.SpanRecord) *callpbv1.Call {
	env, err := decodeV2SpanEnvelope(sp.DataJSON)
	if err != nil {
		return nil
	}
	payload, ok := getV2String(env.Attributes, telemetry.DagCallAttr)
	if !ok || strings.TrimSpace(payload) == "" {
		return nil
	}
	call, err := decodeWorkspaceOpCallPayload(payload)
	if err != nil {
		return nil
	}
	return call
}

func serviceSpanHasAncestorName(spanByID map[string]store.SpanRecord, spanID, ancestorName string) bool {
	needle := strings.TrimSpace(ancestorName)
	for parentID := spanByID[spanID].ParentSpanID; parentID != ""; {
		parent, ok := spanByID[parentID]
		if !ok {
			return false
		}
		if strings.TrimSpace(parent.Name) == needle {
			return true
		}
		parentID = parent.ParentSpanID
	}
	return false
}

func serviceCallArgStrings(call *callpbv1.Call, names ...string) []string {
	value, ok := serviceCallArgValue(call, names...)
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func serviceCallArgBool(call *callpbv1.Call, names ...string) bool {
	value, ok := serviceCallArgValue(call, names...)
	if !ok {
		return false
	}
	flag, _ := value.(bool)
	return flag
}

func serviceCallArgValue(call *callpbv1.Call, names ...string) (any, bool) {
	if call == nil || len(names) == 0 {
		return nil, false
	}
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[strings.TrimSpace(name)] = struct{}{}
	}
	for _, arg := range call.GetArgs() {
		if arg == nil {
			continue
		}
		if _, ok := nameSet[arg.GetName()]; !ok {
			continue
		}
		return serviceLiteralToJSON(arg.GetValue()), true
	}
	return nil, false
}

func serviceLiteralToJSON(lit *callpbv1.Literal) any {
	if lit == nil {
		return nil
	}
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_CallDigest:
		return v.CallDigest
	case *callpbv1.Literal_Null:
		return nil
	case *callpbv1.Literal_Bool:
		return v.Bool
	case *callpbv1.Literal_Enum:
		return v.Enum
	case *callpbv1.Literal_Int:
		return v.Int
	case *callpbv1.Literal_Float:
		return v.Float
	case *callpbv1.Literal_String_:
		return v.String_
	case *callpbv1.Literal_List:
		values := make([]any, 0, len(v.List.GetValues()))
		for _, item := range v.List.GetValues() {
			values = append(values, serviceLiteralToJSON(item))
		}
		return values
	case *callpbv1.Literal_Object:
		out := make(map[string]any, len(v.Object.GetValues()))
		for _, item := range v.Object.GetValues() {
			if item == nil || item.GetName() == "" {
				continue
			}
			out[item.GetName()] = serviceLiteralToJSON(item.GetValue())
		}
		return out
	default:
		return nil
	}
}

func serviceFieldList(outputState map[string]any, name string) []any {
	field := serviceFieldMap(outputState, name)
	items, _ := field["value"].([]any)
	return items
}

func serviceMapString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func serviceMapInt(value map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch raw := value[key].(type) {
		case int:
			return int64(raw)
		case int64:
			return raw
		case float64:
			return int64(raw)
		}
	}
	return 0
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
