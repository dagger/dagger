package main

import "context"

type Echo struct{}

// Say returns the greeting.
func (e *Echo) Say(ctx context.Context, msg string) string {
	return "hello " + msg
}
