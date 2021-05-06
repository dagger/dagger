package cmd

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	goVersion "github.com/hashicorp/go-version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	defaultVersion  = "devel"
	outdatedMessage = "dagger binary is outdated, go to https://github.com/dagger/dagger/doc/update.md to update dagger."
)

// set by goreleaser or other builder using
// -ldflags='-X dagger.io/go/cmd/dagger/cmd.version=<version>'
var (
	version        = defaultVersion
	versionMessage = ""
)

// Disable version hook here
// It can lead to a double check if --check flag is enable
var versionCmd = &cobra.Command{
	Use:     "version",
	Short:   "Print dagger version",
	PreRun:  nil,
	PostRun: nil,
	Args:    cobra.NoArgs,
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
			upToDate, err := isVersionLatest()
			if err != nil {
				fmt.Println("error: could not check version.")
				return
			}

			if !upToDate {
				fmt.Println(outdatedMessage)
			} else {
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

func getVersion() (*goVersion.Version, error) {
	if version != defaultVersion {
		return goVersion.NewVersion(version)
	}

	if build, ok := debug.ReadBuildInfo(); ok {
		return goVersion.NewVersion(build.Main.Version)
	}
	return nil, errors.New("could not read dagger version")
}

func getOnlineVersion(currentVersion *goVersion.Version) (*goVersion.Version, error) {
	req, err := http.NewRequest("GET", "https://releases.dagger.io/dagger/latest_version", nil)
	if err != nil {
		return nil, err
	}

	// dagger/<version> (<OS>; <ARCH>)
	agent := fmt.Sprintf("dagger/%s (%s; %s)", currentVersion.String(), runtime.GOOS, runtime.GOARCH)
	req.Header.Set("User-Agent", agent)
	req.Header.Set("X-Dagger-Version", currentVersion.String())

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
func isVersionLatest() (bool, error) {
	currentVersion, err := getVersion()
	if err != nil {
		return false, err
	}

	latestVersion, err := getOnlineVersion(currentVersion)
	if err != nil {
		return false, err
	}

	if currentVersion.LessThan(latestVersion) {
		return false, nil
	}
	return true, nil
}

func checkVersion() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	daggerDirectory := home + "/.dagger"
	if folder, err := os.Stat(daggerDirectory); !os.IsNotExist(err) {
		if !folder.IsDir() {
			return
		}

		if !isCheckOutdated(daggerDirectory + "/version_check.txt") {
			return
		}

		// Check timestamp
		upToDate, err := isVersionLatest()
		if err != nil {
			return
		}

		if !upToDate {
			versionMessage = outdatedMessage
		}

		// Update check timestamps file
		now := time.Now().Format(time.RFC3339)
		ioutil.WriteFile(daggerDirectory+"/version_check.txt", []byte(now), 0600)
	}
}

func warnVersion() {
	if versionMessage != "" {
		fmt.Println(versionMessage)
	}
}
