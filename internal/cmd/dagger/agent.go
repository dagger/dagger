package daggercmd

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
)

var agentListMode bool
var agentResume agentSessionFlag
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

Examples:
  dagger agent                    # Compose all installed agents and start the prompt
  dagger agent -l                 # List all available agents
  dagger agent editor dagger-go   # Compose only the 'editor' and 'dagger-go' agents
  dagger agent -r                 # Resume a saved session (interactive picker)
  dagger agent -r=<session>       # Resume a specific saved session
`,
	Args: cobra.ArbitraryArgs,
	Annotations: map[string]string{
		// Drop into the same interactive prompt mode as `dagger shell`, so keep
		// completed conversation items in scrollback rather than GC'ing them
		// (verbosity 0 prunes completed spans after GCThreshold).
		showFinalProgressKey: "true",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(
			cmd.Context(),
			client.Params{
				LoadWorkspaceModules: true,
			},
			func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				if agentListMode {
					return listAgents(ctx, dag, args, cmd)
				}
				// Compose all selected agents onto a frozen workspace-bound LLM,
				// then hand the composed LLM to the interactive prompt. A module
				// function returning LLM already lands in prompt mode today.
				llmID, err := composeAgents(ctx, dag, args)
				if err != nil {
					return err
				}
				// -r/--resume optionally restores a saved session before the
				// prompt starts: a session id resumes it directly, the picker
				// keyword (what a bare -r resolves to) opens the interactive
				// picker.
				resume := cmd.Flags().Changed("resume")
				sessionID := agentResume.SessionID()
				return startInteractivePromptModeWithResume(ctx, dag, llmID, sessionID, resume)
			},
		)
	},
}

// agentSessionFlag is the -r/--resume flag value: a saved session id, or the
// reserved word "picker" to open the interactive session picker. Implementing
// pflag.Value (rather than using a plain string flag) keeps the help text
// readable — `--resume session[=picker]` — since pflag renders a custom type's
// NoOptDefVal unquoted after the Type() name. Saved session ids are UUIDs, so
// the keyword can't shadow a real session.
type agentSessionFlag string

// agentSessionPicker is the reserved --resume value naming the interactive
// session picker; it's also what a bare -r resolves to (via NoOptDefVal).
const agentSessionPicker agentSessionFlag = "picker"

func (f *agentSessionFlag) String() string { return string(*f) }

func (f *agentSessionFlag) Set(value string) error {
	*f = agentSessionFlag(value)
	return nil
}

func (f *agentSessionFlag) Type() string { return "session" }

// SessionID resolves the flag to the session to resume: empty for the
// interactive picker, otherwise the session id itself.
func (f agentSessionFlag) SessionID() string {
	if f == agentSessionPicker {
		return ""
	}
	return string(f)
}

func init() {
	agentCmd.Flags().BoolVarP(&agentListMode, "list", "l", false, "List available agents")
	agentCmd.Flags().VarP(&agentResume, "resume", "r", "Resume a saved session (interactive picker if no id given)")
	// A bare -r (no value) resolves to the picker keyword, opening the
	// interactive picker; -r=<id> resumes that session directly. (NoOptDefVal
	// flags require '=' to attach a value — a space-separated one would be
	// parsed as a positional agent name.)
	agentCmd.Flags().Lookup("resume").NoOptDefVal = string(agentSessionPicker)
	agentCmd.Flags().StringArrayVar(&agentCheckpointInclude, "checkpoint-include", nil,
		"Explicitly include and approve an exact suspicious workspace path (repeatable)")
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
