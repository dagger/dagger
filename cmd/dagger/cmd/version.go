package cmd

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	goVersion "github.com/hashicorp/go-version"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/version"
	"golang.org/x/term"
)

const (
	versionFile = "~/.config/dagger/version-check"
	versionURL  = "https://releases.dagger.io/dagger/latest_version"
)

var versionMessage = ""

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print dagger version",
	// Disable version hook here to avoid double version check
	PersistentPreRun:  func(*cobra.Command, []string) {},
	PersistentPostRun: func(*cobra.Command, []string) {},
	Args:              cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("dagger %s (%s) %s/%s\n",
			version.Version,
			version.Revision,
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
	currentVersion, err := goVersion.NewVersion(version.Version)
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
	if version.Version == version.DevelopmentVersion {
		// running devel version
		return
	}

	versionFilePath, err := homedir.Expand(versionFile)
	if err != nil {
		panic(err)
	}
	baseDir := path.Dir(versionFilePath)

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		if err := os.MkdirAll(baseDir, 0o700); err != nil {
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
	ioutil.WriteFile(path.Join(versionFilePath), []byte(now), 0o600)
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
