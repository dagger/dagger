package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) GetUser(ctx context.Context, gender string) (string, error) {
	return dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"apk", "add", "jq"}).
		WithExec([]string{"sh", "-c", fmt.Sprintf("curl https://randomuser.me/api/?gender=%s | jq .results[0].name", gender)}).
		Stdout(ctx)
}
