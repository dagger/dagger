package main

import (
	"fmt"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
)

var exportPath string

var downloadCmd = &FuncCommand{
	Name:  "download",
	Short: "Download an asset to the host (directory, file, container).",
	OnInit: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringVar(&exportPath, "export-path", "", "Path to export to")
		cmd.MarkFlagRequired("export-path")
	},
	OnSelectObject: func(c *callContext, _ string) (*modTypeDef, error) {
		c.Select("export")
		c.Arg("path", exportPath)
		return nil, nil
	},
	CheckReturnType: func(_ *callContext, r *modTypeDef) error {
		if r.Kind == dagger.Objectkind {
			switch r.AsObject.Name {
			case Directory, File, Container:
				if exportPath == "" {
					return fmt.Errorf("missing --export-path flag")
				}
				return nil
			}
		}
		return fmt.Errorf("return type not supported: %s", printReturnType(r))
	},
	AfterResponse: func(_ *callContext, cmd *cobra.Command, _ modTypeDef, response any) error {
		status, ok := response.(bool)
		if !ok {
			return fmt.Errorf("unexpected response %T: %+v", response, response)
		}
		if !status {
			return fmt.Errorf("failed to export asset to %q", exportPath)
		}
		cmd.Printf("Asset exported to %q\n", exportPath)
		return nil
	},
}
