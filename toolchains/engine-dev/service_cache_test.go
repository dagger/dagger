package main

import (
	"strings"
	"testing"
)

func TestEngineStateCacheKey(t *testing.T) {
	for _, name := range []string{"", "test"} {
		t.Run("isolated/"+name, func(t *testing.T) {
			first := engineStateCacheKey(name, false)
			second := engineStateCacheKey(name, false)
			if first == second {
				t.Fatal("independent engines must not share a state directory")
			}
			if !strings.HasPrefix(first, "dagger-dev-engine-state-") {
				t.Fatalf("unexpected cache key: %q", first)
			}
			if name != "" && !strings.HasSuffix(first, "-"+name) {
				t.Fatalf("cache key %q should retain service name %q", first, name)
			}
		})
	}

	for _, name := range []string{"", "test"} {
		if got := engineStateCacheKey(name, true); got != "dagger-dev-engine-state" {
			t.Fatalf("explicitly shared cache must keep its stable key, got %q", got)
		}
	}
}
