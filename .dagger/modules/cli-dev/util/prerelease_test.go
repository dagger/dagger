package util

import (
	"reflect"
	"testing"
)

func TestPrereleaseVariants(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		expected []string
	}{
		{
			name:     "empty prerelease",
			version:  "v1.0.0",
			expected: nil,
		},
		{
			name:     "single segment prerelease",
			version:  "v2.0.0-beta",
			expected: nil,
		},
		{
			name:     "two segment prerelease",
			version:  "v1.0.0-alpha.1",
			expected: []string{"v1.0.0-alpha"},
		},
		{
			name:     "multiple segment prelease",
			version:  "v0.17.0-foo.1.2.3",
			expected: []string{"v0.17.0-foo", "v0.17.0-foo.1", "v0.17.0-foo.1.2"},
		},
		{
			name:     "prerelease with build metadata",
			version:  "v1.2.3-alpha.1.2+build.123",
			expected: []string{"v1.2.3-alpha+build.123", "v1.2.3-alpha.1+build.123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrereleaseVariants(tt.version)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("PrereleaseVariants(%q) = %v, want %v", tt.version, got, tt.expected)
			}
		})
	}
}

// Test for specific known edge cases
func TestPrereleaseVariantsEdgeCases(t *testing.T) {
	// Test with invalid semver
	result := PrereleaseVariants("not-semver")
	if len(result) > 0 {
		t.Errorf("Expected empty result for invalid semver, got %v", result)
	}

	// Test with very long prerelease string
	longVersion := "v1.0.0-alpha.1.2.3.4.5.6.7.8.9.10"
	result = PrereleaseVariants(longVersion)
	if len(result) != 10 {
		t.Errorf("Expected 10 variants for long prerelease, got %d", len(result))
	}
}
