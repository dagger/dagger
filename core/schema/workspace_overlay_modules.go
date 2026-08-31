package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
)

// overlayModule is a workspace-config module loaded through the workspace's
// pending overlay rather than the session's served (on-disk) module set.
type overlayModule struct {
	name string
	mod  dagql.ObjectResult[*core.Module]
}

// overlayModuleLoader adapts workspaceOverlayModules to the hook core's
// WorkspaceServedSchema consumes (core.SetWorkspaceOverlayModuleLoader), so the
// schema an overlay-carrying workspace serves — the one the LLM's tools render
// from and dispatch against — reflects the same overlay-resolved modules that
// Workspace.agents composes.
func (s *workspaceSchema) overlayModuleLoader(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
) ([]dagql.ObjectResult[*core.Module], error) {
	overlay, err := s.workspaceOverlayModules(ctx, ws, nil)
	if err != nil {
		return nil, err
	}
	mods := make([]dagql.ObjectResult[*core.Module], 0, len(overlay))
	for _, om := range overlay {
		mods = append(mods, om.mod)
	}
	return mods, nil
}

// workspaceOverlayModules loads the workspace modules that the workspace's
// pending overlay affects, resolving their source through the overlay instead
// of the host checkout.
//
// The session's served modules (client.pendingModules, gathered once at
// workspace detection) are a snapshot of the on-disk workspace: an agent that
// edits a module's source, or installs a module by staging a dagger.toml edit,
// cannot see its own work when the conversation is recomposed
// (Workspace.agents). This resolves the affected entries from
// workspaceOverlayRootfs, which is host + the overlay's changeset, so the
// self-repair loop (edit module -> reload -> new behavior) closes fully
// in-session. The resulting module identity is keyed on the overlay directory's
// ID, so a further edit yields a further reload and an unchanged overlay is a
// cache hit.
//
// Only entries the overlay actually touches are re-resolved; everything else
// keeps using the served module, so a clean workspace (or one whose edits are
// unrelated to any module) behaves exactly as before.
//
// Known limitations, deliberate for now:
//   - an entry REMOVED from dagger.toml in the overlay still resolves through
//     the served module: this only ever adds or replaces.
//   - legacy +defaultPath entries (entry.LegacyDefaultPath) are left to the
//     served path, whose host-ref based context resolution has no overlay
//     equivalent.
func (s *workspaceSchema) workspaceOverlayModules(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	include []string,
) ([]overlayModule, error) {
	ws := parent.Self()
	if ws == nil || ws.ConfigFile == "" {
		return nil, nil
	}
	if _, ok := ws.OverlayChanges(); !ok {
		return nil, nil
	}

	configFile, err := workspaceConfigFile(ws)
	if err != nil {
		return nil, err
	}
	configDir, err := workspaceConfigDirectory(ws)
	if err != nil {
		return nil, err
	}
	// A config edit can add, remove or repoint any entry, so every entry is
	// suspect; otherwise only the entries whose source tree was edited are.
	configTouched := ws.OverlayPathTouched(configFile)

	cfg, err := readWorkspaceConfig(ctx, ws)
	if err != nil {
		return nil, err
	}
	if envName, ok := selectedWorkspaceEnv(ctx); ok {
		cfg, err = workspace.ApplyEnvOverlay(cfg, envName)
		if err != nil {
			return nil, err
		}
	}
	if len(cfg.Modules) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	slices.Sort(names)
	wanted := overlayIncludedModuleNames(names, include)

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var loaded []overlayModule
	for _, name := range names {
		if wanted != nil {
			if _, ok := wanted[canonicalOverlayModuleName(name)]; !ok {
				continue
			}
		}
		entry := cfg.Modules[name]
		// A built-in SDK install entry carries [as-sdk] authoring metadata, not
		// a loadable module ref (see workspaceConfigPendingModules).
		if entry.AsSDK != nil && coresdk.IsBuiltinSDKName(entry.Source) {
			continue
		}
		if entry.LegacyDefaultPath {
			continue
		}

		src, relevant, err := s.workspaceOverlayModuleSource(ctx, srv, parent, entry, configDir, configTouched)
		if err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
		if !relevant {
			continue
		}

		asModuleArgs, err := BuildLegacyAsModuleArgs(
			name,
			false, // legacy +defaultPath entries are skipped above
			"", "",
			entry.Settings,
			cfg.DefaultsFromDotEnv,
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}

		var mod dagql.ObjectResult[*core.Module]
		if err := srv.Select(ctx, src, &mod,
			dagql.Selector{Field: "asModule", Args: asModuleArgs},
		); err != nil {
			return nil, fmt.Errorf("module %q: load from workspace overlay: %w", name, err)
		}
		loaded = append(loaded, overlayModule{name: name, mod: mod})
	}
	return loaded, nil
}

// workspaceOverlayModuleSource resolves one config entry's module source, and
// reports whether the overlay affects it at all. Local entries inside the
// workspace resolve through the overlay rootfs (host + pending edits); entries
// outside it — absolute paths and remote refs — have no overlay representation,
// so they are only re-resolved when the config itself was edited (a freshly
// installed dependency), through the same root moduleSource field the session
// loader uses.
func (s *workspaceSchema) workspaceOverlayModuleSource(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.Workspace],
	entry workspace.ModuleEntry,
	configDir string,
	configTouched bool,
) (src dagql.ObjectResult[*core.ModuleSource], relevant bool, _ error) {
	ws := parent.Self()

	if core.FastModuleSourceKindCheck(entry.Source, "") == core.ModuleSourceKindLocal {
		resolved := workspace.ResolveModuleEntrySource(configDir, entry.Source)
		if !filepath.IsAbs(resolved) {
			if !configTouched && !overlayTouchesTree(ws, resolved) {
				return src, false, nil
			}
			if err := srv.Select(ctx, parent, &src, dagql.Selector{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(workspaceAPIPath(resolved))},
				},
			}); err != nil {
				return src, false, err
			}
			return src, true, nil
		}
		if !configTouched {
			return src, false, nil
		}
		return s.rootModuleSource(ctx, srv, resolved)
	}

	if !configTouched {
		return src, false, nil
	}
	return s.rootModuleSource(ctx, srv, entry.Source)
}

func (s *workspaceSchema) rootModuleSource(
	ctx context.Context,
	srv *dagql.Server,
	ref string,
) (src dagql.ObjectResult[*core.ModuleSource], relevant bool, _ error) {
	if err := srv.Select(ctx, srv.Root(), &src, dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(ref)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
		},
	}); err != nil {
		return src, false, err
	}
	return src, true, nil
}

// overlayIncludedModuleNames maps include patterns ("module" or "module:item")
// onto config module names, mirroring the session loader's narrowing
// (filterPendingWorkspaceModulesBySelectorInclude). A nil result means "don't
// narrow": either no patterns, or a pattern naming nothing known, in which case
// the loader can't tell which module it refers to.
func overlayIncludedModuleNames(names, include []string) map[string]struct{} {
	if len(include) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(names))
	for _, name := range names {
		known[canonicalOverlayModuleName(name)] = struct{}{}
	}
	wanted := make(map[string]struct{}, len(include))
	for _, pattern := range include {
		modName, _, _ := strings.Cut(pattern, ":")
		modName = canonicalOverlayModuleName(modName)
		if _, ok := known[modName]; !ok {
			return nil
		}
		wanted[modName] = struct{}{}
	}
	return wanted
}

func canonicalOverlayModuleName(name string) string {
	return strcase.ToKebab(name)
}

// overlayTouchesTree reports whether any of the overlay's edits fall inside the
// given workspace-relative directory. Workspace.OverlayPathTouched answers the
// other direction — whether a single file is covered by a touched path — which
// alone would miss the common case here: a module source ROOT whose edited file
// (modules/foo/main.dang) is the touched path.
func overlayTouchesTree(ws *core.Workspace, dir string) bool {
	if ws.OverlayPathTouched(dir) {
		return true
	}
	dir = path.Clean(filepath.ToSlash(dir))
	if dir == "." {
		return len(ws.OverlayTouchedPaths()) > 0
	}
	for _, touched := range ws.OverlayTouchedPaths() {
		touched = path.Clean(filepath.ToSlash(touched))
		if touched == dir || strings.HasPrefix(touched, dir+"/") {
			return true
		}
	}
	return false
}

// mergeOverlayModules layers the overlay-resolved modules onto the session's
// served modules: an entry present in both is replaced in place (keeping the
// served order), and entries the session never loaded — a module installed
// mid-conversation — are appended in config order.
func mergeOverlayModules(
	served []dagql.ObjectResult[*core.Module],
	overlay []overlayModule,
) []dagql.ObjectResult[*core.Module] {
	if len(overlay) == 0 {
		return served
	}
	merged := slices.Clone(served)
	for _, om := range overlay {
		if om.mod.Self() == nil {
			continue
		}
		replaced := false
		for i, mod := range merged {
			if mod.Self() == nil {
				continue
			}
			if canonicalOverlayModuleName(mod.Self().Name()) != canonicalOverlayModuleName(om.name) {
				continue
			}
			merged[i] = om.mod
			replaced = true
			break
		}
		if !replaced {
			merged = append(merged, om.mod)
		}
	}
	return merged
}
