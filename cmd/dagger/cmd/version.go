package cmd

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	goVersion "github.com/hashicorp/go-version"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

const (
	defaultVersion = "devel"
	versionFile    = "~/.config/dagger/version-check"
	versionURL     = "https://releases.dagger.io/dagger/latest_version"
)

// set by goreleaser or other builder using
// -ldflags='-X go.dagger.io/dagger/cmd/dagger/cmd.version=<version>'
var (
	version        = defaultVersion
	versionMessage = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print dagger version",
	// Disable version hook here to avoid double version check
	PersistentPreRun:  func(*cobra.Command, []string) {},
	PersistentPostRun: func(*cobra.Command, []string) {},
	Args:              cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if bi, ok := debug.ReadBuildInfo(); ok && version == defaultVersion {
			// No specific version provided via version
			version = bi.Main.Version
		}
		fmt.Printf("dagger version %v %s/%s\n",
			version,
			runtime.GOOS, runtime.GOARCH,
		)

		if check := viper.GetBool("check"); check {
			versionFilePath, err := homedir.Expand(versionFile)
			if err != nil {
				panic(err)
			}

			_ = os.Remove(versionFilePath)
			checkVersion()
			if !warnVersion() {
				fmt.Println("dagger is up to date.")
			}
		}
	},
}

func init() {
	versionCmd.Flags().Bool("check", false, "check if dagger is up to date")

	versionCmd.InheritedFlags().MarkHidden("environment")
	versionCmd.InheritedFlags().MarkHidden("log-level")
	versionCmd.InheritedFlags().MarkHidden("log-format")

	if err := viper.BindPFlags(versionCmd.Flags()); err != nil {
		panic(err)
	}
}

func isCheckOutdated(path string) bool {
	// Ignore if not in terminal
	if !term.IsTerminal(int(os.Stdout.Fd())) || !term.IsTerminal(int(os.Stderr.Fd())) {
		return false
	}

	// Ignore if CI
	if os.Getenv("CI") != "" || os.Getenv("BUILD_NUMBER") != "" || os.Getenv("RUN_ID") != "" {
		return false
	}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return true
	}
	lastCheck, err := time.Parse(time.RFC3339, string(data))
	if err != nil {
		return true
	}
	nextCheck := lastCheck.Add(24 * time.Hour)
	return !time.Now().Before(nextCheck)
}

func getCurrentVersion() (*goVersion.Version, error) {
	if version != defaultVersion {
		return goVersion.NewVersion(version)
	}

	if build, ok := debug.ReadBuildInfo(); ok {
		// Also return error if version == (devel)
		return goVersion.NewVersion(build.Main.Version)
	}
	return nil, errors.New("could not read dagger version")
}

func getLatestVersion(currentVersion *goVersion.Version) (*goVersion.Version, error) {
	req, err := http.NewRequest("GET", versionURL, nil)
	if err != nil {
		return nil, err
	}

	// dagger/<version> (<OS>; <ARCH>)
	agent := fmt.Sprintf("dagger/%s (%s; %s)", currentVersion.String(), runtime.GOOS, runtime.GOARCH)
	req.Header.Set("User-Agent", agent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimSuffix(string(data), "\n")
	return goVersion.NewVersion(latestVersion)
}

// Compare the binary version with the latest version online
// Return the latest version if current is outdated
func isVersionLatest() (string, error) {
	currentVersion, err := getCurrentVersion()
	if err != nil {
		return "", err
	}

	latestVersion, err := getLatestVersion(currentVersion)
	if err != nil {
		return "", err
	}

	if currentVersion.LessThan(latestVersion) {
		return latestVersion.String(), nil
	}
	return "", nil
}

func checkVersion() {
	if version == defaultVersion {
		// running devel version
		return
	}

	versionFilePath, err := homedir.Expand(versionFile)
	if err != nil {
		panic(err)
	}
	baseDir := path.Dir(versionFilePath)

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		if err := os.MkdirAll(baseDir, 0700); err != nil {
			// mkdir fails, ignore silently
			return
		}
	}

	if !isCheckOutdated(versionFilePath) {
		return
	}

	// Check timestamp
	latestVersion, err := isVersionLatest()
	if err != nil {
		return
	}

	if latestVersion != "" {
		versionMessage = fmt.Sprintf("\nA new version is available (%s), please go to https://github.com/dagger/dagger/doc/install.md for instructions.", latestVersion)
	}

	// Update check timestamps file
	now := time.Now().Format(time.RFC3339)
	ioutil.WriteFile(path.Join(versionFilePath), []byte(now), 0600)
}

func warnVersion() bool {
	if versionMessage == "" {
		return false
	}

	if binPath, err := os.Executable(); err == nil {
		if p, err := os.Readlink(binPath); err == nil {
			// Homebrew detected, print custom message
			if strings.Contains(p, "/Cellar/") {
				fmt.Println("\nA new version is available, please run:\n\nbrew update && brew upgrade dagger")
				return true
			}
		}
	}

	// Print default message
	fmt.Println(versionMessage)
	return true
}
