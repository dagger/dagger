package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/engine"
)

const (
	versionURL = "https://dl.dagger.io/dagger/latest_version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print dagger version",
	// Disable version hook here to avoid double version check
	PersistentPreRun: func(*cobra.Command, []string) {},
	Args:             cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), long())
	},
}

func short() string {
	return fmt.Sprintf("dagger %s (%s:%s)", engine.Version, engine.EngineImageRepo, engine.Tag)
}

func long() string {
	return fmt.Sprintf("%s %s/%s", short(), runtime.GOOS, runtime.GOARCH)
}

func updateAvailable(ctx context.Context) (string, error) {
	if engine.IsDevVersion(engine.Version) {
		return "", nil
	}

	latest, err := latestVersion(ctx)
	if err != nil {
		return "", err
	}

	// Update is available
	if semver.Compare(engine.Version, latest) < 0 {
		return latest, nil
	}

	return "", nil
}

func latestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequest("GET", versionURL, nil)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)

	req.Header.Set(
		"User-Agent",
		fmt.Sprintf("dagger/%s (%s; %s)", engine.Version, runtime.GOOS, runtime.GOARCH),
	)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	latestVersion := strings.TrimSuffix(string(data), "\n")
	return latestVersion, nil
}
