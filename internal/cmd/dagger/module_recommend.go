package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var moduleRecommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Find recommended modules and select which modules to install",
	Long: `Find modules for the current workspace and select which modules to install.
Already installed modules are skipped.

Use --auto-apply to install all recommended modules without a prompt.
Without --auto-apply, installation is skipped in non-interactive mode.`,
	Args: cobra.NoArgs,
	Annotations: map[string]string{
		showFinalProgressKey: "true",
	},
	RunE: runModuleRecommend,
}

func runModuleRecommend(cmd *cobra.Command, _ []string) error {
	return withEngine(cmd.Context(), client.Params{
		SkipWorkspaceModules: true,
	}, func(ctx context.Context, engineClient *client.Client) error {
		dag := engineClient.Dagger()
		recs, install, err := planRecommend(ctx, dag, nil)
		if err != nil || !install {
			return err
		}
		return installRecommended(ctx, dag, recs, nil)
	})
}

// planRecommend computes the recommended modules and prompts (via a Frontend
// form) whether to install them. Both setup and module recommend use this flow.
// The caller owns the engine session and installs the selected modules.
func planRecommend(ctx context.Context, dag *dagger.Client, ui *setupUI) (recs []recommendation, install bool, rerr error) {
	messageCtx := ctx
	ctx, span := Tracer().Start(ctx, "Find recommended modules", telemetry.Reveal(), telemetry.Encapsulate())
	ui.setRecommend(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
	defer telemetry.EndWithCause(span, &rerr)

	recs, err := runRecommend(ctx, dag)
	if errors.Is(err, errCloudNotAuthenticated) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		// Login or context issues shouldn't fail setup as a whole.
		recommendMessage(ui, messageCtx, "recommendations skipped", "Skipped: "+err.Error())
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(recs) == 0 {
		recommendMessage(ui, messageCtx, "no recommendations", "No recommendations.")
		return nil, false, nil
	}

	// Keep the selection message outside the scan span so it remains visible
	// when the scan details are collapsed.
	recs, err = selectRecommendedModules(messageCtx, recs, ui)
	if err != nil {
		return nil, false, err
	}
	if len(recs) == 0 {
		return nil, false, nil
	}
	return recs, true, nil
}

// installRecommended installs the selected modules in the caller's session.
// Setup opens a fresh session after its migration check.
func installRecommended(ctx context.Context, dag *dagger.Client, recs []recommendation, ui *setupUI) error {
	for _, r := range recs {
		err := func() (rerr error) {
			installCtx, span := Tracer().Start(ctx, "dagger module install "+r.Module.Repo,
				telemetry.Reveal(), telemetry.Encapsulate())
			ui.addInstall(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
			defer telemetry.EndWithCause(span, &rerr)

			stdio := telemetry.SpanStdio(installCtx, InstrumentationLibrary)
			defer stdio.Close()
			return installWorkspaceModule(installCtx, stdio.Stdout, dag, r.Module.Repo, "", false)
		}()
		if err != nil {
			return fmt.Errorf("install %s: %w", r.Module.Repo, err)
		}
		ui.addInstalled(r.Module.Name)
	}
	return nil
}

// selectRecommendedModules lets the user choose recommendations individually.
// Every recommendation starts selected, preserving the old affirmative path
// while allowing irrelevant modules to be toggled off before installation.
func selectRecommendedModules(ctx context.Context, recs []recommendation, ui *setupUI) ([]recommendation, error) {
	if autoApply {
		return recs, nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		recommendMessage(ui, ctx, "recommendations skipped", "Install recommended modules? Skipped in non-interactive mode; use `--auto-apply` to accept.")
		return nil, nil
	}

	options := make([]huh.Option[string], 0, len(recs))
	selected := make([]string, 0, len(recs))
	for _, r := range recs {
		label := fmt.Sprintf("%s — matched %s", r.Module.Repo, r.Match)
		options = append(options, huh.NewOption(label, r.Module.Repo).Selected(true))
		selected = append(selected, r.Module.Repo)
	}

	install := true
	multiSelect := huh.NewMultiSelect[string]().
		Options(options...).
		Value(&selected).
		Filterable(false)
	form := huh.NewForm(
		huh.NewGroup(
			idtui.NewFlowMultiSelect(multiSelect, recs[len(recs)-1].Module.Repo),
			idtui.NewExplicitConfirm("Install selected", "Skip", &install).
				Inline(true),
		),
	)
	if err := Frontend.HandleForm(ctx, form); err != nil {
		return nil, err
	}
	if !install {
		recommendMessage(ui, ctx, "recommendations skipped", skippedRecommendations())
		return nil, nil
	}
	selectedRecs := filterRecommendations(recs, selected)
	if len(selectedRecs) == 0 {
		recommendMessage(ui, ctx, "recommendations skipped", skippedRecommendations())
	}
	return selectedRecs, nil
}

func skippedRecommendations() string {
	return "Recommended modules skipped."
}

func filterRecommendations(recs []recommendation, selected []string) []recommendation {
	wanted := make(map[string]struct{}, len(selected))
	for _, repo := range selected {
		wanted[repo] = struct{}{}
	}
	filtered := make([]recommendation, 0, len(selected))
	for _, rec := range recs {
		if _, ok := wanted[rec.Module.Repo]; ok {
			filtered = append(filtered, rec)
		}
	}
	return filtered
}

func recommendMessage(ui *setupUI, ctx context.Context, name, markdown string) {
	if ui != nil {
		ui.setRecommendMessage(markdown)
		return
	}
	setupMessage(ctx, name, markdown)
}
