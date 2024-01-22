package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

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
		default:
			return fmt.Errorf("return type %q requires a sub-command", name)
		}
		return nil
	},
	AfterResponse: func(_ *FuncCommand, cmd *cobra.Command, _ *modTypeDef, response any) error {
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
