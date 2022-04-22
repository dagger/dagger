package mod

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/rs/zerolog/log"
)

type RepoSource interface {
	download(ctx context.Context, req *Require, tmpPath string) error
}

func upgradeToLatestVersion(ctx context.Context, repo string, versions []string, existingVersion string, versionConstraint string) (string, error) {
	// checkout the latest tag
	latestTag, err := latestVersion(ctx, repo, versions, versionConstraint)
	if err != nil {
		return "", err
	}

	c, err := compareVersions(latestTag, existingVersion)
	if err != nil {
		return "", err
	}

	if c < 0 {
		return "", fmt.Errorf("latest git tag is less than the current version")
	}

	return latestTag, nil
}

func latestVersion(ctx context.Context, repository string, versionsRaw []string, versionConstraint string) (string, error) {
	versionsRaw, err := filterVersions(ctx, repository, versionsRaw, versionConstraint)
	if err != nil {
		return "", err
	}

	versions := make([]*version.Version, len(versionsRaw))
	for i, raw := range versionsRaw {
		v, _ := version.NewVersion(raw)
		versions[i] = v
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("repo doesn't have any tags matching the required version")
	}

	sort.Sort(sort.Reverse(version.Collection(versions)))
	version := versions[0].Original()

	return version, nil
}

func filterVersions(ctx context.Context, repository string, versions []string, versionConstraint string) ([]string, error) {
	lg := log.Ctx(ctx).With().
		Str("repository", repository).
		Str("versionConstraint", versionConstraint).
		Logger()

	if versionConstraint == "" {
		versionConstraint = ">= 0"
	}

	constraint, err := version.NewConstraint(versionConstraint)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(versions))
	for _, tagV := range versions {
		if !strings.HasPrefix(tagV, "v") {
			lg.Debug().Str("tag", tagV).Msg("tag version ignored, wrong format")
			continue
		}

		v, err := version.NewVersion(tagV)
		if err != nil {
			lg.Debug().Str("tag", tagV).Err(err).Msg("tag version ignored, parsing error")
			continue
		}

		if constraint.Check(v) {
			// Add tag if it matches the version constraint
			result = append(result, tagV)
			lg.Debug().Str("tag", tagV).Msg("version added")
		} else {
			lg.Debug().Str("tag", tagV).Msg("tag version ignored, does not satisfy constraint")
		}
	}

	return result, nil
}
