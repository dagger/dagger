package daggercmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
)

var agentListMode bool

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
`,
	Args: cobra.ArbitraryArgs,
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
				// Compose all selected agents onto a fresh workspace-bound LLM,
				// then hand the composed LLM to the interactive prompt. A module
				// function returning LLM already lands in prompt mode today.
				llmID, err := composeAgents(ctx, dag, args)
				if err != nil {
					return err
				}
				return startInteractivePromptMode(ctx, dag, llmID)
			},
		)
	},
}

func init() {
	agentCmd.Flags().BoolVarP(&agentListMode, "list", "l", false, "List available agents")
}

// agentIncludeVars maps the positional agent names to the `include` variable of
// the workspace agents query (null when none are named — compose everything).
func agentIncludeVars(include []string) map[string]any {
	if len(include) == 0 {
		return map[string]any{"include": nil}
	}
	return map[string]any{"include": include}
}

const composeAgentsQuery = `query ComposeAgents($include: [String!]) {
  workspace: currentWorkspace {
    agents(include: $include) {
      compose {
        id
      }
    }
  }
}`

func composeAgents(ctx context.Context, dag *dagger.Client, include []string) (string, error) {
	var res struct {
		Workspace struct {
			Agents struct {
				Compose struct {
					ID string
				}
			}
		}
	}
	err := dag.Do(ctx, &dagger.Request{
		Query:     composeAgentsQuery,
		OpName:    "ComposeAgents",
		Variables: agentIncludeVars(include),
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return "", err
	}
	return res.Workspace.Agents.Compose.ID, nil
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
