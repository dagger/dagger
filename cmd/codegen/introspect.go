package main

import (
	"encoding/json"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/spf13/cobra"
)

var outputSchema string

var introspectCmd = &cobra.Command{
	Use:  "introspect",
	RunE: Introspect,
}

func Introspect(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	defer dag.Close()

	var data any
	err = dag.Do(ctx, &dagger.Request{
		Query: introspection.Query,
	}, &dagger.Response{
		Data: &data,
	})
	if err != nil {
		return fmt.Errorf("introspection query: %w", err)
	}
	if data != nil {
		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal introspection json: %w", err)
		}
		if outputSchema != "" {
			return os.WriteFile(outputSchema, jsonData, 0o644)
		}
		cmd.Println(string(jsonData))
	}
	return nil
}

func init() {
	introspectCmd.Flags().StringVarP(&outputSchema, "output", "o", "", "save introspection result to file")
}
