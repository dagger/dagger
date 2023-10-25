package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var callCmd = &FuncCommand{
	Name:  "call",
	Short: "Call a module function",
	Long:  "Call a module function and print the result.\n\nOn a container, the stdout will be returned. On a directory, the list of entries, and on a file, its contents.",
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
		if list, ok := (response).([]any); ok {
			for _, v := range list {
				cmd.Printf("%+v\n", v)
			}
			return nil
		}
		cmd.Printf("%+v\n", response)
		return nil
	},
}
