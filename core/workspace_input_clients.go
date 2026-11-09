package core

import (
	"context"
	"maps"
	"path"
	"path/filepath"
	"slices"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
)

func declaredClientTargets(ws *Workspace, src *ModuleSource) ([]string, error) {
	if ws == nil || ws.HostPath() == "" || src == nil || src.Kind != ModuleSourceKindLocal || src.Local == nil {
		return nil, nil
	}
	modulePath, err := filepath.Rel(ws.HostPath(), filepath.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath))
	if err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	pending := slices.Clone(ws.ModuleClients(filepath.ToSlash(modulePath)))
	for len(pending) > 0 {
		target := pending[0]
		pending = pending[1:]
		if targets[target] {
			continue
		}
		targets[target] = true
		pending = append(pending, ws.ModuleClients(target)...)
	}
	return slices.Sorted(maps.Keys(targets)), nil
}

func (c *workspaceInputCollector) declaredClientSources(ws *Workspace, mod *Module) {
	targets, err := declaredClientTargets(ws, mod.GetSource())
	if err != nil {
		c.issue("cannot resolve declared client paths: %v", err)
		return
	}
	if len(targets) == 0 {
		return
	}
	workspace, bound := WorkspaceFromContext(c.ctx)
	if !bound {
		if err := c.server.Select(c.ctx, c.server.Root(), &workspace, dagql.Selector{Field: "currentWorkspace"}); err != nil {
			c.issue("cannot load declared client workspace: %v", err)
			return
		}
	}
	for _, target := range targets {
		origin := "declared client " + target
		selectors := append(moduleSourceSelectors(ws, nil), provenance.Selector{Path: path.Join("/", target), Kind: provenance.ModuleSource, Origin: origin})
		summary := workspaceInputSummary{Sources: []workspaceInputSource{{Workspace: workspaceInputKey(ws), Selectors: selectors}}}
		reads := &workspaceInputReads{workspace: ws}
		ctx := context.WithValue(c.ctx, workspaceInputReadsKey{}, reads)
		ctx = context.WithValue(ctx, workspaceInputSourceContextKey{}, summary)
		var source dagql.ObjectResult[*ModuleSource]
		err := c.server.Select(ctx, workspace, &source, dagql.Selector{
			Field: "moduleSource",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String("/" + target)}},
		})
		c.result.merge(reads.snapshot())
		if err != nil {
			c.issue("cannot inspect declared client %q: %v", target, err)
			continue
		}
		c.moduleSources(ws, &Module{Source: dagql.NonNull(source)}, origin)
		if source.Self().EntrypointSources {
			c.retainedField(source.Self().ContextDirectory, origin+" sources")
		}
	}
}
