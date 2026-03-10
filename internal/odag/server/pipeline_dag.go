package server

import (
	"encoding/base64"
	"encoding/json"
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

type pipelineObjectDAGResponse struct {
	DerivationVersion string                   `json:"derivationVersion"`
	Context           pipelineObjectDAGContext `json:"context"`
	Module            *pipelineModuleLoad      `json:"module,omitempty"`
	Objects           []pipelineObjectNode     `json:"objects"`
	Edges             []pipelineObjectEdge     `json:"edges"`
	Warnings          []string                 `json:"warnings,omitempty"`
}

type pipelineObjectDAGContext struct {
	TraceID       string `json:"traceID"`
	CallID        string `json:"callID"`
	SessionID     string `json:"sessionID,omitempty"`
	ClientID      string `json:"clientID,omitempty"`
	OutputDagqlID string `json:"outputDagqlID,omitempty"`
	OutputType    string `json:"outputType,omitempty"`
}

type pipelineModuleLoad struct {
	Ref     string   `json:"ref,omitempty"`
	SpanID  string   `json:"spanID,omitempty"`
	CallIDs []string `json:"callIDs,omitempty"`
}

type pipelineObjectNode struct {
	DagqlID           string         `json:"dagqlID"`
	TypeName          string         `json:"typeName,omitempty"`
	OutputState       map[string]any `json:"outputState,omitempty"`
	FirstSeenUnixNano int64          `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64          `json:"lastSeenUnixNano"`
	ProducedByCallIDs []string       `json:"producedByCallIDs,omitempty"`
	Role              string         `json:"role,omitempty"`
	Placeholder       bool           `json:"placeholder,omitempty"`
}

type pipelineObjectEdge struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	FromDagqlID   string `json:"fromDagqlID"`
	ToDagqlID     string `json:"toDagqlID"`
	Label         string `json:"label,omitempty"`
	EvidenceCount int    `json:"evidenceCount,omitempty"`
}

type pipelineCallFact struct {
	SpanID           string
	ID               string
	Name             string
	StartUnixNano    int64
	EndUnixNano      int64
	ClientID         string
	SessionID        string
	ParentCallSpanID string
	TopLevel         bool
	CallDepth        int
	ReceiverDagqlID  string
	OutputDagqlID    string
	ReturnType       string
	OutputState      map[string]any
	Internal         bool
}

func (s *Server) handlePipelineObjectDAG(w http.ResponseWriter, r *http.Request) {
	q, err := parseV2Query(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(q.TraceID) == "" {
		http.Error(w, "traceID is required", http.StatusBadRequest)
		return
	}

	callID := normalizePipelineCallID(strings.TrimSpace(r.URL.Query().Get("callID")))
	if callID == "" {
		http.Error(w, "callID is required", http.StatusBadRequest)
		return
	}

	spans, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), q.TraceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("load trace %s: %v", q.TraceID, err), http.StatusInternalServerError)
		return
	}
	traceMeta, err := s.store.GetTrace(r.Context(), q.TraceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("get trace %s: %v", q.TraceID, err), http.StatusInternalServerError)
		return
	}
	terminalEvent := findProjectionEvent(proj, callID)
	if terminalEvent == nil || terminalEvent.RawKind != "call" {
		http.Error(w, "call not found", http.StatusNotFound)
		return
	}

	pipelines := collectV2CLIRuns(traceMeta.Status, q.TraceID, q, spans, proj, scopeIdx)
	pipeline := pipelineByTerminalCallID(pipelines, q.TraceID, callID)

	callFacts, err := buildPipelineCallFacts(q.TraceID, spans, proj, scopeIdx, q.IncludeInternal)
	if err != nil {
		http.Error(w, fmt.Sprintf("decode pipeline facts: %v", err), http.StatusInternalServerError)
		return
	}
	terminal, ok := callFacts[callID]
	if !ok {
		http.Error(w, "call facts not found", http.StatusNotFound)
		return
	}

	callSubtreeIDs := collectProjectionCallSubtreeIDs(proj, callID)
	allowedCallIDs := map[string]struct{}{}
	if pipeline != nil {
		allowedCallIDs = pipelineSpanIDSet(pipeline.CallIDs)
	}
	chain := reconstructPipelineCallChain(callFacts, terminal, allowedCallIDs)
	nodes, edges := buildPipelineObjectGraph(terminal, chain, callFacts, allowedCallIDs, callSubtreeIDs)

	resp := pipelineObjectDAGResponse{
		DerivationVersion: derivationVersionV2,
		Context: pipelineObjectDAGContext{
			TraceID:       q.TraceID,
			CallID:        callID,
			SessionID:     terminal.SessionID,
			ClientID:      terminal.ClientID,
			OutputDagqlID: terminal.OutputDagqlID,
			OutputType:    terminal.ReturnType,
		},
		Module:   detectPipelineModuleLoad(q.TraceID, proj, scopeIdx, q.IncludeInternal, *terminalEvent, callSubtreeIDs),
		Objects:  nodes,
		Edges:    edges,
		Warnings: proj.Warnings,
	}
	writeJSON(w, http.StatusOK, resp)
}

func normalizePipelineCallID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if _, spanID, ok := strings.Cut(raw, "/"); ok && spanID != "" {
		return spanID
	}
	return raw
}

func pipelineByTerminalCallID(items []v2CLIRun, traceID, callID string) *v2CLIRun {
	want := spanKey(traceID, callID)
	for i := range items {
		if items[i].TerminalCallID == want {
			return &items[i]
		}
	}
	return nil
}

func pipelineSpanIDSet(callIDs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(callIDs))
	for _, callID := range callIDs {
		callID = normalizePipelineCallID(callID)
		if callID == "" {
			continue
		}
		out[callID] = struct{}{}
	}
	return out
}

func collectProjectionCallSubtreeIDs(proj *transform.TraceProjection, rootCallID string) map[string]struct{} {
	out := map[string]struct{}{}
	if proj == nil || rootCallID == "" {
		return out
	}
	parentByCallID := map[string]string{}
	for _, event := range proj.Events {
		if event.RawKind != "call" {
			continue
		}
		parentByCallID[event.SpanID] = event.ParentCallSpanID
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
		for candidateID, parentID := range parentByCallID {
			if parentID == callID {
				stack = append(stack, candidateID)
			}
		}
	}
	return out
}

func buildPipelineCallFacts(
	traceID string,
	spans []store.SpanRecord,
	proj *transform.TraceProjection,
	scopeIdx *derive.ScopeIndex,
	includeInternal bool,
) (map[string]*pipelineCallFact, error) {
	if proj == nil || scopeIdx == nil {
		return nil, nil
	}

	spanOutputState, err := pipelineOutputStatesBySpanID(spans)
	if err != nil {
		return nil, err
	}

	facts := make(map[string]*pipelineCallFact)
	for _, event := range proj.Events {
		if event.RawKind != "call" {
			continue
		}
		if !includeInternal && event.Internal {
			continue
		}
		facts[event.SpanID] = &pipelineCallFact{
			SpanID:           event.SpanID,
			ID:               spanKey(traceID, event.SpanID),
			Name:             event.Name,
			StartUnixNano:    event.StartUnixNano,
			EndUnixNano:      event.EndUnixNano,
			ClientID:         scopeIdx.ClientIDForSpan(event.SpanID),
			SessionID:        scopeIdx.SessionIDForSpan(event.SpanID),
			ParentCallSpanID: event.ParentCallSpanID,
			TopLevel:         event.TopLevel,
			CallDepth:        event.CallDepth,
			ReceiverDagqlID:  event.ReceiverStateDigest,
			OutputDagqlID:    event.OutputStateDigest,
			ReturnType:       event.ReturnType,
			OutputState:      spanOutputState[event.SpanID],
			Internal:         event.Internal,
		}
	}
	return facts, nil
}

func pipelineOutputStatesBySpanID(spans []store.SpanRecord) (map[string]map[string]any, error) {
	if len(spans) == 0 {
		return nil, nil
	}

	ordered := append([]store.SpanRecord(nil), spans...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartUnixNano != ordered[j].StartUnixNano {
			return ordered[i].StartUnixNano < ordered[j].StartUnixNano
		}
		if ordered[i].EndUnixNano != ordered[j].EndUnixNano {
			return ordered[i].EndUnixNano < ordered[j].EndUnixNano
		}
		return ordered[i].SpanID < ordered[j].SpanID
	})

	bySpanID := make(map[string]map[string]any, len(ordered))
	byDigest := map[string]map[string]any{}
	for _, sp := range ordered {
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			return nil, err
		}
		outputState, err := decodePipelineOutputStatePayload(env.Attributes)
		if err != nil {
			return nil, err
		}
		dagqlID, _ := getV2String(env.Attributes, telemetry.DagOutputAttr)
		if outputState != nil && dagqlID != "" {
			byDigest[dagqlID] = outputState
		}
		if outputState == nil && dagqlID != "" {
			outputState = byDigest[dagqlID]
		}
		if outputState != nil {
			bySpanID[sp.SpanID] = outputState
		}
	}
	return bySpanID, nil
}

func reconstructPipelineCallChain(
	callFacts map[string]*pipelineCallFact,
	terminal *pipelineCallFact,
	allowedCallIDs map[string]struct{},
) []*pipelineCallFact {
	if terminal == nil {
		return nil
	}

	chain := []*pipelineCallFact{terminal}
	seen := map[string]struct{}{terminal.SpanID: {}}
	current := terminal
	for current != nil && current.ReceiverDagqlID != "" {
		parent := findPipelineReceiverParent(callFacts, terminal.ClientID, current.ReceiverDagqlID, current.StartUnixNano, allowedCallIDs)
		if parent == nil {
			break
		}
		if _, dup := seen[parent.SpanID]; dup {
			break
		}
		chain = append(chain, parent)
		seen[parent.SpanID] = struct{}{}
		current = parent
	}

	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	return chain
}

func findPipelineReceiverParent(
	callFacts map[string]*pipelineCallFact,
	clientID string,
	receiverDagqlID string,
	beforeUnixNano int64,
	allowedCallIDs map[string]struct{},
) *pipelineCallFact {
	var best *pipelineCallFact
	for _, fact := range callFacts {
		if fact == nil || fact.ClientID != clientID || !fact.TopLevel || fact.OutputDagqlID != receiverDagqlID {
			continue
		}
		if len(allowedCallIDs) > 0 {
			if _, ok := allowedCallIDs[fact.SpanID]; !ok {
				continue
			}
		}
		if beforeUnixNano > 0 && fact.StartUnixNano > beforeUnixNano {
			continue
		}
		if best == nil {
			best = fact
			continue
		}
		if fact.StartUnixNano > best.StartUnixNano {
			best = fact
			continue
		}
		if fact.StartUnixNano == best.StartUnixNano && fact.SpanID > best.SpanID {
			best = fact
		}
	}
	return best
}

func buildPipelineObjectGraph(
	terminal *pipelineCallFact,
	chain []*pipelineCallFact,
	callFacts map[string]*pipelineCallFact,
	allowedCallIDs map[string]struct{},
	callSubtreeIDs map[string]struct{},
) ([]pipelineObjectNode, []pipelineObjectEdge) {
	nodesByID := map[string]*pipelineObjectNode{}
	producedBy := map[string]map[string]struct{}{}
	factsByOutput := map[string][]*pipelineCallFact{}
	for _, fact := range callFacts {
		if fact == nil || fact.OutputDagqlID == "" {
			continue
		}
		factsByOutput[fact.OutputDagqlID] = append(factsByOutput[fact.OutputDagqlID], fact)
	}

	chainNodes := make([]*pipelineCallFact, 0, len(chain))
	for _, fact := range chain {
		if !pipelineFactRendersObjectNode(fact, terminal) {
			continue
		}
		node := ensurePipelineObjectNode(nodesByID, fact.OutputDagqlID)
		if node.OutputState == nil && fact.OutputState != nil {
			node.OutputState = fact.OutputState
		}
		if node.TypeName == "" {
			node.TypeName = pipelineNodeTypeName(fact.OutputState, fact.ReturnType)
		}
		node.FirstSeenUnixNano = pipelineMinUnixNano(node.FirstSeenUnixNano, fact.StartUnixNano)
		node.LastSeenUnixNano = pipelineMaxUnixNano(node.LastSeenUnixNano, fact.EndUnixNano)
		if fact.OutputDagqlID == terminal.OutputDagqlID {
			node.Role = "output"
		} else if node.Role == "" {
			node.Role = "chain"
		}
		recordPipelineProducedBy(producedBy, fact.OutputDagqlID, fact.ID)
		chainNodes = append(chainNodes, fact)
	}

	seenEdges := map[string]struct{}{}
	edges := make([]pipelineObjectEdge, 0)
	for i := 1; i < len(chainNodes); i++ {
		from := chainNodes[i-1]
		to := chainNodes[i]
		if from == nil || to == nil || from.OutputDagqlID == "" || to.OutputDagqlID == "" || from.OutputDagqlID == to.OutputDagqlID {
			continue
		}
		key := "chain\x00" + from.OutputDagqlID + "\x00" + to.OutputDagqlID + "\x00" + pipelineCallStepLabel(to.Name)
		if _, ok := seenEdges[key]; ok {
			continue
		}
		seenEdges[key] = struct{}{}
		edges = append(edges, pipelineObjectEdge{
			ID:            "chain:" + from.OutputDagqlID + "->" + to.OutputDagqlID + ":" + pipelineCallStepLabel(to.Name),
			Kind:          "call_chain",
			FromDagqlID:   from.OutputDagqlID,
			ToDagqlID:     to.OutputDagqlID,
			Label:         pipelineCallStepLabel(to.Name),
			EvidenceCount: 1,
		})
	}

	for _, fact := range chainNodes {
		if fact == nil || fact.OutputDagqlID == "" {
			continue
		}
		node := nodesByID[fact.OutputDagqlID]
		if node == nil || node.OutputState == nil {
			continue
		}
		for _, ref := range extractFieldRefs(node.OutputState) {
			if ref.DagqlID == "" {
				continue
			}
			if _, ok := nodesByID[ref.DagqlID]; !ok {
				continue
			}
			key := "ref\x00" + fact.OutputDagqlID + "\x00" + ref.DagqlID + "\x00" + ref.Path
			if _, ok := seenEdges[key]; ok {
				continue
			}
			seenEdges[key] = struct{}{}
			edges = append(edges, pipelineObjectEdge{
				ID:            "dep:" + fact.OutputDagqlID + "->" + ref.DagqlID + ":" + ref.Path,
				Kind:          "field_ref",
				FromDagqlID:   fact.OutputDagqlID,
				ToDagqlID:     ref.DagqlID,
				Label:         ref.Path,
				EvidenceCount: 1,
			})
		}
	}

	if terminal != nil && pipelineTerminalNeedsResultSink(terminal) {
		sinkID := "result:" + terminal.SpanID
		sink := ensurePipelineObjectNode(nodesByID, sinkID)
		sink.Role = "output"
		sink.Placeholder = true
		sink.TypeName = terminal.ReturnType
		sink.FirstSeenUnixNano = terminal.StartUnixNano
		sink.LastSeenUnixNano = terminal.EndUnixNano
		recordPipelineProducedBy(producedBy, sinkID, terminal.ID)
		for i := len(chainNodes) - 1; i >= 0; i-- {
			prev := chainNodes[i]
			if prev == nil || prev.OutputDagqlID == "" || nodesByID[prev.OutputDagqlID] == nil {
				continue
			}
			key := "chain\x00" + prev.OutputDagqlID + "\x00" + sinkID + "\x00" + pipelineCallStepLabel(terminal.Name)
			if _, ok := seenEdges[key]; ok {
				break
			}
			seenEdges[key] = struct{}{}
			edges = append(edges, pipelineObjectEdge{
				ID:            "chain:" + prev.OutputDagqlID + "->" + sinkID + ":" + pipelineCallStepLabel(terminal.Name),
				Kind:          "call_chain",
				FromDagqlID:   prev.OutputDagqlID,
				ToDagqlID:     sinkID,
				Label:         pipelineCallStepLabel(terminal.Name),
				EvidenceCount: 1,
			})
			break
		}
	}

	nodes := make([]pipelineObjectNode, 0, len(nodesByID))
	for dagqlID, node := range nodesByID {
		for _, fact := range factsByOutput[dagqlID] {
			if fact == nil {
				continue
			}
			if len(callSubtreeIDs) > 0 {
				if _, ok := callSubtreeIDs[fact.SpanID]; !ok {
					if _, allowed := allowedCallIDs[fact.SpanID]; !allowed {
						continue
					}
				}
			} else if len(allowedCallIDs) > 0 {
				if _, allowed := allowedCallIDs[fact.SpanID]; !allowed {
					continue
				}
			}
			recordPipelineProducedBy(producedBy, dagqlID, fact.ID)
			node.FirstSeenUnixNano = pipelineMinUnixNano(node.FirstSeenUnixNano, fact.StartUnixNano)
			node.LastSeenUnixNano = pipelineMaxUnixNano(node.LastSeenUnixNano, fact.EndUnixNano)
			if node.OutputState == nil && fact.OutputState != nil {
				node.OutputState = fact.OutputState
			}
			if node.TypeName == "" && (fact.OutputState != nil || fact.ReturnType != "") {
				node.TypeName = pipelineNodeTypeName(fact.OutputState, fact.ReturnType)
			}
		}
		node.ProducedByCallIDs = setToSortedSlice(producedBy[dagqlID])
		if node.TypeName == "" {
			node.TypeName = "Object"
		}
		nodes = append(nodes, *node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].FirstSeenUnixNano != nodes[j].FirstSeenUnixNano {
			return nodes[i].FirstSeenUnixNano < nodes[j].FirstSeenUnixNano
		}
		if nodes[i].Role != nodes[j].Role {
			return nodes[i].Role < nodes[j].Role
		}
		return nodes[i].DagqlID < nodes[j].DagqlID
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromDagqlID != edges[j].FromDagqlID {
			return edges[i].FromDagqlID < edges[j].FromDagqlID
		}
		if edges[i].ToDagqlID != edges[j].ToDagqlID {
			return edges[i].ToDagqlID < edges[j].ToDagqlID
		}
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}
		return edges[i].Label < edges[j].Label
	})
	return nodes, edges
}

func pipelineFactRendersObjectNode(fact, terminal *pipelineCallFact) bool {
	if fact == nil || fact.OutputDagqlID == "" {
		return false
	}
	if terminal != nil && fact.SpanID == terminal.SpanID && pipelineTerminalNeedsResultSink(terminal) {
		return false
	}
	return true
}

func pipelineTerminalNeedsResultSink(terminal *pipelineCallFact) bool {
	if terminal == nil {
		return false
	}
	return pipelineReturnNeedsResultSink(terminal.ReturnType, terminal.OutputState)
}

func pipelineReturnNeedsResultSink(returnType string, outputState map[string]any) bool {
	rawType := strings.TrimSpace(returnType)
	if strings.HasPrefix(rawType, "[]") {
		return true
	}
	normalized := pipelineNormalizeTypeName(rawType)
	if normalized == "" {
		if typ, ok := getV2String(outputState, "type"); ok {
			normalized = pipelineNormalizeTypeName(typ)
		}
	}
	switch normalized {
	case "", "Boolean", "Float", "ID", "Int", "Integer", "JSON", "String", "Void":
		return true
	default:
		return false
	}
}

func ensurePipelineObjectNode(nodesByID map[string]*pipelineObjectNode, dagqlID string) *pipelineObjectNode {
	node := nodesByID[dagqlID]
	if node == nil {
		node = &pipelineObjectNode{DagqlID: dagqlID}
		nodesByID[dagqlID] = node
	}
	return node
}

func recordPipelineProducedBy(producedBy map[string]map[string]struct{}, dagqlID, callID string) {
	if dagqlID == "" || callID == "" {
		return
	}
	if producedBy[dagqlID] == nil {
		producedBy[dagqlID] = map[string]struct{}{}
	}
	producedBy[dagqlID][callID] = struct{}{}
}

func selectPipelineFactForRef(candidates []*pipelineCallFact, callSubtreeIDs map[string]struct{}) *pipelineCallFact {
	var best *pipelineCallFact
	for _, fact := range candidates {
		if fact == nil {
			continue
		}
		if len(callSubtreeIDs) > 0 {
			if _, ok := callSubtreeIDs[fact.SpanID]; !ok && !fact.TopLevel {
				continue
			}
		}
		if best == nil {
			best = fact
			continue
		}
		if fact.OutputState != nil && best.OutputState == nil {
			best = fact
			continue
		}
		if fact.OutputState == nil && best.OutputState != nil {
			continue
		}
		if fact.CallDepth > best.CallDepth {
			best = fact
			continue
		}
		if fact.CallDepth == best.CallDepth && fact.EndUnixNano > best.EndUnixNano {
			best = fact
		}
	}
	return best
}

func pipelineNodeTypeName(outputState map[string]any, fallback string) string {
	if typ, ok := getV2String(outputState, "type"); ok && typ != "" {
		return typ
	}
	if fallback != "" {
		return fallback
	}
	return "Object"
}

func pipelineFieldRefType(state map[string]any, path string) string {
	if state == nil {
		return ""
	}
	fieldsRaw, ok := state["fields"]
	if !ok {
		return ""
	}
	fields, ok := fieldsRaw.(map[string]any)
	if !ok {
		return ""
	}
	fieldRaw, ok := fields[path]
	if !ok {
		return ""
	}
	field, ok := fieldRaw.(map[string]any)
	if !ok {
		return ""
	}
	typ, _ := field["type"].(string)
	return pipelineNormalizeTypeName(typ)
}

func pipelineNormalizeTypeName(raw string) string {
	raw = strings.TrimSpace(raw)
	for strings.HasPrefix(raw, "[]") {
		raw = strings.TrimPrefix(raw, "[]")
	}
	raw = strings.TrimSuffix(raw, "!")
	return raw
}

func pipelineCallStepLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "step"
	}
	if idx := strings.LastIndex(name, "."); idx >= 0 && idx+1 < len(name) {
		return name[idx+1:]
	}
	return name
}

func pipelineMinUnixNano(current, next int64) int64 {
	if current == 0 || (next > 0 && next < current) {
		return next
	}
	return current
}

func pipelineMaxUnixNano(current, next int64) int64 {
	if next > current {
		return next
	}
	return current
}

func decodePipelineOutputStatePayload(attrs map[string]any) (map[string]any, error) {
	payload, ok := getV2String(attrs, telemetry.DagOutputStateAttr)
	if !ok || payload == "" {
		return nil, nil
	}

	version, _ := getV2String(attrs, telemetry.DagOutputStateVersionAttr)
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	switch version {
	case "", telemetry.DagOutputStateVersionV1:
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("json decode: %w", err)
		}
		return out, nil
	case telemetry.DagOutputStateVersionV2:
		var out callpbv1.OutputState
		if err := proto.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("protobuf decode: %w", err)
		}
		return pipelineOutputStateProtoToMap(&out), nil
	default:
		return nil, fmt.Errorf("unsupported output state version: %s", version)
	}
}

func pipelineOutputStateProtoToMap(state *callpbv1.OutputState) map[string]any {
	if state == nil {
		return nil
	}
	fields := make(map[string]any, len(state.GetFields()))
	for _, field := range state.GetFields() {
		if field == nil || field.GetName() == "" {
			continue
		}
		item := map[string]any{
			"name":  field.GetName(),
			"type":  field.GetType(),
			"value": pipelineLiteralToJSON(field.GetValue()),
		}
		if refs := pipelineStringSliceToAny(field.GetRefs()); len(refs) > 0 {
			item["refs"] = refs
		}
		fields[field.GetName()] = item
	}
	return map[string]any{
		"type":   state.GetType(),
		"fields": fields,
	}
}

func pipelineLiteralToJSON(lit *callpbv1.Literal) any {
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
			values = append(values, pipelineLiteralToJSON(item))
		}
		return values
	case *callpbv1.Literal_Object:
		out := make(map[string]any, len(v.Object.GetValues()))
		for _, field := range v.Object.GetValues() {
			if field == nil || field.GetName() == "" {
				continue
			}
			out[field.GetName()] = pipelineLiteralToJSON(field.GetValue())
		}
		return out
	default:
		return nil
	}
}

func pipelineStringSliceToAny(values []string) []any {
	if len(values) == 0 {
		return nil
	}
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func detectPipelineModuleLoad(
	traceID string,
	proj *transform.TraceProjection,
	scopeIdx *derive.ScopeIndex,
	includeInternal bool,
	terminal transform.MutationEvent,
	callSubtreeIDs map[string]struct{},
) *pipelineModuleLoad {
	if proj == nil || scopeIdx == nil {
		return nil
	}
	clientID := scopeIdx.ClientIDForSpan(terminal.SpanID)
	if clientID == "" {
		return nil
	}

	module := &pipelineModuleLoad{}
	callSet := map[string]struct{}{}
	for _, event := range proj.Events {
		if event.SpanID == terminal.SpanID {
			continue
		}
		if !includeInternal && event.Internal {
			continue
		}
		if scopeIdx.ClientIDForSpan(event.SpanID) != clientID {
			continue
		}
		if event.StartUnixNano > terminal.StartUnixNano {
			continue
		}
		if _, inSubtree := callSubtreeIDs[event.SpanID]; inSubtree {
			continue
		}
		if event.RawKind != "call" {
			if strings.HasPrefix(strings.ToLower(event.Name), "load module: ") {
				module.Ref = strings.TrimSpace(strings.TrimPrefix(event.Name, "load module: "))
				module.SpanID = spanKey(traceID, event.SpanID)
			}
			continue
		}
		if isModulePreludeCall(event.Name) {
			callSet[spanKey(traceID, event.SpanID)] = struct{}{}
		}
	}
	module.CallIDs = setToSortedSlice(callSet)
	if module.Ref == "" && len(module.CallIDs) == 0 {
		return nil
	}
	return module
}

func isModulePreludeCall(name string) bool {
	switch strings.TrimSpace(name) {
	case "Query.moduleSource", "ModuleSource.asModule", "Module.serve":
		return true
	default:
		return false
	}
}
