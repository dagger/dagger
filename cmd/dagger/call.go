package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// TODO: remove these ghost commands after next release (v0.9.8)
var downloadCmd = &cobra.Command{
	Use:                "download",
	Short:              "Download an asset from module function",
	Aliases:            []string{"export", "dl"},
	Hidden:             true,
	SilenceUsage:       true,
	DisableFlagParsing: true,
	Args: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(`%q has been replaced by "dagger call COMMANDS... --output=PATH"`, cmd.CommandPath())
	},
	Run: func(cmd *cobra.Command, args []string) {
		// do nothing
	},
}

var upCmd = &cobra.Command{
	Use:                "up",
	Short:              "Start a service and expose its ports to the host",
	Hidden:             true,
	SilenceUsage:       true,
	DisableFlagParsing: true,
	Args: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(`%q has been replaced by "dagger call COMMANDS... up"`, cmd.CommandPath())
	},
	Run: func(cmd *cobra.Command, args []string) {
		// do nothing
	},
}

var shellCmd = &cobra.Command{
	Use:                "shell",
	Short:              "Open a shell in a container",
	Hidden:             true,
	SilenceUsage:       true,
	DisableFlagParsing: true,
	Args: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(`%q has been replaced by "dagger call COMMANDS... shell"`, cmd.CommandPath())
	},
	Run: func(cmd *cobra.Command, args []string) {
		// do nothing
	},
}

var outputPath string
var outputExport bool

var callCmd = &FuncCommand{
	Name:  "call",
	Short: "Call a module function",
	Long:  "Call a module function and print the result.\n\nOn a container, the stdout will be returned. On a directory, the list of entries, and on a file, its contents.",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "Path in the host to save the result to")
	},
	OnSelectObjectLeaf: func(c *FuncCommand, name string) error {
		switch name {
		case Container:
			if outputPath != "" {
				c.Select("export")
				c.Arg("path", outputPath)
				outputExport = true
				return nil
			}
			// TODO: Combined `output` in the API. Querybuilder
			// doesn't support querying sibling fields.
			c.Select("stdout")
		case Directory:
			if outputPath != "" {
				c.Select("export")
				c.Arg("path", outputPath)
				outputExport = true
				return nil
			}
			c.Select("entries")
		case File:
			if outputPath != "" {
				c.Select("export")
				c.Arg("path", outputPath)
				c.Arg("allowParentDirPath", true)
				outputExport = true
				return nil
			}
			c.Select("contents")
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
		default:
			writer := cmd.OutOrStdout()

			if outputPath != "" {
				if outputExport {
					logOutputSuccess(cmd, outputPath)
					return nil
				}

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
		for _, v := range t {
			if err := printFunctionResult(w, v); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
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
