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
	telemetry "github.com/dagger/otel-go"
)

type workspaceMigrateArgs struct {
	// Proceed even if modules cannot be loaded to generate settings hints.
	Force bool `default:"false"`
}

func (s *workspaceSchema) migrate(
	ctx context.Context,
	ws *core.Workspace,
	args workspaceMigrateArgs,
) (migration *core.WorkspaceMigration, rerr error) {
	if ws.HostPath() == "" {
		return nil, fmt.Errorf("workspace migration is local-only")
	}

	emptyChanges, err := core.NewEmptyChangeset(ctx)
	if err != nil {
		return nil, err
	}

	compatWorkspace := ws.CompatWorkspace()
	if compatWorkspace == nil {
		return &core.WorkspaceMigration{
			Changes: emptyChanges,
			Steps:   nil,
		}, nil
	}

	ctx, span := core.Tracer(ctx).Start(ctx, "prepare workspace migration")
	defer telemetry.EndWithCause(span, &rerr)

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}

	query, err := core.CurrentQuery(workspaceCtx)
	if err != nil {
		return nil, err
	}

	var plan *workspace.MigrationPlan
	if err := func() (rerr error) {
		_, span := core.Tracer(ctx).Start(ctx, "plan migration")
		defer telemetry.EndWithCause(span, &rerr)
		plan, rerr = workspace.PlanMigration(compatWorkspace)
		return rerr
	}(); err != nil {
		return nil, err
	}
	cfg, err := workspace.ParseConfig(plan.WorkspaceConfigData)
	if err != nil {
		return nil, fmt.Errorf("parse planned workspace config: %w", err)
	}
	var updatedDir dagql.ObjectResult[*core.Directory]
	if workspaceConfigUsesMigratedModuleSources(cfg) {
		if _, preparedDir, err := s.workspaceMigrationPreparedDirectories(ctx, ws, plan); err == nil {
			updatedDir = preparedDir
		}
	}
	hints, hintWarnings := s.collectWorkspaceSettingsHintsFromConfig(ctx, ws, cfg, plan.ProjectRoot, updatedDir)
	if len(hintWarnings) > 0 {
		if !args.Force {
			return nil, fmt.Errorf("could not load modules to generate settings hints: %s; use --force to migrate anyway", strings.Join(hintWarnings, "; "))
		}
		appendWorkspaceMigrationNonGapWarnings(plan, hintWarnings)
	}
	if len(hints) > 0 {
		updated, err := workspace.UpdateConfigBytesWithHints(plan.WorkspaceConfigData, cfg, hints)
		if err != nil {
			return nil, fmt.Errorf("render planned workspace config with hints: %w", err)
		}
		plan.WorkspaceConfigData = updated
	}

	warnings := workspaceMigrationWarnings(plan)

	lockBytes, err := s.workspaceMigrationLockBytes(workspaceCtx, query, plan)
	if err != nil {
		return nil, err
	}

	changes, err := s.workspaceMigrationChangeset(ctx, ws, plan, lockBytes)
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

func (s *workspaceSchema) workspaceMigrationLockBytes(
	ctx context.Context,
	query *core.Query,
	plan *workspace.MigrationPlan,
) (_ []byte, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "refresh workspace lock")
	defer telemetry.EndWithCause(span, &rerr)

	var lock *workspace.Lock
	if len(plan.LockData) > 0 {
		parsed, err := workspace.ParseLock(plan.LockData)
		if err != nil {
			return nil, fmt.Errorf("parse planned workspace lock: %w", err)
		}
		lock = parsed
	} else {
		lock = workspace.NewLock()
	}

	refreshMods := make([]workspaceRefreshModule, 0, len(plan.LookupSources))
	for _, source := range plan.LookupSources {
		if _, ok, err := lock.GetModuleResolve(source); err != nil {
			return nil, err
		} else if ok {
			continue
		}
		refreshMods = append(refreshMods, workspaceRefreshModule{
			Name:   source,
			Source: source,
		})
	}
	if len(refreshMods) > 0 {
		if err := refreshWorkspaceModuleLookups(ctx, query, lock, refreshMods); err != nil {
			return nil, fmt.Errorf("refresh migrated module lookups: %w", err)
		}
	}

	entries, err := lock.Entries()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	lockBytes, err := lock.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal workspace lock: %w", err)
	}
	return lockBytes, nil
}

func (s *workspaceSchema) workspaceMigrationChangeset(
	ctx context.Context,
	ws *core.Workspace,
	plan *workspace.MigrationPlan,
	lockBytes []byte,
) (_ *core.Changeset, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "build migration changeset")
	defer telemetry.EndWithCause(span, &rerr)

	baseDir, updatedDir, err := s.workspaceMigrationPreparedDirectories(ctx, ws, plan)
	if err != nil {
		return nil, err
	}

	updatedDir, err = withWorkspaceMigrationFile(ctx, updatedDir, filepath.Join(workspace.LockDirName, workspace.ConfigFileName), plan.WorkspaceConfigData, "write workspace config")
	if err != nil {
		return nil, err
	}

	if len(plan.MigrationReportData) > 0 {
		updatedDir, err = withWorkspaceMigrationFile(ctx, updatedDir, plan.MigrationReportPath, plan.MigrationReportData, "write migration report")
		if err != nil {
			return nil, err
		}
	}

	if len(lockBytes) > 0 {
		updatedDir, err = withWorkspaceMigrationFile(ctx, updatedDir, filepath.Join(workspace.LockDirName, workspace.LockFileName), lockBytes, "write workspace lock")
		if err != nil {
			return nil, err
		}
	}

	if err := func() (rerr error) {
		removeCtx, span := core.Tracer(ctx).Start(ctx, "remove legacy config")
		defer telemetry.EndWithCause(span, &rerr)
		var err error
		updatedDir, err = workspaceMigrationSelectDirectory(removeCtx, updatedDir, "withoutFile", []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(workspace.ModuleConfigFileName)},
		})
		return err
	}(); err != nil {
		return nil, fmt.Errorf("migration remove legacy config: %w", err)
	}

	var changes *core.Changeset
	if err := func() (rerr error) {
		diffCtx, span := core.Tracer(ctx).Start(ctx, "compute migration changeset")
		defer telemetry.EndWithCause(span, &rerr)
		var err error
		changes, err = workspaceMigrationChanges(diffCtx, updatedDir, baseDir)
		return err
	}(); err != nil {
		return nil, fmt.Errorf("migration changeset: %w", err)
	}
	return changes, nil
}

func (s *workspaceSchema) workspaceMigrationPreparedDirectories(
	ctx context.Context,
	ws *core.Workspace,
	plan *workspace.MigrationPlan,
) (_ dagql.ObjectResult[*core.Directory], _ dagql.ObjectResult[*core.Directory], rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "prepare migrated workspace directory")
	defer telemetry.EndWithCause(span, &rerr)

	baseDir, err := s.workspaceMigrationBaseDirectory(ctx, ws, plan)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, dagql.ObjectResult[*core.Directory]{}, err
	}

	updatedDir := baseDir

	if len(plan.MigratedModuleConfigData) > 0 {
		updatedDir, err = withWorkspaceMigrationFile(ctx, updatedDir, plan.MigratedModuleConfigPath, plan.MigratedModuleConfigData, "write migrated module config")
		if err != nil {
			return dagql.ObjectResult[*core.Directory]{}, dagql.ObjectResult[*core.Directory]{}, err
		}
	}

	return baseDir, updatedDir, nil
}

func (s *workspaceSchema) workspaceMigrationBaseDirectory(
	ctx context.Context,
	ws *core.Workspace,
	plan *workspace.MigrationPlan,
) (dagql.ObjectResult[*core.Directory], error) {
	projectRootPath, err := workspaceMigrationProjectRootPath(ws, plan)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}

	baseDir, err := s.resolveRootfs(ctx, ws, projectRootPath, core.CopyFilter{}, false)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}

	return baseDir, nil
}

func withWorkspaceMigrationFile(
	ctx context.Context,
	dir dagql.ObjectResult[*core.Directory],
	filePath string,
	contents []byte,
	spanName string,
) (updated dagql.ObjectResult[*core.Directory], rerr error) {
	if spanName == "" {
		spanName = "write migration file"
	}
	ctx, span := core.Tracer(ctx).Start(ctx, spanName)
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
) (*core.Changeset, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var changes dagql.ObjectResult[*core.Changeset]
	beforeID, err := before.ID()
	if err != nil {
		return nil, err
	}
	if err := srv.Select(ctx, after, &changes, dagql.Selector{
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)},
		},
	}); err != nil {
		return nil, err
	}
	return changes.Self(), nil
}

func workspaceMigrationProjectRootPath(ws *core.Workspace, plan *workspace.MigrationPlan) (string, error) {
	if ws.HostPath() == "" {
		return "", fmt.Errorf("workspace migration is local-only")
	}
	if plan == nil || plan.ProjectRoot == "" {
		return "", fmt.Errorf("migration project root is unavailable")
	}

	rel, err := filepath.Rel(ws.HostPath(), plan.ProjectRoot)
	if err != nil {
		return "", fmt.Errorf("migration project root: %w", err)
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("migration project root %q escapes workspace boundary %q", plan.ProjectRoot, ws.HostPath())
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
			fmt.Sprintf("%d migration gap(s) need manual review; see %s", plan.MigrationGapCount, plan.MigrationReportPath),
		)
	}
	return warnings
}

func workspaceConfigUsesMigratedModuleSources(cfg *workspace.Config) bool {
	if cfg == nil {
		return false
	}

	migratedModulesDir := filepath.Clean(filepath.Join(workspace.LockDirName, "modules"))
	for _, entry := range cfg.Modules {
		resolvedSource := workspace.ResolveModuleEntrySource(workspace.LockDirName, entry.Source)
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
