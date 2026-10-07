package naming

import "testing"

func TestDictionaryFor(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    *Dictionary
	}{
		{"", Latest},
		{"v1.0.0", Initial},
		{"1.0.0", Initial},
		{"v1.0.0-beta.15", Initial},
		{"v1.0.0-dev-123+abc", Initial},
		{"v1.2.3", Initial},
		{"v0.19.4", Initial},
		{"not-a-version", Initial},
	} {
		if got := DictionaryFor(tc.version); got != tc.want {
			t.Errorf("DictionaryFor(%q) = %p, want %p", tc.version, got, tc.want)
		}
	}
}
