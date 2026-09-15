package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) FromPlatform(platform dagger.Platform) string {
	return string(platform)
}

func (m *Test) ToPlatform(platform string) dagger.Platform {
	return dagger.Platform(platform)
}

func (m *Test) FromPlatforms(platform []dagger.Platform) []string {
	result := []string{}
	for _, p := range platform {
		result = append(result, string(p))
	}
	return result
}

func (m *Test) ToPlatforms(platform []string) []dagger.Platform {
	result := []dagger.Platform{}
	for _, p := range platform {
		result = append(result, dagger.Platform(p))
	}
	return result
}
