package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var outputFormat string
var outputPath string
var jsonOutput bool

var callCmd = &FuncCommand{
	Name:  "call [options]",
	Short: "Call a module function",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "Save the result to a local file or directory")

		cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Present result as JSON")
	},
	OnSelectObjectLeaf: func(fc *FuncCommand, obj functionProvider) error {
		names := make([]string, 0)
		for _, f := range GetLeafFunctions(obj) {
			names = append(names, f.Name)
		}
		if len(names) > 0 {
			// FIXME: Consider adding a method to the query builder specifically
			// for multiple selections to avoid this workaround. Even if there's
			// just one field it helps to show the field's name, not just the
			// value, and multiple selection allows it because it binds to the
			// parent selection.
			if len(names) == 1 {
				names = append(names, "")
			}
			fc.Select(names...)
			outputFormat = "yaml"
		}
		return nil
	},
	AfterResponse: func(c *FuncCommand, cmd *cobra.Command, modType *modTypeDef, response any) error {
		if jsonOutput {
			outputFormat = "json"
		}

		buf := new(bytes.Buffer)

		switch outputFormat {
		case "json":
			// disable HTML escaping to improve readability
			encoder := json.NewEncoder(buf)
			encoder.SetEscapeHTML(false)
			encoder.SetIndent("", "    ")
			if err := encoder.Encode(response); err != nil {
				return err
			}
		case "yaml":
			out, err := yaml.Marshal(response)
			if err != nil {
				return err
			}
			if _, err := buf.Write(out); err != nil {
				return err
			}
		default:
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
