package generator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"dagger.io/dagger"
	"dagger.io/dagger/codegen/introspection"
	"dagger.io/dagger/modules"
)

var ErrUnknownSDKLang = errors.New("unknown sdk language")

type SDKLang string

const (
	SDKLangGo     SDKLang = "go"
	SDKLangNodeJS SDKLang = "nodejs"
)

type Config struct {
	// Language supported by this codegen infra.
	Lang SDKLang

	// Destination directory for generated code.
	OutputDir string

	// Generate code for a Dagger module.
	ModuleRef    *modules.Ref
	ModuleConfig *modules.Config
}

type Generator interface {
	// Generate runs codegen and returns a map of default filename to content for that file.
	Generate(ctx context.Context, schema *introspection.Schema) (*GeneratedState, error)
}

type GeneratedState struct {
	// Overlay is the overlay filesystem that contains generated code to write
	// over the output directory.
	Overlay fs.FS

	// PostCommands are commands that need to be run after the codegen has
	// finished. This is used for example to run `go mod tidy` after generating
	// Go code.
	PostCommands []*exec.Cmd

	// NeedSync indicates that the code needs to be generated again. This can
	// happen if the codegen spat out templates that depend on generated types.
	// In that case the codegen needs to be run again with both the templates and
	// the initially generated types available.
	NeedRegenerate bool
}

// SetSchemaParents sets all the parents for the fields.
func SetSchemaParents(schema *introspection.Schema) {
	for _, t := range schema.Types {
		for _, f := range t.Fields {
			f.ParentObject = t
		}
	}
}

// Introspect gets the Dagger Schema
func Introspect(ctx context.Context, dag *dagger.Client) (*introspection.Schema, error) {
	var introspectionResp introspection.Response
	err := dag.Do(ctx, &dagger.Request{
		Query:  introspection.Query,
		OpName: "IntrospectionQuery",
	}, &dagger.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, fmt.Errorf("introspection query: %w", err)
	}

	return introspectionResp.Schema, nil
}

func Overlay(ctx context.Context, logsW io.Writer, overlay fs.FS, outputDir string) (rerr error) {
	return fs.WalkDir(overlay, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, err := os.Stat(filepath.Join(outputDir, path)); err == nil {
				return nil
			}
			fmt.Fprintln(logsW, "creating directory", path)
			return os.MkdirAll(filepath.Join(outputDir, path), 0o755)
		}

		var needsWrite bool

		newContent, err := fs.ReadFile(overlay, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		outPath := filepath.Join(outputDir, path)
		oldContent, err := os.ReadFile(outPath)
		if err != nil {
			needsWrite = true
		} else {
			needsWrite = string(oldContent) != string(newContent)
		}

		if needsWrite {
			fmt.Fprintln(logsW, "writing", path)
			return os.WriteFile(outPath, newContent, 0o600)
		}

		return nil
	})
}
