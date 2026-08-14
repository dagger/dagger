package signoff

import (
	"fmt"
	"strings"
)

// ParseStandaloneResolvedImages retains exactly one runtime identity for every image reference
// admitted by the Rust-owned standalone fixture. Other process output is intentionally ignored.
func ParseStandaloneResolvedImages(stdout string, expected map[string]struct{}) (map[string]string, error) {
	const prefix = "Sign-off image resolved: "
	resolved := make(map[string]string, len(expected))
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		reference, digestHex, present := strings.Cut(strings.TrimPrefix(line, prefix), " sha256:")
		digest := "sha256:" + digestHex
		if !present || !validSHA256(digest) {
			return nil, fmt.Errorf("resolved image evidence is malformed")
		}
		if _, allowed := expected[reference]; !allowed {
			return nil, fmt.Errorf("resolved image evidence names undeclared reference %q", reference)
		}
		if _, duplicate := resolved[reference]; duplicate {
			return nil, fmt.Errorf("resolved image evidence duplicates reference %q", reference)
		}
		resolved[reference] = digest
	}
	if len(resolved) != len(expected) {
		return nil, fmt.Errorf("resolved image evidence is incomplete")
	}
	return resolved, nil
}
