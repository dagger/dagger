package idtui

import (
	"errors"
	"fmt"
	"testing"
)

type quietTestError struct{}

func (quietTestError) Error() string {
	return "wrapped quiet error"
}

func (quietTestError) Extensions() map[string]any {
	return map[string]any{
		"_quiet":    true,
		"_message":  "clean message",
		"_exitCode": 1,
	}
}

func TestQuietErrorMessage(t *testing.T) {
	t.Parallel()

	t.Run("extracts quiet message from wrapped error", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", quietTestError{})
		msg, exitCode, ok := quietErrorMessage(err)
		if !ok {
			t.Fatal("quietErrorMessage() did not detect quiet error")
		}
		if msg != "clean message" {
			t.Fatalf("quietErrorMessage() message = %q, want %q", msg, "clean message")
		}
		if exitCode != 1 {
			t.Fatalf("quietErrorMessage() exit code = %d, want 1", exitCode)
		}
	})

	t.Run("ignores ordinary errors", func(t *testing.T) {
		if _, _, ok := quietErrorMessage(errors.New("boom")); ok {
			t.Fatal("quietErrorMessage() detected a quiet error where there was none")
		}
	})
}
