package cmd

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	goVersion "github.com/hashicorp/go-version"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/mod"
	"go.dagger.io/dagger/version"
	"golang.org/x/term"
)

const (
	versionFile     = "~/.config/dagger/version-check"
	versionURL      = "https://releases.dagger.io/dagger/latest_version"
	universeTagsURL = "https://api.github.com/repos/dagger/universe/tags"
)

var (
	daggerVersionMessage   = ""
	universeVersionMessage = ""
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print dagger and universe version",
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

		// TODO Display universe version
		// How can I retrieve it if it's not vendor ?

		if check := viper.GetBool("check"); check {
			versionFilePath, err := homedir.Expand(versionFile)
			if err != nil {
				panic(err)
			}

			_ = os.Remove(versionFilePath)
			checkVersion()
			if !warnDaggerVersion() {
				fmt.Println("dagger is up to date.")
			}
			if !warnUniverseVersion() {
				fmt.Println("universe is up to date.")
			}
		}
	},
}

func init() {
	versionCmd.Flags().Bool("check", false, "check if dagger and universe are up to date")

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

func getDaggerLatestVersion(currentVersion *goVersion.Version) (*goVersion.Version, error) {
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

// Compare dagger version with the latest release online
// Return the latest dagger version if current is outdated
func isDaggerVersionLatest() (string, error) {
	currentVersion, err := goVersion.NewVersion(version.Version)
	if err != nil {
		return "", err
	}

	latestVersion, err := getDaggerLatestVersion(currentVersion)
	if err != nil {
		return "", err
	}

	if currentVersion.LessThan(latestVersion) {
		return latestVersion.String(), nil
	}
	return "", nil
}

// Call https://api.github.com/repos/dagger/universe/tags
func listUniverseTags() ([]string, error) {
	req, err := http.NewRequest("GET", universeTagsURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tagsDTO []struct {
		Name string `json:"name"`
	}

	err = json.Unmarshal(data, &tagsDTO)
	if err != nil {
		return nil, err
	}

	// Reduce DTO to simple string array
	tags := []string{}
	for _, tag := range tagsDTO {
		tags = append(tags, tag.Name)
	}

	return tags, nil
}

func getUniverseLatestVersion() (*goVersion.Version, error) {
	tags, err := listUniverseTags()
	if err != nil {
		return nil, err
	}

	// Get latest available version
	constraint, err := goVersion.NewConstraint(mod.UniverseVersionConstraint)
	if err != nil {
		return nil, err
	}

	// Retrieve the latest supported universe version
	var versions []*goVersion.Version
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "v") {
			continue
		}

		v, err := goVersion.NewVersion(tag)
		if err != nil {
			continue
		}

		if constraint.Check(v) {
			versions = append(versions, v)
		}
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("universe repository has no version matching the required version")
	}

	sort.Sort(sort.Reverse(goVersion.Collection(versions)))
	return versions[0], nil
}

func getUniverseCurrentVersion() (*goVersion.Version, error) {
	// TODO Should be replaced with the current universe version
	// How I can fetch it
	return goVersion.NewVersion("0.1.0")
}

// Compare the universe version with the latest version online
// Return the latest universe version if the current is outdated
func isUniverseVersionLatest() (string, error) {
	currentVersion, err := getUniverseCurrentVersion()
	if err != nil {
		return "", err
	}

	latestVersion, err := getUniverseLatestVersion()
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
		if err := os.MkdirAll(baseDir, 0700); err != nil {
			// mkdir fails, ignore silently
			return
		}
	}

	if !isCheckOutdated(versionFilePath) {
		return
	}

	// Check version
	universeLatestVersion, err := isUniverseVersionLatest()
	if err != nil {
		return
	}

	if universeLatestVersion != "" {
		universeVersionMessage = fmt.Sprintf("A new version of universe is available (%s), please run 'dagger mod get github.com/dagger/universe/stdlib'", universeLatestVersion)
	}

	// Check timestamp
	daggerLatestVersion, err := isDaggerVersionLatest()
	if err != nil {
		return
	}

	if daggerLatestVersion != "" {
		daggerVersionMessage = fmt.Sprintf("\nA new version of dagger is available (%s), please go to https://github.com/dagger/dagger/doc/install.md for instructions.", daggerLatestVersion)
	}

	// Update check timestamps file
	now := time.Now().Format(time.RFC3339)
	ioutil.WriteFile(path.Join(versionFilePath), []byte(now), 0600)
}

func warnDaggerVersion() bool {
	if daggerVersionMessage == "" {
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
	fmt.Println(daggerVersionMessage)
	return true
}

func warnUniverseVersion() bool {
	if universeVersionMessage == "" {
		return false
	}

	fmt.Println(universeVersionMessage)
	return true
}
