package main

import (
	"context"
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/dev/internal/dagger"
	"golang.org/x/mod/semver"
)

// VersionInfo is a helper for passing version information around.
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers

type VersionInfo struct {
	Tag string

	NextVersion string
	Timestamp   string
	Commit      string
	Dev         string
}

var commitRegexp = regexp.MustCompile("^[0-9a-f]{40}$")

func newVersion(ctx context.Context, dir *dagger.Directory, version string) (*VersionInfo, error) {
	switch {
	case version == "":
		dgst, err := dirhash(ctx, dir)
		if err != nil {
			return nil, err
		}
		next, err := nextVersion(ctx, dir)
		if err != nil {
			return nil, err
		}
		return &VersionInfo{
			Dev:         dgst,
			NextVersion: next,
			Timestamp:   pseudoversionTimestamp(time.Time{}),
		}, nil
	case commitRegexp.MatchString(version):
		next, err := nextVersion(ctx, dir)
		if err != nil {
			return nil, err
		}
		return &VersionInfo{
			Commit:      version,
			NextVersion: next,
			Timestamp:   pseudoversionTimestamp(time.Now().UTC()),
		}, nil
	case semver.IsValid(version):
		return &VersionInfo{Tag: version}, nil
	default:
		return nil, fmt.Errorf("could not parse version info %q", version)
	}
}

func (info *VersionInfo) String() string {
	if info.Tag != "" {
		return info.Tag
	}

	nextVersion := info.NextVersion
	if nextVersion == "" {
		nextVersion = "v0.0.0"
	}
	timestamp := info.Timestamp
	if timestamp == "" {
		timestamp = pseudoversionTimestamp(time.Time{})
	}

	var rest string
	switch {
	case info.Commit != "":
		rest = info.Commit[:12]
	case info.Dev != "":
		rest = "dev-" + info.Dev[:12]
	default:
		rest = "dev-" + strings.Repeat("0", 12)
	}
	return fmt.Sprintf("%s-%s-%s", nextVersion, timestamp, rest)
}

// nextVersion determines the "next" version to be released.
//
// It first attempts to use the version in .changes/.next, but if this fails,
// or that version seems to have already been released, then we automagically
// calculate the next patch release in the current series.
func nextVersion(ctx context.Context, dir *dagger.Directory) (string, error) {
	// this is kinda meh, since it assumes changie releases match up with git
	// tags - thankfully this is true for us (otherwise, we'd have to look at
	// *all* the tags in the source, which would be slow)
	entries, err := dir.Directory(".changes").Entries(ctx)
	if err != nil {
		return "", err
	}

	// if there's a defined next version, try and use that
	var definedNextVersion string
	if slices.Contains(entries, ".next") {
		content, err := dir.File(".changes/.next").Contents(ctx)
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if len(line) == 0 {
				// empty
				continue
			}
			if strings.HasPrefix(line, "#") {
				// comment
				continue
			}

			definedNextVersion = baseVersion(line)
			break
		}
	}

	// also try and determine what the last version was, so we can
	// auto-determine a next version from that
	var lastVersion string
	for _, entry := range entries {
		entry, _ := strings.CutSuffix(entry, filepath.Ext(entry))
		if semver.Compare(entry, lastVersion) > 0 {
			lastVersion = entry
		}
	}
	if lastVersion == "" {
		return "", fmt.Errorf("could not find any valid versions")
	}
	lastVersion = baseVersion(lastVersion)

	majorMinor := semver.MajorMinor(lastVersion)
	patchStr, _ := strings.CutPrefix(lastVersion, majorMinor+".")
	patch, err := strconv.Atoi(patchStr)
	if err != nil {
		return "", err
	}
	nextVersion := fmt.Sprintf("%s.%d", majorMinor, patch+1)

	if semver.Compare(definedNextVersion, nextVersion) > 0 {
		// if the defined next version is larger than the auto-generated one,
		// override it - this'll be the case for when we plan to bump to a
		// minor version
		nextVersion = definedNextVersion
	}
	return nextVersion, nil
}

func dirhash(ctx context.Context, dir *dagger.Directory) (string, error) {
	id, err := dir.ID(ctx)
	if err != nil {
		return "", err
	}
	h := sha1.New() //nolint:gosec
	h.Write([]byte(id))
	dgst := hex.EncodeToString(h.Sum(nil))
	return dgst, nil
}

func pseudoversionTimestamp(t time.Time) string {
	// go time formatting is bizarre - this translates to "yymmddhhmmss"
	return t.Format("060102150405")
}

func baseVersion(version string) string {
	version = strings.TrimSuffix(version, semver.Build(version))
	version = strings.TrimSuffix(version, semver.Prerelease(version))
	return version
}
