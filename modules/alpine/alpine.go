package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	goapk "chainguard.dev/apko/pkg/apk/apk"
)

const (
	alpineRepository  = "https://dl-cdn.alpinelinux.org/alpine"
	alpineReleasesURL = "https://alpinelinux.org/releases.json"
)

func alpineReleases() (*goapk.Releases, error) {
	res, err := http.Get(alpineReleasesURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get alpine releases: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unable to get alpine releases at %s: %v", alpineReleasesURL, res.Status)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read alpine releases: %w", err)
	}
	var releases goapk.Releases
	if err := json.Unmarshal(b, &releases); err != nil {
		return nil, fmt.Errorf("failed to unmarshal alpine releases: %w", err)
	}

	return &releases, nil
}

func alpineRepositories(branch goapk.ReleaseBranch) []string {
	mainRepo := fmt.Sprintf("%s/%s/%s", alpineRepository, branch.ReleaseBranch, "main")
	communityRepo := fmt.Sprintf("%s/%s/%s", alpineRepository, branch.ReleaseBranch, "community")
	return []string{mainRepo, communityRepo}
}
