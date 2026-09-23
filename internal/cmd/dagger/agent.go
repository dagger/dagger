package daggercmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

var agentListMode bool
var agentResume string
var agentTrace string
var agentFocus string
var agentPartial bool
var agentListArchives bool
var agentSourceSession string
var agentGeneration string

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
		if err := validateArchiveFlags(agentTrace, agentSourceSession, agentGeneration, agentFocus, agentListArchives, agentListMode, args); err != nil {
			return err
		}
		// The prompt is about to use the LLM, so renew an expired subscription
		// login up front. The on-demand refresher hook exports the renewed
		// token on the engine's first credential lookup.
		if !agentListArchives && !agentListMode {
			if err := llmconfig.RefreshOAuthTokensIfNeeded(cmd.Context()); err != nil {
				slog.Warn("failed to refresh LLM OAuth tokens", "error", err)
			}
		}
		params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, args)
		if err != nil {
			return err
		}
		return withEngine(
			cmd.Context(), params,
			func(ctx context.Context, engineClient *client.Client) error {
				source := archive.NewClient(client.EngineConn(engineClient)).WithStallTimeout(30 * time.Second)
				if agentListArchives {
					return listAgentArchives(ctx, source, agentTrace, cmd.OutOrStdout())
				}
				source = source.WithSourceSession(agentSourceSession)
				dag := engineClient.Dagger()
				if agentListMode {
					return listAgents(ctx, dag, args, cmd)
				}
				// Compose all selected agents onto a workspace-bound LLM, capturing
				// a stable baseline when possible, then open the prompt. A module
				// function returning LLM already lands in prompt mode today.
				//
				// Trace restore initializes only inert client plumbing. Its archived
				// anchors, not a destination base LLM, own the provider and workspace.
				var llmID string
				var err error
				if agentTrace == "" {
					llmID, err = composeAgents(ctx, dag, args, cmd)
				}
				if err != nil {
					return err
				}
				restore := traceRestore{
					source:     source,
					traceID:    agentTrace,
					generation: agentGeneration,
					agent:      agentFocus,
					partial:    agentPartial,
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
	agentCmd.Flags().BoolVar(&agentListArchives, "list-archives", false,
		"List retained engine archives without restoring; --trace filters the list")
	agentCmd.Flags().StringVar(&agentSourceSession, "source-session", "",
		"With --trace and --generation, select the archive's source session")
	agentCmd.Flags().StringVar(&agentGeneration, "generation", "",
		"With --trace and --source-session, select the exact archive generation")
	agentCmd.Flags().StringVar(&agentFocus, "agent", "",
		"With --trace, focus this restored agent (runtime handle or name) instead of the top-level one")
	agentCmd.Flags().BoolVar(&agentPartial, "partial", false, "Unsupported: restore requires a complete verified agent graph")
	_ = agentCmd.Flags().MarkHidden("partial")
}

func validateArchiveFlags(traceID, source, generation, focus string, listArchives, listAgents bool, args []string) error {
	if listArchives {
		if listAgents || len(args) != 0 || source != "" || generation != "" || focus != "" {
			return fmt.Errorf("--list-archives accepts only --trace as an archive filter; do not combine it with agent names, --list, --agent, --source-session, or --generation")
		}
		return nil
	}
	if traceID != "" && listAgents {
		return fmt.Errorf("--trace cannot be combined with --list")
	}
	if traceID == "" && (source != "" || generation != "" || focus != "") {
		return fmt.Errorf("--source-session, --generation, and --agent require --trace")
	}
	if (source == "") != (generation == "") {
		return fmt.Errorf("--source-session and --generation must be supplied together; discover choices with --list-archives")
	}
	return nil
}

type agentArchiveLister interface {
	ListAll(context.Context, archive.ListOptions) ([]archive.Manifest, error)
}

// Listing only reads archive metadata: no leases, bootstrap, module composition,
// provider lookup, or runtime restoration are needed.
func listAgentArchives(ctx context.Context, source agentArchiveLister, traceID string, out io.Writer) error {
	manifests, err := source.ListAll(ctx, archive.ListOptions{})
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "TRACE\tSOURCE SESSION\tGENERATION\tSTATE\tSTARTED\tTITLE"); err != nil {
		return err
	}
	for _, m := range manifests {
		if traceID != "" && m.TraceID != traceID {
			continue
		}
		// Quoting prevents user-provided titles from injecting terminal controls
		// or breaking a metadata row into multiple lines.
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%q\n", m.TraceID, m.SourceSession, m.Generation, m.State, m.StartedAt.UTC().Format(time.RFC3339), m.Title); err != nil {
			return err
		}
	}
	return w.Flush()
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
