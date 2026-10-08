package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestModuleProcessPlumbingParent(t *testing.T) {
	tracer := sdktrace.NewTracerProvider().Tracer("plumbing-test")
	_, plumbing := tracer.Start(t.Context(), "call module entrypoint")
	requestCtx, _ := tracer.Start(t.Context(), "POST /query")
	requestCtx = WithModuleProcessRequest(requestCtx, plumbing.SpanContext())
	nestedCtx, _ := tracer.Start(requestCtx, "Query.greetings")

	for _, tc := range []struct {
		name     string
		nested   bool
		spanName string
		field    string
		folds    bool
	}{
		{name: "serveModule", spanName: "Query.serveModule", field: "serveModule", folds: true},
		{name: "engine-private call", spanName: "Module._implementationScoped", field: "_implementationScoped", folds: true},
		{name: "body call", spanName: "Query.greetings", field: "greetings"},
		{name: "nested engine-private call", nested: true, spanName: "Module._implementationScoped", field: "_implementationScoped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := requestCtx
			if tc.nested {
				ctx = nestedCtx
			}
			parent, ok := moduleProcessPlumbingParent(ctx, tc.spanName, tc.field)
			require.Equal(t, tc.folds, ok)
			if tc.folds {
				require.Equal(t, plumbing.SpanContext(), parent)
			}
		})
	}

	_, ok := moduleProcessPlumbingParent(t.Context(), "Query.serveModule", "serveModule")
	require.False(t, ok, "a request from any other client keeps its parent")
}
