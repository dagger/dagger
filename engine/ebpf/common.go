// Package ebpf provides eBPF-based tracing utilities for the Dagger engine.
package ebpf

import "context"

// Tracer is the common interface for all eBPF tracers.
type Tracer interface {
	// Run starts the tracer and blocks until the context is cancelled.
	// It should be called in a goroutine.
	Run(ctx context.Context)

	// Close releases all resources held by the tracer.
	Close() error
}
