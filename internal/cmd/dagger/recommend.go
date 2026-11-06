package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
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
type RecommendFn func(context.Context, *core.Workspace) ([]string, error)

// SimpleRecommend recommends a module when any file path pattern matches.
func SimpleRecommend(patterns ...string) RecommendFn {
	return func(ctx context.Context, ws *core.Workspace) ([]string, error) {
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

// recommendRule is a modules.json `recommend` object. A module is recommended
// by the paths that match any of its matchers.
type recommendRule struct {
	AnyOf []recommendMatcher
	// unknown marks a rule with keys this CLI does not understand. It never
	// matches, so a newer rule cannot recommend more than intended.
	unknown bool
}

// UnmarshalJSON never fails: a malformed rule never matches, rather than
// making the whole catalog unreadable.
func (r *recommendRule) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	r.unknown = json.Unmarshal(data, &fields) != nil
	for key, raw := range fields {
		if key != "anyOf" || json.Unmarshal(raw, &r.AnyOf) != nil {
			r.unknown = true
		}
	}
	return nil
}

// recommendMatcher is one `anyOf` entry: a glob, optionally with exactly one
// content check on each matched file.
type recommendMatcher struct {
	Glob       string
	JSONKey    string
	TOMLTable  string
	INISection string
	// unknown marks a matcher with an unknown key, a non-string value, or an
	// invalid combination of keys. It never matches.
	unknown bool
}

// UnmarshalJSON never fails: a malformed matcher never matches, rather than
// making the whole catalog unreadable.
func (m *recommendMatcher) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	m.unknown = json.Unmarshal(data, &fields) != nil
	checks := 0
	for key, raw := range fields {
		var dst *string
		switch key {
		case "glob":
			dst = &m.Glob
		case "jsonKey":
			dst = &m.JSONKey
			checks++
		case "tomlTable":
			dst = &m.TOMLTable
			checks++
		case "iniSection":
			dst = &m.INISection
			checks++
		default:
			m.unknown = true
			continue
		}
		if json.Unmarshal(raw, dst) != nil || *dst == "" {
			m.unknown = true
		}
	}
	if m.Glob == "" || checks > 1 {
		m.unknown = true
	}
	return nil
}

// recommendFn compiles the rule into a RecommendFn. Matchers run in order and
// their paths are concatenated, exactly as the hand-written callbacks did. A
// rule without usable matchers compiles to nil, which is never called.
func (r *recommendRule) recommendFn() RecommendFn {
	if r == nil || r.unknown {
		return nil
	}
	fns := make([]RecommendFn, 0, len(r.AnyOf))
	for _, m := range r.AnyOf {
		if fn := m.recommendFn(); fn != nil {
			fns = append(fns, fn)
		}
	}
	if len(fns) == 0 {
		return nil
	}
	return func(ctx context.Context, ws *core.Workspace) ([]string, error) {
		var matches []string
		for _, fn := range fns {
			paths, err := fn(ctx, ws)
			if err != nil {
				return nil, err
			}
			matches = append(matches, paths...)
		}
		return matches, nil
	}
}

func (m recommendMatcher) recommendFn() RecommendFn {
	if m.unknown {
		return nil
	}
	var check func(string) bool
	switch {
	case m.JSONKey != "":
		check = hasJSONKey(m.JSONKey)
	case m.TOMLTable != "":
		check = hasTOMLTable(m.TOMLTable)
	case m.INISection != "":
		check = hasINISection(m.INISection)
	default:
		return SimpleRecommend(m.Glob)
	}
	return func(ctx context.Context, ws *core.Workspace) ([]string, error) {
		return recommendConfigFiles(ctx, ws, m.Glob, check)
	}
}

func recommendationDirectory(ws *core.Workspace) *core.Directory {
	return ws.Directory("/", core.WorkspaceDirectoryOpts{
		Exclude: recommendExcludeDirs,
	})
}

// runRecommend scans the workspace with each catalog entry's recommendation
// function. Skips already-installed modules. Returns the matches
// sorted by module name. Used by `dagger init` and `dagger module recommend`.
func runRecommend(ctx context.Context, dag *dagger.Client, mods []registryModule) ([]recommendation, error) {
	installed, err := installedModuleNames(ctx, dag)
	if err != nil {
		return nil, err
	}

	return recommendModules(ctx, core.NewQuery(dag).CurrentWorkspace(), mods, installed)
}

func recommendModules(ctx context.Context, ws *core.Workspace, mods []registryModule, installed map[string]bool) ([]recommendation, error) {
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
