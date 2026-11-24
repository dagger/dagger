package core

import "context"

type engineWorkKey struct{}

// WithEngineWork marks ctx as the engine's own work rather than a client's,
// such as an SDK building a module's runtime. A module function called with
// it runs as engine work: the calls its module makes are internal in
// telemetry, and so are those of the modules it calls in turn and of the
// processes it starts.
//
// The engine's internal selects in general don't carry it: some of them,
// such as running a check's body, call a user's own function.
func WithEngineWork(ctx context.Context) context.Context {
	return context.WithValue(ctx, engineWorkKey{}, true)
}

// IsEngineWork reports whether ctx is the engine's own work (WithEngineWork).
func IsEngineWork(ctx context.Context) bool {
	work, _ := ctx.Value(engineWorkKey{}).(bool)
	return work
}
