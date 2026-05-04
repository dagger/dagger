package main

import "context"

type Echo struct{}

// Fire does a thing and returns nothing but an error.
func (e *Echo) Fire(ctx context.Context) error {
	return nil
}

// Emit does a thing with no return at all.
func (e *Echo) Emit() {}
