package main

import "testing"

func TestResolveLockMode(t *testing.T) {
	t.Run("both empty", func(t *testing.T) {
		mode, err := resolveLockMode("", "")
		if err != nil {
			t.Fatalf("resolveLockMode returned unexpected error: %v", err)
		}
		if mode != "" {
			t.Fatalf("expected empty mode, got %q", mode)
		}
	})

	t.Run("uses global when param empty", func(t *testing.T) {
		mode, err := resolveLockMode("", "strict")
		if err != nil {
			t.Fatalf("resolveLockMode returned unexpected error: %v", err)
		}
		if mode != "strict" {
			t.Fatalf("expected strict, got %q", mode)
		}
	})

	t.Run("param takes precedence over global", func(t *testing.T) {
		mode, err := resolveLockMode("update", "strict")
		if err != nil {
			t.Fatalf("resolveLockMode returned unexpected error: %v", err)
		}
		if mode != "update" {
			t.Fatalf("expected update, got %q", mode)
		}
	})

	t.Run("invalid global mode", func(t *testing.T) {
		_, err := resolveLockMode("", "weird")
		if err == nil {
			t.Fatalf("expected error for invalid global lock mode")
		}
	})

	t.Run("invalid param mode", func(t *testing.T) {
		_, err := resolveLockMode("weird", "auto")
		if err == nil {
			t.Fatalf("expected error for invalid param lock mode")
		}
	})
}
