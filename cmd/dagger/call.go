package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var outputFormat string
var outputPath string
var jsonOutput bool
var responsePayload map[string]any

var callCmd = &FuncCommand{
	Name:  "call [options]",
	Short: "Call a module function",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "Save the result to a local file or directory")

		cmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "Present result as JSON")
	},
	OnSelectObjectLeaf: func(ctx context.Context, fc *FuncCommand, obj functionProvider) error {
		typeName := obj.ProviderName()
		switch typeName {
		case Container, Directory, File:
			if outputPath != "" {
				fc.Select("export")
				fc.Arg("path", outputPath)
				if typeName == File {
					fc.Arg("allowParentDirPath", true)
				}
				return nil
			}
		}

		// There's no fields in `Container` that trigger container execution so
		// we use `sync` first to evaluate, and then load the new `Container`
		// from that response before continuing.
		if typeName == Container {
			var cid dagger.ContainerID
			fc.Select("sync")
			if err := fc.Request(ctx, &cid); err != nil {
				return err
			}
			fc.q = fc.q.Root().Select("loadContainerFromID").Arg("id", cid)
		}

		// Add the object's name so we always have something to show.
		responsePayload = make(map[string]any)
		responsePayload["_type"] = typeName

		names := make([]string, 0)
		for _, f := range GetLeafFunctions(obj) {
			names = append(names, f.Name)
		}

		if len(names) > 0 {
			// FIXME: Consider adding a method to the query builder speficically
			// for multiple selections to avoid this workaround. Even if there's
			// just one field it helps to show the field's name, not just the
			// value, and multiple selection allows it because it binds to the
			// parent selection.
			if len(names) == 1 {
				names = append(names, "")
			}

			fc.Select(names...)
		}

		return nil
	},
	AfterResponse: func(c *FuncCommand, cmd *cobra.Command, modType *modTypeDef, response any) error {
		switch modType.Name() {
		case Container, Directory, File:
			if outputPath != "" {
				respPath, ok := response.(string)
				if !ok {
					return fmt.Errorf("unexpected response %T: %+v", response, response)
				}
				cmd.PrintErrf("Saved to %q.\n", respPath)
				return nil
			}
		}

		if responsePayload != nil {
			r, err := addPayload(response, responsePayload)
			if err != nil {
				return err
			}
			response = r

			// Use yaml when printing scalars because it's more human-readable
			// and handles lists and multiline strings well.
			if stdoutIsTTY {
				outputFormat = "yaml"
			} else {
				outputFormat = "json"
			}
		}

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
		case "":
			if err := printFunctionResult(buf, response); err != nil {
				return err
			}
		default:
			return fmt.Errorf("wrong output format %q", outputFormat)
		}

		if outputPath != "" {
			if err := writeOutputFile(outputPath, buf); err != nil {
				return fmt.Errorf("couldn't write output to file: %w", err)
			}
			path, err := filepath.Abs(outputPath)
			if err != nil {
				// don't fail because at this point the output has been saved successfully
				slog.Warn("Failed to get absolute path", "error", err)
				path = outputPath
			}
			cmd.PrintErrf("Saved output to %q.\n", path)
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

// addPayload merges a map into a response from getting an object's values.
func addPayload(response any, payload map[string]any) (any, error) {
	switch t := response.(type) {
	case []any:
		r := make([]any, 0, len(t))
		for _, v := range t {
			p, err := addPayload(v, payload)
			if err != nil {
				return nil, err
			}
			r = append(r, p)
		}
		return r, nil
	case map[string]any:
		if len(t) == 0 {
			return payload, nil
		}
		r := make(map[string]any, len(t)+len(payload))
		for k, v := range t {
			r[k] = v
		}
		for k, v := range responsePayload {
			r[k] = v
		}
		return r, nil
	default:
		return nil, fmt.Errorf("unexpected response %T for object values: %+v", response, response)
	}
}

// writeOutputFile writes the buffer to a file, creating the parent directories
// if needed.
func writeOutputFile(path string, buf *bytes.Buffer) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644) //nolint: gosec
}

func printFunctionResult(w io.Writer, r any) error {
	switch t := r.(type) {
	case []any:
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
