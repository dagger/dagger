package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var outputPath string
var jsonOutput bool

var callCmd = &FuncCommand{
	Name:  "call [options]",
	Short: "Call a module function",
	Long: strings.ReplaceAll(`Call a module function and print the result.

If the last argument is either a Container, Directory, or File, the pipeline
will be evaluated (the result of calling ´sync´) without presenting any output.
Providing the ´--output´ option (shorthand: ´-o´) is equivalent to calling
´export´ instead. To print a property of these core objects, continue chaining
by appending it to the end of the command (for example, ´stdout´, ´entries´, or
´contents´).
`,
		"´",
		"`",
	),
	Example: strings.TrimSpace(`
dagger call test
dagger call build -o ./bin/myapp
dagger call lint stdout
`,
	),
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Present result as JSON")
		cmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "Path in the host to save the result to")
	},
	OnSelectObjectLeaf: func(c *FuncCommand, name string) error {
		switch name {
		case Container, Directory, File:
			if outputPath != "" {
				c.Select("export")
				c.Arg("path", outputPath)
				if name == File {
					c.Arg("allowParentDirPath", true)
				}
				return nil
			}
			c.Select("sync")
		case Terminal:
			c.Select("websocketEndpoint")
		default:
			return fmt.Errorf("return type %q requires a sub-command", name)
		}
		return nil
	},
	BeforeRequest: func(_ *FuncCommand, _ *cobra.Command, modType *modTypeDef) error {
		return nil
	},
	AfterResponse: func(c *FuncCommand, cmd *cobra.Command, modType *modTypeDef, response any) error {
		switch modType.Name() {
		case Container, Directory, File:
			if outputPath != "" {
				logOutputSuccess(cmd, outputPath)
				return nil
			}

			// Just `sync`, don't print the result (id), but let user know.

			// TODO: This is only "needed" when there's no output because
			// you're left wondering if the command did anything. Otherwise,
			// the output is sent only to progrock (TUI), so we'd need to check
			// there if possible. Decide whether this message is ok in all cases,
			// better to not print it, or to conditionally check.
			cmd.PrintErrf("%s evaluated. Use \"%s --help\" to see available sub-commands.\n", modType.Name(), cmd.CommandPath())
			return nil
		default:
			// TODO: Since IDs aren't stable to be used in the CLI, we should
			// silence all ID results (or present in a compact way like
			// ´<ContainerID:etpdi9gue9l5>`), but need a KindScalar TypeDef
			// to get the name from modType.
			// You can't select `id`, but you can select `sync`, and there
			// may be others.
			buf := new(bytes.Buffer)

			// especially useful for lists and maps
			if jsonOutput {
				// disable HTML escaping to improve readability
				encoder := json.NewEncoder(buf)
				encoder.SetEscapeHTML(false)
				encoder.SetIndent("", "    ")
				if err := encoder.Encode(response); err != nil {
					return err
				}
			} else {
				if err := printFunctionResult(buf, response); err != nil {
					return err
				}
			}

			if outputPath != "" {
				if err := writeOutputFile(outputPath, buf); err != nil {
					return fmt.Errorf("couldn't write output to file: %w", err)
				}
				logOutputSuccess(cmd, outputPath)
			}

			writer := cmd.OutOrStdout()
			buf.WriteTo(writer)

			// TODO(vito) right now when stdoutIsTTY we'll be printing to a Progrock
			// vertex, which currently adds its own linebreak (as well as all the
			// other UI clutter), so there's no point doing this. consider adding
			// back when we switch to printing "clean" output on exit.
			// if stdoutIsTTY && !strings.HasSuffix(buf.String(), "\n") {
			// 	fmt.Fprintln(writer, "⏎")
			// }

			return nil
		}
	},
}

// writeOutputFile writes the buffer to a file, creating the parent directories
// if needed.
func writeOutputFile(path string, buf *bytes.Buffer) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644) //nolint: gosec
}

// logOutputSuccess prints to stderr the output path to the user.
func logOutputSuccess(cmd *cobra.Command, path string) {
	path, err := filepath.Abs(path)
	if err != nil {
		// don't fail because at this point the output has been saved successfully
		cmd.PrintErrf("WARNING: failed to get absolute path: %s\n", err)
		path = outputPath
	}
	cmd.PrintErrf("Saved output to %q.\n", path)
}

func printFunctionResult(w io.Writer, r any) error {
	switch t := r.(type) {
	case []any:
		// TODO: group in progrock
		for _, v := range t {
			if err := printFunctionResult(w, v); err != nil {
				return err
			}
			fmt.Fprintln(w)
		}
		return nil
	case map[string]any:
		// NB: we're only interested in values because this is where we unwrap
		// things like {"container":{"from":{"withExec":{"stdout":"foo"}}}}.
		for _, v := range t {
			if err := printFunctionResult(w, v); err != nil {
				return err
			}
		}
		return nil
	case string:
		fmt.Fprint(w, t)
	default:
		fmt.Fprintf(w, "%+v", t)
	}
	return nil
}
