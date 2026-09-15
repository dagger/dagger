package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromPlatform(
	// +default="linux/arm64"
	platform dagger.Platform,
) string {
	return string(platform)
}

func (m *Test) ToPlatform(platform string) dagger.Platform {
	return dagger.Platform(platform)
}
