//go:build tools

// Package codegendeps requires the third-party packages that code generated
// for Go modules imports, but this SDK itself doesn't.
//
// Module codegen copies this module's requirements into each generated
// module's go.mod. Requiring these here pins their versions there too, so
// `go mod tidy` in a generated module doesn't look up, over the network,
// which module provides each import and pick whatever version is newest.
//
// The tools build tag keeps them out of builds; go mod tidy still sees them.
// Keep in sync with the imports in cmd/codegen/generator/go/templates.
package codegendeps

import (
	_ "github.com/Khan/genqlient/graphql"
	_ "github.com/dagger/otel-go"
	_ "github.com/dagger/querybuilder"
	_ "github.com/vektah/gqlparser/v2/gqlerror"
	_ "go.opentelemetry.io/otel"
	_ "go.opentelemetry.io/otel/propagation"
	_ "go.opentelemetry.io/otel/sdk/resource"
	_ "go.opentelemetry.io/otel/semconv/v1.40.0"
	_ "go.opentelemetry.io/otel/trace"
)
