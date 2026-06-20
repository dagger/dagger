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
)

// workspaceModuleInitArgs is the schema-facing arg set for
// Workspace.moduleInit. The redesigned shape returns a Changeset rather
// than applying immediately; callers preview and apply via the standard
// changeset path. The SelfCalls arg is gone (the feature graduated to a
// runtime-capability check), and Path is new (workspace-relative, defaults
// to .dagger/modules/<name>). SDK is the user-facing SDK name from
// [modules.<name>.as-sdk] or the module entry name, not a module source ref.
type workspaceModuleInitArgs struct {
	Name    string
	SDK     string    `default:""`
	Path    string    `default:""`
	Source  string    `default:""`
	Include []string  `default:"[]"`
	Args    core.JSON `default:""`
	Here    bool      `default:"false"`
}

// moduleInit builds the workspace edits required to create a new module
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
// Every change is staged into one Changeset and returned. No filesystem
// write happens inside this function; the caller previews via
// `handleChangesetResponseAt` (or any other Changeset consumer) and decides
// whether to apply. This eliminates the half-mutated-workspace failure
// window the previous immediate-apply shape inherited.
//
//nolint:gocyclo // inherently branchy orchestration (validate args, resolve SDK, plan changes)
func (s *workspaceSchema) moduleInit(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceModuleInitArgs,
) (res dagql.ObjectResult[*core.Changeset], _ error) {
	if args.Name == "" {
		return res, fmt.Errorf("module name is required")
	}
	if args.SDK == "" {
		return res, fmt.Errorf("SDK name is required")
	}

	// Resolve the workspace-relative path for the new module. Empty = default
	// layout; we treat that as the signal to auto-install in [modules.*].
	relPath := args.Path
	usingDefaultPath := relPath == ""
	if usingDefaultPath {
		relPath = filepath.Join(".dagger", "modules", args.Name)
	}
	relPath = filepath.Clean(relPath)
	if filepath.IsAbs(relPath) {
		return res, fmt.Errorf("--path %q must be workspace-relative, not absolute", args.Path)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return res, fmt.Errorf("--path %q must not escape the workspace root", args.Path)
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigMustExist, args.Here)
	if err != nil {
		return res, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	sdkName, sdkEntry, sdkRef, err := installedSDKSource(cfg, args.SDK)
	if err != nil {
		return res, err
	}

	// Reject name conflicts in installed modules and reject path conflicts
	// across any SDK's authored modules. Two SDKs claiming the same path is
	// a corruption we shouldn't silently extend.
	if _, exists := cfg.Modules[args.Name]; exists {
		return res, fmt.Errorf("module %q is already installed in this workspace", args.Name)
	}
	for installedName, installed := range cfg.Modules {
		if installed.AsSDK == nil {
			continue
		}
		for _, m := range installed.AsSDK.Modules {
			if m.Path == relPath {
				return res, fmt.Errorf("a module is already authored at %q under modules.%s.as-sdk", relPath, installedName)
			}
		}
	}

	sdkEntry.AsSDK.Modules = append(sdkEntry.AsSDK.Modules, workspace.SDKManagedModule{Path: relPath})
	cfg.Modules[sdkName] = sdkEntry

	// Resolve which engine runtime ref the new module should declare. The
	// runtime/SDK split allows an SDK to *delegate* execution to a separate
	// runtime module by exposing a `targetRuntime: String!` field. When the
	// field isn't declared, the SDK module IS the runtime — its own
	// installed ref serves as the runtime ref. That's the common case today
	// (every shipped SDK does codegen + runtime in one module).
	runtimeRef, err := s.resolveModuleRuntimeRef(ctx, sdkRef)
	if err != nil {
		return res, err
	}

	if usingDefaultPath {
		cfg.Modules[args.Name] = workspace.ModuleEntry{Source: relPath}
	}

	// Render new dagger.toml bytes through the format-preserving editor.
	existingConfigBytes, err := readConfigBytes(ctx, parent)
	if err != nil {
		return res, fmt.Errorf("read workspace config: %w", err)
	}
	newConfigBytes, err := workspace.UpdateConfigBytes(existingConfigBytes, cfg)
	if err != nil {
		return res, fmt.Errorf("update workspace config: %w", err)
	}

	// Generate the new module's config file (dagger-module.toml) ONLY. The
	// engine owns the module config; the SDK's initModule (merged in below)
	// owns every other file in the module directory — starter source, the
	// generated client, language scaffolding, etc. Emitting just the config
	// here keeps the engine and SDK changesets disjoint by construction, so
	// they can never collide on a shared path ("added in both changesets").
	// The diff is workspace-root-relative because the moduleSource is
	// constructed against the local workspace context.
	moduleDiff, err := s.workspaceModuleInitConfigDiff(ctx, args, relPath, runtimeRef)
	if err != nil {
		return res, err
	}

	configRelPath, err := workspaceConfigFile(parent)
	if err != nil {
		return res, err
	}

	// Layer the workspace edits onto the workspace rootfs and compute the
	// resulting Changeset. Operation order matters only for legibility — the
	// diff is computed at the end.
	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return res, fmt.Errorf("resolve workspace rootfs: %w", err)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes, 0o644)
	if err != nil {
		return res, fmt.Errorf("stage workspace config update: %w", err)
	}
	updatedDir, err = workspaceWithDirectoryOverlay(ctx, dag, updatedDir, moduleDiff)
	if err != nil {
		return res, fmt.Errorf("stage module generated context: %w", err)
	}

	engineChanges, err := workspaceMigrationChanges(ctx, updatedDir, baseDir)
	if err != nil {
		return res, err
	}

	sdkArgs, err := coresdk.DecodeInitArgs(args.Args)
	if err != nil {
		return res, err
	}
	loadedSDK, err := s.loadWorkspaceSDK(ctx, sdkRef)
	if err != nil {
		return res, err
	}
	moduleInitializer, ok := loadedSDK.AsModuleInitializer()
	if !ok {
		return res, fmt.Errorf("%q does not support module init", args.SDK)
	}
	workspaceObj, err := s.currentWorkspaceObject(ctx)
	if err != nil {
		return res, err
	}
	sdkChanges, err := moduleInitializer.InitModule(ctx, workspaceObj, args.Name, relPath, sdkArgs)
	if err != nil {
		return res, fmt.Errorf("sdk module init: %w", err)
	}

	// Enforce the ownership split: the SDK's initModule must not touch the
	// engine-owned config files. Catching it here yields a clear, actionable
	// error instead of a cryptic "added in both changesets" from the merge
	// below (or, worse, an SDK silently clobbering config the engine wrote).
	if err := validateSDKInitChangesetOwnership(ctx, args.SDK, sdkChanges, configRelPath, relPath); err != nil {
		return res, err
	}

	return mergeWorkspaceInitChangeset(ctx, engineChanges, sdkChanges)
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
// rest of the module directory. It therefore selects updatedConfigDirectory
// (config-only) rather than generatedContextDirectory (full codegen pass),
// so the engine's changeset stays a single file and never overlaps the
// SDK's. runtimeRef is what gets recorded as the module's `runtime` field on
// disk; it may differ from args.SDK when the SDK delegates execution to a
// separate runtime module (see resolveModuleRuntimeRef).
func (s *workspaceSchema) workspaceModuleInitConfigDiff(
	ctx context.Context,
	args workspaceModuleInitArgs,
	relPath string,
	runtimeRef string,
) (dagql.ObjectResult[*core.Directory], error) {
	var res dagql.ObjectResult[*core.Directory]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("dagql server: %w", err)
	}

	baseSelector := workspaceModuleInitSourceSelector(relPath)

	var configExists dagql.Boolean
	if err := srv.Select(ctx, srv.Root(), &configExists,
		baseSelector,
		dagql.Selector{Field: "configExists"},
	); err != nil {
		return res, fmt.Errorf("check existing module at %q: %w", relPath, err)
	}
	if bool(configExists) {
		return res, fmt.Errorf("a module already exists at %q", relPath)
	}

	selectors := []dagql.Selector{
		baseSelector,
		{Field: "withName", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(args.Name)}}},
		{Field: "withSDK", Args: []dagql.NamedInput{{Name: "source", Value: dagql.String(runtimeRef)}}},
	}
	if args.Source != "" {
		selectors = append(selectors, dagql.Selector{
			Field: "withSourceSubpath",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(args.Source)}},
		})
	}
	if len(args.Include) > 0 {
		patterns := make(dagql.ArrayInput[dagql.String], len(args.Include))
		for i, include := range args.Include {
			patterns[i] = dagql.String(include)
		}
		selectors = append(selectors, dagql.Selector{
			Field: "withIncludes",
			Args:  []dagql.NamedInput{{Name: "patterns", Value: patterns}},
		})
	}
	selectors = append(selectors,
		dagql.Selector{
			Field: "withEngineVersion",
			Args:  []dagql.NamedInput{{Name: "version", Value: dagql.String(modules.EngineVersionLatest)}},
		},
		// updatedConfigDirectory writes only the module config file and diffs
		// it against the (empty) source context, so the result is exactly
		// {<relPath>/dagger-module.toml}. Using generatedContextDirectory here
		// would instead run a full SDK codegen pass and emit the starter
		// source + generated client too, which then collides with the SDK's
		// own initModule output during the merge below.
		dagql.Selector{Field: "updatedConfigDirectory"},
	)

	if err := srv.Select(ctx, srv.Root(), &res, selectors...); err != nil {
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
	mode int,
) (dagql.ObjectResult[*core.Directory], error) {
	var out dagql.ObjectResult[*core.Directory]
	err := dag.Select(ctx, dir, &out,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(path)},
				{Name: "contents", Value: dagql.String(contents)},
				{Name: "permissions", Value: dagql.Int(mode)},
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

// resolveModuleRuntimeRef returns the runtime ref to record in the new
// module's dagger-module.toml given the SDK ref the caller picked.
//
// Resolution order:
//
//  1. Load the SDK module and look for a `targetRuntime: String!` field on
//     its main object. If present and non-empty, use its value. This is the
//     decoupled case: the SDK and the runtime are different modules; the
//     SDK declares which runtime its codegen targets.
//
//  2. Otherwise default to sdkRef itself. The SDK module IS the runtime —
//     this is the common case today and requires no SDK-side declaration.
//
// The introspection path is intentionally fail-soft: any error during
// the lookup falls through to the default. The override exists so SDKs
// CAN opt into the split, not as a load-bearing dependency. No SDK
// currently exposes `targetRuntime`, so today every call resolves via
// the default; the override hook waits dormant for the first opt-in.
func (s *workspaceSchema) resolveModuleRuntimeRef(ctx context.Context, sdkRef string) (string, error) {
	if sdkRef == "" {
		return "", fmt.Errorf("cannot resolve runtime ref from empty SDK ref")
	}

	if override, ok := s.lookupSDKTargetRuntime(ctx, sdkRef); ok && override != "" {
		return override, nil
	}
	return sdkRef, nil
}

// lookupSDKTargetRuntime attempts to read the SDK module's optional
// `targetRuntime` field. Returns ("", false) when the field is absent or
// any step of the lookup fails — callers fall back to the SDK ref itself.
//
// Resolution chain: load the SDK module via the standard SDK loader, ask
// for the RuntimeTarget capability, and if implemented, call it. Fail-soft
// on every error path — the override is opt-in, not load-bearing, so any
// failure to load or call the field falls through to the default and the
// caller records the SDK's own ref as the runtime.
func (s *workspaceSchema) lookupSDKTargetRuntime(ctx context.Context, sdkRef string) (string, bool) {
	loaded, err := s.loadWorkspaceSDK(ctx, sdkRef)
	if err != nil {
		return "", false
	}
	target, ok := loaded.AsRuntimeTarget()
	if !ok {
		return "", false
	}
	ref, err := target.TargetRuntime(ctx)
	if err != nil || ref == "" {
		return "", false
	}
	return ref, true
}

func workspaceModuleInitSourceSelector(refPath string) dagql.Selector {
	return dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(refPath)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
			{Name: "allowNotExists", Value: dagql.Boolean(true)},
			{Name: "requireKind", Value: dagql.Opt(core.ModuleSourceKindLocal)},
		},
	}
}
