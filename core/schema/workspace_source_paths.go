package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/core/workspace"
)

// workspaceModuleReadPaths reads only known config paths while following local
// references. The content read can then admit the complete dependency graph
// without admitting ignored caches in those modules.
func (s *workspaceSchema) workspaceModuleReadPaths(ctx context.Context, ws *core.Workspace, roots []string) ([]string, []string, error) {
	return collectWorkspaceModuleReadPaths(roots, func(dir string) (*modules.ModuleConfig, error) {
		includes := make([]string, 0, len(modules.ConfigFilenames()))
		for _, filename := range modules.ConfigFilenames() {
			includes = append(includes, path.Join(dir, filename))
		}
		root, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{Include: includes}, false)
		if err != nil {
			return nil, err
		}
		filename, found, err := moduleConfigInDir(ctx, &core.DirectoryStatFS{Dir: root}, dir)
		if err != nil || !found {
			return nil, err
		}
		data, err := core.DirectoryReadFile(ctx, root, path.Join(dir, filename))
		if err != nil {
			return nil, err
		}
		if filename == workspace.LegacyModuleConfigFileName {
			return workspace.ParseLegacyModuleConfigTolerant(data)
		}
		cfg, err := modules.ParseModuleConfigForFilename(data, filename)
		if err != nil {
			return nil, err
		}
		return &cfg.ModuleConfig, nil
	})
}

func collectWorkspaceModuleReadPaths(
	roots []string,
	readConfig func(string) (*modules.ModuleConfig, error),
) (moduleDirs, contentDirs []string, err error) {
	pending := append([]string(nil), roots...)
	visited := map[string]bool{}
	content := map[string]bool{}
	configs := map[string]*modules.ModuleConfig{}
	loadConfig := func(dir string) (*modules.ModuleConfig, error) {
		if cfg, ok := configs[dir]; ok {
			return cfg, nil
		}
		cfg, err := readConfig(dir)
		if err != nil {
			return nil, fmt.Errorf("read module config at %q: %w", dir, err)
		}
		configs[dir] = cfg
		return cfg, nil
	}
	for len(pending) > 0 {
		dir := path.Clean(filepath.ToSlash(pending[0]))
		pending = pending[1:]
		if !filepath.IsLocal(dir) {
			return nil, nil, fmt.Errorf("module path %q escapes workspace root", dir)
		}
		if visited[dir] {
			continue
		}
		visited[dir] = true
		content[dir] = true
		moduleDirs = append(moduleDirs, dir)
		cfg, err := loadConfig(dir)
		if err != nil {
			return nil, nil, err
		}
		if cfg == nil {
			continue
		}
		if cfg.Source != "" && !filepath.IsAbs(cfg.Source) {
			if source := path.Join(dir, filepath.ToSlash(cfg.Source)); filepath.IsLocal(source) {
				content[source] = true
			}
		}
		refs := slices.Concat(cfg.Toolchains, cfg.Dependencies)
		if cfg.Blueprint != nil {
			refs = append(refs, cfg.Blueprint)
		}
		if cfg.SDK != nil && !sdk.IsBuiltinSDKName(cfg.SDK.Source) {
			refs = append(refs, &modules.ModuleConfigDependency{Source: cfg.SDK.Source, Pin: cfg.SDK.Pin})
		}
		for _, ref := range refs {
			if ref == nil || filepath.IsAbs(ref.Source) {
				continue
			}
			target := path.Join(dir, filepath.ToSlash(ref.Source))
			if !filepath.IsLocal(target) {
				continue
			}
			switch core.FastModuleSourceKindCheck(ref.Source, ref.Pin) {
			case core.ModuleSourceKindGit:
				continue
			case "":
				// A dotted name can refer to a local module. Probe its known
				// config paths without uploading the directory or contacting
				// a remote host. Cache the result for the graph visit below.
				candidate, err := loadConfig(target)
				if err != nil {
					return nil, nil, err
				}
				if candidate == nil {
					continue
				}
			}
			pending = append(pending, target)
		}
	}
	for dir := range content {
		contentDirs = append(contentDirs, dir)
	}
	sort.Strings(moduleDirs)
	sort.Strings(contentDirs)
	return moduleDirs, contentDirs, nil
}
