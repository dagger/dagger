package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	experimentalIncludeCore bool
)

var callCmd = &FuncCommand{
	Name:  "call",
	Short: "Call a module function",
	Long:  "Call a module function and print the result.\n\nOn a container, the stdout will be returned. On a directory, the list of entries, and on a file, its contents.",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().BoolVarP(&experimentalIncludeCore, "experimental-include-core", "x", false, "Enable experimental inclusion of core APIs in the call command")
	},
	BeforeLoad: func(fc *FuncCommand) error {
		fc.IncludeCore = experimentalIncludeCore
		return nil
	},
	OnSelectObjectLeaf: func(c *FuncCommand, name string) error {
		switch name {
		case Container:
			// TODO: Combined `output` in the API. Querybuilder
			// doesn't support querying sibling fields.
			c.Select("stdout")
		case Directory:
			c.Select("entries")
		case File:
			c.Select("contents")
		default:
			// TODO: Check if it's a core object and sub-select `id` by default.
			return fmt.Errorf("return type not supported: %s", name)
		}
		return nil
	},
	AfterResponse: func(_ *FuncCommand, cmd *cobra.Command, _ *modTypeDef, response any) error {
		return printResponse(cmd, response)
	},
}

func printResponse(cmd *cobra.Command, r any) error {
	switch t := r.(type) {
	case []any:
		for _, v := range t {
			if err := printResponse(cmd, v); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, v := range t {
			if err := printResponse(cmd, v); err != nil {
				return err
			}
		}
		return nil
	default:
		cmd.Printf("%+v\n", t)
	}
	return nil
}
