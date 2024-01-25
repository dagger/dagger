package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var outputPath string
var jsonOutput bool

var callCmd = &FuncCommand{
	Name:  "call",
	Short: "Call a module function",
	Long: `Call a module function and print the result

If the last argument is either Container, Directory, or File, the pipeline
will be evaluated (the result of calling *sync*) without presenting any output.
Providing the --output option (shorthand: -o) is equivalent to calling *export*
instead. To print a property of these core objects, continue chaining by
appending it to the end of the command (for example, *stdout*, *entries*, or
*contents*).
`,
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
		if modType.Name() != Terminal {
			return nil
		}

		// Even though these flags are global, we only check them just before query
		// execution because you may want to debug an error during loading or for
		// --help.
		if silent || !(progress == "auto" && autoTTY || progress == "tty") {
			return fmt.Errorf("running shell without the TUI is not supported")
		}
		if debug {
			return fmt.Errorf("running shell with --debug is not supported")
		}
		if outputPath != "" {
			return fmt.Errorf("running shell with --output is not supported")
		}
		return nil
	},
	AfterResponse: func(c *FuncCommand, cmd *cobra.Command, modType *modTypeDef, response any) error {
		switch modType.Name() {
		case Terminal:
			termEndpoint, ok := response.(string)
			if !ok {
				return fmt.Errorf("unexpected response %T: %+v", response, response)
			}
			return attachToShell(cmd.Context(), c.c, termEndpoint)
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
			writer := cmd.OutOrStdout()

			if outputPath != "" {
				file, err := openOutputFile(outputPath)
				if err != nil {
					return fmt.Errorf("couldn't write output to file: %w", err)
				}

				defer func() {
					file.Close()
					logOutputSuccess(cmd, outputPath)
				}()

				writer = io.MultiWriter(writer, file)
			}

			// especially useful for lists and maps
			if jsonOutput {
				jb, err := json.MarshalIndent(response, "", "    ")
				if err != nil {
					return err
				}
				fmt.Fprintf(writer, "%s\n", jb)
				return nil
			}

			return printFunctionResult(writer, response)
		}
	},
}

// openOutputFile opens a file for writing, creating the parent directories if needed.
func openOutputFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
}

// logOutputSuccess prints to stderr the the output path to the user.
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
		}
		return nil
	case map[string]any:
		// TODO: group in progrock
		for _, v := range t {
			if err := printFunctionResult(w, v); err != nil {
				return err
			}
		}
		return nil
	default:
		fmt.Fprintf(w, "%+v\n", t)
	}
	return nil
}
