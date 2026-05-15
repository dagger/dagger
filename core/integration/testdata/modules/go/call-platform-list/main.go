package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromPlatforms(
	// +default=["linux/arm64", "linux/amd64"]
	platforms []dagger.Platform,
) []string {
	r := make([]string, 0, len(platforms))
	for _, p := range platforms {
		r = append(r, string(p))
	}
	return r
}

func (m *Test) ToPlatforms(platforms []string) []dagger.Platform {
	r := make([]dagger.Platform, 0, len(platforms))
	for _, p := range platforms {
		r = append(r, dagger.Platform(p))
	}
	return r
}
