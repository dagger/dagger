package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
	"github.com/dagger/dagger/internal/odag/transform"
	"google.golang.org/protobuf/proto"
)

type v2WorkspaceOp struct {
	ID               string             `json:"id"`
	TraceID          string             `json:"traceID"`
	WorkspaceRoot    string             `json:"workspaceRoot,omitempty"`
	SessionID        string             `json:"sessionID,omitempty"`
	ClientID         string             `json:"clientID,omitempty"`
	RootClientID     string             `json:"rootClientID,omitempty"`
	DeviceID         string             `json:"deviceID,omitempty"`
	DeviceMachineID  string             `json:"deviceMachineID,omitempty"`
	SpanID           string             `json:"spanID,omitempty"`
	Name             string             `json:"name"`
	Kind             string             `json:"kind"`
	Direction        string             `json:"direction"`
	CallName         string             `json:"callName"`
	Path             string             `json:"path,omitempty"`
	TargetType       string             `json:"targetType,omitempty"`
	Status           string             `json:"status"`
	StatusCode       string             `json:"statusCode,omitempty"`
	StartUnixNano    int64              `json:"startUnixNano"`
	EndUnixNano      int64              `json:"endUnixNano"`
	ReceiverDagqlID  string             `json:"receiverDagqlID,omitempty"`
	OutputDagqlID    string             `json:"outputDagqlID,omitempty"`
	PipelineClientID string             `json:"pipelineClientID,omitempty"`
	PipelineID       string             `json:"pipelineID,omitempty"`
	PipelineCommand  string             `json:"pipelineCommand,omitempty"`
	Evidence         []v2EntityEvidence `json:"evidence,omitempty"`
	Relations        []v2EntityRelation `json:"relations,omitempty"`
}

type workspaceOpCallPayload struct {
	Call *callpbv1.Call
}

func (s *Server) handleV2WorkspaceOps(w http.ResponseWriter, r *http.Request) {
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

	items := make([]v2WorkspaceOp, 0)
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
		items = append(items, collectV2WorkspaceOps(traceMeta.Status, traceID, q, spans, proj, scopeIdx)...)
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

func collectV2WorkspaceOps(
	traceStatus string,
	traceID string,
	q v2Query,
	spans []store.SpanRecord,
	proj *transform.TraceProjection,
	scopeIdx *derive.ScopeIndex,
) []v2WorkspaceOp {
	if proj == nil || scopeIdx == nil {
		return nil
	}

	callPayloads, err := decodeWorkspaceOpCallPayloads(spans)
	if err != nil {
		return nil
	}

	pipelinesByClient := map[string][]v2CLIRun{}
	for _, pipeline := range collectV2CLIRuns(traceStatus, traceID, q, spans, proj, scopeIdx) {
		if pipeline.ClientID == "" {
			continue
		}
		pipelinesByClient[pipeline.ClientID] = append(pipelinesByClient[pipeline.ClientID], pipeline)
	}

	items := make([]v2WorkspaceOp, 0)
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
		rootClientID := workspaceOpRootClientID(scopeIdx, sessionID, clientID)
		deviceMachineID := workspaceOpDeviceMachineID(scopeIdx, rootClientID, clientID)
		deviceID := workspaceOpDeviceID(deviceMachineID)

		kind, direction, targetType := classifyWorkspaceOpCall(event.Name)
		if kind == "" {
			continue
		}

		callPayload := callPayloads[event.SpanID]
		path := workspaceOpTarget(event.Name, callPayload)
		pipeline := workspaceOpPipelineForEvent(pipelinesByClient[clientID], event)
		status := deriveV2WorkspaceOpStatus(traceStatus, event.StatusCode)
		name := workspaceOpDisplayName(kind, targetType, path)

		item := v2WorkspaceOp{
			ID:              "workspace-op:" + traceID + "/" + event.SpanID,
			TraceID:         traceID,
			SessionID:       sessionID,
			ClientID:        clientID,
			RootClientID:    rootClientID,
			DeviceID:        deviceID,
			DeviceMachineID: deviceMachineID,
			SpanID:          event.SpanID,
			Name:            name,
			Kind:            kind,
			Direction:       direction,
			CallName:        event.Name,
			Path:            path,
			TargetType:      targetType,
			Status:          status,
			StatusCode:      event.StatusCode,
			StartUnixNano:   event.StartUnixNano,
			EndUnixNano:     event.EndUnixNano,
			ReceiverDagqlID: event.ReceiverStateDigest,
			OutputDagqlID:   event.OutputStateDigest,
			Evidence:        buildV2WorkspaceOpEvidence(event.Name, path, direction, pipeline),
			Relations:       buildV2WorkspaceOpRelations(sessionID, clientID, rootClientID, deviceID, pipeline, event),
		}
		if pipeline != nil {
			item.PipelineID = pipeline.ID
			item.PipelineClientID = pipeline.ClientID
			item.PipelineCommand = pipeline.Command
		}
		items = append(items, item)
	}

	return items
}

func classifyWorkspaceOpCall(name string) (kind string, direction string, targetType string) {
	switch strings.TrimSpace(name) {
	case "Host.directory":
		return "host-directory", "read", "Directory"
	case "Host.file":
		return "host-file", "read", "File"
	case "Host.socket", "Host.unixSocket":
		return "host-socket", "read", "Socket"
	case "Host.service":
		return "host-service", "read", "Service"
	case "Host.tunnel":
		return "host-tunnel", "write", "Service"
	case "Directory.export":
		return "directory-export", "write", "Directory"
	case "File.export":
		return "file-export", "write", "File"
	case "Container.export":
		return "container-export", "write", "Container"
	default:
		return "", "", ""
	}
}

func traceContainsWorkspaceOpSpans(spans []store.SpanRecord) bool {
	for _, sp := range spans {
		if kind, _, _ := classifyWorkspaceOpCall(sp.Name); kind != "" {
			return true
		}
	}
	return false
}

func decodeWorkspaceOpCallPayloads(spans []store.SpanRecord) (map[string]*workspaceOpCallPayload, error) {
	out := make(map[string]*workspaceOpCallPayload, len(spans))
	for _, sp := range spans {
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			return nil, err
		}
		payload, _ := getV2String(env.Attributes, telemetry.DagCallAttr)
		if payload == "" {
			continue
		}
		call, err := decodeWorkspaceOpCallPayload(payload)
		if err != nil {
			return nil, err
		}
		out[sp.SpanID] = &workspaceOpCallPayload{Call: call}
	}
	return out, nil
}

func decodeWorkspaceOpCallPayload(payload string) (*callpbv1.Call, error) {
	callBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	var call callpbv1.Call
	if err := proto.Unmarshal(callBytes, &call); err != nil {
		return nil, fmt.Errorf("protobuf decode: %w", err)
	}
	return &call, nil
}

func workspaceOpTarget(callName string, payload *workspaceOpCallPayload) string {
	if payload == nil || payload.Call == nil {
		return ""
	}
	switch strings.TrimSpace(callName) {
	case "Host.service":
		if label := workspaceOpHostServiceTarget(payload.Call); label != "" {
			return label
		}
	case "Host.tunnel":
		if label := serviceTunnelDisplayName(nil, payload.Call); label != "" {
			return label
		}
		return "host -> service"
	}
	for _, name := range []string{"path", "dest", "target"} {
		if value := workspaceOpCallArgString(payload.Call, name); value != "" {
			return value
		}
	}
	return ""
}

func workspaceOpRootClientID(scopeIdx *derive.ScopeIndex, sessionID, clientID string) string {
	if scopeIdx == nil {
		return ""
	}
	if rootClientID := strings.TrimSpace(scopeIdx.RootClientByID[clientID]); rootClientID != "" {
		return rootClientID
	}
	if sessionID != "" {
		if session, ok := scopeIdx.SessionByID[sessionID]; ok && strings.TrimSpace(session.RootClientID) != "" {
			return session.RootClientID
		}
	}
	return strings.TrimSpace(clientID)
}

func workspaceOpDeviceMachineID(scopeIdx *derive.ScopeIndex, rootClientID, clientID string) string {
	if scopeIdx == nil {
		return ""
	}
	for _, candidate := range []string{rootClientID, clientID} {
		if candidate == "" {
			continue
		}
		if client, ok := scopeIdx.ClientByID[candidate]; ok {
			if machineID := strings.TrimSpace(client.ClientMachineID); machineID != "" {
				return machineID
			}
		}
	}
	return ""
}

func workspaceOpDeviceID(machineID string) string {
	machineID = strings.TrimSpace(machineID)
	if machineID == "" {
		return ""
	}
	return "device:" + machineID
}

func workspaceOpHostServiceTarget(call *callpbv1.Call) string {
	if call == nil {
		return ""
	}
	host := workspaceOpCallArgString(call, "host")
	if host == "" {
		host = "localhost"
	}
	value, ok := serviceCallArgValue(call, "ports")
	if !ok {
		return host
	}
	items, ok := value.([]any)
	if !ok {
		return host
	}
	ports := serviceTunnelPortsFromAny(items)
	if len(ports) == 0 {
		return host
	}
	parts := make([]string, 0, min(len(ports), 3))
	limit := len(ports)
	if limit > 2 {
		limit = 2
	}
	for _, port := range ports[:limit] {
		parts = append(parts, workspaceOpHostServicePortLabel(host, port))
	}
	if len(ports) > limit {
		parts = append(parts, fmt.Sprintf("+%d more", len(ports)-limit))
	}
	return strings.Join(parts, ", ")
}

func workspaceOpHostServicePortLabel(host string, port serviceTunnelPort) string {
	left := "service"
	if port.Frontend > 0 {
		left = fmt.Sprintf("service:%d", port.Frontend)
	}
	right := host
	if port.Backend > 0 {
		right = fmt.Sprintf("%s:%d", host, port.Backend)
	}
	if protocol := strings.TrimSpace(port.Protocol); protocol != "" {
		return strings.ToLower(protocol) + " " + left + " -> " + right
	}
	return left + " -> " + right
}

func workspaceOpCallArgString(call *callpbv1.Call, names ...string) string {
	if call == nil || len(names) == 0 {
		return ""
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
		if value, ok := workspaceOpLiteralString(arg.GetValue()); ok {
			return value
		}
	}
	return ""
}

func workspaceOpLiteralString(lit *callpbv1.Literal) (string, bool) {
	if lit == nil {
		return "", false
	}
	switch v := lit.GetValue().(type) {
	case *callpbv1.Literal_String_:
		return v.String_, true
	case *callpbv1.Literal_Enum:
		return v.Enum, true
	default:
		return "", false
	}
}

func workspaceOpPipelineForEvent(pipelines []v2CLIRun, event transform.MutationEvent) *v2CLIRun {
	for i := range pipelines {
		pipeline := &pipelines[i]
		if pipeline.StartUnixNano > 0 && event.StartUnixNano > 0 && event.StartUnixNano < pipeline.StartUnixNano {
			continue
		}
		if pipeline.EndUnixNano > 0 && event.EndUnixNano > pipeline.EndUnixNano {
			continue
		}
		return pipeline
	}
	return nil
}

func deriveV2WorkspaceOpStatus(traceStatus, statusCode string) string {
	if statusCode != "" && statusCode != "STATUS_CODE_OK" && statusCode != "OK" {
		return "failed"
	}
	if traceStatus == "ingesting" {
		return "running"
	}
	return "ready"
}

func workspaceOpDisplayName(kind, targetType, path string) string {
	base := targetType
	if base == "" {
		base = "Workspace op"
	}
	if path == "" {
		return strings.ReplaceAll(kind, "-", " ")
	}
	return base + " " + path
}

func buildV2WorkspaceOpEvidence(callName, path, direction string, pipeline *v2CLIRun) []v2EntityEvidence {
	evidence := []v2EntityEvidence{
		{
			Kind:       "Workspace call",
			Confidence: "high",
			Source:     callName,
			Note:       strings.TrimSpace(direction + " access"),
		},
	}
	if path != "" {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Path argument",
			Confidence: "high",
			Source:     "call payload",
			Note:       path,
		})
	}
	if pipeline != nil {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Attached pipeline",
			Confidence: "medium",
			Source:     pipeline.Command,
			Note:       pipeline.Name,
		})
	}
	return evidence
}

func buildV2WorkspaceOpRelations(sessionID, clientID, rootClientID, deviceID string, pipeline *v2CLIRun, event transform.MutationEvent) []v2EntityRelation {
	relations := make([]v2EntityRelation, 0, 7)
	if sessionID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "belongs-to",
			Target:     sessionID,
			TargetKind: "session",
		})
	}
	if clientID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "owned-by",
			Target:     clientID,
			TargetKind: "client",
		})
	}
	if rootClientID != "" && rootClientID != clientID {
		relations = append(relations, v2EntityRelation{
			Relation:   "rooted-at",
			Target:     rootClientID,
			TargetKind: "client",
		})
	}
	if deviceID != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "originates-from",
			Target:     deviceID,
			TargetKind: "device",
		})
	}
	if pipeline != nil {
		relations = append(relations, v2EntityRelation{
			Relation:   "attached-to",
			Target:     pipeline.ID,
			TargetKind: "pipeline",
			Note:       pipeline.Command,
		})
	}
	if event.ReceiverStateDigest != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "reads-from",
			Target:     event.ReceiverStateDigest,
			TargetKind: "dagql-state",
		})
	}
	if event.OutputStateDigest != "" {
		relations = append(relations, v2EntityRelation{
			Relation:   "returns",
			Target:     event.OutputStateDigest,
			TargetKind: "dagql-state",
		})
	}
	return relations
}
