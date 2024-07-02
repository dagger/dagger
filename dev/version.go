package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/dagger/dagger/dev/internal/dagger"
	"golang.org/x/mod/semver"
)

type VersionInfo struct {
	Tag    string
	Commit string
	Suffix string
	Dev    string
}

var commitRegexp = regexp.MustCompile("^[0-9a-f]{40}$")

func newVersion(ctx context.Context, dir *dagger.Directory, version string, suffix string) (*VersionInfo, error) {
	versionInfo := &VersionInfo{}
	if suffix != "" {
		versionInfo.Suffix = suffix
	}

	switch {
	// If no version is set, we are assuming that this is a local dev build
	case version == "":
		id, err := dir.ID(ctx)
		if err != nil {
			return nil, err
		}

		h := sha1.New() //nolint:gosec
		h.Write([]byte(id))
		dgst := hex.EncodeToString(h.Sum(nil))
		return &VersionInfo{Dev: dgst}, nil
	case semver.IsValid(version):
		versionInfo.Tag = version
	case version == "main":
		versionInfo.Tag = version
	case commitRegexp.MatchString(version):
		versionInfo.Commit = version
	default:
		return nil, fmt.Errorf("could not parse version info %q", version)
	}

	return versionInfo, nil
}

func (info *VersionInfo) String() string {
	value := ""

	// If this is dev, return early
	if info.Dev != "" {
		return "dev-" + info.Dev
	}

	// Try commit first
	if info.Commit != "" {
		value = info.Commit
	}
	// Use a tag instead of a commit if available
	if info.Tag != "" {
		value = info.Tag
	}

	if info.Suffix != "" {
		value += fmt.Sprintf("-%s", info.Suffix)
	}

	return value
}
