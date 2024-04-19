package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func DaggerCall(ctx context.Context, args ...string) error {
	binary := "dagger"
	if path, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_CLI_BIN"); ok {
		binary = path
	}

	cmd := exec.CommandContext(ctx, binary)
	cmd.Args = append(cmd.Args, "--progress=plain", "call", "--source=.")
	if path, err := hostDockerConfig(); err == nil {
		cmd.Args = append(cmd.Args, "--host-docker-config=file:"+path)
	}
	cmd.Args = append(cmd.Args, args...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Println(">", strings.Join(cmd.Args, " "))
	return cmd.Run()
}

func hostDockerConfig() (string, error) {
	if runtime.GOOS != "linux" {
		// doesn't work on darwin, untested on windows
		return "", fmt.Errorf("cannot get docker dir on %s", runtime.GOOS)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".docker/config.json")
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}
