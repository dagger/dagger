package main

import "context"

type Toplevel struct{}

func (t *Toplevel) SayHello(ctx context.Context) (string, error) {
	return dag.SSH().SayHello(ctx)
}
