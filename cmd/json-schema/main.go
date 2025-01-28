// dagger-json-schema is a tool to generate json schema from Dagger module config
// struct.
package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/config"
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

		commentMap := make(map[string]string)
		if err := jsonschema.ExtractGoComments("github.com/dagger/dagger", target.path, commentMap); err != nil {
			return err
		}
		for k, v := range commentMap {
			// remove all standalone newlines
			re := regexp.MustCompile(`([^\n])\n([^\n])`)
			commentMap[k] = re.ReplaceAllString(v, `$1 $2`)
		}

		r := new(jsonschema.Reflector)
		r.CommentMap = commentMap

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
		value: &modules.ModuleConfigWithUserFields{},
	},
	{
		id:    "engine.json",
		path:  "./engine/config",
		value: &config.Config{},
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
