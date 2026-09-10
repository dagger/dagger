package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client"
)

var workspaceFindNames []string

var workspaceFindCmd = &cobra.Command{
	Use:   "find [PATH...]",
	Short: "Find paths in the selected workspace",
	Long: `Find files and directories in the selected workspace.

PATH defaults to the workspace's current directory. Relative paths start
at that directory. Absolute paths start at the workspace root.

Without --name, print each target followed by its contents, one path per line.
Directory entries end with /. Hidden entries are included.

Use --name to print paths with a base name that matches a glob pattern.
Use --name more than once to match any of the patterns.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, pattern := range workspaceFindNames {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("invalid name pattern %q: %w", pattern, err)
			}
		}
		if len(args) == 0 {
			args = []string{"."}
		}
		return withEngine(cmd.Context(), client.Params{
			SkipWorkspaceModules: true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			ws := engineClient.Dagger().CurrentWorkspace()
			var errs []error
			for _, target := range args {
				displayTarget := path.Clean(target)
				entries, err := ws.Directory(target).Glob(ctx, "**/*")
				if err != nil {
					// A file cannot be searched as a directory. Validate it before
					// printing its path, retaining the directory error if both fail.
					if _, fileErr := ws.File(target).Sync(ctx); fileErr != nil {
						errs = append(errs, fmt.Errorf("find workspace path %q: %w", target, err))
						continue
					}
					entries = nil
				}

				if workspaceFindNameMatches(displayTarget, workspaceFindNames) {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), displayTarget); err != nil {
						return err
					}
				}
				for _, entry := range entries {
					displayPath := workspaceFindDisplayPath(displayTarget, entry)
					if !workspaceFindNameMatches(displayPath, workspaceFindNames) {
						continue
					}
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), displayPath); err != nil {
						return err
					}
				}
			}
			return errors.Join(errs...)
		})
	},
}

func workspaceFindNameMatches(target string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	nameTarget := strings.TrimSuffix(target, "/")
	if nameTarget == "" {
		nameTarget = "/"
	}
	name := path.Base(nameTarget)
	for _, pattern := range patterns {
		matched, _ := path.Match(pattern, name)
		if matched {
			return true
		}
	}
	return false
}

func init() {
	workspaceFindCmd.Flags().StringArrayVar(&workspaceFindNames, "name", nil, "Print paths with a base name that matches the glob pattern")
}

func workspaceFindDisplayPath(target, entry string) string {
	isDir := strings.HasSuffix(entry, "/")
	entry = strings.TrimSuffix(entry, "/")

	var result string
	switch target {
	case ".":
		result = "./" + entry
	case "/":
		result = "/" + entry
	default:
		result = target + "/" + entry
	}
	if isDir {
		result += "/"
	}
	return result
}
