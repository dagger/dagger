package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var exportPath string

var downloadCmd = &FuncCommand{
	Name:    "download",
	Aliases: []string{"export", "dl"},
	Short:   "Download an asset from module function",
	Long:    "Download an asset returned by a module function and save it to the host.\n\nWorks with a Directory, File or Container.",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringVar(&exportPath, "export-path", ".", "Path to export to")
	},
	OnSelectObjectLeaf: func(c *FuncCommand, name string) error {
		switch name {
		case Directory, File, Container:
			c.Select("export")
			c.Arg("path", exportPath)
			if name == File {
				c.Arg("allowParentDirPath", true)
			}
		}
		return nil
	},
	BeforeRequest: func(_ *FuncCommand, cmd *cobra.Command, returnType *modTypeDef) error {
		switch returnType.ObjectName() {
		case Directory, File, Container:
			flag := cmd.Flags().Lookup("export-path")
			if returnType.ObjectName() == Container && flag != nil && !flag.Changed {
				return fmt.Errorf("flag --export-path is required for containers")
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
