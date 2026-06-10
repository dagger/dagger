package main

import (
	"context"
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
// vendored or generated content. Patterns match Workspace.Directory's exclude
// semantics (path-prefix style).
var recommendExcludeDirs = []string{
	".git/",
	".dagger/",
	"node_modules/",
	"vendor/",
	"dist/",
	"build/",
	"target/",
}

// runRecommend scans the workspace for files matching each registry entry's
// recommend patterns. Skips already-installed modules. Returns the matches
// sorted by module name. Used by `dagger setup` step 3.
func runRecommend(ctx context.Context, dag *dagger.Client) ([]recommendation, error) {
	ws := dag.CurrentWorkspace()

	installed, err := installedModuleNames(ctx, dag)
	if err != nil {
		return nil, err
	}

	mods, err := loadModuleRegistry()
	if err != nil {
		return nil, err
	}

	// "/" resolves to the workspace root regardless of cwd; excluding common
	// vendored/generated dirs keeps matches relevant.
	dir := ws.Directory("/", dagger.WorkspaceDirectoryOpts{
		Exclude: recommendExcludeDirs,
	})

	recs := make([]recommendation, 0, len(mods))
	for _, m := range mods {
		if len(m.Recommend) == 0 || installed[m.Name] {
			continue
		}
		// Recommend the module if any of its patterns match; report the
		// lexicographically smallest matched path so output is stable.
		var match string
		for _, pattern := range m.Recommend {
			matches, err := dir.Glob(ctx, pattern)
			if err != nil {
				// A bad pattern in the registry shouldn't take down the whole
				// scan; just skip the pattern.
				continue
			}
			if len(matches) == 0 {
				continue
			}
			sort.Strings(matches)
			if match == "" || matches[0] < match {
				match = matches[0]
			}
		}
		if match == "" {
			continue
		}
		recs = append(recs, recommendation{Module: m, Match: match})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Module.Name < recs[j].Module.Name })

	return recs, nil
}

// installedModuleNames returns the set of module names installed in the
// current workspace.
func installedModuleNames(ctx context.Context, dag *dagger.Client) (map[string]bool, error) {
	var res struct {
		CurrentWorkspace struct {
			ModuleList []struct {
				Name string
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query: `query { currentWorkspace { moduleList { name } } }`,
	}, &dagger.Response{Data: &res}); err != nil {
		return nil, fmt.Errorf("list installed modules: %w", err)
	}
	installed := make(map[string]bool, len(res.CurrentWorkspace.ModuleList))
	for _, m := range res.CurrentWorkspace.ModuleList {
		installed[m.Name] = true
	}
	return installed, nil
}
