package daggercmd

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client"
)

var workspaceExportCmd = newWorkspaceExportCmd()

type workspaceExportOptions struct {
	output  string
	include []string
	exclude []string
}

func newWorkspaceExportCmd() *cobra.Command {
	var opts workspaceExportOptions
	cmd := &cobra.Command{
		Use:   "export [PATH] -o DEST",
		Short: "Export a file or directory from the selected workspace",
		Long: `Export a file or directory from the selected workspace to the local client.

PATH defaults to the workspace's current directory. Relative paths start
at that directory. Absolute paths start at the workspace root.

For a directory, merge its contents into DEST. Use --include and --exclude
to filter directory contents. These options can be repeated and use patterns
relative to PATH. They cannot be used with a file.`,
		Example: `  dagger ws export -o ./workspace
  dagger ws export ./dist -o ./download
  dagger -W github.com/dagger/dagger ws export /README.md -o ./README.md`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.output == "" {
				return fmt.Errorf("--output is required")
			}
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			return withEngine(cmd.Context(), client.Params{
				SkipWorkspaceModules: true,
			}, func(ctx context.Context, engineClient *client.Client) error {
				finalPath, err := exportWorkspacePath(ctx, engineClient.Dagger().CurrentWorkspace(), target, opts)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Saved to %q.\n", finalPath)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&opts.output, "output", "o", "", "Local destination path")
	cmd.Flags().StringArrayVar(&opts.include, "include", nil, "Include directory paths that match the glob pattern (repeatable)")
	cmd.Flags().StringArrayVar(&opts.exclude, "exclude", nil, "Exclude directory paths that match the glob pattern (repeatable)")
	return cmd
}

func exportWorkspacePath(ctx context.Context, ws *dagger.Workspace, target string, opts workspaceExportOptions) (string, error) {
	dir := ws.Directory(target)
	if _, dirErr := dir.Sync(ctx); dirErr == nil {
		return ws.Directory(target, dagger.WorkspaceDirectoryOpts{
			Include: opts.include,
			Exclude: opts.exclude,
		}).Export(ctx, opts.output)
	} else {
		file := ws.File(target)
		if _, fileErr := file.Sync(ctx); fileErr != nil {
			return "", fmt.Errorf("export workspace path %q: %w", target, dirErr)
		}
		if len(opts.include) > 0 || len(opts.exclude) > 0 {
			return "", fmt.Errorf("--include and --exclude can only be used with a directory")
		}
		return file.Export(ctx, opts.output, dagger.FileExportOpts{AllowParentDirPath: true})
	}
}
