package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteEngineFunctions(t *testing.T) {
	daggerBin := "dagger" // $PATH
	if bin := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"); bin != "" {
		daggerBin = bin
	}
	daggerArgs := []string{"--cloud", "functions"}
	cmd := exec.Command(daggerBin, daggerArgs...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	err := cmd.Run()
	require.Error(t, err, "expected to return an error")
	require.Contains(t, stderr.String(), "please run `dagger login <org>` first")
}
