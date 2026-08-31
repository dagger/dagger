package daggercmd

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

var agentListMode bool
var agentResume agentResumeFlag
var agentResumeTimeout time.Duration
var agentFocus string
var agentCheckpointInclude []string
var agentCheckpointExclude []string
var agentCheckpointMaxUntrackedFileBytes int
var agentCheckpointMaxUntrackedTotalBytes int
var agentCheckpointMaxUntrackedFiles int

var agentCmd = &cobra.Command{
	Use:   "agent [options] [name...]",
	Short: "Compose your installed agent modules and drop into an interactive prompt.",
	Long: `Compose your installed agent modules — their tools and system prompts — onto a base LLM, and drop into the interactive prompt with them all live.

Each installed module that exposes an @agent function contributes its toolset and
system prompt. With no arguments, every installed agent is composed, in
alphabetical order. Name one or more agents to compose only those.

Resuming restores every agent from a retained trace under the same identity,
with its committed conversation and lifecycle state. A bare -r opens the
connected engine's archive picker; an attached trace ID selects one directly.
Restoring a trace whose agents are still running forks them: the restored
instances are new runtimes in this session, not a hand-off of the live ones.
Messages that were enqueued but never consumed are not restored.

Examples:
  dagger agent                    # Compose all installed agents and start the prompt
  dagger agent -l                 # List all available agents
  dagger agent editor dagger-go   # Compose only the 'editor' and 'dagger-go' agents
  dagger agent -r                 # Pick a retained agent trace to resume
  dagger agent -r=<trace-id>      # Resume a specific trace
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
		if err := validateAgentResumeFlags(
			resume,
			agentResumeTimeout,
			cmd.Flags().Changed("resume-timeout"),
			cmd.Flags().Changed("agent"),
			args,
		); err != nil {
			return err
		}
		return withEngineAfterClose(
			cmd.Context(),
			client.Params{
				// Agent prompt sessions retain their engine telemetry for resume.
				// finalizeEngineParams supplies the canonical live command trace ID
				// after frontend telemetry initialization.
				ArchiveTelemetry: !agentListMode,
				// A restored trace carries its own workspace and composition.
				// Loading destination modules would make a cold resume depend on
				// the checkout it happens to be launched from.
				LoadWorkspaceModules: !resume || agentListMode,
			},
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				if agentListMode {
					return listAgents(ctx, dag, args, cmd)
				}
				// Compose all selected agents onto a frozen workspace-bound LLM,
				// then hand the composed LLM to the interactive prompt. A module
				// function returning LLM already lands in prompt mode today.
				//
				// Trace restore deliberately starts from an unbound base instead:
				// the restored recipes carry their own frozen workspaces and must not
				// read or load modules from the destination checkout.
				var llmID string
				var err error
				if resume {
					llmID, err = freshAgentBase(ctx, dag)
				} else {
					llmID, err = composeAgents(ctx, dag, args)
				}
				if err != nil {
					return err
				}
				archiveClient := engineClient.ArchiveClient()
				liveTraceID := engineClient.ArchiveTraceID
				opts := interactivePromptModeOpts{
					generateSessionTitle: true,
					restoreSource:        newEngineTraceRestoreSource(archiveClient),
					archiveMetadata: func(updateCtx context.Context, title string) error {
						return archiveClient.UpdateMetadata(updateCtx, liveTraceID, archive.MetadataUpdate{Title: title})
					},
				}
				if resume {
					opts.restore = &traceRestore{
						traceID: agentResume.TraceID(),
						timeout: agentResumeTimeout,
						agent:   agentFocus,
					}
				}
				return startInteractivePromptModeWithResume(ctx, dag, llmID, opts)
			},
			func(engineClient *client.Client) {
				setAgentResumeHint(Frontend, engineClient.ArchiveTraceID)
			},
		)
	},
}

const agentResumeHintTitle = "RESUME SESSION"

func setAgentResumeHint(frontend any, traceID string) {
	parsed, err := trace.TraceIDFromHex(traceID)
	if err != nil || !parsed.IsValid() || parsed.String() != traceID {
		return
	}
	suggester, ok := frontend.(idtui.SuggestedCommandFrontend)
	if !ok {
		return
	}
	suggester.SetSuggestedCommand(agentResumeHintTitle, "dagger agent -r="+traceID)
}

// agentResumeFlag is the optional value of -r/--resume: a trace ID, or the
// reserved picker sentinel installed by pflag for a bare flag.
type agentResumeFlag string

const agentResumePicker agentResumeFlag = "picker"

func (f *agentResumeFlag) String() string { return string(*f) }

func (f *agentResumeFlag) Set(value string) error {
	*f = agentResumeFlag(value)
	return nil
}

func (f *agentResumeFlag) Type() string { return "trace" }

func (f agentResumeFlag) TraceID() string {
	if f == agentResumePicker {
		return ""
	}
	return string(f)
}

func init() {
	agentCmd.Flags().BoolVarP(&agentListMode, "list", "l", false, "List available agents")
	agentCmd.Flags().VarP(&agentResume, "resume", "r", "Resume agents from a retained trace (engine archive picker if no trace ID is given)")
	// Optional-value flags require '=' for an attached value. A space-separated
	// value remains positional and is rejected as an agent composition.
	agentCmd.Flags().Lookup("resume").NoOptDefVal = string(agentResumePicker)
	agentCmd.Flags().DurationVar(&agentResumeTimeout, "resume-timeout", 0,
		"Maximum idle interval without bytes before a resume stream fails (background history retries nonfatally)")
	agentCmd.Flags().StringVar(&agentFocus, "agent", "",
		"Focus this restored agent (instance ID or name) instead of the top-level one")
	agentCmd.Flags().StringArrayVar(&agentCheckpointInclude, "checkpoint-include", nil,
		"Approve a workspace path pattern for the portable checkpoint (repeatable)")
	agentCmd.Flags().StringArrayVar(&agentCheckpointExclude, "checkpoint-exclude", nil,
		"Exclude a workspace path pattern from the portable checkpoint (repeatable)")
	agentCmd.Flags().IntVar(&agentCheckpointMaxUntrackedFileBytes, "checkpoint-max-untracked-file-bytes", 0,
		"Maximum bytes for one untracked checkpoint file (default 16 MiB)")
	agentCmd.Flags().IntVar(&agentCheckpointMaxUntrackedTotalBytes, "checkpoint-max-untracked-total-bytes", 0,
		"Maximum aggregate untracked checkpoint bytes (default 64 MiB)")
	agentCmd.Flags().IntVar(&agentCheckpointMaxUntrackedFiles, "checkpoint-max-untracked-files", 0,
		"Maximum untracked checkpoint files (default 4096)")
}

// agentIncludeVars maps the positional agent names to the `include` variable of
// the workspace agents query (null when none are named — compose everything).
func agentIncludeVars(include []string) map[string]any {
	if len(include) == 0 {
		return map[string]any{"include": nil}
	}
	return map[string]any{"include": include}
}

// checkpointVars is the capture policy every freeze in this session runs under:
// the initial one that composes the agents, and each re-freeze that follows a
// save or a reset.
func checkpointVars() map[string]any {
	return map[string]any{
		"checkpointInclude":      agentCheckpointInclude,
		"checkpointExclude":      agentCheckpointExclude,
		"maxUntrackedFileBytes":  optionalPositiveInt(agentCheckpointMaxUntrackedFileBytes),
		"maxUntrackedTotalBytes": optionalPositiveInt(agentCheckpointMaxUntrackedTotalBytes),
		"maxUntrackedFiles":      optionalPositiveInt(agentCheckpointMaxUntrackedFiles),
	}
}

func composeAgentsVars(include []string) map[string]any {
	vars := checkpointVars()
	maps.Copy(vars, agentIncludeVars(include))
	return vars
}

func optionalPositiveInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
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

const checkpointWorkspaceQuery = `query CheckpointWorkspace(
  $checkpointInclude: [String!]
  $checkpointExclude: [String!]
  $maxUntrackedFileBytes: Int
  $maxUntrackedTotalBytes: Int
  $maxUntrackedFiles: Int
) {
  workspace: currentWorkspace {
    checkpoint(
      include: $checkpointInclude
      exclude: $checkpointExclude
      maxUntrackedFileBytes: $maxUntrackedFileBytes
      maxUntrackedTotalBytes: $maxUntrackedTotalBytes
      maxUntrackedFiles: $maxUntrackedFiles
    ) {
      id
    }
  }
}`

// checkpointWorkspace freezes the live workspace into a fresh portable
// checkpoint, under the same capture policy the session was composed with.
//
// A conversation only stays portable while its workspace is a frozen tree, so
// every rebinding of the agent's workspace mid-session goes through here rather
// than binding the live checkout back in: a bound tool keeps reading a
// host-independent tree, and the trace keeps a replayable leaf.
func checkpointWorkspace(ctx context.Context, dag *dagger.Client) (*dagger.Workspace, error) {
	var res struct {
		Workspace struct {
			Checkpoint struct {
				ID string
			}
		}
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     checkpointWorkspaceQuery,
		OpName:    "CheckpointWorkspace",
		Variables: checkpointVars(),
	}, &dagger.Response{Data: &res}); err != nil {
		return nil, err
	}
	return dagger.Ref[*dagger.Workspace](dag, dagger.ID(res.Workspace.Checkpoint.ID)), nil
}

const composeAgentsQuery = `query ComposeAgents(
  $include: [String!]
  $checkpointInclude: [String!]
  $checkpointExclude: [String!]
  $maxUntrackedFileBytes: Int
  $maxUntrackedTotalBytes: Int
  $maxUntrackedFiles: Int
) {
  workspace: currentWorkspace {
    checkpoint(
      include: $checkpointInclude
      exclude: $checkpointExclude
      maxUntrackedFileBytes: $maxUntrackedFileBytes
      maxUntrackedTotalBytes: $maxUntrackedTotalBytes
      maxUntrackedFiles: $maxUntrackedFiles
    ) {
      agents(include: $include) {
        compose {
          id
        }
      }
    }
  }
}`

func composeAgents(ctx context.Context, dag *dagger.Client, include []string) (string, error) {
	var res struct {
		Workspace struct {
			Checkpoint struct {
				Agents struct {
					Compose struct {
						ID string
					}
				}
			}
		}
	}
	err := dag.Do(ctx, &dagger.Request{
		Query:     composeAgentsQuery,
		OpName:    "ComposeAgents",
		Variables: composeAgentsVars(include),
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return "", err
	}
	return res.Workspace.Checkpoint.Agents.Compose.ID, nil
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
