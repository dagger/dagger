package sdkmeta

import "testing"

func TestLookup(t *testing.T) {
	for _, input := range []string{
		"go", "golang", "go-sdk", "github.com/dagger/go-sdk",
		"github.com/dagger/go-sdk@v1.2.3", "github.com/dagger/go-sdk@feature/sdk",
		"https://github.com/dagger/go-sdk.git@v1.2.3",
		"ssh://git@github.com/dagger/go-sdk.git#v1.2.3",
		"git@github.com:dagger/go-sdk.git@v1.2.3",
	} {
		t.Run(input, func(t *testing.T) {
			entry, ok, err := Lookup(input)
			if err != nil || !ok || entry.Name != Go || entry.Repo != "github.com/dagger/go-sdk" {
				t.Fatalf("Lookup(%q) = %+v, %v, %v", input, entry, ok, err)
			}
		})
	}
	for _, input := range []string{
		"unknown", "./go", "./go-sdk", "github.com/acme/go-sdk",
		"github.com/dagger/go-sdk-extra", "github.com/dagger/go-sdk/subdir",
		"https://github.com/dagger/go-sdk.git#v1:subdir",
	} {
		t.Run(input, func(t *testing.T) {
			if entry, ok, err := Lookup(input); err != nil || ok {
				t.Fatalf("Lookup(%q) = %+v, %v, %v", input, entry, ok, err)
			}
		})
	}
}
