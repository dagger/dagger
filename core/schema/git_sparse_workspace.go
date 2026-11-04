package schema

import (
	"context"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/layercopy"
)

type sparseWorkspaceTreeArgs struct {
	Cwd string
}

func (s *gitSchema) sparseWorkspaceTree(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args sparseWorkspaceTreeArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	// Pinned like a tree without .git: the commit alone decides these files, so
	// a later selection by SHA must not reuse a mutable branch's recipe.
	ref := parent.Self()
	if ref.Ref.Name != ref.Ref.SHA {
		err := srv.Select(ctx, ref.Repo, &inst,
			dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(ref.Ref.SHA)}}},
			dagql.Selector{Field: "__sparseWorkspaceTree", Args: []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(args.Cwd)}}},
		)
		return inst, err
	}
	backend, ok := ref.Backend.(*core.RemoteGitRef)
	if !ok {
		return inst, fmt.Errorf("sparse workspace tree: %T is not a remote ref", ref.Backend)
	}
	dir, err := backend.SparseTree(ctx, srv, func(ctx context.Context, files *core.GitTreeFiles) ([]string, error) {
		return planGitWorkspaceFiles(ctx, files, args.Cwd)
	})
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

// planGitWorkspaceFiles picks the files of a commit that loading its workspace
// reads: the config files, and the context of each module the workspace's config
// declares and each local module they depend on. It costs two requests, one for
// the config files and one for the rest, and one more for a config that
// includes another.
//
// The plan only decides how much of a run avoids the full checkout. Every read
// checks by itself whether the sparse tree holds what it selects, so a file
// this misses makes that read slower, not wrong; and a config this can't parse
// is left for module loading to report.
func planGitWorkspaceFiles(ctx context.Context, files *core.GitTreeFiles, cwd string) ([]string, error) {
	configNames := []string{
		workspace.ConfigFileName,
		workspace.LockFileName,
		workspace.LegacyLockFileName,
	}
	configNames = append(configNames, modules.ConfigFilenames()...)
	configs := files.Named(configNames...)
	if err := files.Fetch(ctx, configs); err != nil {
		return nil, err
	}
	planned := map[string]struct{}{}
	for _, p := range configs {
		planned[p] = struct{}{}
	}

	pathExists := func(_ context.Context, p string) (string, bool, error) {
		p = path.Clean(filepath.ToSlash(p))
		return path.Dir(p), files.Has(p) || files.Tree.IsDir(p), nil
	}
	ws, err := workspace.DetectInRoot(ctx, pathExists, cwd, ".")
	if err != nil {
		return nil, err
	}
	if ws == nil || ws.ConfigFile == "" {
		return slices.Sorted(maps.Keys(planned)), nil
	}

	visited := map[string]struct{}{}
	var visitModule func(dir string)
	visitModule = func(dir string) {
		dir = path.Clean(dir)
		if _, seen := visited[dir]; seen || !filepath.IsLocal(dir) {
			return
		}
		visited[dir] = struct{}{}
		src, err := gitWorkspaceModuleConfig(ctx, files, dir)
		if src == nil {
			if err != nil {
				slog.Debug("sparse workspace tree: skipping module config", "dir", dir, "error", err)
			}
			return
		}
		selected, err := files.Tree.SelectedFiles("", layercopy.Filter{Include: moduleSourceContextIncludes(src)})
		if err != nil {
			slog.Debug("sparse workspace tree: skipping module context", "dir", dir, "error", err)
		}
		for _, p := range selected {
			planned[p] = struct{}{}
		}
		deps := append(slices.Clone(src.ConfigDependencies), src.ConfigToolchains...)
		if src.ConfigBlueprint != nil {
			deps = append(deps, src.ConfigBlueprint)
		}
		for _, dep := range deps {
			if dep != nil && workspace.IsLocalRef(dep.Source, dep.Pin) {
				visitModule(path.Join(dir, dep.Source))
			}
		}
		if src.SDK != nil && workspace.IsLocalRef(src.SDK.Source, "") {
			visitModule(path.Join(dir, src.SDK.Source))
		}
	}
	visitConfig := func(file string) *workspace.Config {
		// An included config can have any name, so it may not be fetched yet.
		if err := files.Fetch(ctx, []string{file}); err != nil {
			slog.Debug("sparse workspace tree: skipping workspace config", "file", file, "error", err)
			return nil
		}
		data, err := files.Read(ctx, file)
		if err != nil {
			return nil
		}
		planned[file] = struct{}{}
		dir := path.Dir(file)
		cfg, err := workspace.ParseConfigAt(ctx, data, dir)
		if err != nil {
			slog.Debug("sparse workspace tree: skipping workspace config", "file", file, "error", err)
			return nil
		}
		visitSource := func(source string) {
			if source != "" && workspace.IsLocalRef(source, "") {
				visitModule(workspace.ResolveModuleEntrySource(dir, source))
			}
		}
		for _, name := range slices.Sorted(maps.Keys(cfg.Modules)) {
			visitSource(cfg.Modules[name].Source)
		}
		for _, name := range slices.Sorted(maps.Keys(cfg.SDKs)) {
			visitSource(cfg.SDKs[name].Module)
		}
		for _, envName := range slices.Sorted(maps.Keys(cfg.Env)) {
			env := cfg.Env[envName]
			for _, name := range slices.Sorted(maps.Keys(env.Modules)) {
				visitSource(env.Modules[name].Source)
			}
		}
		return cfg
	}
	if cfg := visitConfig(ws.ConfigFile); cfg != nil {
		for _, include := range cfg.Include {
			if workspace.IsLocalRef(include.Source, "") {
				visitConfig(workspace.ResolveModuleEntrySource(path.Dir(ws.ConfigFile), include.Source))
			}
		}
	}
	return slices.Sorted(maps.Keys(planned)), nil
}

// gitWorkspaceModuleConfig reads the module config in dir into a source, with
// the same parsing that loads the module, so its includes are the same. It's
// nil when dir has no config, or none that parses.
func gitWorkspaceModuleConfig(ctx context.Context, files *core.GitTreeFiles, dir string) (*core.ModuleSource, error) {
	for _, filename := range modules.ConfigFilenames() {
		file := path.Join(dir, filename)
		if !files.Has(file) {
			continue
		}
		data, err := files.Read(ctx, file)
		if err != nil {
			return nil, err
		}
		src := &core.ModuleSource{SourceRootSubpath: dir, ConfigFilename: filename}
		if err := (&moduleSourceSchema{}).initFromModConfig(data, src); err != nil {
			return nil, err
		}
		return src, nil
	}
	return nil, nil
}
