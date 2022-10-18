package version

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime/debug"
	"strings"
)

const lenCommitHash = 9

var (
	ErrNoBuildInfo = errors.New("unable to read build info")
	ErrParseCommit = errors.New("unable to read HEAD commit hash")
)

// Revision returns the VCS revision being used to build or empty string
func Revision() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ErrNoBuildInfo
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value[:lenCommitHash], nil
		}
	}
	return "", nil
}

// Revision returns the VCS revision being used to build or imported buildkit version
func GetBuildInfo() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ErrNoBuildInfo
	}

	for _, d := range bi.Deps {
		if d.Path == "github.com/moby/buildkit" {
			return d.Version, nil
		}
	}
	return "", nil
}

// Workaround the fact that debug.ReadBuildInfo doesn't work in tests:
// https://github.com/golang/go/issues/33976
func GetGoMod() (string, error) {
	out, err := exec.Command("go", "list", "-m", "github.com/moby/buildkit").CombinedOutput()
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(out))
	_, version, ok := strings.Cut(trimmed, " ")
	if !ok {
		return "", fmt.Errorf("unexpected go list output: %s", trimmed)
	}
	return version, nil
}

// Workaround the fact that debug.ReadBuildInfo doesn't work in tests:
// https://github.com/golang/go/issues/33976
// func GetCommitHash() (string, error) {
// 	hash, err := exec.Command("git", "rev-parse", "--short", "HEAD").CombinedOutput()
// 	if err != nil {
// 		return "", err
// 	}
// 	trimmed := strings.TrimSpace(string(hash))
// 	return trimmed, nil
// }
