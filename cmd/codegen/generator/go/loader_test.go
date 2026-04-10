package gogenerator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A broken package must fail type-def loading immediately, even when Go can
// still return a package node with partial syntax/type information.
func TestLoadPackageFailsOnPackageErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.23.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "helper.go"), []byte(`package main

type Helper struct{}
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

type Broken struct {
	Field MissingType
}
`), 0o600))

	_, _, err := loadPackage(context.Background(), dir, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "MissingType")
}
