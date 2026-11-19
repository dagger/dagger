package main

import "context"

type Gprobe struct{}

// Greeting calls into the probe dependency, so the call's nested client
// serves this module and loads its dependency's schema.
func (m *Gprobe) Greeting(ctx context.Context, name string) (string, error) {
	return dag.Probe().Greeting(ctx, name)
}
