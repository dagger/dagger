package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

type v2Workspace struct {
	ID                string             `json:"id"`
	Root              string             `json:"root"`
	Name              string             `json:"name"`
	FirstSeenUnixNano int64              `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64              `json:"lastSeenUnixNano"`
	OpCount           int                `json:"opCount"`
	ReadCount         int                `json:"readCount"`
	WriteCount        int                `json:"writeCount"`
	SessionCount      int                `json:"sessionCount"`
	TraceCount        int                `json:"traceCount"`
	PipelineCount     int                `json:"pipelineCount"`
	Ops               []v2WorkspaceOp    `json:"ops,omitempty"`
	Evidence          []v2EntityEvidence `json:"evidence,omitempty"`
	Relations         []v2EntityRelation `json:"relations,omitempty"`
}

type workspaceAggregate struct {
	item                  v2Workspace
	sessionIDs            map[string]struct{}
	traceIDs              map[string]struct{}
	pipelineIDs           map[string]struct{}
	repeatedRootReadCount int
	relativeAttachCount   int
}

func (s *Server) handleV2Workspaces(w http.ResponseWriter, r *http.Request) {
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

	ops := make([]v2WorkspaceOp, 0)
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
		qOps := q
		qOps.IncludeInternal = true
		ops = append(ops, collectV2WorkspaceOps(traceMeta.Status, traceID, qOps, spans, proj, scopeIdx)...)
	}

	items := collectV2Workspaces(ops)
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastSeenUnixNano != items[j].LastSeenUnixNano {
			return items[i].LastSeenUnixNano > items[j].LastSeenUnixNano
		}
		return items[i].Root < items[j].Root
	})

	page, next := paginate(items, q.Offset, q.Limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"derivationVersion": derivationVersionV2,
		"items":             page,
		"nextCursor":        next,
	})
}

func collectV2Workspaces(ops []v2WorkspaceOp) []v2Workspace {
	opsBySession := map[string][]v2WorkspaceOp{}
	sessionOrder := make([]string, 0)
	for _, op := range ops {
		sessionKey := workspaceSessionKey(op)
		if _, ok := opsBySession[sessionKey]; !ok {
			sessionOrder = append(sessionOrder, sessionKey)
		}
		opsBySession[sessionKey] = append(opsBySession[sessionKey], op)
	}

	aggregates := map[string]*workspaceAggregate{}
	for _, sessionKey := range sessionOrder {
		sessionOps := opsBySession[sessionKey]
		roots := selectObservedWorkspaceRoots(sessionOps)
		if len(roots) == 0 {
			continue
		}
		assigned := assignWorkspaceOpsToRoots(sessionOps, roots)
		for _, root := range roots {
			rootOps := assigned[root]
			if len(rootOps) == 0 {
				continue
			}
			agg, ok := aggregates[root]
			if !ok {
				agg = &workspaceAggregate{
					item: v2Workspace{
						ID:   "workspace:" + root,
						Root: root,
						Name: workspaceDisplayName(root),
					},
					sessionIDs:  map[string]struct{}{},
					traceIDs:    map[string]struct{}{},
					pipelineIDs: map[string]struct{}{},
				}
				aggregates[root] = agg
			}
			for _, op := range rootOps {
				if op.TraceID != "" {
					agg.traceIDs[op.TraceID] = struct{}{}
				}
				if op.SessionID != "" {
					agg.sessionIDs[op.SessionID] = struct{}{}
				}
				if pipelineID := nonEmpty(op.PipelineID, op.TraceID+"/"+op.PipelineClientID); strings.TrimSpace(pipelineID) != "" && op.PipelineClientID != "" {
					agg.pipelineIDs[pipelineID] = struct{}{}
				}
				if agg.item.FirstSeenUnixNano == 0 || op.StartUnixNano < agg.item.FirstSeenUnixNano {
					agg.item.FirstSeenUnixNano = op.StartUnixNano
				}
				if op.EndUnixNano > agg.item.LastSeenUnixNano {
					agg.item.LastSeenUnixNano = op.EndUnixNano
				}
				agg.item.OpCount++
				if op.Direction == "write" {
					agg.item.WriteCount++
				} else {
					agg.item.ReadCount++
				}
				if op.CallName == "Host.directory" && workspaceCanonicalPath(op.Path) == root {
					agg.repeatedRootReadCount++
				}
				if !workspacePathIsAbsolute(op.Path) {
					agg.relativeAttachCount++
				}
				agg.item.Ops = append(agg.item.Ops, op)
			}
		}
	}

	items := make([]v2Workspace, 0, len(aggregates))
	for _, agg := range aggregates {
		sort.Slice(agg.item.Ops, func(i, j int) bool {
			if agg.item.Ops[i].StartUnixNano != agg.item.Ops[j].StartUnixNano {
				return agg.item.Ops[i].StartUnixNano > agg.item.Ops[j].StartUnixNano
			}
			return agg.item.Ops[i].ID > agg.item.Ops[j].ID
		})
		if len(agg.item.Ops) > 40 {
			agg.item.Ops = agg.item.Ops[:40]
		}
		agg.item.SessionCount = len(agg.sessionIDs)
		agg.item.TraceCount = len(agg.traceIDs)
		agg.item.PipelineCount = len(agg.pipelineIDs)
		agg.item.Evidence = buildV2WorkspaceEvidence(agg.item.Root, agg.repeatedRootReadCount, agg.relativeAttachCount)
		agg.item.Relations = buildV2WorkspaceRelations(agg.sessionIDs, agg.pipelineIDs)
		items = append(items, agg.item)
	}
	return items
}

func workspaceSessionKey(op v2WorkspaceOp) string {
	if op.SessionID != "" {
		return "session:" + op.SessionID
	}
	if op.ClientID != "" {
		return "client:" + op.ClientID
	}
	return "trace:" + op.TraceID
}

func selectObservedWorkspaceRoots(sessionOps []v2WorkspaceOp) []string {
	counts := map[string]int{}
	for _, op := range sessionOps {
		if op.CallName != "Host.directory" {
			continue
		}
		path := workspaceCanonicalPath(op.Path)
		if path == "" {
			continue
		}
		counts[path]++
	}

	type candidate struct {
		root  string
		count int
	}
	candidates := make([]candidate, 0, len(counts))
	for root, count := range counts {
		if count < 2 {
			continue
		}
		candidates = append(candidates, candidate{root: root, count: count})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].count != candidates[j].count {
			return candidates[i].count > candidates[j].count
		}
		if len(candidates[i].root) != len(candidates[j].root) {
			return len(candidates[i].root) < len(candidates[j].root)
		}
		return candidates[i].root < candidates[j].root
	})

	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		shadowed := false
		for _, accepted := range roots {
			if workspacePathBelongsToRoot(candidate.root, accepted) {
				shadowed = true
				break
			}
		}
		if shadowed {
			continue
		}
		roots = append(roots, candidate.root)
	}
	return roots
}

func assignWorkspaceOpsToRoots(sessionOps []v2WorkspaceOp, roots []string) map[string][]v2WorkspaceOp {
	assigned := make(map[string][]v2WorkspaceOp, len(roots))
	if len(roots) == 0 {
		return assigned
	}
	for _, op := range sessionOps {
		path := workspaceCanonicalPath(op.Path)
		bestRoot := ""
		if path != "" {
			for _, root := range roots {
				if !workspacePathBelongsToRoot(path, root) {
					continue
				}
				if len(root) > len(bestRoot) {
					bestRoot = root
				}
			}
		} else if len(roots) == 1 {
			bestRoot = roots[0]
		}
		if bestRoot == "" {
			continue
		}
		assigned[bestRoot] = append(assigned[bestRoot], op)
	}
	return assigned
}

func workspaceCanonicalPath(path string) string {
	path = strings.TrimSpace(path)
	if !workspacePathIsAbsolute(path) {
		return ""
	}
	return filepath.Clean(path)
}

func workspacePathIsAbsolute(path string) bool {
	path = strings.TrimSpace(path)
	return path != "" && filepath.IsAbs(path)
}

func workspacePathBelongsToRoot(path, root string) bool {
	path = workspaceCanonicalPath(path)
	root = workspaceCanonicalPath(root)
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	if root == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func workspaceDisplayName(root string) string {
	root = workspaceCanonicalPath(root)
	if root == "" {
		return "Workspace"
	}
	base := filepath.Base(root)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return root
	}
	return base
}

func buildV2WorkspaceEvidence(root string, repeatedRootReadCount, relativeAttachCount int) []v2EntityEvidence {
	evidence := []v2EntityEvidence{
		{
			Kind:       "Observed root",
			Confidence: "medium",
			Source:     root,
			Note:       fmt.Sprintf("%d repeated Host.directory reads anchored this workspace root.", repeatedRootReadCount),
		},
	}
	if relativeAttachCount > 0 {
		evidence = append(evidence, v2EntityEvidence{
			Kind:       "Relative attachment",
			Confidence: "medium",
			Source:     fmt.Sprintf("%d ops", relativeAttachCount),
			Note:       "Relative exports were attached only when the session had one unambiguous observed root.",
		})
	}
	return evidence
}

func buildV2WorkspaceRelations(sessionIDs, pipelineIDs map[string]struct{}) []v2EntityRelation {
	relations := make([]v2EntityRelation, 0, len(sessionIDs)+len(pipelineIDs))
	for _, sessionID := range setToSortedSlice(sessionIDs) {
		relations = append(relations, v2EntityRelation{
			Relation:   "observed-in",
			Target:     sessionID,
			TargetKind: "session",
			Note:       "Workspace root was observed through workspace ops in this session.",
		})
	}
	for _, pipelineID := range setToSortedSlice(pipelineIDs) {
		relations = append(relations, v2EntityRelation{
			Relation:   "touched-by",
			Target:     pipelineID,
			TargetKind: "pipeline",
			Note:       "At least one attached workspace op belonged to this pipeline.",
		})
	}
	return relations
}
