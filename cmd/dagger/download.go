package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var exportPath string

var downloadCmd = &FuncCommand{
	Name:  "download",
	Short: "Download an asset to the host (directory, file, container).",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringVar(&exportPath, "export-path", "", "Path to export to")
		cmd.MarkFlagRequired("export-path")
	},
	OnSelectObjectLeaf: func(c *FuncCommand, _ string) error {
		c.Select("export")
		c.Arg("path", exportPath)
		return nil
	},
	BeforeRequest: func(_ *FuncCommand, returnType *modTypeDef) error {
		switch returnType.ObjectName() {
		case Directory, File, Container:
			if exportPath == "" {
				return fmt.Errorf("missing --export-path flag")
			}
			return nil
		}
		return fmt.Errorf("return type not supported: %s", printReturnType(returnType))
	},
	AfterResponse: func(_ *FuncCommand, cmd *cobra.Command, _ *modTypeDef, response any) error {
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
