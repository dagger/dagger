package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func daggerCloudWithEnv(t *testing.T, env []string, args []string, testCommandFn func(*testing.T, error, *bytes.Buffer, *bytes.Buffer)) {
	daggerBin := "dagger" // $PATH
	if bin := os.Getenv("_EXPERIMENTAL_DAGGER_CLI_BIN"); bin != "" {
		daggerBin = bin
	}
	cmd := exec.Command(daggerBin, args...)

	cmd.Env = env
	cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH"), "HOME="+os.Getenv("HOME"))

	stdout := &bytes.Buffer{}
	cmd.Stdout = stdout

	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	err := cmd.Run()

	testCommandFn(t, err, stdout, stderr)
}

func TestCloudEngineUnauth(t *testing.T) {
	env := []string{}
	args := []string{"--cloud", "functions"}
	daggerCloudWithEnv(t, env, args, func(t *testing.T, err error, stdout *bytes.Buffer, stderr *bytes.Buffer) {
		require.Error(t, err, fmt.Sprintf(
			"expected '%s dagger %s' to return an error, but instead: %s",
			strings.Join(env, " "),
			strings.Join(args, " "),
			stdout.String()))
		require.Contains(t, stderr.String(), "please run `dagger login <org>` first")
	})
}

func TestCloudEngineWithCloudToken(t *testing.T) {
	// This token MUST be of type `Engine`
	daggerCloudToken := os.Getenv("DAGGER_CLOUD_TOKEN_ENGINE")

	if daggerCloudToken == "" {
		t.Skip("DAGGER_CLOUD_TOKEN_ENGINE not set")
	}

	env := []string{"DAGGER_CLOUD_TOKEN=" + daggerCloudToken}
	args := []string{"--cloud", "--mod", "github.com/gerhard/daggerverse/sysi@sysi/v0.1.0", "functions"}

	daggerCloudWithEnv(t, env, args, func(t *testing.T, err error, stdout *bytes.Buffer, stderr *bytes.Buffer) {
		require.NoError(t, err, fmt.Sprintf(
			"expected '%s dagger %s' to succeed, but instead: %s",
			strings.Join(env, " "),
			strings.Join(args, " "),
			stderr.String()))
		require.Contains(t, stdout.String(), "https://github.com/fastfetch-cli/fastfetch")
		require.Contains(t, stderr.String(), "dagger call fastfetch")
	})
}

func TestCloudEngineEnvWithCloudToken(t *testing.T) {
	// This token MUST be of type `Engine`
	daggerCloudToken := os.Getenv("DAGGER_CLOUD_TOKEN_ENGINE")

	if daggerCloudToken == "" {
		t.Skip("DAGGER_CLOUD_TOKEN_ENGINE not set")
	}

	env := []string{"DAGGER_CLOUD_TOKEN=" + daggerCloudToken, "DAGGER_CLOUD_ENGINE=true"}
	args := []string{"--mod", "github.com/gerhard/daggerverse/sysi@sysi/v0.1.0", "functions"}

	daggerCloudWithEnv(t, env, args, func(t *testing.T, err error, stdout *bytes.Buffer, stderr *bytes.Buffer) {
		require.NoError(t, err, fmt.Sprintf(
			"expected '%s dagger %s' to succeed, but instead: %s",
			strings.Join(env, " "),
			strings.Join(args, " "),
			stderr.String()))
		require.Contains(t, stdout.String(), "https://github.com/fastfetch-cli/fastfetch")
		require.Contains(t, stderr.String(), "dagger call fastfetch")
	})
}
