package signoff

import "testing"

func TestParseStandaloneResolvedImagesRequiresTheExactClosedSet(t *testing.T) {
	t.Parallel()
	expected := map[string]struct{}{"rust:1.97.1": {}, "nginx:1.24.0-alpine3.17": {}}
	stdout := "unrelated output\n" +
		"Sign-off image resolved: rust:1.97.1 sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"Sign-off image resolved: nginx:1.24.0-alpine3.17 sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
	resolved, err := ParseStandaloneResolvedImages(stdout, expected)
	if err != nil {
		t.Fatalf("parse exact resolved image set: %v", err)
	}
	if len(resolved) != 2 || resolved["rust:1.97.1"] != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected resolved image evidence: %#v", resolved)
	}
	for name, mutated := range map[string]string{
		"missing":   "Sign-off image resolved: rust:1.97.1 sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
		"extra":     stdout + "Sign-off image resolved: alpine:latest sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\n",
		"duplicate": stdout + "Sign-off image resolved: rust:1.97.1 sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n",
		"malformed": "Sign-off image resolved: rust:1.97.1 sha256:not-a-digest\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStandaloneResolvedImages(mutated, expected); err == nil {
				t.Fatalf("accepted %s resolved image evidence", name)
			}
		})
	}
}
