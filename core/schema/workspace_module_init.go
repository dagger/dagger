package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	coresdk "github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceInitModuleArgs struct {
	Name       string
	SDK        string
	Path       string    `default:""`
	Source     string    `default:""`
	Include    []string  `default:"[]"`
	Args       core.JSON `default:""`
	Here       bool      `default:"false"`
	NoGenerate bool      `default:"false"`
}

// initModuleChanges builds the workspace edits required to create a new module
// owned by this workspace: the module config file (dagger-module.toml) at
// `path`, the authoring entry under `[[modules.<sdk>.as-sdk.modules]]`, and —
// when the default path is used — an `[modules.<name>]` install for the new
// module so it's callable in this workspace. The SDK must already be
// installed as an SDK; init is dispatch, not install.
//
// The engine's changeset is deliberately limited to engine-owned config
// (the workspace dagger.toml and the module's dagger-module.toml). All other
// files in the new module directory — starter source, the generated client,
// language scaffolding — come from the SDK's `initModule` Changeset, which is
// merged in at the end. The two are disjoint by construction, so the merge
// never conflicts on a shared path.
//
// Every change is staged into one Changeset and returned. No filesystem write
// happens inside this function.
//
// The returned initScope names what was created, so the caller can generate for
// exactly it (see workspaceSchema.withScopedGeneration).
//
//nolint:gocyclo // inherently branchy orchestration (validate args, resolve SDK, plan changes)
func (s *workspaceSchema) initModuleChanges(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInitModuleArgs,
) (res dagql.ObjectResult[*core.Changeset], scope initScope, _ error) {
	ws := parent.Self()
	if args.Name == "" {
		return res, scope, fmt.Errorf("module name is required")
	}
	if args.SDK == "" {
		return res, scope, fmt.Errorf("SDK name is required")
	}

	// --path reads like every other workspace path a user types: relative to
	// where they are standing, with a leading "/" meaning the workspace root
	// (resolveWorkspacePath, the same resolver `dagger install` uses for a
	// local ref). Everything downstream of here is workspace-root-relative.
	// Resolved before the config is read so a bad path is reported as a bad
	// path, not as whatever else the workspace's dagger.toml turns out to be
	// missing or malformed about.
	var relPath string
	usingDefaultPath := args.Path == ""
	if !usingDefaultPath {
		var err error
		relPath, err = resolveWorkspacePath(args.Path, ws.Cwd)
		if err != nil {
			return res, scope, fmt.Errorf("--path %q must not escape the workspace root", args.Path)
		}
	}

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, args.Here)
	if err != nil {
		return res, scope, err
	}

	// An empty --path means the default layout, which is also the signal to
	// auto-install in [modules.*]. The default is anchored at the directory
	// holding the dagger.toml being edited, not at the workspace root: a config
	// in a subdirectory records module sources relative to itself, so
	// scaffolding anywhere else would write an entry pointing outside its own
	// project. ConfigDir is already a clean path inside the root, so the join
	// cannot escape.
	if usingDefaultPath {
		relPath = filepath.Join(staged.ConfigDir, ".dagger", "modules", args.Name)
	}

	cfg := staged.Config
	sdkName, sdkEntry, sdkRef, err := installedSDKSource(cfg, args.SDK)
	if err != nil {
		return res, scope, err
	}

	// Everything in dagger.toml is written relative to the directory holding
	// it, while relPath is workspace-root-relative like the rest of the engine.
	configPath, err := workspace.SDKManagedPathFor(staged.ConfigDir, relPath)
	if err != nil {
		return res, scope, err
	}

	// Reject name conflicts in installed modules and reject path conflicts
	// across any SDK's authored modules. Two SDKs claiming the same path is
	// a corruption we shouldn't silently extend.
	if _, exists := cfg.Modules[args.Name]; exists {
		return res, scope, fmt.Errorf("module %q is already installed in this workspace", args.Name)
	}
	for installedName, installed := range cfg.Modules {
		if installed.AsSDK == nil {
			continue
		}
		for _, m := range installed.AsSDK.Modules {
			authored, err := workspace.ResolveSDKManagedPath(staged.ConfigDir, m.Path)
			if err != nil {
				return res, scope, fmt.Errorf("module managed by %q: %w", installedName, err)
			}
			if authored == relPath {
				return res, scope, fmt.Errorf("a module is already authored at %q under modules.%s.as-sdk", relPath, installedName)
			}
		}
	}

	sdkEntry.AsSDK.Modules = append(sdkEntry.AsSDK.Modules, workspace.SDKManagedModule{Path: configPath})
	cfg.Modules[sdkName] = sdkEntry

	loadedSDK, err := s.loadWorkspaceSDK(ctx, ws, staged.ConfigDir, sdkRef)
	if err != nil {
		return res, scope, err
	}

	// By default the SDK module is also the runtime. SDKs that split authoring
	// from execution advertise RuntimeTarget and provide the runtime ref.
	defaultRuntimeRef, err := moduleEntrySourceWithPinRelativeTo(staged.ConfigDir, relPath, sdkEntry)
	if err != nil {
		return res, scope, err
	}
	runtimeRef := defaultRuntimeRef
	if target, ok := loadedSDK.AsRuntimeTarget(); ok {
		runtimeRef, err = target.TargetRuntime(ctx)
		if err != nil {
			return res, scope, fmt.Errorf("resolve SDK target runtime: %w", err)
		}
		if runtimeRef == "" {
			return res, scope, fmt.Errorf("SDK target runtime is empty")
		}
	}

	if usingDefaultPath {
		cfg.Modules[args.Name] = workspace.ModuleEntry{Source: configPath}
	}

	// Render new dagger.toml bytes through the format-preserving editor.
	newConfigBytes, err := workspace.UpdateConfigBytes(staged.Data, cfg)
	if err != nil {
		return res, scope, fmt.Errorf("update workspace config: %w", err)
	}

	// Generate the new module's config file (dagger-module.toml) ONLY. The
	// engine owns the module config; the SDK's initModule (merged in below)
	// owns every other file in the module directory — starter source, the
	// generated client, language scaffolding, etc. Emitting just the config
	// here keeps the engine and SDK changesets disjoint by construction, so
	// they can never collide on a shared path ("added in both changesets").
	// The diff is workspace-root-relative because the moduleSource is
	// constructed against the local workspace context.
	// Layer the workspace edits onto the workspace rootfs and compute the
	// resulting Changeset. Operation order matters only for legibility — the
	// diff is computed at the end.
	baseDir, err := s.workspaceOverlayRootfs(ctx, ws)
	if err != nil {
		return res, scope, fmt.Errorf("resolve workspace rootfs: %w", err)
	}
	moduleDiff, err := s.workspaceModuleInitConfigDiff(ctx, baseDir, args, relPath, runtimeRef)
	if err != nil {
		return res, scope, err
	}
	configRelPath := staged.ConfigFile

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, scope, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes)
	if err != nil {
		return res, scope, fmt.Errorf("stage workspace config update: %w", err)
	}
	updatedDir, err = workspaceWithDirectoryOverlay(ctx, dag, updatedDir, moduleDiff)
	if err != nil {
		return res, scope, fmt.Errorf("stage module generated context: %w", err)
	}

	engineChanges, err := workspaceMigrationChanges(ctx, updatedDir, baseDir)
	if err != nil {
		return res, scope, err
	}

	sdkArgs, err := coresdk.DecodeInitArgs(args.Args)
	if err != nil {
		return res, scope, err
	}
	moduleInitializer, ok := loadedSDK.AsModuleInitializer()
	if !ok {
		return res, scope, fmt.Errorf("%q does not support module init", args.SDK)
	}
	sdkWorkspace, err := rootAnchoredWorkspace(ctx, parent)
	if err != nil {
		return res, scope, err
	}
	sdkChanges, err := moduleInitializer.InitModule(ctx, sdkWorkspace, args.Name, relPath, sdkArgs)
	if err != nil {
		return res, scope, fmt.Errorf("sdk module init: %w", err)
	}

	// Enforce the ownership split: the SDK's initModule must not touch the
	// engine-owned config files. Catching it here yields a clear, actionable
	// error instead of a cryptic "added in both changesets" from the merge
	// below (or, worse, an SDK silently clobbering config the engine wrote).
	if err := validateSDKInitChangesetOwnership(ctx, args.SDK, sdkChanges, configRelPath, relPath); err != nil {
		return res, scope, err
	}

	merged, err := mergeWorkspaceInitChangeset(ctx, engineChanges, sdkChanges)
	if err != nil {
		return res, scope, err
	}

	// Changesets are merged through Git, which does not track directory modes.
	// A newly added directory is therefore recreated using the engine process's
	// umask instead of retaining the mode staged by the engine or SDK. Normalize
	// only the module root here: it is owned by module init, while directories
	// below it remain owned by the SDK and keep their explicitly authored modes.
	res, err = changesetWithDirectoryMode(ctx, merged, relPath, 0o755)
	if err != nil {
		return res, scope, err
	}
	return res, initScope{sdk: sdkName, path: relPath}, nil
}

// changesetWithDirectoryMode returns changes with path's mode explicitly set
// in the after snapshot. Overlaying an empty directory changes only the root
// directory metadata and preserves all files and child-directory modes.
func changesetWithDirectoryMode(
	ctx context.Context,
	changes dagql.ObjectResult[*core.Changeset],
	path string,
	mode int,
) (dagql.ObjectResult[*core.Changeset], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return changes, fmt.Errorf("dagql server: %w", err)
	}

	var empty dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, srv.Root(), &empty, dagql.Selector{Field: "directory"}); err != nil {
		return changes, fmt.Errorf("create empty directory: %w", err)
	}
	emptyID, err := empty.ID()
	if err != nil {
		return changes, fmt.Errorf("empty directory ID: %w", err)
	}

	var after dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, changes.Self().After, &after, dagql.Selector{
		Field: "withDirectory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(filepath.ToSlash(path))},
			{Name: "source", Value: dagql.NewID[*core.Directory](emptyID)},
			{Name: "permissions", Value: dagql.Opt(dagql.Int(mode))},
		},
	}); err != nil {
		return changes, fmt.Errorf("set directory mode for %q: %w", path, err)
	}

	beforeID, err := changes.Self().Before.ID()
	if err != nil {
		return changes, fmt.Errorf("changeset before ID: %w", err)
	}
	var normalized dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, after, &normalized, dagql.Selector{
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)},
		},
	}); err != nil {
		return changes, fmt.Errorf("rebuild changeset with directory mode: %w", err)
	}
	return normalized, nil
}

// validateSDKInitChangesetOwnership enforces the CLI-1.0 ownership split: the
// engine owns the workspace config (dagger.toml) and the module config
// (dagger-module.toml); the SDK's initModule owns every other file in the
// module directory. An SDK that writes an engine-owned config would otherwise
// collide in the merge as a cryptic "added in both changesets" — or silently
// overwrite the config the engine just generated — so reject it up front.
func validateSDKInitChangesetOwnership(
	ctx context.Context,
	sdkName string,
	sdkChanges dagql.ObjectResult[*core.Changeset],
	workspaceConfigPath string,
	modulePath string,
) error {
	if sdkChanges.Self() == nil {
		return nil
	}
	paths, err := sdkChanges.Self().ComputePaths(ctx)
	if err != nil {
		return fmt.Errorf("inspect sdk init changeset: %w", err)
	}

	engineOwned := map[string]string{
		filepath.Clean(workspaceConfigPath):                             "workspace config",
		filepath.Join(modulePath, workspace.ModuleConfigFileName):       "module config",
		filepath.Join(modulePath, workspace.LegacyModuleConfigFileName): "module config",
	}

	var touched []string
	for _, p := range slices.Concat(paths.Added, paths.Modified) {
		if label, ok := engineOwned[filepath.Clean(p)]; ok {
			touched = append(touched, fmt.Sprintf("%s (%s)", filepath.Clean(p), label))
		}
	}
	slices.Sort(touched)
	touched = slices.Compact(touched)
	if len(touched) > 0 {
		return fmt.Errorf(
			"sdk %q initModule must not modify engine-owned file(s): %s",
			sdkName, strings.Join(touched, ", "),
		)
	}
	return nil
}

// workspaceModuleInitConfigDiff builds the new module's config file
// (dagger-module.toml) and returns it as a context-directory diff suitable
// for overlaying onto the workspace rootfs. It deliberately does NOT run
// codegen: per the CLI-1.0 contract the engine owns only the config file
// (dagger.toml + dagger-module.toml), while the SDK's initModule owns the
// rest of the module directory. Serializing the engine-owned config directly
// keeps this diff to one file and prevents it from overlapping the SDK's
// output. runtimeRef is what gets recorded as the module's `runtime` field;
// it may differ from args.SDK when the SDK delegates execution to a separate
// runtime module (see resolveModuleRuntimeRef).
func (s *workspaceSchema) workspaceModuleInitConfigDiff(
	ctx context.Context,
	workspaceRoot dagql.ObjectResult[*core.Directory],
	args workspaceInitModuleArgs,
	relPath string,
	runtimeRef string,
) (dagql.ObjectResult[*core.Directory], error) {
	var res dagql.ObjectResult[*core.Directory]
	statFS := &core.DirectoryStatFS{Dir: workspaceRoot}
	for _, filename := range []string{workspace.ModuleConfigFileName, workspace.LegacyModuleConfigFileName} {
		_, exists, err := core.StatFSExists(ctx, statFS, filepath.Join(relPath, filename))
		if err != nil {
			return res, fmt.Errorf("check existing module at %q: %w", relPath, err)
		}
		if exists {
			return res, fmt.Errorf("a module already exists at %q", relPath)
		}
	}

	source := args.Source
	if source == "." {
		source = ""
	}
	config, err := modules.MarshalModuleConfigForFilename(&modules.ModuleConfigWithUserFields{
		ModuleConfig: modules.ModuleConfig{
			Name:          args.Name,
			EngineVersion: engine.Version,
			SDK:           &modules.SDK{Source: runtimeRef},
			Source:        source,
			Include:       append([]string(nil), args.Include...),
		},
	}, workspace.ModuleConfigFileName)
	if err != nil {
		return res, fmt.Errorf("marshal module config: %w", err)
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("dagql server: %w", err)
	}
	if err := srv.Select(ctx, srv.Root(), &res,
		dagql.Selector{Field: "directory"},
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(filepath.ToSlash(filepath.Join(relPath, workspace.ModuleConfigFileName)))},
				{Name: "contents", Value: dagql.String(config)},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		},
	); err != nil {
		return res, fmt.Errorf("generate module config: %w", err)
	}
	return res, nil
}

// workspaceWithFile overlays a single file with the given workspace-relative
// path onto dir.
func workspaceWithFile(
	ctx context.Context,
	dag *dagql.Server,
	dir dagql.ObjectResult[*core.Directory],
	path string,
	contents []byte,
) (dagql.ObjectResult[*core.Directory], error) {
	var out dagql.ObjectResult[*core.Directory]
	err := dag.Select(ctx, dir, &out,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(path)},
				{Name: "contents", Value: dagql.String(contents)},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		},
	)
	return out, err
}

// workspaceWithDirectoryOverlay overlays src onto dir at the workspace root,
// effectively merging the diff Directory's contents into the workspace.
func workspaceWithDirectoryOverlay(
	ctx context.Context,
	dag *dagql.Server,
	dir dagql.ObjectResult[*core.Directory],
	src dagql.ObjectResult[*core.Directory],
) (dagql.ObjectResult[*core.Directory], error) {
	srcID, err := src.ID()
	if err != nil {
		return dir, fmt.Errorf("source directory id: %w", err)
	}
	var out dagql.ObjectResult[*core.Directory]
	err = dag.Select(ctx, dir, &out,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(".")},
				{Name: "source", Value: dagql.NewID[*core.Directory](srcID)},
			},
		},
	)
	return out, err
}
