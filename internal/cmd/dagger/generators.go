package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	generateListMode    bool
	generateRequireLoad bool
	generateNoApply     bool
)

func init() {
	registerCommandArtifactFlags(generateCmd)
	generateCmd.Flags().BoolVarP(&generateListMode, "list", "l", false, "List available generators")
	generateCmd.Flags().BoolVar(&generateRequireLoad, "require-load", false, "Fail if any workspace module cannot be loaded (default: report as a warning and generate the rest)")
	generateCmd.Flags().BoolVar(&generateNoApply, "no-apply", false, "Compute and show a summary of generated changes without applying them")
}

var generateCmd = &cobra.Command{
	Use:   "generate [FILTERS] [OPTIONS]",
	Short: "Generate derived files for your project — code, SDKs, types, docs, etc.",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		disposition, err := generateChangesetDisposition(generateListMode, autoApply, generateNoApply, idtui.RunningInAgent())
		if err != nil {
			return err
		}

		params := client.Params{
			SkipWorkspaceModules: true,
		}
		params, err = artifactClientParams(params, args)
		if err != nil {
			return err
		}
		return withEngine(
			cmd.Context(),
			params,
			func(ctx context.Context, engineClient *client.Client) error {
				ctx, span := Tracer().Start(ctx, "generators", telemetry.Passthrough())
				defer span.End()
				slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
				dag := engineClient.Dagger()
				ws := dag.CurrentWorkspace()
				all, err := commandArtifactsWithFlags(ctx, dag, ws, cmd, args, generateRequireLoad)
				if err != nil {
					return err
				}
				generators := all.FilterGenerateCommand()
				if generateListMode {
					return listArtifactSelection(ctx, dag, generators, cmd)
				}
				Frontend.SetPrimary(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
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

For an up-to-date check that fails on pending changes, use dagger check --generated=true`)
	}
	return changesetDispositionPrompt, nil
}

func runGenerators(ctx context.Context, dag *dagger.Client, generators *dagger.Artifacts, failures []artifactLoadFailure, cmd *cobra.Command, disposition changesetDisposition) (rerr error) {
	previewOut := cmd.ErrOrStderr()
	if disposition == changesetDispositionNoApply {
		// The primary span focuses rendering on generators. Attach the
		// preview to that span too; command stderr is attached to the root span
		// and would otherwise be hidden by plain/report progress frontends.
		previewStdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
		defer previewStdio.Close()
		previewOut = previewStdio.Stderr
	}
	// We don't actually use the API for rendering results
	// Instead, we rely on telemetry
	// FIXME: this feels a little weird. Can we move the relevant telemetry collection in the API?
	results, err := evaluateArtifacts(ctx, dag, generators, false)
	if err != nil {
		return err
	}
	if err := artifactResultErrors(results); err != nil {
		return err
	}
	changes := make([]*dagger.Changeset, 0, len(results))
	for _, result := range results {
		if result.Value == nil || result.Value.Type != "Generator" {
			return fmt.Errorf("%s did not return a Generator", result.Artifact.URI)
		}
		changes = append(changes, dagger.Ref[*dagger.Generator](dag, result.Value.ID).Changeset())
	}
	cwd, err := dag.CurrentWorkspace().Cwd(ctx)
	if err != nil {
		return err
	}
	cwd = strings.Trim(cwd, "/")
	if cwd != "" && cwd != "." {
		cfg, err := artifactWorkspaceConfig(ctx, dag.CurrentWorkspace())
		if err != nil {
			return err
		}
		// Installed SDK generators produce workspace-root changesets. Module
		// generators produce changesets relative to the invocation directory.
		var sdkPaths []string
		for _, sdk := range cfg.SDKs {
			sdkPaths = append(sdkPaths, cliName(sdk.Module)+"/generate")
		}
		sdkGenerators := map[string]bool{}
		if len(sdkPaths) > 0 {
			// Discovery resolves the active entrypoint, including -m overrides.
			selected := dag.CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: sdkPaths}).FilterGenerateCommand()
			uris, err := artifactURIs(ctx, dag, selected, false)
			if err != nil {
				return err
			}
			for _, uri := range uris {
				sdkGenerators[uri] = true
			}
		}
		for i, changeset := range changes {
			if sdkGenerators[results[i].Artifact.URI] {
				continue
			}
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
	cfg, err := artifactWorkspaceConfig(ctx, ws)
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
	generated := ws.WithChanges(changes)
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
		dir := workspace.ResolveModuleEntrySource(path.Dir(strings.TrimPrefix(configFile, "/")), entry.Source)
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
		remaining, loadErr := artifactLoadFailures(loadCtx, dag, generated.Artifacts(dagger.WorkspaceArtifactsOpts{Include: []string{name}}))
		if loadErr == nil {
			for _, failure := range remaining {
				if failure.URI == "dag://"+name+"/load" {
					loadErr = fmt.Errorf("still fails to load with this run's changes: %s", strings.TrimSuffix(failure.LoadError, "; run `dagger generate` and commit the generated files"))
					break
				}
			}
		}
		telemetry.EndWithCause(span, &loadErr)
	}
	return nil
}
