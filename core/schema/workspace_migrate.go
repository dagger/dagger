package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

type workspaceMigrationProgressContextKey struct{}

type workspaceMigrationPlanBundle struct {
	WorkspacePlans          []*workspace.MigrationPlan
	ParentPlans             []workspaceMigrationParentPlan
	ModuleConfigConversions []workspaceMigrationModuleConfigConversion
}

type workspaceMigrationLegacyLockMove struct {
	SourcePath string
	TargetPath string
	Data       []byte
}

const workspaceMigrationLockModulesResolveOperation = "modules.resolve"

func (plans workspaceMigrationPlanBundle) empty() bool {
	return len(plans.WorkspacePlans) == 0 &&
		len(plans.ParentPlans) == 0 &&
		len(plans.ModuleConfigConversions) == 0
}

func (s *workspaceSchema) migrate(
	ctx context.Context,
	ws *core.Workspace,
	args struct{},
) (migration *core.WorkspaceMigration, rerr error) {
	if ws.HostPath() == "" {
		return nil, fmt.Errorf("workspace migration is local-only")
	}

	emptyChanges, err := core.NewEmptyChangeset(ctx)
	if err != nil {
		return nil, err
	}

	if ws.ConfigFile != "" {
		// FIXME(workspace-migrate): Existing workspace config is treated as an
		// explicit opt-in, so migration does not scan for legacy child
		// dagger.json files below it yet.
		return &core.WorkspaceMigration{
			Changes: emptyChanges,
			Steps:   nil,
		}, nil
	}

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}

	query, err := core.CurrentQuery(workspaceCtx)
	if err != nil {
		return nil, err
	}

	showProgress := shouldRecordWorkspaceMigrationProgress(ctx, query, ws)
	ctx = context.WithValue(ctx, workspaceMigrationProgressContextKey{}, showProgress)
	if showProgress {
		var span trace.Span
		ctx, span = core.Tracer(ctx).Start(ctx, "prepare migration diff")
		defer telemetry.EndWithCause(span, &rerr)
	}

	compatWorkspaces, discoveryWarnings, err := s.workspaceMigrationCompatWorkspaces(ctx, ws)
	if err != nil {
		return nil, err
	}
	plans := make([]*workspace.MigrationPlan, 0, len(compatWorkspaces))
	for _, compatWorkspace := range compatWorkspaces {
		// Discovered local modules are converted in place; never route them
		// through PlanMigration, which would move and delete their dagger.json
		// and break the reference that pointed at them.
		if !compatWorkspace.MustMigrateToWorkspaceConfig() || compatWorkspace.DiscoveredLocalModule {
			continue
		}

		plan, err := s.prepareWorkspaceMigrationPlan(ctx, compatWorkspace)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}

	parentPlans, err := workspaceMigrationParentPlansForPlainModules(ws, compatWorkspaces, plans)
	if err != nil {
		return nil, err
	}
	moduleConfigConversions, err := workspaceMigrationModuleConfigConversions(compatWorkspaces)
	if err != nil {
		return nil, err
	}
	parentPlans, err = workspaceMigrationInstallDiscoveredModuleSDKs(plans, parentPlans, compatWorkspaces)
	if err != nil {
		return nil, err
	}
	planBundle := workspaceMigrationPlanBundle{
		WorkspacePlans:          plans,
		ParentPlans:             parentPlans,
		ModuleConfigConversions: moduleConfigConversions,
	}
	warnings := workspaceMigrationPlanBundleWarnings(planBundle)
	warnings = append(warnings, discoveryWarnings...)

	if planBundle.empty() {
		return &core.WorkspaceMigration{
			Changes: emptyChanges,
			Steps:   nil,
		}, nil
	}

	changes, err := s.workspaceMigrationChangeset(ctx, ws, planBundle)
	if err != nil {
		return nil, err
	}

	return &core.WorkspaceMigration{
		Changes: changes,
		Steps: []*core.WorkspaceMigrationStep{
			{
				Code:        "legacy-dagger-json",
				Description: "Migrated to workspace format",
				Warnings:    warnings,
				Changes:     changes,
			},
		},
	}, nil
}

func (s *workspaceSchema) prepareWorkspaceMigrationPlan(
	ctx context.Context,
	compatWorkspace *workspace.CompatWorkspace,
) (*workspace.MigrationPlan, error) {
	plan, err := workspace.PlanMigration(compatWorkspace)
	if err != nil {
		return nil, err
	}
	recordWorkspaceMigrationModuleSpans(ctx, compatWorkspace.Modules)
	return plan, nil
}

func (s *workspaceSchema) workspaceMigrationCompatWorkspaces(
	ctx context.Context,
	ws *core.Workspace,
) ([]*workspace.CompatWorkspace, []string, error) {
	if ws.HostPath() == "" {
		return nil, nil, fmt.Errorf("workspace migration is local-only")
	}

	rootDir, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{
		Exclude: []string{
			".git",
			".git/**",
			"**/.git",
			"**/.git/**",
		},
	}, false)
	if err != nil {
		return nil, nil, err
	}
	configPaths, discoveryWarnings, err := workspaceMigrationLegacyConfigPaths(ctx, rootDir, ws)
	if err != nil {
		return nil, nil, err
	}

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace client context: %w", err)
	}
	query, err := core.CurrentQuery(workspaceCtx)
	if err != nil {
		return nil, nil, err
	}
	bk, err := query.Engine(workspaceCtx)
	if err != nil {
		return nil, nil, fmt.Errorf("engine client: %w", err)
	}
	statFS := core.NewCallerStatFS(bk)

	compatWorkspaces := make([]*workspace.CompatWorkspace, 0, len(configPaths))
	for _, cp := range configPaths {
		// A locally-referenced module may legitimately live under a hidden
		// directory (e.g. ./.internal/foo); only filter hidden paths for the
		// selected/glob discovery set, not for explicit dependency references.
		if !cp.DiscoveredLocalModule && workspaceMigrationHiddenPath(cp.Path) {
			continue
		}
		configPath := filepath.Join(ws.HostPath(), filepath.FromSlash(cp.Path))
		configDir := filepath.Dir(configPath)
		hasWorkspaceConfig, err := workspaceMigrationHasExplicitConfigAncestor(workspaceCtx, statFS, ws.HostPath(), configDir)
		if err != nil {
			return nil, nil, err
		}
		if hasWorkspaceConfig {
			// FIXME(workspace-migrate): Match the top-level explicit-config rule
			// for now: pre-existing workspace configs own migration below them.
			continue
		}

		data, err := bk.ReadCallerHostFile(workspaceCtx, configPath)
		if err != nil {
			return nil, nil, fmt.Errorf("reading legacy module config %s: %w", cp.Path, err)
		}
		compatWorkspace, err := workspaceMigrationCompatWorkspaceForLegacyConfig(data, configPath)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing legacy module config %s: %w", cp.Path, err)
		}
		if compatWorkspace == nil {
			continue
		}
		compatWorkspace.DiscoveredLocalModule = cp.DiscoveredLocalModule
		compatWorkspaces = append(compatWorkspaces, compatWorkspace)
	}
	return compatWorkspaces, discoveryWarnings, nil
}

// workspaceMigrationConfigPath is a legacy config discovered for migration,
// tagged with whether it was reached by following a local module reference.
type workspaceMigrationConfigPath struct {
	Path                  string
	DiscoveredLocalModule bool
}

func workspaceMigrationLegacyConfigPaths(
	ctx context.Context,
	rootDir dagql.ObjectResult[*core.Directory],
	ws *core.Workspace,
) ([]workspaceMigrationConfigPath, []string, error) {
	// Migration is intentionally scoped to the selected legacy project plus
	// its conventional project-local module directory. A repo may contain
	// unrelated dagger.json files in testdata, examples, or nested projects;
	// those should only migrate when the user runs migration from that project.
	selectedConfig, selected, err := workspaceMigrationSelectedLegacyConfigPath(ctx, rootDir, ws)
	if err != nil {
		return nil, nil, err
	}

	projectRoot := workspaceMigrationCleanRelPath(ws.Cwd)
	if selected {
		projectRoot = path.Dir(selectedConfig)
		if projectRoot == "." {
			projectRoot = ""
		}
	}

	initial := make([]string, 0, 1)
	if selected {
		initial = append(initial, selectedConfig)
	}

	moduleConfigPattern := path.Join(projectRoot, workspace.LockDirName, "modules", "**", workspace.LegacyModuleConfigFileName)
	modulePaths, err := rootDir.Self().Glob(ctx, rootDir, moduleConfigPattern)
	if err != nil {
		return nil, nil, fmt.Errorf("find legacy module configs under %s: %w", path.Join(projectRoot, workspace.LockDirName, "modules"), err)
	}
	initial = append(initial, modulePaths...)
	initial = workspaceMigrationUniqueSortedPaths(initial)

	// Seed the dependency walk from the configs that will actually be migrated,
	// dropping hidden .dagger/modules/** glob hits (e.g. a scratch dir): an
	// ignored hidden config must not be read, nor drag its own local deps into
	// migration. Hidden modules reached from a non-hidden explicit reference are
	// still discovered by the walk itself.
	seeds := make([]string, 0, len(initial))
	for _, p := range initial {
		if workspaceMigrationHiddenPath(p) {
			continue
		}
		seeds = append(seeds, p)
	}
	discovered, warnings, err := workspaceMigrationDiscoverLocalModules(ctx, rootDir, projectRoot, seeds)
	if err != nil {
		return nil, nil, err
	}

	// Selected/glob origin wins over a discovered origin: a config that is both a
	// .dagger/modules/** hit and locally referenced keeps its existing handling.
	result := make([]workspaceMigrationConfigPath, 0, len(initial)+len(discovered))
	seen := make(map[string]struct{}, len(initial)+len(discovered))
	for _, p := range initial {
		result = append(result, workspaceMigrationConfigPath{Path: p})
		seen[path.Clean(p)] = struct{}{}
	}
	for _, p := range discovered {
		if _, ok := seen[path.Clean(p)]; ok {
			continue
		}
		seen[path.Clean(p)] = struct{}{}
		result = append(result, workspaceMigrationConfigPath{Path: p, DiscoveredLocalModule: true})
	}
	return result, warnings, nil
}

// workspaceMigrationDiscoverLocalModules walks the local toolchain/dependency
// references of every seed config, transitively, and returns the legacy config
// paths of the locally-defined modules that should be migrated in place. The
// traversal dedups by canonical directory (so a module reachable from several
// places — a diamond — is migrated once) and is cycle-safe: the visited set is
// seeded with every initial config dir and populated before recursing, and an
// explicit worklist avoids unbounded recursion. Modules that resolve outside
// the workspace, that have no dagger.json, or that define their own workspace
// semantics (toolchains/blueprint) are skipped — safely, since a legacy
// dagger.json still loads.
func workspaceMigrationDiscoverLocalModules(
	ctx context.Context,
	rootDir dagql.ObjectResult[*core.Directory],
	projectRoot string,
	seedPaths []string,
) (discovered []string, warnings []string, _ error) {
	visited := make(map[string]struct{}, len(seedPaths))
	for _, p := range seedPaths {
		visited[workspaceMigrationCleanRelPath(path.Dir(p))] = struct{}{}
	}

	type frame struct {
		dir string
		cfg *modules.ModuleConfig
	}
	stack := make([]frame, 0, len(seedPaths))
	for _, p := range seedPaths {
		cfg, err := workspaceMigrationReadLegacyModuleConfig(ctx, rootDir, p)
		if err != nil {
			return nil, nil, err
		}
		stack = append(stack, frame{dir: workspaceMigrationCleanRelPath(path.Dir(p)), cfg: cfg})
	}

	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, ref := range workspace.LocalModuleRefs(f.cfg) {
			// An absolute source is not workspace-relative; path.Join would
			// silently rebase it under the referrer's dir and migrate the wrong
			// in-tree module, so treat it like an out-of-workspace reference.
			if path.IsAbs(filepath.ToSlash(ref.Source)) {
				warnings = append(warnings, fmt.Sprintf(
					"skipped migrating local module %q: %q is an absolute path outside the workspace and was left as-is",
					ref.Name, ref.Source))
				continue
			}
			modDir := workspaceMigrationJoinRelPath(f.dir, ref.Source)
			// Migration is scoped to the selected project; a ref that resolves
			// outside it (e.g. a toolchain shared from a sibling directory)
			// belongs to another project and is left as legacy.
			if !workspaceMigrationWithinProject(projectRoot, modDir) {
				warnings = append(warnings, fmt.Sprintf(
					"skipped migrating local module %q: %q resolves outside the migrated project and was left as-is",
					ref.Name, ref.Source))
				continue
			}
			if _, ok := visited[modDir]; ok {
				continue
			}
			visited[modDir] = struct{}{}

			cfgPath := path.Join(modDir, workspace.LegacyModuleConfigFileName)
			exists, err := workspaceMigrationPathExists(ctx, rootDir, cfgPath)
			if err != nil {
				return nil, nil, err
			}
			if !exists {
				continue
			}
			cfg, err := workspaceMigrationReadLegacyModuleConfig(ctx, rootDir, cfgPath)
			if err != nil {
				return nil, nil, err
			}
			if workspace.HasOwnWorkspaceSemantics(cfg) {
				warnings = append(warnings, fmt.Sprintf(
					"skipped migrating local module %q at %q: it defines its own toolchains or blueprint and must be migrated separately",
					ref.Name, modDir))
				continue
			}
			discovered = append(discovered, cfgPath)
			stack = append(stack, frame{dir: modDir, cfg: cfg})
		}
	}
	sort.Strings(discovered)
	warnings = workspaceMigrationUniqueSortedPaths(warnings)
	return discovered, warnings, nil
}

func workspaceMigrationReadLegacyModuleConfig(
	ctx context.Context,
	rootDir dagql.ObjectResult[*core.Directory],
	relPath string,
) (*modules.ModuleConfig, error) {
	data, err := core.DirectoryReadFile(ctx, rootDir, path.Clean(relPath))
	if err != nil {
		return nil, fmt.Errorf("read legacy module config %q: %w", relPath, err)
	}
	cfg, err := workspace.ParseLegacyModuleConfigTolerant(data)
	if err != nil {
		return nil, fmt.Errorf("parse legacy module config %q: %w", relPath, err)
	}
	return cfg, nil
}

func workspaceMigrationJoinRelPath(dir, source string) string {
	return workspaceMigrationCleanRelPath(path.Join(dir, filepath.ToSlash(source)))
}

// workspaceMigrationWithinProject reports whether the cleaned, root-relative
// modDir is inside the migrated project (projectRoot, also root-relative; ""
// means the workspace root). A ref that lands outside — a sibling reached via
// ".." or anything above the root — is out of migration scope.
func workspaceMigrationWithinProject(projectRoot, modDir string) bool {
	if projectRoot == "" || projectRoot == "." {
		return modDir != ".." && !strings.HasPrefix(modDir, "../")
	}
	return modDir == projectRoot || strings.HasPrefix(modDir, projectRoot+"/")
}

func workspaceMigrationSelectedLegacyConfigPath(
	ctx context.Context,
	rootDir dagql.ObjectResult[*core.Directory],
	ws *core.Workspace,
) (string, bool, error) {
	if ws == nil {
		return "", false, fmt.Errorf("workspace is required")
	}

	dir := workspaceMigrationCleanRelPath(ws.Cwd)
	for {
		candidate := path.Join(dir, workspace.LegacyModuleConfigFileName)
		exists, err := workspaceMigrationPathExists(ctx, rootDir, candidate)
		if err != nil {
			return "", false, err
		}
		if exists {
			return candidate, true, nil
		}
		if dir == "." {
			return "", false, nil
		}
		next := path.Dir(dir)
		if next == dir {
			return "", false, nil
		}
		dir = next
	}
}

func workspaceMigrationCleanRelPath(p string) string {
	if p == "" || p == "." {
		return "."
	}
	return path.Clean(filepath.ToSlash(p))
}

func workspaceMigrationCompatWorkspaceForLegacyConfig(data []byte, configPath string) (*workspace.CompatWorkspace, error) {
	compatWorkspace, err := workspace.ParseMigrationCompatWorkspaceAt(data, configPath)
	if err != nil || compatWorkspace != nil {
		return compatWorkspace, err
	}

	modCfg, err := modules.ParseModuleConfigForFilename(data, workspace.LegacyModuleConfigFileName)
	if err != nil {
		return nil, err
	}
	if modCfg.Name == "" {
		return nil, nil
	}
	return &workspace.CompatWorkspace{
		Config:      &modCfg.ModuleConfig,
		ConfigPath:  configPath,
		ProjectRoot: filepath.Dir(configPath),
	}, nil
}

func workspaceMigrationUniqueSortedPaths(paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	sort.Strings(paths)
	unique := paths[:1]
	for _, p := range paths[1:] {
		if p != unique[len(unique)-1] {
			unique = append(unique, p)
		}
	}
	return unique
}

func workspaceMigrationHiddenPath(relPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) >= 3 && parts[0] == workspace.LockDirName && parts[1] == "modules" {
		for _, part := range parts[2:] {
			if part != "" && strings.HasPrefix(part, ".") {
				return true
			}
		}
		return false
	}

	for _, part := range parts {
		if part != "" && strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func workspaceMigrationHasExplicitConfigAncestor(
	ctx context.Context,
	statFS core.StatFS,
	root string,
	dir string,
) (bool, error) {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	for {
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			_, exists, err := core.StatFSExists(ctx, statFS, filepath.Join(dir, workspace.ConfigFileName))
			if err != nil {
				return false, fmt.Errorf("check workspace config at %s: %w", dir, err)
			}
			if exists {
				return true, nil
			}
		}
		if dir == root {
			return false, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return false, nil
		}
		dir = next
	}
}

func (s *workspaceSchema) workspaceMigrationChangeset(
	ctx context.Context,
	ws *core.Workspace,
	plans workspaceMigrationPlanBundle,
) (_ *core.Changeset, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "build migration changeset", workspaceMigrationWrapperSpanOpts(ctx)...)
	defer telemetry.EndWithCause(span, &rerr)

	baseDir, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, err
	}
	updatedDir := baseDir

	lockMoves, err := workspaceMigrationLegacyLockMoves(ctx, ws, baseDir, plans)
	if err != nil {
		return nil, err
	}

	targetPaths, err := workspaceMigrationRootTargetPaths(ws, plans)
	if err != nil {
		return nil, err
	}
	for _, move := range lockMoves {
		targetPaths = append(targetPaths, move.TargetPath)
	}
	if err := validateWorkspaceMigrationTargetPaths(ctx, baseDir, targetPaths); err != nil {
		return nil, err
	}

	updatedDir, err = applyWorkspaceMigrationLegacyLockMoves(ctx, updatedDir, lockMoves)
	if err != nil {
		return nil, err
	}
	updatedDir, err = applyWorkspaceMigrationWorkspacePlans(ctx, ws, updatedDir, plans.WorkspacePlans)
	if err != nil {
		return nil, err
	}
	updatedDir, err = applyWorkspaceMigrationModuleConfigConversions(ctx, ws, updatedDir, plans.ModuleConfigConversions)
	if err != nil {
		return nil, err
	}
	updatedDir, err = applyWorkspaceMigrationParentPlans(ctx, ws, updatedDir, plans.ParentPlans)
	if err != nil {
		return nil, err
	}

	var changes dagql.ObjectResult[*core.Changeset]
	if err := func() (rerr error) {
		diffCtx, span := core.Tracer(ctx).Start(ctx, "compute migration changeset", telemetry.Internal())
		defer telemetry.EndWithCause(span, &rerr)
		var err error
		changes, err = workspaceMigrationChanges(diffCtx, updatedDir, baseDir)
		return err
	}(); err != nil {
		return nil, fmt.Errorf("migration changeset: %w", err)
	}
	return changes.Self(), nil
}

func applyWorkspaceMigrationLegacyLockMoves(
	ctx context.Context,
	dir dagql.ObjectResult[*core.Directory],
	moves []workspaceMigrationLegacyLockMove,
) (dagql.ObjectResult[*core.Directory], error) {
	var err error
	for _, move := range moves {
		dir, err = withWorkspaceMigrationFile(ctx, dir, move.TargetPath, move.Data, "workspace lock: "+workspaceMigrationDisplayPath(move.SourcePath)+" -> "+workspaceMigrationDisplayPath(move.TargetPath))
		if err != nil {
			return dir, err
		}
		dir, err = removeWorkspaceMigrationFile(ctx, dir, move.SourcePath, "remove legacy workspace lock", "migration remove legacy workspace lock")
		if err != nil {
			return dir, err
		}
	}
	return dir, nil
}

func applyWorkspaceMigrationWorkspacePlans(
	ctx context.Context,
	ws *core.Workspace,
	dir dagql.ObjectResult[*core.Directory],
	plans []*workspace.MigrationPlan,
) (dagql.ObjectResult[*core.Directory], error) {
	var err error
	for _, plan := range plans {
		dir, err = applyWorkspaceMigrationWorkspacePlan(ctx, ws, dir, plan)
		if err != nil {
			return dir, err
		}
	}
	return dir, nil
}

func applyWorkspaceMigrationWorkspacePlan(
	ctx context.Context,
	ws *core.Workspace,
	dir dagql.ObjectResult[*core.Directory],
	plan *workspace.MigrationPlan,
) (dagql.ObjectResult[*core.Directory], error) {
	if len(plan.MigratedModuleConfigData) > 0 {
		migratedModuleConfigPath, err := workspaceMigrationRootPath(ws, plan, plan.MigratedModuleConfigPath)
		if err != nil {
			return dir, err
		}
		dir, err = withWorkspaceMigrationFile(ctx, dir, migratedModuleConfigPath, plan.MigratedModuleConfigData, "move module: "+workspace.LegacyModuleConfigFileName+" -> "+workspaceMigrationDisplayPath(migratedModuleConfigPath))
		if err != nil {
			return dir, err
		}
	}

	workspaceConfigPath, err := workspaceMigrationRootPath(ws, plan, workspace.ConfigFileName)
	if err != nil {
		return dir, err
	}
	dir, err = withWorkspaceMigrationFile(ctx, dir, workspaceConfigPath, plan.WorkspaceConfigData, "workspace configuration: "+workspaceMigrationDisplayPath(workspaceConfigPath))
	if err != nil {
		return dir, err
	}

	if len(plan.MigrationReportData) > 0 {
		migrationReportPath, err := workspaceMigrationRootPath(ws, plan, plan.MigrationReportPath)
		if err != nil {
			return dir, err
		}
		dir, err = withWorkspaceMigrationFile(ctx, dir, migrationReportPath, plan.MigrationReportData, "migration report: "+workspaceMigrationDisplayPath(migrationReportPath))
		if err != nil {
			return dir, err
		}
	}

	legacyConfigPath, err := workspaceMigrationRootPath(ws, plan, workspace.LegacyModuleConfigFileName)
	if err != nil {
		return dir, err
	}
	return removeWorkspaceMigrationFile(ctx, dir, legacyConfigPath, "remove legacy config", "migration remove legacy config")
}

func applyWorkspaceMigrationModuleConfigConversions(
	ctx context.Context,
	ws *core.Workspace,
	dir dagql.ObjectResult[*core.Directory],
	conversions []workspaceMigrationModuleConfigConversion,
) (dagql.ObjectResult[*core.Directory], error) {
	for _, conversion := range conversions {
		currentConfigPath, err := workspaceMigrationRootPathForProject(ws, conversion.ProjectRoot, workspace.ModuleConfigFileName)
		if err != nil {
			return dir, err
		}
		dir, err = withWorkspaceMigrationFile(ctx, dir, currentConfigPath, conversion.ConfigData, "module config: "+workspace.LegacyModuleConfigFileName+" -> "+workspaceMigrationDisplayPath(currentConfigPath))
		if err != nil {
			return dir, err
		}

		legacyConfigPath, err := workspaceMigrationRootPathForProject(ws, conversion.ProjectRoot, workspace.LegacyModuleConfigFileName)
		if err != nil {
			return dir, err
		}
		dir, err = removeWorkspaceMigrationFile(ctx, dir, legacyConfigPath, "remove legacy module config", "migration remove legacy module config")
		if err != nil {
			return dir, err
		}
	}
	return dir, nil
}

func applyWorkspaceMigrationParentPlans(
	ctx context.Context,
	ws *core.Workspace,
	dir dagql.ObjectResult[*core.Directory],
	plans []workspaceMigrationParentPlan,
) (dagql.ObjectResult[*core.Directory], error) {
	for _, plan := range plans {
		workspaceConfigPath, err := workspaceMigrationRootPathForProject(ws, plan.ProjectRoot, workspace.ConfigFileName)
		if err != nil {
			return dir, err
		}
		dir, err = withWorkspaceMigrationFile(ctx, dir, workspaceConfigPath, plan.WorkspaceConfigData, "parent workspace configuration: "+workspaceMigrationDisplayPath(workspaceConfigPath))
		if err != nil {
			return dir, err
		}
		if len(plan.MigrationReportData) == 0 {
			continue
		}
		migrationReportPath, err := workspaceMigrationRootPathForProject(ws, plan.ProjectRoot, plan.MigrationReportPath)
		if err != nil {
			return dir, err
		}
		dir, err = withWorkspaceMigrationFile(ctx, dir, migrationReportPath, plan.MigrationReportData, "migration report: "+workspaceMigrationDisplayPath(migrationReportPath))
		if err != nil {
			return dir, err
		}
	}
	return dir, nil
}

func removeWorkspaceMigrationFile(
	ctx context.Context,
	dir dagql.ObjectResult[*core.Directory],
	filePath string,
	spanName string,
	errPrefix string,
) (updated dagql.ObjectResult[*core.Directory], rerr error) {
	removeCtx, span := core.Tracer(ctx).Start(ctx, spanName, telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

	updated, err := workspaceMigrationSelectDirectory(removeCtx, dir, "withoutFile", []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString(path.Clean(filepath.ToSlash(filePath)))},
	})
	if err != nil {
		return dir, fmt.Errorf("%s: %w", errPrefix, err)
	}
	return updated, nil
}

func workspaceMigrationRootTargetPaths(ws *core.Workspace, plans workspaceMigrationPlanBundle) ([]string, error) {
	seen := make(map[string]struct{})
	paths := make([]string, 0, len(plans.WorkspacePlans)*3+len(plans.ParentPlans)*2+len(plans.ModuleConfigConversions))
	addPath := func(rootPath string) error {
		cleanPath := path.Clean(filepath.ToSlash(rootPath))
		if _, ok := seen[cleanPath]; ok {
			return fmt.Errorf("migration target %q is planned more than once", cleanPath)
		}
		seen[cleanPath] = struct{}{}
		paths = append(paths, rootPath)
		return nil
	}

	for _, plan := range plans.WorkspacePlans {
		for _, targetPath := range workspaceMigrationTargetPaths(plan) {
			rootPath, err := workspaceMigrationRootPath(ws, plan, targetPath)
			if err != nil {
				return nil, err
			}
			if err := addPath(rootPath); err != nil {
				return nil, err
			}
		}
	}
	for _, conversion := range plans.ModuleConfigConversions {
		rootPath, err := workspaceMigrationRootPathForProject(ws, conversion.ProjectRoot, workspace.ModuleConfigFileName)
		if err != nil {
			return nil, err
		}
		if err := addPath(rootPath); err != nil {
			return nil, err
		}
	}
	for _, plan := range plans.ParentPlans {
		rootPath, err := workspaceMigrationRootPathForProject(ws, plan.ProjectRoot, workspace.ConfigFileName)
		if err != nil {
			return nil, err
		}
		if err := addPath(rootPath); err != nil {
			return nil, err
		}
		if len(plan.MigrationReportData) > 0 {
			rootPath, err := workspaceMigrationRootPathForProject(ws, plan.ProjectRoot, plan.MigrationReportPath)
			if err != nil {
				return nil, err
			}
			if err := addPath(rootPath); err != nil {
				return nil, err
			}
		}
	}
	return paths, nil
}

func workspaceMigrationLegacyLockMoves(
	ctx context.Context,
	ws *core.Workspace,
	baseDir dagql.ObjectResult[*core.Directory],
	plans workspaceMigrationPlanBundle,
) ([]workspaceMigrationLegacyLockMove, error) {
	projectRoots := workspaceMigrationLegacyLockProjectRoots(plans)

	moves := make([]workspaceMigrationLegacyLockMove, 0, len(projectRoots))
	for _, projectRoot := range projectRoots {
		sourcePath, err := workspaceMigrationRootPathForProject(ws, projectRoot, workspace.LegacyLockFilePath)
		if err != nil {
			return nil, err
		}
		exists, err := workspaceMigrationPathExists(ctx, baseDir, sourcePath)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		data, err := core.DirectoryReadFile(ctx, baseDir, path.Clean(filepath.ToSlash(sourcePath)))
		if err != nil {
			return nil, fmt.Errorf("read legacy workspace lock %q: %w", sourcePath, err)
		}
		data, err = workspaceMigrationFilterLegacyLockData(data)
		if err != nil {
			return nil, fmt.Errorf("filter legacy workspace lock %q: %w", sourcePath, err)
		}

		targetPath, err := workspaceMigrationRootPathForProject(ws, projectRoot, workspace.LockFileName)
		if err != nil {
			return nil, err
		}
		moves = append(moves, workspaceMigrationLegacyLockMove{
			SourcePath: sourcePath,
			TargetPath: targetPath,
			Data:       data,
		})
	}
	return moves, nil
}

func workspaceMigrationFilterLegacyLockData(data []byte) ([]byte, error) {
	lock, err := workspace.ParseLock(data)
	if err != nil {
		return nil, err
	}
	entries, err := lock.Entries()
	if err != nil {
		return nil, err
	}
	filtered := workspace.NewLock()
	for _, entry := range entries {
		if entry.Namespace == "" && entry.Operation == workspaceMigrationLockModulesResolveOperation {
			continue
		}
		if err := filtered.SetLookup(entry.Namespace, entry.Operation, entry.Inputs, entry.Result); err != nil {
			return nil, fmt.Errorf("preserve lock entry %s %v: %w", entry.Operation, entry.Inputs, err)
		}
	}
	return filtered.Marshal()
}

func workspaceMigrationLegacyLockProjectRoots(plans workspaceMigrationPlanBundle) []string {
	seenProjects := map[string]struct{}{}
	projectRoots := make([]string, 0, len(plans.WorkspacePlans)+len(plans.ParentPlans)+len(plans.ModuleConfigConversions))
	addProject := func(projectRoot string) {
		projectRoot = filepath.Clean(projectRoot)
		if _, ok := seenProjects[projectRoot]; ok {
			return
		}
		seenProjects[projectRoot] = struct{}{}
		projectRoots = append(projectRoots, projectRoot)
	}
	for _, plan := range plans.WorkspacePlans {
		addProject(plan.ProjectRoot)
	}
	for _, plan := range plans.ParentPlans {
		addProject(plan.ProjectRoot)
	}
	for _, conversion := range plans.ModuleConfigConversions {
		addProject(conversion.ProjectRoot)
	}
	return projectRoots
}

func workspaceMigrationRootPath(ws *core.Workspace, plan *workspace.MigrationPlan, relPath string) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("migration plan is unavailable")
	}
	return workspaceMigrationRootPathForProject(ws, plan.ProjectRoot, relPath)
}

func workspaceMigrationRootPathForProject(ws *core.Workspace, projectRoot string, relPath string) (string, error) {
	projectRootPath, err := workspaceMigrationProjectRootRelPath(ws, projectRoot)
	if err != nil {
		return "", err
	}
	if projectRootPath == "." {
		return relPath, nil
	}
	return filepath.Join(projectRootPath, relPath), nil
}

func workspaceMigrationTargetPaths(plan *workspace.MigrationPlan) []string {
	paths := make([]string, 0, 3)
	if len(plan.MigratedModuleConfigData) > 0 {
		paths = append(paths, plan.MigratedModuleConfigPath)
	}
	paths = append(paths, workspace.ConfigFileName)
	if len(plan.MigrationReportData) > 0 {
		paths = append(paths, plan.MigrationReportPath)
	}
	return paths
}

func recordWorkspaceMigrationModuleSpans(ctx context.Context, modules []workspace.CompatWorkspaceModule) {
	if !workspaceMigrationProgressEnabled(ctx) {
		return
	}

	seen := make(map[string]struct{}, len(modules))
	for _, mod := range modules {
		if mod.Source == "" {
			continue
		}
		if _, ok := seen[mod.Source]; ok {
			continue
		}
		seen[mod.Source] = struct{}{}

		_, span := core.Tracer(ctx).Start(ctx, "install module: "+mod.Source)
		span.End()
	}
}

func shouldRecordWorkspaceMigrationProgress(ctx context.Context, query *core.Query, ws *core.Workspace) bool {
	if query == nil {
		return true
	}
	seenKeys, err := query.TelemetrySeenKeyStore(ctx)
	if err != nil {
		return true
	}
	key := "workspace.migrate.progress:" + ws.HostPath()
	if ws.ClientID != "" {
		// Keep migration summaries visible for separate CLI clients that use the
		// same host path, while still deduping repeated selects within one client.
		key += ":" + ws.ClientID
	}
	return dagql.ShouldEmitTelemetry(ctx, seenKeys, key, false)
}

func workspaceMigrationProgressEnabled(ctx context.Context) bool {
	enabled, ok := ctx.Value(workspaceMigrationProgressContextKey{}).(bool)
	return !ok || enabled
}

func workspaceMigrationWrapperSpanOpts(ctx context.Context) []trace.SpanStartOption {
	if !workspaceMigrationProgressEnabled(ctx) {
		return []trace.SpanStartOption{telemetry.Internal()}
	}
	return []trace.SpanStartOption{telemetry.Passthrough()}
}

func workspaceMigrationDisplayPath(filePath string) string {
	return path.Clean(filepath.ToSlash(filePath))
}

func validateWorkspaceMigrationTargetPaths(
	ctx context.Context,
	baseDir dagql.ObjectResult[*core.Directory],
	targetPaths []string,
) error {
	for _, targetPath := range targetPaths {
		cleanPath := path.Clean(filepath.ToSlash(targetPath))
		exists, err := workspaceMigrationPathExists(ctx, baseDir, cleanPath)
		if err != nil {
			return fmt.Errorf("check migration target %q: %w", cleanPath, err)
		}
		if exists {
			return fmt.Errorf("migration target %q already exists; refusing to overwrite existing workspace data", cleanPath)
		}
	}
	return nil
}

func workspaceMigrationPathExists(
	ctx context.Context,
	baseDir dagql.ObjectResult[*core.Directory],
	filePath string,
) (bool, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}
	_, err = baseDir.Self().Stat(ctx, baseDir, srv, path.Clean(filepath.ToSlash(filePath)), false)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

//nolint:unparam
func withWorkspaceMigrationFile(
	ctx context.Context,
	dir dagql.ObjectResult[*core.Directory],
	filePath string,
	contents []byte,
	spanName string,
	spanOpts ...trace.SpanStartOption,
) (updated dagql.ObjectResult[*core.Directory], rerr error) {
	if spanName == "" {
		spanName = "write migration file"
	}
	if !workspaceMigrationProgressEnabled(ctx) {
		spanOpts = append(spanOpts, telemetry.Internal())
	}
	ctx, span := core.Tracer(ctx).Start(ctx, spanName, spanOpts...)
	defer telemetry.EndWithCause(span, &rerr)
	updated, err := workspaceMigrationSelectDirectory(ctx, dir, "withNewFile", []dagql.NamedInput{
		{Name: "path", Value: dagql.NewString(path.Clean(filepath.ToSlash(filePath)))},
		{Name: "contents", Value: dagql.String(contents)},
		{Name: "permissions", Value: dagql.Int(0o644)},
	})
	if err != nil {
		return dir, fmt.Errorf("migration write %q: %w", filePath, err)
	}
	return updated, nil
}

func workspaceMigrationSelectDirectory(
	ctx context.Context,
	dir dagql.ObjectResult[*core.Directory],
	field string,
	args []dagql.NamedInput,
) (updated dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return updated, err
	}
	if err := srv.Select(ctx, dir, &updated, dagql.Selector{
		Field: field,
		Args:  args,
	}); err != nil {
		return updated, err
	}
	return updated, nil
}

func workspaceMigrationChanges(
	ctx context.Context,
	after dagql.ObjectResult[*core.Directory],
	before dagql.ObjectResult[*core.Directory],
) (changes dagql.ObjectResult[*core.Changeset], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return changes, err
	}

	beforeID, err := before.ID()
	if err != nil {
		return changes, err
	}
	if err := srv.Select(ctx, after, &changes, dagql.Selector{
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)},
		},
	}); err != nil {
		return changes, err
	}
	return changes, nil
}

func workspaceMigrationProjectRootRelPath(ws *core.Workspace, projectRoot string) (string, error) {
	if ws.HostPath() == "" {
		return "", fmt.Errorf("workspace migration is local-only")
	}
	if projectRoot == "" {
		return "", fmt.Errorf("migration project root is unavailable")
	}
	rel, err := filepath.Rel(ws.HostPath(), projectRoot)
	if err != nil {
		return "", fmt.Errorf("migration project root: %w", err)
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("migration project root %q escapes workspace boundary %q", projectRoot, ws.HostPath())
	}
	return rel, nil
}

func appendWorkspaceMigrationNonGapWarnings(plan *workspace.MigrationPlan, warnings []string) {
	if plan == nil || len(warnings) == 0 {
		return
	}

	gapStart := len(plan.Warnings) - plan.MigrationGapCount
	if gapStart < 0 {
		gapStart = 0
	}

	updated := make([]string, 0, len(plan.Warnings)+len(warnings))
	updated = append(updated, plan.Warnings[:gapStart]...)
	updated = append(updated, warnings...)
	updated = append(updated, plan.Warnings[gapStart:]...)
	plan.Warnings = updated
}

func workspaceMigrationWarnings(plan *workspace.MigrationPlan) []string {
	if plan == nil || len(plan.Warnings) == 0 {
		return nil
	}

	warnings := make([]string, 0, len(plan.Warnings))
	nonGapCount := len(plan.Warnings) - plan.MigrationGapCount
	if nonGapCount < 0 {
		nonGapCount = 0
	}
	warnings = append(warnings, plan.Warnings[:nonGapCount]...)
	if plan.MigrationGapCount > 0 {
		warnings = append(warnings,
			fmt.Sprintf("%d old setting(s) need review; see %s", plan.MigrationGapCount, plan.MigrationReportPath),
		)
	}
	return warnings
}

func workspaceMigrationPlanBundleWarnings(plans workspaceMigrationPlanBundle) []string {
	var warnings []string
	seen := map[string]struct{}{}
	addWarning := func(warning string) {
		if warning == "" {
			return
		}
		if _, ok := seen[warning]; ok {
			return
		}
		seen[warning] = struct{}{}
		warnings = append(warnings, warning)
	}

	for _, plan := range plans.WorkspacePlans {
		for _, warning := range workspaceMigrationWarnings(plan) {
			addWarning(warning)
		}
	}
	for _, plan := range plans.ParentPlans {
		for _, warning := range plan.Warnings {
			addWarning(warning)
		}
	}
	return warnings
}

func workspaceConfigUsesMigratedModuleSources(cfg *workspace.Config, configDir string) bool {
	if cfg == nil {
		return false
	}

	migratedModulesDir := filepath.Clean(filepath.Join(workspace.LockDirName, "modules"))
	for _, entry := range cfg.Modules {
		resolvedSource := workspace.ResolveModuleEntrySource(configDir, entry.Source)
		if filepath.IsAbs(resolvedSource) {
			continue
		}
		resolvedSource = filepath.Clean(resolvedSource)
		if resolvedSource == migratedModulesDir ||
			strings.HasPrefix(resolvedSource, migratedModulesDir+string(filepath.Separator)) {
			return true
		}
	}

	return false
}
