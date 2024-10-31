// dagger-json-schema is a tool to generate json schema from Dagger module config
// struct.
package main

import (
	"encoding/json"
	"os"
	"slices"

	"github.com/dagger/dagger/core/modules"
	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:  "json-schema",
	RunE: generateSchema,
	Args: cobra.ExactArgs(1),
}

func generateSchema(cmd *cobra.Command, args []string) error {
	for _, target := range targets {
		if !slices.Contains(args, target.id) {
			continue
		}

		r := new(jsonschema.Reflector)
		if err := r.AddGoComments("github.com/dagger/dagger", target.path); err != nil {
			return err
		}

		s := r.Reflect(target.value)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(s); err != nil {
			return err
		}
	}

	return nil
}

var targets = []target{
	{
		id:    "dagger.json",
		path:  "./core/modules",
		value: &modules.ModuleConfig{},
	},
}

type target struct {
	id    string
	path  string
	value any
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
