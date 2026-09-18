package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

var (
	generateListMode    bool
	generateRequireLoad bool
	generateNoApply     bool
)

func init() {
	generateCmd.Flags().BoolVarP(&generateListMode, "list", "l", false, "List available generators")
	generateCmd.Flags().BoolVar(&generateRequireLoad, "require-load", false, "Fail if any workspace module cannot be loaded (default: report as a warning and generate the rest)")
	generateCmd.Flags().BoolVar(&generateNoApply, "no-apply", false, "Compute and show a summary of generated changes without applying them")
}

var generateCmd = &cobra.Command{
	Use:   "generate [options] [address...]",
	Short: "Generate derived files for your project — code, SDKs, types, docs, etc.",
	Long: `Generate derived files for your project — code, SDKs, types, docs, etc.

Examples:
  dagger generate                            # Generate all assets
  dagger generate -l                         # List all available generators
  dagger generate --no-apply                 # Show generated changes without applying them
  dagger generate dag://go/bin                     # Generate by selecting the generator function
  dagger -W github.com/acme/ws generate dag://go/bin  # Generate against explicit workspace
`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		disposition, err := generateChangesetDisposition(generateListMode, autoApply, generateNoApply, idtui.RunningInAgent())
		if err != nil {
			return err
		}

		params := client.Params{
			LoadWorkspaceModules: true,
		}
		params, err = artifactClientParams(params, args)
		if err != nil {
			return err
		}
		return withEngine(
			cmd.Context(),
			params,
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				ws := dag.CurrentWorkspace()
				all, err := commandArtifacts(ctx, dag, ws, args, generateRequireLoad)
				if err != nil {
					return err
				}
				generators := all.FilterDirectives([]string{"generate"})
				if generateListMode {
					return listArtifactSelection(ctx, dag, generators, cmd.OutOrStdout())
				}
				failures, err := artifactLoadFailures(ctx, dag, all)
				if err != nil {
					return err
				}
				return runGenerators(ctx, dag, generators, failures, cmd, disposition)
			},
		)
	},
}

func generateChangesetDisposition(list, apply, noApply, runningInAgent bool) (changesetDisposition, error) {
	if apply && noApply {
		return changesetDispositionPrompt, errors.New("--auto-apply and --no-apply cannot be used together")
	}
	if list {
		return changesetDispositionPrompt, nil
	}
	if apply {
		return changesetDispositionApply, nil
	}
	if noApply {
		return changesetDispositionNoApply, nil
	}
	if runningInAgent {
		return changesetDispositionPrompt, errors.New(`dagger generate requires an explicit changeset choice when run by a coding agent:
  pass -y/--auto-apply to apply generated changes
  pass --no-apply to show generated changes without applying them

For an up-to-date check that fails on pending changes, use dagger check --generate`)
	}
	return changesetDispositionPrompt, nil
}

// 'dagger generators' (runs by default)
func runGenerators(ctx context.Context, dag *dagger.Client, generatorGroup *dagger.Artifacts, failures []artifactLoadFailure, cmd *cobra.Command, disposition changesetDisposition) (rerr error) {
	ctx, zoomSpan := Tracer().Start(ctx, "generators", telemetry.Passthrough())
	defer zoomSpan.End()
	Frontend.SetPrimary(dagui.SpanID{SpanID: zoomSpan.SpanContext().SpanID()})
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	previewOut := cmd.ErrOrStderr()
	if disposition == changesetDispositionNoApply {
		// SetPrimary above focuses rendering on the generators span. Attach the
		// preview to that span too; command stderr is attached to the root span
		// and would otherwise be hidden by plain/report progress frontends.
		previewStdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
		defer previewStdio.Close()
		previewOut = previewStdio.Stderr
	}
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	results, err := evaluateArtifacts(ctx, dag, generatorGroup, false)
	if err != nil {
		return err
	}
	if err := artifactResultErrors(results); err != nil {
		return err
	}
	changes := make([]*dagger.Changeset, 0, len(results))
	for _, result := range results {
		if result.Value == nil || result.Value.Type != "Changeset" {
			return fmt.Errorf("%s did not return a Changeset", result.Artifact.URI)
		}
		changes = append(changes, dagger.Ref[*dagger.Changeset](dag, result.Value.ID))
	}
	cwd, err := dag.CurrentWorkspace().Cwd(ctx)
	if err != nil {
		return err
	}
	cwd = strings.Trim(cwd, "/")
	if cwd != "" && cwd != "." {
		for i, changeset := range changes {
			before := dag.Directory().WithDirectory(cwd, changeset.Before())
			after := dag.Directory().WithDirectory(cwd, changeset.After())
			changes[i] = after.Changes(before)
		}
	}
	merged := dag.Changeset().WithChangesets(changes, dagger.ChangesetWithChangesetsOpts{OnConflict: dagger.ChangesetsMergeConflictFailEarly})
	if err := verifyRegeneratedModules(ctx, dag, merged, failures); err != nil {
		return err
	}
	generated := dag.CurrentWorkspace().WithChanges(merged)
	_, err = handleWorkspaceResponseWithDisposition(ctx, dag, dag.CurrentWorkspace(), generated, disposition, previewOut)
	if errors.Is(err, idtui.ErrNonInteractive) {
		return fmt.Errorf("%w; pass -y/--auto-apply to apply changes, or --no-apply to show them without applying", idtui.ErrNonInteractive)
	}
	return err
}

// Check previously unloadable modules against the complete generated tree.
// A load failure remains a warning because generation can repair other files.
func verifyRegeneratedModules(ctx context.Context, dag *dagger.Client, changes *dagger.Changeset, failures []artifactLoadFailure) error {
	if len(failures) == 0 {
		return nil
	}
	ws := dag.CurrentWorkspace()
	cfg, err := artifactWorkspaceConfig(ctx, dag, ws)
	if err != nil {
		return err
	}
	configFile, err := ws.ConfigFile(ctx)
	if err != nil {
		return err
	}
	added, err := changes.AddedPaths(ctx)
	if err != nil {
		return err
	}
	modified, err := changes.ModifiedPaths(ctx)
	if err != nil {
		return err
	}
	removed, err := changes.RemovedPaths(ctx)
	if err != nil {
		return err
	}
	touched := append(append(added, modified...), removed...)
	root := ws.Directory("/").WithChanges(changes)
	for _, failure := range failures {
		address, err := dagaddress.Parse(failure.URI)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(address.Path, "/load")
		entry, ok := cfg.Modules[name]
		if !ok || !workspace.IsLocalRef(entry.Source, "") {
			continue
		}
		dir := workspace.ResolveModuleEntrySource(path.Dir(configFile), entry.Source)
		changed := false
		for _, file := range touched {
			changed = changed || dir == "." || file == dir || strings.HasPrefix(file, dir+"/")
		}
		if !changed {
			continue
		}
		loadCtx, span := Tracer().Start(ctx, name, telemetry.Reveal(), trace.WithAttributes(
			attribute.Bool(telemetryattrs.GenerateRegeneratedAttr, true),
			attribute.Bool(telemetry.UIRollUpLogsAttr, true),
			attribute.Bool(telemetry.UIRollUpSpansAttr, true),
		))
		_, loadErr := root.AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: dir}).AsModule().Name(loadCtx)
		telemetry.EndWithCause(span, &loadErr)
	}
	return nil
}
