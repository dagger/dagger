package daggercmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

var agentListMode bool
var agentResume string
var agentTrace string
var agentFocus string
var agentPartial bool

var agentCmd = &cobra.Command{
	Use:   "agent [FILTERS] [OPTIONS]",
	Short: "Compose your installed agent modules and drop into an interactive prompt.",
	Args:  cobra.ArbitraryArgs,
	Annotations: map[string]string{
		// Drop into the same interactive prompt mode as `dagger shell`, so keep
		// completed conversation items in scrollback rather than GC'ing them
		// (verbosity 0 prunes completed spans after GCThreshold).
		showFinalProgressKey: "true",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		resume := cmd.Flags().Changed("resume")
		// Refuse the combinations that have no meaning before any engine work
		// happens (hack/designs/resume-from-trace.md §5.4).
		if err := validateAgentTraceFlags(agentTrace, resume, args); err != nil {
			return err
		}
		if agentPartial {
			return fmt.Errorf("--partial is not supported: a complete verified agent graph is required")
		}
		if agentTrace != "" && agentListMode {
			return fmt.Errorf("--trace cannot be combined with --list")
		}
		// The prompt is about to use the LLM, so renew an expired subscription
		// login up front. The on-demand refresher hook exports the renewed
		// token on the engine's first credential lookup.
		if err := llmconfig.RefreshOAuthTokensIfNeeded(cmd.Context()); err != nil {
			slog.Warn("failed to refresh LLM OAuth tokens", "error", err)
		}
		params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, args)
		if err != nil {
			return err
		}
		return withEngine(
			cmd.Context(), params,
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				if agentListMode {
					return listAgents(ctx, dag, args, cmd)
				}
				// Compose all selected agents onto a workspace-bound LLM, capturing
				// a stable baseline when possible, then open the prompt. A module
				// function returning LLM already lands in prompt mode today.
				//
				// Trace restore deliberately starts from an unbound base instead:
				// the restored recipes carry their own frozen workspaces and must not
				// read or load modules from the destination checkout.
				var llmID string
				var err error
				if agentTrace != "" {
					llmID, err = freshAgentBase(ctx, dag)
				} else {
					llmID, err = composeAgents(ctx, dag, args, cmd)
				}
				if err != nil {
					return err
				}
				restore := traceRestore{
					traceID: agentTrace,
					agent:   agentFocus,
					partial: agentPartial,
				}
				return startInteractivePromptModeWithResume(ctx, dag, llmID, interactivePromptModeOpts{
					restore:              restore,
					generateSessionTitle: true,
				})
			},
		)
	},
}

func init() {
	registerCommandArtifactFlags(agentCmd)
	agentCmd.Flags().BoolVarP(&agentListMode, "list", "l", false, "List available agents")
	agentCmd.Flags().StringVarP(&agentResume, "resume", "r", "", "Unsupported: use --trace with a verified trace ID")
	agentCmd.Flags().Lookup("resume").NoOptDefVal = "removed"
	_ = agentCmd.Flags().MarkHidden("resume")
	agentCmd.Flags().StringVar(&agentTrace, "trace", "",
		"Restore agents and their Workspaces from a verified trace archive; load scrollback in the background")
	agentCmd.Flags().StringVar(&agentFocus, "agent", "",
		"With --trace, focus this restored agent (runtime handle or name) instead of the top-level one")
	agentCmd.Flags().BoolVar(&agentPartial, "partial", false, "Unsupported: restore requires a complete verified agent graph")
	_ = agentCmd.Flags().MarkHidden("partial")
}

const freshAgentBaseQuery = `query AgentBase {
  llm {
    id
  }
}`

func freshAgentBase(ctx context.Context, dag *dagger.Client) (string, error) {
	var res struct {
		LLM struct {
			ID string
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:  freshAgentBaseQuery,
		OpName: "AgentBase",
	}, &dagger.Response{Data: &res}); err != nil {
		return "", err
	}
	return res.LLM.ID, nil
}

func composeAgents(ctx context.Context, dag *dagger.Client, include []string, cmd *cobra.Command) (string, error) {
	workspace, err := snapshotWorkspace(ctx, dag)
	if err != nil {
		return "", err
	}
	all, err := commandArtifactsWithFlags(ctx, dag, workspace, cmd, include, true)
	if err != nil {
		return "", err
	}
	selection := all.FilterTypes([]string{"Expertise"})
	expertise, err := selection.AsExpertise(ctx)
	if err != nil {
		return "", err
	}
	refs := make([]*dagger.Expertise, len(expertise))
	for i := range expertise {
		refs[i] = &expertise[i]
	}
	id, err := dag.LLM().WithWorkspace(workspace).Compose(refs).ID(ctx)
	return string(id), err
}

// Capture once before binding or composing tools. A failed capture must not
// silently introduce a live checkout dependency into the committed recipe.
func snapshotWorkspace(ctx context.Context, dag *dagger.Client) (*dagger.Workspace, error) {
	workspace := dag.CurrentWorkspace()
	id, err := workspace.Snapshot().ID(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("capture workspace for agent: %w", err)
	}
	return dagger.Ref[*dagger.Workspace](dag, id), nil
}

func listAgents(ctx context.Context, dag *dagger.Client, include []string, cmd *cobra.Command) error {
	all, err := commandArtifactsWithFlags(ctx, dag, dag.CurrentWorkspace(), cmd, include, true)
	if err != nil {
		return err
	}
	return listArtifactSelection(ctx, dag, all.FilterTypes([]string{"Expertise"}), cmd)
}
