package daggercmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var workspaceMigrateCmd = newMigrationCommand(false)
var moduleMigrateCmd = newMigrationCommand(true)

func init() {
	workspaceCmd.AddCommand(workspaceMigrateCmd)
	moduleCmd.AddCommand(moduleMigrateCmd)
}

func newMigrationCommand(moduleOnly bool) *cobra.Command {
	var noApply bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate the workspace and its installed local modules",
		Long: `Plan migration of the workspace and its installed local modules.

Other legacy module configurations are optional candidates. They start
unselected and are skipped in non-interactive mode, including with --auto-apply.
Use dagger module migrate PATH to select one explicitly.

Review all selected changes together. Use --auto-apply to apply without a
prompt, or --no-apply to preview without changing files.`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{showFinalProgressKey: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			disposition, err := workspaceExecDisposition(autoApply, noApply)
			if err != nil {
				return err
			}
			interactive := !noApply && canPromptForInit(progress, stdinIsTTY, autoApply)
			var complete, enableCloud bool
			err = withSetupSessions(cmd.Context(), nil, func(ctx context.Context, connect func(context.Context) (*client.Client, func(), error)) error {
				if err := func() error {
					ec, closeSession, err := connect(ctx)
					if err != nil {
						return err
					}
					defer closeSession()
					complete, err = runMigration(ctx, ec.Dagger(), cmd, args, moduleOnly, disposition)
					return err
				}(); err != nil {
					return err
				}
				if complete && !moduleOnly && interactive {
					var err error
					enableCloud, err = offerWorkspaceNextSteps(ctx, cmd, connect)
					return err
				}
				return nil
			})
			if err != nil || !complete || moduleOnly || noApply {
				return err
			}
			if interactive {
				return runWorkspaceCloudNextStep(cmd, enableCloud)
			}
			return printWorkspaceNextSteps(cmd)
		},
	}
	if moduleOnly {
		cmd.Use = "migrate [PATH]"
		cmd.Short = "Migrate one local module in place"
		cmd.Long = `Migrate one local dagger.json to dagger-module.toml in place.

PATH defaults to the workspace current directory. Relative paths start there;
absolute paths start at the workspace root. A dagger.toml is not required.
If one exists, migration also registers the module's SDK scope. Otherwise,
only the requested module is converted; no workspace config is created.
Configurations with workspace fields require workspace migration.

Review module and SDK configuration changes together. Use --auto-apply to apply
without a prompt, or --no-apply to preview without changing files.`
		cmd.Args = cobra.MaximumNArgs(1)
	}
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Preview migration without changing files")
	if !moduleOnly {
		cmd.Flags().StringArray("module", nil, "Also migrate this module explicitly (repeatable)")
	}
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig, mayWriteWorkspaceConfig, mayProduceOutput, mayRenderPipeline)
	setWorkspaceFlagPolicy(cmd)
	return cmd
}

// runMigration presents an engine-owned plan and exports it only after approval.
func runMigration(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, args []string, moduleOnly bool, disposition changesetDisposition) (bool, error) {
	ws := dag.CurrentWorkspace()
	var migration *dagger.WorkspaceMigration
	var selectedModules []string
	if moduleOnly {
		target := "."
		if len(args) > 0 {
			target = args[0]
		}
		migration = ws.MigrateModule(dagger.WorkspaceMigrateModuleOpts{Path: target})
	} else {
		var err error
		selectedModules, err = cmd.Flags().GetStringArray("module")
		if err != nil {
			return false, err
		}
		migration = ws.Migrate(dagger.WorkspaceMigrateOpts{Modules: selectedModules})
	}
	migration, err := materializeMigration(ctx, dag, migration)
	if err != nil {
		return false, err
	}
	changes, err := migration.Changes().Sync(ctx)
	if err != nil {
		return false, err
	}
	if !moduleOnly {
		configFile, err := migration.ConfigFile(ctx)
		if err != nil {
			return false, err
		}
		if configFile == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "No legacy workspace migration is needed. Run %s init to initialize a workspace.\n", commandPrefixForLocalWorkspace(cmd))
			return false, nil
		}
		candidates, err := migration.ModuleCandidates(ctx)
		if err != nil {
			return false, err
		}
		selected, err := selectMigrationCandidates(ctx, cmd, candidates, disposition)
		if err != nil {
			return false, err
		}
		if len(selected) > 0 {
			for _, target := range selected {
				selectedModules = append(selectedModules, "/"+target)
			}
			migration, err = materializeMigration(ctx, dag, ws.Migrate(dagger.WorkspaceMigrateOpts{Modules: selectedModules}))
			if err != nil {
				return false, err
			}
			changes, err = migration.Changes().Sync(ctx)
			if err != nil {
				return false, err
			}
		}
	}
	warnings, err := migrationStepWarnings(ctx, migration)
	if err != nil {
		return false, err
	}
	for _, warning := range warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), warning)
	}
	unchanged, err := changes.IsEmpty(ctx)
	if err != nil {
		return false, err
	}
	if unchanged {
		fmt.Fprintln(cmd.OutOrStdout(), "No migration needed.")
		return disposition != changesetDispositionNoApply, nil
	}
	if disposition == changesetDispositionPrompt {
		command := migrationApplyCommand(cmd, args, moduleOnly, selectedModules)
		setupMessage(ctx, "migration command", "Apply this migration:\n\n```sh\n"+command+"\n```")
		ctx = context.WithValue(ctx, changesetPromptCommandKey{}, command)
	}
	return handleWorkspaceResponseWithDisposition(ctx, dag, ws, ws.WithChanges(changes), disposition, cmd.OutOrStdout())
}

// Keep the preview and metadata on the same plan. Migration reads live files,
// so querying the planning call again could produce a different result.
func materializeMigration(ctx context.Context, dag *dagger.Client, migration *dagger.WorkspaceMigration) (*dagger.WorkspaceMigration, error) {
	id, err := migration.ID(ctx)
	if err != nil {
		return nil, err
	}
	return dagger.Ref[*dagger.WorkspaceMigration](dag, id), nil
}

func migrationApplyCommand(cmd *cobra.Command, args []string, moduleOnly bool, selected []string) string {
	command := commandPrefixForLocalWorkspace(cmd)
	if moduleOnly {
		command += " module migrate"
		for _, arg := range args {
			command += " " + shellQuote(arg)
		}
	} else {
		command += " workspace migrate"
		for _, target := range selected {
			command += " --module " + shellQuote(target)
		}
	}
	return command + " --auto-apply"
}

func selectMigrationCandidates(ctx context.Context, cmd *cobra.Command, candidates []string, disposition changesetDisposition) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if autoApply || disposition == changesetDispositionNoApply || !canPromptForInit(progress, isatty.IsTerminal(os.Stdin.Fd()), false) {
		fmt.Fprintln(cmd.OutOrStdout(), "Optional module candidates were skipped. Some can be fixtures. Select a module explicitly:")
		prefix := commandPrefixForLocalWorkspace(cmd)
		for _, dir := range candidates {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s module migrate %s\n", prefix, shellQuote("/"+dir))
		}
		return nil, nil
	}
	var selected []string
	var migrate bool
	options := make([]huh.Option[string], 0, len(candidates))
	prefix := commandPrefixForLocalWorkspace(cmd)
	for _, dir := range candidates {
		options = append(options, huh.NewOption(prefix+" module migrate "+shellQuote("/"+dir), dir))
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().Title("Other module candidates (can include fixtures)").Options(options...).Value(&selected),
		idtui.NewExplicitConfirm("Migrate selected", "Skip all other modules", &migrate),
	))
	if err := Frontend.HandleForm(ctx, form); err != nil {
		return nil, err
	}
	if !migrate {
		setupMessage(ctx, "optional migrations skipped", "Skipped optional module migrations.")
		return nil, nil
	}
	var commands strings.Builder
	commands.WriteString("Selected module migrations. Changes still need approval.\n\n```sh\n")
	for _, dir := range selected {
		fmt.Fprintf(&commands, "%s module migrate %s\n", prefix, shellQuote("/"+dir))
	}
	commands.WriteString("```\n")
	setupMessage(ctx, "selected module migrations", commands.String())
	return selected, nil
}

func commandPrefixForLocalWorkspace(cmd *cobra.Command) string {
	workdirChanged := false
	if flag := cmd.Flag("workdir"); flag != nil {
		workdirChanged = flag.Changed
	}
	if workspaceRef == "" && !workdirChanged {
		return "dagger"
	}
	if selected, local, err := sdkInitConfigSearchRoot(); err == nil && local {
		return "dagger -W " + shellQuote(selected)
	}
	return "dagger -W " + shellQuote(workspaceRef)
}
