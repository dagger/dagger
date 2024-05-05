package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
)

var (
	queryFile          string
	queryVarsInput     []string
	queryVarsJSONInput string
)

var queryCmd = &cobra.Command{
	Use:     "query [options] [operation]",
	Aliases: []string{"q"},
	Short:   "Send API queries to a dagger engine",
	Long: `Send API queries to a dagger engine.

When no document file is provided, reads query from standard input.

Can optionally provide the GraphQL operation name if there are multiple
queries in the document.
`,
	Example: `dagger query <<EOF
{
  container {
    from(address:"hello-world") {
      withExec(args:["/hello"]) {
        stdout
      }
    }
  }
}
EOF
`,
	GroupID: execGroup.ID,
	Args:    cobra.MaximumNArgs(1), // operation can be specified
	RunE: func(cmd *cobra.Command, args []string) error {
		return optionalModCmdWrapper(Query, "")(cmd, args)
	},
}

func Query(ctx context.Context, engineClient *client.Client, _ *dagger.Module, cmd *cobra.Command, args []string) (rerr error) {
	res, err := runQuery(ctx, engineClient, args)
	if err != nil {
		return err
	}
	result, err := json.MarshalIndent(res, "", "    ")
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", result)
	return nil
}

func runQuery(
	ctx context.Context,
	engineClient *client.Client,
	args []string,
) (map[string]any, error) {
	var operation string
	if len(args) > 0 {
		operation = args[0]
	}

	vars := make(map[string]interface{})
	if len(queryVarsJSONInput) > 0 {
		if err := json.Unmarshal([]byte(queryVarsJSONInput), &vars); err != nil {
			return nil, err
		}
	} else {
		vars = getKVInput(queryVarsInput)
	}

	// Use the provided query file if specified
	// Otherwise, if stdin is a pipe or other non-tty thing, read from it.
	var operations string
	if queryFile != "" {
		inBytes, err := os.ReadFile(queryFile)
		if err != nil {
			return nil, err
		}
		operations = string(inBytes)
	} else if !term.IsTerminal(int(os.Stdin.Fd())) {
		inBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, err
		}
		operations = string(inBytes)
	}

	res := make(map[string]interface{})
	err := engineClient.Do(ctx, operations, operation, vars, &res)
	return res, err
}

func getKVInput(kvs []string) map[string]interface{} {
	m := make(map[string]interface{})
	for _, kv := range kvs {
		split := strings.SplitN(kv, "=", 2)
		m[split[0]] = split[1]
	}
	return m
}

func init() {
	queryCmd.Flags().StringVar(&queryFile, "doc", "", "Read query from file (defaults to reading from stdin)")
	queryCmd.Flags().StringSliceVar(&queryVarsInput, "var", nil, "List of query variables, in key=value format")
	queryCmd.Flags().StringVar(&queryVarsJSONInput, "var-json", "", "Query variables in JSON format (overrides --var)")
	queryCmd.MarkFlagFilename("doc", "graphql", "gql")
}
