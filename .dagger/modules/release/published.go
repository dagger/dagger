package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"dagger/release/internal/dagger"
)

// When a package registry already has the exact version a release is about
// to upload, count that component as published instead of uploading it
// again. Registries reject re-uploads, so without this, rerunning a release
// that failed partway fails again on every component an earlier attempt
// already published. Set to false to report those components as failed
// instead.
const treatAlreadyPublishedAsDone = true

const alreadyPublishedNote = "already published, not uploaded again"

// pypiVersionURL returns the PyPI JSON API URL for a dagger-io version.
func pypiVersionURL(pypiRepo, pypiURL, version string) (string, error) {
	base := "https://pypi.org"
	switch {
	case pypiURL != "":
		u, err := url.Parse(pypiURL)
		if err != nil {
			return "", err
		}
		base = u.Scheme + "://" + u.Host
	case pypiRepo == "test":
		base = "https://test.pypi.org"
	}
	return base + "/pypi/dagger-io/" + pep440Version(version) + "/json", nil
}

var pep440PrereleaseRe = regexp.MustCompile(`^(\d+\.\d+\.\d+)-(alpha|beta|rc)\.?(\d+)$`)

// pep440Version converts a semver prerelease like 1.0.0-beta.17 to the
// normalized form Python packaging publishes it as, 1.0.0b17.
func pep440Version(version string) string {
	m := pep440PrereleaseRe.FindStringSubmatch(version)
	if m == nil {
		return version
	}
	return m[1] + map[string]string{"alpha": "a", "beta": "b", "rc": "rc"}[m[2]] + m[3]
}

// npmVersionURL returns the registry URL for an @dagger.io/dagger version.
func npmVersionURL(npmRegistryURL, version string) string {
	base := strings.TrimSuffix(npmRegistryURL, "/")
	if base == "" {
		base = "https://registry.npmjs.org"
	}
	return base + "/@dagger.io%2fdagger/" + version
}

// hexVersionURL returns the Hex API URL for a dagger package version.
func hexVersionURL(hexAPIURL, version string) string {
	base := strings.TrimSuffix(hexAPIURL, "/")
	if base == "" {
		base = "https://hex.pm/api"
	}
	return base + "/packages/dagger/releases/" + version
}

// registryHasVersion reports whether a registry API URL answers 200 (the
// version exists) or 404 (it doesn't). Any other answer is an error.
func registryHasVersion(ctx context.Context, versionURL string) (bool, error) {
	status, _, err := registryGet(ctx, versionURL)
	if err != nil {
		return false, err
	}
	switch status {
	case "200":
		return true, nil
	case "404":
		return false, nil
	}
	return false, fmt.Errorf("GET %s: unexpected status %s", versionURL, status)
}

// crateHasVersion reports whether the dagger-sdk crate's entry in a sparse
// Cargo index lists version.
func crateHasVersion(ctx context.Context, cargoRegistryIndex, version string) (bool, error) {
	index := strings.TrimPrefix(cargoRegistryIndex, "sparse+")
	if index == "" {
		index = "https://index.crates.io/"
	}
	entryURL := strings.TrimSuffix(index, "/") + "/da/gg/dagger-sdk"
	status, body, err := registryGet(ctx, entryURL)
	if err != nil {
		return false, err
	}
	switch status {
	case "404":
		return false, nil
	case "200":
	default:
		return false, fmt.Errorf("GET %s: unexpected status %s", entryURL, status)
	}
	for _, line := range strings.Split(body, "\n") {
		var entry struct {
			Vers string `json:"vers"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Vers == version {
			return true, nil
		}
	}
	return false, nil
}

// registryGet fetches url and returns the HTTP status and body.
func registryGet(ctx context.Context, rawURL string) (status, body string, rerr error) {
	out, err := dag.Wolfi().
		Container(dagger.WolfiContainerOpts{Packages: []string{"curl", "ca-certificates"}}).
		WithEnvVariable("REGISTRY_URL", rawURL).
		// Ask the registry every time; never reuse an earlier answer.
		WithEnvVariable("CACHE_BUSTER", time.Now().Format(time.RFC3339Nano)).
		WithExec([]string{"sh", "-c", `curl -sS -o /tmp/body -w '%{http_code}' "$REGISTRY_URL" > /tmp/status && cat /tmp/status && echo && cat /tmp/body`}).
		Stdout(ctx)
	if err != nil {
		return "", "", fmt.Errorf("GET %s: %w", rawURL, err)
	}
	status, body, _ = strings.Cut(out, "\n")
	return strings.TrimSpace(status), body, nil
}
