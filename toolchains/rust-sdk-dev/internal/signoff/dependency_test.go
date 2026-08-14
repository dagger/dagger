package signoff

import (
	"strings"
	"testing"
)

func TestInstalledRustDependencyMustBeExactGitAtAFullRevision(t *testing.T) {
	t.Parallel()
	revision := strings.Repeat("a", 40)
	manifest := `[package]
name = "proof"

[dependencies]
dagger-sdk = { git = "https://github.com/iw/dagger", rev = "` + revision + `" }
`
	observation, err := ObserveInstalledRustDependency(manifest)
	if err != nil {
		t.Fatalf("observe exact Git dependency: %v", err)
	}
	if observation.Descriptor.Revision != revision || observation.Descriptor.Package != "dagger-sdk" ||
		!canonicalSHA256(observation.DescriptorDigest) {
		t.Fatalf("unexpected observation: %#v", observation)
	}

	for name, declaration := range map[string]string{
		"registry": `"=1.0.0-beta.10"`,
		"path":     `{ path = "../../sdk" }`,
		"branch":   `{ git = "https://github.com/iw/dagger", branch = "main" }`,
		"tag":      `{ git = "https://github.com/iw/dagger", tag = "v1" }`,
		"extra":    `{ git = "https://github.com/iw/dagger", rev = "` + revision + `", version = "1" }`,
	} {
		t.Run(name, func(t *testing.T) {
			mutated := "[dependencies]\ndagger-sdk = " + declaration + "\n"
			if _, err := ObserveInstalledRustDependency(mutated); err == nil {
				t.Fatalf("%s dependency substitution was admitted", name)
			}
		})
	}
}
