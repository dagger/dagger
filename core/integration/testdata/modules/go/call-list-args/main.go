package main

import (
	"context"
	"dagger/minimal/internal/dagger"
	"strings"
)

type Minimal struct{}

func (m *Minimal) Hello(msgs []string) string {
	return strings.Join(msgs, "+")
}

func (m *Minimal) Reads(ctx context.Context, files []dagger.File) (string, error) {
	var contents []string
	for _, f := range files {
		content, err := f.Contents(ctx)
		if err != nil {
			return "", err
		}
		contents = append(contents, content)
	}
	return strings.Join(contents, "+"), nil
}
