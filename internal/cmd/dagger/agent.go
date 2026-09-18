package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

var agentListMode bool
var agentResume agentSessionFlag
var agentTrace string
var agentFocus string
var agentPartial bool

var agentCmd = &cobra.Command{
	Use:   "agent [options] [address...]",
	Short: "Compose your installed agent modules and drop into an interactive prompt.",
	Long: `Compose your installed agent modules — their tools and system prompts — onto a base LLM, and drop into the interactive prompt with them all live.

Each installed module that exposes an @agent function contributes its toolset and
system prompt. With no arguments, every installed agent is composed, in
alphabetical order. Name one or more agents to compose only those.

With --trace, a past session is restored from the trace it published to Dagger
Cloud: every agent it ran comes back under the same identity, with the
conversation and lifecycle state it had, and the old session's whole progress
view is scrolled back beside your prompt. Two caveats. Restoring a trace whose
agents are still running FORKS them — the restored instances are new runtimes
in this session, not a hand-off of the live ones. And messages that were
enqueued but never consumed are not in the trace at all, so they are not
restored; anything a turn actually consumed is part of its conversation and is.

Examples:
  dagger agent                    # Compose all installed agents and start the prompt
  dagger agent -l                 # List all available agents
  dagger agent dag://editor dag://dagger-go   # Compose only the 'editor' and 'dagger-go' agents
  dagger agent -r                 # Resume a saved session (interactive picker)
  dagger agent -r=<session>       # Resume a specific saved session
  dagger agent --trace <id>       # Restore a past session from its Dagger Cloud trace
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
		params, err := artifactClientParams(client.Params{LoadWorkspaceModules: agentTrace == "" || agentListMode}, args)
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
					llmID, err = composeAgents(ctx, dag, args)
				}
				if err != nil {
					return err
				}
				// -r/--resume optionally restores a saved session before the
				// prompt starts: a session id resumes it directly, the picker
				// keyword (what a bare -r resolves to) opens the interactive
				// picker. --trace restores a past session from its published
				// trace instead.
				sessionID := agentResume.SessionID()
				restore := traceRestore{
					traceID: agentTrace,
					agent:   agentFocus,
					partial: agentPartial,
				}
				return startInteractivePromptModeWithResume(ctx, dag, llmID, interactivePromptModeOpts{
					sessionID:            sessionID,
					resume:               resume,
					restore:              restore,
					generateSessionTitle: true,
				})
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
	agentCmd.Flags().StringVar(&agentTrace, "trace", "",
		"Restore a past session from its Dagger Cloud trace: its agents, their conversations, and its scrollback")
	agentCmd.Flags().StringVar(&agentFocus, "agent", "",
		"With --trace, focus this restored agent (runtime handle or name) instead of the top-level one")
	agentCmd.Flags().BoolVar(&agentPartial, "partial", false,
		"With --trace, restore what the trace carries enough to restore instead of failing on the first agent it does not")
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

func composeAgents(ctx context.Context, dag *dagger.Client, include []string) (string, error) {
	workspace, err := snapshotWorkspace(ctx, dag)
	if err != nil {
		return "", err
	}
	all, err := commandArtifacts(ctx, dag, workspace, include, true)
	if err != nil {
		return "", err
	}
	selection := all.FilterDirectives([]string{"agent"})
	id, err := selection.ID(ctx)
	if err != nil {
		return "", err
	}
	var response struct {
		Selection struct {
			Items []struct {
				ID        dagger.ID
				URI       string
				Arguments []struct {
					Name    string
					TypeDef struct{ AsObject *struct{ Name string } }
				}
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query AgentArtifacts($id: ID!) {
	 selection: node(id: $id) { ... on Artifacts { items { id uri arguments { name typeDef { asObject { name } } } } } }
	}`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	if err != nil {
		return "", err
	}
	base, err := dag.LLM().WithWorkspace(workspace).ID(ctx)
	if err != nil {
		return "", err
	}
	for _, artifact := range response.Selection.Items {
		inputs := map[string]any{}
		for _, arg := range artifact.Arguments {
			if arg.TypeDef.AsObject != nil && arg.TypeDef.AsObject.Name == "LLM" {
				inputs[arg.Name] = base
			}
		}
		encoded, err := json.Marshal(inputs)
		if err != nil {
			return "", err
		}
		var evaluated struct {
			Artifact struct{ Value struct{ ID dagger.ID } }
		}
		err = dag.Do(ctx, &dagger.Request{Query: `query ComposeAgentArtifact($id: ID!, $arguments: JSON!) {
		 artifact: node(id: $id) { ... on Artifact { value(arguments: $arguments) { ... on LLM { id } } } }
		}`, Variables: map[string]any{"id": artifact.ID, "arguments": string(encoded)}}, &dagger.Response{Data: &evaluated})
		if err != nil {
			return "", fmt.Errorf("compose %s: %w", artifact.URI, err)
		}
		base = evaluated.Artifact.Value.ID
	}
	return string(base), nil
}

// Attempt the effectful capture once before binding or composing tools. Capture
// is best effort: startup and reload can use the live workspace if it fails.
func snapshotWorkspace(ctx context.Context, dag *dagger.Client) (*dagger.Workspace, error) {
	workspace := dag.CurrentWorkspace()
	id, err := workspace.Snapshot().ID(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.WarnContext(ctx, "could not snapshot workspace; continuing with the live workspace", "error", err)
		return workspace, nil
	}
	return dagger.Ref[*dagger.Workspace](dag, id), nil
}

func listAgents(ctx context.Context, dag *dagger.Client, include []string, cmd *cobra.Command) error {
	all, err := commandArtifacts(ctx, dag, dag.CurrentWorkspace(), include, true)
	if err != nil {
		return err
	}
	return listArtifactSelection(ctx, dag, all.FilterDirectives([]string{"agent"}), cmd.OutOrStdout())
}
