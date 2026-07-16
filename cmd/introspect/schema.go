package main

import (
	"os"

	"github.com/dagger/dagger/cmd/codegen/introspection/sdl"
	"github.com/spf13/cobra"
)

// Schema generates and outputs a graphqls schema file for dagger.
func Schema(cmd *cobra.Command, args []string) error {
	resp, err := getIntrospection(cmd.Context())
	if err != nil {
		return err
	}
	sdl.Format(os.Stdout, resp.Schema)
	return nil
}
