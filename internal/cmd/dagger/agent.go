package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	telemetry "github.com/dagger/otel-go"
)

var agentListMode bool
var agentResume agentResumeFlag
var agentTrace string
var agentFocus string
var agentSourceSession string
var agentGeneration string

var agentCmd = &cobra.Command{
	Use:   "agent [options] [name...]",
	Short: "Compose your installed agent modules and drop into an interactive prompt.",
	Long: `Compose your installed agent modules — their tools and system prompts — onto a base LLM, and drop into the interactive prompt with them all live.

Each installed module that exposes an @agent function contributes its toolset and
system prompt. With no arguments, every installed agent is composed, in
alphabetical order. Name one or more agents to compose only those.

With -r <trace-id>, restore agents, committed conversations, Workspaces, tools,
lifecycle state, and notification subscriptions from a past session's trace. No
destination agent modules are composed. A retained engine archive provides a
verified bootstrap before background history loading. If no local archive is
retained, the CLI transparently fetches the trace from Dagger Cloud and validates
its observed canonical records and recipe closure. Cloud restore waits for the
whole trace and cannot prove that later records or entirely absent agents were not
lost; it does not invent an archive finality seal.

Restore forks new inert runtimes; it does not hand off a live session or start a
model turn. Pending messages not committed to a conversation are not recovered.

Restore is best-effort: an agent the trace does not carry enough to restore is
skipped, along with its subscriptions, and a warning names it and why.

Run -r with no trace ID to list retained engine archives without restoring. Use
--source-session when a trace belongs to multiple source sessions. Add --generation
to pin an exact engine archive cut; explicit generations never fall back to Cloud.

Examples:
  dagger agent                    # Compose all installed agents and start the prompt
  dagger agent -l                 # List all available agents
  dagger agent editor dagger-go   # Compose only the 'editor' and 'dagger-go' agents
  dagger agent -r                 # List retained engine archives
  dagger agent -r <trace-id>      # Restore agents from a past session's trace
`,
	Args: cobra.ArbitraryArgs,
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
		// Set once the prompt exits with agents behind it; read after the TUI
		// has torn down. Atomic because a second Ctrl+D exits the frontend
		// without waiting for the run to return.
		var resumeTrace atomic.Pointer[string]
		err = withEngine(
			cmd.Context(),
			client.Params{
				// A trace carries the workspace and module recipes needed to restore
				// its agents. Loading modules from the destination checkout would
				// both be unnecessary and make cold restore depend on that checkout.
				LoadWorkspaceModules: !listArchives && traceID == "",
			},
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
					llmID, err = composeAgents(ctx, dag, args)
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
					exitedResumable: func(traceID string) {
						resumeTrace.Store(&traceID)
					},
				})
			},
		)
		if resumed := resumeTrace.Load(); resumed != nil {
			printResumeHint(cmd.ErrOrStderr(), *resumed)
		}
		return err
	},
}

// printResumeHint tells the user how to come back to the session they just
// left, spelling out --resume since -r alone is easy to forget.
func printResumeHint(w io.Writer, traceID string) {
	out := termenv.NewOutput(w)
	fmt.Fprintln(w, out.String("To resume this session, run:").Bold())
	fmt.Fprintf(w, "  dagger agent --resume %s\n", traceID)
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

// agentIncludeVars maps the positional agent names to the `include` variable of
// the workspace agents query (null when none are named — compose everything).
func agentIncludeVars(include []string) map[string]any {
	if len(include) == 0 {
		return map[string]any{"include": nil}
	}
	return map[string]any{"include": include}
}

const composeAgentsQuery = `query ComposeAgents($include: [String!], $workspace: ID!) {
  workspace: node(id: $workspace) { ... on Workspace {
    agents(include: $include) {
      compose {
        id
      }
    }
  } }
}`

func composeAgents(ctx context.Context, dag *dagger.Client, include []string) (string, error) {
	workspace, err := snapshotWorkspace(ctx, dag)
	if err != nil {
		return "", err
	}
	id, err := workspace.ID(ctx)
	if err != nil {
		return "", err
	}
	vars := agentIncludeVars(include)
	vars["workspace"] = id
	var res struct {
		Workspace struct {
			Agents struct {
				Compose struct {
					ID string
				}
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     composeAgentsQuery,
		OpName:    "ComposeAgents",
		Variables: vars,
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return "", err
	}
	return res.Workspace.Agents.Compose.ID, nil
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

const listAgentsQuery = `query ListAgents($include: [String!]) {
  workspace: currentWorkspace {
    agents(include: $include) {
      list {
        name
        description
      }
    }
  }
}`

// listAgents renders 'dagger agent -l': the name and description of each
// composable agent. The module-loading work is encapsulated under a single span
// so list mode stays quiet, matching 'dagger up -l' / 'dagger checks -l'.
func listAgents(ctx context.Context, dag *dagger.Client, include []string, cmd *cobra.Command) error {
	ctx, span := Tracer().Start(ctx, "fetch agent information", telemetry.Encapsulate())
	defer span.End()

	var res struct {
		Workspace struct {
			Agents struct {
				List []struct {
					Name        string
					Description string
				}
			}
		}
	}
	err := dag.Do(ctx, &dagger.Request{
		Query:     listAgentsQuery,
		OpName:    "ListAgents",
		Variables: agentIncludeVars(include),
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	for _, agent := range res.Workspace.Agents.List {
		firstLine := agent.Description
		if idx := strings.Index(firstLine, "\n"); idx != -1 {
			firstLine = firstLine[:idx]
		}
		fmt.Fprintf(tw, "%s\t%s\n", cliName(agent.Name), firstLine)
	}
	return tw.Flush()
}
