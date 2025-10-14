package main

import (
	"context"
	"dagger/test/internal/dagger"
	"fmt"
	"slices"
)

type Test struct{}

func (m *Test) CheckFoo() dagger.CheckStatus {
	return dagger.CheckStatusCompleted
}

func (m *Test) CheckFiles(
	ctx context.Context,
	// +defaultPath="."
	dir *dagger.Directory,
) (dagger.CheckStatus, error) {
	entries, err := dir.Entries(ctx)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no files found in the directory")
	}
	if !slices.Contains(entries, "dagger.json") {
		return "", fmt.Errorf("dagger.json not found in the directory")
	}
	return "", nil
}

/*
func (m *Test) CheckSecret(
	ctx context.Context,
	s *dagger.Secret,
) (string, error) {
	plaintext, err := s.Plaintext(ctx)
	if err != nil {
		return "", err
	}
	return string(plaintext[0]) + "!" + plaintext[1:], nil
}
*/
