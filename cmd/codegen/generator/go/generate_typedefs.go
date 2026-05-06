//go:build !legacy_typedefs
// +build !legacy_typedefs

package gogenerator

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// GenerateTypeDefs is retained on the default build only to satisfy the
// generator.Generator interface. The standalone typedef-extraction path
// is superseded by the AST-based scan embedded in GenerateModule; build
// with -tags legacy_typedefs to restore the old packages.Load-based
// implementation (see generate_typedefs_legacy.go).
func (g *GoGenerator) GenerateTypeDefs(ctx context.Context, schema *introspection.Schema, schemaVersion string) (*generator.GeneratedState, error) {
	return nil, fmt.Errorf("generate-typedefs is no longer supported; rebuild with -tags legacy_typedefs to use the legacy path")
}
