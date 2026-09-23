package daggercmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	telemetry "github.com/dagger/otel-go"
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
	Use:   "agent [options] [name...]",
	Short: "Compose your installed agent modules and drop into an interactive prompt.",
	Long: `Compose your installed agent modules — their tools and system prompts — onto a base LLM, and drop into the interactive prompt with them all live.

Each installed module that exposes an @agent function contributes its toolset and
system prompt. With no arguments, every installed agent is composed, in
alphabetical order. Name one or more agents to compose only those.

With --trace, a verified archive restores every agent, its committed conversation,
Workspace, tools, lifecycle state, and notification subscriptions. No destination
agent modules are composed. The prompt becomes usable before unrelated historical
telemetry finishes loading; original telemetry supplies scrollback.

Restore forks new inert runtimes; it does not hand off a live session or start a
model turn. Pending messages not committed to a conversation are not recovered.
Legacy local JSON session files and -r/--resume are no longer supported.
This initial restore path requires a retained engine archive. Cloud-only traces
remain viewable with dagger trace, but cannot yet supply verified restore finality.

Use --list-archives to discover retained archives without restoring. If a trace
belongs to multiple source sessions, select both --source-session and --generation
from that list; restoration never silently chooses a different archive.

Examples:
  dagger agent                    # Compose all installed agents and start the prompt
  dagger agent -l                 # List all available agents
  dagger agent editor dagger-go   # Compose only the 'editor' and 'dagger-go' agents
  dagger agent --trace <id>       # Restore a verified trace archive
`,
	Args: cobra.ArbitraryArgs,
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
		return withEngine(
			cmd.Context(),
			client.Params{
				// A trace carries the workspace and module recipes needed to restore
				// its agents. Loading modules from the destination checkout would
				// both be unnecessary and make cold restore depend on that checkout.
				LoadWorkspaceModules: !agentListArchives && agentTrace == "",
			},
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
					llmID, err = composeAgents(ctx, dag, args)
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
