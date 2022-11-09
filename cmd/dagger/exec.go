package main

import (
	"github.com/spf13/cobra"
)

var execCmd = &cobra.Command{
	Use:                   "exec [command]",
	Aliases:               []string{"x"},
	DisableFlagsInUseLine: true,
	Long:                  "Executes the specified command in a Dagger session\n\nDAGGER_HOST will be injected automatically.",
	Short:                 "Executes a command in a Dagger session",
	Example: `
dagger query <<EOF
{
  container {
    from(address:"hello-world") {
      exec(args:["/hello"]) {
        stdout {
          contents
        }
      }
    }
  }
}
EOF
`,
	Run:  Exec,
	Args: cobra.MaximumNArgs(1), // operation can be specified
}

func Exec(cmd *cobra.Command, args []string) {
	// ctx := context.Background()
}
