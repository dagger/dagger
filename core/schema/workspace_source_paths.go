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
	readlink, err := s.workspaceModuleReadlink(ctx, ws)
	if err != nil {
		return nil, nil, err
	}
	extraContent := map[string]bool{}
	resolve := func(p string) (string, error) {
		resolved, links, err := resolveWorkspaceModuleLinks(p, readlink)
		if err != nil {
			return "", err
		}
		for _, link := range links {
			extraContent[link] = true
		}
		extraContent[resolved] = true
		return resolved, nil
	}
	moduleDirs, contentDirs, err := collectWorkspaceModuleReadPaths(roots, func(dir string) (*modules.ModuleConfig, error) {
		dir, _, err := resolveWorkspaceModuleLinks(dir, readlink)
		if err != nil {
			return nil, err
		}
		for _, filename := range modules.ConfigFilenames() {
			configPath, links, err := resolveWorkspaceModuleLinks(path.Join(dir, filename), readlink)
			if err != nil {
				return nil, err
			}
			root, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{Include: []string{configPath}}, false)
			if err != nil {
				return nil, err
			}
			_, found, err := core.StatFSExists(ctx, &core.DirectoryStatFS{Dir: root}, configPath)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			data, err := core.DirectoryReadFile(ctx, root, configPath)
			if err != nil {
				return nil, err
			}
			// A config link needs its target file, not the target's directory.
			for _, link := range links {
				extraContent[link] = true
			}
			extraContent[configPath] = true
			if filename == workspace.LegacyModuleConfigFileName {
				return workspace.ParseLegacyModuleConfigTolerant(data)
			}
			cfg, err := modules.ParseModuleConfigForFilename(data, filename)
			if err != nil {
				return nil, err
			}
			return &cfg.ModuleConfig, nil
		}
		return nil, nil
	}, func(dir string) (string, error) {
		resolved, _, err := resolveWorkspaceModuleLinks(dir, readlink)
		return resolved, err
	})
	if err != nil {
		return nil, nil, err
	}
	for i, dir := range moduleDirs {
		moduleDirs[i], err = resolve(dir)
		if err != nil {
			return nil, nil, err
		}
	}
	for _, dir := range contentDirs {
		if _, err := resolve(dir); err != nil {
			return nil, nil, err
		}
		extraContent[dir] = true
	}
	contentDirs = contentDirs[:0]
	for dir := range extraContent {
		contentDirs = append(contentDirs, dir)
	}
	sort.Strings(contentDirs)
	return moduleDirs, contentDirs, nil
}

func collectWorkspaceModuleReadPaths(
	roots []string,
	readConfig func(string) (*modules.ModuleConfig, error),
	canonicalize func(string) (string, error),
) (moduleDirs, contentDirs []string, err error) {
	type visit struct {
		path      string
		ancestors []string
	}
	pending := make([]visit, len(roots))
	for i, root := range roots {
		pending[i].path = root
	}
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
		item := pending[0]
		dir := path.Clean(filepath.ToSlash(item.path))
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
		canonical := dir
		if canonicalize != nil {
			canonical, err = canonicalize(dir)
			if err != nil {
				return nil, nil, err
			}
		}
		// Keep the alias available, but do not walk back into a physical
		// ancestor through it. Other aliases can have distinct relative refs.
		if slices.Contains(item.ancestors, canonical) {
			continue
		}
		ancestors := append(slices.Clone(item.ancestors), canonical)
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
			pending = append(pending, visit{path: target, ancestors: ancestors})
		}
	}
	for dir := range content {
		contentDirs = append(contentDirs, dir)
	}
	sort.Strings(moduleDirs)
	sort.Strings(contentDirs)
	return moduleDirs, contentDirs, nil
}
