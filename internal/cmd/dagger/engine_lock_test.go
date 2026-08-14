package daggercmd

import "testing"

func TestResolveLockMode(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		mode, err := resolveLockMode("")
		if err != nil {
			t.Fatalf("resolveLockMode returned unexpected error: %v", err)
		}
		if mode != "" {
			t.Fatalf("expected empty mode, got %q", mode)
		}
	})

	t.Run("explicit frozen", func(t *testing.T) {
		mode, err := resolveLockMode("frozen")
		if err != nil {
			t.Fatalf("resolveLockMode returned unexpected error: %v", err)
		}
		if mode != "frozen" {
			t.Fatalf("expected frozen, got %q", mode)
		}
	})

	t.Run("explicit live", func(t *testing.T) {
		mode, err := resolveLockMode("live")
		if err != nil {
			t.Fatalf("resolveLockMode returned unexpected error: %v", err)
		}
		if mode != "live" {
			t.Fatalf("expected live, got %q", mode)
		}
	})

	t.Run("explicit disabled is preserved", func(t *testing.T) {
		mode, err := resolveLockMode("disabled")
		if err != nil {
			t.Fatalf("resolveLockMode returned unexpected error: %v", err)
		}
		if mode != "disabled" {
			t.Fatalf("expected disabled, got %q", mode)
		}
	})

	t.Run("invalid mode", func(t *testing.T) {
		_, err := resolveLockMode("weird")
		if err == nil {
			t.Fatalf("expected error for invalid lock mode")
		}
	})
}
