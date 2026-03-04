// This is an extremely minimal subset of github.com/dagger/otel-go so we can
// avoid depending on everything else. Critically we have to avoid pre-1.0
// dependencies like the OTel logging SDK.
package engineconn

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel/propagation"
)

var propagator = propagation.NewCompositeTextMapPropagator(
	propagation.Baggage{},
	propagation.TraceContext{},
)

func propagationEnv(ctx context.Context) []string {
	carrier := newEnvCarrier(false)
	propagator.Inject(ctx, carrier)
	return carrier.Env
}

type envCarrier struct {
	System bool
	Env    []string
}

func newEnvCarrier(system bool) *envCarrier {
	return &envCarrier{
		System: system,
	}
}

var _ propagation.TextMapCarrier = (*envCarrier)(nil)

func (c *envCarrier) Get(key string) string {
	envName := strings.ToUpper(key)
	for _, env := range c.Env {
		env, val, ok := strings.Cut(env, "=")
		if ok && env == envName {
			return val
		}
	}
	if c.System {
		if envVal := os.Getenv(envName); envVal != "" {
			return envVal
		}
	}
	return ""
}

func (c *envCarrier) Set(key, val string) {
	c.Env = append(c.Env, strings.ToUpper(key)+"="+val)
}

func (c *envCarrier) Keys() []string {
	keys := make([]string, 0, len(c.Env))
	for _, env := range c.Env {
		env, _, ok := strings.Cut(env, "=")
		if ok {
			keys = append(keys, env)
		}
	}
	return keys
}
