package generator

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

var ErrUnknownSDKLang = errors.New("unknown sdk language")

type SDKLang string

const (
	SDKLangGo         SDKLang = "go"
	SDKLangTypeScript SDKLang = "typescript"
)

type Generator interface {
	// GenerateModule runs codegen in a context of a module and returns a map of
	// default filename to content for that file.
	GenerateModule(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*GeneratedState, error)

	// GenerateClient runs codegen in a context of a standalone client and returns
	// a map of default filename to content for that file.
	GenerateClient(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*GeneratedState, error)

	// GenerateLibrary only generate the library bindings for the given schema.
	GenerateLibrary(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*GeneratedState, error)

	// GenerateTypeDefs extract type definitions from a module and returns a map
	// of default filename to content for that file.
	GenerateTypeDefs(ctx context.Context) (*GeneratedState, error)
}

type GeneratedState struct {
	// Overlay is the overlay filesystem that contains generated code to write
	// over the output directory.
	Overlay fs.FS

	// PostCommands are commands that need to be run after the codegen has
	// finished. This is used for example to run `go mod tidy` after generating
	// Go code.
	PostCommands []*exec.Cmd

	// NeedRegenerate indicates that the code needs to be generated again. This
	// can happen if the codegen spat out templates that depend on generated
	// types. In that case the codegen needs to be run again with both the
	// templates and the initially generated types available.
	NeedRegenerate bool
}
