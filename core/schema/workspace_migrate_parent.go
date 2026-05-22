package schema

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	coresdk "github.com/dagger/dagger/core/sdk"
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
	assignments, err := workspaceMigrationParentAssignments(ws, compatWorkspaces, workspacePlans)
	if err != nil {
		return nil, err
	}
	parentPlans, err := workspaceMigrationParentPlans(ws, assignments, workspacePlans)
	if err != nil {
		return nil, err
	}
	if parentPlans, err = workspaceMigrationInstallParentSDKModules(ws, workspacePlans, parentPlans, assignments); err != nil {
		return nil, err
	}
	return workspaceMigrationWarnExplicitModuleLoading(ws, workspacePlans, parentPlans, assignments)
}

func workspaceMigrationParentAssignments(
	ws *core.Workspace,
	compatWorkspaces []*workspace.CompatWorkspace,
	workspacePlans []*workspace.MigrationPlan,
) ([]workspaceMigrationParentAssignment, error) {
	if ws == nil || ws.HostPath() == "" {
		return nil, fmt.Errorf("workspace host path is required")
	}

	assignments := make([]workspaceMigrationParentAssignment, 0, len(compatWorkspaces))
	for _, compatWorkspace := range compatWorkspaces {
		if compatWorkspace == nil || compatWorkspace.MustMigrateToWorkspaceConfig() {
			continue
		}
		parentRoot, err := workspaceMigrationNearestPlannedParent(compatWorkspace.ProjectRoot, workspacePlans)
		if err != nil {
			return nil, err
		}
		if parentRoot == "" {
			parentRoot = ws.HostPath()
		}
		assignments = append(assignments, workspaceMigrationParentAssignment{
			CompatWorkspace:   compatWorkspace,
			ParentProjectRoot: parentRoot,
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
			WorkspaceConfigData: []byte(minimalWorkspaceConfig),
		})
	}
	return parentPlans, nil
}

func workspaceMigrationNearestPlannedParent(projectRoot string, plans []*workspace.MigrationPlan) (string, error) {
	if projectRoot == "" {
		return "", fmt.Errorf("module project root is required")
	}
	var nearest string
	for _, plan := range plans {
		if plan == nil || plan.ProjectRoot == "" {
			continue
		}
		contains, err := workspaceMigrationPathContains(plan.ProjectRoot, projectRoot)
		if err != nil {
			return "", fmt.Errorf("planned parent path: %w", err)
		}
		if !contains {
			continue
		}
		if nearest == "" || len(filepath.Clean(plan.ProjectRoot)) > len(filepath.Clean(nearest)) {
			nearest = plan.ProjectRoot
		}
	}
	return nearest, nil
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

func workspaceMigrationInstallParentSDKModules(
	ws *core.Workspace,
	workspacePlans []*workspace.MigrationPlan,
	parentPlans []workspaceMigrationParentPlan,
	assignments []workspaceMigrationParentAssignment,
) ([]workspaceMigrationParentPlan, error) {
	// NOTE(workspace-migrate): These SDK modules are written to the parent
	// workspace configs without refreshing lock entries during migration. Future
	// workspace commands can resolve them through the normal lock refresh path.
	modulesByParent := map[string][]coresdk.WorkspaceModule{}
	skippedByParent := map[string][]workspaceMigrationSkippedSDKInstall{}
	for _, assignment := range assignments {
		mod, ok, err := workspaceMigrationSDKModule(assignment.CompatWorkspace)
		if err != nil {
			return nil, err
		}
		parentRoot := assignment.ParentProjectRoot
		if !ok {
			skipped, ok, err := workspaceMigrationSkippedSDKInstallForAssignment(ws, assignment)
			if err != nil {
				return nil, err
			}
			if ok {
				skippedByParent[parentRoot] = append(skippedByParent[parentRoot], skipped)
			}
			continue
		}
		modulesByParent[parentRoot] = append(modulesByParent[parentRoot], mod)
	}
	if len(modulesByParent) == 0 && len(skippedByParent) == 0 {
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

	parentRootSet := map[string]struct{}{}
	for parentRoot := range modulesByParent {
		parentRootSet[parentRoot] = struct{}{}
	}
	for parentRoot := range skippedByParent {
		parentRootSet[parentRoot] = struct{}{}
	}
	parentRoots := make([]string, 0, len(parentRootSet))
	for parentRoot := range parentRootSet {
		parentRoots = append(parentRoots, parentRoot)
	}
	sort.Strings(parentRoots)

	for _, parentRoot := range parentRoots {
		mods := workspaceMigrationDedupSDKModules(modulesByParent[parentRoot])
		skipped := workspaceMigrationDedupSkippedSDKInstalls(skippedByParent[parentRoot])
		if len(mods) == 0 && len(skipped) == 0 {
			continue
		}

		if plan, ok := workspacePlansByRoot[parentRoot]; ok {
			updated, err := workspaceMigrationConfigWithSDKModules(plan.WorkspaceConfigData, mods)
			if err != nil {
				return nil, fmt.Errorf("install SDK modules in migrated workspace config at %s: %w", parentRoot, err)
			}
			updated = workspaceMigrationConfigWithSkippedSDKComments(updated, skipped)
			plan.WorkspaceConfigData = updated
			workspaceMigrationAppendSkippedSDKReports(plan, skipped)
			continue
		}

		parentIndex, ok := parentPlanIndexes[parentRoot]
		if !ok {
			return nil, fmt.Errorf("parent workspace config for %s is not planned", parentRoot)
		}
		updated, err := workspaceMigrationConfigWithSDKModules(parentPlans[parentIndex].WorkspaceConfigData, mods)
		if err != nil {
			return nil, fmt.Errorf("install SDK modules in parent workspace config at %s: %w", parentRoot, err)
		}
		parentPlans[parentIndex].WorkspaceConfigData = workspaceMigrationConfigWithSkippedSDKComments(updated, skipped)
		workspaceMigrationAppendSkippedSDKParentReports(&parentPlans[parentIndex], skipped)
	}

	return parentPlans, nil
}

type workspaceMigrationSkippedSDKInstall struct {
	ModuleDir string
	Runtime   string
}

func workspaceMigrationSDKModule(compatWorkspace *workspace.CompatWorkspace) (coresdk.WorkspaceModule, bool, error) {
	if compatWorkspace == nil || compatWorkspace.Config == nil || compatWorkspace.Config.SDK == nil {
		return coresdk.WorkspaceModule{}, false, nil
	}
	return coresdk.WorkspaceModuleForRuntime(compatWorkspace.Config.SDK.Source)
}

func workspaceMigrationSkippedSDKInstallForAssignment(
	ws *core.Workspace,
	assignment workspaceMigrationParentAssignment,
) (workspaceMigrationSkippedSDKInstall, bool, error) {
	compatWorkspace := assignment.CompatWorkspace
	if compatWorkspace == nil || compatWorkspace.Config == nil || compatWorkspace.Config.SDK == nil || compatWorkspace.Config.SDK.Source == "" {
		return workspaceMigrationSkippedSDKInstall{}, false, nil
	}
	moduleDir, err := workspaceMigrationProjectRootRelPath(ws, compatWorkspace.ProjectRoot)
	if err != nil {
		return workspaceMigrationSkippedSDKInstall{}, false, err
	}
	return workspaceMigrationSkippedSDKInstall{
		ModuleDir: workspaceMigrationDisplayPath(moduleDir),
		Runtime:   compatWorkspace.Config.SDK.Source,
	}, true, nil
}

func workspaceMigrationDedupSDKModules(mods []coresdk.WorkspaceModule) []coresdk.WorkspaceModule {
	if len(mods) == 0 {
		return nil
	}
	seen := map[coresdk.WorkspaceModule]struct{}{}
	deduped := make([]coresdk.WorkspaceModule, 0, len(mods))
	for _, mod := range mods {
		if mod.Name == "" || mod.Source == "" {
			continue
		}
		if _, ok := seen[mod]; ok {
			continue
		}
		seen[mod] = struct{}{}
		deduped = append(deduped, mod)
	}
	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].Name == deduped[j].Name {
			return deduped[i].Source < deduped[j].Source
		}
		return deduped[i].Name < deduped[j].Name
	})
	return deduped
}

func workspaceMigrationDedupSkippedSDKInstalls(skipped []workspaceMigrationSkippedSDKInstall) []workspaceMigrationSkippedSDKInstall {
	if len(skipped) == 0 {
		return nil
	}
	seen := map[workspaceMigrationSkippedSDKInstall]struct{}{}
	deduped := make([]workspaceMigrationSkippedSDKInstall, 0, len(skipped))
	for _, skip := range skipped {
		if skip.ModuleDir == "" || skip.Runtime == "" {
			continue
		}
		if _, ok := seen[skip]; ok {
			continue
		}
		seen[skip] = struct{}{}
		deduped = append(deduped, skip)
	}
	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].ModuleDir == deduped[j].ModuleDir {
			return deduped[i].Runtime < deduped[j].Runtime
		}
		return deduped[i].ModuleDir < deduped[j].ModuleDir
	})
	return deduped
}

func workspaceMigrationConfigWithSDKModules(
	configData []byte,
	mods []coresdk.WorkspaceModule,
) ([]byte, error) {
	cfg, err := workspace.ParseConfig(configData)
	if err != nil {
		return nil, err
	}

	changed := false
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	for _, mod := range mods {
		if workspaceMigrationInstallSDKModule(cfg.Modules, mod) {
			changed = true
		}
	}
	if !changed {
		return configData, nil
	}

	return workspace.UpdateConfigBytes(configData, cfg)
}

func workspaceMigrationInstallSDKModule(
	modules map[string]workspace.ModuleEntry,
	mod coresdk.WorkspaceModule,
) bool {
	for _, entry := range modules {
		if entry.Source == mod.Source {
			return false
		}
	}

	name := mod.Name
	if existing, ok := modules[name]; ok && existing.Source != mod.Source {
		name = workspaceMigrationUniqueSDKModuleName(modules, name)
	}
	modules[name] = workspace.ModuleEntry{
		Source: mod.Source,
	}
	return true
}

func workspaceMigrationConfigWithSkippedSDKComments(
	configData []byte,
	skipped []workspaceMigrationSkippedSDKInstall,
) []byte {
	if len(skipped) == 0 {
		return configData
	}
	var b strings.Builder
	b.Write(configData)
	if len(configData) > 0 && !strings.HasSuffix(string(configData), "\n") {
		b.WriteString("\n")
	}
	for _, skip := range skipped {
		b.WriteString("# ")
		b.WriteString(workspaceMigrationSkippedSDKInstallMessage(skip))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func workspaceMigrationAppendSkippedSDKReports(plan *workspace.MigrationPlan, skipped []workspaceMigrationSkippedSDKInstall) {
	if plan == nil || len(skipped) == 0 {
		return
	}
	warnings := make([]string, 0, len(skipped))
	for _, skip := range skipped {
		warnings = append(warnings, workspaceMigrationSkippedSDKInstallMessage(skip))
		workspaceMigrationAppendPlanReport(plan, workspaceMigrationSkippedSDKInstallReportEntry(skip))
	}
	appendWorkspaceMigrationNonGapWarnings(plan, warnings)
}

func workspaceMigrationAppendSkippedSDKParentReports(
	plan *workspaceMigrationParentPlan,
	skipped []workspaceMigrationSkippedSDKInstall,
) {
	if plan == nil || len(skipped) == 0 {
		return
	}
	for _, skip := range skipped {
		plan.Warnings = append(plan.Warnings, workspaceMigrationSkippedSDKInstallMessage(skip))
		plan.MigrationReportPath = workspaceMigrationReportPath
		plan.MigrationReportData = workspaceMigrationAppendReportEntry(plan.MigrationReportData, workspaceMigrationSkippedSDKInstallReportEntry(skip))
	}
}

func workspaceMigrationSkippedSDKInstallMessage(skip workspaceMigrationSkippedSDKInstall) string {
	return fmt.Sprintf("Skipped SDK install when migrating module %s: no known SDK for runtime %q", skip.ModuleDir, skip.Runtime)
}

func workspaceMigrationSkippedSDKInstallReportEntry(skip workspaceMigrationSkippedSDKInstall) string {
	return fmt.Sprintf(
		"## Skipped SDK install for %s\n\n"+
			"No workspace SDK module is known for runtime `%s`, so migration did not install one automatically.\n\n"+
			"ACTION: Install the SDK module manually if `%s` needs runtime helpers from the parent workspace.\n",
		skip.ModuleDir,
		skip.Runtime,
		skip.ModuleDir,
	)
}

func workspaceMigrationUniqueSDKModuleName(modules map[string]workspace.ModuleEntry, base string) string {
	candidate := base + "-runtime"
	if _, exists := modules[candidate]; !exists {
		return candidate
	}
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s-runtime-%d", base, i)
		if _, exists := modules[candidate]; !exists {
			return candidate
		}
	}
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
			"The root `dagger.json` is still a valid module, but it must be loaded explicitly.\n\n" +
			"- **This works**: `dagger -m . call --help`\n" +
			"- **This no longer works**: `dagger call --help`\n\n" +
			"ACTION: If your scripts rely on implicit loading of the root module, change them to use explicit loading.\n"
	}
	return fmt.Sprintf(
		"## %s requires explicit loading\n\n"+
			"`%s` is still a valid module, but it must be loaded explicitly.\n\n"+
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
