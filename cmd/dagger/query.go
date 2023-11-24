package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
	"golang.org/x/term"
)

var (
	queryFocus         bool
	queryFile          string
	queryVarsInput     []string
	queryVarsJSONInput string
)

var queryCmd = &cobra.Command{
	Use:                   "query [flags] [operation]",
	Aliases:               []string{"q"},
	DisableFlagsInUseLine: true,
	Long:                  "Send API queries to a dagger engine\n\nWhen no document file, read query from standard input.",
	Short:                 "Send API queries to a dagger engine",
	Example: `
dagger query <<EOF
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
	RunE: func(cmd *cobra.Command, args []string) error {
		focus = queryFocus
		return loadModCmdWrapper(Query, "")(cmd, args)
	},
	Args: cobra.MaximumNArgs(1), // operation can be specified
}

func init() {
	queryCmd.Flags().BoolVar(&queryFocus, "focus", false, "Only show output for focused commands.")
}

func Query(ctx context.Context, engineClient *client.Client, _ *dagger.Module, _ *cobra.Command, args []string) (rerr error) {
	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex("query", "query", progrock.Focused())
	defer func() { vtx.Done(rerr) }()

	res, err := runQuery(ctx, engineClient, args)
	if err != nil {
		return err
	}
	result, err := json.MarshalIndent(res, "", "    ")
	if err != nil {
		return err
	}

	var out io.Writer
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		out = os.Stdout
	} else {
		out = vtx.Stdout()
	}

	fmt.Fprintf(out, "%s\n", result)

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
	// changing usage template so examples are displayed below flags
	queryCmd.SetUsageTemplate(`Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}
Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`)

	queryCmd.Flags().StringVar(&queryFile, "doc", "", "document query file")
	queryCmd.Flags().StringSliceVar(&queryVarsInput, "var", nil, "query variable")
	queryCmd.Flags().StringVar(&queryVarsJSONInput, "var-json", "", "json query variables (overrides --var)")
}
