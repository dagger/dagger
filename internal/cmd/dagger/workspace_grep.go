package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
)

var workspaceGrepCmd = newWorkspaceGrepCmd()

type workspaceGrepOptions struct {
	ignoreCase   bool
	fixedStrings bool
	filesOnly    bool
	globs        []string
	all          bool
	multiline    bool
	json         bool
}

func newWorkspaceGrepCmd() *cobra.Command {
	var opts workspaceGrepOptions
	cmd := &cobra.Command{
		Use:   "grep PATTERN [PATH...]",
		Short: "Search file contents in the selected workspace",
		Long: `Search file contents in the selected workspace, including subdirectories.

PATTERN is a case-sensitive regular expression. Use -F for a literal string.
PATH defaults to the workspace's current directory. Relative paths start
at that directory. Absolute paths start at the workspace root.

Honor .gitignore, .ignore, and .rgignore files by default. Include hidden files.
Use --all to include ignored files. Use -g to filter paths with glob patterns.

Print each full matching line as path:line:text, with no surrounding lines.
Output paths are relative to the workspace's current directory.
Highlight matches when stdout is a terminal, unless NO_COLOR is set.
Use -l for paths only, or --json for a JSON array of structured matches.
The -l and --json options cannot be combined.

Exit status is 0 if a match is found, 1 if no matches are found, and 2 on error.`,
		Example: `  dagger ws grep 'CurrentWorkspace' sdk/go core
  dagger -W github.com/dagger/dagger ws grep -i -g '*.md' 'workspace'
  dagger ws grep -l -F 'TODO'`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MinimumNArgs(1)(cmd, args); err != nil {
				return workspaceGrepError(cmd, err)
			}
			if opts.json && opts.filesOnly {
				return workspaceGrepError(cmd, fmt.Errorf("--json and --files-with-matches cannot be combined"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var matched bool
			err := withEngine(cmd.Context(), client.Params{
				SkipWorkspaceModules: true,
			}, func(ctx context.Context, engineClient *client.Client) error {
				dag := engineClient.Dagger()
				cwd, err := dag.CurrentWorkspace().Cwd(ctx)
				if err != nil {
					return fmt.Errorf("load workspace cwd: %w", err)
				}
				paths, err := workspaceGrepPaths(cwd, args[1:])
				if err != nil {
					return err
				}
				results, err := searchWorkspace(ctx, dag, args[0], paths, opts)
				if err != nil {
					return err
				}
				for i := range results {
					results[i].FilePath, err = workspaceGrepDisplayPath(cwd, results[i].FilePath)
					if err != nil {
						return err
					}
				}
				matched = len(results) > 0
				return printWorkspaceGrep(cmd.OutOrStdout(), results, opts, stdoutIsTTY && !termenv.EnvNoColor())
			})
			if err != nil {
				return workspaceGrepError(cmd, err)
			}
			if !matched {
				// An empty result is a successful search, so keep the engine run
				// successful and apply grep's exit status after it completes.
				return idtui.ExitError{OriginalCode: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&opts.ignoreCase, "ignore-case", "i", false, "Ignore case when matching")
	cmd.Flags().BoolVarP(&opts.fixedStrings, "fixed-strings", "F", false, "Treat PATTERN as a literal string")
	cmd.Flags().BoolVarP(&opts.filesOnly, "files-with-matches", "l", false, "Print only paths of matching files")
	cmd.Flags().StringArrayVarP(&opts.globs, "glob", "g", nil, "Include or exclude paths with a glob pattern (repeatable; prefix ! to exclude)")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Include ignored files")
	cmd.Flags().BoolVar(&opts.multiline, "multiline", false, "Allow matches to span multiple lines")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Print a JSON array of structured matches")
	// grep owns -i. Shadow the global flag by name to prevent Cobra from
	// merging its conflicting shorthand, while preserving its long form.
	cmd.Flags().BoolVar(&shellOnError, "shell-on-error", false, "Open a shell when a container exec fails (needs an interactive terminal)")
	shellFlag := cmd.Flags().Lookup("shell-on-error")
	setFlagCapabilities(shellFlag, mayCallEngine)
	shellFlag.Annotations[globalFlagAliasAnnotation] = []string{"true"}
	cmd.SetFlagErrorFunc(workspaceGrepError)
	return cmd
}

// workspaceGrepError preserves diagnostics already printed by the frontend.
func workspaceGrepError(cmd *cobra.Command, err error) error {
	var exit idtui.ExitError
	if !errors.As(err, &exit) {
		fmt.Fprintln(cmd.ErrOrStderr(), cmd.ErrPrefix(), err)
	}
	return idtui.ExitError{OriginalCode: 2, Original: err}
}

// Workspace.search takes root-relative paths; file and directory take paths
// relative to the workspace cwd. Keep that distinction out of the CLI.
func workspaceGrepPaths(cwd string, targets []string) ([]string, error) {
	relCwd, err := workspaceRelativeCwd(cwd)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		targets = []string{"."}
	}
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		p := filepath.ToSlash(target)
		if path.IsAbs(p) {
			p = path.Clean(strings.TrimLeft(p, "/"))
		} else {
			p = path.Join(filepath.ToSlash(relCwd), p)
		}
		if p == ".." || strings.HasPrefix(p, "../") {
			return nil, fmt.Errorf("search path %q escapes workspace root", target)
		}
		// Keep paths beginning with '-' from becoming search tool options.
		if strings.HasPrefix(p, "-") {
			p = "./" + p
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func workspaceGrepDisplayPath(cwd, filePath string) (string, error) {
	relCwd, err := workspaceRelativeCwd(cwd)
	if err != nil {
		return "", err
	}
	name, err := filepath.Rel(filepath.Join("/", relCwd), filepath.Join("/", filepath.FromSlash(filePath)))
	if err != nil {
		return "", fmt.Errorf("resolve search result path %q: %w", filePath, err)
	}
	return filepath.ToSlash(name), nil
}

type workspaceGrepResult struct {
	FilePath       string                  `json:"filePath"`
	LineNumber     int                     `json:"lineNumber"`
	MatchedLines   string                  `json:"matchedLines"`
	AbsoluteOffset int                     `json:"absoluteOffset"`
	Submatches     []workspaceGrepSubmatch `json:"submatches"`
}

type workspaceGrepSubmatch struct {
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func searchWorkspace(ctx context.Context, dag *dagger.Client, pattern string, paths []string, opts workspaceGrepOptions) ([]workspaceGrepResult, error) {
	fields := "filePath lineNumber matchedLines absoluteOffset submatches { text start end }"
	if opts.filesOnly {
		fields = "filePath"
	}
	var res struct {
		CurrentWorkspace struct {
			Search []workspaceGrepResult
		}
	}
	err := dag.Do(ctx, &dagger.Request{
		Query: `query WorkspaceGrep($pattern: String!, $paths: [String!]!, $globs: [String!]!,
			$literal: Boolean!, $multiline: Boolean!, $insensitive: Boolean!,
			$skipIgnored: Boolean!, $filesOnly: Boolean!) {
			currentWorkspace {
				search(pattern: $pattern, paths: $paths, globs: $globs,
					literal: $literal, multiline: $multiline, insensitive: $insensitive,
					skipIgnored: $skipIgnored, skipHidden: false, filesOnly: $filesOnly) {
					` + fields + `
				}
			}
		}`,
		Variables: map[string]any{
			"pattern": pattern, "paths": paths, "globs": append([]string{}, opts.globs...),
			"literal": opts.fixedStrings, "multiline": opts.multiline, "insensitive": opts.ignoreCase,
			"skipIgnored": !opts.all, "filesOnly": opts.filesOnly,
		},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return nil, fmt.Errorf("search workspace: %w", err)
	}
	return res.CurrentWorkspace.Search, nil
}

func printWorkspaceGrep(out io.Writer, results []workspaceGrepResult, opts workspaceGrepOptions, color bool) error {
	if results == nil {
		results = []workspaceGrepResult{}
	}
	if opts.json {
		for i := range results {
			if results[i].Submatches == nil {
				results[i].Submatches = []workspaceGrepSubmatch{}
			}
		}
		return json.NewEncoder(out).Encode(results)
	}
	seen := map[string]bool{}
	for _, result := range results {
		if opts.filesOnly {
			if !seen[result.FilePath] {
				if _, err := fmt.Fprintln(out, result.FilePath); err != nil {
					return err
				}
				seen[result.FilePath] = true
			}
			continue
		}
		var offset int
		for i, line := range strings.Split(strings.TrimSuffix(result.MatchedLines, "\n"), "\n") {
			text := line
			if color {
				text = highlightWorkspaceGrepLine(line, offset, result.Submatches)
			}
			if _, err := fmt.Fprintf(out, "%s:%d:%s\n", result.FilePath, result.LineNumber+i, text); err != nil {
				return err
			}
			offset += len(line) + 1
		}
	}
	return nil
}

func highlightWorkspaceGrepLine(line string, offset int, matches []workspaceGrepSubmatch) string {
	// Offsets are bytes within matchedLines, including any preceding newlines.
	// Sort a copy so rendering does not change structured results.
	matches = slices.Clone(matches)
	slices.SortFunc(matches, func(a, b workspaceGrepSubmatch) int { return a.Start - b.Start })
	var out strings.Builder
	written := 0
	for _, match := range matches {
		start := max(written, match.Start-offset)
		end := min(len(line), match.End-offset)
		if start >= end {
			continue
		}
		out.WriteString(line[written:start])
		out.WriteString("\x1b[1;31m")
		out.WriteString(line[start:end])
		out.WriteString("\x1b[0m")
		written = end
	}
	out.WriteString(line[written:])
	return out.String()
}
