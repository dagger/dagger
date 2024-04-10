package util

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func HostDockerDir() (string, error) {
	if runtime.GOOS != "linux" {
		// doesn't work on darwin, untested on windows
		return "", fmt.Errorf("cannot get docker dir on %s", runtime.GOOS)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".docker")
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}
