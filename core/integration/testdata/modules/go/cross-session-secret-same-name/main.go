package main

import "context"

type Caller struct{}

func (*Caller) Fn(ctx context.Context, val string) (string, error) {
	return dag.Secreter().GiveBack(dag.SetSecret("FOO", val)).Plaintext(ctx)
}
