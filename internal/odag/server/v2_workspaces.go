package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/store"
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
	SessionIDs        []string           `json:"sessionIDs,omitempty"`
	ClientIDs         []string           `json:"clientIDs,omitempty"`
	PipelineIDs       []string           `json:"pipelineIDs,omitempty"`
	Ops               []v2WorkspaceOp    `json:"ops,omitempty"`
	Evidence          []v2EntityEvidence `json:"evidence,omitempty"`
	Relations         []v2EntityRelation `json:"relations,omitempty"`
}

type workspaceAggregate struct {
	item                  v2Workspace
	sessionIDs            map[string]struct{}
	clientIDs             map[string]struct{}
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

	ops, err := s.store.ListDerivedWorkspaceOps(r.Context(), store.DerivedWorkspaceOpQuery{
		TraceID:      q.TraceID,
		SessionID:    q.SessionID,
		ClientID:     q.ClientID,
		FromUnixNano: q.FromUnixNano,
		ToUnixNano:   q.ToUnixNano,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("list materialized workspace ops: %v", err), http.StatusInternalServerError)
		return
	}
	if len(ops) == 0 {
		traceIDs, err := s.resolveV2TraceIDs(r.Context(), q)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				http.Error(w, fmt.Sprintf("resolve traces: %v", err), http.StatusInternalServerError)
				return
			}
		}
		if len(traceIDs) > 0 {
			refreshTraceIDs := make([]string, 0, len(traceIDs))
			missingTraceIDs, err := s.store.MissingDerivedWorkspaceTraceIDs(r.Context(), traceIDs)
			if err != nil {
				http.Error(w, fmt.Sprintf("check materialized workspaces: %v", err), http.StatusInternalServerError)
				return
			}
			refreshTraceIDs = append(refreshTraceIDs, missingTraceIDs...)
			staleTraceIDs, err := s.staleDerivedWorkspaceTraceIDs(r.Context(), traceIDs)
			if err != nil {
				http.Error(w, fmt.Sprintf("check stale materialized workspaces: %v", err), http.StatusInternalServerError)
				return
			}
			refreshTraceIDs = append(refreshTraceIDs, staleTraceIDs...)
			refreshTraceIDs = normalizeWorkspaceTraceIDs(refreshTraceIDs)
			if len(refreshTraceIDs) > 0 {
				if err := RefreshMaterializedWorkspaces(r.Context(), s.store, refreshTraceIDs); err != nil {
					http.Error(w, fmt.Sprintf("materialize workspaces: %v", err), http.StatusInternalServerError)
					return
				}
				ops, err = s.store.ListDerivedWorkspaceOps(r.Context(), store.DerivedWorkspaceOpQuery{
					TraceID:      q.TraceID,
					SessionID:    q.SessionID,
					ClientID:     q.ClientID,
					FromUnixNano: q.FromUnixNano,
					ToUnixNano:   q.ToUnixNano,
				})
				if err != nil {
					http.Error(w, fmt.Sprintf("list materialized workspace ops: %v", err), http.StatusInternalServerError)
					return
				}
			}
		}
	}

	items := collectV2WorkspacesFromAssignedOps(materializedWorkspaceOpsToV2(ops))
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

func (s *Server) staleDerivedWorkspaceTraceIDs(ctx context.Context, traceIDs []string) ([]string, error) {
	items := make([]string, 0, len(traceIDs))
	for _, traceID := range normalizeWorkspaceTraceIDs(traceIDs) {
		summary, err := s.store.GetDerivedWorkspaceTrace(ctx, traceID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if summary.WorkspaceOpCount > 0 {
			continue
		}
		traceMeta, err := s.store.GetTrace(ctx, traceID)
		if err != nil {
			return nil, err
		}
		if summary.RefreshedUnixNano >= traceMeta.LastSeenUnixNano {
			continue
		}
		spans, err := s.store.ListTraceSpans(ctx, traceID)
		if err != nil {
			return nil, err
		}
		if traceContainsWorkspaceOpSpans(spans) {
			items = append(items, traceID)
		}
	}
	return items, nil
}

func collectV2WorkspacesFromAssignedOps(ops []v2WorkspaceOp) []v2Workspace {
	aggregates := map[string]*workspaceAggregate{}
	for _, op := range ops {
		root := strings.TrimSpace(op.WorkspaceRoot)
		if root == "" {
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
				clientIDs:   map[string]struct{}{},
				traceIDs:    map[string]struct{}{},
				pipelineIDs: map[string]struct{}{},
			}
			aggregates[root] = agg
		}
		if op.TraceID != "" {
			agg.traceIDs[op.TraceID] = struct{}{}
		}
		if op.SessionID != "" {
			agg.sessionIDs[op.SessionID] = struct{}{}
		}
		for _, clientID := range []string{op.ClientID, op.PipelineClientID} {
			if clientID != "" {
				agg.clientIDs[clientID] = struct{}{}
			}
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
		agg.item.SessionIDs = sortedStringKeys(agg.sessionIDs)
		agg.item.ClientIDs = sortedStringKeys(agg.clientIDs)
		agg.item.PipelineIDs = sortedStringKeys(agg.pipelineIDs)
		agg.item.SessionCount = len(agg.sessionIDs)
		agg.item.TraceCount = len(agg.traceIDs)
		agg.item.PipelineCount = len(agg.pipelineIDs)
		agg.item.Evidence = buildV2WorkspaceEvidence(agg.item.Root, agg.repeatedRootReadCount, agg.relativeAttachCount)
		agg.item.Relations = buildV2WorkspaceRelations(agg.sessionIDs, agg.pipelineIDs)
		items = append(items, agg.item)
	}
	return items
}

func materializedWorkspaceOpsToV2(items []store.DerivedWorkspaceOpRecord) []v2WorkspaceOp {
	out := make([]v2WorkspaceOp, 0, len(items))
	for _, item := range items {
		out = append(out, v2WorkspaceOp{
			ID:               item.ID,
			TraceID:          item.TraceID,
			WorkspaceRoot:    item.WorkspaceRoot,
			SessionID:        item.SessionID,
			ClientID:         item.ClientID,
			SpanID:           item.SpanID,
			Name:             item.Name,
			Kind:             item.Kind,
			Direction:        item.Direction,
			CallName:         item.CallName,
			Path:             item.Path,
			TargetType:       item.TargetType,
			Status:           item.Status,
			StatusCode:       item.StatusCode,
			StartUnixNano:    item.StartUnixNano,
			EndUnixNano:      item.EndUnixNano,
			ReceiverDagqlID:  item.ReceiverDagqlID,
			OutputDagqlID:    item.OutputDagqlID,
			PipelineClientID: item.PipelineClientID,
			PipelineID:       item.PipelineID,
			PipelineCommand:  item.PipelineCommand,
		})
	}
	return out
}

func sortedStringKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	items := make([]string, 0, len(m))
	for key := range m {
		if strings.TrimSpace(key) == "" {
			continue
		}
		items = append(items, key)
	}
	sort.Strings(items)
	return items
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

func selectFallbackWorkspaceRoots(sessionOps []v2WorkspaceOp) []string {
	candidates := map[string]struct{}{}
	for _, op := range sessionOps {
		if op.CallName != "Host.directory" {
			continue
		}
		path := workspaceNormalizedPath(op.Path)
		if path == "" || workspacePathIsAbsolute(path) {
			continue
		}
		candidates[path] = struct{}{}
	}
	if len(candidates) != 1 {
		return nil
	}
	return sortedStringKeys(candidates)
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
		if bestRoot == "" && !workspacePathIsAbsolute(op.Path) && len(roots) == 1 {
			bestRoot = roots[0]
		}
		if bestRoot == "" {
			continue
		}
		assigned[bestRoot] = append(assigned[bestRoot], op)
	}
	return assigned
}

func workspaceNormalizedPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func workspaceCanonicalPath(path string) string {
	path = workspaceNormalizedPath(path)
	if !workspacePathIsAbsolute(path) {
		return ""
	}
	return path
}

func workspacePathIsAbsolute(path string) bool {
	path = strings.TrimSpace(path)
	return path != "" && filepath.IsAbs(path)
}

func workspacePathBelongsToRoot(path, root string) bool {
	absPath := workspaceCanonicalPath(path)
	absRoot := workspaceCanonicalPath(root)
	if absPath != "" && absRoot != "" {
		if absPath == absRoot {
			return true
		}
		if absRoot == string(filepath.Separator) {
			return true
		}
		return strings.HasPrefix(absPath, absRoot+string(filepath.Separator))
	}

	relPath := workspaceNormalizedPath(path)
	relRoot := workspaceNormalizedPath(root)
	if relPath == "" || relRoot == "" || workspacePathIsAbsolute(relPath) || workspacePathIsAbsolute(relRoot) {
		return false
	}
	if relPath == relRoot {
		return true
	}
	return strings.HasPrefix(relPath, relRoot+string(filepath.Separator))
}

func workspaceDisplayName(root string) string {
	normalized := workspaceNormalizedPath(root)
	if normalized == "" {
		return "Workspace"
	}
	if canonical := workspaceCanonicalPath(normalized); canonical != "" {
		normalized = canonical
	}
	base := filepath.Base(normalized)
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		return normalized
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
			Note:       "Relative exports were attached only when the owning scope had one unambiguous observed root.",
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
