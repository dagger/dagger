package main

import (
	"context"
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/dagger/dagger/ci/internal/dagger"
	"golang.org/x/mod/semver"
)

type VersionInfo struct {
	Tag    string
	Commit string
	Dev    string
}

var commitRegexp = regexp.MustCompile("^[0-9a-f]{40}$")

func newVersion(ctx context.Context, dir *dagger.Directory, version string) (*VersionInfo, error) {
	switch {
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
		return &VersionInfo{Tag: version}, nil
	case commitRegexp.MatchString(version):
		return &VersionInfo{Commit: version}, nil
	default:
		return nil, fmt.Errorf("could not parse version info %q", version)
	}
}

func (info *VersionInfo) String() string {
	if info.Tag != "" {
		return info.Tag
	}
	if info.Commit != "" {
		return info.Commit
	}
	if info.Dev != "" {
		return "dev-" + info.Dev
	}
	return "dev-unknown"
}
