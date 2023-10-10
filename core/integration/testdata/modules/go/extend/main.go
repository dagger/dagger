package main

import "context"

func (c *Container) Echo(ctx context.Context, msg string) (string, error) {
	return c.WithExec([]string{"echo", msg}).Stdout(ctx)
}
