package main

import "testing"

func TestShouldMoveNpmLatest(t *testing.T) {
	for _, tc := range []struct {
		published, current string
		want               bool
		why                string
	}{
		{"1.0.0-beta.28", "1.0.0-beta.25.2", true, "a newer beta takes latest from an older one"},
		{"1.0.0-beta.9", "1.0.0-beta.25.2", false, "and an older one does not, where a string compare would"},
		{"1.0.0-beta.28", "", true, "no latest yet"},
		{"1.0.0-beta.29", "1.0.0", false, "a prerelease of the released version stays below it"},
		{"1.0.1-beta.1", "1.0.0", false, "and so does the next patch line's first beta"},
		{"1.1.0-beta.1", "1.0.0", false, "and the next minor's"},
		{"2.0.0-beta.1", "1.0.0", false, "and the next major's"},
		{"1.0.0", "1.0.0-beta.29", true, "the first stable promotes itself"},
		{"1.0.1", "1.0.0", true, "and later stables still move it"},
		{"1.0.0-beta.28", "1.0.0-beta.28", false, "the same version is not above itself"},
		{"not-a-version", "1.0.0", false, "an unparseable version moves nothing"},
	} {
		if got := shouldMoveNpmLatest(tc.published, tc.current); got != tc.want {
			t.Errorf("shouldMoveNpmLatest(%q, %q) = %v, want %v — %s",
				tc.published, tc.current, got, tc.want, tc.why)
		}
	}
}
