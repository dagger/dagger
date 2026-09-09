package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"dagger.io/dagger"
)

// recommendation pairs a registry entry with the workspace-relative path that
// triggered its recommendation.
type recommendation struct {
	Module registryModule
	Match  string
}

// recommendExcludeDirs lists directories we strip from the workspace snapshot
// before globbing. Keeps the work cheap and avoids false positives from
// vendored or generated content. Patterns use Workspace.Directory's
// docker-ignore style, which anchors un-prefixed patterns to the root. We
// want recursive matches (e.g. a polyglot repo's `frontend/node_modules`),
// so each entry is doubled: one anchored at the root, one wildcarded for
// any nested depth.
var recommendExcludeDirs = []string{
	".git/", "**/.git/",
	".dagger/", "**/.dagger/",
	"node_modules/", "**/node_modules/",
	"vendor/", "**/vendor/",
	"dist/", "**/dist/",
	"build/", "**/build/",
	"target/", "**/target/",
}

// RecommendFn returns the workspace-root-relative paths that recommend a module.
// An empty result means no recommendation. A nil function is never called.
type RecommendFn func(context.Context, *dagger.Workspace) ([]string, error)

// SimpleRecommend recommends a module when any file path pattern matches.
func SimpleRecommend(patterns ...string) RecommendFn {
	return func(ctx context.Context, ws *dagger.Workspace) ([]string, error) {
		dir := recommendationDirectory(ws)
		var matches []string
		for _, pattern := range patterns {
			paths, err := dir.Glob(ctx, pattern)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, err
				}
				// Skip a failed pattern, as the registry scan did before.
				continue
			}
			matches = append(matches, paths...)
		}
		return matches, nil
	}
}

func recommendationDirectory(ws *dagger.Workspace) *dagger.Directory {
	return ws.Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: recommendExcludeDirs,
	})
}

// runRecommend scans the workspace with each registry entry's recommendation
// function. Skips already-installed modules. Returns the matches
// sorted by module name. Used by `dagger setup` step 3.
func runRecommend(ctx context.Context, dag *dagger.Client) ([]recommendation, error) {
	installed, err := installedModuleNames(ctx, dag)
	if err != nil {
		return nil, err
	}

	return recommendModules(ctx, dag.CurrentWorkspace(), loadModuleRegistry(), installed)
}

func recommendModules(ctx context.Context, ws *dagger.Workspace, mods []registryModule, installed map[string]bool) ([]recommendation, error) {
	ws = ws.WithWorkdir(".")

	recs := make([]recommendation, 0, len(mods))
	for _, m := range mods {
		if m.Recommend == nil || installed[m.Name] {
			continue
		}
		// Recommend the module if its function returns matches; report the
		// lexicographically smallest matched path so output is stable.
		matches, err := m.Recommend(ctx, ws)
		if err != nil {
			return nil, fmt.Errorf("recommend %s: %w", m.Name, err)
		}
		if len(matches) == 0 {
			continue
		}
		sort.Strings(matches)
		recs = append(recs, recommendation{Module: m, Match: matches[0]})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Module.Name < recs[j].Module.Name })

	return recs, nil
}

// installedModuleNames returns the set of module names installed in the
// current workspace.
func installedModuleNames(ctx context.Context, dag *dagger.Client) (map[string]bool, error) {
	var res struct {
		CurrentWorkspace struct {
			Modules []struct {
				Name string
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { modules { name } } }`,
	}, &dagger.Response{Data: &res}); err != nil {
		return nil, fmt.Errorf("list installed modules: %w", err)
	}
	installed := make(map[string]bool, len(res.CurrentWorkspace.Modules))
	for _, m := range res.CurrentWorkspace.Modules {
		installed[m.Name] = true
	}
	return installed, nil
}
