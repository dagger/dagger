package core

import (
	"context"
	"path"

	"github.com/dagger/dagger/core/provenance"
)

// withWorkspaceInputDefaults maps the Host calls inside LoadUserDefaults to
// their workspace files. Leave unmapped calls under the ordinary observer.
func withWorkspaceInputDefaults(ctx context.Context, src *ModuleSource) context.Context {
	reads := currentWorkspaceInputReads(ctx)
	if reads == nil {
		return ctx
	}
	ws := workspaceInputSourceWorkspace(ctx)
	if ws == nil {
		ws = src.Workspace.Self()
	}
	if ws == nil || workspaceInputKey(ws) == "" {
		return ctx
	}
	if _, local := ws.LocalSourceHostPath(); !local {
		return ctx
	}
	// Non-local sources inside a module do not search the caller's host.
	// Local sources have already selected their non-module caller's metadata.
	inModule, err := runningInsideModule(ctx)
	if err != nil || inModule {
		return ctx
	}
	selectors, err := workspaceHostEnvSelectors(ctx, ws)
	if err != nil || len(selectors) == 0 {
		return ctx
	}
	if src.Kind == ModuleSourceKindLocal {
		root, mapped := moduleSourceWorkspaceRoot(ws, src)
		if !mapped {
			return ctx
		}
		selectors = append(selectors, provenance.Selector{
			Path: path.Join(root, src.SourceRootSubpath, ".env"), Kind: provenance.ModuleSource, Origin: "module defaults",
		})
	}
	summary := workspaceInputSummary{Sources: []workspaceInputSource{{Workspace: workspaceInputKey(ws), Selectors: selectors}}}.normalized()
	reads.add(summary)
	return context.WithValue(ctx, workspaceInputSourceContextKey{}, summary)
}
