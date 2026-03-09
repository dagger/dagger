package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/transform"
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
}

type pipelineObjectEdge struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	FromDagqlID   string `json:"fromDagqlID"`
	ToDagqlID     string `json:"toDagqlID"`
	Label         string `json:"label,omitempty"`
	EvidenceCount int    `json:"evidenceCount,omitempty"`
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

	_, proj, scopeIdx, err := s.loadV2TraceScope(r.Context(), q.TraceID)
	if err != nil {
		http.Error(w, fmt.Sprintf("load trace %s: %v", q.TraceID, err), http.StatusInternalServerError)
		return
	}
	terminal := findProjectionEvent(proj, callID)
	if terminal == nil || terminal.RawKind != "call" {
		http.Error(w, "call not found", http.StatusNotFound)
		return
	}

	callSubtreeIDs := collectProjectionCallSubtreeIDs(proj, callID)
	nodesByID := map[string]*pipelineObjectNode{}
	producedBy := map[string]map[string]struct{}{}
	for _, obj := range proj.Objects {
		for _, st := range obj.StateHistory {
			if st.StateDigest == "" {
				continue
			}
			if _, ok := callSubtreeIDs[st.SpanID]; !ok {
				continue
			}
			if !q.IncludeInternal {
				if event := findProjectionEvent(proj, st.SpanID); event != nil && event.Internal {
					continue
				}
			}
			node := nodesByID[st.StateDigest]
			if node == nil {
				node = &pipelineObjectNode{
					DagqlID:           st.StateDigest,
					TypeName:          obj.TypeName,
					OutputState:       st.OutputStateJSON,
					FirstSeenUnixNano: st.StartUnixNano,
					LastSeenUnixNano:  st.EndUnixNano,
				}
				if node.TypeName == "" {
					node.TypeName = "Object"
				}
				nodesByID[st.StateDigest] = node
			}
			if node.OutputState == nil && st.OutputStateJSON != nil {
				node.OutputState = st.OutputStateJSON
			}
			if typ, ok := getV2String(st.OutputStateJSON, "type"); ok && typ != "" {
				node.TypeName = typ
			}
			if node.FirstSeenUnixNano == 0 || (st.StartUnixNano > 0 && st.StartUnixNano < node.FirstSeenUnixNano) {
				node.FirstSeenUnixNano = st.StartUnixNano
			}
			if st.EndUnixNano > node.LastSeenUnixNano {
				node.LastSeenUnixNano = st.EndUnixNano
			}
			if producedBy[st.StateDigest] == nil {
				producedBy[st.StateDigest] = map[string]struct{}{}
			}
			producedBy[st.StateDigest][spanKey(q.TraceID, st.SpanID)] = struct{}{}
		}
	}

	nodes := make([]pipelineObjectNode, 0, len(nodesByID))
	for dagqlID, node := range nodesByID {
		node.ProducedByCallIDs = setToSortedSlice(producedBy[dagqlID])
		nodes = append(nodes, *node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].FirstSeenUnixNano != nodes[j].FirstSeenUnixNano {
			return nodes[i].FirstSeenUnixNano < nodes[j].FirstSeenUnixNano
		}
		return nodes[i].DagqlID < nodes[j].DagqlID
	})

	edges := make([]pipelineObjectEdge, 0)
	seenEdges := map[string]struct{}{}
	for _, node := range nodes {
		for _, ref := range extractFieldRefs(node.OutputState) {
			if _, ok := nodesByID[ref.DagqlID]; !ok {
				continue
			}
			key := node.DagqlID + "\x00" + ref.DagqlID + "\x00" + ref.Path
			if _, ok := seenEdges[key]; ok {
				continue
			}
			seenEdges[key] = struct{}{}
			edges = append(edges, pipelineObjectEdge{
				ID:            "dep:" + node.DagqlID + "->" + ref.DagqlID + ":" + ref.Path,
				Kind:          "field_ref",
				FromDagqlID:   node.DagqlID,
				ToDagqlID:     ref.DagqlID,
				Label:         ref.Path,
				EvidenceCount: 1,
			})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromDagqlID != edges[j].FromDagqlID {
			return edges[i].FromDagqlID < edges[j].FromDagqlID
		}
		if edges[i].ToDagqlID != edges[j].ToDagqlID {
			return edges[i].ToDagqlID < edges[j].ToDagqlID
		}
		return edges[i].Label < edges[j].Label
	})

	resp := pipelineObjectDAGResponse{
		DerivationVersion: derivationVersionV2,
		Context: pipelineObjectDAGContext{
			TraceID:       q.TraceID,
			CallID:        callID,
			SessionID:     scopeIdx.SessionIDForSpan(callID),
			ClientID:      scopeIdx.ClientIDForSpan(callID),
			OutputDagqlID: terminal.OutputStateDigest,
			OutputType:    terminal.ReturnType,
		},
		Module:   detectPipelineModuleLoad(q.TraceID, proj, scopeIdx, q.IncludeInternal, *terminal, callSubtreeIDs),
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
