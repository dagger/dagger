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
	DagOpInternalArgs
}

func (s *workspaceSchema) migrate(
	ctx context.Context,
	ws *core.Workspace,
	_ workspaceMigrateArgs,
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

	ctx, span := core.Tracer(ctx).Start(ctx, "Migrated to workspace format")
	defer telemetry.EndWithCause(span, &rerr)
	workspaceMigrationConsole(ctx, "Migrated to workspace format")

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

	warnings := workspaceMigrationWarnings(plan)
	for _, warning := range warnings {
		workspaceMigrationConsole(ctx, "Warning: %s", warning)
	}

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

	refreshMods := make([]workspaceRefreshModule, 0, len(plan.Result.LookupSources))
	for _, source := range plan.Result.LookupSources {
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

	projectRootPath, err := workspaceMigrationProjectRootPath(ws, plan)
	if err != nil {
		return nil, err
	}

	baseDir, err := s.resolveRootfs(ctx, ws, projectRootPath, core.CopyFilter{}, false)
	if err != nil {
		return nil, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	updatedDir := baseDir

	if plan.SourceCopyPath != "" {
		if err := func() (rerr error) {
			copyCtx, span := core.Tracer(ctx).Start(ctx, "migrate source directory")
			defer telemetry.EndWithCause(span, &rerr)

			srcDir, err := s.resolveRootfs(copyCtx, ws, filepath.Join(projectRootPath, plan.SourceCopyPath), core.CopyFilter{}, false)
			if err != nil {
				return fmt.Errorf("migration source directory %q: %w", plan.SourceCopyPath, err)
			}
			if err := srv.Select(copyCtx, updatedDir, &updatedDir,
				dagql.Selector{
					Field: "withDirectory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.NewString(path.Clean(filepath.ToSlash(plan.SourceCopyDest)))},
						{Name: "directory", Value: dagql.NewID[*core.Directory](srcDir.ID())},
					},
				},
			); err != nil {
				return fmt.Errorf("migration copy source directory: %w", err)
			}
			if plan.RemoveOldSource {
				if err := srv.Select(copyCtx, updatedDir, &updatedDir,
					dagql.Selector{
						Field: "withoutDirectory",
						Args: []dagql.NamedInput{
							{Name: "path", Value: dagql.NewString(path.Clean(filepath.ToSlash(plan.SourceCopyPath)))},
						},
					},
				); err != nil {
					return fmt.Errorf("migration remove old source directory: %w", err)
				}
			}
			return nil
		}(); err != nil {
			return nil, err
		}
	}

	if len(plan.MigratedModuleConfigData) > 0 {
		updatedDir, err = withWorkspaceMigrationFile(ctx, srv, updatedDir, plan.MigratedModuleConfigPath, plan.MigratedModuleConfigData, "write migrated module config")
		if err != nil {
			return nil, err
		}
	}

	updatedDir, err = withWorkspaceMigrationFile(ctx, srv, updatedDir, filepath.Join(workspace.LockDirName, workspace.ConfigFileName), plan.WorkspaceConfigData, "write workspace config")
	if err != nil {
		return nil, err
	}

	if len(plan.MigrationReportData) > 0 {
		workspaceMigrationConsole(ctx, "If you apply this migration, review %s.", plan.Result.MigrationReportPath)
		updatedDir, err = withWorkspaceMigrationFile(ctx, srv, updatedDir, plan.Result.MigrationReportPath, plan.MigrationReportData, "write migration report")
		if err != nil {
			return nil, err
		}
	}

	if len(lockBytes) > 0 {
		updatedDir, err = withWorkspaceMigrationFile(ctx, srv, updatedDir, filepath.Join(workspace.LockDirName, workspace.LockFileName), lockBytes, "write workspace lock")
		if err != nil {
			return nil, err
		}
	}

	if err := func() (rerr error) {
		removeCtx, span := core.Tracer(ctx).Start(ctx, "remove legacy config")
		defer telemetry.EndWithCause(span, &rerr)
		return srv.Select(removeCtx, updatedDir, &updatedDir,
			dagql.Selector{
				Field: "withoutFile",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString(workspace.ModuleConfigFileName)},
				},
			},
		)
	}(); err != nil {
		return nil, fmt.Errorf("migration remove legacy config: %w", err)
	}

	var changes dagql.ObjectResult[*core.Changeset]
	if err := func() (rerr error) {
		diffCtx, span := core.Tracer(ctx).Start(ctx, "compute migration changeset")
		defer telemetry.EndWithCause(span, &rerr)
		return srv.Select(diffCtx, updatedDir, &changes,
			dagql.Selector{
				Field: "changes",
				Args: []dagql.NamedInput{
					{Name: "from", Value: dagql.NewID[*core.Directory](baseDir.ID())},
				},
			},
		)
	}(); err != nil {
		return nil, fmt.Errorf("migration changeset: %w", err)
	}

	return changes.Self(), nil
}

func withWorkspaceMigrationFile(
	ctx context.Context,
	srv *dagql.Server,
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
	if err := srv.Select(ctx, dir, &updated,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(path.Clean(filepath.ToSlash(filePath)))},
				{Name: "contents", Value: dagql.String(string(contents))},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		},
	); err != nil {
		return dir, fmt.Errorf("migration write %q: %w", filePath, err)
	}
	return updated, nil
}

func workspaceMigrationConsole(ctx context.Context, msg string, args ...any) {
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	fmt.Fprintf(telemetry.GlobalWriter(ctx, ""), msg, args...)
}

func workspaceMigrationProjectRootPath(ws *core.Workspace, plan *workspace.MigrationPlan) (string, error) {
	if ws.HostPath() == "" {
		return "", fmt.Errorf("workspace migration is local-only")
	}
	if plan == nil || plan.Result == nil || plan.Result.ProjectRoot == "" {
		return "", fmt.Errorf("migration project root is unavailable")
	}

	rel, err := filepath.Rel(ws.HostPath(), plan.Result.ProjectRoot)
	if err != nil {
		return "", fmt.Errorf("migration project root: %w", err)
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("migration project root %q escapes workspace boundary %q", plan.Result.ProjectRoot, ws.HostPath())
	}
	return rel, nil
}

func workspaceMigrationWarnings(plan *workspace.MigrationPlan) []string {
	if plan == nil || plan.Result == nil || len(plan.Result.Warnings) == 0 {
		return nil
	}

	warnings := make([]string, 0, len(plan.Result.Warnings))
	nonGapCount := len(plan.Result.Warnings) - plan.GapCount
	if nonGapCount < 0 {
		nonGapCount = 0
	}
	warnings = append(warnings, plan.Result.Warnings[:nonGapCount]...)
	if plan.GapCount > 0 {
		warnings = append(warnings,
			fmt.Sprintf("%d migration gap(s) need manual review; see %s", plan.GapCount, plan.Result.MigrationReportPath),
		)
	}
	return warnings
}
