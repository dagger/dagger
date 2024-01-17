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
			path, err := filepath.Abs(outputPath)
			if err != nil {
				path = outputPath
			}

			// Exported successfully if it got to this point.
			if outputExport {
				// Not actually an error but stderr is also used for log messages.
				cmd.PrintErrf("Exported to %q.\n", path)
				return nil
			}

			file, err := os.Create(outputPath)
			if err != nil {
				return err
			}

			defer func() {
				file.Close()
				cmd.PrintErrf("Saved result to %q.\n", path)
			}()

			writer = io.MultiWriter(writer, file)
		}

		return printFunctionResult(writer, response)
	},
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
