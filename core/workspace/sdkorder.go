package workspace

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/modules"
)

// SDKManagedModuleConfig pairs a workspace-managed SDK module's workspace-root-
// relative directory with its parsed module config. Config is nil when the
// module has no readable/parseable dagger-module.toml yet, in which case the
// module contributes no ordering edges (treated as a leaf).
type SDKManagedModuleConfig struct {
	Path   string
	Config *modules.ModuleConfig
}

type sdkOrderNode struct {
	origPath  string
	canonPath string
	cfg       *modules.ModuleConfig
	deps      []int
}

// visitState tracks how far a module has got through the depth-first walk.
type visitState int

const (
	unvisited visitState = iota
	// visiting marks a module on the walk's current path. Reaching it again
	// means the path looped back on itself: a dependency cycle.
	visiting
	// emitted marks a module already appended to the result. Reaching it again
	// is a diamond (two modules sharing a dependency), which is expected — it
	// stays emitted exactly once.
	emitted
)

// OrderSDKModulesLeafFirst orders workspace-managed SDK module paths so that
// every module's locally-managed dependencies precede it (leaf-first) and each
// path appears exactly once. Only a dependency that resolves to another module
// in the managed set creates an ordering edge; external (git/registry) refs,
// absolute sources, and locals pointing outside the managed set are treated as
// leaves. Declaration order breaks ties, so a list of independent modules — or
// one already in a valid order — is returned unchanged. The returned strings are
// the original authored paths (not canonicalized). It errors, naming the cycle,
// when the managed modules form a dependency cycle.
func OrderSDKModulesLeafFirst(mods []SDKManagedModuleConfig) ([]string, error) {
	// Dedup managed modules by canonical path; first declaration wins.
	index := make(map[string]int, len(mods))
	nodes := make([]sdkOrderNode, 0, len(mods))
	for _, m := range mods {
		cp := canonicalWorkspacePath(m.Path)
		if _, ok := index[cp]; ok {
			continue
		}
		index[cp] = len(nodes)
		nodes = append(nodes, sdkOrderNode{
			origPath:  m.Path,
			canonPath: cp,
			cfg:       m.Config,
		})
	}

	// Edges are restricted to the managed set: external (git/registry) refs,
	// absolute sources, and locals resolving outside the set are leaves.
	for i := range nodes {
		cfg := nodes[i].cfg
		if cfg == nil {
			continue
		}
		seen := make(map[int]struct{})
		for _, dep := range cfg.Dependencies {
			if dep == nil || !IsLocalRef(dep.Source, dep.Pin) {
				continue
			}
			// An absolute source is not workspace-relative; the loader rejects
			// absolute local deps, so it never denotes a managed module here.
			if path.IsAbs(filepath.ToSlash(dep.Source)) {
				continue
			}
			depPath := canonicalWorkspacePath(path.Join(nodes[i].canonPath, filepath.ToSlash(dep.Source)))
			depIdx, ok := index[depPath]
			if !ok {
				continue
			}
			if _, dup := seen[depIdx]; dup {
				continue
			}
			// A self-edge is kept: a self-dependency is a degenerate cycle and
			// is reported like any other by the DFS below.
			seen[depIdx] = struct{}{}
			nodes[i].deps = append(nodes[i].deps, depIdx)
		}
	}

	// Depth-first, appending each module only after every dependency it reaches
	// — which is exactly leaf-first.
	state := make([]visitState, len(nodes))
	result := make([]string, 0, len(nodes))
	var curPath []int

	var visit func(i int) error
	visit = func(i int) error {
		switch state[i] {
		case emitted:
			return nil
		case visiting:
			return sdkModuleCycleError(nodes, curPath, i)
		case unvisited:
		}
		state[i] = visiting
		curPath = append(curPath, i)
		for _, dep := range nodes[i].deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		curPath = curPath[:len(curPath)-1]
		state[i] = emitted
		result = append(result, nodes[i].origPath)
		return nil
	}

	for i := range nodes {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func canonicalWorkspacePath(p string) string {
	if p == "" {
		return "."
	}
	return path.Clean(filepath.ToSlash(p))
}

func sdkModuleCycleError(nodes []sdkOrderNode, curPath []int, repeated int) error {
	cycle := make([]string, 0, len(curPath)+1)
	started := false
	for _, i := range curPath {
		if i == repeated {
			started = true
		}
		if started {
			cycle = append(cycle, nodes[i].canonPath)
		}
	}
	cycle = append(cycle, nodes[repeated].canonPath)
	return fmt.Errorf("workspace SDK modules form a dependency cycle: %s", strings.Join(cycle, " -> "))
}
