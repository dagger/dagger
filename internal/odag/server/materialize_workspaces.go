package server

import (
	"context"
	"errors"
	"path/filepath"
	"fmt"
	"sort"
	"strings"

	"github.com/dagger/dagger/internal/odag/derive"
	"github.com/dagger/dagger/internal/odag/store"
)

type MaterializedWorkspaceRefreshProgress struct {
	TraceID   string
	Completed int
	Total     int
}

func RefreshMaterializedWorkspaces(ctx context.Context, st *store.Store, traceIDs []string) error {
	return RefreshMaterializedWorkspacesWithProgress(ctx, st, traceIDs, nil)
}

func RefreshMaterializedWorkspacesWithProgress(
	ctx context.Context,
	st *store.Store,
	traceIDs []string,
	progress func(MaterializedWorkspaceRefreshProgress),
) error {
	traceIDs = normalizeWorkspaceTraceIDs(traceIDs)
	total := len(traceIDs)
	for i, traceID := range traceIDs {
		if err := refreshMaterializedWorkspaceTrace(ctx, st, traceID); err != nil {
			return err
		}
		if progress != nil {
			progress(MaterializedWorkspaceRefreshProgress{
				TraceID:   traceID,
				Completed: i + 1,
				Total:     total,
			})
		}
	}
	return nil
}

func RebuildMaterializedWorkspaces(ctx context.Context, st *store.Store) error {
	return RebuildMaterializedWorkspacesWithProgress(ctx, st, nil)
}

func RebuildMaterializedWorkspacesWithProgress(
	ctx context.Context,
	st *store.Store,
	progress func(MaterializedWorkspaceRefreshProgress),
) error {
	traceIDs, err := st.ListTraceIDs(ctx)
	if err != nil {
		return fmt.Errorf("list traces for workspace rebuild: %w", err)
	}
	if err := st.ClearDerivedWorkspaceProjection(ctx); err != nil {
		return err
	}
	if err := RefreshMaterializedWorkspacesWithProgress(ctx, st, traceIDs, progress); err != nil {
		return fmt.Errorf("refresh materialized workspaces: %w", err)
	}
	return nil
}

func normalizeWorkspaceTraceIDs(traceIDs []string) []string {
	seen := make(map[string]struct{}, len(traceIDs))
	items := make([]string, 0, len(traceIDs))
	for _, rawTraceID := range traceIDs {
		traceID := strings.TrimSpace(rawTraceID)
		if traceID == "" {
			continue
		}
		if _, ok := seen[traceID]; ok {
			continue
		}
		seen[traceID] = struct{}{}
		items = append(items, traceID)
	}
	return items
}

func refreshMaterializedWorkspaceTrace(ctx context.Context, st *store.Store, traceID string) error {
	traceMeta, err := st.GetTrace(ctx, traceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return st.ReplaceDerivedWorkspaceProjection(ctx, traceID, nil, store.DerivedWorkspaceTraceRecord{
				TraceID: traceID,
			})
		}
		return fmt.Errorf("get trace %s: %w", traceID, err)
	}

	spans, err := st.ListTraceSpans(ctx, traceID)
	if err != nil {
		return fmt.Errorf("list spans for trace %s: %w", traceID, err)
	}
	proj, scopeIdx, err := buildV2TraceScope(traceID, spans)
	if err != nil {
		return fmt.Errorf("build v2 scope for trace %s: %w", traceID, err)
	}
	ops := collectV2WorkspaceOps(traceMeta.Status, traceID, v2Query{IncludeInternal: true}, spans, proj, scopeIdx)
	moduleRootsByOwner, err := collectModuleSourceWorkspaceRoots(traceID, spans, scopeIdx)
	if err != nil {
		return fmt.Errorf("collect module-source workspaces for trace %s: %w", traceID, err)
	}
	materialized := materializeWorkspaceTraceOps(ops, moduleRootsByOwner)
	if err := st.ReplaceDerivedWorkspaceProjection(ctx, traceID, materialized.Ops, store.DerivedWorkspaceTraceRecord{
		TraceID:        traceID,
		WorkspaceCount: materialized.WorkspaceCount,
	}); err != nil {
		return fmt.Errorf("persist materialized workspaces for trace %s: %w", traceID, err)
	}
	return nil
}

type materializedWorkspaceTrace struct {
	Ops            []store.DerivedWorkspaceOpRecord
	WorkspaceCount int
}

func materializeWorkspaceTraceOps(ops []v2WorkspaceOp, moduleRootsByOwner map[string][]string) materializedWorkspaceTrace {
	if len(ops) == 0 {
		return materializedWorkspaceTrace{}
	}

	sort.Slice(ops, func(i, j int) bool {
		if ops[i].StartUnixNano != ops[j].StartUnixNano {
			return ops[i].StartUnixNano < ops[j].StartUnixNano
		}
		return ops[i].ID < ops[j].ID
	})

	opsByOwner := make(map[string][]v2WorkspaceOp)
	ownerOrder := make([]string, 0)
	for _, op := range ops {
		key := workspaceOwnerKey(op)
		if _, ok := opsByOwner[key]; !ok {
			ownerOrder = append(ownerOrder, key)
		}
		opsByOwner[key] = append(opsByOwner[key], op)
	}

	assignedRoots := make(map[string]string, len(ops))
	for _, ownerKey := range ownerOrder {
		groupOps := opsByOwner[ownerKey]
		roots := append([]string(nil), moduleRootsByOwner[ownerKey]...)
		if len(roots) == 0 {
			roots = selectObservedWorkspaceRoots(groupOps)
		}
		if len(roots) == 0 {
			continue
		}
		assigned := assignWorkspaceOpsToRoots(groupOps, roots)
		for root, rootOps := range assigned {
			for _, op := range rootOps {
				assignedRoots[op.ID] = root
			}
		}
	}

	workspaceRoots := map[string]struct{}{}
	items := make([]store.DerivedWorkspaceOpRecord, 0, len(assignedRoots))
	for _, op := range ops {
		root := strings.TrimSpace(assignedRoots[op.ID])
		if root == "" {
			continue
		}
		workspaceRoots[root] = struct{}{}
		items = append(items, store.DerivedWorkspaceOpRecord{
			ID:               op.ID,
			TraceID:          op.TraceID,
			WorkspaceRoot:    root,
			SessionID:        op.SessionID,
			ClientID:         op.ClientID,
			SpanID:           op.SpanID,
			Name:             op.Name,
			Kind:             op.Kind,
			Direction:        op.Direction,
			CallName:         op.CallName,
			Path:             op.Path,
			TargetType:       op.TargetType,
			Status:           op.Status,
			StatusCode:       op.StatusCode,
			StartUnixNano:    op.StartUnixNano,
			EndUnixNano:      op.EndUnixNano,
			ReceiverDagqlID:  op.ReceiverDagqlID,
			OutputDagqlID:    op.OutputDagqlID,
			PipelineClientID: op.PipelineClientID,
			PipelineID:       op.PipelineID,
			PipelineCommand:  op.PipelineCommand,
		})
	}
	return materializedWorkspaceTrace{
		Ops:            items,
		WorkspaceCount: len(workspaceRoots),
	}
}

func workspaceOwnerKey(op v2WorkspaceOp) string {
	return workspaceOwnerKeyForScope(op.TraceID, op.SessionID, op.ClientID)
}

func workspaceOwnerKeyForScope(traceID, sessionID, clientID string) string {
	switch {
	case strings.TrimSpace(clientID) != "":
		return "client:" + clientID
	case strings.TrimSpace(sessionID) != "":
		return "session:" + sessionID
	default:
		return "trace:" + traceID
	}
}

func collectModuleSourceWorkspaceRoots(
	traceID string,
	spans []store.SpanRecord,
	scopeIdx *derive.ScopeIndex,
) (map[string][]string, error) {
	if scopeIdx == nil || len(spans) == 0 {
		return nil, nil
	}

	callPayloads, err := decodeWorkspaceOpCallPayloads(spans)
	if err != nil {
		return nil, err
	}

	rootsByOwner := map[string]map[string]struct{}{}
	for _, sp := range spans {
		env, err := decodeV2SpanEnvelope(sp.DataJSON)
		if err != nil {
			continue
		}

		root := moduleSourceWorkspaceRootFromOutputState(env.Attributes)
		if root == "" {
			root = moduleSourceWorkspaceRootFromCall(sp.Name, callPayloads[sp.SpanID])
		}
		if root == "" {
			continue
		}

		ownerKey := workspaceOwnerKeyForScope(traceID, scopeIdx.SessionIDForSpan(sp.SpanID), scopeIdx.ClientIDForSpan(sp.SpanID))
		if _, ok := rootsByOwner[ownerKey]; !ok {
			rootsByOwner[ownerKey] = map[string]struct{}{}
		}
		rootsByOwner[ownerKey][root] = struct{}{}
	}

	out := make(map[string][]string, len(rootsByOwner))
	for ownerKey, roots := range rootsByOwner {
		out[ownerKey] = sortedStringKeys(roots)
	}
	return out, nil
}

func moduleSourceWorkspaceRootFromCall(name string, payload *workspaceOpCallPayload) string {
	if strings.TrimSpace(name) != "Query.moduleSource" || payload == nil || payload.Call == nil {
		return ""
	}
	refString := strings.TrimSpace(workspaceOpCallArgString(payload.Call, "refString"))
	if refString == "" || !filepath.IsAbs(refString) {
		return ""
	}
	return filepath.Clean(refString)
}

func moduleSourceWorkspaceRootFromOutputState(attrs map[string]any) string {
	state, err := decodePipelineOutputStatePayload(attrs)
	if err != nil || state == nil {
		return ""
	}
	if strings.TrimSpace(moduleSourceStateTypeName(state)) != "ModuleSource" {
		return ""
	}
	if !strings.Contains(strings.ToUpper(moduleSourceStateFieldString(state, "Kind")), "LOCAL") {
		return ""
	}

	contextDir := strings.TrimSpace(moduleSourceStateNestedString(state, "Local", "ContextDirectoryPath"))
	if contextDir == "" || !filepath.IsAbs(contextDir) {
		return ""
	}

	sourceRootSubpath := strings.TrimSpace(moduleSourceStateFieldString(state, "SourceRootSubpath"))
	if sourceRootSubpath == "" || sourceRootSubpath == "." {
		return filepath.Clean(contextDir)
	}
	return filepath.Clean(filepath.Join(contextDir, sourceRootSubpath))
}

func moduleSourceStateTypeName(state map[string]any) string {
	value, _ := state["type"].(string)
	return strings.TrimSpace(value)
}

func moduleSourceStateFieldString(state map[string]any, name string) string {
	field := moduleSourceStateFieldMap(state, name)
	value, _ := field["value"].(string)
	return strings.TrimSpace(value)
}

func moduleSourceStateNestedString(state map[string]any, fieldName, nestedName string) string {
	field := moduleSourceStateFieldMap(state, fieldName)
	value, _ := field["value"].(map[string]any)
	nestedValue, _ := value[nestedName].(string)
	return strings.TrimSpace(nestedValue)
}

func moduleSourceStateFieldMap(state map[string]any, name string) map[string]any {
	fields, _ := state["fields"].(map[string]any)
	field, _ := fields[name].(map[string]any)
	return field
}
