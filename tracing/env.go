package tracing

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/propagation"
)

func PropagationEnv(ctx context.Context) []string {
	tc := propagation.TraceContext{}
	carrier := propagation.MapCarrier{}
	tc.Inject(ctx, carrier)
	env := []string{}
	for _, f := range tc.Fields() {
		if val, ok := carrier[f]; ok {
			// traceparent vs. TRACEPARENT matters
			env = append(env, strings.ToUpper(f)+"="+val)
		}
	}
	return env
}
