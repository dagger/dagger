// Generate JSON schemas for Dagger configuration files.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/invopop/jsonschema"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
)

type target struct {
	output string
	path   string
	value  any
}

var targets = []target{
	{"dagger.schema.json", "./core/modules", &modules.LegacyModuleConfigWithUserFields{}},
	{"dagger-module.schema.json", "./core/modules", &modules.CurrentModuleConfigWithUserFields{}},
	{"dagger-workspace.schema.json", "./core/workspace", &workspace.Config{}},
}

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	newline := regexp.MustCompile(`([^\n])\n([^\n])`)
	for _, target := range targets {
		r := new(jsonschema.Reflector)
		if err := r.AddGoComments("github.com/dagger/dagger", target.path); err != nil {
			return fmt.Errorf("%s: %w", target.output, err)
		}
		for k, v := range r.CommentMap {
			// Remove standalone newlines.
			r.CommentMap[k] = newline.ReplaceAllString(v, `$1 $2`)
		}

		schema := r.Reflect(target.value)
		if target.output == "dagger-workspace.schema.json" {
			if err := addWorkspaceCompatibilityAliases(schema); err != nil {
				return fmt.Errorf("%s: %w", target.output, err)
			}
		}

		data, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			return fmt.Errorf("%s: %w", target.output, err)
		}
		if err := os.WriteFile("docs/static/reference/"+target.output, append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func addWorkspaceCompatibilityAliases(schema *jsonschema.Schema) error {
	moduleEntry := schema.Definitions["ModuleEntry"]
	if moduleEntry == nil || moduleEntry.Properties == nil {
		return fmt.Errorf("ModuleEntry definition not found")
	}
	moduleEntry.Properties.Set("entrypoint", &jsonschema.Schema{
		Type:        "boolean",
		Description: "Deprecated alias for main.",
		Deprecated:  true,
	})
	return nil
}
