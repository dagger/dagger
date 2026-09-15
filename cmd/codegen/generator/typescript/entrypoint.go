package typescriptgenerator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/psanford/memfs"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/generator/typescript/templates"
)

// DefaultEntrypointFile is the filename of the static dispatch entrypoint
// the runtime expects at the user's module root.
const DefaultEntrypointFile = "__dagger.entrypoint.ts"

// GenerateEntrypoint renders the static dispatch `__dagger.entrypoint.ts`
// for the user's module from a previously-emitted typedef JSON. The path to
// that JSON, the module root, and other options come from
// `Config.EntrypointConfig`.
func (g *TypeScriptGenerator) GenerateEntrypoint(ctx context.Context) (*generator.GeneratedState, error) {
	cfg := g.Config.EntrypointConfig
	if cfg == nil {
		return nil, fmt.Errorf("generate-entrypoint: missing EntrypointConfig")
	}
	if cfg.TypedefJSONPath == "" {
		return nil, fmt.Errorf("generate-entrypoint: TypedefJSONPath is required")
	}

	data, err := os.ReadFile(cfg.TypedefJSONPath)
	if err != nil {
		return nil, fmt.Errorf("read typedef json %q: %w", cfg.TypedefJSONPath, err)
	}

	var module templates.TypedefModule
	if err := json.Unmarshal(data, &module); err != nil {
		return nil, fmt.Errorf("parse typedef json: %w", err)
	}

	tmpl := templates.NewEntrypoint(&module, templates.EntrypointOptions{
		SDKImportPath: cfg.SDKImportPath,
		ModuleRoot:    cfg.ModuleRoot,
		SourceDir:     cfg.SourceDir,
	})

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "entrypoint", &module); err != nil {
		return nil, fmt.Errorf("render entrypoint: %w", err)
	}

	outFile := cfg.OutputFile
	if outFile == "" {
		outFile = DefaultEntrypointFile
	}

	mfs := memfs.New()
	if err := mfs.WriteFile(outFile, buf.Bytes(), 0o644); err != nil {
		return nil, fmt.Errorf("write entrypoint to overlay: %w", err)
	}

	return &generator.GeneratedState{Overlay: mfs}, nil
}
