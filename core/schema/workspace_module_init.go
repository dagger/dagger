package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// workspaceModuleInitArgs is the schema-facing arg set for
// Workspace.moduleInit. The redesigned shape returns a Changeset rather
// than applying immediately; callers preview and apply via the standard
// changeset path. The SelfCalls arg is gone (the feature graduated to a
// runtime-capability check), and Path is new (workspace-relative, defaults
// to .dagger/modules/<name>).
type workspaceModuleInitArgs struct {
	Name    string
	SDK     string   `default:""`
	Path    string   `default:""`
	Source  string   `default:""`
	Include []string `default:"[]"`
	Here    bool     `default:"false"`
}

// moduleInit builds the workspace edits required to create a new module
// owned by this workspace: codegen output (dagger-module.toml plus
// SDK-generated source) at `path`, the SDK install entry under
// `[sdks.<sdk-name>]`, the authoring entry under
// `[[sdks.<sdk-name>.modules]]`, and — when the default path is used — an
// `[modules.<name>]` install so the new module is callable in this
// workspace.
//
// Every change is staged into one Changeset and returned. No filesystem
// write happens inside this function; the caller previews via
// `handleChangesetResponseAt` (or any other Changeset consumer) and decides
// whether to apply. This eliminates the half-mutated-workspace failure
// window the previous immediate-apply shape inherited.
func (s *workspaceSchema) moduleInit(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceModuleInitArgs,
) (res *core.Changeset, _ error) {
	if args.Name == "" {
		return nil, fmt.Errorf("module name is required")
	}
	if args.SDK == "" {
		return nil, fmt.Errorf("--sdk is required")
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
		return nil, fmt.Errorf("--path %q must be workspace-relative, not absolute", args.Path)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("--path %q must not escape the workspace root", args.Path)
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigInitIfMissing, args.Here)
	if err != nil {
		return nil, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	if cfg.SDKs == nil {
		cfg.SDKs = map[string]workspace.SDKEntry{}
	}

	// Reject name conflicts in installed modules and reject path conflicts
	// across any SDK's authored modules. Two SDKs claiming the same path is
	// a corruption we shouldn't silently extend.
	if _, exists := cfg.Modules[args.Name]; exists {
		return nil, fmt.Errorf("module %q is already installed in this workspace", args.Name)
	}
	for sdkName, sdkEntry := range cfg.SDKs {
		for _, m := range sdkEntry.Modules {
			if m.Path == relPath {
				return nil, fmt.Errorf("a module is already authored at %q under sdks.%s", relPath, sdkName)
			}
		}
	}

	sdkName := workspace.ConventionalSDKShortName(args.SDK)
	sdkEntry, sdkInstalled := cfg.SDKs[sdkName]
	if !sdkInstalled {
		sdkEntry = workspace.SDKEntry{Source: args.SDK}
	}
	sdkEntry.Modules = append(sdkEntry.Modules, workspace.SDKManagedModule{Path: relPath})
	cfg.SDKs[sdkName] = sdkEntry

	// Resolve which engine runtime ref the new module should declare. The
	// runtime/SDK split allows an SDK to *delegate* execution to a separate
	// runtime module by exposing a `targetRuntime: String!` field. When the
	// field isn't declared, the SDK module IS the runtime — its own
	// installed ref serves as the runtime ref. That's the common case today
	// (every shipped SDK does codegen + runtime in one module).
	runtimeRef, err := s.resolveModuleRuntimeRef(ctx, args.SDK)
	if err != nil {
		return nil, err
	}

	if usingDefaultPath {
		cfg.Modules[args.Name] = workspace.ModuleEntry{Source: relPath}
	}

	// Render new dagger.toml bytes through the format-preserving editor.
	existingConfigBytes, err := readConfigBytes(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("read workspace config: %w", err)
	}
	newConfigBytes, err := workspace.UpdateConfigBytes(existingConfigBytes, cfg)
	if err != nil {
		return nil, fmt.Errorf("update workspace config: %w", err)
	}

	// Generate the new module's context directory (dagger-module.toml +
	// SDK-emitted source). The diff is workspace-root-relative because the
	// moduleSource is constructed against the local workspace context.
	moduleDiff, err := s.workspaceModuleInitGeneratedDiff(ctx, args, relPath, runtimeRef)
	if err != nil {
		return nil, err
	}

	configRelPath, err := workspaceConfigFile(parent)
	if err != nil {
		return nil, err
	}

	// Layer the workspace edits onto the workspace rootfs and compute the
	// resulting Changeset. Operation order matters only for legibility — the
	// diff is computed at the end.
	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace rootfs: %w", err)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes, 0o644)
	if err != nil {
		return nil, fmt.Errorf("stage workspace config update: %w", err)
	}
	updatedDir, err = workspaceWithDirectoryOverlay(ctx, dag, updatedDir, moduleDiff)
	if err != nil {
		return nil, fmt.Errorf("stage module generated context: %w", err)
	}

	return workspaceMigrationChanges(ctx, updatedDir, baseDir)
}

// workspaceModuleInitGeneratedDiff drives the moduleSource codegen chain
// for the new module and returns the resulting context-directory diff,
// suitable for overlaying onto the workspace rootfs. runtimeRef is what
// gets recorded as the module's `runtime` field on disk; it may differ
// from args.SDK when the SDK delegates execution to a separate runtime
// module (see resolveModuleRuntimeRef).
func (s *workspaceSchema) workspaceModuleInitGeneratedDiff(
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
		dagql.Selector{Field: "generatedContextDirectory"},
	)

	if err := srv.Select(ctx, srv.Root(), &res, selectors...); err != nil {
		return res, fmt.Errorf("generate module context: %w", err)
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
				{Name: "directory", Value: dagql.NewID[*core.Directory](srcID)},
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
// Implementation note: the full lookup requires loading the SDK as a
// callable module and selecting `targetRuntime` on its main object. That
// dagql chain isn't wired here yet — no SDK currently exposes the field,
// so adding the call would be untested speculation. The function signature
// is the integration seam; when an SDK opts into the runtime/SDK split,
// the body of this function is where the moduleSource → asModule → select
// chain lands.
func (s *workspaceSchema) lookupSDKTargetRuntime(_ context.Context, _ string) (string, bool) {
	return "", false
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
