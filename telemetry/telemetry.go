package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/google/uuid"
	"github.com/mitchellh/go-homedir"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/pkg"
	"go.dagger.io/dagger/version"
)

const (
	apiKey       = "cb9777c166aefe4b77b31f961508191c" //nolint
	telemetryURL = "https://t.dagger.io/v1"
)

type Property struct {
	Name  string
	Value interface{}
}

func TrackAsync(ctx context.Context, eventName string, properties ...*Property) chan struct{} {
	doneCh := make(chan struct{}, 1)
	go func() {
		defer close(doneCh)
		Track(ctx, eventName, properties...)
	}()
	return doneCh
}

func Track(ctx context.Context, eventName string, properties ...*Property) {
	lg := log.Ctx(ctx).
		With().
		Str("event", eventName).
		Logger()

	if telemetryDisabled() {
		return
	}

	repo := gitRepoURL(ctx, ".")

	// Base properties
	props := map[string]interface{}{
		"dagger_version":  version.Version,
		"dagger_revision": version.Revision,
		"os":              runtime.GOOS,
		"arch":            runtime.GOARCH,
	}

	if repo != "" {
		// Hash the repository URL for privacy
		props["git_repository_hash"] = hash(repo)
	}

	if projectDir, found := pkg.GetCueModParent(); found {
		// Hash the project path for privacy
		props["project_path_hash"] = hash(projectDir)
	}

	// Merge extra properties
	for _, p := range properties {
		props[p.Name] = p.Value
	}

	deviceID, err := getDeviceID(repo)
	if err != nil {
		lg.Trace().Err(err).Msg("failed to get device id")
		return
	}

	lg = lg.With().Str("device_id", deviceID).Fields(props).Logger()

	ev := &event{
		DeviceID:        deviceID,
		EventType:       eventName,
		Time:            time.Now().Unix(),
		AppVersion:      version.Version,
		OSName:          runtime.GOOS,
		Platform:        runtime.GOARCH,
		IP:              "$remote", // Use "$remote" to use the IP address on the upload request
		EventProperties: props,
	}

	p := &payload{
		APIKey: apiKey,
		Events: []*event{ev},
	}

	b := new(bytes.Buffer)
	if err := json.NewEncoder(b).Encode(p); err != nil {
		lg.Trace().Err(err).Msg("failed to encode payload")
		return
	}

	req, err := http.NewRequest("POST", telemetryURL, b)
	if err != nil {
		lg.Trace().Err(err).Msg("failed to prepare request")
	}

	req.Header = map[string][]string{
		"Content-Type": {"application/json"},
		"Accept":       {"*/*"},
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		lg.Trace().Err(err).Msg("failed to send telemetry event")
		return
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		lg.Trace().Str("status", resp.Status).Msg("telemetry request failed")
		return
	}

	lg.Trace().Dur("duration", time.Since(start)).Msg("telemetry event")
}

type payload struct {
	APIKey string   `json:"api_key,omitempty"`
	Events []*event `json:"events"`
}

type event struct {
	UserID          string                 `json:"user_id,omitempty"`
	DeviceID        string                 `json:"device_id,omitempty"`
	EventType       string                 `json:"event_type,omitempty"`
	Time            int64                  `json:"time,omitempty"`
	AppVersion      string                 `json:"app_version,omitempty"`
	Platform        string                 `json:"platform,omitempty"`
	OSName          string                 `json:"os_name,omitempty"`
	OSVersion       string                 `json:"os_version,omitempty"`
	IP              string                 `json:"ip,omitempty"`
	EventProperties map[string]interface{} `json:"event_properties,omitempty"`
}

func isCI() bool {
	return os.Getenv("CI") != "" || // GitHub Actions, Travis CI, CircleCI, Cirrus CI, GitLab CI, AppVeyor, CodeShip, dsari
		os.Getenv("BUILD_NUMBER") != "" || // Jenkins, TeamCity
		os.Getenv("RUN_ID") != "" // TaskCluster, dsari
}

func telemetryDisabled() bool {
	return os.Getenv("DAGGER_TELEMETRY_DISABLE") != "" || // dagger specific env
		os.Getenv("DO_NOT_TRACK") != "" // https://consoledonottrack.com/
}

func getDeviceID(repo string) (string, error) {
	if isCI() {
		if repo == "" {
			return "", fmt.Errorf("unable to determine device ID")
		}
		return "ci-" + hash(repo), nil
	}
	idFile, err := homedir.Expand("~/.config/dagger/cli_id")
	if err != nil {
		return "", err
	}
	id, err := os.ReadFile(idFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		if err := os.MkdirAll(filepath.Dir(idFile), 0755); err != nil {
			return "", err
		}

		id = []byte(uuid.New().String())
		if err := os.WriteFile(idFile, id, 0600); err != nil {
			return "", err
		}
	}
	return string(id), nil
}

// hash returns the sha256 digest of the string
func hash(s string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))
}

// gitRepoURL returns the git repository remote, if any.
func gitRepoURL(ctx context.Context, path string) string {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return ""
	}

	origin, err := repo.Remote("origin")
	if err != nil {
		return ""
	}

	urls := origin.Config().URLs
	if len(urls) == 0 {
		return ""
	}

	endpoint, err := parseGitURL(urls[0])
	if err != nil {
		log.Ctx(ctx).Debug().Err(err).Str("url", urls[0]).Msg("failed to parse git URL")
		return ""
	}

	return endpoint
}
