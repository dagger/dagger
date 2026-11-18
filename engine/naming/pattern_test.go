package naming

import "testing"

func TestMapNamePattern(t *testing.T) {
	kebab := func(name string) string {
		s, err := Convert(name, Kebab, Uppercase)
		if err != nil {
			return name
		}
		return s
	}
	for pattern, want := range map[string]string{
		"items":           "items",
		"fooBar":          "foo-bar",
		"item*":           "item*",
		"foo*":            "foo*",
		"fooBar*":         "foo-bar*",
		"*":               "*",
		"**":              "**",
		"e2eTest?":        "e2e-test?",
		"*:e2eTest":       "*:e2e-test",
		"{fooBar,bazQux}": "{foo-bar,baz-qux}",
		"test-*":          "test-*",
		"*-lint":          "*-lint",
		"*_e2eTest":       "*-e2e-test",
		"go:*-*":          "go:*-*",
		"ci/fooBar/**":    "ci/foo-bar/**",
		"test[0-9]":       "test[0-9]",
		"test[!0-4]":      "test[!0-4]",
		`test[\]0-9]`:     `test[\]0-9]`,
		`foo\*Bar`:        `foo\*bar`,
	} {
		if got := MapNamePattern(pattern, kebab); got != want {
			t.Errorf("MapNamePattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}
