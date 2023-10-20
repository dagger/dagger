package main

import (
	"dagger.io/dagger"
	"github.com/spf13/cobra"
)

var callCmd = &FuncCommand{
	Name:  "call",
	Short: "Call a module's function and print the result",
	Long:  "On a container, the stdout will be returned. On a directory, the list of entries, and on a file, its contents.",
	OnSelectObject: func(c *callContext, name string) (*modTypeDef, error) {
		switch name {
		case Container:
			// TODO: Combined `output` in the API. Querybuilder
			// doesn't support querying sibling fields.
			c.Select("stdout")
			return &modTypeDef{Kind: dagger.Stringkind}, nil
		case Directory:
			c.Select("entries")
			return &modTypeDef{Kind: dagger.Listkind,
				AsList: &modList{
					ElementTypeDef: &modTypeDef{
						Kind: dagger.Stringkind},
				}}, nil
		case File:
			c.Select("contents")
			return &modTypeDef{Kind: dagger.Stringkind}, nil
		}
		return nil, nil
	},
	AfterResponse: func(_ *callContext, cmd *cobra.Command, _ modTypeDef, response any) error {
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
