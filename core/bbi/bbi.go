package bbi

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

// Start a new BBI session
func NewSession(self dagql.Object, srv *dagql.Server) Session {
	// FIXME: for now we always use the "flat" BBI driver
	flat, ok := drivers["flat"]
	if !ok {
		panic("BBI driver 'flat' not registered")
	}
	return flat.NewSession(self, srv)
}

// BBI stands for "Body-Brain Interface".
// A BBI implements a strategy for mapping a Dagger object's API to LLM function calls
// The perfect BBI has not yet been designed, so there are multiple BBI implementations,
// and an interface for easily swapping them out.
// Hopefully in the future the perfect BBI design will emerge, and we can retire
// the pluggable interface.
type Driver interface {
	NewSession(dagql.Object, *dagql.Server) Session
}

var drivers = make(map[string]Driver)

func Register(name string, driver Driver) {
	drivers[name] = driver
}

// A stateful BBI session
type Session interface {
	// Return a set of tools for the next llm loop
	// The tools may modify the state without worrying about synchronization:
	// it's the agent's responsibility to not call tools concurrently.
	Tools() []Tool
	Self() dagql.Object
}

// A frontend for LLM tool calling
type Tool struct {
	Name        string
	Description string
	Schema      map[string]interface{}
	Call        func(context.Context, interface{}) (interface{}, error)
}
