package main

import (
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
)

var callCmd = &FuncCommand{
	Name:  "call",
	Short: "Call a module's function.",
	OnSelectObject: func(c *callContext, name string) (*modTypeDef, error) {
		switch name {
		case Container:
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
			c.Select("content")
			return &modTypeDef{Kind: dagger.Stringkind}, nil
		}
		return nil, nil
	},
	OnResult: func(_ *callContext, cmd *cobra.Command, _ modTypeDef, result *any) error {
		lines, ok := (*result).([]string)
		if ok {
			cmd.Println(strings.Join(lines, "\n"))
		} else {
			cmd.Printf("%+v\n", *result)
		}
		return nil
	},
}
