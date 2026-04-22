package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceRefreshModulesArgs struct {
	ModuleNames []string `name:"moduleNames" default:"[]"`
}

type workspaceRefreshModule struct {
	Name   string
	Source string
}

func (s *workspaceSchema) refreshModules(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceRefreshModulesArgs,
) (*core.Changeset, error) {
	ws := parent.Self()
	if ws.HostPath() == "" {
		return nil, fmt.Errorf("workspace lock refresh is local-only")
	}
	if !ws.HasConfig {
		return nil, fmt.Errorf("no config.toml found in workspace")
	}

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}

	query, err := core.CurrentQuery(workspaceCtx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Engine(workspaceCtx)
	if err != nil {
		return nil, fmt.Errorf("engine client: %w", err)
	}

	cfg, err := readWorkspaceConfig(workspaceCtx, ws)
	if err != nil {
		return nil, err
	}

	modules, err := resolveWorkspaceRefreshModules(cfg, args.ModuleNames)
	if err != nil {
		return nil, err
	}

	lock, err := readWorkspaceLock(workspaceCtx, bk, ws)
	if err != nil {
		return nil, err
	}
	if err := refreshWorkspaceModuleLookups(workspaceCtx, query, lock, modules); err != nil {
		return nil, err
	}

	return s.workspaceLockChangeset(ctx, ws, lock)
}

func refreshWorkspaceModuleLookups(
	ctx context.Context,
	query *core.Query,
	lock *workspace.Lock,
	modules []workspaceRefreshModule,
) error {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return fmt.Errorf("dagql server: %w", err)
	}

	resultsBySource := make(map[string]workspace.LookupResult, len(modules))
	for _, mod := range modules {
		result, ok := resultsBySource[mod.Source]
		if !ok {
			kind, err := resolveWorkspaceModuleSourceKind(ctx, srv, workspaceRefreshModuleRef(mod.Source))
			if err != nil {
				return fmt.Errorf("module %q source %q: %w", mod.Name, mod.Source, err)
			}
			if kind != core.ModuleSourceKindGit {
				return fmt.Errorf("module %q source %q is not a git module", mod.Name, mod.Source)
			}

			existing, hasExisting, err := lock.GetModuleResolve(mod.Source)
			if err != nil {
				return fmt.Errorf("module %q source %q: %w", mod.Name, mod.Source, err)
			}
			policy := workspace.LockPolicy("")
			if hasExisting {
				policy = existing.Policy
			}

			result, err = resolveModuleSourceLookupResult(ctx, query, mod.Source, policy)
			if err != nil {
				return fmt.Errorf("module %q source %q: %w", mod.Name, mod.Source, err)
			}
			resultsBySource[mod.Source] = result
		}

		if err := lock.SetModuleResolve(mod.Source, result); err != nil {
			return fmt.Errorf("module %q source %q: %w", mod.Name, mod.Source, err)
		}
	}

	return nil
}

func resolveWorkspaceRefreshModules(cfg *workspace.Config, names []string) ([]workspaceRefreshModule, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one workspace module name is required")
	}

	mods := make([]workspaceRefreshModule, 0, len(names))
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		entry, ok := cfg.Modules[name]
		if !ok {
			missing = append(missing, name)
			continue
		}

		mods = append(mods, workspaceRefreshModule{
			Name:   name,
			Source: entry.Source,
		})
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("workspace module(s) not found: %s", strings.Join(missing, ", "))
	}
	return mods, nil
}

func resolveWorkspaceModuleSourceKind(ctx context.Context, srv *dagql.Server, source string) (core.ModuleSourceKind, error) {
	var kind core.ModuleSourceKind
	if err := srv.Select(ctx, srv.Root(), &kind,
		workspaceInstallModuleSourceSelector(source),
		dagql.Selector{Field: "kind"},
	); err != nil {
		return "", fmt.Errorf("resolve module source kind: %w", err)
	}
	return kind, nil
}

func workspaceRefreshModuleRef(source string) string {
	if core.FastModuleSourceKindCheck(source, "") != core.ModuleSourceKindLocal {
		return source
	}
	return filepath.Join(workspace.LockDirName, source)
}

func (s *workspaceSchema) workspaceLockChangeset(
	ctx context.Context,
	ws *core.Workspace,
	lock *workspace.Lock,
) (*core.Changeset, error) {
	lockBytes, err := lock.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal workspace lock: %w", err)
	}

	baseDir, err := s.resolveRootfs(ctx, ws, resolveWorkspacePath(".", ws.Path), core.CopyFilter{}, false)
	if err != nil {
		return nil, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var updatedDir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, baseDir, &updatedDir,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(path.Join(workspace.LockDirName, workspace.LockFileName))},
				{Name: "contents", Value: dagql.String(lockBytes)},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("workspace lockfile write: %w", err)
	}

	var changes dagql.ObjectResult[*core.Changeset]
	baseDirID, err := baseDir.ID()
	if err != nil {
		return nil, fmt.Errorf("workspace lockfile base directory ID: %w", err)
	}
	if err := srv.Select(ctx, updatedDir, &changes,
		dagql.Selector{
			Field: "changes",
			Args: []dagql.NamedInput{
				{Name: "from", Value: dagql.NewID[*core.Directory](baseDirID)},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("workspace lockfile changeset: %w", err)
	}

	return changes.Self(), nil
}
