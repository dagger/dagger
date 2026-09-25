package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

var agentListMode bool
var agentResume agentResumeFlag
var agentTrace string
var agentFocus string
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
		traceID, listArchives, args, err := resolveResumeFlags(
			agentResume, cmd.Flags().Changed("resume"),
			agentTrace, cmd.Flags().Changed("trace"),
			args)
		if err != nil {
			return err
		}
		// Refuse the combinations that have no meaning before any engine work
		// happens (hack/designs/resume-from-trace.md §5.4).
		if err := validateAgentTraceFlags(traceID, args); err != nil {
			return err
		}
		if err := validateArchiveFlags(traceID, agentSourceSession, agentGeneration, agentFocus, listArchives, agentListMode, args); err != nil {
			return err
		}
		// The prompt is about to use the LLM, so renew an expired subscription
		// login up front. The on-demand refresher hook exports the renewed
		// token on the engine's first credential lookup.
		if !listArchives && !agentListMode {
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
				if listArchives {
					return listAgentArchives(ctx, source, cmd.OutOrStdout())
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
				if traceID == "" {
					llmID, err = composeAgents(ctx, dag, args, cmd)
				}
				if err != nil {
					return err
				}
				restore := traceRestore{
					source:        source,
					traceID:       traceID,
					generation:    agentGeneration,
					sourceSession: agentSourceSession,
					agent:         agentFocus,
				}
				return startInteractivePromptModeWithResume(ctx, dag, llmID, interactivePromptModeOpts{
					restore:              restore,
					generateSessionTitle: true,
				})
			},
		)
	},
}

// agentResumeFlag is the -r/--resume value: the trace to restore, or the
// reserved word "list" that a bare -r resolves to (via NoOptDefVal). A custom
// pflag.Value keeps the help readable — `--resume trace[=list]` — since pflag
// renders a custom type's NoOptDefVal unquoted after its Type() name. Trace IDs
// are hex, so the keyword cannot shadow one.
type agentResumeFlag string

const agentResumeList agentResumeFlag = "list"

func (f *agentResumeFlag) String() string { return string(*f) }

func (f *agentResumeFlag) Set(value string) error {
	*f = agentResumeFlag(value)
	return nil
}

func (f *agentResumeFlag) Type() string { return "trace" }

// resolveResumeFlags folds -r/--resume and its deprecated --trace alias into
// the trace to restore, or a request to list archives, returning the
// remaining positional arguments.
func resolveResumeFlags(resume agentResumeFlag, resumeSet bool, traceAlias string, traceAliasSet bool, args []string) (traceID string, listArchives bool, rest []string, err error) {
	switch {
	case resumeSet && traceAliasSet:
		return "", false, nil, errors.New("--trace is a deprecated alias of -r/--resume; pass only -r")
	case traceAliasSet:
		if traceAlias == "" {
			return "", false, nil, errors.New("--trace requires a trace ID")
		}
		return traceAlias, false, args, nil
	case !resumeSet:
		return "", false, args, nil
	case resume == "":
		return "", false, nil, errors.New("-r/--resume requires a trace ID, or no value to list archives")
	case resume != agentResumeList:
		return string(resume), false, args, nil
	}
	// A bare -r takes no value, so `-r <id>` parses the ID as a positional
	// argument. Agent names are never trace IDs, so accept that spelling too.
	if len(args) == 1 {
		if _, err := trace.TraceIDFromHex(args[0]); err == nil {
			return args[0], false, nil, nil
		}
	}
	return "", true, args, nil
}

func init() {
	registerCommandArtifactFlags(agentCmd)
	agentCmd.Flags().BoolVarP(&agentListMode, "list", "l", false, "List available agents")
	agentCmd.Flags().VarP(&agentResume, "resume", "r",
		"Restore agents from a past session's trace: a retained engine archive, falling back to Dagger Cloud. With no trace ID, list retained engine archives")
	// A bare -r resolves to the list keyword; -r <trace-id> and -r=<trace-id>
	// both restore (resolveResumeFlags recognizes the space-separated form).
	agentCmd.Flags().Lookup("resume").NoOptDefVal = string(agentResumeList)
	agentCmd.Flags().StringVar(&agentTrace, "trace", "", "Restore agents from a past session's trace")
	_ = agentCmd.Flags().MarkDeprecated("trace", "use -r/--resume <trace-id> instead")
	agentCmd.Flags().StringVar(&agentSourceSession, "source-session", "",
		"With -r, select the source session in an engine archive or Cloud trace")
	agentCmd.Flags().StringVar(&agentGeneration, "generation", "",
		"With -r and --source-session, select the exact archive generation")
	agentCmd.Flags().StringVar(&agentFocus, "agent", "",
		"With -r, focus this restored agent (runtime handle or name) instead of the top-level one")
}

func validateArchiveFlags(traceID, source, generation, focus string, listArchives, listAgents bool, args []string) error {
	if listArchives {
		if listAgents || len(args) != 0 || source != "" || generation != "" || focus != "" {
			return fmt.Errorf("-r without a trace ID lists archives; do not combine it with agent names, --list, --agent, --source-session, or --generation")
		}
		return nil
	}
	if traceID != "" && listAgents {
		return fmt.Errorf("-r/--resume cannot be combined with --list")
	}
	if traceID == "" && (source != "" || generation != "" || focus != "") {
		return fmt.Errorf("--source-session, --generation, and --agent require -r/--resume <trace-id>")
	}
	if generation != "" && source == "" {
		return fmt.Errorf("--generation requires --source-session; discover engine cuts with a bare -r")
	}
	return nil
}

type agentArchiveLister interface {
	ListAll(context.Context, archive.ListOptions) ([]archive.Manifest, error)
}

// Listing only reads archive metadata: no leases, bootstrap, module composition,
// provider lookup, or runtime restoration are needed.
func listAgentArchives(ctx context.Context, source agentArchiveLister, out io.Writer) error {
	manifests, err := source.ListAll(ctx, archive.ListOptions{})
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "TRACE\tSOURCE SESSION\tGENERATION\tSTATE\tSTARTED\tTITLE"); err != nil {
		return err
	}
	for _, m := range manifests {
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
