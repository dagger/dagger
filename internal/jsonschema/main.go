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

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	// go generate runs in this package's directory. Resolve paths from the repository root.
	if err := os.Chdir("../.."); err != nil {
		return err
	}

	targets := []struct {
		output string
		path   string
		value  any
	}{
		{"dagger.schema.json", "./core/modules", &modules.LegacyModuleConfigWithUserFields{}},
		{"dagger-module.schema.json", "./core/modules", &modules.CurrentModuleConfigWithUserFields{}},
		{"dagger-workspace.schema.json", "./core/workspace", &workspace.Config{}},
	}
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

		data, err := json.MarshalIndent(r.Reflect(target.value), "", "  ")
		if err != nil {
			return fmt.Errorf("%s: %w", target.output, err)
		}
		if err := os.WriteFile("docs/static/reference/"+target.output, append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}
