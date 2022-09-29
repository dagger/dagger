package main

import (
	"context"
	"os"

	"go.dagger.io/dagger/sdk/go/dagger"
)

func (r *test) testMount(ctx context.Context, in dagger.FSID) (string, error) {
	bytes, err := os.ReadFile("/mnt/in/foo")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
