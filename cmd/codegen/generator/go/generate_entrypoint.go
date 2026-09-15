package gogenerator

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/cmd/codegen/generator"
)

// GenerateEntrypoint is a no-op for the Go SDK — the engine constructs the
// dispatch directly from the user's compiled module, no entrypoint file is
// generated.
func (g *GoGenerator) GenerateEntrypoint(ctx context.Context) (*generator.GeneratedState, error) {
	return nil, fmt.Errorf("generate-entrypoint is not implemented for the %s SDK", generator.SDKLangGo)
}
