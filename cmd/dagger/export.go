package main

import (
	"fmt"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
)

var exportPath string

var exportCmd = &FuncCommand{
	Name:  "export",
	Short: "Export an asset to the host (directory, file).",
	OnSetFlags: func(cmd *cobra.Command, returnType *modTypeDef) error {
		if returnType.Kind == dagger.Objectkind {
			switch returnType.AsObject.Name {
			case Directory, File:
				cmd.Flags().StringVar(&exportPath, "export-path", "", "Path to export to.")
			}
		}
		return nil
	},
	OnSelectObject: func(c *callContext, _ string) (*modTypeDef, error) {
		if exportPath != "" {
			c.Select("export")
			c.Arg("path", exportPath)
			return &modTypeDef{Kind: dagger.Booleankind}, nil
		}
		return nil, nil
	},
	CheckReturnType: func(_ *callContext, _ *modTypeDef) error {
		if exportPath == "" {
			return fmt.Errorf("missing argument or return type not supported")
		}
		return nil
	},
}
