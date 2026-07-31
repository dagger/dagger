package schema

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
)

var workspaceMigrationReportPath = filepath.Join(workspace.LockDirName, "migration-report.md")

type workspaceMigrationParentPlan struct {
	ProjectRoot         string
	WorkspaceConfigData []byte
	Warnings            []string
	MigrationReportPath string
	MigrationReportData []byte
}

type workspaceMigrationParentAssignment struct {
	CompatWorkspace   *workspace.CompatWorkspace
	ParentProjectRoot string
}

func workspaceMigrationParentPlansForPlainModules(
	ws *core.Workspace,
	compatWorkspaces []*workspace.CompatWorkspace,
	workspacePlans []*workspace.MigrationPlan,
) ([]workspaceMigrationParentPlan, error) {
	assignments, err := workspaceMigrationParentAssignments(ws, compatWorkspaces)
	if err != nil {
		return nil, err
	}
	parentPlans, err := workspaceMigrationParentPlans(ws, assignments, workspacePlans)
	if err != nil {
		return nil, err
	}
	if parentPlans, err = workspaceMigrationInstallParentSDKModules(workspacePlans, parentPlans, assignments); err != nil {
		return nil, err
	}
	return workspaceMigrationWarnExplicitModuleLoading(ws, workspacePlans, parentPlans, assignments)
}

func workspaceMigrationParentAssignments(
	ws *core.Workspace,
	compatWorkspaces []*workspace.CompatWorkspace,
) ([]workspaceMigrationParentAssignment, error) {
	if ws == nil || ws.HostPath() == "" {
		return nil, fmt.Errorf("workspace host path is required")
	}

	assignments := make([]workspaceMigrationParentAssignment, 0, len(compatWorkspaces))
	for _, compatWorkspace := range compatWorkspaces {
		if compatWorkspace == nil {
			continue
		}
		// A discovered local dependency/toolchain target is loaded through its
		// referrer, not explicitly; it must not create a parent workspace, hoist
		// its runtime, or warn about explicit loading.
		if compatWorkspace.DiscoveredLocalModule {
			continue
		}
		if compatWorkspace.Config == nil || compatWorkspace.Config.SDK == nil {
			continue
		}
		// A project-style layout (source in a subdirectory) installs the module
		// into its own migrated workspace config with its SDK recorded as-sdk;
		// there is no separate runtime pin to hoist and no explicit-loading
		// warning. Only the "repo is just a dagger module" shape — source at
		// the project root — flows through here, whether or not toolchains
		// force a workspace plan of its own.
		if compatWorkspace.MustMigrateToWorkspaceConfig() && !workspace.ModuleSourceAtRoot(compatWorkspace.Config) {
			continue
		}
		// The runtime pin and warning land at the module's own project root:
		// either its planned dagger.toml (toolchains case) or a minimal parent
		// config synthesized there. Never at the workspace/git root — a module
		// repo must not grow a repo-root workspace claiming sibling modules.
		assignments = append(assignments, workspaceMigrationParentAssignment{
			CompatWorkspace:   compatWorkspace,
			ParentProjectRoot: compatWorkspace.ProjectRoot,
		})
	}
	return assignments, nil
}

func workspaceMigrationParentPlans(
	ws *core.Workspace,
	assignments []workspaceMigrationParentAssignment,
	workspacePlans []*workspace.MigrationPlan,
) ([]workspaceMigrationParentPlan, error) {
	if ws == nil || ws.HostPath() == "" {
		return nil, fmt.Errorf("workspace host path is required")
	}

	plannedRoots := workspaceMigrationPlannedProjectRoots(workspacePlans)
	parentRoots := make([]string, 0, len(assignments))
	seen := map[string]struct{}{}
	for _, assignment := range assignments {
		parentRoot := assignment.ParentProjectRoot
		if parentRoot == "" {
			return nil, fmt.Errorf("parent project root is required")
		}
		if _, planned := plannedRoots[parentRoot]; planned {
			continue
		}
		if _, ok := seen[parentRoot]; ok {
			continue
		}
		seen[parentRoot] = struct{}{}
		parentRoots = append(parentRoots, parentRoot)
	}
	sort.Strings(parentRoots)

	parentPlans := make([]workspaceMigrationParentPlan, 0, len(parentRoots))
	for _, parentRoot := range parentRoots {
		parentPlans = append(parentPlans, workspaceMigrationParentPlan{
			ProjectRoot:         parentRoot,
			WorkspaceConfigData: []byte(initialWorkspaceConfig),
		})
	}
	return parentPlans, nil
}

func workspaceMigrationPlannedProjectRoots(plans []*workspace.MigrationPlan) map[string]struct{} {
	roots := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if plan == nil || plan.ProjectRoot == "" {
			continue
		}
		roots[plan.ProjectRoot] = struct{}{}
	}
	return roots
}

// workspaceMigrationConfigTargets indexes the migrated workspace configs (both
// migration plans and synthesized parent plans) by project root, so an SDK
// install can locate the config that owns a module and update it in place.
type workspaceMigrationConfigTargets struct {
	workspacePlansByRoot map[string]*workspace.MigrationPlan
	parentPlanIndexes    map[string]int
	parentPlans          []workspaceMigrationParentPlan
}

func newWorkspaceMigrationConfigTargets(
	workspacePlans []*workspace.MigrationPlan,
	parentPlans []workspaceMigrationParentPlan,
) workspaceMigrationConfigTargets {
	byRoot := make(map[string]*workspace.MigrationPlan, len(workspacePlans))
	for _, plan := range workspacePlans {
		if plan != nil && plan.ProjectRoot != "" {
			byRoot[filepath.Clean(plan.ProjectRoot)] = plan
		}
	}
	indexes := make(map[string]int, len(parentPlans))
	for i, plan := range parentPlans {
		indexes[filepath.Clean(plan.ProjectRoot)] = i
	}
	return workspaceMigrationConfigTargets{
		workspacePlansByRoot: byRoot,
		parentPlanIndexes:    indexes,
		parentPlans:          parentPlans,
	}
}

// owningRoot returns the project root of the nearest planned workspace that
// contains moduleRoot, or "" if none does.
func (t workspaceMigrationConfigTargets) owningRoot(moduleRoot string) (string, error) {
	var owning string
	consider := func(root string) error {
		contains, err := workspaceMigrationPathContains(root, moduleRoot)
		if err != nil {
			return err
		}
		if contains && (owning == "" || len(root) > len(owning)) {
			owning = root
		}
		return nil
	}
	for root := range t.workspacePlansByRoot {
		if err := consider(root); err != nil {
			return "", err
		}
	}
	for root := range t.parentPlanIndexes {
		if err := consider(root); err != nil {
			return "", err
		}
	}
	return owning, nil
}

// update applies transform to the workspace config identified by root, which
// must be an exact planned root (e.g. from owningRoot or a parent assignment).
func (t workspaceMigrationConfigTargets) update(root string, transform func([]byte) ([]byte, error)) error {
	clean := filepath.Clean(root)
	if plan, ok := t.workspacePlansByRoot[clean]; ok {
		updated, err := transform(plan.WorkspaceConfigData)
		if err != nil {
			return err
		}
		plan.WorkspaceConfigData = updated
		return nil
	}
	if idx, ok := t.parentPlanIndexes[clean]; ok {
		updated, err := transform(t.parentPlans[idx].WorkspaceConfigData)
		if err != nil {
			return err
		}
		t.parentPlans[idx].WorkspaceConfigData = updated
		return nil
	}
	return fmt.Errorf("workspace config for %q is not planned", root)
}

func workspaceMigrationInstallParentSDKModules(
	workspacePlans []*workspace.MigrationPlan,
	parentPlans []workspaceMigrationParentPlan,
	assignments []workspaceMigrationParentAssignment,
) ([]workspaceMigrationParentPlan, error) {
	// NOTE(workspace-migrate): These SDK installs are written to the parent
	// workspace configs without refreshing lock entries during migration. Future
	// workspace commands can resolve them through the normal lock refresh path.
	//
	// The install is recorded through the same as-sdk mechanism as every other
	// migrated runtime (short name, resolved to its real ref by the setup SDK
	// fixup pass), so a discovered local module sharing the runtime reuses the
	// same entry — one SDK install serves every module in the repo.
	sdksByParent := map[string][]string{}
	for _, assignment := range assignments {
		cfg := assignment.CompatWorkspace.Config
		if cfg == nil || cfg.SDK == nil || cfg.SDK.Source == "" {
			continue
		}
		parentRoot := assignment.ParentProjectRoot
		sdksByParent[parentRoot] = append(sdksByParent[parentRoot], cfg.SDK.Source)
	}
	if len(sdksByParent) == 0 {
		return parentPlans, nil
	}

	targets := newWorkspaceMigrationConfigTargets(workspacePlans, parentPlans)

	parentRoots := make([]string, 0, len(sdksByParent))
	for parentRoot := range sdksByParent {
		parentRoots = append(parentRoots, parentRoot)
	}
	sort.Strings(parentRoots)

	for _, parentRoot := range parentRoots {
		sources := sdksByParent[parentRoot]
		if err := targets.update(parentRoot, func(data []byte) ([]byte, error) {
			cfg, err := workspace.ParseConfig(data)
			if err != nil {
				return nil, err
			}
			for _, source := range sources {
				workspace.AddMigratedSDKInstall(cfg, source)
			}
			return workspace.UpdateConfigBytes(data, cfg)
		}); err != nil {
			return nil, fmt.Errorf("install SDK modules at %s: %w", parentRoot, err)
		}
	}

	return parentPlans, nil
}

// workspaceMigrationInstallDiscoveredModuleSDKs records the runtime of every
// discovered, converted-in-place local module in the workspace config that owns
// it, so a module loaded from a local ref inside the workspace has its SDK
// installed and pinned (with the module listed under [[modules.<sdk>.as-sdk.modules]]).
// Discovered modules are converted in place and deliberately skip the parent-plan
// flow (no "requires explicit loading" warning), so their SDK install is handled
// here instead.
func workspaceMigrationInstallDiscoveredModuleSDKs(
	workspacePlans []*workspace.MigrationPlan,
	parentPlans []workspaceMigrationParentPlan,
	compatWorkspaces []*workspace.CompatWorkspace,
) ([]workspaceMigrationParentPlan, error) {
	targets := newWorkspaceMigrationConfigTargets(workspacePlans, parentPlans)

	for _, compatWorkspace := range compatWorkspaces {
		if compatWorkspace == nil || !compatWorkspace.DiscoveredLocalModule {
			continue
		}
		if compatWorkspace.Config == nil || compatWorkspace.Config.SDK == nil || compatWorkspace.Config.SDK.Source == "" {
			continue
		}

		owner, err := targets.owningRoot(compatWorkspace.ProjectRoot)
		if err != nil {
			return nil, err
		}
		if owner == "" {
			// Every discovered module descends from a config that is itself
			// migrated into a workspace, so a planned owner is expected.
			return nil, fmt.Errorf("no migrated workspace owns discovered module %q", compatWorkspace.ProjectRoot)
		}
		modulePath, err := filepath.Rel(owner, compatWorkspace.ProjectRoot)
		if err != nil {
			return nil, fmt.Errorf("discovered module %q path: %w", compatWorkspace.ProjectRoot, err)
		}
		modulePath = filepath.ToSlash(filepath.Clean(modulePath))
		sdkSource := compatWorkspace.Config.SDK.Source

		if err := targets.update(owner, func(data []byte) ([]byte, error) {
			return workspaceMigrationConfigWithMigratedModuleSDK(data, sdkSource, modulePath)
		}); err != nil {
			return nil, fmt.Errorf("install SDK for discovered module %q: %w", compatWorkspace.ProjectRoot, err)
		}
	}
	return parentPlans, nil
}

func workspaceMigrationConfigWithMigratedModuleSDK(configData []byte, sdkSource, modulePath string) ([]byte, error) {
	cfg, err := workspace.ParseConfig(configData)
	if err != nil {
		return nil, err
	}
	workspace.AddMigratedModuleSDK(cfg, sdkSource, modulePath)
	return workspace.UpdateConfigBytes(configData, cfg)
}

func workspaceMigrationWarnExplicitModuleLoading(
	ws *core.Workspace,
	workspacePlans []*workspace.MigrationPlan,
	parentPlans []workspaceMigrationParentPlan,
	assignments []workspaceMigrationParentAssignment,
) ([]workspaceMigrationParentPlan, error) {
	if len(assignments) == 0 {
		return parentPlans, nil
	}

	workspacePlansByRoot := make(map[string]*workspace.MigrationPlan, len(workspacePlans))
	for _, plan := range workspacePlans {
		if plan == nil || plan.ProjectRoot == "" {
			continue
		}
		workspacePlansByRoot[plan.ProjectRoot] = plan
	}
	parentPlanIndexes := make(map[string]int, len(parentPlans))
	for i, plan := range parentPlans {
		parentPlanIndexes[plan.ProjectRoot] = i
	}

	for _, assignment := range assignments {
		moduleDir, err := workspaceMigrationProjectRootRelPath(ws, assignment.CompatWorkspace.ProjectRoot)
		if err != nil {
			return nil, err
		}
		moduleDir = workspaceMigrationDisplayPath(moduleDir)

		warning := workspaceMigrationExplicitModuleWarning(moduleDir)
		reportEntry := workspaceMigrationExplicitModuleReportEntry(moduleDir)

		if plan, ok := workspacePlansByRoot[assignment.ParentProjectRoot]; ok {
			appendWorkspaceMigrationNonGapWarnings(plan, []string{warning})
			workspaceMigrationAppendPlanReport(plan, reportEntry)
			continue
		}

		parentIndex, ok := parentPlanIndexes[assignment.ParentProjectRoot]
		if !ok {
			return nil, fmt.Errorf("parent workspace config for %s is not planned", assignment.ParentProjectRoot)
		}
		parentPlans[parentIndex].Warnings = append(parentPlans[parentIndex].Warnings, warning)
		parentPlans[parentIndex].MigrationReportPath = workspaceMigrationReportPath
		parentPlans[parentIndex].MigrationReportData = workspaceMigrationAppendReportEntry(parentPlans[parentIndex].MigrationReportData, reportEntry)
	}

	return parentPlans, nil
}

func workspaceMigrationAppendPlanReport(plan *workspace.MigrationPlan, reportEntry string) {
	if plan.MigrationReportPath == "" {
		plan.MigrationReportPath = workspaceMigrationReportPath
	}
	plan.MigrationReportData = workspaceMigrationAppendReportEntry(plan.MigrationReportData, reportEntry)
}

func workspaceMigrationExplicitModuleWarning(moduleDir string) string {
	if moduleDir == "." {
		return "Root module requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m . ...`."
	}
	return fmt.Sprintf("%s requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m %s ...`.", moduleDir, moduleDir)
}

func workspaceMigrationExplicitModuleReportEntry(moduleDir string) string {
	if moduleDir == "." {
		return "## Root module requires explicit loading\n\n" +
			"The root module is still valid, but it must be loaded explicitly.\n\n" +
			"- **This works**: `dagger -m . call --help`\n" +
			"- **This no longer works**: `dagger call --help`\n\n" +
			"ACTION: If your scripts rely on implicit loading of the root module, change them to use explicit loading.\n"
	}
	return fmt.Sprintf(
		"## %s requires explicit loading\n\n"+
			"The module at `%s` is still valid, but it must be loaded explicitly.\n\n"+
			"- **This works**: `dagger -m %s call --help`\n"+
			"- **This no longer works**: `cd %s; dagger call --help`\n\n"+
			"ACTION: If your scripts rely on implicit loading of `%s`, change them to use explicit loading.\n",
		moduleDir,
		moduleDir,
		moduleDir,
		moduleDir,
		moduleDir,
	)
}

func workspaceMigrationAppendReportEntry(reportData []byte, reportEntry string) []byte {
	var b strings.Builder
	if len(reportData) == 0 {
		b.WriteString("# Migration Report\n\n")
	} else {
		existing := string(reportData)
		b.WriteString(existing)
		if !strings.HasSuffix(existing, "\n") {
			b.WriteString("\n")
		}
		if !strings.HasSuffix(existing, "\n\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString(reportEntry)
	if !strings.HasSuffix(reportEntry, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func workspaceMigrationPathContains(parent, child string) (bool, error) {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false, err
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel), nil
}
